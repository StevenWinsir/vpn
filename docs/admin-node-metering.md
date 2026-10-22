# Administrator node catalog and metering boundaries

## Development acceptance path

1. Use the website administrator account, not a customer role. Open `/admin/nodes` after `/login`.
2. Import a `proxies:` list using the [managed protocol matrix](managed-protocols.md). Set direct/dedicated, region, enabled state, plan allowlist and rate. Updates require the version displayed by the editor.
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

Current shared proxy credentials (including VLESS UUIDs or WireGuard keys) do not identify each customer at the node. Native login, hidden YAML, JWTs and client heartbeats cannot revoke a credential already extracted and used by a different program. Forged or suppressed client traffic reports cannot be trusted as the sole commercial ledger. Therefore production config rejects both node-catalog and private-file proxy delivery until a deployed node-side authority exists.

A production access-control replacement needs customer-bound credentials or short-lived authorizations understood by the actual node, bounded stale authorization and outage rules, and expiry/revocation enforcement at ingress. These controls are separate from the chosen client-only billing source: this project continues to charge only client reports. Preventing dishonest under-reporting would additionally require independent trustworthy usage evidence; request signatures alone cannot provide it. Keeping client-only billing therefore retains that explicit fraud risk. Merely adding an agent API without deploying access enforcement on the real proxy nodes does not revoke extracted shared passwords. The current production guard remains in place.

Node details use a read-only repeatable-read transaction so that metadata, optimistic-lock version, plan permissions and decrypted YAML come from one snapshot even when another administrator saves concurrently. The browser cancels superseded node reads before opening a new import draft and prevents duplicate in-flight submissions. Traffic acknowledgements must preserve the already-confirmed profile/rate binding; only the settled configuration-selection path can change it. Rejected acknowledgements do not discard pending counters or renew the authorization lease.

## macOS boundary

Managed Core presently owns a loopback mixed listener and the client can manage system proxy settings using ownership/journal guards. That does not cover applications bypassing the OS proxy and is not all-device Clash TUN. Native acceptance isolates actual system proxy changes to avoid disrupting the developer's machine. Production requires separately verified TUN privilege/route setup, IPv6/UDP/DNS handling, sleep/wake, network changes, crash cleanup, competing VPNs, and signed/notarized distribution.

Do not publish a development debug bundle, simulate successful payment in production, disable TLS verification, or advertise authoritative quotas based on the current client-reported ledger.

## 阶段 4 更新（节点权威计量）

上文"生产代理交付被阻止"的原因（共享凭据不能按用户撤销、客户端自报不可信）由阶段 4 的节点代理方案解决：已登记并在线的节点由节点代理按用户发放独立凭据、直接读取转发器计数并强制撤销/到期/预算。生产环境仍然拒绝共享凭据与客户端上报，只允许有节点代理的线路。详见 [macos-stage4.md](macos-stage4.md)。`doctor` 现在额外报告 `agents_online`、`authoritative_nodes`、`grants_needing_reconcile`；`node_authoritative` 表示有在线代理，**不等于**该协议的强制控制已在真实节点验收（报告里的 `enforcement_must_be_accepted_per_protocol_on_the_real_node` 警告保留）。

## 节点组运营要点（节点组交接书第 7 轮）

`/admin/nodes` 按“套餐筛选 → 节点组 → 服务器”管理线路。用户只看到节点组；组内的服务器可以同名，每台设置自己的最大在线设备数。

- **容量在设备实际连接时生效**。登录、拉取目录、选择线路都不占名额（旧版 APP 例外：它在选择线路时就绑定配置，因此选择即占位，见 `backend/DEPLOYMENT.md` 第 7 轮）。调低容量不会踢掉在线设备，只拒绝新的接入，后台显示“超出”。
- **分配顺序**：本设备已在线的服务器 → 用户在该组的固定服务器 → 负载率最低的服务器（负载率相同时随机）。全组满载时返回 `group_full`。
- **停用、删除或移组服务器**：其上的设备会改连同组的其他服务器。新版 APP 自动重连一次；旧版 APP 停止，并提示“线路配置已更新，请重新选择线路”。删除确认框会写明受影响的在线设备数。
- **计费**：倍率属于节点组。改连前后各段按各自的组倍率结算，账本序号连续（由 `TestCoreOnlineLeasesEndToEnd` 与 `TestReleasedCoreAgainstNodeGroupsPostgres` 验证）。
- **发布与回滚**：迁移得到的服务器容量为 1000（相当于不限），发布后应立即改为真实值。回滚到节点组之前的版本时，同组的多台服务器会以同名的多条线路出现，倍率和套餐来自双写的旧列；再次升级后组恢复。完整清单与压测数据见 `backend/DEPLOYMENT.md` 第 7 轮。
