package managed

import (
	"context"
	"errors"
)

// ErrTunUnavailable is returned by the stage-2 engine: authentication, catalog and
// config handling exist, but no tunnel is created until stage 3.
var ErrTunUnavailable = errors.New("tun unavailable in this build")

// Engine is the adapter between the managed state machine and the embedded Mihomo.
// Start is called only after every authorization gate has passed; it receives the
// single validated Profile and nothing else.
type Engine interface {
	Start(ctx context.Context, profile Profile) error
	Stop() error
}

type unavailableEngine struct{}

func (unavailableEngine) Start(context.Context, Profile) error { return ErrTunUnavailable }
func (unavailableEngine) Stop() error                          { return nil }

// UnavailableEngine is the production engine for this stage.
func UnavailableEngine() Engine { return unavailableEngine{} }
