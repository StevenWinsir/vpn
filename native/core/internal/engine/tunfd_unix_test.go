//go:build !windows

package engine

import (
	"os"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func pipeFD(t *testing.T) (int, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	fd, err := syscall.Dup(int(w.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	return fd, r
}

// A descriptor nobody took over is closed by release (the VPN interface must go away).
func TestReleaseClosesADescriptorStillOwned(t *testing.T) {
	fd, _ := pipeFD(t)
	owned, err := adoptFD(fd)
	if err != nil {
		t.Fatal(err)
	}
	if !owned.open() {
		t.Fatal("adopted descriptor reported closed")
	}
	owned.release()
	var st syscall.Stat_t
	if syscall.Fstat(fd, &st) == nil {
		syscall.Close(fd)
		t.Fatal("release left the descriptor open")
	}
	if owned.set {
		t.Fatal("release must forget the descriptor")
	}
	owned.release() // idempotent
}

// Once the owner (Mihomo's listener) closed it and the number was reused by an unrelated
// file, release must leave that file alone.
func TestReleaseNeverClosesAReusedNumber(t *testing.T) {
	fd, _ := pipeFD(t)
	owned, err := adoptFD(fd)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Close(fd); err != nil { // the listener closed it
		t.Fatal(err)
	}
	other, err := os.CreateTemp(t.TempDir(), "unrelated")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	reused := int(other.Fd())
	if reused != fd {
		// Make the number collide on purpose: the kernel hands out the lowest free one,
		// but the test must not depend on scheduling elsewhere in the process.
		if err := unix.Dup2(reused, fd); err != nil {
			t.Fatal(err)
		}
		defer syscall.Close(fd)
	}
	owned.release()
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		t.Fatalf("release closed an unrelated file that reused the number: %v", err)
	}
}

func TestAdoptRefusesAClosedNumber(t *testing.T) {
	fd, _ := pipeFD(t)
	syscall.Close(fd)
	if _, err := adoptFD(fd); err == nil {
		t.Fatal("adopted a closed descriptor")
	}
}
