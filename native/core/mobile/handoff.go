package mobile

import (
	"context"
	"encoding/json"
	"runtime"
	"runtime/debug"
	"time"

	"asterlink/core/internal/ipc"
	"asterlink/core/internal/managed"
	"asterlink/core/internal/service"
)

// iOS-only surface (handoff_app_ios.md 4.2, 7, 8.7; shared/contracts/handoff-v1.json). The
// app and the packet tunnel extension each run a Core; ExportSession and AdoptSession move
// the one signed-in session between them so that only one process ever writes to the server.
// They are not ipc-v1 commands: the desktop pipes and Android never see them. Kotlin ignores
// these functions. Both answer an ipc-v1 response document so Swift decodes them like Call.

// handoffBudget bounds export (close the data path, settle) and adopt (GET /session, the
// catalog and the line's config).
const handoffBudget = 45 * time.Second

// ExportSession closes the data path, settles every counted byte and hands the session out:
// result {"ticket": "<ticket document>"}. The Core then reports state "released" and holds
// no token. The ticket carries the bearer token: keep it in memory only.
func ExportSession() string {
	return active().handoff(func(ctx context.Context, m *managed.Manager) (any, error) {
		ticket, err := m.ExportHandoff(ctx)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(ticket)
		if err != nil {
			return nil, err
		}
		return struct {
			Ticket string `json:"ticket"`
		}{string(raw)}, nil
	})
}

// AdoptSession continues the session a ticket describes (the extension at start, the app
// when the extension hands it back): result is the snapshot with action_error, exactly as
// after login.
func AdoptSession(ticketJSON string) string {
	return active().handoff(func(ctx context.Context, m *managed.Manager) (any, error) {
		ticket, err := managed.ParseHandoff([]byte(ticketJSON), time.Now())
		if err != nil {
			return nil, err
		}
		snapshot, err := m.AdoptHandoff(ctx, ticket)
		return service.Reply(snapshot, err), nil
	})
}

// handoff runs fn like a command: serialised with Call, refused when no Core runs.
func (s *session) handoff(fn func(context.Context, *managed.Manager) (any, error)) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() || s.manager == nil {
		return encode(ipc.Response{ProtocolVersion: ipc.ProtocolVersion, Error: &ipc.Error{Code: CodeNotStarted, Message: "core is not running"}})
	}
	ctx, cancel := context.WithTimeout(s.ctx, handoffBudget)
	defer cancel()
	result, err := fn(ctx, s.manager)
	response := ipc.Response{ProtocolVersion: ipc.ProtocolVersion, Generation: s.manager.Generation()}
	if err != nil {
		response.Error = service.ErrorFor(err)
		return encode(response)
	}
	body, mErr := json.Marshal(result)
	if mErr != nil {
		response.Error = &ipc.Error{Code: ipc.CodeInvalidRequest, Message: "internal encoding error"}
		return encode(response)
	}
	response.OK, response.Result = true, body
	return encode(response)
}

// TuneMemory applies the extension's memory budget (8.7): a soft limit for the Go heap in
// bytes (0 keeps the current one) and the GC target percentage (0 keeps it). The extension's
// jetsam limit counts the whole process, so the Swift side also watches phys_footprint.
func TuneMemory(limitBytes int64, gcPercent int) {
	if limitBytes > 0 {
		debug.SetMemoryLimit(limitBytes)
	}
	if gcPercent > 0 {
		debug.SetGCPercent(gcPercent)
	}
}

// FreeMemory returns as much memory to the system as the Go runtime can (8.7 soft threshold).
func FreeMemory() { debug.FreeOSMemory() }

// RuntimeStats is a non-secret diagnostic document for the cycle and memory gates
// (D-CYCLES, D-MEM): goroutines, Go heap in use and the bytes obtained from the system.
func RuntimeStats() string {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	body, _ := json.Marshal(struct {
		Goroutines int    `json:"goroutines"`
		HeapInUse  uint64 `json:"heap_in_use"`
		Sys        uint64 `json:"sys"`
		NumGC      uint32 `json:"num_gc"`
	}{runtime.NumGoroutine(), stats.HeapInuse, stats.Sys, stats.NumGC})
	return string(body)
}
