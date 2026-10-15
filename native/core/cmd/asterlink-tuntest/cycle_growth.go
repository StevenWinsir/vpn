package main

import "fmt"

// Keep unrelated scheduler/descriptor tolerances, but never spend them on the
// known per-connect leak. A +1 NAT worker fails even if total goroutines fell.
func cycleGrowth(g0, g, fd0, fd, nat0, nat int) []string {
	var failures []string
	if nat > nat0 {
		failures = append(failures, fmt.Sprintf("TCPNat.loopCheckTimeout growth %d->%d (+%d); allowed growth is zero", nat0, nat, nat-nat0))
	}
	if g-g0 > 20 || fd-fd0 > 8 {
		failures = append(failures, fmt.Sprintf("growth: goroutines %d->%d, fds %d->%d", g0, g, fd0, fd))
	}
	return failures
}
