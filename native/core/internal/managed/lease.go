package managed

import "time"

// lease tracks how long the server's authorization stays valid on THIS machine.
// The remaining length is computed from the server's own clock (authorization_expires_at
// minus server_time) and counted from the moment the request was sent, so network
// latency consumes the lease. Expiry is the earlier of a monotonic and a wall-clock
// deadline: the monotonic one resists clock changes, the wall one still advances while
// the machine sleeps (Go's monotonic clock on macOS pauses during sleep). A clock
// rolled back can therefore never extend authorization (handoff_app.md section 5.2).
type lease struct {
	monotonic time.Time
	wall      time.Time
	valid     bool
}

func newLease(requestStart, serverTime time.Time, authorizationExpiresAt *time.Time, leaseSeconds int) lease {
	if authorizationExpiresAt == nil || leaseSeconds <= 0 {
		return lease{}
	}
	remaining := authorizationExpiresAt.Sub(serverTime)
	if limit := time.Duration(leaseSeconds) * time.Second; remaining > limit {
		remaining = limit
	}
	if remaining <= 0 {
		return lease{}
	}
	return lease{monotonic: requestStart.Add(remaining), wall: requestStart.Round(0).Add(remaining), valid: true}
}

func (l lease) active(now time.Time) bool {
	return l.valid && now.Before(l.monotonic) && now.Round(0).Before(l.wall)
}

// remaining is the conservative time left (0 when expired).
func (l lease) remaining(now time.Time) time.Duration {
	if !l.active(now) {
		return 0
	}
	mono := l.monotonic.Sub(now)
	if wall := l.wall.Sub(now.Round(0)); wall < mono {
		return wall
	}
	return mono
}
