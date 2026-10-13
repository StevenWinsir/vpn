# Windows x64 client — round 1 foundation

Status of round 1 of `handoff_app_windows.md`: **engineering skeleton only**. There is no login, no node
list, no privileged service behaviour and no TUN. Nothing in this build can show "connected".

## What exists

```text
apps/windows/
  AsterLink.sln
  global.json                 SDK pinned to 10.0.401 (rollForward: latestPatch)
  NuGet.config                nuget.org only, source-mapped
  Directory.Build.props       warnings are errors; locked package restore
  toolchain.lock.json         every pinned version and how it was verified
  .env.example                the same three public build keys as apps/macos
  AsterLink.Shared/           net10.0   — Snapshot DTOs, IPC framing + messages, .env parser, platform report
  AsterLink.App/              net10.0-windows WPF, asInvoker, PerMonitorV2 DPI  (AsterLink.exe)
  AsterLink.Service/          net10.0-windows Worker Service, idle supervisor    (AsterLink.Service.exe)
  AsterLink.Tests/            net10.0   — xUnit, reads the real shared/contracts/*.json
.github/workflows/windows.yml
scripts/test-native-windows.py  (--suite foundation)
```

`AsterLink.Shared` and the test project deliberately target plain `net10.0`, so the contract logic is verified on
macOS and Linux as well as on Windows.

### C# contract coverage

* `snapshot-v1.json`: every shared fixture decodes. All integers are `long`, so values above 2^53 stay exact;
  fractional, string-typed or overflowing numbers are rejected. Go timestamps with nanosecond fractions parse.
  A structural test fails if a Snapshot model ever gains a field that could hold a secret.
* `ipc-v1.json`: 4-byte big-endian framing, 2 MiB ceiling checked before allocating, empty / truncated / oversize
  / dribbling-stream cases, command names equal to the contract.
* `ok=true` plus `action_error` is modelled as a failed action (`SnapshotReply.Succeeded == false`).
* `env-v1.json`: every case is run against the C# parser, in addition to extra path / fragment / IPv6 / symlink cases.

## Support matrix change (decision made in round 1)

The handoff plan proposed "Windows 10 22H2 + Windows 11". The official .NET 10 supported-OS list (updated
2026-09-28) names Windows 11 23H2/24H2/25H2/26H1 and, for Windows 10, only the 21H2, 1809 and 1607 *Enterprise/IoT*
editions. Windows 10 22H2 is not on it, and Windows 10 left mainstream support on 2025-10-14.

| Tier | Systems | Meaning |
| --- | --- | --- |
| Target | Windows 11 x64, 24H2 and 25H2 | What rounds 2–8 test and what release notes promise |
| Best effort | Windows 10 22H2 x64 with ESU | May work; not tested by .NET, not promised |
| Not supported | Windows 10 without ESU, Windows on ARM, 32-bit | No ARM64 product is added to make testing easier |

Changing the runtime to keep Windows 10 would mean shipping an unsupported runtime or hand-maintaining an older one,
which is the wrong trade for a paid product.

## What CI proves, and what it does not

`windows.yml` runs on `windows-2025`, which is **Windows Server 2025 x64**. GitHub offers no x64 Windows 10/11
client image (its only Windows 11 images are Arm64). The job:

* records the real OS build, architecture and whether the OS is a client SKU, and fails if it is not native x64;
* restores in locked mode, builds with warnings as errors, runs the xUnit suite;
* publishes `win-x64`, self-contained, without trimming, AOT or single-file, and checks both executables are PE32+ x64;
* launches `AsterLink.exe --self-test`, which must render its window and write an observed-facts report;
* runs `AsterLink.Service.exe --probe` (not installed as a service).

It does **not** prove Windows 10/11 client compatibility, UAC, SCM installation, Wintun, routing, DNS or any
networking. Those stay `NOT_RUN` until they run on a real, authorised x64 client machine.

## Round-2 prerequisites recorded here

* **Wintun**: 0.14.1 from wintun.net, published ZIP SHA-256 in `toolchain.lock.json`. Not downloaded in round 1.
  Round 2 must download, verify the hash, inspect the Authenticode signature of the amd64 DLL, and read the
  license text shipped inside the ZIP before vendoring.
* **Test machine**: an x64 Windows 11 (24H2/25H2) machine or an x64-hosted VM that can be reverted, plus a
  controlled SS/VLESS test node and a target to fetch through it. Without it round 2's TUN PoC is `BLOCKED`.
  Windows ARM64 under emulation cannot stand in for x64 driver behaviour.
* **Installer licence**: Inno Setup's vendor asks commercial users to buy a licence; decide the tool in round 8.
* **Go platform split** — the compile failures that `GOOS=windows GOARCH=amd64` currently produces are listed in
  `docs/windows-go-port-baseline.md`.
