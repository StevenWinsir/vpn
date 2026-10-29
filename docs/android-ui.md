# Android client: account, lines and the formal UI (round 3)

Round 3 of `handoff_app_android.md`: the data administrators configure on the website (plans, lines,
rates) reaches the Android app, behind a three-tab interface of commercial quality. Connecting is wired
through the round-2 `AsterVpnService` but its acceptance (metering, rates, notification content) is round 4.

## Architecture

```text
MainActivity (splash, VPN consent, notification permission, Custom Tabs, settings, clipboard)
  └─ AsterRoot (Compose): Login │ Connect · Lines · Me
        renders SessionState only
SessionController (app-scoped, main thread)          ← port of the Windows SessionController (PR #23)
  ├─ CoreClient ──ipc-v1──▶ Go Core (gomobile, in process): login, watcher, gates, metering
  ├─ CredentialStore = KeystoreCredentialStore (AES-256-GCM, Android Keystore, DataStore)
  ├─ LoginPreferences = AppPreferences (remembered email, theme; DataStore)
  ├─ DeviceIdentity  = FileDeviceIdentity (random UUID in files/)
  └─ TunnelControl   = TunnelLauncher ──▶ AsterVpnService ──▶ CoreTunnelSession (connect at the decided generation)
```

* **The Core decides, the UI renders.** `ConnectionView`, `NodeList` and the empty states are pure functions
  of the redacted snapshot (`shared/contracts/snapshot-v1.json`) plus the command in flight. An administrator
  without a plan is blocked because the Core says `upgrade_required`, not because of any role check here.
* **Controller semantics kept from Windows:** an operation version discards poll results that raced a user
  action; a snapshot of an older generation is ignored; `action_error` is a failed action with fresh state;
  `stale_generation` re-reads the state and says "状态已变化"; exactly one silent re-login after a
  `native_session_*` expiry (never after a refused password); a lost Core ends the session (sign in again).
  Polling is 1 s while connected, 2 s otherwise (an in-process read, no network).
* **Android additions:** the controller is application-scoped, so it keeps watching while the VPN runs with no
  screen; one quiet `refresh_catalog` right after sign-in (the watcher's first catalog otherwise takes ~2 s) and
  on returning to the foreground, throttled to 10 s (`ProcessLifecycleOwner`). When the Core ends a tunnel by
  itself (quota, plan, changed line) the controller stops the foreground service too.
* **Connect hand-off:** the connect must run inside the consented foreground `VpnService`. `TunnelLauncher`
  passes the generation the user decided on to the service and waits (120 s bound) for the Core's reply.
  Cancelling a connect stops the whole Core (the only way back to a known state), like Windows.
* **Messages:** a Core refusal shows the Core's own Chinese text; problems found in the app carry a code that
  maps to `values/strings.xml` / `values-en/strings.xml`. Logic branches on codes only.

## Remember password, device id, backup

* Opt-in "记住密码". The password is sealed with AES-256-GCM under `com.asterlink.vpn.credentials.v1`, a key that
  exists only in the Android Keystore. The account (`API base + lower-cased email`) is the GCM associated data,
  so a ciphertext copied to another account does not open; the DataStore entry name is a SHA-256 of the account,
  so the file lists no emails. An entry that no longer opens (key gone, tampered) is deleted and the user types
  the password again. There is no plaintext fallback: a Keystore failure means "not remembered" with a notice.
* `invalid_credentials` deletes the saved password; "退出登录" deletes it and the remembered email; a normal
  app exit keeps it. No token is ever written: a restart signs in online with the saved password.
* `device_id` is a random UUID in the app's files directory (no ANDROID_ID/IMEI/MAC). Backup and device
  transfer are off for every domain, so neither the ciphertext nor the device id moves to another phone.

## Pages and states

| Page | Content |
| --- | --- |
| Login | brand mark, email (autofill hints), password with visibility toggle, remember switch with a one-line explanation, full-width sign-in with progress, register / website links (Custom Tabs), a footer that shows only "版本 x.y.z" (development builds add a neutral "开发版" marker), start-problem banner |
| Connect | brand + plan chip; 168 dp ring button (sweeping while busy, brand green when connected, grey when blocked); status in the fixed words 未连接 / 正在准备 / 正在连接 / 已连接 · hh:mm:ss / 正在断开 / 无法连接 (live region); the Core's reason or failure; buy/renew when the plan is the problem; cancel while connecting; current line card (hidden without a plan) with 专线/普通线路 and 线路倍率 N×; rates, sparkline and "本次已用 X · 按倍率计 Y" only while connected; plan card with expiry, remaining-traffic bar and "正在同步用量" on its own line |
| Lines | search (name, region, line label), 全部/专线/普通 filter, groups by region in server order, line rate, pull-to-refresh; empty states keep "no plan", "syncing", "none configured" and "no match" apart; switching while connected asks first ("将断开当前连接") and stays disconnected |
| Me | avatar/name/email; plan with renew and orders (website); appearance (system/light/dark); background & battery and system VPN settings (always-on is not supported); website dashboard; about (app version only); copy diagnostics (six lines: app version, OS version, state, error code, time); sign-out with confirmation. No open-source entry (D10) |

Design tokens (2026-10, `handoff_app_standardization.md` 4.2): `ui/theme/Theme.kt` holds `LightTokens` and `DarkTokens`, one field per website `--al-*` role,
and `res/values(-night)/colors.xml` the launcher/splash colours; `scripts/test-brand-assets.py` compares every value with
`frontend/src/app/globals.css`. Material You is off; 16 dp cards, 56 dp pill buttons, tabular figures. The brand blue is retired.
Icons: Material Icons vectors (only those used); the adaptive launcher icon (background `#0D7B61`, shield foreground, monochrome layer),
`ic_notification` (notification and quick-settings tile) and the splash are generated from `frontend/brand/mark.svg`
(`docs/brand-and-copy.md`). Strings (Chinese and English) are guarded by `scripts/test-app-copy.py` and, per screenshot, by `ui/CopyGuard.kt`.

**Not built (the website has no such pages):** privacy policy and terms links. "忘记密码" opens the website's `/forgot-password`
(the `WebPage` enum is the only place to touch).

## Tests and evidence

| Command | What it proves |
| --- | --- |
| `python3 scripts/test-native-android.py --suite unit` | 78 app JVM tests: controller (27, against a scripted Core: A01-A03 logic), contracts/presentation (9: snapshot-v1 strict decoding, >2^53, purchase reasons from native-v1/v2, diagnostics redaction), the 30 screenshot tests, earlier protocol/TUN-spec tests |
| `python3 scripts/test-native-android.py --suite ui` | the 30 Roborazzi screenshots (each test also asserts its state) copied to `artifacts/native-android/ui/` |
| `python3 scripts/test-native-android.py --suite emulator` | adds `SecurityStoreTest` on the device: real Keystore round trip, account binding, tamper rejection, nothing in clear, stable device id |
| `python3 scripts/test-native-android.py --suite account` | real app controller + Go Core + Keystore against the real backend (`TestAndroidAccountWorld`, private PostgreSQL, `adb reverse`): see the handoff round-3 record for the 20 cases and timings |

Screenshots render on Robolectric's Android 15 sandbox (`sdk = 35`): its Android 16 sandbox needs Java 21
and the toolchain lock pins JDK 17. Compose rendering does not depend on the sandbox level; the app itself
still targets 36 and is verified on the API 36 emulator.

The account suite needs no credentials and never touches `demo.hyshentou.cn` or the remote test database.
Checking against the demo server with a real account is a manual step (see the handoff record).
