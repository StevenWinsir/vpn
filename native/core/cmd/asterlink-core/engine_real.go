//go:build !fakeengine

package main

import (
	"asterlink/core/internal/engine"
	"asterlink/core/internal/managed"
)

func newEngine() managed.Engine { return engine.New() }
