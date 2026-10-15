// Package leakcheck contains non-privileged acceptance diagnostics, not a leak
// allowance. Even one surviving TCP NAT timeout worker is a regression.
package leakcheck

import (
	"bytes"
	"fmt"
	"runtime/pprof"
	"strconv"
	"strings"
)

const TCPNATFrame = "TCPNat).loopCheckTimeout"

// CountTCPNAT reads pprof debug=1, whose blocks contain an aggregated count.
// Counting matching lines instead would mistake 20 identical workers for one.
func CountTCPNAT(profile string) (int, error) {
	if !strings.HasPrefix(profile, "goroutine profile: total ") {
		return 0, fmt.Errorf("missing goroutine profile header")
	}
	total := 0
	for _, block := range strings.Split(profile, "\n\n") {
		if !strings.Contains(block, TCPNATFrame) {
			continue
		}
		found := false
		for _, line := range strings.Split(block, "\n") {
			if count, _, ok := strings.Cut(line, " @ "); ok {
				n, err := strconv.Atoi(count)
				if err != nil || n <= 0 || found {
					return 0, fmt.Errorf("invalid TCP NAT profile count")
				}
				total += n
				found = true
			}
		}
		if !found {
			return 0, fmt.Errorf("TCP NAT stack has no count")
		}
	}
	return total, nil
}

func TCPNAT() (int, error) {
	var profile bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&profile, 1); err != nil {
		return 0, err
	}
	return CountTCPNAT(profile.String())
}
