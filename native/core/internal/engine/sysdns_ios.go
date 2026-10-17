//go:build ios

package engine

// System DNS on iOS is the tunnel's NEDNSSettings (handoff_app_ios.md 8.2): the Core never
// changes a system setting, so there is nothing to capture or restore. Round 2 decides how
// the extension's own control-plane lookups avoid the tunnel resolver (section 7).

func captureSystemDNS(string) error { return nil }

// RestoreSystemDNS has nothing to restore on iOS. Safe at any time.
func RestoreSystemDNS() error { return nil }
