# Administrator node catalog and metering boundaries

## Development acceptance path

1. Use the website administrator account, not a customer role. Open `/admin/nodes` after `/login`.
2. Import a `proxies:` list. Set direct/dedicated, region, enabled state, plan allowlist and rate. Updates require the version displayed by the editor.
3. Sign in on the normal macOS managed app using a current eligible customer account. Test orders only qualify with both development test flags enabled. Starter does not allow dedicated nodes.
4. Refresh server configuration, select an allowed node and explicitly connect. The UI never needs an editable YAML or subscription export URL.
5. Compare Core cumulative upload/download, server `client_traffic_reports` deltas, and subscription `used_units`. Do not treat UI display-reset counters as billing counters.
6. Change rate/configuration, exhaust quota or expire the subscription. The official client stops, settles the previous segment and requires renewed confirmed authorization before connecting again.

`go run ./cmd/doctor -email customer@example.invalid` inspects missing migrations, active administrators, the catalog flag, valid encryption key, test eligibility, allowed nodes and decryption errors. It runs inside a read-only PostgreSQL repeatable-read transaction. `managed_development_ready` means these configuration prerequisites pass, not that a real remote node or macOS TUN has been tested. `production_ready` and `node_authoritative` remain false.

## Data ownership and confidentiality

Browser auth uses HttpOnly cookies and server-side authorization. Native auth uses a separate opaque session token stored as a hash by the API. Administrators can read a selected node's YAML; metadata lists and audit records do not contain proxy secrets. Node YAML is encrypted at rest using AES-GCM and a separately backed-up key, with node ID as authenticated associated data.

Only the selected node credential is sent to Core; the other eligible entries are metadata until selected. Core-owned configuration remains private to the app and ordinary UI export/import paths are closed. This is UI/data-minimization, **not DRM**. A user with control of their device can inspect memory, files or a modified client and obtain credentials. Server-side TLS or encrypted YAML does not solve that.

The macOS remembered password is an explicit option, saved in a non-synchronizing Keychain generic-password item scoped to the exact API base. No plaintext SharedPreferences fallback exists. A new process may prefill it but must still complete online authentication. Normal app exit revokes the active session while retaining the chosen remembered password; explicit logout removes the Keychain item. A failed Keychain operation is surfaced without raw native error or credential details. Other platforms do not claim this feature.

## Metering rules

`charged_units = (upload_delta + download_delta) * rate_permille`. Integer units are one thousandth of a byte. Rate 500 means 0.5x, rate 1000 means 1x. Both directions count. Sequence numbers and cumulative baselines reject resets, incompatible replays and skipped sequences. An acknowledged replay never charges twice. Each segment retains its bound rate/version even when an administrator edits the node; final settlement precedes a new binding.

The local catalog acceptance fixture transferred 3,540 bytes at 500 permille (1,770,000 units), then another 3,540 bytes at 1000 permille (5,310,000 cumulative units). Its evidence explicitly labels source `client_reported`, `node_authoritative: false`. Reproduce it with `scripts/test-node-catalog.py`; it uses real Mihomo TCP forwarding and PostgreSQL, but local controlled proxy endpoints rather than commercial Internet nodes.

Admin changes are committed immediately. Synchronization/version checks are bounded by client polling/traffic reporting, not a push-based instant revocation guarantee. The configured report interval is 60 seconds and the lease is 90 seconds. Changing a profile stops the managed connection and requires configuration refresh/reconnection. There is no seamless live circuit replacement claim.

## Why production proxy delivery is blocked

Current shared SS/HTTP/SOCKS5 credentials do not identify each customer at the node. Native login, hidden YAML, JWTs and client heartbeats cannot revoke a credential already extracted and used by a different program. Forged or suppressed client traffic reports cannot be trusted as the sole commercial ledger. Therefore production config rejects both node-catalog and private-file proxy delivery until a deployed node-side authority exists.

A production access-control replacement needs customer-bound credentials or short-lived authorizations understood by the actual node, bounded stale authorization and outage rules, and expiry/revocation enforcement at ingress. These controls are separate from the chosen client-only billing source: this project continues to charge only client reports. Preventing dishonest under-reporting would additionally require independent trustworthy usage evidence; request signatures alone cannot provide it. Keeping client-only billing therefore retains that explicit fraud risk. Merely adding an agent API without deploying access enforcement on the real proxy nodes does not revoke extracted shared passwords. The current production guard remains in place.

Node details use a read-only repeatable-read transaction so that metadata, optimistic-lock version, plan permissions and decrypted YAML come from one snapshot even when another administrator saves concurrently. The browser cancels superseded node reads before opening a new import draft and prevents duplicate in-flight submissions. Traffic acknowledgements must preserve the already-confirmed profile/rate binding; only the settled configuration-selection path can change it. Rejected acknowledgements do not discard pending counters or renew the authorization lease.

## macOS boundary

Managed Core presently owns a loopback mixed listener and the client can manage system proxy settings using ownership/journal guards. That does not cover applications bypassing the OS proxy and is not all-device Clash TUN. Native acceptance isolates actual system proxy changes to avoid disrupting the developer's machine. Production requires separately verified TUN privilege/route setup, IPv6/UDP/DNS handling, sleep/wake, network changes, crash cleanup, competing VPNs, and signed/notarized distribution.

Do not publish a development debug bundle, simulate successful payment in production, disable TLS verification, or advertise authoritative quotas based on the current client-reported ledger.
