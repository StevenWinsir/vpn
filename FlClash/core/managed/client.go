package managed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

var APIBase = "https://demo.hyshentou.cn/api/v1/client"

const MaxCounter int64 = 1 << 50

type Status struct {
	ServerTime             time.Time  `json:"server_time"`
	SubscriptionExpiresAt  *time.Time `json:"subscription_expires_at"`
	AuthorizationExpiresAt *time.Time `json:"authorization_expires_at"`
	SessionIdleTimeout     int        `json:"session_idle_timeout_seconds"`
	ProfileVersion         string     `json:"profile_version"`
	SessionID              string     `json:"session_id"`
	ExpiresAt              time.Time  `json:"expires_at"`
	CanConnect             bool       `json:"can_connect"`
	Reason                 string     `json:"reason"`
	RemainingBytes         int64      `json:"remaining_bytes"`
	TotalBytes             int64      `json:"total_bytes"`
	UsedUnits              int64      `json:"used_units"`
	PlanName               string     `json:"plan_name"`
	LastSequence           int64      `json:"last_sequence"`
	UploadBytes            int64      `json:"upload_bytes"`
	DownloadBytes          int64      `json:"download_bytes"`
	ReportInterval         int        `json:"report_interval_seconds"`
	LeaseSeconds           int        `json:"lease_seconds"`
	MeteringSource         string     `json:"metering_source"`
	RatePermille           int64      `json:"rate_permille"`
}

type Counters struct {
	Sequence      int64 `json:"sequence"`
	UploadBytes   int64 `json:"upload_bytes"`
	DownloadBytes int64 `json:"download_bytes"`
}

type APIError struct{ Code string }

func (e *APIError) Error() string { return e.Code }

type Client struct {
	sequence atomic.Int64
	base     string
	token    string
	http     *http.Client
}

func NewClient(base, token string, transport http.RoundTripper) (*Client, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 || len(token) != 43 {
		return nil, &APIError{"invalid_client_config"}
	}
	return newClient(base, token, transport)
}

func newClient(base, token string, transport http.RoundTripper) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && net.ParseIP(u.Hostname()).IsLoopback())) {
		return nil, &APIError{"invalid_client_config"}
	}
	if transport == nil {
		transport = NewTransport(nil)
	}
	return &Client{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) do(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return &APIError{"invalid_traffic"}
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return &APIError{"invalid_client_config"}
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(req)
	if err != nil {
		return &APIError{"heartbeat_unavailable"}
	}
	defer response.Body.Close()
	limit := int64(64 << 10)
	if path == "/config" || strings.HasPrefix(path, "/config?") {
		limit = 8 << 20
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(data)) > limit || !utf8.Valid(data) {
		return &APIError{"invalid_server_response"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var payload struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &payload) == nil && len(payload.Error.Code) > 0 && len(payload.Error.Code) < 80 {
			return &APIError{PublicError(&APIError{payload.Error.Code})}
		}
		return &APIError{"heartbeat_rejected"}
	}
	if output != nil && json.Unmarshal(data, output) != nil {
		return &APIError{"invalid_server_response"}
	}
	return nil
}

func validStatus(status Status) bool {
	if status.SessionID == "" || status.ReportInterval != 60 || status.LeaseSeconds != 90 || status.SessionIdleTimeout != 180 || status.RatePermille < 1 || status.RatePermille > 10000 || status.MeteringSource != "client_reported" || status.ServerTime.IsZero() || status.ExpiresAt.IsZero() || status.RemainingBytes < 0 || status.RemainingBytes > MaxCounter || status.TotalBytes < 0 || status.TotalBytes > MaxCounter || status.RemainingBytes > status.TotalBytes || status.UsedUnits < 0 || status.UploadBytes < 0 || status.UploadBytes > MaxCounter || status.DownloadBytes < 0 || status.DownloadBytes > MaxCounter || status.LastSequence < 0 || status.LastSequence > 1<<40 {
		return false
	}
	if !status.CanConnect {
		return status.Reason != "" && status.AuthorizationExpiresAt == nil
	}
	version, err := hex.DecodeString(status.ProfileVersion)
	return err == nil && len(version) == 32 && status.Reason == "" && status.RemainingBytes > 0 && status.SubscriptionExpiresAt != nil && status.AuthorizationExpiresAt != nil && status.AuthorizationExpiresAt.After(status.ServerTime) && !status.AuthorizationExpiresAt.After(status.ServerTime.Add(time.Duration(status.LeaseSeconds)*time.Second)) && !status.AuthorizationExpiresAt.After(status.ExpiresAt) && !status.AuthorizationExpiresAt.After(*status.SubscriptionExpiresAt)
}

func authorizationDeadline(status Status, started time.Time) time.Time {
	if !status.CanConnect || status.AuthorizationExpiresAt == nil || status.SubscriptionExpiresAt == nil {
		return started
	}
	deadline := started.Add(status.AuthorizationExpiresAt.Sub(status.ServerTime))
	for _, absolute := range []time.Time{status.ExpiresAt, *status.SubscriptionExpiresAt} {
		if absolute.Before(deadline) {
			deadline = absolute
		}
	}
	return deadline
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var status Status
	err := c.do(ctx, "GET", "/session", nil, &status)
	if err == nil && !validStatus(status) {
		err = &APIError{"invalid_server_response"}
	}
	if err == nil {
		c.observeSequence(status.LastSequence)
	}
	return status, err
}

func (c *Client) Report(ctx context.Context, counters Counters) (Status, error) {
	var response struct {
		Session Status `json:"session"`
	}
	err := c.do(ctx, "POST", "/traffic", counters, &response)
	if err == nil && (!validStatus(response.Session) || response.Session.LastSequence != counters.Sequence || response.Session.UploadBytes != counters.UploadBytes || response.Session.DownloadBytes != counters.DownloadBytes) {
		err = &APIError{"invalid_server_response"}
	}
	if err == nil {
		c.observeSequence(response.Session.LastSequence)
	}
	return response.Session, err
}

func (c *Client) Logout(ctx context.Context) error {
	var result struct {
		LoggedOut bool `json:"logged_out"`
	}
	if err := c.do(ctx, "POST", "/logout", struct{}{}, &result); err != nil {
		return err
	}
	if !result.LoggedOut {
		return &APIError{"invalid_server_response"}
	}
	return nil
}
func (c *Client) Close() { c.http.CloseIdleConnections() }
func errorCode(err error) string {
	var api *APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return "heartbeat_unavailable"
}

type LoginParams struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	DeviceID   string `json:"device_id"`
	Platform   string `json:"platform"`
	AppVersion string `json:"app_version"`
}

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type Profile struct {
	Nodes   []NodeChoice `json:"nodes,omitempty"`
	NodeID  string       `json:"node_id,omitempty"`
	YAML    string       `json:"yaml"`
	Version string       `json:"version"`
	Session Status       `json:"session"`
}

func (p LoginParams) Valid() bool {
	device := strings.ReplaceAll(p.DeviceID, "-", "")
	id, err := hex.DecodeString(device)
	platform := p.Platform == "android" || p.Platform == "macos" || p.Platform == "windows" || p.Platform == "linux"
	return err == nil && len(id) == 16 && device != strings.Repeat("0", 32) && len(p.DeviceID) == 36 && p.DeviceID[8] == '-' && p.DeviceID[13] == '-' && p.DeviceID[18] == '-' && p.DeviceID[23] == '-' && platform && len(p.AppVersion) <= 64 && len(p.Email) <= 254 && strings.Contains(p.Email, "@") && len(p.Password) > 0 && len(p.Password) <= 72 && utf8.ValidString(p.Password) && !strings.ContainsAny(p.Email, "\r\n\x00")
}

func Login(ctx context.Context, base string, transport http.RoundTripper, params LoginParams) (*Client, User, Status, error) {
	if !params.Valid() {
		return nil, User{}, Status{}, &APIError{"invalid_client_login"}
	}
	client, err := newClient(base, "", transport)
	if err != nil {
		return nil, User{}, Status{}, err
	}
	var result struct {
		Token   string `json:"token"`
		User    User   `json:"user"`
		Session Status `json:"session"`
	}
	err = client.do(ctx, "POST", "/login", params, &result)
	if err == nil {
		_, tokenErr := NewClient(base, result.Token, client.http.Transport)
		if tokenErr != nil || !validStatus(result.Session) || result.User.ID == "" || len(result.User.Email) > 254 || result.User.Email == "" || len(result.User.Name) > 512 {
			err = &APIError{"invalid_server_response"}
		}
	}
	if err != nil {
		client.Close()
		return nil, User{}, Status{}, err
	}
	client.token = result.Token
	client.observeSequence(result.Session.LastSequence)
	return client, result.User, result.Session, nil
}

func (c *Client) Config(ctx context.Context) (Profile, error) {
	return c.ConfigForNode(ctx, "")
}

func (c *Client) observeSequence(sequence int64) {
	for previous := c.sequence.Load(); sequence > previous; previous = c.sequence.Load() {
		if c.sequence.CompareAndSwap(previous, sequence) {
			return
		}
	}
}

type NodeChoice struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Region       string `json:"region"`
	LineType     string `json:"line_type"`
	RatePermille int64  `json:"rate_permille"`
	Version      int64  `json:"version"`
}

func (p Profile) ValidCatalog() bool {
	if p.NodeID == "" {
		return len(p.Nodes) == 0
	}
	if len(p.Nodes) == 0 || len(p.Nodes) > 128 {
		return false
	}
	ids, names := map[string]bool{}, map[string]bool{}
	found := false
	for _, node := range p.Nodes {
		id, err := hex.DecodeString(strings.ReplaceAll(node.ID, "-", ""))
		if err != nil || len(id) != 16 || len(node.ID) != 36 || ids[node.ID] || node.Name == "" || len(node.Name) > 256 || strings.ContainsFunc(node.Name, unicode.IsControl) || names[node.Name] || node.RatePermille < 1 || node.RatePermille > 10000 || node.Version < 1 || node.LineType != "direct" && node.LineType != "dedicated" {
			return false
		}
		ids[node.ID], names[node.Name] = true, true
		if node.ID == p.NodeID {
			found = node.RatePermille == p.Session.RatePermille
		}
	}
	return found
}

func (c *Client) ConfigForNode(ctx context.Context, nodeID string) (Profile, error) {
	var profile Profile
	query := url.Values{"last_sequence": []string{strconv.FormatInt(c.sequence.Load(), 10)}}
	if nodeID != "" {
		query.Set("node_id", nodeID)
	}
	if err := c.do(ctx, "GET", "/config?"+query.Encode(), nil, &profile); err != nil {
		return Profile{}, err
	}
	digest := sha256.Sum256([]byte(profile.YAML))
	if len(profile.YAML) == 0 || len(profile.YAML) > 1<<20 || !utf8.ValidString(profile.YAML) || strings.ContainsRune(profile.YAML, 0) || hex.EncodeToString(digest[:]) != profile.Version || profile.Version != profile.Session.ProfileVersion || !validStatus(profile.Session) || !profile.Session.CanConnect || !profile.ValidCatalog() || nodeID != "" && profile.NodeID != nodeID {
		return Profile{}, &APIError{"invalid_server_response"}
	}
	c.observeSequence(profile.Session.LastSequence)
	return profile, nil
}

func PublicError(err error) string {
	code := errorCode(err)
	switch code {
	case "managed_tun_permission_required", "managed_tun_start_failed", "managed_tun_cleanup_failed", "managed_tun_route_conflict", "managed_tun_route_check_failed":
		return code
	case "invalid_core_counters", "traffic_unconfirmed", "final_traffic_unconfirmed", "invalid_runtime_options", "managed_connection_required", "runtime_start_failed", "lifecycle_reconfirmation_required", "lease_expired", "session_mismatch":
		return code
	case "unsupported_managed_configuration", "configuration_storage_failed", "configuration_cleanup_failed", "configuration_apply_failed", "configuration_expired", "managed_configuration_required", "invalid_managed_selection":
		return code
	case "invalid_credentials", "invalid_client_login", "invalid_client_config", "native_session_required", "native_session_expired", "upgrade_required", "paid_vip_required", "profile_required", "subscription_expired", "quota_exhausted", "device_limit", "subscription_changed", "profile_changed", "client_config_unavailable", "traffic_conflict", "traffic_sequence", "heartbeat_unavailable", "heartbeat_rejected", "invalid_server_response", "auth_unavailable", "session_failed", "rate_limited", "operation_superseded", "operation_in_progress", "meter_not_ready", "logout_unconfirmed":
		return code
	default:
		return "request_failed"
	}
}
