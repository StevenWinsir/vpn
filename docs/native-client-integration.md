# 原生客户端接入：后端实现与当前未完成项

## 交付状态

**P0–P4 的相应阶段验收已通过，下一步 P5，不可发布。** 第七轮真实 macOS App 补齐服务端配置的 Mihomo 语义验证、归属、私有原子保存和内核应用、受限节点选择及失败/换账户清理，原有登录、Core 重启、App 重开与正常退出也通过。正常状态为 `configuration_applied`，不再只是 Go 内存暂存；顶层 `can_connect=false`，唯一 Meter/真实计数和首次授权仍待 P5。最新记录见 `HANDOFF.md` 第 9.11、19 节。

此前未保留登录接线的描述仅属历史。账户、受管配置和节点接线均已保留；生产导航只有账户/服务端节点/语言主题，旧 profile 自动加载、导入/导出/备份恢复、脚本、资源侧载与 external-controller 路径关闭。旧 actions 和 Core RPC 硬拒绝，JNI quickSetup 永久禁止旧配置加载，不能在 P5 授权 listener 后恢复。用户旧文件/数据库行保留但不自动加载。CoreController/DesktopCoreLifecycle/ServiceState 生命周期所有权不变；流量报告、尾账及运行态计量断连仍待 P5。

Flutter/Dart/Rust/JDK/NDK 与锁定依赖版本保持，第六轮 integration_test 依赖继续保留。第七轮 core/go.mod 最低语言版本由 1.21 改为 Go 1.25，以使用 os.Root 的安全文件和原子替换 API，实际构建仍使用已安装 Go 1.26.6。Xcode 已正常初始化，完整 macOS App 构建/运行通过。Android 本轮重新执行 arm64 Core vet/c-shared 构建；120 项 JVM 为第六轮历史结果，没有冒充本轮重跑。系统代理命令隔离，APK/JNI 设备、真实节点/TUN/后台/其他平台及线上服务仍未验收。

前端域名：`https://test.hyshentou.cn`。后端域名：`https://demo.hyshentou.cn`。网站现有 Next.js 同源代理不变；原生 Core 固定通过 HTTPS 访问后端原生前缀，不允许 UI 更改主机。此次没有访问线上接口，没有改动真实 `.env`、线上服务或真实数据库。

## 已实现的后端协议

统一前缀 `/api/v1/client`。原生接口使用独立的随机 Bearer token，不复用网页 Cookie/JWT，不伪造浏览器 Origin。JSON 错误结构与现有 API 相同，响应均为 `Cache-Control: no-store`。

| 方法 | 路径 | 行为 |
| --- | --- | --- |
| POST | `/login` | 验证账户，记录安装实例 UUID、平台、版本，创建独立原生会话；同账户、同安装实例的旧会话被撤销 |
| GET | `/session` | 返回会话期限、套餐状态、剩余流量、最近已确认的上报序号/累计计数 |
| GET | `/config` | 验证真实 VIP 权益、套餐周期和同时在线设备上限，读取套餐 YAML，并将会话绑定到该订阅周期 |
| POST | `/traffic` | 原子写入去重账本并更新订阅计数，返回最新余额和 `can_connect` |
| POST | `/logout` | 撤销当前原生会话 |

登录成功返回 token、公开用户信息和 session。token 为随机 32 字节的 URL-safe 编码，数据库只保存 SHA-256 哈希。绝对有效期使用已有 `SESSION_HOURS`，默认 12 小时；超过 3 分钟没有成功访问的会话失效。**客户端已实现 token 仅在 Go 内存、密码提交后清空输入框、不持久化自动登录凭据；仅安装实例 UUID 可持久化。** Core 失联或重启需要重新登录。

设备上限在配置下载时按当前订阅周期、当前权益指纹的活跃绑定会话检查，事务先锁用户行，避免多设备并发绕过上限。旧周期或已失效指纹不占当前名额。指纹覆盖套餐、测试权益来源、专线权限和设备上限；同周期权限变化需要重新登录/绑定。同套餐同周期增加有效期或额度不会重置累计计数。安装 UUID 仍不是硬件证明。

状态新增 `server_time`、`subscription_expires_at`、`authorization_expires_at`、`session_idle_timeout_seconds`、`profile_version`。有效授权不晚于服务端时间 +90 秒、会话到期和套餐到期，任何拒绝状态均无授权截止值。P2–P4 在真实 Meter 尚未接通时由 Go 协调器每 60 秒 GET `/session` 保活；已登录 UI 每 2 秒只查询 Core 缓存，不续期受管配置。P5 启动 Meter 时必须交接给唯一计量写入者，不能并行维护两套上报序列。

免费用户返回 `upgrade_required`；没有正式付费权益的测试订阅返回 `paid_vip_required`；到期返回 `subscription_expired`；无余额返回 `quota_exhausted`。未绑定配置的会话不能上报至其他用户或其他订阅。

## YAML 的配置与安全边界

新增后端环境变量：

```dotenv
CLIENT_PROFILE_DIR=/etc/asterlink/client-profiles
CLIENT_ALLOW_TEST_ENTITLEMENTS=false
```

在私有目录中由管理员提供与套餐 ID 对应的文件，例如 `starter.yaml`、`pro.yaml`、`max.yaml`。这些文件必须包含实际可用、权限匹配的代理节点；本轮没有提供或部署真实节点。目录不可放在前端 `public` 或 Nginx 静态目录中，服务账号只需读取权限。

后端只接受安全格式的套餐 ID，使用受目录约束的 `os.OpenRoot` 打开文件，拒绝路径/符号链接逃逸、非普通文件、空文件、无效 UTF-8、NUL 和超过 1 MiB 的内容。响应包含 YAML、SHA-256 版本和 session。P4 客户端已执行真实 `config.ParseRawConfig` 语义验证后再 `hub.ApplyConfig`；这些检查不保证节点实际可达。

P4 接受内联节点、显式 select 组和受限内联规则/DNS；拒绝远程 providers、脚本、自动测速组、外部 geodata、独立 listeners/tunnels 和未纳入代理类型。服务端端口/LAN/TUN/controller/UI/profile 等运行控制字段由客户端策略移除，不能绕过 P5。具体字段、代理类型及数量限制见 `HANDOFF.md` 第 19.3 节；不保证任意现有 Clash YAML 都兼容，线上模板本轮未核验。

原始 YAML、版本和 `{user_id,session_id,generation}` 归属及随机配置 ID 原子保存在 `<Core home>/asterlink-managed-v1/active.json`，目录 0700、文件 0600，不进入旧 profiles 数据库。只清理准确标记的自有文件，不递归删除旧配置；写盘/应用失败、失效/退出/换账户及冷启动清除受管状态，不恢复旧身份或旧配置。没有任意路径或原始 YAML 导出 RPC，文件权限不是防本机管理员提取的保证。

未设置目录或文件缺失时返回 `client_config_unavailable`，不会返回示例节点、公开订阅或本地缓存作为后备。

已绑定状态和流量报告重新检查文件 SHA-256：变化返回 `profile_changed`，撤回返回 `client_config_unavailable`，禁止旧配置继续获授权。重新 GET 配置可更新绑定版本但不清零计数。账户页已支持检查/重新加载：先清理旧配置、下载、语义验证、原子保存并实际应用，失败不回退。运行后的真实计数、尾账和重新授权仍待 P5；当前始终停止，没有待结算代理流量。检查不是推送，也不能撤销已提取的节点凭据。

**现有网站仅实现模拟购买，`paid_test` 订单不会自动变成真实付费 VIP。** 只有在非生产环境，同时开启 `TEST_PURCHASE_ENABLED` 与 `CLIENT_ALLOW_TEST_ENTITLEMENTS`，测试权益才可用于开发联调。`APP_ENV=production` 明确拒绝 `CLIENT_ALLOW_TEST_ENTITLEMENTS=true`。不要将测试订单改名冒充可信支付；真实权益开通仍需要可信支付回调或经授权的后台流程。

## 流量账本

新增 `native_sessions` 和 `client_traffic_reports` 表。保留现有 `subscriptions.used_units`、`upload_bytes`、`download_bytes`，网页可以继续读取同一份订阅计数。

每个会话上报累计 `upload_bytes`、`download_bytes` 和从 1 开始递增的 `sequence`。重试必须使用相同序号和完全相同的计数，不能在等待确认时改变该批次。服务器按会话累计值计算增量，忽略客户端对用户、套餐、价格、倍率的选择，额外未知字段会被拒绝。

唯一约束为 `(session_id, sequence)`。同批次并发重试只扣一次；同序号不同内容、跳号或计数倒退会失败。事务按用户、会话、订阅的顺序加锁，防止多设备余额覆盖以及和购买/续费事务冲突。会话绑定订阅 ID 和 `starts_at`，避免旧周期的延迟上报扣到新周期。重复批次也返回**当前**余额，而不是缓存第一次响应。

同周期权限或配置变化后仍允许已产生尾账入账，但 HTTP 200 返回 `can_connect=false`；结算成功与继续连接必须分开判断。不同订阅周期仍拒绝向新周期扣费。

当前明确采用上下行合计 **1 倍**，即 `1000 permille`；一个计费单位是 1/1000 字节。没有根据总流量伪造普通/专线的逐节点倍率计费。所有账本记录标记 `source=client_reported`。当增量超过剩余额度时仍记录实际声明的用量，返回余额下限为 0，不伪造丢弃超额部分。

### 不属于可信节点计量

账户持有人可以修改客户端或伪造累计计数；客户端取得的 YAML/代理凭据也不能保证不可提取。仅靠本设计不能防止恶意漏报、离线使用缓存凭据或修改版客户端忽略断连信号。强制终止进程也可能丢失最后未上报的一段用量。

正式收费运营需要节点侧可信计量、每用户/设备可撤销的代理凭据、节点侧到期/额度执行，以及经验证的计费倍率。本轮没有实现这些能力，不能把客户端上报包装成防篡改计费。

## 已接线的账户协调器与待接线 Meter

`FlClash/core/managed/` 的通信/账户/安全文件生产代码使用 Go 标准库；真实 Mihomo 配置引擎在主包 `core/managed_configuration.go`。协调器和七个 RPC 已调用登录、配置、状态、受限节点选择及退出；既有 Meter 仍独立待接。`managedSelectProxy` 校验 generation、configuration_id、owner 和服务器声明的组成员，元数据不包含原始 YAML/凭据。专属 transport 禁止环境代理/跳转/不可信证书，Android 非 VPN DNS 观察在登录前启动，DNS 和 TCP 共用 protect 适配；通用日志不输出请求参数。

库实现首次确认后授权、每分钟上报、一秒采样、最多 90 秒且受套餐/会话到期约束的授权、失败/额度不足停止回调、计数回退拒绝、串行上报和原批重试。本轮以请求开始时间保守换算截止，覆盖响应延迟/时钟偏差，不在响应到达时重置 90 秒。关闭包含受 context 约束的 HTTP 最终上报和可选退出，但等待锁/采样回调的整个 Close 尚无可证明硬时限。关闭后再次 Flush 不触发报告/停止回调；调用方仍须等待旧会话关闭再安装新会话。

P2–P4 已完成账户/受管配置清理、冷启动/重启门禁及拒绝启动；**P5 仍需将真实计数器接到代理流量和唯一 Meter，处理运行中关闭全部连接、Android 服务与桌面代理设置，以及最后尾账**。当前 `managedFlush` 明确返回 `meter_not_ready`，不发送伪造报告。Go 计时器不能替代真实 Android 后台、休眠恢复或 Windows/macOS/Linux TUN 验证。

## 已运行的验证

后端测试使用独立临时 PostgreSQL，只监听私有 Unix socket，结束后关闭/删除。脚本设置 `RUN_DB_TESTS=0`；原 `TestPostgresIntegration` 现在优先使用临时 DSN，因此网页注册/登录/购买/刷新/退出/并发回归实际执行，不再因禁止真实库而跳过，也不加载真实 `.env`。

```bash
# 需要本机已安装 PostgreSQL 的 initdb/pg_ctl 和 Go。
python3 scripts/test-native.py

cd backend
GOPROXY=off GOTOOLCHAIN=local go vet ./...

cd ../FlClash/core
GOPROXY=off GOTOOLCHAIN=local go test -race -count=3 -v ./managed
GOPROXY=off GOTOOLCHAIN=local go vet ./managed
```

覆盖：原生认证、免费/测试/正式订阅区别、私有文件读取与逃逸防护、设备上限、同设备旧会话撤销、8 并发相同报告去重、跨设备最新余额、错误序号/计数、退出/过期/停用、订阅换周期隔离、生产配置限制，以及独立计量库的本地额度停止、断网后原样重试、并发/关闭竞态、重定向与不可信证书拒绝。

第七轮最新结果：Flutter 1,927 项零失败/跳过，Core 133 / managed 38 项及三轮竞态、vet，后端 13 项顶层隔离回归和 Android Core vet/c-shared 均通过。真实 macOS App 三阶段验证配置应用、节点选择、语义错误/缺失/替换/换账户清理、Core 重启、App 重开及正常退出；最终重建正常入口 App，通过测试标签隔离检查。证据 `artifacts/p4-20260930-r7/`，详见 `HANDOFF.md` 第 9.11、19 节。尚缺后台上报、真实节点、系统代理/TUN/平台断连及线上域名/TLS/Nginx 联调。

历史第四轮结果：Core 128 项、managed 26 项及竞态、Flutter 1,893 项、独立 Kotlin 服务门禁 14 项、后端 13 项、网站 typecheck/lint 和 Chrome 代理 11 项通过，见 `artifacts/p2-p3-20260930/`。当时 Android Gradle/Xcode 阻塞已在第五/六轮解决。第三轮 Rust 和第六轮 120 项 JVM 保留历史证据，不计为第七轮重跑。

## 后续部署前的必要条件

当前不要作为完成原生客户端接入的版本发布。后端单独部署前也需要先备份并在测试环境执行显式迁移，再配置私有 YAML 和正式权益：

```bash
cd backend
# 必须先确认工作目录、数据库目标和备份；本轮没有执行此命令连接真实数据库。
go run ./cmd/api -migrate
```

迁移为新增表/约束及 `native_sessions` 两个追加列，不删除原表；本轮仅在新建临时 schema 验证重复迁移，不代表旧生产库升级已验收。旧绑定记录默认空指纹，需重新登录；新客户端拒绝缺少新授权字段的旧响应，发布需先后端后客户端灰度。保留生产 HTTPS、Cookie、Origin、数据库 TLS，不为测试绕过限制。
