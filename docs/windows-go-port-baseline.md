# Go Core on windows/amd64 — baseline measured in round 1

> **Round 2 update.** Every row of the port list below is done and `GOOS=windows GOARCH=amd64 GOAMD64=v1
> CGO_ENABLED=0 go build ./cmd/asterlink-core` now produces a PE32+ x64 file without `fakeengine`; the
> non-darwin placeholder no longer links on Windows (`network_other.go` is `!darwin && !windows`, and
> `TestWindowsDoesNotLinkThePlaceholder` / `--suite core` fail if that regresses). Design and ownership:
> [windows-tun-design.md](windows-tun-design.md). How to run the real TUN test:
> [windows-tun-poc.md](windows-tun-poc.md). What follows is the round-1 baseline, kept for comparison.

Measured on 2026-10-13 from a macOS arm64 host with Go 1.26.6, Mihomo submodule at
`70f0570405c3c2c47bb113b88db95006d239b346`, no Core code changed:

```sh
cd native/core
GOOS=windows GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=0 go build ./cmd/asterlink-core   # FAILS
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./internal/...                      # compiles
```

## Result

The entry point fails with four errors, all in `cmd/asterlink-core/main.go` (`unix.Dup`, `unix.CloseOnExec`,
`unix.Dup2`, `unix.Close`: the stdout/IPC file-descriptor shuffle and POSIX signals).

**Every package under `internal/` already compiles for Windows.** That is the danger, not the good news:
`internal/network/network_other.go` is tagged `!darwin`, so Windows silently receives the CI placeholder
(`bindToInterface` is a no-op, `PhysicalDefault` returns `ErrUnsupported`). Fixing only `main.go` would produce a
Windows binary that builds, starts and has **no outbound-interface binding and no TUN**. Round 2 must change the
tag so non-darwin, non-windows hosts keep the placeholder and Windows gets a real implementation (or a hard
`ErrUnsupported` that the engine turns into a visible failure), and must prove it with a test that fails if the
placeholder is ever linked into a Windows binary.

## Port list for round 2 (from the handoff plan, confirmed against the source)

| Source | What is macOS-specific | Round-2 action |
| --- | --- | --- |
| `cmd/asterlink-core/main.go:12,36-46,80` | `x/sys/unix` Dup/Dup2, POSIX signals | split `_unix.go` / `_windows.go`; private pipe handle, console-control events |
| `internal/network/network_other.go` | placeholder linked on every non-darwin OS | exclude windows from the tag; add `network_windows.go` |
| `internal/network/network.go:151` | competing-VPN detection looks for `utun*` | classify by route ownership / adapter GUID |
| `internal/engine/mihomo.go:71,103` | `runtime.GOOS != "darwin"` rejects TUN | platform adapter; readiness from adapter LUID and routes |
| `internal/engine/sysdns.go` | `networksetup`, mDNSResponder | Windows DNS owner and recovery record |
| `internal/engine/ipv6.go:126-149` | `/sbin/route -reject` | Windows early-refusal or full IPv6 proxying |
| `internal/managed/manager.go:187` | `Platform: "macos"` hard-coded | derive `windows` from the trusted build, not from the client |
| `internal/engine/config.go:59` | runtime template ends at `MATCH,node` | append a final reject so a node without UDP never falls to DIRECT (static finding at `Clash.Meta/tunnel/tunnel.go:685-710`; not reproduced on a machine) |
| `cmd/asterlink-tuntest` | macOS-only harness (`scutil`, `netstat`, `utun`) | Windows harness behind `--allow-network-changes` |

`native/legacy-core` is untouched and stays the home of the protocol/browser-to-ledger regressions.
