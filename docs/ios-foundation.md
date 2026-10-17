# iOS client foundation (round 1)

Round 1 of `handoff_app_ios.md`: the build chain from the Go Core and pinned Mihomo to a SwiftUI app with a packet
tunnel extension, verified on this Mac and in the iOS Simulator. There is no tunnel yet: the extension refuses every
start, and the Core's iOS platform answers `tun_unavailable`. Device work waits for an Apple Developer team and an
iPhone (handoff P1, P2).

## Layout

```text
apps/ios/
  AsterLink.xcodeproj          app, AsterLinkTunnel (packet tunnel extension), AsterLinkTests; Xcode 16+ synchronized
                               folders, so adding a source file does not change project.pbxproj; shared scheme AsterLink
  AsterLink/                   SwiftUI app: theme tokens, CoreBridge (MobileCall), IOSPlatform (round-1 stub), foundation page
  AsterLinkTunnel/             PacketTunnelProvider (round 1: refuses to start)
  AsterLinkTests/              CoreBindingTests (ipc-v1 through the linked Core), E-SIM-NE experiment
  Packages/AsterLinkKit/       business logic shared by app and extension; `swift test` runs on the Mac
  Config/                      Base.xcconfig, Info.plists, entitlements; Generated.xcconfig (from .env, not committed)
  Frameworks/                  AsterCore.xcframework + core-build.json (built, not committed)
  toolchain.lock.json          Go, x/mobile, Xcode (local and CI), deployment target, runtime, signing status
  .env.example                 the three public keys every client uses
```

## Building

```sh
python3 scripts/build-native-ios.py --target xcframework   # gomobile bind -> apps/ios/Frameworks
python3 scripts/build-native-ios.py --target app           # + Generated.xcconfig + unsigned simulator build
python3 scripts/test-native-ios.py --suite foundation      # no simulator needed
python3 scripts/test-native-ios.py --suite unit            # no simulator needed
python3 scripts/test-native-ios.py --suite simulator       # needs the iOS simulator runtime
```

* **gomobile** is built from the version `native/core/go.mod` pins into `.runtime/ios/bin`; the build refuses a tool
  that does not match the lock. `-target=ios,iossimulator -iosversion 17.0`, the same ldflags as Android.
* **The xcframework** has `ios-arm64` (device) and `ios-arm64_x86_64-simulator`; `core-build.json` records the
  Core version, Mihomo revision and patch, and each slice's SHA-256.
* **`.env` → xcconfig.** The file is validated with `scripts/native_env.py` (env-v1). An xcconfig treats `//` as a
  comment, so URLs are written as `https:/$()/host`; the built Info.plists contain the full `https://` value.
  Without `apps/ios/.env` the build uses `.env.example` and says so.
* **One Go runtime per process.** The test bundle reaches the Core only through the host app (`BUNDLE_LOADER`);
  linking the Go archive into the bundle as well would load a second Go runtime. The simulator suite checks that
  the bundle has no Go symbols.
* **Swift 6 language mode** stays on. The extension uses the completion-handler `startTunnel`, because the async
  override would send the non-Sendable options dictionary across isolation domains.

## Platform split

`GOOS=ios` also satisfies the `darwin` build tag. Without `!ios`, an iOS build would link macOS code that runs
`/sbin/route` and `networksetup`. Round 1 adds `!ios` to `engine/{platform_unix,sysdns,ipv6_route}.go` (and their
tests) and `network/network_darwin.go`, and adds `engine/{platform,sysdns,ipv6}_ios.go` and `network/network_ios.go`.
`managed.ClientPlatform()` reports `ios`; the backend accepts `platform=ios` (`backend/internal/api/native.go`).

Evidence: `go list` for `GOOS=ios` selects exactly the iOS files; the device slice contains the Mihomo revision but
no `/sbin/route` or `networksetup` string, while the same scan finds both in a darwin `asterlink-core`.

## Simulator limits

The iOS Simulator does not run Packet Tunnel Providers. Experiment E-SIM-NE records what `saveToPreferences` does
on this Xcode and runtime (see the round-1 record in `handoff_app_ios.md`). The tunnel data path, DNS, IPv6, memory
and lifecycle can only be accepted on a device.

## CI

`.github/workflows/ios.yml` runs on `macos-15` with Xcode 26.3, the CI baseline in the lock (`xcode.ci`). Its build
number is recorded from the first run rather than guessed. The simulator suite there uses the newest iOS runtime the
image ships (`--runtime latest`); exit 3 (BLOCKED, no runtime) is tolerated, a failure is not.
