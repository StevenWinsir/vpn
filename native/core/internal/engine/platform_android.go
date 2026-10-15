//go:build android

package engine

import (
	"context"
	"fmt"

	"asterlink/core/internal/managed"
)

// Android TUN (handoff_app_android.md 6, 7.1). The device is not created by sing-tun: the
// app's VpnService.Builder establishes it and hands the Core a file descriptor, which the
// template passes to Mihomo as tun.file-descriptor with auto-route and
// auto-detect-interface off. That path is round 2. Until it exists this build has no
// tunnel and says so: connect fails with tun_unavailable instead of linking the macOS
// utun/route implementation (which GOOS=android would otherwise match through its
// implicit "linux" tag) or an empty stand-in that would report a tunnel it never made.
func platformSupported() bool { return false }

func preflightPlatform(context.Context) error {
	return fmt.Errorf("%w: the Android VpnService tunnel is not part of this build yet", managed.ErrTunUnavailable)
}

func tunnelDevices() map[string]bool { return map[string]bool{} }

// tunReady is never true without the fd path; Start cannot be reached anyway because
// platformSupported is false.
func tunReady(map[string]bool) bool { return false }
