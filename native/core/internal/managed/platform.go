package managed

import "runtime"

// ClientPlatform is the platform value the login request carries. It comes from the
// build target, never from anything the GUI or the user supplies, so a client cannot
// claim another platform to obtain different entitlements. The server validates the
// value against its own allow-list (backend/internal/api/native.go).
func ClientPlatform() string { return clientPlatformFor(runtime.GOOS) }

func clientPlatformFor(goos string) string {
	switch goos {
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	case "android":
		// GOOS=android also satisfies the "linux" build tag; it must not fall through.
		return "android"
	default:
		// Linux builds exist only for CI; the server accepts "linux" for them.
		return "linux"
	}
}
