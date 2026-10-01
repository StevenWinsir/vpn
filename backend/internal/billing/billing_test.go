package billing

import (
	"math"
	"testing"
	"time"
	"vpn/backend/internal/model"
)

func TestChargeUnits(t *testing.T) {
	for _, tc := range []struct{ raw, rate, want int64 }{{1024, 1000, 1024000}, {1024, 500, 512000}, {1, 500, 500}, {0, 1000, 0}} {
		got, err := ChargeUnits(tc.raw, tc.rate)
		if err != nil || got != tc.want {
			t.Fatalf("charge(%d,%d)=%d,%v", tc.raw, tc.rate, got, err)
		}
	}
	for _, tc := range [][2]int64{{-1, 500}, {1, 0}, {math.MaxInt64, 1000}, {1, 10001}} {
		if _, err := ChargeUnits(tc[0], tc[1]); err == nil {
			t.Fatal("invalid counter accepted")
		}
	}
	a, _ := ChargeUnits(1, 500)
	b, _ := ChargeUnits(1, 500)
	both, _ := ChargeUnits(2, 500)
	if a+b != both {
		t.Fatal("fractional traffic must not be lost")
	}
}
func TestEligibility(t *testing.T) {
	now := time.Now()
	s := model.Subscription{StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), TrafficLimitBytes: 10}
	if !Eligible(&s, now, false) {
		t.Fatal("valid plan denied")
	}
	s.ExpiresAt = now
	if Eligible(&s, now, false) {
		t.Fatal("expired plan accepted")
	}
	s.ExpiresAt = now.Add(time.Hour)
	s.UsedUnits = 10000
	if Eligible(&s, now, false) {
		t.Fatal("quota exhausted accepted")
	}
	s.UsedUnits = 0
	s.IsTest = true
	if Eligible(&s, now, false) {
		t.Fatal("test entitlement accepted in production")
	}
	if !Eligible(&s, now, true) {
		t.Fatal("development test entitlement denied")
	}
	if Eligible(nil, now, true) {
		t.Fatal("nil entitlement accepted")
	}
}
