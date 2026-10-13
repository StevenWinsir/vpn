//go:build !darwin && !windows

package network

import "syscall"

// Off macOS and Windows there is no tunnel and nothing to bind: these exist so the
// Core's logic can be unit-tested on CI hosts. Windows must never link this file: a
// Windows binary built on it would have no outbound-interface binding and no TUN, so
// the build tag excludes it and network_windows_test.go fails if that ever changes.
func bindToInterface(syscall.RawConn, string, int) error { return nil }
func PhysicalDefault() (Interface, error)                { return Interface{}, ErrUnsupported }
func ViaTunnel() string                                  { return "" }
func otherTunnels() []string                             { return nil }
