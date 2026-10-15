# Node-side commercial enforcement (round 7) — what is proven, how to run it

Rounds 1–6 made the Windows client work. Round 7 asks a different question: **if the user stops cooperating, does the
node still enforce the commercial rules?** The Core's grant path (`connectGranted`, capabilities, ready grant, revoke,
node-confirmed balance) already exists and is shared with macOS (`docs/macos-stage4.md`); Windows consumes it unchanged.
Round 7 adds the evidence tooling and the UI source label — no second ledger, no Windows-specific API.

## XUDP compatibility correction (after Android round 2)

The original round-7 independent UDP probe used plain VLESS command 2. It did **not** prove native-client UDP:
all pinned Mihomo native clients default to XUDP (command 3, mux.cool), previously missing in NodeAgent.
The reference forwarder now supports this UDP subset. Rebuild the node agent and `asterlink-billtest`.
`--udp-sink` now uses the **real pinned Mihomo VLESS adapter with default XUDP**, with no fallback to command 2.
TCP remains the independent raw VLESS client; neither probe sends `/traffic`, runs the Manager, nor creates a TUN.
Reports label the probe `UDP(XUDP/pinned Mihomo)`. A legacy command-2-only node is a regression **FAIL**, not PASS.
`B05-revoke` also checks a fresh XUDP carrier with the revoked credential is refused.
See [implementation, tests and limits](nodeagent-xudp.md). Historical evidence below is retained as command-2 evidence;
the new `native/core/integration` tests and updated real-agent loopback acceptance are separate from Windows TUN acceptance.

## What round 7 added

| Item | Where |
| --- | --- |
| `asterlink-billtest --grants`: an **independent VLESS client** that uses only the credential a grant carried (no Core, no TUN, no usage report) — the attack node-side enforcement exists for | `native/core/cmd/asterlink-billtest/{grants,vlessclient,grants_main}.go` |
| `asterlink-trafficsink --udp-listen`: token-gated UDP echo, so established **UDP** flows can be held open through the node | `native/core/cmd/asterlink-trafficsink` |
| The harness judged against a fake managed node **in both directions** (faithful node passes; a node that skips billing, does not cut revoked flows, accepts `/traffic`, or oversells fails) | `grants_test.go` |
| The same tool run against the **real** API + PostgreSQL + mTLS + node agent binary on loopback | `backend/internal/api/billtest_e2e_test.go`, built by `scripts/test-native.py` |
| `test-native-windows.py --suite grants` and a CI step (live part is `BLOCKED` on hosted runners by design) | `scripts/`, `.github/workflows/windows.yml` |
| Connect page and line list say **who keeps the books**: "计量：节点实测，由服务器强制执行" vs "计量：客户端上报（内部测试线路）" | `NodeRow.MeteringLabelOf`, `ConnectView.xaml` |

## Cases

| Case | What the tool does | Pass means |
| --- | --- | --- |
| `G-CAPS` | `GET /capabilities` | contract 2 and `node_reported` are offered |
| `G-NODES` | reads the catalog | the plan contains a **node-governed** line. None → `BLOCKED` (no node under your management ⇒ commercial acceptance cannot be claimed) |
| `B04-reject` | after a grant exists on a session, posts `/traffic` and `/config` | `/traffic` → `409 metering_source_mismatch`, `/config` refused, ledger unmoved (no double billing, no shared credential) |
| `B04-node-bills` | independent client moves a known payload through the node, **sending no report at all** | ledger rose by the sink-observed payload × bound rate (bracketed: payload … +2 % + allowance) and `remaining == floor((total×1000−used)/1000)` |
| `B05-revoke` | holds a TCP **and** a UDP flow, revokes | both flows die within `--cut-within-seconds` (default 15), the credential is then refused for new connections |
| `B05-idle-expiry` (`--idle-expiry`) | holds flows with **zero API traffic** | the node ends the grant on its own clock (session idle timeout), established flows included |
| `B05-*` / `B07-*` (`--hold-seconds N --hold-case ID`) | opens flows and waits for **you** to change the server side: end the plan, disable the node, lower the quota (add `--hold-drive`), stop the API, kill the agent | the node cuts the flows and refuses the credential |
| `B06-race` (`--race-sessions N [--race-at TIME]`) | N devices ask for a grant at the same instant on a small remaining quota and drive until cut | used units never exceed the quota beyond in-flight tolerance (256 KiB × devices); run on Windows **and** macOS with the same `--race-at` for the cross-machine case |
| `B04-tun` (`--tun`, elevated Windows) | the production Manager: grant → real TUN → child-process transfer → disconnect | snapshot says `node_reported`, no client confirmation/pending batch, the Core sent **zero** `/traffic`, the node's charge matches |

`B07` (control plane / agent failure) splits in two. *Lease enforcement without the control plane* and *agent crash
recovery with exact checkpointed usage* are covered by the loopback tests
`TestNodeEnforcesLeaseWhenControlPlaneVanishesPostgres` and `TestNodeAgentCrashRecoveryDoesNotLoseCheckpointedUsagePostgres`
(real agent binary); the live equivalent is `--hold-seconds` while you stop the API or kill the agent on your node.
Duplicate / out-of-order usage reports are idempotent by cumulative counters (`TestEpochRestartAndReconcilePostgres`).

## Evidence in this round (2026-10-14)

* macOS, real components on loopback (`python3 scripts/test-native.py -run TestBilltest ./internal/api`): API + PostgreSQL + mTLS +
  real node agent binary + real traffic sink + `asterlink-billtest --grants`.
  `G-CAPS`, `G-NODES`, `B04-reject`, `B04-node-bills` (payload 12,582,914 B → +6,292,218,500 units at 500‰, 0.01 % over the body),
  `B05-revoke` (TCP+UDP cut ≈ 1 s after revoking), `B05-idle-expiry` (cut 14 s after the last API call, idle timeout 14 s),
  `B05-expire` (plan ended with flows open → cut ≈ 4 s, credential refused afterwards), `B06-race` (3 devices, 6 MiB quota:
  1 granted, 2 refused `quota_exhausted`, **used exactly 6,291,456,000 of 6,291,456,000 units**, the winner's flow cut at the quota) — all PASS.
  Timings are sampled at 250 ms and depend on the test's 1 s agent sync; they are not production latency promises.
* Harness unit tests: 114 Go tests in the `grants` suite (race detector on macOS/Linux), node agent module 18 tests.
* **Not run (NOT_RUN / BLOCKED):** anything against `demo.hyshentou.cn` — the current test account has no node governed by an agent
  (see below); Windows 10/11 real-machine `B04-tun`; Windows+macOS cross-machine `B06`. The original round also did not test
  Mihomo's VLESS **UDP**; that protocol-level gap is addressed by the later XUDP correction above, not retroactively by these results.

## Production-mode capability matrix

"Authoritative" = per-user credential, trusted node counters, kill of established flows, lease and budget enforcement are all
implemented **and tested** on the node. Anything else must not be sold as a billed, enforced line.

| Protocol / transport | Node-side enforcement | Evidence | May go in a paid plan? |
| --- | --- | --- | --- |
| VLESS over TCP, reference forwarder, **with TLS** | authoritative | agent unit tests; loopback e2e above (plaintext in tests only) | yes, once on a node you manage and after the live run below |
| VLESS plain UDP (command 2), reference forwarder | authoritative (counted, cut on revoke) | retained forwarder regression tests; historical round-7 probe | only with TLS and live-node acceptance; this is not Mihomo's default encoding |
| VLESS XUDP / UDP-only Mux.Cool (command 3), reference forwarder | per-grant payload counters, shared epoch cap, deadline, immediate Remove; guarded per-frame targets | real pinned Mihomo loopback integration; updated `--udp-sink`; CI on Linux/macOS/Windows | after public TLS-node and real-client TUN acceptance; loopback is not a release waiver |
| VLESS Reality / XTLS flow / general TCP Mux.Cool / packetaddr | none (forwarder does not implement them) | — | **no** |
| Shadowsocks | none (shared-port AEAD cannot tell users apart; needs per-grant ports) | — | **no** — stays an internal development line (`client_reported`) |
| Trojan / WireGuard / others | none | — | **no** |
| Plaintext VLESS | enforced but unencrypted | — | never on the public internet (`NODEAGENT_ALLOW_PLAINTEXT` is for tests) |

`client_reported` lines stay development metering; the production guard (`426 client_upgrade_required` for `/config`, `/traffic`) is not
relaxed by anything in this round.

## Running the live part

Prerequisites you must provide (none of them exist in the repository):

1. **A server you control** running `asterlink-nodeagent` with a TLS certificate for `NODEAGENT_VLESS_TLS_*`, registered in
   `/admin/nodes` → 节点代理 (fingerprint), with the node's YAML pointing at its public `server/port/servername` (`docs/macos-stage4.md` §3–4).
   The backend needs `NODE_AGENT_ADDR` + mTLS files. Until this exists `G-NODES` is `BLOCKED` and the round's commercial acceptance is **BLOCKED**.
2. A **test account** with a real subscription and spare device slots (the tool uses `aux`, `aux-tun` and `race-N` device ids derived
   from the base id; raise the plan's device limit or expect `device_limit`), and quota for roughly `(4+8) MiB × rate` per run (smaller with
   `--upload-mib/--download-mib`; for `B06` lower the account's remaining quota to a few MiB so the devices really contend).
3. The traffic sink on a server **the node can reach** (public IP, not loopback — the agent refuses private destinations by design):
   `ASTERLINK_SINK_TOKEN=<random> ./asterlink-trafficsink-linux-amd64 --listen :8088 --udp-listen :8089`, firewalled to the node and your test machines.

```powershell
# No network change, no elevation (independent client only). Windows or macOS; build from CI artifacts or `--suite grants`.
$env:ASTERLINK_BILL_API_BASE   = 'https://demo.hyshentou.cn/api/v1/client'
$env:ASTERLINK_BILL_EMAIL      = 'test@test.com'
$env:ASTERLINK_BILL_PASSWORD   = Read-Host 'password'
$env:ASTERLINK_BILL_SINK       = 'http://<sink-ip>:8088'
$env:ASTERLINK_BILL_SINK_TOKEN = Read-Host 'sink token'
.\asterlink-billtest.exe --grants --spend-test-quota --udp-sink <sink-ip>:8089 --idle-expiry --output .\grants-1

# Operator cases: open flows, then change the server side within the window (once per cause)
.\asterlink-billtest.exe --grants --spend-test-quota --udp-sink <sink-ip>:8089 --hold-seconds 120 --hold-case B05-expire   --output .\grants-expire   # end the plan
.\asterlink-billtest.exe --grants --spend-test-quota --udp-sink <sink-ip>:8089 --hold-seconds 300 --hold-case B05-quota --hold-drive --output .\grants-quota  # quota ~20 MB
.\asterlink-billtest.exe --grants --spend-test-quota --udp-sink <sink-ip>:8089 --hold-seconds 120 --hold-case B07-outage --output .\grants-outage  # stop the API / kill the agent

# B06 across machines: same instant on both
.\asterlink-billtest.exe --grants --spend-test-quota --race-sessions 3 --race-at 2026-10-14T21:30:00+08:00 --output .\grants-race

# B04 over the production path (elevated Windows, company VPN disconnected, adds --allow-network-changes)
.\asterlink-billtest.exe --grants --spend-test-quota --tun --allow-network-changes --output .\grants-tun
```

Exit code `0` = all PASS, `1` = a case FAILed, `3` = BLOCKED (nothing proven). Report: `<output>\billing-report.json` (redacted).

`--udp-sink` tests XUDP over the node connection, **not an OS TUN UDP transfer**. Omitting it still means UDP is NOT_TESTED.
Use a sink IP for deterministic testing; the pinned Mihomo UDP adapter resolves destination names on the client.
The node must permit outbound UDP to the sink, and its private-target guard remains enabled on public deployments.

### Your company VPN

* The unit tests, CI and the loopback evidence are unaffected.
* The independent-client cases (no `--tun`) change no route or DNS, so they may run with the VPN up, **if** the node and the sink are
  reachable through it; a VPN that blocks or reroutes them gives connection failures reported as `FAIL`/`BLOCKED`, and the cut latencies
  would then include the VPN's path. Prefer a VPN-free network for numbers you intend to keep.
* `--tun` needs the VPN **disconnected** on the Windows test machine (a VPN adapter makes `Preflight` stop with `vpn_conflict` → `BLOCKED`).

## Limits worth knowing

* The reference forwarder counts application payload only; the client Mihomo counts slightly more. **The node is the bill.** The window
  shows local usage beside the server-confirmed balance.
* Budget is returned only after the node confirms a revocation, so on a tiny quota a new grant right after revoking can briefly see
  `quota_exhausted`; the tool waits for the confirmation before asking again.
* Grants are minted per connection; switching line, disconnecting or quitting revokes the grant first (existing Core behaviour).
* Not proven by this maintenance: Windows 10/11 real-machine TUN UDP; cross-machine race; real public TLS-node deployment; payments and
  invoicing — those remain release gates in `handoff_app_windows.md` §13. The pinned-client **protocol-level** NodeAgent XUDP regression is now automated.
