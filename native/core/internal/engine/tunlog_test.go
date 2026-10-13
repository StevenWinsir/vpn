package engine

import (
	"strings"
	"testing"
	"time"

	mlog "github.com/metacubex/mihomo/log"
)

func waitFor(t *testing.T, w *tunErrors) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := w.Err(); got != "" {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	return ""
}

// ReCreateTun only logs its failure; the watcher must surface exactly that line.
func TestTunCreationFailureIsSurfacedFromTheLog(t *testing.T) {
	w := watchTunErrors()
	defer w.Close()
	mlog.Warnln("[TUN] something unrelated")
	mlog.Errorln("[Rule] another error that is not about the TUN")
	mlog.Errorln("Start TUN listening error: %s", "Cannot create a file when that file already exists.")
	if got := waitFor(t, w); got != "Cannot create a file when that file already exists." {
		t.Fatalf("got %q", got)
	}
}

func TestTunFailureTextIsCapped(t *testing.T) {
	w := watchTunErrors()
	defer w.Close()
	mlog.Errorln("Start TUN listening error: %s", strings.Repeat("x", 1000))
	if got := waitFor(t, w); len(got) != 200 {
		t.Fatalf("length %d, want the 200 byte cap", len(got))
	}
}

func TestEachWatcherStartsClean(t *testing.T) {
	first := watchTunErrors()
	mlog.Errorln("Start TUN listening error: %s", "old failure")
	waitFor(t, first)
	first.Close()
	second := watchTunErrors()
	defer second.Close()
	time.Sleep(50 * time.Millisecond)
	if got := second.Err(); got != "" {
		t.Fatalf("a new Start inherited the previous failure %q", got)
	}
}
