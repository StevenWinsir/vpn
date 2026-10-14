# Windows metering and rate acceptance (round 5) — how to run it

Round 5 closes the loop *login → pick a line → connect → real usage → charge at the bound rate → stop*.
The loop itself is the shared Core (`managed.Manager` + the single `meter.Meter`) that macOS already
uses; on Windows the GUI reaches it through Service and IPC (rounds 3–4) and the engine is the Wintun TUN of
round 2. Nothing was duplicated for Windows. What round 5 adds is the **evidence tooling**, because until now
the real engine had never been measured against the real ledger:

| Tool | Where it runs | Purpose |
| --- | --- | --- |
| `asterlink-trafficsink` | a server the test node can reach (**not** the machine under test) | Serves an exact-size body, swallows an upload, counts both. The independent truth for "how many bytes crossed the node". |
| `asterlink-billtest` | the elevated Windows test machine | Drives the *production* Manager + engine against the *real* API/DB and compares the ledger with the formula. |

`asterlink-billtest` is a test tool: never packaged, refuses to run without `--allow-network-changes`, without
an elevated token, or while another VPN is up (the Core reports `vpn_conflict` → the case is `BLOCKED`).

> **Company VPN:** disconnect it on the Windows test machine first (same rule as round 2). A VPN adapter makes
> `Preflight` stop with `vpn_conflict`; nothing is changed and the report says `BLOCKED`. Your Mac and its VPN do
> not matter for building, unit tests or CI; only the live run needs the VPN-free Windows machine.

## What the live run proves

| Case | Expected |
| --- | --- |
| `A-LOGIN` | the account signs in on the real API (`platform=windows`) and receives a plan and nodes |
| `B01-nN-connect` | `Connect` passes the first metering ACK, brings the TUN up and a real forwarding probe succeeds |
| `B01-nN-live` | the usage the window shows already includes the transfer (same numbers the App binds to) |
| `B01-nN-sink` | the sink independently saw exactly the bytes sent and received (default 20 MiB up + 80 MiB down) |
| `B01-nN-ledger` | `charged units == (up delta + down delta) × rate_permille`, and `remaining == floor((total×1000 − used)/1000)` — **exact integers**, per distinct rate (default: one dedicated and one ordinary line) |
| `B01-nN-body` | counters cover the body, exceeding it only by protocol framing (≤ 10 %) plus bounded background traffic |
| `B01-rates` | the different rates were measured with the same procedure |
| `B02-lost-*` (`--ack-loss`) | one **real** `/traffic` reply is discarded; the retry carries the identical sequence and counters (`B02-retry`) and the server charges the batch once (`B02-once`) |
| `A04-watch` (`--watch-stop-seconds`) | after *you* edit the node/plan/quota in the admin panel the tunnel closes by itself with a stable reason code and the tail is settled |
| `A04-reconnect` / `A04-rebind` | a removed/expired cause refuses a new connection; an edited line reconnects at the **new** rate |

The overhead column in `B01-nN-body` is the answer to "does the counter equal the file size": it does not, by
design (Mihomo counts proxied payload including HTTP framing; the sink counts body only). The report states
the measured difference instead of hiding it.

Not covered here (later rounds): browser DoH / DHCP / multi-NIC / sleep (round 6), `node_reported` authoritative
billing (round 7). Admin-controlled `client_reported` nodes stay *development metering*; this round does not lift
the production guard.

## 1. Start the sink (on your server, once)

Build for Linux on the Mac (the CI artifact `windows-core-poc` also contains `asterlink-trafficsink-linux-amd64`):

```bash
python3 scripts/test-native-windows.py --suite billing --out artifacts/native-windows/billing
```

On the server, with a random token of at least 16 characters, and a firewall rule that admits **only** your test
machine and the proxy node to the port:

```bash
ASTERLINK_SINK_TOKEN='<random>' ./asterlink-trafficsink-linux-amd64 --listen :8088
```

Plain HTTP on purpose (the wire bytes then are payload plus HTTP framing). It sends bytes to anyone holding the
token, so stop it when finished.

## 2. Prepare the account

Use a **test** account whose plan covers at least the dedicated and an ordinary line, with enough quota for
`(20+80) MiB × 1.1 × rate` per line (the run is `BLOCKED` with the needed amount if the quota is too small).
`test@test.com` must hold a real subscription like any user; the admin role does not bypass it (A02).

## 3. Run (elevated PowerShell, VPN disconnected)

Secrets come from the environment only. Copy `asterlink-billtest.exe` from the CI artifact (unsigned: SmartScreen
"More info → Run anyway"; do not disable Defender).

```powershell
$env:ASTERLINK_BILL_API_BASE   = 'https://demo.hyshentou.cn/api/v1/client'
$env:ASTERLINK_BILL_EMAIL      = 'test@test.com'
$env:ASTERLINK_BILL_PASSWORD   = Read-Host 'password'   # typed, not stored
$env:ASTERLINK_BILL_SINK       = 'http://<server-ip>:8088'
$env:ASTERLINK_BILL_SINK_TOKEN = Read-Host 'sink token'
.\asterlink-billtest.exe --allow-network-changes --ack-loss --output .\bill-1
```

Useful flags: `--nodes "HK Dedicated,JP Standard"` (ids or exact names), `--upload-mib/--download-mib` (smaller for
a small quota), `--background-kib` (tolerated foreign traffic per direction, default 2048). Close browsers and
sync clients during the run; the machine's other traffic also rides the tunnel and is metered, which is the point
of the `body` tolerance.

Live admin-change check (A04/B03) — run once per cause and set the matching `--expect-stop`:

```powershell
# in another window you will, after CONNECTED is printed, disable the node in the admin panel
.\asterlink-billtest.exe --allow-network-changes --output .\watch-node-off --watch-stop-seconds 120 --expect-stop profile_required,node_unavailable
# edit the rate of the node             -> --expect-stop profile_changed   (and A04-rebind proves the new rate binds)
# end/expire the plan                   -> --expect-stop subscription_expired
# set a tiny quota, then:  --watch-drive --expect-stop quota_exhausted
```

`--expect-stop` takes a comma-separated list of acceptable reason codes. Exit code `0` = all passed, `1` = a case failed, `3` = `BLOCKED` (nothing was proven). The report is
`<output>\billing-report.json` (redacted: no token, password, address or URL; hosts appear as hash prefixes).
Please send that file and the console output back.

`py -3 scripts/test-native-windows.py --suite billing --allow-network-changes` runs the same thing from a
checkout (plus the unit tests) when the same environment is set.

## Recovery

`asterlink-billtest` closes the tunnel and settles the tail on exit, including Ctrl+C. If it is killed hard, the
adapter, routes, DNS and filters disappear with the process (round 2 `T09-kill`). A batch that could not be
acknowledged stays pending in the *server's* view only until the next login settles it; re-running the tool
does that.

## What the unit/CI part covers on every pull request

`go test` (race on macOS/Linux) over `meter`, `managed`, `apiclient`, `asterlink-billtest` and
`asterlink-trafficsink`: contract examples (20 MiB + 80 MiB → 104,857,600,000 / 52,428,800,000 units),
one-unit-off and double-charge detection, ACK loss and identical retry against a faithful fake API, a server that
double-bills or mis-bills being *caught*, node deleted / plan lost / rate edited while connected, `BLOCKED` on a
small quota. Hosted runners cannot create a TUN against the real API, so `billing.live` is `BLOCKED` there by
design and the real evidence is the report from the Windows machine.
