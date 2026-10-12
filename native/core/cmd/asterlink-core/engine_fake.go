//go:build fakeengine

package main

import (
	"asterlink/core/internal/engine"
	"asterlink/core/internal/managed"
)

// Test-only: see internal/engine/fake.go. Never compiled into a release build.
func newEngine() managed.Engine { return engine.NewFake() }
