package leakcheck

import "testing"

func TestCountTCPNAT(t *testing.T) {
	for _, tc := range []struct {
		name, profile string
		want          int
		bad           bool
	}{
		{"none", "goroutine profile: total 1\n1 @ 0x123\n# other.worker\n", 0, false},
		{"first block", "goroutine profile: total 19\n19 @ 0x123\n# (*TCPNat).loopCheckTimeout+0x1\n", 19, false},
		{"multiple stacks", "goroutine profile: total 20\n2 @ 0x123\n# other.worker\n\n17 @ 0x456\n# (*TCPNat).loopCheckTimeout+0x1\n\n1 @ 0x789\n# (*TCPNat).loopCheckTimeout+0x2\n", 18, false},
		{"missing profile", "", 0, true},
		{"missing count", "goroutine profile: total 1\n# (*TCPNat).loopCheckTimeout\n", 0, true},
		{"invalid count", "goroutine profile: total 1\nnope @ 0x123\n# (*TCPNat).loopCheckTimeout\n", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CountTCPNAT(tc.profile)
			if (err != nil) != tc.bad || got != tc.want {
				t.Fatalf("CountTCPNAT = %d, %v; want %d, bad=%v", got, err, tc.want, tc.bad)
			}
		})
	}
}

func TestLiveProfile(t *testing.T) {
	if _, err := TCPNAT(); err != nil {
		t.Fatal(err)
	}
}
