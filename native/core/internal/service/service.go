// Package service adapts IPC commands to the managed state machine. It is the only
// place that decodes command payloads, and it decodes strictly.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"asterlink/core/internal/apiclient"
	"asterlink/core/internal/ipc"
	"asterlink/core/internal/managed"
)

// Per-command budgets stay below the Helper's deadlines (connect 100s, others 40-60s) so the
// Core, not the Helper, decides how a slow exchange ends.
const (
	commandTimeout = 45 * time.Second
	connectTimeout = 90 * time.Second
)

var (
	deviceID   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	catalogRev = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Service struct {
	manager *managed.Manager
}

func New(manager *managed.Manager) *Service { return &Service{manager: manager} }

// Close runs when the session ends: close the data path, settle the last traffic batch,
// revoke. All of it is bounded; the Helper waits up to 12 seconds for the process to exit.
func (s *Service) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.manager.Shutdown(ctx)
}

func strict(raw json.RawMessage, into any) bool {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(into) == nil && !decoder.More()
}

func bad(message string) *ipc.Error {
	return &ipc.Error{Code: ipc.CodeInvalidRequest, Message: message}
}

func fromError(err error) *ipc.Error {
	var apiErr *apiclient.Error
	if errors.As(err, &apiErr) {
		return &ipc.Error{Code: apiErr.Code, Message: apiclient.Message(apiErr.Code)}
	}
	return &ipc.Error{Code: "network_unreachable", Message: apiclient.Message("network_unreachable")}
}

// needsFreshGeneration lists the commands that act on the current session and must
// quote its generation; logout, disconnect and read_snapshot are always safe to send.
var needsFreshGeneration = map[string]bool{ipc.CmdRefreshCatalog: true, ipc.CmdSelectNode: true, ipc.CmdConnect: true}

func (s *Service) Handle(parent context.Context, request ipc.Request) (any, *ipc.Error) {
	budget := commandTimeout
	if request.Command == ipc.CmdConnect {
		budget = connectTimeout
	}
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	if needsFreshGeneration[request.Command] && request.Generation != s.manager.Generation() {
		return nil, &ipc.Error{Code: "stale_generation", Message: apiclient.Message("stale_generation")}
	}
	switch request.Command {
	case ipc.CmdLogin:
		var in struct {
			Email      string `json:"email"`
			Password   string `json:"password"`
			APIBase    string `json:"api_base"`
			Channel    string `json:"channel"`
			DeviceID   string `json:"device_id"`
			AppVersion string `json:"app_version"`
		}
		if !strict(request.Payload, &in) {
			return nil, bad("malformed login payload")
		}
		in.Email = strings.TrimSpace(in.Email)
		if in.Email == "" || len(in.Email) > 254 || in.Password == "" || len(in.Password) > 72 || !utf8.ValidString(in.Password) ||
			!deviceID.MatchString(in.DeviceID) || len(in.AppVersion) > 64 || (in.Channel != "development" && in.Channel != "release") {
			return nil, bad("invalid login parameters")
		}
		snapshot, err := s.manager.Login(ctx, managed.LoginParams{Email: in.Email, Password: in.Password, APIBase: in.APIBase, Channel: in.Channel, DeviceID: in.DeviceID, AppVersion: in.AppVersion})
		// The snapshot is returned even on failure so the UI can show last_error.
		return reply(snapshot, err)
	case ipc.CmdReadSnapshot:
		if !strict(request.Payload, &struct{}{}) {
			return nil, bad("read_snapshot takes no payload")
		}
		return s.manager.Snapshot(), nil
	case ipc.CmdRefreshCatalog:
		if !strict(request.Payload, &struct{}{}) {
			return nil, bad("refresh_catalog takes no payload")
		}
		snapshot, err := s.manager.RefreshCatalog(ctx)
		return reply(snapshot, err)
	case ipc.CmdSelectNode:
		var in struct {
			NodeID          string `json:"node_id"`
			CatalogRevision string `json:"catalog_revision"`
		}
		if !strict(request.Payload, &in) || !deviceID.MatchString(in.NodeID) || !catalogRev.MatchString(in.CatalogRevision) {
			return nil, bad("invalid select_node payload")
		}
		snapshot, err := s.manager.SelectNode(ctx, in.NodeID, in.CatalogRevision)
		return reply(snapshot, err)
	case ipc.CmdConnect:
		if !strict(request.Payload, &struct{}{}) {
			return nil, bad("connect takes no payload")
		}
		snapshot, err := s.manager.Connect(ctx)
		return reply(snapshot, err)
	case ipc.CmdDisconnect:
		return s.manager.Disconnect(ctx), nil
	case ipc.CmdLogout:
		return s.manager.Logout(ctx), nil
	}
	return nil, &ipc.Error{Code: ipc.CodeNotImplemented, Message: "command not implemented in this build"}
}

// reply reports a failed action as ok=true with the new snapshot plus an action_error,
// so the UI always receives the up-to-date state alongside the reason.
func reply(snapshot managed.Snapshot, err error) (any, *ipc.Error) {
	if err == nil {
		return struct {
			managed.Snapshot
			ActionError *ipc.Error `json:"action_error"`
		}{Snapshot: snapshot}, nil
	}
	failure := fromError(err)
	return struct {
		managed.Snapshot
		ActionError *ipc.Error `json:"action_error"`
	}{Snapshot: snapshot, ActionError: failure}, nil
}
