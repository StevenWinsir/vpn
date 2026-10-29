# Windows installer, upgrade, uninstall and release hand-over (round 8)

Status: **code and CI-tested installer pipeline; release is NOT approved.** There is no trusted code-signing certificate, no clean
Windows 10/11 machine run, and no managed node (round 7), so the output is the **internal channel**. A self-signed *test*
certificate proves the signing pipeline only; it is trusted by nobody and gives no SmartScreen reputation.

> 2026-10-28 (handoff_app_standardization.md rounds 4-6): the wizard and every message are Simplified Chinese
> (`Packaging/Languages/ChineseSimplified.isl`, reworded from the vendor's community translation); the installer, the exe, the
> Start-menu shortcut, the tray and the wizard images all carry the brand icon generated from `frontend/brand/mark.svg`
> (`docs/brand-and-copy.md`); the service shows as "AsterLink 后台服务" in the Services list (`ServiceConstants`); closing the
> window always hides the app to the tray (`docs/windows-client-ui.md`). Upgrade over a running copy now says, in Chinese, to quit
> from the tray icon ("^" if hidden). Customer-visible text (installer messages, service name and description, product and
> publisher fields) is guarded by `scripts/test-app-copy.py`; the public folder holds only the installer and `SHA256SUMS`.

## 1. What the installer is

`AsterLink-Setup-x64-<version>.exe` (Inno Setup 6.7.1, `apps/windows/Packaging/setup.iss`) is a carrier and a user interface. It
unpacks the stage tree into its own temporary folder and runs **one** privileged program, the service executable's `install --stage`
verb, at the start of Inno's install step (`ssInstall`), before Inno has recorded anything. The verb (all of it from the earlier rounds, plus the upgrade logic below):

1. verifies every staged file against `manifest.json` and refuses a wrong channel/version **before touching the machine**;
2. refuses a **downgrade** (exit 7, `downgrade_refused`) unless run by hand with `--allow-downgrade`;
3. stops the old service (the Core settles and closes the session; nothing connected survives an upgrade, no login is restored);
4. **renames** the old `%ProgramFiles%\AsterLink` to `AsterLink.previous` (the old version stays whole and verifiable),
   creates the protected tree, copies Inno's `setup\` folder (the uninstaller) over from the old tree, copies, re-verifies the installed copy, registers the service (LocalSystem, auto start,
   restart-on-crash) and waits for `Running`;
5. on **any** failure puts the previous version back, verifies it again and restarts its service (`rolled back: …` in the message);
   a rollback that itself fails says `ROLLBACK FAILED` and asks for a re-run;
6. deletes `.previous` only after the new version is proven. A `.previous` left by a power loss is settled at the next install
   (restored if the live tree is missing, dropped otherwise).

Inno itself only adds, and only after the verb succeeded (a failed verb ends Setup with exit code 3 and leaves none of them): the
Add/Remove Programs entry, the all-users Start-menu shortcut, `{app}\setup\unins000.exe`, and an
optional "Start AsterLink" checkbox that starts the GUI **as the original, non-elevated user** (`runasoriginaluser`, never with the
administrator token; skipped in silent installs). x64 only (`x64os`, no ARM64), Windows 10 2004 (build 19041) or newer.

The install folder is fixed: the service only runs from `%ProgramFiles%\AsterLink`.

## 2. Install, upgrade, downgrade

* **Install**: run the Setup, approve one UAC prompt (a standard user enters an administrator's credentials; the cleanup in §3 then
  belongs to the *standard* user, which is correct). Cancelling UAC means nothing ran and nothing changed.
* **Upgrade**: download the newer Setup and run it over the old version. If AsterLink is open, Setup asks to quit it from the tray
  (Retry/Cancel); silent mode fails instead of guessing. There is no auto-updater and no resident updater process (handoff decision).
* **Downgrade**: refused. To go back, uninstall first, or run the old stage's service verb by hand with `--allow-downgrade`.
* **Rollback after a failed upgrade** is automatic (§1 step 5). To roll back to an *older release on purpose*: uninstall, install the
  older Setup.
* **Repair** (stopped service, wrong ACL, corrupt recovery record) stays in the app's service card (UAC) as in round 3; a file that no
  longer matches the manifest needs a reinstall.
* Silent forms (for IT): `Setup.exe /VERYSILENT /SUPPRESSMSGBOXES /NORESTART /LOG=setup.log`; the verb's JSON answer or error is in the
  log (`install verb: …`).

## 3. Uninstall

Apps & features → AsterLink → Uninstall (or `unins000.exe`). The uninstaller:

1. refuses while AsterLink is open;
2. asks, default **No**: "also remove the saved passwords and preferences of the Windows user running this uninstall?" Silent:
   `/REMOVEUSERDATA=1` (remove) or `0`/absent (keep). If yes, the installed GUI runs `--cleanup-user-data --passwords --preferences`
   **as the account that approved the uninstall** (Inno's uninstaller has no original-user token; with a normal admin-approval
   prompt that is the same user. A standard user who types another administrator's password gets that administrator's AsterLink
   items cleaned instead and keeps their own): it removes that account's `AsterLink/*` Credential Manager items (only that prefix, other
   applications' items are never touched) and `%LocalAppData%\AsterLink` (settings **and the device id**: a reinstall registers as a
   new device and may use a device slot until the server side releases the old one);
3. runs the service's `uninstall` verb from a **copy outside the install folder** (a program cannot delete itself): removes the service,
   the product's own folders, the leftover upgrade folder and `%ProgramData%\AsterLink`. Unrelated files in the folder are left.
   If the verb refuses (for example the Core is still running) the uninstall stops before Inno removes its entry, so it can be run again.
   It removes no shared driver: Wintun is embedded in the Core and loaded from memory, nothing is installed system-wide.

The elevated uninstaller never walks other Windows users' vaults; **other users' saved passwords stay in their own vaults** until
they remove them (Credential Manager → Generic credentials → `AsterLink/…`). Uninstall changes no route, DNS or adapter; a failure
is reported in a dialog/log and is never described as "network restored".

## 4. Signing

`scripts/windows/sign-files.ps1`, used by `package-native-windows.py --sign <mode>`:

| Mode | Use | Secrets |
| --- | --- | --- |
| `none` (default of the packager) | unsigned internal build | – |
| `test` | self-signed certificate "AsterLink TEST signing - NOT A TRUSTED RELEASE", no time stamp | none; never ship |
| `thumbprint` | a certificate already in the store or on a token (EV / HSM / cloud signing) | `ASTERLINK_SIGN_THUMBPRINT`, `ASTERLINK_TIMESTAMP_URL` |
| `pfx` | a PFX file | `ASTERLINK_SIGN_PFX`, `ASTERLINK_SIGN_PFX_PASSWORD`, `ASTERLINK_TIMESTAMP_URL` |

Secrets come from the protected signing environment, never from argv or any `.env` in the repository. thumbprint/pfx **require** a
time-stamp URL and demand `Status = Valid`. Order: our own binaries (`AsterLink*.exe/.dll`, `asterlink-core.exe`; Microsoft's runtime
files keep Microsoft's signature) are signed → `manifest.json` is written (so it hashes the *signed* files) → Inno builds the Setup and
signs it and the uninstaller (`SignedUninstaller`) through the same script.

**The service does not verify Authenticode.** Trust at run time still comes from the protected directory ACL plus the manifest hashes
(rounds 3 and 6), and the manifest channel stays `internal`; `--channel release` is refused, so a test signature can never be mistaken
for a release. Authenticode verification in the service/GUI (peer and Core identity) is a separate piece of work to do together with
the real certificate.

## 5. What ships (`--release-dir`)

Since 2026-10-28 (handoff_app_standardization.md round 5, D15) the release folder is public and holds only
`AsterLink-Setup-x64-<v>.exe` and a `SHA256SUMS` that lists only it. Everything below lives in `<release-dir>-private`
(CI: `artifacts/windows/release/installer-private`, artifact `windows-release-private-INTERNAL`), is internal and is never
published; the files keep their content. The stage is published without PDBs, Source Link or the commit suffix on the product
version, and the Core is built with `-s -w -buildvcs=false` and no engine revision.

Internal: `release-info.json` (version, channel, signing mode, `trusted_signature: false`, source
commits, installer hash, open gates), `manifest.json` (a copy; the installed one stays in the stage), `dependencies.cdx.json` (CycloneDX-shaped inventory: Go modules linked into the Core for
windows/amd64, the .NET runtime version, Wintun, Mihomo; there are no third-party NuGet packages), `THIRD_PARTY_LICENSES.txt` (the same
windows/amd64 module set; a module whose published archive has no license file uses the reviewed upstream copy in
`scripts/third_party_licenses/`, which names its source),
`SOURCE.txt` and the two GPL source archives (`git archive` of this repository and of the pinned Mihomo commit, with rebuild
instructions), `README-内部资料.txt` and a `SHA256SUMS` of the folder. `release-check.ps1` (`-PrivateDir`, default `<ReleaseDir>-private`)
re-reads both `SHA256SUMS` independently, requires the public folder to hold only the installer, and scans the text files for
secret-looking content; `test-native-windows.py --suite release` additionally scans the stage the installer carries
(`release.scrub_*`: no private-only file, no engine marker in a text file, binaries within `scripts/release-scrub-baseline.json`).

Build and test:

```sh
# Windows x64 with Go, .NET SDK 10.0.401 and Inno Setup 6.7.1 (ISCC on PATH or in %ProgramFiles(x86)%)
py -3 scripts/package-native-windows.py --channel internal --installer --sign test --release-dir artifacts\native-windows\installer
py -3 scripts/test-native-windows.py --suite release --allow-service-install --sign test      # elevated; installs and removes the product
```

## 6. What was and was not verified

| Case | Evidence |
| --- | --- |
| Upgrade classification, cleanup choices (unit), checksum writer/reader, `setup.iss` contract, release channel still refused | macOS and Linux: `--suite release` (portable part), 12 + C# tests |
| Real Setup: silent install, service registration, Add/Remove entry, shortcut, no GUI started, chain GUI→Service→Core | CI on Windows Server 2025 (`release-check.ps1 -Install`) |
| Reinstall over itself; Setup refused while the app is open; uninstall refused while the app is open | same |
| Failed upgrade → rollback to the previous verified, running version (forced by holding a file handle in the old tree) | same |
| Downgrade refused with nothing changed | same |
| Uninstall keeping / removing the current user's saved password and preferences; other application's credential untouched | same (CI runner user is an administrator) |
| TEST signatures present on Setup and every own binary; tampering detected; a test signature does **not** validate as trusted | same |
| **R01 clean Windows 10/11 machine**, real UAC (standard user + admin credentials, cancel UAC), default Defender / Secure Boot / memory integrity | **NOT_RUN** |
| **R02** other Windows users' credentials after uninstall, upgrade from a *different older real release*, interrupted upgrade by power loss | **NOT_RUN** (only the single-user and the forced-failure rollback paths are covered) |
| Trusted signature, time stamp, SmartScreen / Defender behaviour | **BLOCKED**: no certificate |
| Windows 11 | NOT_RUN |

## 7. Gates before a public release (none is closed by this round)

1. A code-signing certificate (OV/EV) and time stamp, then `--sign thumbprint|pfx`, then service/GUI-side Authenticode verification.
2. R01/R02 on clean Windows 10 22H2 and Windows 11 machines with default security settings; record Defender/SmartScreen prompts as they are
   (do not advise switching protection off).
3. Round 7: a managed node with authoritative metering. Only VLESS(TCP/UDP)+TLS through the reference forwarder is enforced node-side.
4. **Legal review**: Mihomo is GPL-3.0 (source shipped here); Wintun's prebuilt-binary licence restricts redistribution/extraction
   (`toolchain.lock.json`, `wintun.license_note`): decide whether the in-memory embedding by sing-tun is acceptable for a commercial
   installer or ship the unmodified `wintun.dll` next to the Core; Inno Setup commercial licence; `github.com/rasky/go-lzo`
   (linked by Mihomo) ships only GPL-2.0 text (`LICENSE.gpl`), so confirm it may be combined with GPL-3.0 Mihomo.
5. Website operations (payments, backups, MFA, monitoring) are outside this client.

## 8. For the Windows test machine

Nothing in this round touches routes, DNS or adapters, so a company VPN does not interfere with install, upgrade or uninstall checks.
Create a restore point, download the `windows-installer-test-signed` artifact of the PR's Windows run, and:

```powershell
# elevated PowerShell; installs and removes the product
powershell -ExecutionPolicy Bypass -File scripts\windows\release-check.ps1 -ReleaseDir C:\kit\installer -ExpectSignature test -Report C:\kit\report.json -Install
```

For the manual R01 check use a **standard** user: double-click the Setup, enter an administrator's credentials, install, start AsterLink
from the Start menu, sign in; then upgrade (run the same Setup again), then uninstall once with "No" and once with "Yes" to the
saved-data question, and note any Windows Security / SmartScreen message verbatim. (A self-signed test certificate will show as an
unknown publisher; that is expected and is why the real certificate is a gate.)
