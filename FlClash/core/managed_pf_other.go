//go:build !darwin

package main

import (
	"context"
	"errors"
	"io"
)

func newManagedEgressGuard(context.Context, string) (io.Closer, error) {
	return nil, errors.New("macOS egress guard is unavailable on this platform")
}
