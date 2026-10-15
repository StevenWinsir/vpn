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
	for {
		frame, err := ReadFrame(rw, MaxFrame)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		response, shutdown := HandleOne(context.Background(), frame, info, handler)
		body, err := json.Marshal(response)
		if err != nil {
			return err
		}
		if err := WriteFrame(rw, body); err != nil {
			return err
		}
		if shutdown {
			return nil
		}
	}
}

// HandleOne answers one request body. It is the single place a request is decoded and
// dispatched: the framed pipe (ServeWith) and the in-process mobile binding
// (native/core/mobile) both call it, so strict decoding, the protocol version check,
// the per-command limits and the generation echo cannot drift between them. shutdown
// reports that the caller must end the session after delivering the response. A body
// the framing layer would have refused (empty or over MaxFrame) is answered with an
// error here, because an in-process caller has no stream to abandon.
func HandleOne(ctx context.Context, frame []byte, info BuildInfo, handler Handler) (response Response, shutdown bool) {
	if len(frame) == 0 {
		return Response{ProtocolVersion: ProtocolVersion, Error: &Error{CodeInvalidRequest, "empty request"}}, false
	}
	if len(frame) > MaxFrame {
		return Response{ProtocolVersion: ProtocolVersion, Error: &Error{CodePayloadTooLarge, "request exceeds the frame limit"}}, false
	}
	request, bad := DecodeRequest(frame)
	response = Response{RequestID: request.RequestID, ProtocolVersion: ProtocolVersion, Generation: request.Generation}
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
		return response, true
	case handler != nil:
		result, failure := handler.Handle(ctx, request)
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
	return response, false
}
