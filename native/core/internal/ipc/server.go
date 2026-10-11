package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
)

// Handler implements every command other than hello and shutdown. A nil Handler
// leaves them answering not_implemented (the stage-1 behaviour).
type Handler interface {
	// Handle returns a JSON-serialisable result or a stable error.
	Handle(ctx context.Context, request Request) (any, *Error)
	// Close is called once when the session ends (shutdown, EOF or a framing error).
	Close()
}

// BuildInfo is injected by the entry point (ldflags), never read from the environment.
type BuildInfo struct {
	CoreVersion    string
	MihomoRevision string
}

// Serve answers requests sequentially until EOF, a framing error or shutdown.
// Framing errors terminate the session because the stream can no longer be trusted.
func Serve(rw io.ReadWriter, info BuildInfo) error { return ServeWith(rw, info, nil) }

// ServeWith is Serve with a command handler.
func ServeWith(rw io.ReadWriter, info BuildInfo, handler Handler) error {
	if handler != nil {
		defer handler.Close()
	}
	reply := func(response Response) error {
		body, err := json.Marshal(response)
		if err != nil {
			return err
		}
		return WriteFrame(rw, body)
	}
	for {
		frame, err := ReadFrame(rw, MaxFrame)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		request, bad := DecodeRequest(frame)
		response := Response{RequestID: request.RequestID, ProtocolVersion: ProtocolVersion, Generation: request.Generation}
		switch {
		case bad != nil:
			response.Error = bad
		case request.ProtocolVersion != ProtocolVersion:
			response.Error = &Error{CodeProtocolUnsupported, "protocol version not supported"}
		case request.Command == CmdHello:
			result, _ := json.Marshal(HelloResult{
				Component: "asterlink-core", CoreVersion: info.CoreVersion,
				ProtocolVersion: ProtocolVersion, MinProtocolVersion: ProtocolVersion, MaxProtocolVersion: ProtocolVersion,
				MihomoRevision: info.MihomoRevision,
			})
			response.OK, response.Result = true, result
		case request.Command == CmdShutdown:
			response.OK = true
			return reply(response)
		case handler != nil:
			result, failure := handler.Handle(context.Background(), request)
			if failure != nil {
				response.Error = failure
			} else if encoded, err := json.Marshal(result); err != nil {
				response.Error = &Error{CodeInvalidRequest, "internal encoding error"}
			} else {
				response.OK, response.Result = true, encoded
			}
		default:
			response.Error = &Error{CodeNotImplemented, "command not implemented in this build"}
		}
		if err := reply(response); err != nil {
			return err
		}
	}
}
