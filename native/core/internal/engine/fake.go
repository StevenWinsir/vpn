//go:build fakeengine

package engine

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"asterlink/core/internal/managed"
	"asterlink/core/internal/network"
)

// Fake is a test-only engine compiled solely under the `fakeengine` tag, which no
// release build uses. It never touches the network stack: "forwarded" bytes are read
// from a counters file the acceptance test writes, so the real Core binary can be
// driven against the real API and database without root.
//
//	ASTERLINK_FAKE_COUNTERS         path of a file holding "<upload> <download>"
//	ASTERLINK_FAKE_COUNTERS + .fail file naming the step that fails: preflight | start | verify
type Fake struct {
	mu      sync.Mutex
	running bool
}

func NewFake() *Fake { return &Fake{} }

var _ managed.Engine = (*Fake)(nil)

func failing(step string) bool {
	path := os.Getenv("ASTERLINK_FAKE_COUNTERS")
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path + ".fail")
	return err == nil && strings.TrimSpace(string(data)) == step
}

func (f *Fake) Preflight(context.Context) (network.Interface, error) {
	if failing("preflight") {
		return network.Interface{}, managed.ErrVPNConflict
	}
	return network.Interface{}, nil
}

func (f *Fake) Start(context.Context, managed.Profile) error {
	if failing("start") {
		return managed.ErrTunnelFailed
	}
	f.mu.Lock()
	f.running = true
	f.mu.Unlock()
	return nil
}

func (f *Fake) Verify(context.Context) error {
	if failing("verify") {
		return managed.ErrTunnelUnhealthy
	}
	return nil
}

func (f *Fake) Totals() (int64, int64, error) {
	path := os.Getenv("ASTERLINK_FAKE_COUNTERS")
	if path == "" {
		return 0, 0, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, nil
	}
	var up, down int64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d %d", &up, &down); err != nil {
		return 0, 0, nil
	}
	return up, down, nil
}

func (f *Fake) Stop() error {
	f.mu.Lock()
	f.running = false
	f.mu.Unlock()
	return nil
}
