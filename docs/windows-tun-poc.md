# Windows real-TUN proof of concept (round 2) — how to run it

`asterlink-tuntest.exe` drives the **production** engine package (`native/core/internal/engine`), so the
template that ships is the template that is tested. It is a test tool: it is never part of an installer and
refuses to run without `--allow-network-changes`, without an elevated token, or while another VPN is up.

> **Only on a machine you control and can recover.** It creates a Wintun adapter, split default routes and
> Windows Filtering Platform filters for a few minutes. Create a restore point first, keep a second way
> online (phone hotspot), and **disconnect any company VPN on that machine** — the harness detects an
> active VPN adapter and stops with `blocked`, because stacking a second tunnel on top of a VPN would test
> nothing and could cut the VPN off.

## What it proves (and the case ids in the report)

| Case | Expected |
| --- | --- |
| `T-PRE` | elevated, no other VPN up, a physical default route exists |
| `T01a` / `T02` | adapter and routes appear; a real handshake and an HTTP probe pass through the node |
| `T01` | a request from an ordinary client (no proxy configured) leaves from a different address |
| `T01-noproxy` | the per-user WinINET proxy setting is untouched while connected |
| `T03-dns-adapter` | the TUN adapter's DNS server is `198.18.0.2` |
| `T03-dns-system` | a lookup by an ordinary program (Windows DNS Client service) is answered by the tunnel resolver (fake-ip) |
| `T03-dns-any53` | port-53 traffic aimed at `9.9.9.9` is hijacked or blocked, never answered by that resolver |
| `T02-udp` | an SNTP request (UDP/123) gets a reply through the node |
| `T04-ipv6`, `T04-ipv6-fallback` | IPv6 is proxied, or refused in under a second so browsers fall back to IPv4 |
| `T05` | with `--api-host`, the Core's own dial (bound to the physical adapter) still reaches the API **by name** while the tunnel is up |
| `T05-loop` | an idle tunnel does not feed itself |
| `T07` | after stop: no adapter, same IPv4/IPv6 routes, same per-adapter DNS, same proxy setting |
| `T02-udp-reject` (`--udp-reject-test`) | a node declared `udp:false` makes UDP fail; the SNTP reply must **not** arrive (a reply = UDP left directly) |
| `T09-kill` (`--kill-test`) | a connected Core is hard-killed (`TerminateProcess`); adapter, routes, DNS and filters are gone and the system resolver answers for real again |
| `T-CYCLES` | N connect/disconnect cycles leave no accumulation (handles, goroutines, adapters) |

Probes that stand in for applications run from a **copy** of the exe at another path, because sing-tun's
firewall exemption is keyed on the executable path (see `docs/windows-tun-design.md`, section 7).

## Get the binary

Either the artifact `windows-core-poc` of the `Windows client CI` run for the round-2 pull request
(`asterlink-tuntest.exe`, built by GitHub on Windows Server 2025; the run prints its SHA-256), or build it on
the Mac:

```bash
python3 scripts/test-native-windows.py --suite core --out artifacts/native-windows/core
```

The file is unsigned, so SmartScreen may warn ("More info" → "Run anyway") and an antivirus may look at it
because the Wintun driver library is loaded from memory. Do not disable Defender; if it blocks or
quarantines the file, stop and send the message — that is a finding for the release round.

## Run (elevated PowerShell)

The node comes from the environment, never from the command line or a file:

```powershell
$env:ASTERLINK_POC_SS_SERVER   = '<node host>'
$env:ASTERLINK_POC_SS_PORT     = '<port>'
$env:ASTERLINK_POC_SS_PASSWORD = '<password>'
# optional: $env:ASTERLINK_POC_SS_CIPHER = 'aes-256-gcm'  (default)

.\asterlink-tuntest.exe --allow-network-changes `
  --output "$env:USERPROFILE\Desktop\aster-tun-1" `
  --cycles 3 --api-host demo.hyshentou.cn:443 --udp-reject-test --kill-test
```

Typical duration is 4–8 minutes. The report is `aster-tun-1\tun-report.json`
(machine facts, one line per case, node host only as a 4-byte hash). Exit codes: `0` all pass, `1` a case
failed, `3` blocked (a prerequisite is missing, nothing was proven).

## If something goes wrong

The design leaves nothing persistent behind, so closing the window is the first recovery step:

```powershell
taskkill /F /IM asterlink-tuntest.exe
Get-NetAdapter | Where-Object { $_.Name -eq 'AsterLink' }   # should print nothing within a few seconds
```

Only if the internet is still gone: disable and re-enable the physical adapter, `ipconfig /flushdns`, then
Device Manager → *Network adapters* → uninstall the `Wintun Userspace Tunnel` named `AsterLink`, and as a last
resort restart Windows or use the restore point. Do **not** run a global `netsh winsock reset` / `netsh int
ip reset`; report what you saw instead, because a leftover is exactly what this test is meant to find.
