package config

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// Referral programme settings (handoff_coupon.md 14.2). The switch is stored inverted so
// a configuration built without Load (tests) has the programme on, as the default does.
const (
	DefaultReferralPollSeconds = 10
	MaxReferralRewardGiB       = 10240
)

type Referral struct {
	// Disabled is REFERRAL_ENABLED=false: codes entered at registration are ignored and
	// the reward worker pauses; first purchases are still recorded.
	Disabled bool
	// MaxRewardGiB caps one reward (REFERRAL_REWARD_MAX_GIB); 0 means no cap.
	MaxRewardGiB int
	// PollInterval is how often the reward worker looks for work (REFERRAL_POLL_INTERVAL).
	PollInterval time.Duration
}

func loadReferral(c *Config) error {
	enabled, err := strconv.ParseBool(env("REFERRAL_ENABLED", "true"))
	if err != nil {
		return errors.New("invalid REFERRAL_ENABLED")
	}
	c.Referral.Disabled = !enabled
	n, err := strconv.Atoi(env("REFERRAL_REWARD_MAX_GIB", "0"))
	if err != nil || n < 0 || n > MaxReferralRewardGiB {
		return errors.New("invalid REFERRAL_REWARD_MAX_GIB (allowed 0-10240, 0 = no cap)")
	}
	c.Referral.MaxRewardGiB = n
	raw := strings.TrimSpace(env("REFERRAL_POLL_INTERVAL", strconv.Itoa(DefaultReferralPollSeconds)+"s"))
	d, err := time.ParseDuration(raw)
	if err != nil || d < time.Second || d > 5*time.Minute {
		return errors.New("invalid REFERRAL_POLL_INTERVAL (allowed 1s-5m)")
	}
	c.Referral.PollInterval = d
	return nil
}

// ReferralEnabled reports whether the referral programme is on.
func (c Config) ReferralEnabled() bool { return !c.Referral.Disabled }

// ReferralPoll is the reward worker's interval; the default applies when the
// configuration was built without Load.
func (c Config) ReferralPoll() time.Duration {
	if c.Referral.PollInterval < time.Second {
		return DefaultReferralPollSeconds * time.Second
	}
	return c.Referral.PollInterval
}
