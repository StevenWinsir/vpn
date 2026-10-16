# Android client: mobile lifecycle and reliability (round 5)

Round 5 of `handoff_app_android.md` makes the VPN survive what a phone does all day — screen off,
deep sleep, Doze, Wi-Fi ↔ cellular, airplane mode, another VPN app, being swiped away — **without
weakening authorization**. Before this round the Core treated every wake-up like a desktop
coming back from sleep: it stopped the tunnel, and the user had to reconnect by hand minutes after
every screen-off.

## The authorization gate (handoff 7.5)

`managed.Options.ResumePolicy` (shared Core). The zero value `ResumeStop` is the desktop behaviour
and is unchanged on macOS and Windows; only the Android binding (`native/core/mobile`) chooses
`ResumeGate`.

A **lapse** is anything after which the Core can no longer assume the segment is still authorized:

| Lapse | Detected by | Reason code |
| --- | --- | --- |
| The CPU slept or the process was not scheduled; the clock was set | the 1 s watchdog: wall vs monotonic clock diverge by > 5 s, or the watchdog was not woken for > 10 s | `lifecycle_reconfirmation_required` |
| The physical network changed (Wi-Fi ↔ cellular, new address, gone) | the host's network signature (ConnectivityManager), checked every 3 s | `network_changed` |
| The lease ran out while the control plane was unreachable | the authorization gate itself (`lease_expired`) | `lease_expired` |

Under `ResumeGate` a lapse does **not** end the segment:

1. **Close the gate.** The engine (`Mihomo.Suspend`) puts the tunnel in its suspended state: every
   new TCP/UDP flow from the device is dropped and every open flow is closed. The TUN descriptor,
   its routes and its DNS stay, so applications keep sending into the tunnel and **nothing leaves
   it**: no leak to the physical network, no use without authorization. (The DNS hijack keeps
   answering; fake-ip A records are local, and the few lookups it forwards use the node as always
   and are not proxy flows.)
2. **Re-confirm online.** The physical network is read again (Preflight; no network → wait). A
   metered segment then sends a traffic batch — a pending one is replayed unchanged — whose
   acknowledgement settles what was carried *and* renews the lease, exactly like a periodic report;
   a node-reported segment asks `GET /session`.
3. **Decide.**
   - Authorized → `Mihomo.Resume`, then the same **real probe through the tunnel** that Connect
     uses (Verify). Only then is the segment "connected" again, now bound to the confirmed network.
   - The server says no (plan ended, quota used up, line changed or disabled, ledger conflict) →
     the ordinary stop: data path closed, tail settled, reason kept for the user.
   - No network / no answer / the probe failed → keep the gate closed and retry with backoff
     (2 s doubling to 30 s). A network-change hint from the app or the app coming to the foreground
     retries at once (`Manager.Nudge`).
   - After **5 minutes of awake time** without an answer (`DefaultGateLimit`; time asleep does not
     count) the segment is ended to save power, with `network_unreachable` / `no_network`.

The user can disconnect at any time while the gate is closed; it settles like any disconnect.
Nothing about authorization is relaxed: the lease is not extended, sleep detection is not turned
off, and the gate forwards nothing until the server has answered.

**Snapshot.** While closed, the segment stays `state: "connected"` with `tunnel_health: "gated"`
and a `gate` object (`reason`, `waiting_network`, `closed_seconds`, `attempts`); `last_gate`
records how the latest gate ended (`reopened`/`stopped`, `closed_ms`, `total_closures`). Both are
optional fields that only the Android policy produces (`shared/contracts/snapshot-v1.json`,
fixture `connected_gated`). The app shows **正在重新验证** (re-confirming) or **等待网络**
(server/network unreachable) with the button still offering Disconnect; the ongoing notification
shows the same title without rates.

## Background: the catalog long-poll (handoff 7.6)

Measured first, then decided. A connected segment learns the server's verdict from every traffic
acknowledgement (metered) or session renewal (node-reported), independently of the catalog
watcher: the lifecycle suite re-rates the line while the app is in the background and the tunnel
stops on the next acknowledgement (2.4 s with the world's 2 s report interval; the bound is the
report interval — 60 s in production). Stopping does **not** depend on the watcher, so:

- `ProcessLifecycleOwner` ON_STOP → `Mobile.setForeground(false)`: the `/nodes` long-poll pauses
  after the request in flight (measured: 0 catalog requests in 12 s of background, against ≈ 1 per
  2 s in the foreground; traffic reports continue unchanged). ON_START → resumes at once and
  nudges a waiting gate.
- While paused the Core marks its catalog stale; binding a line then reads the catalog once first.
  Without that, a line bound from the stale list (a tile connect in the background, or a tap right
  after returning) would be invalidated as `profile_changed` by the next catalog — the first
  device run showed exactly this (`TestBindingWhileTheWatchIsPausedReadsTheCatalogFirst`).

## Other lifecycle changes in the app

| Area | Change |
| --- | --- |
| Another VPN takes over (L04) | `onRevoke` (another VPN app, or the user in Settings) tells the controller on the main thread *before* the service's disconnect starts: the tunnel stops and settles as before, and the user now sees **"VPN 已被系统设置或其它 VPN 应用关闭，本次流量已结算"** (in the app, or as the stop alert when no screen is open) instead of a silent disconnect. |
| Notifications denied (N01) | The VPN works without the permission (verified). The account page shows whether notifications are on and links to the app's notification settings (Android only asks once in-app). |
| Battery optimisation | The account page shows whether the app is exempt ("已允许后台运行") or restricted, and links to the system setting. The app never requests an exemption itself. |
| Swiped from recents (L05) | Unchanged by design: the foreground VPN service keeps the process and the tunnel (verified). Force-stop removes the VPN at once and nothing restarts it. |
| Reboot (L06) | Nothing connects by itself (no boot receiver, always-on unsupported, verified). |
| LAN access ("允许局域网") | **Decided: not added in this release.** The tunnel stays full (no `excludeRoute`), so nothing on the local network is reachable while connected (casting, printers). It needs API 33+, changes what "connected" protects against, and none of the lifecycle cases needed it; recorded for a later round together with a UI explanation. |

## The lifecycle suite

```sh
python3 scripts/test-native-android.py --suite lifecycle --allow-network-changes [--keep-emulator] \
    [--screen-off-seconds 600] [--soak-seconds 180] [--cycles 20] [--no-reboot]
```

It reuses the billing suite's world (`TestAndroidBillingWorld` on a private PostgreSQL, the
round-2 test node and the traffic sink on this Mac's loopback; user `life`) with two new loopback
controls: `api` (the client API becomes unreachable: connections are closed) and `hits` (requests
per endpoint, for 7.6). `LifecycleAcceptanceTest` runs in the app with the production Core AAR
(no acceptance hooks) and changes the **emulator** like a phone changes, through shell commands
(UiAutomation), all undone in `finally`. The second VPN app of L04 lives in the test APK only
(`CompetitorActivity`/`CompetitorVpnService`).

| Case | What happens | Pass when |
| --- | --- | --- |
| N01 | starts with POST_NOTIFICATIONS revoked, connects, then grants it back | connected and forwarding without the permission |
| L01-stall | the app process is stopped for 15 s (SIGSTOP from a child process, then SIGCONT) — its clocks jump like after deep sleep | a gate closes (`lifecycle_reconfirmation_required`), re-confirms and reopens **the same connection** without any user action; forwards |
| L01-screen | screen off for `--screen-off-seconds` (handoff: 10 min), then on | still the same connection, forwards (the emulator's CPU does not deep-sleep, so this proves the screen path; the sleep path is L01-stall) |
| L01-gate-holds | client API unreachable, then a stall | gate waits ("等待网络" on the page and the notification); a download through the tunnel fails, the sink sends 0 bytes, the VPN network is kept; API back → reopens and forwards |
| L02-doze | deep Doze forced (unplugged, screen off) for 15 s | traffic reports keep reaching the server, a download works, same connection afterwards |
| BG-catalog / BG-verdict / BG-reconnect | `setForeground(false)`; the line re-rated in the background; back to foreground | no catalog polls in the background, reports continue; the tunnel stops within the report interval; the catalog catches up and a new connection stays up |
| L03-wifi-cellular | `svc wifi disable` (cellular takes over), then enable | a gate per change, re-confirmed on the new network, same connection, forwards on both |
| L03-airplane | airplane mode on, then off | gate waits (nothing forwarded), reopens when the network is back |
| L04-revoke | a second VPN app starts | our tunnel stops, the tail is settled (server ledger = Core's confirmed counters), the user is told `vpn_revoked` once; reconnecting afterwards works |
| L05-task-removed | the activity's task is removed | the VPN service and the connection remain |
| L07-cycles | `--cycles` connect → forward → disconnect | all forward; descriptors and threads do not grow |
| L07-soak | connected `--soak-seconds`, 1 MiB every 30 s, sampled every 15 s | stays connected; no descriptor growth (samples in `soak-samples.json`) |
| L05-am-kill (host) | `am kill` while connected | the process (a foreground service) and its VPN are untouched |
| L05-force-stop (host) | `am force-stop` while connected | the VPN is gone at once; no process, no service; nothing restarts it |
| L06-reboot (host) | the emulator reboots | 20 s after boot: no VPN, no process, no service, no always-on app |
| logcat | | no password, node secret or sink token |

Evidence: `artifacts/native-android/lifecycle/` (`lifecycle-report.json`, `instrument.txt`,
`timeline.json` — every state/gate/reason change with its time —, `soak-samples.json`,
`world.log`; not committed).

### Not automated (NOT_RUN, by design)

- **L07-8h** — an 8-hour connected sample: `scripts/android/lifecycle.sh soak --hours 8` on a
  running, connected app (emulator or phone over USB debugging; read-only sampling of PSS, CPU %,
  threads, descriptors, VPN state, battery, and `dumpsys batterystats` at both ends into a CSV).
  Fill in the numbers from a real run; none are estimated here.
- **L08** — Private DNS off / opportunistic / strict and the `any:53` hijack are the tun suite's
  T03 (round 2, PASS). Chrome's own DoH and a WebRTC leak page need Chrome on the image and a person:
  with the VPN connected, open `chrome://settings/security` → "Use secure DNS" on, browse; then open
  a WebRTC leak test page and check that no public address of the physical network appears (private
  /mDNS candidates are recorded separately).
- **Vendor ROMs** (Xiaomi/Huawei/OPPO/vivo background killing), real radio hand-over and real
  battery drain need a real phone — a release gate of round 6.

## Manual check on the emulator or a phone

`scripts/android/lifecycle.sh` prints and runs the same device commands the suite uses:

```sh
scripts/android/lifecycle.sh state          # VPN network, process, service
scripts/android/lifecycle.sh screen-off 10  # L01 by hand: 10 minutes, then the state
scripts/android/lifecycle.sh doze-in        # L02 by hand (then doze-out)
scripts/android/lifecycle.sh wifi-off       # L03 by hand (then wifi-on; airplane-on/off)
```

What to look for in the app: after a screen-off the status says **已连接** again within a second
or two of unlocking (or briefly **正在重新验证**); with no network it says **等待网络** and the
notification says the same; nothing needs to be pressed. Starting another VPN app ends AsterLink's
tunnel with the "已被…关闭" message.

**Company VPN on this Mac:** the automated suite only uses the emulator's network and this Mac's
loopback, so it is not affected (the probe of Verify reaches its public URLs through the test
node, i.e. through this Mac's network). A manual test against `demo.hyshentou.cn` with real
lines must be done with the company VPN disconnected and the macOS AsterLink app quit; otherwise
it measures the company network, not the line.
