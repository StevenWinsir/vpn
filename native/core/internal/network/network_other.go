//go:build !darwin

package network

import "syscall"

// Off macOS there is no tunnel and nothing to bind: these exist so the Core's
// logic can be unit-tested on CI hosts.
func bindToInterface(syscall.RawConn, string, int) error { return nil }
func PhysicalDefault() (Interface, error)                { return Interface{}, ErrUnsupported }
func ViaTunnel() string                                  { return "" }
