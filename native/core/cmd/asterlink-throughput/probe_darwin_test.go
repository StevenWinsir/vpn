package main

import (
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// The probe reads a live process's thread priorities. This test process runs unclamped (31);
// a child under `taskpolicy -c utility` is clamped to 20, which is exactly the F1 signature.
func TestProbeReadsThreadPriorities(t *testing.T) {
	s := probeCore("", os.Getpid())
	if s.Err != "" || s.Threads < 1 || s.PrioMin < 4 || s.PrioMax < s.PrioMin {
		t.Fatalf("own process: %+v", s)
	}
	if _, err := exec.LookPath("taskpolicy"); err != nil {
		t.Skip("taskpolicy not available")
	}
	child := exec.Command("taskpolicy", "-c", "utility", "sleep", "5")
	if err := child.Start(); err != nil {
		t.Skip("cannot start a clamped child: " + err.Error())
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	time.Sleep(200 * time.Millisecond)
	c := probeCore("", child.Process.Pid)
	if c.Err != "" || c.PrioMax > 20 {
		t.Fatalf("utility-clamped child %s: %+v", strconv.Itoa(child.Process.Pid), c)
	}
}
