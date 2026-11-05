package main

import (
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The probe opens a process for query only and reads its priority class, its execution-speed
// throttling policy and the power source. This test process has set no policy, so it must read
// as "system"; after it opts itself out the way the service does for the Core (F4), "opted_out".
func TestProbeReadsClassAndThrottling(t *testing.T) {
	s := probeCore("", os.Getpid())
	if s.PrioClass != "normal" || s.Throttling != "system" || s.Power == "" || s.Threads < 1 {
		t.Fatalf("own process before opting out: %+v", s)
	}
	state := powerThrottlingState{Version: powerThrottlingVersion, ControlMask: powerThrottlingExecutionSpeed}
	set := kernel32.NewProc("SetProcessInformation")
	if ok, _, err := set.Call(uintptr(windows.CurrentProcess()), processPowerThrottling, uintptr(unsafe.Pointer(&state)), unsafe.Sizeof(state)); ok == 0 {
		t.Skipf("this Windows build does not accept the policy: %v", err)
	}
	if s = probeCore("", os.Getpid()); s.Throttling != "opted_out" {
		t.Fatalf("after opting out: %+v", s)
	}
	if found := probeCore("no-such-process-asterlink.exe", 0); found.Err != "not running" {
		t.Fatalf("missing process: %+v", found)
	}
}

func TestThrottlingNames(t *testing.T) {
	cases := map[string]powerThrottlingState{
		"system":    {},
		"opted_out": {ControlMask: 1},
		"eco":       {ControlMask: 1, StateMask: 1},
	}
	for want, state := range cases {
		if got := throttlingName(state); got != want {
			t.Errorf("%+v: %s, want %s", state, got, want)
		}
	}
}
