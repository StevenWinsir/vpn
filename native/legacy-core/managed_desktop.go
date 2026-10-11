//go:build !(android && cgo)

package main

import (
	"core/managed"
	"net/http"
)

func newManagedTransport() http.RoundTripper { return managed.NewTransport(nil) }
func stopManagedPlatform()                   {}
