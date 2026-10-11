package ipc

import (
	"bytes"
	"encoding/json"
)

// ProtocolVersion is independent of the application version.
const ProtocolVersion = 1

// Command names (handoff_app.md section 5.3). Only Hello and Shutdown are
// implemented in stage 1; the rest are recognised so they fail with a stable code
// instead of being treated as unknown input.
const (
	CmdHello          = "hello"
	CmdLogin          = "login"
	CmdReadSnapshot   = "read_snapshot"
	CmdRefreshCatalog = "refresh_catalog"
	CmdSelectNode     = "select_node"
	CmdConnect        = "connect"
	CmdDisconnect     = "disconnect"
	CmdLogout         = "logout"
	CmdShutdown       = "shutdown"
)

// Stable error codes. Clients branch on these, never on message text.
const (
	CodeInvalidRequest      = "invalid_request"
	CodeUnknownCommand      = "unknown_command"
	CodeNotImplemented      = "not_implemented"
	CodeProtocolUnsupported = "protocol_unsupported"
	CodePayloadTooLarge     = "payload_too_large"
)

// perCommandLimit bounds a whole request frame. Commands not listed are unknown.
var perCommandLimit = map[string]uint32{
	CmdHello:          4 << 10,
	CmdShutdown:       1 << 10,
	CmdReadSnapshot:   1 << 10,
	CmdRefreshCatalog: 1 << 10,
	CmdSelectNode:     4 << 10,
	CmdConnect:        4 << 10,
	CmdDisconnect:     1 << 10,
	CmdLogout:         1 << 10,
	CmdLogin:          16 << 10,
}

type Request struct {
	RequestID       string          `json:"request_id"`
	ProtocolVersion int             `json:"protocol_version"`
	Generation      uint64          `json:"generation"`
	Command         string          `json:"command"`
	Payload         json.RawMessage `json:"payload,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Response struct {
	RequestID       string          `json:"request_id"`
	ProtocolVersion int             `json:"protocol_version"`
	Generation      uint64          `json:"generation"`
	OK              bool            `json:"ok"`
	Result          json.RawMessage `json:"result,omitempty"`
	Error           *Error          `json:"error,omitempty"`
}

// HelloResult carries only non-secret build identity.
type HelloResult struct {
	Component          string `json:"component"`
	CoreVersion        string `json:"core_version"`
	ProtocolVersion    int    `json:"protocol_version"`
	MinProtocolVersion int    `json:"min_protocol_version"`
	MaxProtocolVersion int    `json:"max_protocol_version"`
	MihomoRevision     string `json:"mihomo_revision"`
}

// DecodeRequest strictly decodes one frame: unknown fields, trailing data and
// oversized per-command bodies are errors. On an unknown command or oversize the
// partially decoded request is returned so the reply can echo request_id.
func DecodeRequest(frame []byte) (Request, *Error) {
	var request Request
	decoder := json.NewDecoder(bytes.NewReader(frame))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return Request{}, &Error{CodeInvalidRequest, "malformed request"}
	}
	if int(decoder.InputOffset()) != len(bytes.TrimRight(frame, " \t\r\n")) {
		return Request{}, &Error{CodeInvalidRequest, "trailing data"}
	}
	if request.RequestID == "" || len(request.RequestID) > 64 {
		return Request{}, &Error{CodeInvalidRequest, "request_id required (<=64 bytes)"}
	}
	limit, known := perCommandLimit[request.Command]
	if !known {
		return request, &Error{CodeUnknownCommand, "unknown command"}
	}
	if uint32(len(frame)) > limit {
		return request, &Error{CodePayloadTooLarge, "request too large for command"}
	}
	return request, nil
}
