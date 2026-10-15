# Android client — round 1 foundation

Status of round 1 of `handoff_app_android.md`: **engineering skeleton**. The Go Core and the pinned Mihomo are
bound into the app with gomobile and answer in-process; the app shows the Core's build identity. There is no
login screen, no VpnService and no TUN: the Android engine reports `tun_unavailable` on any connect attempt, so
nothing in this build can show "connected" or move traffic.

## What exists

```text
apps/android/
  settings.gradle.kts / build.gradle.kts / gradle.properties
  gradle/libs.versions.toml        every library and plugin version
  gradle/wrapper/                  Gradle 9.8.1 wrapper, distribution SHA-256 pinned
  toolchain.lock.json              every pinned tool and how it was verified
  .env.example                     the same three public build keys as macOS/Windows
  build-logic/                     included build: ClientEnv (.env parser, env-v1 rules) as the
                                   `asterlink.client-env` plugin, with tests on shared/contracts/env-v1.json
  app/                             com.asterlink.vpn, Kotlin + Jetpack Compose (Material 3)
    core/       Protocol.kt (ipc-v1 documents, strict, Long numbers), CoreClient.kt, CoreBridge.kt (gomobile)
    vpn/        HostNetwork.kt (default network for the Core), AndroidPlatform.kt (Core callbacks)
    ui/         theme/ (design tokens of handoff 9.2), foundation/ (the round-1 screen)
    src/test/         JVM tests (protocol, client, host-network field set)
    src/androidTest/  CoreBindingTest: the real AAR on an emulator
native/core/
  mobile/                          the gomobile binding: Start / Call / Stop + the Platform callback interface
  internal/network/host.go         HostSnapshot: the host's default-network report, strictly decoded
  internal/network/network_android.go
  internal/engine/{platform,sysdns,ipv6}_android.go
scripts/build-native-android.py    --target aar | apk
scripts/test-native-android.py     --suite foundation | unit | emulator
.github/workflows/android.yml      Linux build job + KVM emulator job
```

## Go Core: the Android platform split

`GOOS=android` implicitly satisfies the `linux` build tag. Before this round an Android build therefore compiled
the macOS implementation (`sysdns.go` driving `networksetup`, `ipv6_route.go` driving `/sbin/route`,
`platform_unix.go` waiting for a `utun` device) and the do-nothing network placeholder — it built without error,
which is exactly what made it dangerous.

| File | Before | Now |
| --- | --- | --- |
| `engine/platform_unix.go`, `sysdns.go`, `ipv6_route.go` (+ their tests) | `!windows` | `!windows && !android` |
| `network/network_other.go` | `!darwin && !windows` | `!darwin && !windows && !android` |
| `engine/platform_android.go` | — | `platformSupported() == false`: connect fails with `tun_unavailable` until the round-2 fd path exists |
| `engine/sysdns_android.go` | — | no system change; records the default network's resolvers for the control plane |
| `engine/ipv6_android.go` | — | no IPv6 address when the node cannot carry it → Android blocks the address family |
| `network/network_android.go` | — | sockets are "bound" with `VpnService.protect(fd)`; the default network comes from the host |
| `managed/platform.go` | android → `linux` | android → `android` (the login request's `platform`) |

`mobile/platform_split_test.go` checks the file sets with `go/build` for android/arm64, amd64 and arm on every
host, and that darwin/windows/linux keep theirs.

`network.HostSnapshot` (`host.go`) is how the app reports the default network (`{"available","interface","index",
"addresses","dns","metered"}`); an Android 11+ app cannot read netlink routes. It is decoded strictly (unknown
fields, bad addresses, an available network without a link are errors). Its signature uses the same rules as a
desktop interface (IPv4 individually, IPv6 by /64), so a privacy-address rotation does not disconnect.

## The binding (`native/core/mobile`)

```go
type Platform interface {           // Kotlin: vpn/AndroidPlatform.kt
    Protect(fd int32) bool
    OpenTun(specJSON string) int32  // round 2
    NetworkSnapshot() string
}
func Start(p Platform, buildJSON string) error  // {"cache_dir": "<Context.getCacheDir>"}
func Call(requestJSON string) string            // one ipc-v1 document in, one out
func Stop()
const CodeNotStarted = "core_not_started"
```

* **One decoder.** `ipc.ServeWith` now calls `ipc.HandleOne`, and so does `mobile.Call`. Strict decoding, the
  protocol version, per-command limits and the generation echo cannot drift between the pipe and the binding.
  `HandleOne` additionally answers what the framing layer would have refused (empty body → `invalid_request`,
  over 2 MiB → `payload_too_large`), because an in-process caller has no stream to abandon.
* **Same session.** `Start` builds the same `managed.Manager` + `service.Service` the desktop Core runs.
  The API base, channel and app version travel in `login` exactly as on macOS/Windows; `Start` takes only
  process facts (`cache_dir`, because Android has no writable `TMPDIR`; the engine's Mihomo home lives there).
* **Before Start / after Stop** `hello` and `shutdown` answer as usual and every session command answers
  `core_not_started`. `shutdown` through `Call` ends the session like `Stop`.
* **Serial commands**, as on one pipe. `Stop` cancels a command in flight (a 90-second connect) before the
  bounded shutdown, so it never waits the command out.
* gomobile exports only `Start`, `Call`, `Stop`, `Platform` and `CodeNotStarted` (Java package
  `com.asterlink.core.mobile`); errors are unexported so they do not widen the Java surface.

The `connect`-reaches-the-handler question from the plan ("can `read_snapshot` run concurrently with
`connect`?") is answered conservatively for round 1: no. Commands are serialised; round 3's SessionController
polls between commands, exactly like the macOS and Windows GUIs over their single pipe.

## Toolchain (details and evidence: `apps/android/toolchain.lock.json`)

| Item | Pinned | Note |
| --- | --- | --- |
| Go | 1.26.6 | same as the desktop jobs |
| golang.org/x/mobile | `v0.0.0-20250218173823-21e291c9c26e` | see below |
| NDK | 28.2.13676358 | 16 KB ELF alignment by default |
| compileSdk / targetSdk / minSdk | 37 / 36 / 26 | `platforms;android-37.0` |
| AGP / Gradle / Kotlin / Compose BOM | 9.4.1 / 9.8.1 / 2.4.21 / 2026.09.00 | newest stable on 2026-10-15 |
| JDK | 17 (Homebrew OpenJDK locally, Temurin in CI) | Android Studio is optional |
| SDK | `/opt/homebrew/share/android-commandlinetools` | the only SDK on the dev Mac |

**Why not the newest x/mobile.** `gomobile bind` needs `golang.org/x/mobile` in the bound module's build list.
The 2026-10-11 x/mobile requires x/crypto 0.58, x/net 0.61, x/sys 0.49 and `go 1.26.0`; minimal version selection
would have moved the macOS and Windows Core builds onto those versions as a side effect of adding Android.
Commit `21e291c` (2025-02-18) is the newest whose requirements equal what the Core already links, so it adds
only build-time tool modules (x/tools 0.30, x/mod 0.23, x/image, x/exp/shiny). `go version -m` on the darwin
`asterlink-core` shows x/crypto 0.33, x/net 0.35, x/sys 0.30 before and after; the foundation suite fails if
those lines ever leave `native/core/go.mod`. gomobile and gobind are built from that pin (`go.mod` `tool` block)
into `.runtime/android/bin`; nothing is installed globally.

**One SDK.** Homebrew's `android-commandlinetools` is the single `ANDROID_HOME`; the emulator and system images
were added to it. If Android Studio is installed later, point it at the same directory instead of letting it
create a second SDK.

## Build

```sh
export JAVA_HOME=/opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home
export ANDROID_HOME=/opt/homebrew/share/android-commandlinetools
python3 scripts/build-native-android.py --target aar --check-reproducible   # Core AAR (+ second bind compared)
python3 scripts/build-native-android.py --target apk                        # Debug APK
```

The AAR and its record (`core-build.json`: versions, per-ABI `.so` size/SHA-256/ELF machine/LOAD alignment, AAR
SHA-256) go to `apps/android/build/core/`, which is never committed. Gradle does not run gomobile; its
`verifyCoreAar` task (a dependency of `preBuild`) refuses an AAR whose SHA-256 differs from the record.
Release builds are refused until round 6, and Gradle refuses a release build from `.env.example`.

### 16 KB page alignment

`build-native-android.py` reads every `libgojni.so` with the NDK's `llvm-readelf -lW` and fails if any `LOAD`
segment is aligned below `0x4000`; it also checks the ELF machine against the ABI. For the APK it runs
`zipalign -c -P 16 4` and fails if a `.so` is compressed (`useLegacyPackaging = false` keeps them stored, so
they are mapped directly from the APK). To check by hand:

```sh
$ANDROID_HOME/ndk/28.2.13676358/toolchains/llvm/prebuilt/darwin-x86_64/bin/llvm-readelf -lW libgojni.so | grep LOAD
$ANDROID_HOME/build-tools/36.0.0/zipalign -c -P 16 -v 4 app-debug.apk
```

### Size baseline (round 1, Debug, arm64-v8a only)

| Artifact | Size |
| --- | --- |
| `libgojni.so` (Go Core + Mihomo, `-s -w -trimpath`) | 25.1 MiB uncompressed |
| `asterlink-core.aar` | 8.8 MiB |
| `app-debug.apk` (stored `.so`, no R8) | 36.5 MiB |

The `.so` is stored uncompressed on purpose (page-aligned mapping). Round 6 measures the release APK
(R8, ABI split). Two binds of the same commit produce a byte-identical `libgojni.so` (`--check-reproducible`).

## Tests

| Suite | What it proves |
| --- | --- |
| `test-native-android.py --suite foundation` | lock ↔ catalog ↔ wrapper ↔ go.mod ↔ workflow; the desktop Core's x/ dependencies unchanged; `.env.example` valid and `.env` ignored; the Go platform split; `go vet` for android/arm64 and android/amd64; manifest baseline (backup off, extraction rules cover every domain, no cleartext, system CAs only, no VPN declarations yet) |
| `--suite unit` | Go host tests of `mobile`, `ipc`, `network`, `managed` (the binding answers every `ipc-v1.json` case byte-identically to the pipe; login carries the build's platform; Stop interrupts a command in flight); Gradle `build-logic` env-v1 tests; app JVM tests |
| `--suite emulator` | on an AVD: instrumented `CoreBindingTest` with the real AAR; the launched app shows the Core version and Mihomo commit; the binding tests cross-compiled for the device, where `platform=android` is asserted on the login body; device API/ABI/page size recorded |

The desktop entry points are unchanged and still pass with the shared changes: `go test -race ./...`,
`go vet` (also `-tags fakeengine`, windows/amd64, linux/amd64), `scripts/test-native.py`,
`scripts/test-native-macos.py --suite unit`; `ci.yml` and `windows.yml` run on the PR.

## Network notes for this machine

The emulator's traffic leaves through the Mac. While the office VPN is connected:

* Downloads from `services.gradle.org` ran at about 20 KB/s and `sdkmanager` at about 30–100 KB/s, while each
  HTTPS connection to `dl.google.com` was limited to roughly 0.5 MB/s. The Gradle distribution was fetched from
  the Tencent mirror and checked against the official SHA-256 before the wrapper used it; SDK archives were
  fetched with parallel ranged requests through a local proxy and installed by `sdkmanager` itself, which checks
  every archive against the SHA-1 in Google's repository XML. Off the VPN neither is needed.
* Round 1 does not depend on the route: nothing contacts the AsterLink API or a node. From round 2 on (real TUN,
  real nodes) the office VPN and the macOS AsterLink client must be off, or results are polluted by a tunnel in
  a tunnel (`handoff_app_android.md` 3.4).

## Not in round 1

VpnService, TUN fd, protect for Mihomo's outer sockets (`dialer.DefaultSocketHook`), the Android config template
variant (round 2); login/remembered password/SessionController/real UI (round 3); metering UI (round 4); the
suspend gate (round 5); signing, R8, ABI splits, licences (round 6). The 16 KB-page, API 29 and API 26 system
images are not installed yet (needed in rounds 2 and 6).
