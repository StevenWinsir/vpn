// Package control is the agent's only link to the control plane: two calls over mTLS
// (handoff_app.md 7.4). It carries no policy; every decision stays in the API.
package control

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
	"strings"
	"time"
)

const maxResponse = 8 << 20

type SyncRequest struct {
	Epoch        int64    `json:"epoch"`
	AgentVersion string   `json:"agent_version"`
	Protocols    []string `json:"protocols"`
	Installed    []string `json:"installed"`
	Removed      []string `json:"removed"`
}

type Order struct {
	GrantID          string            `json:"grant_id"`
	Action           string            `json:"action"`
	Protocol         string            `json:"protocol"`
	Credential       map[string]string `json:"credential"`
	RatePermille     int64             `json:"rate_permille"`
	ExpiresAt        *time.Time        `json:"expires_at"`
	EpochBudgetBytes int64             `json:"epoch_budget_bytes"`
}

type SyncResponse struct {
	ServerTime   time.Time `json:"server_time"`
	NodeID       string    `json:"node_id"`
	LeaseSeconds int       `json:"lease_seconds"`
	Grants       []Order   `json:"grants"`
}

type UsageRequest struct {
	GrantID       string `json:"grant_id"`
	Epoch         int64  `json:"epoch"`
	Sequence      int64  `json:"sequence"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
	Final         bool   `json:"final"`
}

type UsageResponse struct {
	Replayed         bool   `json:"replayed"`
	State            string `json:"state"`
	Epoch            int64  `json:"epoch"`
	Sequence         int64  `json:"sequence"`
	EpochBudgetBytes int64  `json:"epoch_budget_bytes"`
}

// Error is a failed exchange. Transient failures (network, 429, 5xx) are retried by
// the caller; everything else is a decision of the control plane.
type Error struct {
	Status    int
	Code      string
	Transient bool
}

func (e *Error) Error() string { return fmt.Sprintf("control plane: %d %s", e.Status, e.Code) }

// API is what the agent needs; tests substitute it.
type API interface {
	Sync(context.Context, SyncRequest) (SyncResponse, error)
	Usage(context.Context, UsageRequest) (UsageResponse, error)
}

type Client struct {
	base *url.URL
	http *http.Client
}

// New builds an mTLS client. The server certificate is always verified: against roots
// when given, otherwise against the system store. There is no insecure option.
func New(rawURL string, cert tls.Certificate, roots *x509.CertPool) (*Client, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("control plane URL must be a plain https origin")
	}
	transport := &http.Transport{
		Proxy:               nil,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, RootCAs: roots},
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second, MaxIdleConns: 2, IdleConnTimeout: 60 * time.Second,
	}
	return &Client{base: u, http: &http.Client{Transport: transport, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) CloseIdle() { c.http.CloseIdleConnections() }

func (c *Client) post(ctx context.Context, path string, in, out any) error {
	data, err := json.Marshal(in)
	if err != nil {
		return &Error{Code: "invalid_request"}
	}
	target := *c.base
	target.Path = "/internal/v1/node-agents" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(data))
	if err != nil {
		return &Error{Code: "invalid_request"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return &Error{Code: "network_unreachable", Transient: true}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse {
		return &Error{Status: resp.StatusCode, Code: "bad_response", Transient: true}
	}
	if resp.StatusCode/100 == 2 {
		if json.Unmarshal(body, out) != nil {
			return &Error{Status: resp.StatusCode, Code: "bad_response", Transient: true}
		}
		return nil
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &envelope)
	code := envelope.Error.Code
	if code == "" || len(code) > 64 || strings.ContainsFunc(code, func(r rune) bool { return r < 0x21 || r > 0x7e }) {
		code = "bad_response"
	}
	return &Error{Status: resp.StatusCode, Code: code, Transient: resp.StatusCode == 429 || resp.StatusCode >= 500}
}

func (c *Client) Sync(ctx context.Context, in SyncRequest) (SyncResponse, error) {
	var out SyncResponse
	err := c.post(ctx, "/sync", in, &out)
	return out, err
}

func (c *Client) Usage(ctx context.Context, in UsageRequest) (UsageResponse, error) {
	var out UsageResponse
	err := c.post(ctx, "/usage", in, &out)
	return out, err
}
