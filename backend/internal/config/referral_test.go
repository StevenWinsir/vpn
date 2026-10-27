package config

import (
	"testing"
	"time"
)

// handoff_coupon.md 14.2: REFERRAL_ENABLED (default on), REFERRAL_REWARD_MAX_GIB
// (0-10240, default 0 = no cap) and REFERRAL_POLL_INTERVAL (1s-5m, default 10s).
func TestReferralConfiguration(t *testing.T) {
	for _, key := range []string{"REFERRAL_ENABLED", "REFERRAL_REWARD_MAX_GIB", "REFERRAL_POLL_INTERVAL"} {
		t.Setenv(key, "")
	}
	var c Config
	if err := loadReferral(&c); err != nil || !c.ReferralEnabled() || c.Referral.MaxRewardGiB != 0 || c.ReferralPoll() != 10*time.Second {
		t.Fatalf("defaults: %v %+v", err, c.Referral)
	}
	t.Setenv("REFERRAL_ENABLED", "false")
	t.Setenv("REFERRAL_REWARD_MAX_GIB", "500")
	t.Setenv("REFERRAL_POLL_INTERVAL", "1s")
	if err := loadReferral(&c); err != nil || c.ReferralEnabled() || c.Referral.MaxRewardGiB != 500 || c.ReferralPoll() != time.Second {
		t.Fatalf("set: %v %+v", err, c.Referral)
	}
	for key, bad := range map[string][]string{
		"REFERRAL_ENABLED":        {"maybe"},
		"REFERRAL_REWARD_MAX_GIB": {"-1", "10241", "x"},
		"REFERRAL_POLL_INTERVAL":  {"500ms", "6m", "10"},
	} {
		for _, value := range bad {
			t.Setenv("REFERRAL_ENABLED", "true")
			t.Setenv("REFERRAL_REWARD_MAX_GIB", "0")
			t.Setenv("REFERRAL_POLL_INTERVAL", "10s")
			t.Setenv(key, value)
			if err := loadReferral(&c); err == nil {
				t.Fatalf("%s=%q accepted", key, value)
			}
		}
	}
	// A configuration built without Load (tests) has the programme on.
	if !(Config{}).ReferralEnabled() || (Config{}).ReferralPoll() != 10*time.Second {
		t.Fatal("zero configuration")
	}
}
