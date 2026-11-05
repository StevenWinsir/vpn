//go:build !darwin && !windows

package main

const defaultCoreName = "asterlink-core"

// probeCore has nothing to read here: Android is checked from outside with
// scripts/android/thread-sched.py over adb, and iOS through the extension's own resource
// samples (handoff_fix.md 6.2). Throughput is still measured.
func probeCore(name string, pid int) coreSample {
	return coreSample{PID: pid, Err: "not available on this platform"}
}
