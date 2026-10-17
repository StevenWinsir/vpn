//go:build ios

package engine

// System DNS on iOS is the tunnel's NEDNSSettings (handoff_app_ios.md 8.2): the Core never
// changes a system setting, so there is nothing to capture or restore. The extension's own
// control-plane lookups avoid the tunnel resolver by resolving over the physical interface
// with the network's reported servers or fixed public ones (network_ios.go).

func captureSystemDNS(string) error { return nil }

// RestoreSystemDNS has nothing to restore on iOS. Safe at any time.
func RestoreSystemDNS() error { return nil }
