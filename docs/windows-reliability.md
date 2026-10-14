# Windows network reliability, leak protection and failure recovery (round 6)

Status: **code and test tooling delivered; first real run on Windows 10 Home 22H2 (build 19045) on 2026-10-14, results in section 6. Windows 11 NOT_RUN; the 8-hour soak, hibernate, NIC switch, power-cut and browser cases are NOT_RUN.**
A case is only claimed as passed where section 6 says so. Rows are filled in the handoff record
(`handoff_app_windows.md`, round 6) with the real result, machine and build.

## 1. What changed in the product

| Change | Why (evidence) | Where |
| --- | --- | --- |
| **Resume from sleep/hibernate is detected on Windows.** The watchdog now also treats "I was not scheduled for more than 10 s" (`stallLimit`) as a suspend, independent of any clock's sleep semantics; the tunnel is closed first and the user must reconnect (`lifecycle_reconfirmation_required`). | The old detector compared the wall clock with Go's monotonic clock. That works on macOS/Linux (the monotonic clock pauses in sleep) but on Windows the runtime's clock is based on the system interrupt time, which is documented to include time spent asleep, so the two would not diverge and a resumed machine could keep its tunnel without any re-check. (Derived from the platform documentation, not yet observed on hardware; the stall detector is correct either way.) Found by reading the code path, not by a failing run; covered by unit tests (`TestLifecycleGuards`, `TestClockWatch*`). | `native/core/internal/managed/tunnel.go` (`clockWatch`), shared by the dev-meter and the grant monitors |
| **Network signature no longer keys on full IPv6 addresses.** IPv4 addresses count individually; IPv6 counts as its /64 prefix. | SLAAC privacy addresses rotate about daily and RA/DHCPv6 renewals add or drop addresses; with the old rule a healthy tunnel would be torn down with `network_changed` in the middle of a long session. A new prefix, a new adapter, or a changed IPv4 address still changes the signature. Unit tests in `signature_test.go`. | `native/core/internal/network/network.go` |
| **Test tooling for the cases that need a real machine** (below). | The handoff requires evidence per case, not code. | `asterlink-tuntest`, `scripts/windows/*.ps1` |

Deliberately **not** changed, with the reasoning:

* **No new WFP policy.** Plan 7.2 allows a user-mode WFP policy only if measurement proves routes + sing-tun's
  `strict-route` are not enough. The measurement is case `T05-bind` (a socket bound to the physical adapter's own
  address). If it FAILS on a real machine, the next step is a minimal Core-owned WFP filter (see 5); until then adding
  one would be a guess.
* **IPv6 for host names stays IPv4-only through fake-ip on Windows.** The round 2 note "tcp6 by host name fails in the
  tunnel" is explained by the pinned Mihomo, not a bug: with `fake-ip` and no `fake-ip-range6`, `withFakeIP` answers every
  AAAA query with an empty answer (`dns/middleware.go`), so applications use the A record (a fake-ip) and reach the
  node over IPv4. IPv6 literals still go through the TUN when the node carries IPv6, and are refused immediately when it
  does not (round 2 `T04-ipv6-fallback`). Consequence to state in product material: *IPv6-only sites reached by name are
  not supported yet.* Adding `fake-ip-range6` would answer AAAA with a fake address even for nodes without IPv6, and
  every connect would then fail through the WFP IPv6 block before falling back, so it is a separate decision, not part of
  this round.
* **No Service-side SCM session/power handlers.** The Core detects suspend itself (above); a logoff closes the GUI,
  which closes the pipe, which stops the Core (round 3, verified by hard-kill cases). `T09-kill-Gui` re-checks that.
  Fast-user-switching without logoff leaves the owner's session alive by design (the owner gate rejects a second user).

## 2. Case matrix and how to run each

All commands run on the **authorised test machine**, never on a work network or with another VPN connected. Cases
marked *harness* use `asterlink-tuntest.exe --allow-network-changes` (elevated); *script* cases use the PowerShell
files (elevated, nothing is installed by them).

Common harness command (node credentials come from the environment, see `docs/windows-tun-poc.md`):

```bat
asterlink-tuntest.exe --allow-network-changes --output C:\asterlink-r6\run1 --cycles 20 --udp-reject-test --kill-test ^
    --api-host demo.hyshentou.cn:443
```

| Case | What it proves | How | Status |
| --- | --- | --- | --- |
| T03-lan-dns | Port-53 queries aimed at **every other adapter's** configured resolver (DHCP router DNS, manual public DNS, 2nd NIC) are blocked or hijacked, never answered by that resolver | harness (new, runs by default) | NOT_RUN |
| T03-dns-system / T03-dns-any53 / T03-dns-adapter | system resolver goes to the tunnel's resolver | harness (round 2, unchanged) | PASS in round 2 on Windows 10 Home 22H2 |
| T05-bind | A socket bound to the physical adapter's source address does not reach the internet directly. **Decides whether a WFP policy is needed** | harness (new) | NOT_RUN |
| T05 / T05-loop | control plane reachable on the physical interface; no self-routing loop | harness (round 2) | PASS in round 2 |
| T07-signature | bringing the tunnel up does not change the physical-network signature (no false `network_changed`) | harness (new) | NOT_RUN |
| T07 (adapters/other VPN) | Wi-Fi ↔ Ethernet and 2-NIC switching; competing VPN | manual: connect, then unplug/plug as below, then `reliability-check.ps1 -Mode Verify` | NOT_RUN |
| T08 | sleep, hibernate, clock set back, late authorization: tunnel closed on resume, no auto-reconnect, lease not extended | manual event + `reliability-check.ps1 -Mode Verify`; the clock-set-back and suspend logic also has unit tests | NOT_RUN on hardware |
| T09-kill-Core / -Gui / -Service | hard kill of each product process while connected: tunnel closed, no orphan Core, system DNS and direct internet intact, Service comes back idle | `reliability-check.ps1 -Mode Kill -Target Core|Gui|Service` | NOT_RUN |
| T09 power cut | cut power (or hard-reset the VM) while connected, boot, log in | `reliability-check.ps1 -Mode Verify` after boot | NOT_RUN |
| T04-ipv6 / T04-ipv6-fallback | IPv6 carried or refused fast | harness (round 2) | PASS in round 2 (see 1 for the host-name limitation) |
| T02-udp / T02-udp-reject | UDP through the node; a node without UDP never goes direct | harness (round 2) | PASS in round 2 |
| R03 20 start/stop cycles | no accumulation of goroutines/handles | harness `--cycles 20` (default) | NOT_RUN at 20 (round 2 ran 5) |
| R03 8-hour connection | CPU/memory/handles/threads curve, exit stays the tunnel's | harness `--soak-seconds 28800 --soak-interval 60 --max-seconds 30000 --output …` writes `soak.csv`; **and** `soak-sampler.ps1 -Hours 8` for the installed Service/Core/GUI | NOT_RUN |
| R03 10 sleep / network-switch combinations | resume closes the tunnel every time | repeat the T08 and T07 manual steps ten times, one Verify report each | NOT_RUN |
| Browser DoH, WebRTC leak page, real AAAA | ordinary browser paths | manual with Chrome/Edge/Firefox; record the page and the result per `docs/windows-client-ui.md` | NOT_RUN |

Manual event recipes (always: connect in the AsterLink window first, note the time, do the event, then run the
Verify script and add the screenshot of the window):

* **Sleep/resume:** Start → Power → Sleep, wake after at least 30 s. Expected: window shows disconnected with the
  "system sleep or clock change" message, Verify reports the adapter absent and DNS/internet working.
* **Hibernate:** `shutdown /h`, power on, log in; same expectation.
* **Clock set back:** set the system time one hour back while connected; expected the same stop. Restore the time
  afterwards (or let Windows time resync) before the next test.
* **Wi-Fi ↔ Ethernet:** with both available, disable the used one (or unplug the cable); expected `network_changed`
  stop, not a hang. Repeat in the other direction. With two adapters up, record which one carries the default route.
* **Other VPN:** with a *different* VPN client's adapter up, connecting must be refused with the conflict message; the
  other VPN's routes must be untouched afterwards.

## 3. Soak criteria (what the harness decides, and what it does not)

`judgeSoak` compares the last sample with the third (two warm-up samples are ignored) and fails on: goroutines +50,
handles +64, threads +32, Go heap +64 MiB, or any exit probe (every 5th sample) that equals the pre-tunnel address
or fails. These are tripwires for *unbounded* growth, not performance targets. The CPU, memory and handle numbers
that go into the handoff come from `soak.csv` / the sampler CSV as measured, not from these limits.

## 4. Known limits that remain after this round

* Nothing in round 6 was executed on a Windows 10/11 client by the author; CI proves compile, unit tests, script
  syntax and an idle Verify run on Windows Server only.
* Hosts with only IPv6 connectivity are not covered (the physical-egress pick requires an IPv4 default gateway).
* `network_changed` is decided from adapter name + IPv4 addresses + IPv6 /64 prefixes; two networks with the same
  IPv4 address on the same adapter are indistinguishable (e.g. 192.168.1.x at home and at an office on the same NIC)
  until the lease or the periodic API check fails.
* The suspend threshold is 10 s: a sleep shorter than that is not noticed by the watchdog (sockets usually survive it).
* No system-wide kill switch: after a *normal* disconnect or crash the machine is on its direct network again (by
  design, plan 7.2).

## 5. If T05-bind FAILS (decision rule, not implemented)

Add a connect-time WFP policy owned by the Core in a dynamic session (removed by the OS with the process): block outbound
traffic on the physical interface from every application except the Core's executable path and a short, explicit
list of control-plane exceptions, keyed to the interface LUID found by `PhysicalDefault`. It must carry its own
negative tests (an ordinary app bound to the physical address is blocked; the API and node outer connections still pass)
and the same single-owner/recovery argument as `docs/windows-tun-design.md` section 1. Do not "ban a few STUN ports".

## 6. First real-machine results (Windows 10 Home 22H2, build 19045, x64, 2026-10-14, user-reported output + JSON reports)

Machine: elevated, company VPN disconnected, WLAN as the physical adapter, SS node with `udp:true`. Build: CI artifacts of PR #25.

| Case | Result | Observed |
| --- | --- | --- |
| harness, 20 cases (`--cycles 20 --udp-reject-test --kill-test`) | 0 FAIL, 1 BLOCKED | the BLOCKED one is `B-COUNT` (no download URL given; metering was round 5) |
| T03-lan-dns | PASS | 1 resolver configured (the router): blocked, not answered |
| T05-bind | PASS | a socket bound to the WLAN address could not connect while the tunnel was up (blocked). **No WFP policy is needed on this machine**; not proof for other drivers/NIC setups |
| T07-signature | PASS | signature identical before and while connected |
| T03-dns-system / any53 / adapter, T02-udp, T02-udp-reject, T05, T05-loop, T07, T09-kill | PASS | as round 2 |
| T04-ipv6-fallback | PASS | mode=tunnel (node carries IPv6), literal answered over HTTP |
| T-CYCLES (20 cycles) | PASS | goroutines 12->31, handles 460->460 (goroutine growth is +19; the soak tripwire is +50, so it is not flagged, but it is worth watching in the 8-hour run) |
| reliability-check Kill Core / Gui / Service | PASS x3 | adapter and split routes gone, no orphan Core, Service back to Running, system DNS real, LAN resolver answers again, direct HTTPS 200, WinINET proxy unchanged, no auto-reconnect |
| reliability-check Verify after one sleep/resume | PASS | tunnel closed, DNS/internet normal, no auto-reconnect |

Observations to follow up:

* After the `--kill-test` session the next start logged `create adapter: Cannot create a file when that file already exists ... [retrying 1/3]` once: the hard-killed Core's adapter was still being torn down. The retry succeeded and nothing was left behind, but it shows the OS removes the adapter asynchronously after `TerminateProcess`.
* The machine has a leftover WinINET value `ProxyServer=127.0.0.1:7890` (`ProxyEnable=0`) from older proxy software; the product never changed it.
* The sleep run was verified only by its end state. It was not recorded whether the window showed the "system sleep" message, so it is not evidence that the new stall detector (rather than the network-signature check) closed the tunnel.

Still NOT_RUN: Windows 11; 8-hour soak (`soak-sampler.ps1`, `--soak-seconds`); 10 sleep/network-switch combinations (1 sleep done); hibernate; Wi-Fi <-> Ethernet; power cut; browser DoH/WebRTC pages; second NIC. These stay out of the support commitment until run.
