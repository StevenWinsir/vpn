# Android TUN design (round 2)

Scope: `handoff_app_android.md` sections 6, 7.1–7.4 and round 2. This round proves the real VpnService TUN on an
emulator with the production engine; sign-in, the connect screen and metering against the backend are rounds 3–4.

## 1. Who owns what

| Resource | Owner | Notes |
| --- | --- | --- |
| VPN consent | the user, via `VpnService.prepare` | the test suite pre-grants it with `appops set com.asterlink.vpn ACTIVATE_VPN allow`, which is what the dialog records |
| `AsterVpnService` (foreground, `BIND_VPN_SERVICE`) | app | registers itself as the Core's VPN host (`VpnHostRegistry`) while it runs; refuses starts without its own action (no always-on) |
| Interface description (`TunSpec`) | Go Core | derived from the parsed Mihomo TUN section (`engine/tunspec.go`), contract `shared/contracts/tun-spec-v1.json` |
| `Builder.establish()` | app (`AsterVpnService.establish`) | applies the spec, `detachFd()`, returns the number; never keeps a descriptor |
| The TUN descriptor | Go Core from then on | handed to Mihomo as `tun.file-descriptor`; closed by Mihomo's listener on Stop; closed by the Core itself if the listener never took it (`engine/tunfd_unix.go`, decided by inode identity, never by a second close) |
| Routes, resolver, address families | Android (VpnService) | `0.0.0.0/0` always, `::/0` only when the node carries IPv6; DNS `198.18.0.2` |
| Socket protection | app (`VpnService.protect`) | called for every Core control-plane socket (`network.Egress`) and every Mihomo socket (`dialer.DefaultSocketHook`) |
| Physical network view | app (`NetworkMonitor`) | best `INTERNET`+`NOT_VPN` network; the Core reads it through `Platform.networkSnapshot` |

Closing the descriptor is what removes the VPN interface. The process dying closes it too, so a killed app leaves no VPN
(case T09).

## 2. Data path and loop prevention

```text
app socket ──▶ tun0 (198.18.0.1/30) ──fd──▶ Mihomo system stack ──▶ MATCH,node ──▶ node outer socket ──protect()──▶ wlan0/rmnet
system resolver ──▶ 198.18.0.2:53 ──▶ any:53 hijack ──▶ fake-ip (198.18.0.0/16)
Core API client ──▶ network.Egress (protect + the physical network's own resolvers) ──▶ wlan0/rmnet
```

The app is **not** excluded from its own VPN (`addDisallowedApplication` is never called): `Verify` and the system resolver
must take the same path as any app. The pinned Mihomo disables its loopback detector on Android (`features.Android`), so
`protect()` on every outbound socket is the only thing preventing a routing loop. `dialer.DefaultSocketHook` is set in
`engine/platform_android.go`; with it set the pinned core ignores interface names and routing marks (the CMFA path). A
socket the host refuses to protect fails its dial (`network.ErrNotProtected`) instead of entering the tunnel.

sing-tun's system stack listens on the TUN's own address with a plain `net.ListenConfig` (not the hook), so its NAT loop is
not protected — correct, since protecting it would route its replies out of the VPN.

## 3. Template (Android variant of `engine/config.go`)

Identical to macOS except: `auto-route: false` (the Builder owns routes), `auto-detect-interface: false` (netlink is refused
to apps on Android 11+), `mtu: 9000` (the Builder uses the same constant). `stack: system` is unchanged and **works** on the
emulator (decision D1 below). The tunnel address `198.18.0.1/30` comes from Mihomo's own derivation from the fake-ip range;
the Builder receives exactly that address, its prefix and the next address as resolver. `MATCH,node` + `MATCH,REJECT` stays:
a node without UDP makes UDP fail instead of going DIRECT (case T06-udp-reject).

## 4. DNS

- The system resolver (netd) asks `198.18.0.2`, the stack hijacks port 53 and answers fake-ip. Private DNS **off** and
  **opportunistic** both end there (opportunistic DoT to `198.18.0.2:853` fails, netd falls back to port 53).
- Private DNS **strict** (`dns.google`): netd resolves the DoT server's name through the VPN resolver (fake-ip), then
  speaks DoT to that fake address, which the TUN carries to the node: real answers, through the tunnel. Not a leak; recorded
  as such (T03-dns-strict). A non-existent name correctly returns NXDOMAIN in this mode.
- A plain UDP query to any resolver (`9.9.9.9:53`) is hijacked (T03-dns-any53).
- Control plane: Go's pure resolver has no `/etc/resolv.conf` on Android and would ask `127.0.0.1:53`, so every API lookup
  (login included) would fail. `network.platformResolvers()` returns the physical network's own resolvers on Android
  (`dialResolver` falls back to them), queried over protected sockets; an address in `198.18.0.0/15` is never accepted as
  one (`HostSnapshot.Resolvers`). T05 proves an API host resolves and connects by name while the tunnel is up.

## 5. IPv6

The engine probes IPv6 through the node before the TUN exists (`nodeCarriesIPv6`). Capable: the spec carries
`fdfe:dcba:9876::1/126` and the Builder adds it plus `::/0`. Not capable: the spec has no IPv6 at all, and Android blocks
the family for every app while the VPN is up (VpnService.Builder: a family with no address, route or DNS is blocked).
An IPv6 connect then fails immediately (`ENETUNREACH`/`NoRouteToHostException`, ~1 ms on the emulator) and apps fall back to
IPv4; nothing leaks and nothing hangs (T04-ipv6-fallback). The emulator's upstream has no IPv6, so the "node carries IPv6"
branch is BLOCKED locally (T04-ipv6), as the handoff requires.

## 6. Underlying network

The app is subject to its own VPN, so `ConnectivityManager.activeNetwork` and the default-network callback report the VPN
once it is up; reading those would hand the Core the tunnel's fake-ip resolver and change the network signature the moment
the tunnel appears (an immediate self-disconnect). `NetworkMonitor` follows the best network with `INTERNET` and `NOT_VPN`
(`registerBestMatchingNetworkCallback` on API 31+, `requestNetwork` before, hence `CHANGE_NETWORK_STATE`), caches its
description for the Core's once-per-second reads and calls `Mobile.networkChanged()` (Mihomo interface cache flush and
resolver reconnect) when it changes. The VPN follows the system default network (`setUnderlyingNetworks(null)`) and
inherits its meteredness (`setMetered(false)`, API 29+).

## 7. Service lifecycle

`ACTION_CONNECT` → `startForeground` (type `specialUse`) → register as host → `TunnelSession.connect()` (production: the
Core's managed `connect`, i.e. every gate, the first metering ACK, the TUN, Verify). `ACTION_DISCONNECT` (app or the
notification's "断开"), `onRevoke` (another VPN, or the user in Settings) and `onDestroy` all run the same bounded
disconnect in the application scope, keep the host registered until it finished (settling needs protected sockets) and
then leave the foreground and stop. A connect while a disconnect of the same instance is settling is dropped. Always-on is
not supported (`SUPPORTS_ALWAYS_ON=false`, and a start without our action stops at once).

## 8. Decisions

- **D1 — stack.** `stack: system` in fd mode works on API 36 (all T01–T07 cases); no change of stack.
- **D2 — fd ownership.** The Core owns the descriptor after `detachFd`; Mihomo's listener closes it; the Core closes it only
  if the same file is still open under that number (`fstat` identity). The app never holds one.
- **D3 — foreground-service type `specialUse`.** The Android docs reserve `systemExempted` for "VPN apps (configured using
  Settings > Network & Internet > VPN)" and throw `ForegroundServiceTypeNotAllowedException` otherwise; whether a just-consented
  app qualifies at its first `startForeground` cannot be ruled in. `specialUse` (with the `PROPERTY_SPECIAL_USE_FGS_SUBTYPE`
  property) is always permitted; the APK is distributed directly, so no Play review applies. Revisit with real devices in round 6.
- **D5 — wait for the VPN to carry the app before handing over the descriptor.** `Builder.establish()` returns
  before the system has made the VPN this app's default network. Measured on API 36: in that window the first lookup
  answered with the name's real addresses from the physical network (not fake-ip) and the first connection went
  nowhere, so `Verify` could have passed or failed without crossing the tunnel. `AsterVpnService.establish` therefore
  waits (at most 5 s, polling every 25 ms) until `activeNetwork` has `TRANSPORT_VPN`; otherwise it closes the interface
  and returns -1 (the Core reports `tunnel_failed`). After the fix the first lookup is fake-ip in ~1 ms.
- **D4 — debug-only hooks.** The acceptance hooks (`native/core/mobile/tuntest.go`) exist only with `-tags asterlink_tuntest`
  (`build-native-android.py --tun-test-hooks`); the record says `tuntest_hooks`, and Gradle refuses any mix of hooks AAR and
  ordinary build (`verifyCoreAar`), so no ordinary or release APK can contain them.

## 9. Findings for other platforms and later rounds

- **VLESS UDP through the reference forwarder.** The pinned Mihomo VLESS client always uses XUDP (mux) for UDP
  (`adapter/outbound/vless.go`: an empty `packet-encoding` turns XUDP on), while `native/nodeagent/forwarder` only speaks
  plain VLESS UDP (command 2). UDP over a NodeAgent-managed VLESS node therefore does not work from any Mihomo client
  (macOS/Windows included); `asterlink-billtest` passed because it uses its own VLESS client. Fix belongs to the forwarder
  (XUDP support) and needs its own round with three-platform regression; not changed here.
- **libgojni.so size.** Round 1 recorded 25.1 MiB for arm64 because `platformSupported()` was a constant `false` and the
  linker dropped the whole unreachable TUN/listener stack. With the real TUN the arm64 `.so` is 44.4 MiB and the Debug APK
  ~56 MiB. This is the real baseline for round 6.
- **One goroutine leaks per TUN start (all platforms, upstream).** The pinned Mihomo creates the TUN listener with
  `context.TODO()` (`listener/sing_tun/server.go`), and sing-tun's system stack starts its TCP NAT timeout loop with that
  context (`stack_system_nat.go`: `NewNat` → `loopCheckTimeout`, which only returns on `ctx.Done()`), so every
  connect leaves one goroutine and its (self-expiring) NAT map behind. Measured: +19 over 20 cycles, nothing else grows,
  descriptors flat. Harmless for a desktop session; a phone that reconnects on every network change accumulates them.
  T-CYCLES counts this stack separately and fails on any other growth. Fix belongs in the pinned core (a cancellable
  context per listener, cancelled in `Listener.Close`) and needs three-platform regression; not changed here.
- **Names that do not exist publicly.** Fake-ip answers A locally but the template forwards AAAA upstream (through the
  node). For a name with no public records that AAAA comes back NXDOMAIN, and Android's resolver then refuses the whole
  name (also from its cache). Real names are unaffected; names that exist only in a private DNS (intranet) cannot work
  through the tunnel anyway. The test target is therefore `example.com` (public A/AAAA), which only the test node maps
  to the sink.
- **UDP to a name is resolved on the client.** Mihomo resolves a UDP destination name before relaying (TCP sends the
  name to the node), so a name the node alone knows cannot carry UDP; T03-udp uses SNTP to a public time server by IP.
- **Verify reason.** `Engine.Verify` now wraps the last probe's error into `tunnel_unhealthy` (`errors.Is` unchanged) so a
  report can say why; it contains only the probe URL.
- **The idle-traffic heuristic does not transfer.** On a phone every app shares the tunnel (a fresh emulator updates Play
  services), so "few bytes while idle" is not a loop test. T05-loop instead requires that no TUN flow targets the node or the
  bootstrap resolver (the signature of the Core's own sockets re-entering the tunnel) and records the background bytes.

## 10. Running it

```bash
python3 scripts/test-native-android.py --suite tun --allow-network-changes --api 36
```

It builds the hooks AAR and the test APK, starts `asterlink-testnode` (pinned Mihomo as Shadowsocks / VLESS / VLESS REALITY
server) and `asterlink-trafficsink` on the host **loopback only**, pre-grants consent, writes the run configuration into the
debug app's private files through `run-as` (never argv), runs `TunAcceptanceTest`, then kills a connected app (T09) and
resets consent. Only the emulator's network changes. The node and the sink never listen on a LAN or company-VPN address;
the emulator reaches them through `10.0.2.2`, and the sink is reachable *through the node* only by the name
`example.com`, which the node alone maps to its loopback (everywhere else it leads to IANA). A first attempt bound the
sink to the host's "primary" address, which on a Mac behind a company VPN is the VPN's tunnel address and was not
reachable even locally; loopback-only avoids any dependence on the host's network.

The user's own nodes (T02-real) come only from the environment: `ASTERLINK_TUN_PROXY_JSON` (one proxy object) or
`ASTERLINK_TUN_REAL_NODES` (a JSON array). For those, disconnect any company VPN on the Mac first: the emulator's traffic
leaves through the Mac, and a VPN underneath would make the result describe the company network, not the node.
