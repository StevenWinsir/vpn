package billing

import (
	"math"
	"testing"
)

func TestClientDelta(t *testing.T) {
	previous := ClientCounters{1, 100, 200}
	up, down, units, err := ClientDelta(previous, ClientCounters{2, 150, 270}, 300000, 100, 200)
	if err != nil || up != 50 || down != 70 || units != 120000 {
		t.Fatalf("bad delta %d %d %d %v", up, down, units, err)
	}
	for _, in := range []ClientCounters{{0, 0, 0}, {1, 100, 200}, {3, 150, 200}, {2, -1, 200}, {2, 99, 200}, {2, 100, 199}, {2, MaxClientCounter + 1, 200}, {1<<40 + 1, 100, 200}} {
		if _, _, _, e := ClientDelta(previous, in, 0, 0, 0); e == nil {
			t.Fatalf("accepted invalid input %+v", in)
		}
	}
	for _, totals := range [][3]int64{{math.MaxInt64, 0, 0}, {0, math.MaxInt64, 0}, {0, 0, math.MaxInt64}, {-1, 0, 0}} {
		if _, _, _, e := ClientDelta(ClientCounters{}, ClientCounters{1, 1, 1}, totals[0], totals[1], totals[2]); e == nil {
			t.Fatal("accepted overflow")
		}
	}
}
