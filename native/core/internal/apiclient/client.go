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
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
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

func (c *Client) do(ctx context.Context, timeout time.Duration, method, path string, query url.Values, token string, in, out any) error {
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

func (c *Client) Logout(ctx context.Context, token string) error {
	return c.do(ctx, RequestTimeout, http.MethodPost, "/logout", nil, token, struct{}{}, nil)
}
