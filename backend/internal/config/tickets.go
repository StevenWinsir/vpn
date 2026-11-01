package config

import (
	"errors"
	"strconv"
)

// Support tickets (handoff_ticket.md 13.2). The switch is stored inverted so a
// configuration built without Load (tests) has tickets on, as the default does.
type Tickets struct {
	// Disabled is TICKETS_ENABLED=false: users can no longer submit, reply or close, but
	// still read their tickets; the console keeps working and expiry keeps running.
	Disabled bool
}

func loadTickets(c *Config) error {
	enabled, err := strconv.ParseBool(env("TICKETS_ENABLED", "true"))
	if err != nil {
		return errors.New("invalid TICKETS_ENABLED")
	}
	c.Tickets.Disabled = !enabled
	return nil
}

// TicketsEnabled reports whether users may submit and answer tickets.
func (c Config) TicketsEnabled() bool { return !c.Tickets.Disabled }
