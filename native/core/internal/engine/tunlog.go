package engine

import (
	"strings"
	"sync"

	mlog "github.com/metacubex/mihomo/log"
)

// tunErrors remembers why Mihomo failed to create the TUN. listener.ReCreateTun returns
// nothing; it only logs "Start TUN listening error: ..." at ERROR level. Without this
// the Core could only report "the device never appeared" after the full timeout, with no
// way to tell a missing privilege from a driver that cannot be loaded. The text is a
// system error (for example "Access is denied."), never node data, and is capped.
type tunErrors struct {
	mu   sync.Mutex
	last string
	done chan struct{}
	stop func()
}

const tunErrorPrefix = "Start TUN listening error: "

// watchTunErrors subscribes to Mihomo's log before the TUN is created.
func watchTunErrors() *tunErrors {
	sub := mlog.Subscribe()
	w := &tunErrors{done: make(chan struct{})}
	w.stop = func() { mlog.UnSubscribe(sub) }
	go func() {
		defer close(w.done)
		for event := range sub {
			if event.LogLevel != mlog.ERROR {
				continue
			}
			if text, found := strings.CutPrefix(event.Payload, tunErrorPrefix); found {
				w.mu.Lock()
				w.last = truncate(strings.TrimSpace(text), 200)
				w.mu.Unlock()
			}
		}
	}()
	return w
}

// Err returns the last TUN creation error text, "" if there was none.
func (w *tunErrors) Err() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.last
}

// Close ends the subscription and waits for the reader to drain.
func (w *tunErrors) Close() {
	w.stop()
	<-w.done
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit]
}
