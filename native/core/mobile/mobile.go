// Package mobile is the in-process binding the Android app and the iOS app and packet tunnel
// extension link through gomobile (handoff_app_android.md 3.3, handoff_app_ios.md 4, 7). The Kotlin side gets Start, Call, Stop, the NetworkChanged
// hint and one callback interface; everything else (session, configuration, lease, metering, grants)
// stays in the same Go Core that macOS and Windows run behind their pipes.
//
// Call takes the same JSON document an IPC frame carries (shared/contracts/ipc-v1.json)
// and answers through ipc.HandleOne, the decoder the pipe server uses, so there is no
// second command parser. Commands run one at a time, like on the desktop pipe.
//
// Nothing secret crosses this boundary in the other direction: the build identity, the
// snapshot and stable error codes. Diagnostics must never reach logcat with a token,
// password or node configuration in them.
package mobile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"asterlink/core/internal/engine"
	"asterlink/core/internal/ipc"
	"asterlink/core/internal/managed"
	"asterlink/core/internal/network"
	"asterlink/core/internal/service"
)

// Set with -ldflags "-X asterlink/core/mobile.version=... -X asterlink/core/mobile.mihomoRevision=...". Only development
// builds stamp the revision; release builds leave it empty (handoff_app_standardization.md 8.2),
// so no default may carry one: an overridden default string would still be linked in.
var (
	version        = "0.0.0-dev"
	mihomoRevision = ""
)

// CodeNotStarted answers every session command sent before Start or after Stop. hello
// and shutdown do not need a running Core and answer as usual.
const CodeNotStarted = "core_not_started"

// Platform is implemented in Kotlin by the VpnService owner, and in Swift by the iOS packet
// tunnel provider (the iOS app's own implementation refuses OpenTun: it never owns a tunnel).
type Platform interface {
	// Protect is VpnService.protect(fd): the socket bypasses the VPN.
	Protect(fd int32) bool
	// OpenTun establishes the VPN interface described by specJSON (engine.TunSpec:
	// addresses, prefixes, MTU, DNS, whether IPv6 is carried; never any node data) and
	// returns the detached file descriptor (iOS: a dup() of the utun descriptor), which the
	// Core owns and closes from then on, or -1 when no VpnService/provider is running or the
	// system refused.
	OpenTun(specJSON string) int32
	// NetworkSnapshot reports the default network (network.ParseHostSnapshot format).
	NetworkSnapshot() string
}

// buildConfig is Start's input: process facts only. The API base, channel and app
// version travel in the login command exactly as on macOS and Windows, where the one
// strict login decoder validates them.
type buildConfig struct {
	// CacheDir is the app's private cache directory (Context.getCacheDir). Android has
	// no writable TMPDIR by default; the engine's per-profile Mihomo home lives here.
	CacheDir string `json:"cache_dir"`
}

// Unexported so gomobile does not add them to the Java surface; Kotlin sees the message.
var (
	errAlreadyStarted = errors.New("core already started")
	errNoPlatform     = errors.New("platform callbacks are required")
	errBuildConfig    = errors.New("invalid build configuration")
)

// shutdownBudget bounds Stop: close the data path, settle the last batch, revoke.
const shutdownBudget = 10 * time.Second

type session struct {
	// mu serialises commands, as one pipe does on the desktop.
	mu      sync.Mutex
	handler ipc.Handler
	ctx     context.Context
	cancel  context.CancelFunc
	// closed is read without mu so Start and Call never wait behind a running command.
	closed atomic.Bool
	// manager receives the foreground hint directly: it must not wait behind a command.
	manager *managed.Manager
}

func newSession(handler ipc.Handler) *session {
	ctx, cancel := context.WithCancel(context.Background())
	return &session{handler: handler, ctx: ctx, cancel: cancel}
}

func (s *session) call(request string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	handler := s.handler
	if s.closed.Load() {
		handler = notStarted{}
	}
	response, shutdown := ipc.HandleOne(s.ctx, []byte(request), info(), handler)
	if shutdown {
		s.closeLocked()
	}
	return encode(response)
}

// close interrupts a command in flight (a 90-second connect), then ends the session.
func (s *session) close() {
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked()
}

func (s *session) closeLocked() {
	if s.closed.Swap(true) {
		return
	}
	s.cancel()
	if s.handler != nil {
		s.handler.Close()
	}
}

var (
	currentMu sync.Mutex
	current   *session
	// idle answers before Start and after Stop.
	idle = &session{handler: notStarted{}, ctx: context.Background(), cancel: func() {}}
)

func active() *session {
	currentMu.Lock()
	defer currentMu.Unlock()
	if current == nil || current.closed.Load() {
		return idle
	}
	return current
}

// Start creates the Core for this process. platform stays registered until Stop.
func Start(platform Platform, buildJSON string) error {
	if platform == nil {
		return errNoPlatform
	}
	config, err := parseBuild(buildJSON)
	if err != nil {
		return err
	}
	currentMu.Lock()
	defer currentMu.Unlock()
	if current != nil && !current.closed.Load() {
		return errAlreadyStarted
	}
	// os.TempDir is what the engine uses for Mihomo's working directory.
	if err := os.Setenv("TMPDIR", config.CacheDir); err != nil {
		return err
	}
	network.SetHost(host{platform})
	engine.SetTunHost(host{platform})
	// A phone sleeps whenever its screen is off: a lapse closes the authorization gate and
	// re-confirms instead of dropping the VPN (handoff_app_android.md 7.5).
	manager := managed.New(managed.Options{CoreVersion: version, Engine: engine.New(), ResumePolicy: managed.ResumeGate})
	current = newSession(service.New(manager))
	current.manager = manager
	return nil
}

// Call answers one request document (the body of an ipc-v1 frame) with one response
// document. It blocks for as long as the command runs (connect: up to 90 seconds), so
// the app calls it off the main thread.
func Call(requestJSON string) string { return active().call(requestJSON) }

// Stop ends the session: an in-flight command is cancelled, the data path closed, the
// last traffic batch settled and the session revoked, all within a bounded time. Safe to
// call at any time and more than once.
func Stop() {
	currentMu.Lock()
	s := current
	currentMu.Unlock()
	if s != nil {
		s.close()
	}
	engine.SetTunHost(nil)
	network.SetHost(nil)
}

// NetworkChanged is the app's hint that the default network changed (a
// ConnectivityManager callback). It refreshes Mihomo's network-dependent state; whether a
// running session may continue is the Core's own decision (the watchdog compares network
// signatures and re-verifies). Safe at any time.
func NetworkChanged() {
	engine.NetworkChanged()
	// A gate waiting for the network retries now rather than after its backoff.
	if s := active(); s.manager != nil {
		s.manager.Nudge()
	}
}

// SetForeground tells the Core whether a screen of the app is visible
// (ProcessLifecycleOwner). In the background the catalog long-poll pauses to spare the
// radio; authorization is unaffected (handoff_app_android.md 7.6). Safe at any time; the
// Core starts in the foreground.
func SetForeground(visible bool) {
	if s := active(); s.manager != nil {
		s.manager.SetForeground(visible)
	}
}

func info() ipc.BuildInfo {
	return ipc.BuildInfo{CoreVersion: version, MihomoRevision: mihomoRevision}
}

func parseBuild(raw string) (buildConfig, error) {
	var config buildConfig
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.DisallowUnknownFields()
	if len(raw) == 0 || len(raw) > 4<<10 || decoder.Decode(&config) != nil || decoder.More() {
		return buildConfig{}, errBuildConfig
	}
	if !filepath.IsAbs(config.CacheDir) || filepath.Clean(config.CacheDir) != config.CacheDir {
		return buildConfig{}, errBuildConfig
	}
	if stat, err := os.Stat(config.CacheDir); err != nil || !stat.IsDir() {
		return buildConfig{}, errBuildConfig
	}
	return config, nil
}

func encode(response ipc.Response) string {
	body, err := json.Marshal(response)
	if err != nil {
		// Response holds only strings, numbers and pre-encoded JSON; this cannot happen,
		// but the caller still gets a well-formed document with a stable code.
		body, _ = json.Marshal(ipc.Response{RequestID: response.RequestID, ProtocolVersion: ipc.ProtocolVersion,
			Error: &ipc.Error{Code: ipc.CodeInvalidRequest, Message: "internal encoding error"}})
	}
	return string(body)
}

// notStarted answers session commands while no Core is running.
type notStarted struct{}

func (notStarted) Handle(context.Context, ipc.Request) (any, *ipc.Error) {
	return nil, &ipc.Error{Code: CodeNotStarted, Message: "core is not running"}
}

func (notStarted) Close() {}

// host adapts the Kotlin callbacks to the network package.
type host struct{ platform Platform }

func (h host) Protect(fd int) bool     { return h.platform.Protect(int32(fd)) }
func (h host) Snapshot() string        { return h.platform.NetworkSnapshot() }
func (h host) OpenTun(spec string) int { return int(h.platform.OpenTun(spec)) }
