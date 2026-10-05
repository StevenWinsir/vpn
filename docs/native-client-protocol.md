# 原生客户端协议与 P1 测试契约

本文件描述固定后端协议及已接通的 P2–P6 代码。第八轮接入真实累计计数、分钟上报、尾账、显式连接和平台停止，没有更改后端 HTTP 协议；实际 macOS App/Gin/PostgreSQL 结果见 HANDOFF.md 第 9.12、20 节。原生接口仍为 `https://demo.hyshentou.cn/api/v1/client`，网站仍经 `https://test.hyshentou.cn/api/v1` 同源代理。P6 设备/实际 OS 矩阵与线上接口尚未验收。

## 共享夹具与包裹层

唯一公共 JSON 夹具为 `FlClash/test/fixtures/native_client_contract.json`，`contract_version=1` 只表示测试语料版本，不是已经实现的 HTTP 协商机制。其八个案例覆盖登录、免费状态、VIP 未绑定、配置就绪、正常结算、额度耗尽、会话过期及退出。固定日期、示例 UUID 和令牌仅用于测试，不是可登录凭据。

| 方法 / 路径 | 成功响应 |
| --- | --- |
| POST `/login` | `{token,user,session}` |
| GET `/session` | 直接状态对象，无 `session` 外层 |
| GET `/config` | `{yaml,version,session}` |
| POST `/traffic` | `{session,replayed}` |
| POST `/logout` | `{logged_out:true}` |

错误统一为 `{error:{code,message}}`。原生响应 `Cache-Control: no-store`，原生登录不设置网页 Cookie。令牌只允许保存在客户端内存中；持久化设备 UUID 不构成硬件证明。

后端 `native_contract_test.go` 对真实快照输出及全部字段作精确比较；Go `contract_test.go` 检查相同字段、HTTP 状态/包裹层、拒绝状态、SHA-256 和授权。Dart 使用同一夹具检查协议，另有运行快照测试。第八轮 Core 140 / managed 40 项顶层测试（包含实际 60 秒报告）、managed 三轮竞态 120 次执行和 Android 126 项 JVM 通过；完整 Flutter、App 及构建计数以 HANDOFF 第 9.12 节和 artifacts 中 verified-counts.json 为准。设备/真实 OS 设置与线上测试不能从单元测试推定。

## 已接通的账户、配置与运行 RPC（P2–P6）

`managedLogin`、`managedLoadConfig`、`managedStatus`、`managedFlush`、`managedLogout`、`managedReset`、`managedSelectProxy`、`managedConnect`、`managedDisconnect` 在 Go/Dart 同步注册。公开结果是结构化账户快照，不是 JSON 字符串，也不含 token/YAML/password；generation 防止迟到请求改变新账户。生产 API 地址由 Core 固定，登录参数不能覆盖 host。

快照顶层 `can_connect` 表示本机当前实例确已取得运行授权，不是直接转发 `session.can_connect`。`configuration_applied` 只表示配置准备完成；显式 Connect 确认首报后才开放回环 mixed listener。`configuration_staged` 仅保留内部无引擎测试接缝。`managedFlush` 在 Meter 存在时结算真实累计，不存在时返回 `meter_not_ready`，不构造占位报告。密码只作为一次性参数，通用日志不输出参数。

Connect 参数是 `{generation,runtime_revision,mixed_port}`，端口限制为 1024–65535；先读取 Core 当前快照，再发送明确连接意图。Stop/Disconnect/生命周期变化会推进运行代次并取消待执行确认；迟到的旧 Connect 不能覆盖更新的 Stop。`startListener` 只验证当前已授权实例，不能自行重新开放。Disconnect 参数 `{generation}`，先停实际数据通道，再在 2 秒预算内结算；重连必须显式再次确认。

可选 `metering` 快照包含 `sequence`、`acknowledged_upload_bytes`、`acknowledged_download_bytes`、`upload_bytes`、`download_bytes`、`confirmed_remaining_bytes`、`estimated_remaining_bytes`、`pending`。累计单位均为字节；confirmed 是后端确认值，estimated 扣除了尚未确认的本地累计增量，不能冒充最终服务端余额。Dart 校验非负、溢出、确认基线不大于当前累计及运行权限结构。

应用成功的可选 `configuration` 为 `{id,version,owner:{generation,user_id,session_id},groups:[{name,type,selected,proxies}]}`；id 是每次保存生成的 32 位十六进制随机实例标识，version 是 64 位 SHA-256，groups 仅含显式 select 组及服务端声明的成员名。Dart 验证完整归属及严格字段集合，拒绝额外 YAML/密码字段。节点选择参数为 `{generation,configuration_id,group,proxy}`；Core 校验当前账户、配置实例及组成员，不允许旧 ID、任意 GLOBAL/DIRECT 或导入节点。选择不启动监听。

配置下载后执行有界 YAML 策略和 `config.ParseRawConfig`，私有原子保存后 `hub.ApplyConfig`，始终关闭端口/TUN/controller 并 suspend。支持内联节点/select/受限规则与 DNS；providers、自动测速、脚本及外部 geodata 等拒绝，详细兼容范围见 HANDOFF.md 第 19.3 节。旧通用 setup/导入/导出/外部 provider RPC 和 JNI quickSetup 永久拒绝，不是仅依靠顶层 can_connect。

## 状态与授权期限

原有状态字段保持，新增加：

| 字段 | 语义 |
| --- | --- |
| `server_time` | 本次快照服务端 UTC 时间 |
| `subscription_expires_at` | 当前订阅绝对到期时间；无订阅为 `null` |
| `authorization_expires_at` | 有效连接授权截止；任何拒绝状态为 `null` |
| `session_idle_timeout_seconds` | 固定 180，成功认证请求允许更新服务端 last-seen |
| `profile_version` | 本会话最近一次绑定配置的 SHA-256；未绑定为空；不是最新版提示 |

`expires_at` 始终是原生会话到期时间，不改名也不偷换为套餐期限。`report_interval_seconds=60`、`lease_seconds=90`、`rate_permille=1000`、`metering_source=client_reported` 保持不变。

允许连接时：

```text
authorization_expires_at = min(server_time + 90 秒,
                               expires_at,
                               subscription_expires_at)
```

独立计量库要求新的时间字段齐全，截止时间严格晚于服务端时间，且不超过三个上限。客户端以**请求开始时间**加服务端授权剩余时长转换成本地期限，并继续保守限制于会话/订阅绝对期限。响应耗时会消耗授权，不会在响应到达时重新得到 90 秒；慢本地时钟不能扩大相对窗口，快时钟可能提前拒绝。该算法不能替代 Android 挂起/桌面睡眠恢复门禁或节点侧执行。

任一 HTTP/网络确认失败仍立即调用停止回调，不提供离线宽限。额度不足一整字节时返回 `quota_exhausted`，避免出现 `can_connect=false` 却没有原因的状态。HTTP 200 的尾账结算也可返回拒绝；库保留 `profile_changed` 等业务原因，不覆盖为无关的 `lease_expired`。

## 保活、真实计量与尾账所有权

登录成功后，在实际 Meter 尚未启动期间，Go 账户协调器每 60 秒读取 `/session`，覆盖免费升级页、配置等待/失败和 `configuration_applied`。不得构造占位 `/traffic`。请求失败立即清理受管配置并保持门禁关闭；受管配置确认最多 90 秒，不由缓存读取续期。按请求起点保守计算的 180 秒空闲或绝对期限到达后必须重新登录。

第一次显式 Connect 将责任转交唯一 Meter，协调器不再同时 GET 保活；Meter 每秒采样、每 60 秒报告。UI 每 2 秒读 Core 缓存，不触发 HTTP、不推进授权，不能成为第二个序列写入者。显式刷新也通过同一个 Meter 报告锁。退出/重置/代次变化取消旧任务；旧 runtimeLease 退休后，其停止回调不能关闭新账户连接。

生产采样使用同进程 `statistic.DefaultManager.TotalTraffic(true)`，固定 Mihomo 提交按最终代理链排除 DIRECT；账户 API 使用独立直连 transport。显示图表只维护自己的基线，清零图表、断连重连、重载都不重置计费累计。采样失败/回退/溢出停止并拒绝旧会话继续；首报 ACK 丢失也保留非空 Meter 与完全相同的 pending 批次，不增加序号伪造新批。

停止先封闭当前实例的新 TCP/UDP、关闭存量连接，再等待处理中的数据通道与累计稳定。尾账原批重试，确认后才能重载配置或切换账户。普通断连失败返回 `traffic_unconfirmed`，当前内存会话保留以便重试；Logout/Reset 的最终释放有截止时间，已关闭会话但尾账不确定时返回 `final_traffic_unconfirmed`，尾账已确认而撤销请求失败则为 `logout_unconfirmed`。不能在会话已销毁后提示用户原会话还可重试。硬杀、系统终止或离线超时不保证尾账完成；未持久化 token/pending，不承诺重开后自动追缴。

后端测试已证明免费/VIP 未绑定状态可用 GET 保活、不写流量序列、180 秒边界拒绝；本轮协调器测试另验证 GET 保活所有权、缓存读取不续期、退出取消任务及迟到响应不能恢复身份。Android 后台与睡眠后的真实调度仍待平台验收。

## 配置与同周期权益变更

`native_sessions` 增加 `entitlement_fingerprint` 和 `profile_version` 两个非空字符串列，旧记录默认空。指纹由当前订阅 `plan_id`、`is_test`、`allow_dedicated`、`max_devices` 计算，不对外暴露。

配置授权同时要求订阅 ID、`starts_at`、权益指纹一致。同周期换套餐、专线权限降低、设备上限变化将使原指纹不匹配；旧客户端下次确认不能继续连接，需要重新登录取得当前权限。MaxDevices 增加也采取相同保守策略。设备名额只统计当前周期、当前指纹、未撤销且未过期/闲置的绑定会话；用户行锁保证新设备并发绑定不能突破降低后的限制。

同套餐同周期续费只增加到期时间或配额，不改变上述指纹，不重置会话累计计数。修改正式/测试权益来源仍受生产配置门禁限制，不会将 `paid_test` 改名为真实权益。指纹描述当前权限，不是记录所有历史修改的单调版本；未经请求观察、又被管理员恢复的权限变化不提供不可逆撤销保证。

计量绑定与连接授权故意分开：同一订阅周期中，旧配置/权限失效后的未确认尾部流量仍可按原累计序列结算；响应为 HTTP 200、`can_connect=false`。更换订阅 ID 或 `starts_at` 则拒绝向新周期入账。不能通过拒绝所有失效状态的上报丢弃已产生的用量。

每次已绑定且权益有效的 `/session`、`/traffic` 都重新检查私有 YAML 文件。SHA-256 改变返回 `profile_changed`；文件撤回/不可读取返回 `client_config_unavailable`。正常确认节奏约 60 秒，网络失败立即停止回调，授权最长 90 秒；这不是推送，也不保证恶意客户端或已提取凭据立即失效。文件读取在事务内，最多 1 MiB；高并发规模下需要另行评估其锁持有时间，不能在未测试时添加可能过期的授权缓存。

### 手动“同步线路”的安全顺序

当前运行后的重载已实现以下顺序：阻止新连接并关闭存量连接 → 在唯一报告锁内采样并确认旧尾账（不确定时原批重试）→ GET `/config` → 验证 SHA-256 和 Mihomo 语义 → 原子替换并实际应用 → 保持同会话累计/序列、仍然停止 → 用户再次明确连接并重新取得计量确认后开放。报告和 GET 配置不能交错覆盖待确认基线。

配置重新绑定会更新版本，但不会清零 `last_sequence`、上传或下载累计。后端返回 `can_connect=true` 仅代表服务器授权，不能跳过本机验证。下载/校验/应用失败不得退回旧配置继续连接；已有等待报告不能在同步过程中被新内容覆盖。不同权限指纹或新周期需重新登录，不走同会话热切换。

## 隔离数据库与可控网络夹具

`python3 scripts/test-native.py` 建立仅监听私有 Unix socket 的临时 PostgreSQL，强制 `RUN_DB_TESTS=0`，提供临时 `NATIVE_TEST_DSN`。原网页 `TestPostgresIntegration` 现在优先使用该 DSN，与原生测试共享建库辅助函数但使用不同随机 schema；该分支不加载真实 `.env`。没有 DSN 时，真实数据库分支仍必须显式 `RUN_DB_TESTS=1` 授权。

`native_fixture_test.go` 提供随机回环端口的 SOCKS5 CONNECT 测试服务及独立 HTTP 目标；代理仅允许访问自己创建的那个回环目标，其他地址会被拒绝。测试实际上传已知正文并读取对应响应；P1 API 测试私有 YAML 使用该临时端口，测试结束关闭服务。共享 JSON 中的 18080 仅是固定语料值，不是生产节点或常驻服务。

上述 P1 夹具本身只验证基础设施。第八轮新增 `core/managed_runtime_test.go` 的真实 Mihomo 代理传输测试；`scripts/test-macos-managed.py` 与 test-only `desktop_acceptance_test.go` 另验证真实 App/Rust IPC/Core/Mihomo → 受控 HTTP CONNECT 节点 → 本机目标 → Gin/私有 PostgreSQL。两次传输最终账本为上传 49,394、下载 65,812 字节，固定倍率下 charged_units=115,206,000；包含代理协议处理中的实际统计字节，不等于只算 HTTP 正文。未修改全局系统代理、TUN、个人浏览器资料或真实账户；测试代码不作为生产节点。

## 升级边界与命令

部署新后端前需在授权的测试/生产目标执行显式追加迁移，并先验证备份与旧库升级。**本轮只迁移临时新库，未升级线上或历史生产 schema。** 已绑定的旧会话缺少新指纹，默认拒绝并要求重新登录。新库客户端拒绝缺少新授权字段的旧服务端响应；这不是可随意混布的无版本协商发布，需先服务端后客户端灰度。

```bash
python3 scripts/test-native.py
cd backend
RUN_DB_TESTS=0 GOPROXY=off GOTOOLCHAIN=local go vet ./...
cd ../FlClash/core
GOPROXY=off GOTOOLCHAIN=local go test -race -count=1 -v ./managed
GOPROXY=off GOTOOLCHAIN=local go vet ./managed
# Xcode 已初始化，继续使用命令级环境包装；无需再次执行许可证操作。
cd ..
bash ../scripts/flclash-env.sh flutter test test/core/native_client_contract_test.dart
```

前七轮证据保留原目录；当前以 `artifacts/p5-p6-20260930-r8/`、HANDOFF 第 9.12、20 节为准。P5 真实计量链路已通过，P6 原生生命周期和代理归属代码已有自动化证据，但 Android 无设备/模拟器、Windows/Linux 无实际 OS 环境，真实 TUN/Doze/睡眠/系统设置恢复仍待验收。测试后重建正常 lib/main.dart App，生产 Core 仅 with_gvisor，不含 managed_acceptance 或测试主机环境入口。线上 TLS/YAML、历史库升级、签名安装与发布未执行；正式收费仍须节点侧可信计量、独立可撤销凭据和额度/到期执行。
