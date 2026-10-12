package billing

import (
	"errors"
	"math"
)

// Node-reported accounting (handoff_app.md 7.4/7.5). Counters come from the forwarder
// and are cumulative per (grant, epoch); an epoch is one agent process lifetime.

var (
	ErrUsageInvalid  = errors.New("invalid node usage counters")
	ErrUsageStale    = errors.New("node usage epoch is older than the recorded one")
	ErrUsageSequence = errors.New("node usage sequence is not contiguous")
	ErrUsageRegress  = errors.New("node usage counters moved backwards")
)

// UsageCursor is the server's record of the last accepted report for a grant.
type UsageCursor struct {
	Epoch, Sequence  int64
	Upload, Download int64
}

// UsageReport is what the node sent.
type UsageReport struct {
	Epoch, Sequence  int64
	Upload, Download int64
}

type UsageKind int

const (
	UsageNext     UsageKind = iota // contiguous report in the open epoch
	UsageNewEpoch                  // first report of a later epoch (counters restart at 0)
	UsageReplay                    // sequence already accepted: compare with the stored record
)

func (r UsageReport) Valid() bool {
	return r.Epoch > 0 && r.Epoch <= 1<<40 && r.Sequence > 0 && r.Sequence <= 1<<40 &&
		r.Upload >= 0 && r.Download >= 0 && r.Upload <= MaxClientCounter && r.Download <= MaxClientCounter
}

// ApplyUsage classifies a report against the cursor and returns the byte deltas it
// adds. Nothing is clamped or repaired: anything inconsistent is an error the node
// must see, never a silent adjustment.
func ApplyUsage(cur UsageCursor, in UsageReport) (kind UsageKind, up, down int64, err error) {
	if !in.Valid() {
		return 0, 0, 0, ErrUsageInvalid
	}
	switch {
	case in.Epoch < cur.Epoch:
		return 0, 0, 0, ErrUsageStale
	case in.Epoch == cur.Epoch && in.Sequence <= cur.Sequence:
		return UsageReplay, 0, 0, nil
	case in.Epoch == cur.Epoch && in.Sequence != cur.Sequence+1:
		return 0, 0, 0, ErrUsageSequence
	case in.Epoch == cur.Epoch:
		if in.Upload < cur.Upload || in.Download < cur.Download {
			return 0, 0, 0, ErrUsageRegress
		}
		return UsageNext, in.Upload - cur.Upload, in.Download - cur.Download, nil
	case in.Sequence != 1:
		return 0, 0, 0, ErrUsageSequence
	default:
		return UsageNewEpoch, in.Upload, in.Download, nil
	}
}

// Charge returns the integer units for raw bytes at the bound rate, plus the new
// raw-byte total, rejecting overflow.
func Charge(chargedBytes, chargedUnits, up, down, rate int64) (bytes, units int64, err error) {
	if up < 0 || down < 0 || up > math.MaxInt64-down {
		return 0, 0, ErrUsageInvalid
	}
	raw := up + down
	delta, err := ChargeUnits(raw, rate)
	if err != nil || chargedBytes < 0 || chargedUnits < 0 || chargedBytes > math.MaxInt64-raw || chargedUnits > math.MaxInt64-delta {
		return 0, 0, ErrUsageInvalid
	}
	return chargedBytes + raw, delta, nil
}

// MinBudgetBytes: below this a grant is not worth issuing (also the top-up quantum floor).
const MinBudgetBytes int64 = 1 << 20

// BudgetBytes converts available units into the raw-byte allowance a node may spend
// at the bound rate, capped by the per-grant chunk. Zero means "nothing to grant".
func BudgetBytes(availableUnits, rate, chunk int64) int64 {
	if availableUnits <= 0 || rate <= 0 || chunk <= 0 {
		return 0
	}
	return min(availableUnits/rate, chunk)
}

// ReservedUnits is what a grant still holds in the period's shared budget pool.
func ReservedUnits(budget, charged, rate int64, released bool) int64 {
	if released || budget <= charged || rate <= 0 {
		return 0
	}
	return (budget - charged) * rate
}

// AvailableUnits is the unreserved part of a period: limit - used - reserved.
func AvailableUnits(limitBytes, usedUnits, reservedUnits int64) int64 {
	if limitBytes <= 0 || limitBytes > MaxClientCounter {
		return 0
	}
	return max(0, limitBytes*1000-usedUnits-reservedUnits)
}
