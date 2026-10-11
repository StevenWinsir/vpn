package managed

import (
	"context"
	"strings"
	"testing"
)

func TestMeterRejectsUnrequestedBillingBindingChange(t *testing.T) {
	for _, field := range []string{"rate", "profile"} {
		for _, denied := range []bool{false, true} {
			t.Run(field+map[bool]string{false: "/allowed", true: "/denied"}[denied], func(t *testing.T) {
				f := newFixture(t)
				_, before, _, _ := f.meter.View()
				f.up.Store(100)
				stops := f.stops.Load()
				f.mu.Lock()
				if field == "rate" {
					f.status.RatePermille = 500
				} else {
					f.status.ProfileVersion = strings.Repeat("b", 64)
				}
				if denied {
					f.status.CanConnect, f.status.Reason = false, "profile_changed"
				}
				f.mu.Unlock()
				err := f.meter.Flush(context.Background())
				if err == nil || PublicError(err) != "invalid_server_response" {
					t.Fatalf("unrequested %s change accepted: %v", field, err)
				}
				view, confirmed, _, _ := f.meter.View()
				if !view.Pending || confirmed.LastSequence != before.LastSequence || confirmed.RatePermille != before.RatePermille || confirmed.ProfileVersion != before.ProfileVersion || f.meter.Status().CanConnect || f.stops.Load() <= stops {
					t.Fatal("invalid acknowledgement advanced the ledger, discarded pending traffic, or kept the proxy open")
				}
				f.mu.Lock()
				f.status.RatePermille, f.status.ProfileVersion = before.RatePermille, before.ProfileVersion
				f.status.CanConnect, f.status.Reason = true, ""
				f.mu.Unlock()
				if err := f.meter.Flush(context.Background()); err != nil {
					t.Fatal(err)
				}
				f.mu.Lock()
				defer f.mu.Unlock()
				last := len(f.reports) - 1
				if f.reports[last] != f.reports[last-1] || f.status.UploadBytes != 100 || f.status.RemainingBytes != 900 {
					t.Fatal("binding recovery did not retry the exact pending batch once")
				}
			})
		}
	}
}
