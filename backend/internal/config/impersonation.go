package config

import (
	"errors"
	"strconv"
	"time"
)

// Bounds of IMPERSONATION_MINUTES: long enough to look around, short enough that a
// forgotten session does not linger.
const (
	DefaultImpersonationMinutes = 30
	MinImpersonationMinutes     = 5
	MaxImpersonationMinutes     = 120
)

func loadImpersonation(c *Config) error {
	n, err := strconv.Atoi(env("IMPERSONATION_MINUTES", strconv.Itoa(DefaultImpersonationMinutes)))
	if err != nil || n < MinImpersonationMinutes || n > MaxImpersonationMinutes {
		return errors.New("invalid IMPERSONATION_MINUTES (allowed 5-120)")
	}
	c.ImpersonationTTL = time.Duration(n) * time.Minute
	return nil
}

// ImpersonationWindow is the hard lifetime of one impersonation session; the default
// applies when the configuration was built without Load (tests).
func (c Config) ImpersonationWindow() time.Duration {
	if c.ImpersonationTTL < MinImpersonationMinutes*time.Minute {
		return DefaultImpersonationMinutes * time.Minute
	}
	return c.ImpersonationTTL
}
