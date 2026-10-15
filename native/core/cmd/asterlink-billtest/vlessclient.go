package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"asterlink/core/internal/managed"

	"github.com/metacubex/mihomo/adapter/outbound"
	C "github.com/metacubex/mihomo/constant"
)

// An independent client of the managed node (round 7, cases B04-B07). TCP speaks VLESS v0 itself;
// UDP uses the pinned Mihomo adapter's default XUDP, with only the credential a grant carried.
// Neither path runs the AsterLink Manager, TUN or usage reporter. This models a user who
// extracts the per-grant credential and runs their own client, with no AsterLink Core, no TUN and no usage
// report. If the node still counts, limits and cuts this client, the control is real.

// nodeTarget is the connection material of one grant. It lives in this process only and is never printed.
type nodeTarget struct {
	server   string
	port     int
	uuid     [16]byte
	useTLS   bool
	serverTL string // SNI, defaults to the server name
	insecure bool   // the profile asked to skip certificate verification (refused for a public node)
}

func targetFromProfile(p managed.Profile) (nodeTarget, error) {
	var t nodeTarget
	proxy := p.Proxy
	if kind, _ := proxy["type"].(string); kind != "vless" {
		return t, fmt.Errorf("the granted node is %q, the independent client implements vless only", kind)
	}
	t.server, _ = proxy["server"].(string)
	switch port := proxy["port"].(type) {
	case int:
		t.port = port
	case int64:
		t.port = int(port)
	case uint64:
		t.port = int(port)
	case float64:
		t.port = int(port)
	}
	id, _ := proxy["uuid"].(string)
	raw, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	if err != nil || len(raw) != 16 || t.server == "" || t.port <= 0 || t.port > 65535 {
		return t, errors.New("the granted node document is incomplete")
	}
	copy(t.uuid[:], raw)
	t.useTLS, _ = proxy["tls"].(bool)
	t.serverTL, _ = proxy["servername"].(string)
	if t.serverTL == "" {
		t.serverTL = t.server
	}
	t.insecure, _ = proxy["skip-cert-verify"].(bool)
	return t, nil
}

// destination is host:port of the controlled sink.
type destination struct {
	host string
	port int
}

func destinationOf(raw string) (destination, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return destination{}, errors.New("bad sink URL")
	}
	port := 80
	if p := u.Port(); p != "" {
		port, err = strconv.Atoi(p)
		if err != nil {
			return destination{}, errors.New("bad sink port")
		}
	} else if u.Scheme == "https" {
		port = 443
	}
	return destination{u.Hostname(), port}, nil
}

func (d destination) header(t nodeTarget, command byte) []byte {
	out := append([]byte{0}, t.uuid[:]...)
	out = append(out, 0, command)
	out = binary.BigEndian.AppendUint16(out, uint16(d.port))
	if ip := net.ParseIP(d.host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return append(append(out, 1), v4...)
		}
		return append(append(out, 3), ip.To16()...)
	}
	out = append(out, 2, byte(len(d.host)))
	return append(out, d.host...)
}

func (t nodeTarget) dial(ctx context.Context) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	address := net.JoinHostPort(t.server, strconv.Itoa(t.port))
	if !t.useTLS {
		return dialer.DialContext(ctx, "tcp", address)
	}
	conn, err := (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: t.serverTL, InsecureSkipVerify: t.insecure, MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", address)
	return conn, err
}

// tcpFlow is one established VLESS TCP stream to the sink, used as a long-lived HTTP/1.1 keep-alive connection.
type tcpFlow struct {
	conn    net.Conn
	reader  *bufio.Reader
	host    string
	token   string
	greeted bool
}

// openTCP performs the VLESS handshake and the first request. A node that refuses the user closes without a
// reply, which surfaces here as an error on the first read.
func openTCP(ctx context.Context, t nodeTarget, to destination, sinkToken string) (*tcpFlow, error) {
	conn, err := t.dial(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = conn.Write(to.header(t, 1)); err != nil {
		conn.Close()
		return nil, err
	}
	f := &tcpFlow{conn: conn, reader: bufio.NewReaderSize(conn, 64<<10), host: net.JoinHostPort(to.host, strconv.Itoa(to.port)), token: sinkToken}
	if _, err = f.get(2, 5*time.Second); err != nil {
		conn.Close()
		return nil, err
	}
	return f, nil
}

// get downloads n body bytes through the flow and returns the bytes the sink delivered.
func (f *tcpFlow) get(n int64, within time.Duration) (int64, error) {
	_ = f.conn.SetDeadline(time.Now().Add(within))
	defer f.conn.SetDeadline(time.Time{})
	request := fmt.Sprintf("GET /down?bytes=%d&t=%s HTTP/1.1\r\nHost: %s\r\nConnection: keep-alive\r\n\r\n", n, f.token, f.host)
	if _, err := io.WriteString(f.conn, request); err != nil {
		return 0, err
	}
	if !f.greeted {
		head := make([]byte, 2)
		if _, err := io.ReadFull(f.reader, head); err != nil {
			return 0, err
		}
		if head[0] != 0 {
			return 0, errors.New("bad vless response")
		}
		if head[1] > 0 {
			if _, err := io.CopyN(io.Discard, f.reader, int64(head[1])); err != nil {
				return 0, err
			}
		}
		f.greeted = true
	}
	response, err := http.ReadResponse(f.reader, nil)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("sink answered %d", response.StatusCode)
	}
	got, err := io.Copy(io.Discard, response.Body)
	if err == nil && got != n {
		err = fmt.Errorf("short body %d of %d", got, n)
	}
	return got, err
}

// put uploads n body bytes through the flow (the sink swallows and counts them).
func (f *tcpFlow) put(n int64, within time.Duration) error {
	_ = f.conn.SetDeadline(time.Now().Add(within))
	defer f.conn.SetDeadline(time.Time{})
	head := fmt.Sprintf("POST /up?t=%s HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\nConnection: keep-alive\r\n\r\n", f.token, f.host, n)
	if _, err := io.WriteString(f.conn, head); err != nil {
		return err
	}
	block := make([]byte, 32<<10)
	for sent := int64(0); sent < n; {
		part := min(int64(len(block)), n-sent)
		if _, err := f.conn.Write(block[:part]); err != nil {
			return err
		}
		sent += part
	}
	response, err := http.ReadResponse(f.reader, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("sink answered %d", response.StatusCode)
	}
	return nil
}

func (f *tcpFlow) close() { _ = f.conn.Close() }

// udpFlow uses the real pinned Mihomo client, NOT plain command-2 VLESS.
// --udp-sink must exercise the same wire encoding as all native clients; it
// must not silently fall back and certify a node that cannot serve their UDP.
type udpFlow struct {
	conn    net.PacketConn
	adapter *outbound.Vless
	target  net.Addr
	prefix  []byte
}

func openUDP(ctx context.Context, t nodeTarget, to destination, sinkToken string) (*udpFlow, error) {
	id := fmt.Sprintf("%x-%x-%x-%x-%x", t.uuid[:4], t.uuid[4:6], t.uuid[6:8], t.uuid[8:10], t.uuid[10:])
	a, err := outbound.NewVless(outbound.VlessOption{Name: "grant-udp-probe", Server: t.server, Port: t.port, UUID: id, UDP: true, TLS: t.useTLS, ServerName: t.serverTL, SkipCertVerify: t.insecure})
	if err != nil {
		return nil, err
	}
	m := &C.Metadata{NetWork: C.UDP, Host: to.host, DstPort: uint16(to.port)}
	pc, err := a.ListenPacketContext(ctx, m)
	if err != nil {
		a.Close()
		return nil, err
	}
	f := &udpFlow{conn: pc, adapter: a, target: m.UDPAddr(), prefix: []byte("t=" + sinkToken + ";")}
	if err = f.ping(5 * time.Second); err != nil {
		f.close()
		return nil, err
	}
	return f, nil
}

// ping sends one token-prefixed datagram and waits for the XUDP echo.
func (f *udpFlow) ping(within time.Duration) error {
	_ = f.conn.SetDeadline(time.Now().Add(within))
	defer f.conn.SetDeadline(time.Time{})
	payload := append(append([]byte(nil), f.prefix...), strconv.FormatInt(time.Now().UnixNano(), 10)...)
	if _, err := f.conn.WriteTo(payload, f.target); err != nil {
		return err
	}
	back := make([]byte, 65535)
	n, _, err := f.conn.ReadFrom(back)
	if err != nil {
		return err
	}
	if string(back[:n]) != string(payload) {
		return errors.New("echo mismatch")
	}
	return nil
}

func (f *udpFlow) close() { _ = f.conn.Close(); _ = f.adapter.Close() }

// probe holds a TCP and a UDP flow open and records when each one first fails. The sampling interval bounds the
// precision of every latency the report states.
type probe struct {
	tcp *tcpFlow
	udp *udpFlow

	mu       sync.Mutex
	tcpDown  time.Time
	udpDown  time.Time
	movedTCP int64
	stop     context.CancelFunc
	done     chan struct{}
}

const probeInterval = 250 * time.Millisecond

// startProbe opens both flows (UDP only when the sink has an echo port) and begins sampling. chunk > 0 makes the
// TCP flow download that many bytes per sample, which is how a byte budget or a quota is driven to exhaustion.
func startProbe(ctx context.Context, t nodeTarget, to destination, udpTo *destination, token string, chunk int64) (*probe, error) {
	p := &probe{done: make(chan struct{})}
	var err error
	if p.tcp, err = openTCP(ctx, t, to, token); err != nil {
		return nil, fmt.Errorf("tcp flow: %w", err)
	}
	if udpTo != nil {
		if p.udp, err = openUDP(ctx, t, *udpTo, token); err != nil {
			p.tcp.close()
			return nil, fmt.Errorf("udp flow: %w", err)
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	p.stop = cancel
	go p.loop(runCtx, chunk)
	return p, nil
}

func (p *probe) loop(ctx context.Context, chunk int64) {
	defer close(p.done)
	tick := time.NewTicker(probeInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if p.tcpAlive() {
			size := int64(1)
			if chunk > 0 {
				size = chunk
			}
			moved, err := p.tcp.get(size, 15*time.Second)
			p.mu.Lock()
			p.movedTCP += moved
			if err != nil && p.tcpDown.IsZero() {
				p.tcpDown = time.Now()
			}
			p.mu.Unlock()
		}
		if p.udp != nil && p.udpAlive() {
			if err := p.udp.ping(2 * time.Second); err != nil {
				p.mu.Lock()
				if p.udpDown.IsZero() {
					p.udpDown = time.Now()
				}
				p.mu.Unlock()
			}
		}
	}
}

func (p *probe) tcpAlive() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.tcpDown.IsZero() }
func (p *probe) udpAlive() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.udpDown.IsZero() }

// allDown reports when every flow has failed (or false if one still works).
func (p *probe) allDown() (time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tcpDown.IsZero() || p.udp != nil && p.udpDown.IsZero() {
		return time.Time{}, false
	}
	last := p.tcpDown
	if p.udpDown.After(last) {
		last = p.udpDown
	}
	return last, true
}

func (p *probe) moved() int64 { p.mu.Lock(); defer p.mu.Unlock(); return p.movedTCP }

func (p *probe) close() {
	p.stop()
	<-p.done
	p.tcp.close()
	if p.udp != nil {
		p.udp.close()
	}
}

// refused reports whether the node turns this credential away: it answers nothing and closes.
func refused(ctx context.Context, t nodeTarget, to destination, token string) bool {
	f, err := openTCP(ctx, t, to, token)
	if err != nil {
		return true
	}
	f.close()
	return false
}
