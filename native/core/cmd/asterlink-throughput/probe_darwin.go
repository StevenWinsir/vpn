package main

import (
	"os/exec"
	"strconv"
	"strings"
)

const defaultCoreName = "asterlink-core"

func findCore(name string) int {
	out, err := exec.Command("pgrep", "-x", name).Output()
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0
	}
	pid, _ := strconv.Atoi(fields[0])
	return pid
}

// probeCore reads the thread priorities with ps, which works on a root-owned Core without
// privileges. It never signals or attaches to the process.
func probeCore(name string, pid int) coreSample {
	if pid == 0 {
		pid = findCore(name)
	}
	if pid == 0 {
		return coreSample{Err: "not running"}
	}
	out, err := exec.Command("ps", "-M", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return coreSample{PID: pid, Err: "ps failed (process gone?)"}
	}
	threads, lo, hi, err := parsePSThreads(string(out))
	if err != nil {
		return coreSample{PID: pid, Err: err.Error()}
	}
	return coreSample{PID: pid, Threads: threads, PrioMin: lo, PrioMax: hi}
}
