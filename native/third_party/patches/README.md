# Mihomo lifecycle patch

The submodule **does not move**: `70f0570405c3c2c47bb113b88db95006d239b346`
from `chen08209/Clash.Meta` (`FlClash`), with `github.com/metacubex/sing-tun v0.4.22`.
`manifest.json` locks the base commit, patch SHA-256 and every original/resulting
file SHA-256. The patch and its regression tests are corresponding source retained
in this repository, not an unrecorded edit to a developer's Go module cache.

## Prepare and verify

At the repository root, after initializing the submodule:

```sh
python3 scripts/prepare_mihomo.py
python3 scripts/prepare_mihomo.py --check
python3 scripts/test-mihomo-patches.py
cd native/core
go test ./...
```

The three client build/test entry points prepare the patch before invoking Go.
`ci.yml` also prepares it before raw Go commands. Preparation is idempotent and
validates exact source bytes (canonical LF, including Windows CRLF checkouts),
refuses a different pin, unknown edits or a partially applied patch, and never
changes a Git index, global Git config, another worktree or the Go module cache.
It first applies and verifies the patch in a disposable directory, then publishes
the verified files. The expected submodule working-tree modification is normal;
**do not commit a different gitlink** to make that status disappear.

`internal/engine/mihomo_patch.go` requires the patch's compile-time marker. A bare
`go build` on an unprepared checkout fails, rather than silently shipping the old
leak. Run the preparation command first; Go does not execute build hooks itself.

Android records the patch identity in `core-build.json` and the TUN report. Its
Python build automatically rebuilds an AAR missing that identity; Gradle also
refuses such an AAR, even when invoked directly. macOS records the patch identity
in the build manifest and `SOURCE.txt`; Windows records it in `SOURCE.txt`.

## Ownership and failure paths

The original listener passed `context.TODO()` into the stack. The system stack's
`NewNat` starts `(*TCPNat).loopCheckTimeout`, whose only exit is `ctx.Done()`.
Closing the stack's sockets alone does not stop that worker or release its map.

The listener now creates its own `context.WithCancel(context.Background())`
after early validation, retains its `CancelFunc`, and cancels **before** other
`Close` operations. It is deliberately not derived from the connect request's
timeout: a successful VPN must outlive that request. Constructor failures after
allocation close the captured listener, including explicit `return nil, err`
paths that overwrite the named return value. The stack is assigned to the
listener before `Start`, so partially started resources are also closed.

Tests use actual sing-tun NAT workers for twenty create/close cycles and verify
that the worker appears while alive and disappears on close. Cancellation is
also required when a closer returns an error. A Linux-only unprivileged test
exercises the constructor's explicit-nil failure path; it is skipped on hosts
without auto-redirect. No test in this script needs a device, driver or routes.
The real Android VpnService acceptance run tests the complete production path.

Both Android `cycles()` and desktop `cycleCheck` count aggregated pprof NAT
workers **before the first cycle** and after the last stop. Any positive growth
fails independently of unrelated scheduler tolerances; it is never subtracted
from total growth. Incomplete runs and fewer than two cycles fail as well.

## Source archives, upgrades and rollback

macOS/Windows GPL source delivery retains two archives: the unchanged upstream
base, and this repository (including this patch, manifest, tests and preparer).
Unpack the Mihomo archive at `native/third_party/Clash.Meta`, then run the prepare
command above. It supports source archives without `.git` by checking original
and patched file hashes. Release cleanliness checks exclude only this verified
patch; unknown submodule changes are rejected instead of hidden.

To upgrade Mihomo, review whether upstream owns/cancels the listener context,
regenerate the patch and manifest against the new explicit base, update the
platform locks together, and repeat native/Core/Android/Windows CI regression.
Do not loosen the zero-growth acceptance gate or apply this patch fuzzily to a
different revision. Reverting this change restores the original pin unchanged
but also reintroduces the leak; ship a previous client only as an explicit
rollback. Already installed applications are not modified: all three clients
must be rebuilt and redeployed to receive the fix.

The managed VLESS UDP/XUDP mismatch is a separate forwarder issue and is not
changed by this patch.
