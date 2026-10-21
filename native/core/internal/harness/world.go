//go:build asterlink_harness

// Package harness is the iOS billing acceptance of handoff_app_ios.md 8.8 (B01, B02, B04, A04,
// C01, and the online presence cases P01-P05 of node groups round 6), run against the real Core (managed.Manager, the meter, the lease, the gates), the real
// pinned Mihomo with the in-process injection engine instead of a TUN, and the real
// Gin/PostgreSQL backend started by scripts/test-native-ios.py --suite billing (the backend's
// billing world). It is compiled only under the `asterlink_harness` tag, which no shipped build
// uses. Its cases are reported with environment "harness", never "device".
package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config is what the suite hands the run: the backend world's facts (world.json), where the
// controlled sink listens (the test node reaches the same loopback address) and the sizes.
type Config struct {
	World struct {
		APIBase      string            `json:"api_base"`
		Password     string            `json:"password"`
		Emails       map[string]string `json:"emails"`
		Nodes        map[string]string `json:"nodes"`
		Names        map[string]string `json:"names"`
		NodeReported bool              `json:"node_reported"`
		AgentTarget  string            `json:"agent_target"`
		AgentResolve bool              `json:"agent_target_resolves"`
		ReportSecs   int               `json:"report_interval_seconds"`
		LeaseSecs    int               `json:"lease_seconds"`
	} `json:"world"`
	Sink struct {
		Host  string `json:"host"`
		Port  int    `json:"port"`
		Token string `json:"token"`
	} `json:"sink"`
	UploadMiB       int64 `json:"upload_mib"`
	DownloadMiB     int64 `json:"download_mib"`
	BackgroundBytes int64 `json:"background_bytes"`
	// CacheDir is where the engine keeps its scratch (the app's private directory on a device).
	CacheDir string `json:"cache_dir"`
}

// Case is one acceptance result. Status is PASS, FAIL or BLOCKED.
type Case struct {
	ID       string `json:"case_id"`
	Status   string `json:"status"`
	Observed string `json:"observed"`
}

// worldClient talks to the backend world's loopback control surface (/__world/*) and reads the
// sink's independent byte counts.
type worldClient struct {
	base *url.URL
	http *http.Client
}

func newWorldClient(apiBase string) (*worldClient, error) {
	u, err := url.Parse(apiBase)
	if err != nil {
		return nil, err
	}
	return &worldClient{base: u, http: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}}, nil
}

func (w *worldClient) post(ctx context.Context, action string, body any) ([]byte, error) {
	data, _ := json.Marshal(body)
	target := (&url.URL{Scheme: w.base.Scheme, Host: w.base.Host, Path: "/__world/" + action}).String()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := w.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNoContent {
		return nil, fmt.Errorf("world %s: %d %s", action, response.StatusCode, strings.TrimSpace(string(out)))
	}
	return out, nil
}

type ledger struct {
	UsedUnits      int64 `json:"used_units"`
	TotalBytes     int64 `json:"total_bytes"`
	RemainingBytes int64 `json:"remaining_bytes"`
	UploadBytes    int64 `json:"upload_bytes"`
	DownloadBytes  int64 `json:"download_bytes"`
}

func (w *worldClient) ledger(ctx context.Context, user string) (ledger, error) {
	out, err := w.post(ctx, "ledger", map[string]string{"user": user})
	var l ledger
	if err != nil {
		return l, err
	}
	return l, json.Unmarshal(out, &l)
}

func (w *worldClient) sink(ctx context.Context) (up, down int64, err error) {
	out, err := w.post(ctx, "sink", map[string]string{})
	if err != nil {
		return 0, 0, err
	}
	var s struct {
		Up   int64 `json:"up_received"`
		Down int64 `json:"down_sent"`
	}
	if err := json.Unmarshal(out, &s); err != nil {
		return 0, 0, err
	}
	return s.Up, s.Down, nil
}

type trafficReport struct {
	Token   string `json:"token_sha256_prefix"`
	Body    string `json:"body"`
	Status  int    `json:"status"`
	Dropped bool   `json:"dropped"`
}

func (w *worldClient) reports(ctx context.Context) ([]trafficReport, error) {
	out, err := w.post(ctx, "traffic", map[string]string{})
	if err != nil {
		return nil, err
	}
	var r struct {
		Reports []trafficReport `json:"reports"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	return r.Reports, nil
}

// dialer is how the controlled client reaches the sink: the engine's injection point.
type dialer func(ctx context.Context, network, address string) (net.Conn, error)

// transfer moves exactly the requested body bytes through the node to the sink: an HTTP/1.1
// request per direction, the sink's own counts being the independent truth.
type transfer struct {
	dial  dialer
	host  string
	port  int
	token string
}

func (t transfer) address() string { return net.JoinHostPort(t.host, fmt.Sprint(t.port)) }

func (t transfer) do(ctx context.Context, header string, bodyBytes int64, read func(io.Reader) (int64, error)) (int64, error) {
	conn, err := t.dial(ctx, "tcp", t.address())
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))
	}
	if _, err := io.WriteString(conn, header); err != nil {
		return 0, err
	}
	block := make([]byte, 64<<10)
	for i := range block {
		block[i] = byte(i % 251)
	}
	for left := bodyBytes; left > 0; {
		n := int64(len(block))
		if left < n {
			n = left
		}
		if _, err := conn.Write(block[:n]); err != nil {
			return 0, err
		}
		left -= n
	}
	var head bytes.Buffer
	one := make([]byte, 1)
	for !bytes.HasSuffix(head.Bytes(), []byte("\r\n\r\n")) {
		if _, err := conn.Read(one); err != nil {
			return 0, fmt.Errorf("connection closed in the headers: %q", head.String())
		}
		head.Write(one)
		if head.Len() > 8192 {
			return 0, fmt.Errorf("oversized response head")
		}
	}
	if !strings.HasPrefix(head.String(), "HTTP/1.1 200") {
		return 0, fmt.Errorf("%s", strings.SplitN(head.String(), "\r\n", 2)[0])
	}
	return read(conn)
}

// download reads exactly bytes of body; it returns the length actually read.
func (t transfer) download(ctx context.Context, bytesWanted int64) (int64, error) {
	header := fmt.Sprintf("GET /down?t=%s&bytes=%d HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", t.token, bytesWanted, t.address())
	return t.do(ctx, header, 0, func(r io.Reader) (int64, error) { return io.Copy(io.Discard, r) })
}

// upload sends exactly bytes of body; it returns what the sink says it received.
func (t transfer) upload(ctx context.Context, bytesSent int64) (int64, error) {
	header := fmt.Sprintf("POST /up?t=%s HTTP/1.1\r\nHost: %s\r\nContent-Type: application/octet-stream\r\nContent-Length: %d\r\nConnection: close\r\n\r\n",
		t.token, t.address(), bytesSent)
	return t.do(ctx, header, bytesSent, func(r io.Reader) (int64, error) {
		text, _ := io.ReadAll(io.LimitReader(r, 4096))
		var reply struct {
			Received int64 `json:"received"`
		}
		if json.Unmarshal(bytes.TrimSpace(text), &reply) != nil {
			return -1, nil
		}
		return reply.Received, nil
	})
}
