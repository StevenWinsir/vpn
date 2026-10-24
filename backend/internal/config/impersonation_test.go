package config

import (
	"testing"
	"time"
)

// IMPERSONATION_MINUTES is the hard life of an impersonation session: 5 to 120, default 30.
func TestImpersonationConfiguration(t *testing.T) {
	paymentBase(t, false)
	t.Setenv("IMPERSONATION_MINUTES", "")
	c, err := Load()
	if err != nil || c.ImpersonationTTL != 30*time.Minute || c.ImpersonationWindow() != 30*time.Minute {
		t.Fatalf("default: %v %v", err, c.ImpersonationTTL)
	}
	for _, ok := range []string{"5", "45", "120"} {
		t.Setenv("IMPERSONATION_MINUTES", ok)
		if _, err = Load(); err != nil {
			t.Fatalf("%s rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"4", "121", "0", "-5", "x", "30m", "1.5"} {
		t.Setenv("IMPERSONATION_MINUTES", bad)
		if _, err = Load(); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	if (Config{}).ImpersonationWindow() != 30*time.Minute {
		t.Fatal("a configuration built without Load needs the default")
	}
}
