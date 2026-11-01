package config

import "testing"

// handoff_ticket.md 13.2: TICKETS_ENABLED, default on; anything but a boolean is refused.
func TestTicketsConfiguration(t *testing.T) {
	t.Setenv("TICKETS_ENABLED", "")
	var c Config
	if err := loadTickets(&c); err != nil || !c.TicketsEnabled() {
		t.Fatalf("default: %v %+v", err, c.Tickets)
	}
	t.Setenv("TICKETS_ENABLED", "false")
	if err := loadTickets(&c); err != nil || c.TicketsEnabled() {
		t.Fatalf("off: %v %+v", err, c.Tickets)
	}
	t.Setenv("TICKETS_ENABLED", "true")
	if err := loadTickets(&c); err != nil || !c.TicketsEnabled() {
		t.Fatalf("on: %v %+v", err, c.Tickets)
	}
	t.Setenv("TICKETS_ENABLED", "maybe")
	if err := loadTickets(&c); err == nil {
		t.Fatal("TICKETS_ENABLED=maybe accepted")
	}
	// A configuration built without Load (tests) has tickets on.
	if !(Config{}).TicketsEnabled() {
		t.Fatal("zero configuration")
	}
}
