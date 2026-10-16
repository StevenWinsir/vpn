# Android client: connection loop, development metering and notifications (round 4)

Round 4 of `handoff_app_android.md` proves the user's main path on the production build: sign in →
choose a line → Connect (system VPN consent) → real TUN → real traffic → usage → charged at the
administrator's rate → stopped automatically when the plan ends, the data runs out or the line
changes. `client_reported` is still the development ledger: it does not lift the production
protections, and only `node_reported` (B04) is fit for charging money.

## What changed in the app

| Area | Change |
| --- | --- |
| Usage on the connect page | "本次连接用量 · 计费" now counts **this connection only**. The Core's client-reported counters are cumulative for the whole sign-in and can span connections at different rates; the controller records a baseline when the tunnel comes up (the last pre-connect counters; zero for a node-reported connection, whose local meter starts at zero) and bills the difference at the one rate the connection is bound to (a changed rate ends the connection). Integer arithmetic, floored like the server. |
| Ongoing notification | "AsterLink · 已连接" with "线路名 · ↑速率 ↓速率", refreshed from the session state (≈1 s while connected), action "断开". Only `AsterVpnService` posts and removes it, and its follower is stopped before removal, so a late update cannot resurrect it after the VPN is gone. |
| Stop alert | When the tunnel ends without the user asking (quota, plan end, re-rated/disabled line, lease, session end) and no screen is open, a notification on its own channel says why (the Core's Chinese reason). A new connection or sign-out clears it. |
| Quick Settings tile (optional) | Connect/disconnect in one tap only when the connect page would allow it (signed in, plan valid, line chosen, VPN consent already given); otherwise it opens the app. Bound only by the system UI (`BIND_QUICK_SETTINGS_TILE`). Not exercised by the automated suites — see the manual steps. |
| VPN ownership | `AsterVpnService` calls `VpnService.prepare()` itself right before `Builder.establish()`. Measured on API 36: without it the first establish of a process returns `null` unless an activity called `prepare()` first (the tile, or a connect after the process restarted, would fail with `tunnel_failed`). Consent withdrawn in Settings → `prepare()` returns an intent → refused. |
| Reconnect at once | A Connect that arrives while the previous Disconnect is still settling waits for it in the same foreground service and then runs (before: refused with `vpn_stopping`). A service that is already ending is no longer reported as running, so the controller's own follow-up stop cannot cancel the queued connect. |
| Polling | The controller reads the snapshot every 1 s whenever a tunnel exists (connected **or stopping**), not only when connected, so a stop and its reason are seen within a second. |

### Shared Core change (affects macOS and Windows too)

`managed`: the reason a guard stopped a running tunnel (`quota_exhausted`, `subscription_expired`,
`profile_changed`, `client_config_unavailable`, `network_changed`, …) is now **kept** in
`last_error` until the user's next action (connect, select, sign-in), instead of being erased by
the keep-alive's next successful `/session` renewal (≤ one report interval later — 60 s in
production, 2 s in the test world). A renewal still clears its own transient failures
(`TestARenewalClearsItsOwnFailureOnly`). PR #35 had kept the erasing behaviour and only fixed the
test; on Android the stop alert and the connect page depend on the reason, and with 2 s report
intervals the reason was measurably lost before the app read it. `TestTheStopReasonSurvivesLaterRenewals`
fails without the change and passes with it. On the desktops the visible effect is that the
stop reason stays on screen until the user acts.

## The billing suite

```sh
python3 scripts/test-native-android.py --suite billing --allow-network-changes [--keep-emulator] \
    [--upload-mib 20 --download-mib 80] [--no-node-agent]
```

Everything runs on this machine: a private Unix-socket PostgreSQL; the backend's
`TestAndroidBillingWorld` (the real Gin router on `127.0.0.1`, reached from the emulator through
`adb reverse`, so the Core's "HTTP only on loopback" rule holds); the round-2 test node
(`cmd/asterlink-testnode`, pinned Mihomo as Shadowsocks and VLESS servers, reached from the
emulator at `10.0.2.2`); the traffic sink (`cmd/asterlink-trafficsink`); and the real node agent
(`native/nodeagent`) over real mTLS for one node-reported line. The APK is the **normal debug build**:
the AAR carries no acceptance hook (the suite refuses a hooks AAR), and the app is driven through
`BillingAcceptanceTest`, which uses the app's own `SessionController`, `TunnelLauncher`,
`AsterVpnService`, Core and notifications. VPN consent is pre-granted with `appops` (what the
dialog records); `POST_NOTIFICATIONS` with `pm grant`.

The lost acknowledgement (B02) is injected by the **world**, not the app: the next `/traffic`
report is committed by the real handler and then its connection is closed without a reply.

Only the emulator's network changes. Nothing reaches `demo.hyshentou.cn` or the remote test
database. Verify still probes its public URLs (`cp.cloudflare.com`, …) through the test node,
i.e. through this Mac's own network.

### Cases

| Case | Requirement |
| --- | --- |
| B01-n1 / B01-n2 | 1× (dedicated, Shadowsocks) and 0.5× (ordinary, VLESS): connect through the gates, first ACK, TUN and Verify; the sink sees exactly the body; the server's counted bytes cover the body within +10 % + 2 MiB background; `used_units` delta = (up + down) × rate **to the unit**; the server's byte deltas equal the Core's confirmed counters; the app shows the server's used/remaining; the connect page's per-connection usage covers the body and bills physical × rate |
| B01-rates | both rates measured with the same procedure |
| N01 | during a download the ongoing notification names the line and shows non-zero rates; it is removed after Disconnect |
| C01 | Connect issued the moment Disconnect returned connects (no `vpn_stopping`) |
| B02 | one real `/traffic` reply lost: the Core resends the identical batch (same sequence, same cumulative counters) and the server charges it once (server deltas = Core counters, formula exact) |
| A04-rate / rebind | the administrator re-rates the connected line: the tunnel stops by itself with `profile_changed`, the VPN network and service are gone, the tail settles, the whole segment is charged at the **old** rate; the next connection binds the new rate |
| A04-disable | the line is disabled while connected: stop with `client_config_unavailable` (or `node_unavailable`), segment charged at its own rate, the line leaves the list, Connect disabled |
| A04-expire / N02 | the plan ends while connected: stop with `subscription_expired`; the stop alert shows the Core's reason; Connect disabled |
| A04-quota | the app keeps downloading until the quota is gone: stop with `quota_exhausted`, server remaining 0, Connect disabled; the overshoot (client-reported: up to one report interval of traffic) is recorded, not hidden |
| B04 | node-reported line through the real agent: connect via a grant, `metering_source=node_reported`; a client `/traffic` on that session → 409 `metering_source_mismatch` with the ledger unmoved; **zero** client reports during the connection; the agent alone bills the sink-exact body at the line's rate within +2 % + 2 MiB |
| vpn_gone / logcat_clean | no VPN network left; logcat holds no password, node secret or sink token |

B04's target is `sink.asterlink.localhost`, a name only the node resolves (RFC 6761 `*.localhost`
→ the host's loopback, where the sink listens). The world checks that this host's resolver does
so; if not, B04 is BLOCKED, never PASS. The app side must resolve it into the tunnel's fake-ip
range (checked), so the bytes cross the node.

### Results (2026-10-16, Mac mini M4, emulator API 36 arm64-v8a, page size 4096, company VPN on)

`--suite billing --allow-network-changes` with the default 20 MiB up + 80 MiB down per rate:
**32 PASS / 0 FAIL / 0 BLOCKED**. Selected measurements:

| Case | Observed |
| --- | --- |
| B01-n1 (1×) | sink exact; counted 20,972,965 + 83,888,065 bytes (+0.01 % / +0.00 %) → 104,861,030,000 units |
| B01-n2 (0.5×) | sink exact; counted 20,972,909 + 83,888,063 bytes → 52,430,486,000 units |
| B02 | dropped `sequence 20` resent unchanged → 200; charged once |
| A04 | stopped 0.9 s (re-rate), 1.3 s (disable), 1.4 s (plan end), 18.7 s of continuous downloading (quota, 12 MiB left at start; overshoot 1,053,992 bytes) after the change; VPN gone within ≤ 22 ms of the stop |
| N01 | "HK-IEPL-01 · ↑306 B/s ↓2.76 MiB/s"; removed 101 ms after Disconnect |
| B04 | 10,485,760-byte body at 500‰ → 5,244,902,500 units (+0.04 %); 0 client reports; probe → 409 `metering_source_mismatch` |

These are local values with 2 s report intervals, not production latency promises. Evidence:
`artifacts/native-android/billing/` (billing-report.json, instrument.txt, world.log; not committed).

Before the fixes above, the same suite measured: every first connect of a process `tunnel_failed`
(establish without `prepare()`); stop reasons empty after the keep-alive (A04 ×3, N02); Connect
right after Disconnect refused with `vpn_stopping`.

## Manual check on the emulator (or a phone) against demo.hyshentou.cn

The suite cannot sign in to the demo server (it needs your account password) and cannot press the
system consent dialog or the Quick Settings tile. **Disconnect the company VPN first**: the
emulator's traffic leaves through this Mac, so with it on you are testing the company network, not
the node, and the demo backend's real nodes may be unreachable from it.

1. Build and install: `python3 scripts/build-native-android.py --target apk`, then
   `adb install -r apps/android/app/build/outputs/apk/debug/app-debug.apk`.
2. Open AsterLink, sign in with an account that has an active plan, choose a line, press Connect,
   allow the VPN dialog (and notifications on Android 13+).
3. In Chrome on the emulator open a few sites and play a video for a minute. Expect: the connect
   page shows "已连接", live rates, the 60 s curve, "本次连接用量 · 计费"; the notification shade
   shows "AsterLink · 已连接 / 线路名 · ↑… ↓…" with "断开".
4. Pull down Quick Settings, edit tiles, add "AsterLink"; tap it to disconnect and again to
   connect (consent was given in step 2). Signed out or without a line it opens the app.
5. In the admin console change the connected line's rate: within a few seconds the app shows the
   reason "线路配置已更新，请重新选择线路"; with the app in the background a "AsterLink · 已断开"
   notification says the same. Reconnect and check the line's new rate on the connect page.
6. On the website check that the plan's used traffic grew by the connection's "计费" amount.

The demo backend must run with `APP_ENV` other than `production` for client-reported lines
(`DevMetering`); a production backend answers `/traffic` with `client_upgrade_required` and only
node-reported lines connect.

## Known limits

- The Quick Settings tile's own connect path (foreground-service start from a tile) is not
  automated; the decision logic (`TileView`) is unit-tested and the service now takes VPN
  ownership itself, which a tile start needs.
- A node-reported line on a real public managed node (B04-live) needs a deployed node agent with
  TLS: not available, so it remains a round-6 / external gate.
- Real phones (vendor background killing, radio changes) are round 5/6.
