package main

import "testing"

func TestCycleGrowthRejectsKnownLeak(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		g0, g, fd0, fd, nat0, nat int
		fail                      bool
	}{
		{"flat", 12, 12, 460, 460, 0, 0, false},
		{"scheduler noise", 12, 14, 460, 461, 0, 0, false},
		{"round 2 nineteen leaks", 12, 31, 460, 460, 1, 20, true},
		{"one leak inside tolerance", 12, 13, 460, 460, 0, 1, true},
		{"one leak masked by other exits", 20, 12, 460, 460, 0, 1, true},
		{"unknown goroutines", 12, 33, 460, 460, 0, 0, true},
		{"descriptors", 12, 12, 460, 469, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := cycleGrowth(tc.g0, tc.g, tc.fd0, tc.fd, tc.nat0, tc.nat)
			if (len(got) > 0) != tc.fail {
				t.Fatalf("failures=%v, want fail=%v", got, tc.fail)
			}
		})
	}
}
