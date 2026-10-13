# Windows Service, restricted IPC and install authorisation (round 3)

Status: **implemented; compiled on macOS, unit-tested on macOS (218 tests) and exercised on Windows Server 2025 by CI.**
Nothing here has run on a Windows 10/11 client yet, and no UAC prompt has been clicked by a human (see section 9).
No tunnel is started anywhere in this round: a Core launched by the service only answers `hello` / `read_snapshot`,
and the GUI never shows "connected".

## 1. Processes and who trusts whom

```text
AsterLink.exe (WPF, ordinary user, asInvoker)
   │  \\.\pipe\AsterLink.Service.v1   Identification-level client; the server is verified BEFORE any byte is sent
   ▼
AsterLink.Service.exe  (SCM, LocalSystem, Session 0)   ←── the only privileged code; takes no arguments as a service
   │  two anonymous pipes, explicit handle list (+NUL), minimal environment, Job Object (kill on close)
   ▼
asterlink-core.exe  (Go: session / catalog / metering / grant; Mihomo + Wintun embedded)
```

The service never logs in, never parses node data, never meters. It checks, supervises and relays. The Core stays the
single owner of HTTP session, configuration and the Meter.

## 2. Layout and the manifest (`InstallLayout`, `ReleaseManifest`)

One layout for a stage tree and for the installed product (so the GUI can tell where it runs from its own location):

```text
<root>\app\AsterLink.exe  …            GUI + self-contained runtime
<root>\service\AsterLink.Service.exe …  service + installer verbs
<root>\core\asterlink-core.exe          Wintun is embedded in the exe: no DLL is ever searched for
<root>\build-config.json                the three public build keys, frozen at build time
<root>\manifest.json                    SHA-256 + size of every file above; channel; version; protocol
```

* The only root a service will run from is `%ProgramFiles%\AsterLink`. Started from anywhere else it logs and stops.
* Roles `gui`, `service`, `core`, `config` must each exist exactly once at their fixed path. A file in `app/`,
  `service/` or `core/` that the manifest does not list (a planted `version.dll`, a second Core) is an integrity failure.
* `channel` is `internal` (development installer, **unsigned**) or `release`. This build refuses a `release`
  manifest because Authenticode verification does not exist until round 8: the two can never be confused.
* Trust in the manifest comes from the directory ACL (`AclPolicy`): owner and every writer must be SYSTEM,
  Administrators or TrustedInstaller; any other principal with write / delete / permission / ownership rights
  makes the tree untrusted. Reparse points anywhere in the tree are refused.

## 3. The pipe

| Defence | Where | Tested by |
| --- | --- | --- |
| Explicit DACL: NETWORK **denied**; SYSTEM + Administrators full; INTERACTIVE read/write; no Everyone / Users / Authenticated Users | `ServiceEndpoint.BuildSecurity` | `PipeSecurityTests` (Windows) |
| First instance created with `FILE_FLAG_FIRST_PIPE_INSTANCE`; the service stops rather than serve beside a squatter | `ServiceEndpoint.Create`, `PipeListener` | `The_service_pipe_name_cannot_be_squatted…` |
| First frame is read (limit 4 KiB) before the caller is inspected, because Windows only allows impersonating a client that has sent data | `ClientSession.AdmitAsync` | e2e |
| Caller facts come from the OS: pipe client PID and session, process token (user SID, integrity), `RunAsClient` SID, image path from the process handle | `ServiceEndpoint.Inspect`, `ProcessIdentity` | `Inspect_reports_the_real_caller…` |
| `PeerPolicy`: image path = installed GUI, image SHA-256 = manifest, token SID = pipe client SID (catches PID reuse), not SYSTEM/LOCAL SERVICE/NETWORK SERVICE/ANONYMOUS, session > 0 and equal, integrity ≥ Medium | `PeerPolicy` (pure) | unit tests for every refusal reason + e2e fake GUI |
| Cheap parse first; the whole-tree hash is only spent on a caller that passed the identity check | `InstallVerifier.LoadMetadata` | – |
| `CommandPolicy`: known command, per-command size limit (same table as the Go Core), strict envelope (no extra / duplicate fields), `protocol_version`, and for `login` the `api_base` + `channel` must equal the protected build config — the GUI cannot choose where the password goes | `CommandPolicy` (pure) | unit tests (incl. every login-target mismatch) + e2e |
| Frames over the 16 KiB largest request limit end the connection after one error reply; over 4 KiB on the first frame, silently | `ClientSession` | e2e `S03-*` |
| One owner: a second user/session gets `owner_busy`; the same user reconnecting waits for the old connection and its Core to be gone first | `OwnerGate` | unit tests |
| Version gate: GUI (hello payload), service, Core (real hello) must all equal the manifest version | `VersionGate` | unit + e2e |

**GUI → service.** `ServiceConnection.ConnectAsync` connects at `Identification` level (a fake server cannot act as the
user), then `VerifyServer` requires: pipe server PID = the PID the SCM reports for `AsterLinkService`; SCM binary path =
the quoted protected service path with no arguments; account `LocalSystem`; that process runs from the protected path as
SYSTEM. Only then is `hello` (and later a password) sent.

## 4. Worker supervision (`WorkerProcess`, `CoreHost`)

* Before launch: full `InstallVerifier.Verify`; the Core file is opened with `FileShare.Read` and hashed through that handle, and
  the handle is held while the Core runs (the file that was hashed is the file that runs).
* `CreateProcess` **suspended** with `PROC_THREAD_ATTRIBUTE_HANDLE_LIST` (only NUL + the two pipe ends are inherited), an explicit
  environment (`SystemRoot`, `windir`, `SystemDrive`, `PATH`, `TEMP`/`TMP` = `%ProgramData%\AsterLink\run`, `ProgramData`, two IPC handles),
  no window, command line = the quoted path only. It is assigned to a Job Object with `KILL_ON_JOB_CLOSE` while still suspended.
* The recovery record is written (atomic replace, flushed to disk) **while the Core is still suspended**; if that fails the Core is
  killed unrun.
* The Core's `hello` must report `asterlink-core`, protocol 1 and the manifest version.
* Stop: closing the command pipe is EOF → the Core settles and exits; after 15 s the job is terminated. The record is deleted only after
  the process is really gone. A Core that dies mid-request gives the GUI `worker_exited`; the next request starts a fresh Core that
  knows no login (the GUI must sign in again — no token is carried over). A hung Core (120 s) is killed: `worker_unresponsive`.
* If the service process dies for any reason the OS closes the job handle and the Core dies with it.

## 5. Recovery on start (`StartupRecovery`, `RecoveryPolicy`)

`%ProgramData%\AsterLink\session.json` (SYSTEM + Administrators only) names one Core: PID, **start time**, path, owner SID. No credential field exists.
On service start (boot or SCM restart): no record → empty the private run directory; record + no such process → clear; record + process whose
start time **and** image path match → terminate exactly that process, clear; record + a different process under that PID → not ours, clear only;
unreadable record → **block** (`recovery_blocked`), keep the evidence, until `repair` archives it as `session.corrupt-<time>.json` and restarts the service.
The service never connects anything at boot and never restores a login. Round 2 found no persistent network state on Windows (adapter,
routes, DNS and WFP filters die with the Core), so no network restoration is done here; `--recover-network` remains the single place to add it.

## 6. Development installer (`AsterLink.Service.exe <verb>`)

Internal channel only. Verbs: `install --stage <dir>`, `uninstall`, `repair`, `status`. All but `status` need an elevated token (exit 5).

* `install`: verifies the whole stage (hashes, channel, version = this binary) **before touching the machine**; stops/removes an old
  service; creates `%ProgramFiles%\AsterLink` with a protected ACL (inheritance cut; SYSTEM+Administrators full, Users read/execute) while empty;
  copies; re-verifies the installed copy; registers the service (`LocalSystem`, auto start, restart-on-crash 5 s/10 s/30 s); waits for `Running`.
  Any failure rolls back: service deleted, tree removed. A UAC refusal happens before any of this runs, so nothing is left.
* `repair` (what the GUI raises through UAC): runs from the installed copy only; re-applies the ACL, archives a corrupt record (and restarts the
  service so recovery re-runs), re-registers the service if needed, starts it. It **never replaces content**: a file that does not match the
  manifest returns exit 6 and needs a fresh install.
* `uninstall`: must run from a copy outside the install root (a program cannot delete itself); removes the service, the product's own
  directories (a reparse point is refused, unrelated files are left) and `%ProgramData%\AsterLink`. It does not touch Credential Manager
  (other users' credentials are not reachable from an elevated uninstaller — round 8 offers the per-user choice) or any shared driver.
* The GUI card (`ServicePanel`) maps measured facts to one message and one action (`ServiceHealthClassifier`): not installed → install (only if the
  GUI sits beside a stage), stopped → start, integrity/version/recovery errors → repair, GUI not in the installed root → open the installed copy.
  A declined UAC prompt (Win32 1223) says "no changes were made".

## 7. What the build proves, per level

| Level | Evidence |
| --- | --- |
| macOS | compile of all 6 projects; 218 policy/contract tests (path, manifest, ACL, peer, command, owner, version, recovery, health) |
| Windows Server 2025 (CI) | `AsterLink.Tests.Windows` (real pipe DACL, real caller inspection, squatting, ACL/junction/hash checks, recovery with real processes, real Go Core through `WorkerProcess`: hello, snapshot, job kill, EOF stop, suspended-record ordering, handle-less start) and `e2e-service.ps1` |
| Windows 10 Home 22H2 (user's machine) | NOT_RUN — see section 9 |

## 8. Known limits (also in the handoff record)

* **Unsigned.** The internal channel trusts a protected directory, not a signature. A formal installer, Authenticode on GUI/service/Core and
  a time stamp are round 8; a `release` manifest is refused until then.
* Hash/ACL checks run when a GUI connects and before each Core launch, not continuously; an administrator can always change files (outside the
  threat model, handoff 4.2).
* The service verifies the GUI by path + hash + token, not by Authenticode (same reason). A process that runs as the same user and injects into the GUI process is not detected.
* Log-off / user switch: the GUI of that session dies, its pipe closes, the Core is stopped. There is no `SERVICE_CONTROL_SESSIONCHANGE` hook yet.
* Several concurrent unauthenticated local connections each hold a pipe instance until they are refused (a local DoS of the pipe only; no privilege).
* The first-frame `hello` is read before any identity check; its size is capped at 4 KiB.
* `Case S01-remote` and `S01-other-user` need a second computer / a second interactive user and are **NOT_RUN** in CI.

## 9. For the Windows 10 test machine (no VPN interaction)

This round changes **no route, DNS, adapter or firewall rule**, so a company VPN does not affect it. Do not connect the company VPN anyway if
you also test the round-2 TUN harness. Create a restore point first (the harness installs and removes a service and writes under Program Files).
Download the `windows-internal-stage` artifact of the PR's Windows run, extract it to e.g. `C:\asterlink-kit`, then in an elevated PowerShell:

```powershell
powershell -ExecutionPolicy Bypass -File C:\asterlink-kit\scripts\e2e-service.ps1 -Stage C:\asterlink-kit\stage -Report C:\asterlink-kit\report.json
```

For the manual UAC check: run `C:\asterlink-kit\stage\app\AsterLink.exe` as a normal user → the service card offers "install"; click it, decline once
(expect "已取消管理员授权"), click again and accept; it installs, then offers to open the installed copy.
