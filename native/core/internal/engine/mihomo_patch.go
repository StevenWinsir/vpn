package engine

import "github.com/metacubex/mihomo/listener/sing_tun"

// A bare go build must fail instead of silently shipping the old leaking core.
// Prepare a fresh checkout with: python3 scripts/prepare_mihomo.py (repo root).
const mihomoContextPatchVersion = sing_tun.AsterLinkContextPatchVersion
