//go:build !darwin && !windows && !android

package network

import "syscall"

// Off macOS, Windows and Android there is no tunnel and nothing to bind: these exist so
// the Core's logic can be unit-tested on CI hosts. Windows and Android must never link
// this file: a binary built on it would have no outbound-interface binding and no TUN,
// so the build tag excludes it, and network_windows_test.go and the GOOS=android file
// check in native/core/mobile fail if that ever changes. GOOS=android satisfies the
// "linux" tag implicitly, which is why it has to be named here.
func bindToInterface(syscall.RawConn, string, int) error { return nil }
func PhysicalDefault() (Interface, error)                { return Interface{}, ErrUnsupported }
func ViaTunnel() string                                  { return "" }
func otherTunnels() []string                             { return nil }

// platformSignature: the signature is read from the interface itself (network.go).
func platformSignature() (string, bool) { return "", false }

// platformResolvers: the system resolver configuration (/etc/resolv.conf) applies.
func platformResolvers() []string { return nil }
