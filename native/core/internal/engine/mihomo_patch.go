package engine

import (
	"github.com/metacubex/mihomo/listener/sing_tun"
	"github.com/metacubex/mihomo/tunnel/statistic"
)

// A bare go build must fail instead of silently shipping an unpatched core: the old
// leaking TUN listener, or the old metering that billed bytes a REJECT outbound
// discarded (handoff_fix.md F3). Prepare a fresh checkout with:
// python3 scripts/prepare_mihomo.py (repo root).
const (
	mihomoContextPatchVersion = sing_tun.AsterLinkContextPatchVersion
	mihomoRejectPatchVersion  = statistic.AsterLinkRejectPatchVersion
)
