# Windows TUN, DNS, IPv6 and recovery design (round 2)

Status: **implemented and cross-built; not yet run on a real Windows machine.** Every behaviour below that
needs the OS (adapter creation, split routes, WFP filters, DNS Client) is marked *to be proven by the PoC*
and maps to a case in `docs/windows-tun-poc.md`. What is proven without Windows: the code selection per
platform, the generated Mihomo document, the classification/decision logic, the IPC handle protocol logic
(native tests in CI on Windows Server 2025) and the Wintun binary identity.

## 1. One owner per system resource

The handoff plan (7.2) requires a single owner for DNS and routes. After reading the pinned
`sing-tun v0.4.22` (`tun_windows.go`) the owners are:

| Resource | Created by | Removed by | If the Core is killed | Proven by |
| --- | --- | --- | --- | --- |
| Wintun adapter `AsterLink` | sing-tun (`wintun.CreateAdapter`) | sing-tun on `Close` | Wintun 0.14 adapters are removed when the creating process's handles close *(to be proven)* | T09-kill |
| IPv4/IPv6 addresses, split routes `0.0.0.0/1`, `128.0.0.0/1`, `::/1`… (metric 0) | sing-tun, on the adapter's LUID | go with the adapter | go with the adapter | T07, T09-kill |
| DNS server `198.18.0.2` on the **adapter** | sing-tun (`SetDNS`, next address after the TUN's own) | goes with the adapter | goes with the adapter | T03-dns-adapter |
| Port-53 block on every *other* interface and, without a TUN IPv6 address, an IPv6 connect block | sing-tun, `strict-route`, WFP **dynamic** session (`FWPM_SESSION_FLAG_DYNAMIC`) | session close | the OS deletes a dynamic session's filters when the engine handle dies with the process *(to be proven)* | T03-dns-system, T04-ipv6-fallback, T09-kill |
| Control-plane resolver list | the Core, in memory only (`network.Default.SetResolvers`) | the Core | nothing persisted | unit tests |
| Anything else | nobody | – | – | – |

Consequences:

* **No recovery record, no `netsh`, no registry writes.** macOS needs `dns-override.json` because
  `networksetup` changes persist; on Windows the Core changes nothing persistent, so there is nothing to
  restore and nothing that a kill can leave behind, *provided the two "to be proven" rows hold*. If the PoC
  shows a leftover adapter or filter after `TerminateProcess`, round 3 adds the ProgramData ownership
  record (handoff 7.3) and the Service removes the leftover; the engine does not grow a second owner.
* The Core never touches WinINET/WinHTTP proxy settings. The harness records the per-user proxy value before
  and after (`T01-noproxy`).
* `RestoreSystemDNS` (called at start-up, on signals and by `--recover-network`) only forgets the
  in-memory bootstrap resolvers on Windows.

## 2. Wintun

* sing-tun embeds `internal/wintun/amd64/wintun.dll` and loads it **from memory** (`memmod.LoadLibrary`).
  No file is installed next to the Core and there is no DLL search order to attack (PATH, current
  directory, user-writable folders). The Core therefore ships as one executable in round 2.
* The embedded file is byte-identical to the official `wintun-0.14.1.zip` (`bin/amd64/wintun.dll`,
  SHA-256 `e5da8447…dafce`); the ZIP's own hash equals the one published on wintun.net. Both are locked in
  `apps/windows/toolchain.lock.json`; `scripts/test-native-windows.py --suite core` fails if the embedded
  file ever changes (for example after a sing-tun upgrade), and CI runs `Get-AuthenticodeSignature` on it.
* **Open for round 8:** (a) the Wintun license forbids "extracting from or modifying" the DLL and allows
  redistribution alongside software that uses it only through the permitted API; legal review must decide
  whether the in-memory embedding is acceptable for a commercial installer. (b) In-memory (reflective)
  DLL loading is a pattern some anti-malware engines flag; any Defender detection seen in testing is
  recorded, never "fixed" by disabling protection. Shipping the unmodified DLL from the protected install
  directory would need a small patch to the loader in the pinned dependency.
* Adapter creation needs an elevated token (SYSTEM for the Service). `Preflight` checks it first and fails
  with `tun_unavailable` instead of timing out. If creation still fails, Mihomo only *logs*
  `Start TUN listening error: …`; the engine subscribes to that log during `Start` and returns the
  system's reason (for example "Access is denied.") inside `tunnel_failed`.

## 3. Stack and the generated document

The fixed template is shared with macOS (`engine.BuildConfig`); Windows adds exactly two TUN keys:
`strict-route: true` and `device: AsterLink`. Both documents end with `MATCH,node` **then `MATCH,REJECT`**:
the pinned `tunnel.match` skips a UDP-incapable adapter and, with no rule left, answers `DIRECT`.
`TestUDPWithoutSupportIsRejectedNeverDirect` walks the pinned core's own parsed rules/adapters and also
asserts that the old single-rule list really did fall to `DIRECT` for UDP, so the guard cannot be
deleted silently. `stack: system` is kept; a change needs a recorded decision after the PoC.

## 4. Physical egress (control plane never enters its own tunnel)

* `bindToInterface` sets `IP_UNICAST_IF` / `IPV6_UNICAST_IF` (both option 31). The IPv4 option takes the
  interface index in **network byte order**, the IPv6 option in host order. Loopback targets are not bound.
* `PhysicalDefault` enumerates adapters with `GetAdaptersAddresses(INCLUDE_GATEWAYS)` and picks the up
  adapter with an IPv4 default gateway and the lowest interface metric, excluding the Core's own
  adapter and other VPN adapters (`network.PickPhysical`, unit-tested with a company-VPN fixture). Split
  default routes installed by the TUN do not hide it because the original gateway entry stays on the
  physical adapter.
* `ViaTunnel` asks `GetBestInterfaceEx` for 1.1.1.1 and 8.8.8.8 and reports a tunnel-class adapter. It
  is the readiness signal after `Start` (`OwnTunnelUp() && ViaTunnel() == AsterLink`: the adapter exists
  **and** the routing table already sends public traffic through it) and the competing-VPN signal before.
* Bootstrap DNS: before the tunnel exists the physical adapter's own DNS servers (global addresses only,
  at most four, public fallback `1.1.1.1`) are recorded. Go's `PreferGo` resolver is honoured on Windows
  when set explicitly (`net.conf.mustUseGoResolver`), and the resolver's `Dial` hook sends the query
  to those servers on the bound interface; the WFP block exempts only this process, which is exactly the
  Core. The *system* resolver (`GetAddrInfoW` → DNS Client service) would instead be pointed into the
  tunnel and return a fake-ip, so the Core must never use it for its own traffic.

## 5. Competing VPN classification (`network.Adapter.IsOtherVPN`)

An adapter is another VPN only if it is **up** and is a PPP/tunnel-type adapter holding a default route, or
its driver description matches a known VPN driver (Wintun, WireGuard, TAP-Windows, OpenVPN, Cisco
AnyConnect, GlobalProtect, Fortinet, Pulse, Zscaler, Tailscale, ZeroTier, Clash/Mihomo, …). Hyper-V/WSL/Docker
switches, VMware host-only, Bluetooth PAN and Teredo/ISATAP transition tunnels are not conflicts
(handoff 7.2). A client that is installed but disconnected owns no routes and is ignored. `ViaTunnel`
catches any other split-default VPN that the name list misses. The Core's own adapter is never a conflict.

## 6. IPv6

macOS keeps IPv6 in the TUN and refuses it with more-specific `-reject` routes. Windows has no reject
route. The decision is therefore taken **before** the TUN is created (the node probe already runs then):

* node carries IPv6 → the TUN keeps its IPv6 address, IPv6 is proxied (`IPv6Tunnel`);
* node is IPv4-only → the Core removes the TUN's IPv6 address (`prepareTunConf`). With `strict-route`
  and no TUN IPv6 address, sing-tun adds a WFP filter that blocks IPv6 connects of every process but the
  Core, so an application gets an immediate error and Happy Eyeballs uses IPv4 (`IPv6Rejected`).

Nothing leaves through the physical interface in either mode. The system IPv6 stack is never switched
off. *To be proven:* `T04-ipv6-fallback` (a refusal in under 1 s from a **non-exempt** process).

## 7. Why the harness runs probes in a copied executable

sing-tun's permit filter matches `FWPM_CONDITION_ALE_APP_ID`, which is the **executable path**, not the
process. A child started from the harness's own path would be exempt too. `asterlink-tuntest` therefore
copies itself to a temporary `probe-app.exe` and runs every application-like probe (system DNS lookup,
port-53 aimed at 9.9.9.9, IPv6 dial, SNTP) from that copy. The same fact matters for the product: **only
the Core's executable may sit at the exempt path** (round 3: the protected install directory), and a Core
that is also used as a probe tool would weaken the rules.

## 8. IPC channel (private handles)

`asterlink-core.exe` on Windows reads `ASTERLINK_IPC_IN` / `ASTERLINK_IPC_OUT` (decimal handle values of
two inherited anonymous pipes), checks that each is a pipe (`GetFileType`), makes them non-inheritable,
removes the variables and diverts stdout (`SetStdHandle`, `os.Stdout`, and the logrus logger Mihomo
captured at package init) to stderr. Without the variables it exits with code 1 instead of falling back to
stdio. A handle value cannot be re-pointed the way `dup2` re-points descriptor 1, and Mihomo captured the
original `os.Stdout`, so using stdout as the channel could never be made safe. Case S03 is covered by
`TestEngineLogsNeverReachIPCPipes` (native Windows test with the real embedded logger).

## 9. Platform identity

`managed.ClientPlatform()` returns `macos`, `windows` or (CI only) `linux` from `runtime.GOOS` at build
time; the login request no longer hard-codes `macos`. The backend already accepts `windows`.

## 10. What round 2 does not do

* No Windows Service, no named pipe, no installer (round 3 / 8).
* No Windows-specific WFP policy beyond what sing-tun installs; the scenarios that need more (explicitly
  bound physical sockets, browser DoH, WebRTC) are round 6.
* No claim for Windows 10/11 compatibility: nothing here was executed on a client SKU yet.
