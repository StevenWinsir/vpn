package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// coreSample is what the tool can see of the data-plane process from outside, without touching
// it: on macOS the scheduling priority of every thread (handoff_fix.md F1: 20 is the utility
// clamp, 31 normal); on Windows the priority class, the execution-speed throttling policy (F4)
// and whether the machine runs on battery. Fields a platform cannot read stay empty.
type coreSample struct {
	PID        int    `json:"pid"`
	Threads    int    `json:"threads"`
	PrioMin    int    `json:"prio_min,omitempty"`
	PrioMax    int    `json:"prio_max,omitempty"`
	PrioClass  string `json:"priority_class,omitempty"`
	Throttling string `json:"throttling,omitempty"`
	Power      string `json:"power,omitempty"`
	Err        string `json:"error,omitempty"`
}

func (c coreSample) csvFields() []string {
	prio := func(v int) string {
		if v == 0 {
			return ""
		}
		return strconv.Itoa(v)
	}
	return []string{strconv.Itoa(c.PID), strconv.Itoa(c.Threads), prio(c.PrioMin), prio(c.PrioMax), c.PrioClass, c.Throttling, c.Power, c.Err}
}

var coreCSVHeader = []string{"core_pid", "core_threads", "core_prio_min", "core_prio_max", "core_priority_class", "core_throttling", "power", "core_probe_error"}

// One ps -M line ends "... %CPU STAT PRI STIME UTIME [COMMAND]"; the process line also starts
// with USER, PID and TT, a thread line with PID only. PRI is a number with an optional letter.
var psThreadLine = regexp.MustCompile(`\s\d+\.\d+\s+\S+\s+(\d+)[A-Za-z]*\s`)

// parsePSThreads reads the per-thread priorities out of `ps -M -p <pid>`.
func parsePSThreads(out string) (threads, lo, hi int, err error) {
	for i, line := range strings.Split(out, "\n") {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue // header
		}
		m := psThreadLine.FindStringSubmatch(line + " ")
		if m == nil {
			continue
		}
		p, _ := strconv.Atoi(m[1])
		if threads == 0 || p < lo {
			lo = p
		}
		if p > hi {
			hi = p
		}
		threads++
	}
	if threads == 0 {
		return 0, 0, 0, fmt.Errorf("no thread line in ps output")
	}
	return threads, lo, hi, nil
}
