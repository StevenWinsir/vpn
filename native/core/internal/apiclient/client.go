package apiclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"asterlink/core/internal/network"
)

const (
	apiPath         = "/api/v1/client"
	maxResponseSize = 2 << 20
	// RequestTimeout bounds every ordinary call; the catalog long-poll uses its own.
	RequestTimeout = 20 * time.Second
	// WatchTimeout must exceed the server's configurable watch window (max 30s).
	WatchTimeout = 40 * time.Second
)

// Error is a failed API exchange. Code is the stable identifier; Transient marks
// failures worth retrying with backoff (network, 429, 5xx) as opposed to refusals.
type Error struct {
	Status    int
	Code      string
	Transient bool
}

func (e *Error) Error() string { return fmt.Sprintf("api error %d %s", e.Status, e.Code) }

// ParseBase validates the API base exactly like the App's build-time rules
// (shared/contracts/env-v1.json): HTTPS only, except http on a loopback host in a
// development channel. Credentials are never sent to anything else.
func ParseBase(raw, channel string) (*url.URL, error) {
	invalid := &Error{Code: "invalid_api_base"}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != apiPath {
		return nil, invalid
	}
	host := strings.ToLower(u.Hostname())
	loopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && channel == "development" && loopback:
	default:
		return nil, invalid
	}
	return u, nil
}

type Client struct {
	base *url.URL
	http *http.Client
}

// New builds a client with its own transport: no system proxy, no redirects, TLS 1.2+.
func New(base *url.URL) *Client {
	transport := &http.Transport{
		Proxy: nil,
		// While the tunnel is up this pins sockets to the physical interface; otherwise
		// it is a plain dial. Either way the Core's API calls never ride the paid node.
		DialContext:           network.Default.DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: WatchTimeout,
		MaxIdleConns:          2,
		IdleConnTimeout:       30 * time.Second,
		DisableCompression:    false,
	}
	return &Client{base: base, http: &http.Client{
		Transport: transport,
		// A redirect would carry the bearer token to another URL; treat it as failure.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (c *Client) CloseIdle() { c.http.CloseIdleConnections() }

// WrapTransport interposes on this client's HTTP round trips. Only in-process acceptance
// harnesses (cmd/asterlink-billtest, which drops one /traffic acknowledgement on a real
// server) call it; the shipped Core never does.
func (c *Client) WrapTransport(wrap func(http.RoundTripper) http.RoundTripper) {
	c.http.Transport = wrap(c.http.Transport)
}

func (c *Client) do(ctx context.Context, timeout time.Duration, method, path string, query url.Values, token string, in, out any) error {
	return c.doWith(ctx, timeout, method, path, query, token, nil, in, out)
}

func (c *Client) doWith(ctx context.Context, timeout time.Duration, method, path string, query url.Values, token string, headers map[string]string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	target := *c.base
	target.Path = c.base.Path + path
	target.RawQuery = query.Encode()
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return &Error{Code: "invalid_request"}
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return &Error{Code: "invalid_request"}
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Cache-Control", "no-store")
	if in != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return classifyTransport(ctx, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil {
		return classifyTransport(ctx, err)
	}
	if len(data) > maxResponseSize {
		return &Error{Status: response.StatusCode, Code: "bad_response"}
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return &Error{Status: response.StatusCode, Code: "bad_response"}
		}
		return nil
	}
	return failure(response.StatusCode, data)
}

func failure(status int, data []byte) *Error {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	// Only the code is read. The message and any other field are discarded so a
	// misbehaving server cannot push arbitrary text or config into logs or the UI.
	code := ""
	if json.Unmarshal(data, &envelope) == nil {
		code = envelope.Error.Code
	}
	transient := status == http.StatusTooManyRequests || status >= 500
	switch {
	case status == http.StatusTooManyRequests:
		code = "rate_limited"
	case code == "" && status >= 500:
		code = "client_service_unavailable"
	case code == "":
		code = "bad_response"
	}
	if len(code) > 64 || strings.ContainsFunc(code, func(r rune) bool { return r < 0x21 || r > 0x7e }) {
		code = "bad_response"
	}
	return &Error{Status: status, Code: code, Transient: transient}
}

func classifyTransport(ctx context.Context, err error) *Error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return &Error{Code: "canceled"}
	}
	var unknownAuthority x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	var record tls.RecordHeaderError
	if errors.As(err, &unknownAuthority) || errors.As(err, &invalid) || errors.As(err, &hostname) || errors.As(err, &record) {
		return &Error{Code: "tls_failure", Transient: true}
	}
	return &Error{Code: "network_unreachable", Transient: true}
}

func (c *Client) Login(ctx context.Context, in LoginRequest) (LoginResponse, error) {
	var out LoginResponse
	err := c.do(ctx, RequestTimeout, http.MethodPost, "/login", nil, "", in, &out)
	if err == nil && (len(out.Token) != 43 || out.Session.SessionID == "") {
		return LoginResponse{}, &Error{Code: "bad_response"}
	}
	return out, err
}

func (c *Client) Session(ctx context.Context, token string) (Session, error) {
	var out Session
	err := c.do(ctx, RequestTimeout, http.MethodGet, "/session", nil, token, nil, &out)
	return out, err
}

// Nodes performs the catalog long-poll. An empty revision returns immediately.
func (c *Client) Nodes(ctx context.Context, token, revision string) (Catalog, error) {
	query := url.Values{}
	if revision != "" {
		query.Set("revision", revision)
	}
	var out Catalog
	err := c.do(ctx, WatchTimeout, http.MethodGet, "/nodes", query, token, nil, &out)
	if err == nil && out.Revision == "" {
		return Catalog{}, &Error{Code: "bad_response"}
	}
	if out.Nodes == nil {
		out.Nodes = []NodeChoice{}
	}
	return out, err
}

// Config fetches (and thereby binds) the selected node. lastSequence is the already
// confirmed traffic sequence, required by the server for a bound session.
func (c *Client) Config(ctx context.Context, token, nodeID string, lastSequence int64) (ConfigResponse, error) {
	query := url.Values{}
	query.Set("node_id", nodeID)
	query.Set("last_sequence", strconv.FormatInt(lastSequence, 10))
	var out ConfigResponse
	err := c.do(ctx, RequestTimeout, http.MethodGet, "/config", query, token, nil, &out)
	return out, err
}

// Counters is one cumulative traffic report (handoff_app.md 5.1, 7.1).
type Counters struct {
	Sequence      int64 `json:"sequence"`
	UploadBytes   int64 `json:"upload_bytes"`
	DownloadBytes int64 `json:"download_bytes"`
}

// Traffic posts one cumulative report. The server acknowledges a repeated identical
// batch with replayed=true and never charges it twice; a different body under an
// already-used sequence is refused as traffic_conflict.
func (c *Client) Traffic(ctx context.Context, token string, in Counters) (Session, bool, error) {
	var out struct {
		Session  Session `json:"session"`
		Replayed bool    `json:"replayed"`
	}
	err := c.do(ctx, RequestTimeout, http.MethodPost, "/traffic", nil, token, in, &out)
	if err == nil && out.Session.SessionID == "" {
		return Session{}, false, &Error{Code: "bad_response"}
	}
	return out.Session, out.Replayed, err
}

func (c *Client) Logout(ctx context.Context, token string) error {
	return c.do(ctx, RequestTimeout, http.MethodPost, "/logout", nil, token, struct{}{}, nil)
}

// Capabilities reads the public feature flags (no authentication).
func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var out Capabilities
	err := c.do(ctx, RequestTimeout, http.MethodGet, "/capabilities", nil, "", nil, &out)
	return out, err
}

// Connect asks for an online lease on a client-reported group and returns the one server's
// document (contract native-v3). The idempotency key makes a retry after a lost reply
// return the same lease instead of a second one.
func (c *Client) Connect(ctx context.Context, token, key string, in ConnectionRequest) (ConnectionResponse, error) {
	var out ConnectionResponse
	err := c.doWith(ctx, RequestTimeout, http.MethodPost, "/connections", nil, token, map[string]string{"Idempotency-Key": key}, in, &out)
	if err == nil && (out.Connection.ID == "" || out.Connection.GroupID != in.GroupID || out.Session.SessionID == "") {
		return ConnectionResponse{}, &Error{Code: "bad_response"}
	}
	return out, err
}

// ReleaseConnection ends this session's lease. It is idempotent on the server.
func (c *Client) ReleaseConnection(ctx context.Context, token, id, reason string) (ReleaseResponse, error) {
	var out ReleaseResponse
	err := c.do(ctx, RequestTimeout, http.MethodPost, "/connections/"+url.PathEscape(id)+"/release", nil, token, map[string]string{"reason": reason}, &out)
	if err == nil && (out.Connection.ID != id || out.Session.SessionID == "") {
		return ReleaseResponse{}, &Error{Code: "bad_response"}
	}
	return out, err
}

// CreateGrant asks for a per-user credential on a node-reported node. The idempotency
// key makes a retry after a lost reply return the same grant instead of a second one.
// avoid names a server this device just failed on ("" for none).
func (c *Client) CreateGrant(ctx context.Context, token, nodeID, avoid, key string) (GrantStatus, error) {
	var out GrantStatus
	body := map[string]string{"node_id": nodeID}
	if avoid != "" {
		body["avoid_node_id"] = avoid
	}
	err := c.doWith(ctx, RequestTimeout, http.MethodPost, "/grants", nil, token, map[string]string{"Idempotency-Key": key}, body, &out)
	if err == nil && out.GrantID == "" {
		return GrantStatus{}, &Error{Code: "bad_response"}
	}
	return out, err
}

// Grant polls one grant. A pending grant has no credential yet.
func (c *Client) Grant(ctx context.Context, token, id string) (GrantStatus, error) {
	var out GrantStatus
	err := c.do(ctx, RequestTimeout, http.MethodGet, "/grants/"+url.PathEscape(id), nil, token, nil, &out)
	if err == nil && out.GrantID != id {
		return GrantStatus{}, &Error{Code: "bad_response"}
	}
	return out, err
}

// RevokeGrant ends a grant. It is idempotent on the server. reason is one of the release
// reasons, or "" (the body stays {} as older Cores sent it).
func (c *Client) RevokeGrant(ctx context.Context, token, id, reason string) (GrantStatus, error) {
	var out GrantStatus
	var body any = struct{}{}
	if reason != "" {
		body = map[string]string{"reason": reason}
	}
	err := c.do(ctx, RequestTimeout, http.MethodPost, "/grants/"+url.PathEscape(id)+"/revoke", nil, token, body, &out)
	return out, err
}
