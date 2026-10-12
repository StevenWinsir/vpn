// Package forwarder is the enforcement side of a node agent (handoff_app.md 7.3): a
// real proxy that knows which grant every connection belongs to, counts the bytes it
// carries itself, and can stop one grant's traffic at once.
//
// Everything the control plane promises about a grant is enforced here, locally and
// without further help from the control plane: the credential must be installed, the
// absolute deadline must not have passed, and the byte allowance must not be spent.
package forwarder

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Counters are cumulative payload bytes for one grant since this process started
// (one "epoch"). Upload is client -> target, Download is target -> client. Protocol
// framing and TLS overhead are not counted.
type Counters struct{ Upload, Download int64 }

// Grant is what the control plane told the agent to install.
type Grant struct {
	ID         string
	Protocol   string
	Credential map[string]string
	Deadline   time.Time
	// EpochBudget caps this epoch's Upload+Download for the grant.
	EpochBudget int64
}

// Forwarder is what the agent drives. Implementations must make Remove final: when it
// returns, no byte of that grant is in flight and the returned counters are complete.
type Forwarder interface {
	// Protocols are the proxy types this forwarder can enforce.
	Protocols() []string
	Install(Grant) error
	// Update changes the deadline and byte cap of an installed grant.
	Update(id string, deadline time.Time, epochBudget int64) bool
	// Remove deletes the credential, closes every TCP/UDP flow of the grant and waits for
	// them to end. ok is false if the grant was not installed.
	Remove(id string) (final Counters, ok bool)
	Usage() map[string]Counters
	// Expired lists installed grants whose absolute deadline has passed. A spent byte
	// cap is not expiry: the grant stays installed (and silent) so a budget top-up can
	// revive it.
	Expired(now time.Time) []string
	Close() error
}

var (
	ErrUnsupported = errors.New("forwarder does not enforce this protocol")
	ErrDuplicate   = errors.New("grant credential already installed")
)

// entry is the live state of one installed grant, shared by all of its flows.
type entry struct {
	id       string
	deadline atomic.Int64 // unix nanos
	cap      atomic.Int64 // epoch byte cap
	spent    atomic.Int64 // bytes accepted so far (up+down)
	up, down atomic.Int64
	closed   atomic.Bool

	mu    sync.Mutex
	conns map[closer]struct{}
	wg    sync.WaitGroup
}

type closer interface{ Close() error }

func newEntry(g Grant) *entry {
	e := &entry{id: g.ID, conns: map[closer]struct{}{}}
	e.deadline.Store(g.Deadline.UnixNano())
	e.cap.Store(g.EpochBudget)
	return e
}

func (e *entry) live(now time.Time) bool {
	return !e.closed.Load() && now.UnixNano() < e.deadline.Load() && e.spent.Load() < e.cap.Load()
}

// admit atomically allows up to n more bytes under the cap and returns how many.
func (e *entry) admit(n int64) int64 {
	for {
		spent, limit := e.spent.Load(), e.cap.Load()
		room := limit - spent
		if room <= 0 {
			return 0
		}
		take := min(n, room)
		if e.spent.CompareAndSwap(spent, spent+take) {
			return take
		}
	}
}

// refund returns bytes that were admitted but not delivered.
func (e *entry) refund(n int64) {
	if n > 0 {
		e.spent.Add(-n)
	}
}

// track registers a flow; it fails once the grant is closed, so a flow can never be
// added after Remove has swept the set.
func (e *entry) track(c closer) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed.Load() {
		return false
	}
	e.conns[c] = struct{}{}
	e.wg.Add(1)
	return true
}

func (e *entry) untrack(c closer) {
	e.mu.Lock()
	delete(e.conns, c)
	e.mu.Unlock()
	e.wg.Done()
}

func (e *entry) killAll() {
	e.mu.Lock()
	conns := make([]closer, 0, len(e.conns))
	for c := range e.conns {
		conns = append(conns, c)
	}
	e.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

func (e *entry) counters() Counters { return Counters{Upload: e.up.Load(), Download: e.down.Load()} }
