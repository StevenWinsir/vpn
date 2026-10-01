# VPN × FlClash 接入交接文档

> 编写日期：2026-09-29；最近更新：2026-09-30。项目根目录：`/Users/stevenlee/Desktop/vpn`。
> 最新执行（2026-09-30，第八轮）：P5 已接通真实 Mihomo 累计计数、首报确认、每分钟报告、失败/耗尽断连与尾账；P6 的桌面恢复、系统代理归属和 Android 原生生命周期代码已接入并通过本机可执行的自动化。真实 macOS App → Core/Mihomo → 隔离 Gin/PostgreSQL 的分钟报告及累计账本验收通过。最新结果及边界见第 9.12、20 节；前七轮保留为历史。
> **当前状态：P0–P5 的相应阶段验收已满足。configuration_applied 仅表示配置就绪；只有显式连接意图、唯一 Meter 确认和当前运行代次同时有效时，本机 can_connect 才为 true。P6 代码与自动化已完成，但 Android 设备、Windows/Linux 系统及真实系统代理/TUN/睡眠矩阵尚未全部验收，因此 P6 总验收仍不勾选；P7/P8 及正式收费发布未完成。**
> 本轮不连接真实数据库或线上服务，不改真实 `.env`，不提交/推送 Git，不更改系统代理/TUN；仅使用私有临时目录、回环 IPC/端口和构建工具的正常依赖下载。

## 0. 接手先看：从哪里继续

用户的目标是：启动 App 先登录；免费用户提示升级，VIP 从后端取得配置；隐藏用户自行管理 YAML 的入口；每分钟上报流量，后端扣减，额度不足时客户端真正停止代理连接。

接手顺序：**P0 恢复构建环境 → P1 固化协议与测试夹具 → P2 接通 Core 会话桥接 → P3 登录与启动门禁 → P4 受管配置 → P5 计量与断连 → P6 平台生命周期 → P7 联调验收 → P8 发布。** P2–P6 是一个完整功能链，全部通过前不得发布。正式收费还需要第 11 节的节点侧控制，不可把客户端上报当成可信计费。

首个可交付里程碑不是“删掉菜单”，而是：**macOS 隔离测试构建中，冷启动不能连接，登录后可以区分免费/VIP，VIP 能取得并验证配置，错误配置不能启用代理。** macOS 是按现有开发机选择的建议首验平台，并非缩减最终平台范围。

接手前阅读：

- [现有接入说明](docs/native-client-integration.md)、[P1 协议契约](docs/native-client-protocol.md)、[项目 README](README.md)、[网站部署说明](docs/deployment.md)。
- [FlClash 入口规则](FlClash/AGENTS.md)，以及 `.agents/project.md`、`.agents/commands.md`、`.agents/rules.md`、`.agents/architecture.md`。
- 对应工作需阅读 `.agents/skills/core-platform/SKILL.md`、`provider-tests/SKILL.md`、`ui-work/SKILL.md`、`localization/SKILL.md`。

**不要执行 `git clean -fd` 或批量重置：目前 `FlClash/core/managed/` 是未跟踪的新代码。根目录本身不是 Git 仓库，FlClash 才是独立 Git 仓库。**

## 1. 目标与范围约定

### 1.1 用户确认的信息

| 项目 | 已知信息 | 证据范围 |
| --- | --- | --- |
| 网站前端 | `test.hyshentou.cn` | 用户反馈已部署并正常运行 |
| 后端公网域名 | `demo.hyshentou.cn` | 用户提供；本轮未访问 |
| 网站调用链 | 浏览器同源 `/api/v1` → Next.js → 同服务器 Go | 现有修复方案；保持不变 |
| 原生客户端目标 | `https://demo.hyshentou.cn/api/v1/client` | Core 协调器固定生产地址，不接受 RPC 任意指定 host；线上 TLS/原生接口仍待核验 |
| 套餐升级页面 | `https://test.hyshentou.cn/plans` | 按现有前端路由设计；部署后验证 |

不要再次把原生客户端指向 `127.0.0.1:8080`。网站里的回环地址是服务器内部通信；App 的回环地址是用户设备。线上网站正常不等于新的 `/client/*` 接口已经部署；上一轮没有上传或重启线上后端。

### 1.2 产品行为与阶段边界（登录、受管配置、真实计量已接通）

- “每次打开”先按**冷启动/新应用会话必须重新登录**实现，不保存密码或自动登录 token。前后台切换不默认要求用户每次输入密码，但恢复时必须先核验授权；若产品要求每次窗口重新显示也登录，再单独调整，并测试不会把后台 VPN 无意切断。
- 安装实例随机 UUID 可以持久化；登录令牌只在内存中。UUID 不是硬件认证或防伪设备标识。
- 免费、过期、耗尽、设备超限必须有不同状态提示；升级打开网站，不把模拟订单伪装成付费 VIP。
- VIP 登录后自动拉取、校验并应用受管配置；用户可以选择服务端声明的节点，再明确点击“连接”。首报及当前代次确认后才开放本机混合代理；自动加载、刷新或选择节点不自动启动系统代理/TUN。桌面当前开放的是回环 mixed listener，不将桌面 TUN 计入已支持能力。
- 正常每 60 秒上报，一秒本地采样；收到拒绝/零余额或确认请求失败即停止代理，不等待下一分钟。恢复必须经过服务端重新确认，不使用无限期离线授权。
- “断连”至少包含停止新的代理接入和关闭已建立的 TCP/UDP 连接。是否进一步阻断设备所有直连流量属于系统级 kill switch，用户尚未要求，不能宣称已经包含。

## 2. 当前实际保留的代码与限制

### 2.1 已有实现

| 部分 | 已有文件 | 已实现内容 |
| --- | --- | --- |
| 原生 API | [native.go](backend/internal/api/native.go)、[server.go](backend/internal/api/server.go) | 登录、会话状态、VIP 配置、流量上报、退出；新增有效授权期限、权益指纹和运行期配置检查 |
| 数据模型 | [native.go](backend/internal/model/native.go) | `NativeSession`、`ClientTrafficReport`；新增 `entitlement_fingerprint`、`profile_version` 两列，旧记录默认空 |
| 计费规则 | [client.go](backend/internal/billing/client.go) | 累计计数校验、连续序号、增量与溢出校验；固定 1 倍 |
| 环境配置 | [config.go](backend/internal/config/config.go)、[.env.example](backend/.env.example) | `CLIENT_PROFILE_DIR`、`CLIENT_ALLOW_TEST_ENTITLEMENTS`；生产禁止测试权益 |
| 迁移 | [store.go](backend/internal/store/store.go) | 新表加入现有显式迁移 |
| 后端测试 | [native_test.go](backend/internal/api/native_test.go)、[client_test.go](backend/internal/billing/client_test.go)、[config_test.go](backend/internal/config/config_test.go) | 原生接口、并发去重、配置文件安全、生产限制 |
| 受控通信库 | [client.go](FlClash/core/managed/client.go)、[transport.go](FlClash/core/managed/transport.go) | Login/Config/Status/Report/Logout；匿名登录、响应大小限制、SHA-256、独立 DNS/TCP/TLS transport 与错误脱敏 |
| Core 账户与 RPC | [coordinator.go](FlClash/core/managed/coordinator.go)、`core/managed/runtime.go`、[managed.go](FlClash/core/managed.go)、`managed_android.go` / `managed_desktop.go` | 九个结构化 RPC，新增 managedConnect / managedDisconnect；generation + runtime_revision、唯一 Meter、旧实例退休屏障、带截止时间的尾账/退出；未启动 Meter 的 Flush 仍拒绝 |
| P4 受管配置 | `FlClash/core/managed_configuration.go`、`core/managed/configuration.go`、`profile_store.go` | SHA-256、受限配置策略与真实 Mihomo 语义验证；用户/会话/代次归属、私有原子保存、内核应用和失效清理；无旧配置回退 |
| P4 节点与入口收口 | `lib/models/managed_configuration.dart`、`views/managed_proxies.dart`、`views/managed_preferences.dart`、`common/managed_policy.dart` 及导航/旧 actions | 仅账户、服务端节点、语言/主题；绑定配置 ID 的节点选择；旧配置导入、导出、恢复、脚本、资源侧载和外控路径关闭 |
| 原生登录与门禁 | `FlClash/lib/pages/managed_account.dart`、`lib/providers/managed_account.dart`、`lib/models/managed_account.dart`、启动与平台入口 | 四语言登录/账户页、仅 UUID 持久化、Core 失联重置；旧 profile、autoRun、深链接、监听与原生服务启动不能绕过门禁 |
| 实际计量与数据通道 | [meter.go](FlClash/core/managed/meter.go)、`core/managed_runtime.go`、`core/managed_runtime_test.go` | TotalTraffic(true) 真实累计代理字节；图表独立清零；首报/60 秒报告、原批重试、稳定尾账；实例级 TCP/UDP 门禁及实际连接关闭 |
| P6 平台接线 | `lib/providers/actions/setup.dart`、`manager/app_manager.dart`、`application.dart`、`plugins/proxy/*`、Android `ManagedServiceGate` / `ServiceStateMachine` / `ServiceController` | 桌面网络/休眠恢复不自动开放、只恢复仍归本应用所有的系统代理字段；Android 无 Flutter UI 的授权看护、原生启停/通知/权限拒绝、TUN FD 清理；系统实测边界见第 20 节 |
| 隔离测试入口 | [test-native.py](scripts/test-native.py) | 临时私有 Unix socket PostgreSQL 集群；测试后停止与清理 |
| P1 新增契约与测试 | `FlClash/test/fixtures/native_client_contract.json`、后端 `native_contract_test.go`/`native_p1_test.go`/`native_fixture_test.go`、独立库 `contract_test.go` | 8 个公共案例；Go/Dart 契约均已执行通过；原生契约与已有 RPC 专项合计 16 项，并纳入 Flutter 全量 1,860 项通过记录 |
| 说明 | [native-client-integration.md](docs/native-client-integration.md)、[native-client-protocol.md](docs/native-client-protocol.md)、[README.md](README.md) | 本轮状态、协议和边界 |

原 16 个业务变更文件保存在首轮备份；第二轮备份 38 个文件。第三轮开始前备份 716 个源码/文档文件至 `artifacts/p0-p1-20260930-r3/baseline/`，`sha256.json` 列出准确范围，不含真实 `.env`。三轮改动分别见第 13–15 节。根目录没有 Git，不能只打包 `git -C FlClash diff` 就认为已保存后端改动。

### 2.2 明确未完成

FlClash 的 P2–P5 已接通，P6 代码及本机自动化已完成：VIP 配置经过真实 Mihomo 语义验证后应用，连接由唯一 Meter 和显式意图控制；真实累计、报告、尾账与断连已有 macOS App/内核证据。未完成的是 P6 的 Android 设备、Windows/Linux 实际系统和真实 OS 设置/睡眠矩阵，以及完整 P7/P8 联调发布。旧 Profiles 等源码及用户自建数据没有批量删除，但生产导航不可达，相关 actions 和 Core RPC 拒绝，受管配置从不写入旧 Drift 配置集合。配置已应用不等于能连接。

P4 当前支持有内联节点、显式 select 组和内联规则的受限服务端配置；远程 providers、自动测速组、脚本、外部 geodata 等未纳入兼容范围，见第 19.3 节。没有读取或验证线上实际 YAML，不能声称所有 Clash 订阅格式均可用。

上一轮记录称客户端登录文件写入遇到工具安全检查，随后撤回不完整接线。**这只是历史执行记录，不代表此次核实了具体拦截原因，也不代表普通账户登录功能本身不可实现。** 后续遇到权限/工具限制，应记录实际错误并按授权路径解决，不规避限制，不为求“能运行”取消鉴权。

没有提供真实节点 YAML、节点控制面、可信支付或正式 VIP 开通后台；没有迁移真实数据库。历史测试的通过范围见第 9 节。

## 3. 本次核对的环境基线

| 项目 | 本次结果 |
| --- | --- |
| FlClash HEAD | `c7be7023d33615cb624148d41414f80a7d96cede` |
| FlClash Git 状态 | 原有实现和未跟踪文件保留，HEAD/Mihomo/SDK pin 不变。第八轮增加 P5/P6 源码/测试并运行生成器，未改 pubspec 或锁定依赖版本。第七轮 core/go.mod 最低 Go 语言版本改为 1.25 的变更保留；实际 Go 仍为 1.26.6。见第 20 节，无 commit/push |
| Mihomo 子模块 | 已初始化并实际核对为 `70f0570405c3c2c47bb113b88db95006d239b346`；子模块工作区干净 |
| `core/Clash.Meta/go.mod` | 已存在；2026-09-30 固定提交浅获取成功 |
| `.dart_tool/package_config.json` | 已生成；第三轮 `flutter pub get --enforce-lockfile` 退出 0，保留原锁定版本 |
| Flutter/Dart | `/Users/stevenlee/development/flutter`：Flutter `3.47.1 stable`，Framework `6655482ec0`，Dart `3.13.1`；已实测运行 |
| Rust 实际安装 | `~/.cargo/bin`，`rustc 1.95.0 (59807616e 2026-04-14)`；命令级脚本按仓库 pin 选择，不再使用旧默认版本 |
| Go / PostgreSQL / Node | `go1.26.6 darwin/arm64`、PostgreSQL `16.15`、Node `v26.10.0`；本轮测试实际版本 |
| macOS / Xcode | 第六轮开始前用户已完成 `-runFirstLaunch`；首次初始化检查与 SDK 查询均退出 0。现有 macOS `26.6.2` / Xcode `27.0` 下完整 `.app` 构建、真实 App 三阶段运行及退出通过；仍用命令级 DEVELOPER_DIR，不改全局选择，历史 CoreSimulator/许可证阻塞已解除 |
| Android 工具链 | JDK `17.0.20`；SDK 位于 `/opt/homebrew/share/android-commandlinetools`，Platform 36、NDK `28.2.13676358` 已存在，许可证已接受；不是 `~/Library/Android/sdk` |
| Android 剩余依赖与运行目标 | 沿用前轮 SDK/AGP 缓存及 pin；第八轮离线 --rerun-tasks 验证 service/app Kotlin 编译和 126 项 JVM 测试，零失败/错误/跳过；arm64 CGO vet/c-shared 构建通过。adb 无设备且本机无模拟器系统镜像；APK/JNI 整包与设备生命周期仍未验收 |
| 应用版本声明 | `pubspec.yaml`: `0.8.98+2026091401` |
| Dart 约束 | `pubspec.yaml`: `>=3.10.0 <4.0.0` |
| 本地 CI 声明 | `FLUTTER_VERSION=3.47.1`、`GO_VERSION=1.26.4`、`NDK_VERSION=r28c` |
| Rust 声明 | `plugins/rust_api/rust/rust-toolchain.toml`: `1.95.0` |

版本号来自**这份本地 checkout 的声明**，不是在线查询的最新版本或可下载性保证。`.agents/project.md` 中有与当前 pubspec 不一致的旧版本说明；应结合实际锁文件、CI 和生成器验证，不照抄旧说明升级或降级依赖。不要为了通过生成器错误而盲目提升 Dart language version。

2026-09-29 的 SSH/HTTPS/Flutter 获取失败与 Core 离线缺依赖属于历史结果，见 `artifacts/p0-p1-20260929/build-baseline.json`。2026-09-30 已使用用户安装的 SDK，并通过命令级 HTTPS URL 覆盖及 `git submodule update --init --recursive --depth 1` 获取固定子模块；没有修改 `.gitmodules` 或关闭校验。完整历史克隆因下载慢主动停止，浅获取成功后已核对真实 HEAD。

`scripts/flclash-env.sh` 为单次命令补齐工具路径，进入 `FlClash` 后执行原参数，Rust 读取仓库 pin。第三轮增加 Xcode 选择：仅在未显式设置 `DEVELOPER_DIR`、全局仍选 CommandLineTools（或未设置）且标准 Xcode 存在时，为子进程指定 Xcode；不改全局 xcode-select，也不接受许可证。显式环境变量优先。新增 `python3 scripts/test-flclash-env.py`，5 项调用契约测试通过。最新日志目录为 `artifacts/p0-p1-20260930-r3/`。

第三、四轮测试使用 CommandLineTools；第六轮使用已初始化的完整 Xcode，两个 build_assets 始终为 true。macOS 工程沿用 Swift Package Manager，没有切换 CocoaPods。LaunchAtLogin 按工程原有 main 分支从官方 HTTPS 获取，记录提交 `9a894d799269cb591037f9f9cb0961510d4dca81`；仅本次 SwiftPM 缓存收窄 fetch 到 main、使用 HTTP/1.1，未改全局 Git/TLS 或依赖源码。完整 App 构建运行已补齐，历史失败不再是当前阻塞。

## 4. 现有后端协议：接线时不要重新猜测

协议来源：[API 实现](backend/internal/api/native.go)、[路由注册](backend/internal/api/server.go)、[计费规则](backend/internal/billing/client.go)。统一错误结构为 `{"error":{"code":"...","message":"..."}}`，响应禁止缓存。

### 4.1 接口与响应包裹层

| 方法 | 完整路径 | 输入 | 成功响应 |
| --- | --- | --- | --- |
| POST | `/api/v1/client/login` | `email,password,device_id,platform,app_version` | `{token,user,session}` |
| GET | `/api/v1/client/session` | Bearer token | **直接返回状态对象**，没有外层 `session` |
| GET | `/api/v1/client/config` | Bearer token | `{yaml,version,session}`，**不是裸 YAML 响应** |
| POST | `/api/v1/client/traffic` | Bearer + JSON 累计计数 | `{session,replayed}` |
| POST | `/api/v1/client/logout` | Bearer + JSON `{}` | `{logged_out:true}` |

`platform` 仅接受 `android`、`macos`、`windows`、`linux`。`device_id` 必须是非零 UUID。禁止发送额外的 `user_id`、`plan_id`、`rate_permille` 等字段冒充后端决策；现有严格 JSON 解码会拒绝未知字段。

原生 token 是 32 字节随机值编码得到的 43 字符 URL-safe 字符串，数据库保存 SHA-256 哈希；不是网页 JWT，也不使用网页 Cookie。原生请求不必伪造浏览器 Origin；网页 CORS/CSRF 限制必须保持。

`/api/v1/client/bootstrap` 是旧的网页鉴权元数据接口，不是本次原生配置下载接口，不要接错。

### 4.2 状态对象及关键含义

| 字段 | 含义 |
| --- | --- |
| `session_id`、`expires_at` | 原生会话身份与绝对到期时间；**当前 expires_at 不是套餐到期时间** |
| `server_time`、`subscription_expires_at` | 服务端 UTC 时间、套餐绝对到期；无套餐时后者为 `null` |
| `authorization_expires_at` | 允许连接时取服务端时间 +90 秒、会话到期、套餐到期的最早值；拒绝时 `null` |
| `session_idle_timeout_seconds`、`profile_version` | 固定 180 秒闲置超时；本会话绑定配置的 SHA-256，未绑定为空 |
| `can_connect`、`reason` | 服务端授权判断；仍不代表配置已在本机通过验证和加载 |
| `remaining_bytes`、`total_bytes`、`used_units`、`plan_name` | 用户订阅共享余额及展示信息；`used_units` 单位为 1/1000 字节 |
| `last_sequence`、`upload_bytes`、`download_bytes` | 此会话已确认的序号和累计上报字节数，不是用户所有设备的合计 |
| `report_interval_seconds`、`lease_seconds` | 当前固定 60 秒上报、90 秒授权窗口 |
| `metering_source`、`rate_permille` | 当前为 `client_reported`、`1000` |

VIP 刚登录通常得到 `reason=profile_required` 和 `can_connect=false`，因为还没用 `/config` 绑定套餐周期。**不得把这个状态当成“不是 VIP”或直接禁止其下载配置。** 配置成功返回后，再完成本机验证、计量初始化和连接门禁。

状态读取成功会更新会话最后访问时间，但目前独立计量库只有成功上报才推进它自己的授权截止时间；UI 状态轮询不能擅自替代计量授权。

本轮独立库按请求开始时间保守换算有效授权，不在响应到达时重新取得完整 90 秒；新客户端拒绝缺少新授权字段的旧服务端响应。升级需先迁移/升级后端再灰度客户端，详见 [P1 协议契约](docs/native-client-protocol.md)。

### 4.3 必须映射的业务状态

| code / reason | 客户端行为 |
| --- | --- |
| `invalid_credentials` | 留在登录页，提示账号或密码错误 |
| `native_session_required` / `native_session_expired` | 停止代理、清除内存会话，要求重新登录 |
| `profile_required` | 已登录，继续拉取配置，不直接判为免费用户 |
| `upgrade_required` / `paid_vip_required` | 展示升级入口，不启用代理 |
| `subscription_expired` / `quota_exhausted` | 停止代理，展示续费或额度不足提示 |
| `device_limit` | 不启用代理，提示退出其他设备 |
| `subscription_changed` | 停止代理；新周期或同周期权益指纹变化均需重新登录、重新绑定 |
| `profile_changed` | 停止代理；按安全同步顺序下载、验证、应用和重新确认，不沿用旧 YAML |
| `client_config_unavailable` | 显示线路配置未就绪，可重试；不能回退到旧配置连接 |
| `traffic_conflict` / `traffic_sequence` | 停止代理并要求重新认证；记录不含秘密的诊断信息 |
| 网络失败 / 非法响应 / 5xx | 明确连接失败或授权中断；不能显示假成功 |

HTTP 200 也可能包含 `can_connect=false`。尤其流量最终结算可以成功，但不再有连接权限。**“报告被接收”和“继续代理获授权”必须分开判断。**

### 4.4 计数与事务约定

发送内容示例仅表示结构：

```json
{"sequence":1,"upload_bytes":1048576,"download_bytes":2097152}
```

这是会话累计值，不是本分钟增量。下一批序号递增 1，计数单调不减。响应不确定时必须重发原批次的相同内容，收到确认前不能用新计数覆盖待确认批次。后端以 `(session_id,sequence)` 去重，重复请求也返回最新共享余额。

后端写事务顺序为用户 → 原生会话 → 订阅；沿用此顺序，避免和购买/续费互锁。原生会话绑定订阅 ID 与 `starts_at`，旧周期会话不能向新周期扣费。超过余额的声明用量仍入账，余额最低返回 0。

本轮另外以 `plan_id/is_test/allow_dedicated/max_devices` 指纹约束配置授权；同周期权限变化后的尾账仍可结算，但响应拒绝继续连接。设备名额只统计当前有效指纹；同套餐同周期增加期限/额度保持原计数。正常 `/session`/`/traffic` 均检查 YAML 版本/撤回，正常确认节奏约 60 秒，不是推送或节点即时撤销。

现阶段只采用上传 + 下载的 1 倍计费，不支持靠总计数区分普通 0.5 倍和专线 1 倍。改倍率必须一起设计逐节点证据、账本快照与客户端估算，不能只改一个常量。

## 5. 推荐接入结构与不可破坏的规则

以下结构中的登录页、公开账户状态、类型化 RPC、Go 协调器、受控 HTTPS 和启动门禁已于 P2–P3 落地，P4 已补齐受管配置实际应用、归属与受限节点 UI；**Meter/真实计数、运行中计量断连及平台运行验证仍为 P5–P6 目标**：

```text
Flutter 登录页 / 账户状态 / 受管配置 UI
  ↕ 类型明确的 Core RPC（敏感参数与返回值不进日志）
Go Core 原生账户协调器（内存 token、配置版本、会话代次）
  ├─ HTTPS 登录 / 配置下载 → demo.hyshentou.cn
  ├─ managed.Meter → 内核累计代理流量 → 每 60 秒上报
  └─ 连接授权门禁 → 当前平台既有启动/停止所有者
       桌面：DesktopCoreLifecycle / SetupAction / 系统代理清理
       Android：ServiceState / ServiceController / VPN 服务
```

已由 Go Core 持有 token 并执行账户通信。Flutter 只接收必要公开状态，密码仅在登录调用时短暂传递，不新造 JWT，不把 token 放入偏好、URL、YAML 或命令行参数。未来计量接线仍必须避免 Flutter 与 Go 各自维护上报序列和授权计时器。

所有会话切换使用 generation/revision：旧请求、旧 stop 回调、旧状态事件不能影响新登录。网络会话协调器只管理授权，**不能成为第二个进程/Android 服务生命周期所有者**。桌面仍经 `CoreController` 和 `DesktopCoreLifecycle`，Android 仍经 `ServiceState` 收敛开始/停止意图。

生产 RPC phase 为 `signed_out / authenticating / profile_required / loading_configuration / configuration_applied / restricted / unavailable`，会话失效返回 `signed_out`。`configuration_staged` 仅保留旧兼容枚举及不带配置引擎的内部测试接缝，生产协调器必须安装配置引擎。`ready/connecting/connected/stopping` 等运行态应在 P5 条件齐备后接入。Core 先启动为不监听、不加载历史配置的控制状态以支持 RPC；“Core 已启动”或“配置已应用”均不等于“VPN 已连接”。

## 6. 按顺序执行的实施任务

### P0｜建立可复现构建与安全基线

**修改范围：** 原则上先不改业务代码；必要的构建修复单独提交。

**当前进展：P0 的最低平台门槛已验收。** 第六轮已在当前 Mac 完整构建 `.app` 并实际运行 Application、原生插件、包内 Go Core 和 Rust IPC；三次独立 App 进程及 Core 均确认正常退出，保留旧配置/autoRun 的隔离启动通过。SDK/子模块和原自动化基线保持；Android 编译/JVM 有独立通过证据。此结论不包含 APK 安装、分发签名/公证或所有平台后台/TUN，详见第 9.10、18 节。

1. 保存当前 16 个业务变更文件，记录 FlClash commit、未跟踪目录与文件哈希。为根目录后端选择明确的备份/版本管理方式，不假设已有 Git。
2. 确认 SDK 实际安装位置；按本地 CI/锁文件配置 Flutter/Dart、Go、Rust、Xcode 工具链。Android 另外配置 JDK 17、SDK 和 NDK。
3. 在获得所需网络访问条件后初始化固定子模块，安装锁定依赖；先构建上游基线，记录原有失败，再加入定制功能。
4. 执行第 9 节隔离后端和计量库测试；不要使用真实 `.env` 跑注册、购买或迁移测试。
5. 阅读并遵循 FlClash 生成代码、生命周期、UI 和本地化规范。模型/provider/数据库或 ARB 变更后运行生成器，不能手写生成文件。

**完成标准：** 至少一个目标平台能构建运行；隔离测试可重复；已记录 SDK 版本、子模块状态及基线问题。缺少 SDK 时可以补代码和文档，但不能宣称 App 验收通过。

### P1｜固定协议、准备隔离夹具并补关键后端状态

**主要文件：** `backend/internal/api/native.go`、`native_test.go`、`internal/config/*`、`internal/model/native.go`、`FlClash/core/managed/client.go`、相关新测试。

1. **已完成并验收：** 同一公共 JSON 的 8 个案例由后端、Go HTTP 库和 Dart 共同核对。原生契约及已有 RPC 专项 16 项通过，完整 Flutter 1,860 项通过，分析零问题。这里验证现有结构化消息容器，不代表已新增受管账户 RPC。
2. **隔离夹具已完成并测试：** 临时数据库创建账户/订阅；P1 私有 YAML 使用随机回环端口 SOCKS5 和专用 HTTP 目标，实际测试上传/下载及拒绝其他目标；不改真实用户权益。
3. **策略与服务端验证完成：** 60/90/180 秒含义固定，未绑定时每 60 秒 GET `/session`，绑定后单一 Meter 负责；已测免费/VIP 未绑定保活及 180 秒边界。App 定时器/协调器仍属 P2/P3，未实现。
4. **已实现并测试：** 新增套餐绝对到期与有效授权截止；服务端取会话/套餐/90 秒最早截止，独立库同步严格校验和保守计时；覆盖精确到期、请求延迟、时钟偏差及不足一字节额度。
5. **后端机制/策略完成：** 指纹覆盖同周期套餐/专线/设备上限/测试来源变更；状态/报告检查配置版本及撤回；取配置不清空累计账。新增并发名额、配置替换/撤回/重绑、续费及失效尾账用例全部通过。手动同步按钮与真实内核安全重载仍属 P2/P4/P5；无热更新推送或节点侧撤销保证。
6. **网页回归通过：** 原认证/购买测试优先使用临时 DSN，不再跳过；前端源码和同源代理保持不变，Chrome 代理测试 11 项通过；生产测试权益禁用规则未改变。

**当前结论：P1 已完成验收（2026-09-30，第三轮）。** 后端、独立库、公共 Go/Dart 契约、期限/状态边界、隔离夹具和网页回归均有实际通过记录。后续 P2/P3 和 P4 分别以第六轮、第七轮独立证据验收，不由 P1 推导；计量/真实断连与平台验收仍属于 P5–P6。完整契约与同步顺序见 [native-client-protocol.md](docs/native-client-protocol.md)。

**完成标准：** 字段契约、时限和状态矩阵明确；新增边界测试通过；旧网页认证/购买回归不退化。

### P2｜添加 Core 账户协调器和类型明确的 RPC

**已有入口：** `FlClash/lib/core/controller.dart`、`interface.dart`、`method.dart`、`core/constant.go`、`core/method.go`、`core/managed/*`。

**当前状态：P2 阶段验收通过（第六轮）。** 原账户协调器与 RPC 不重写；完整 macOS App 已通过真实 Flutter → Rust IPC → Go Core → Gin → 临时 PostgreSQL 的登录、配置暂存、失效、取消与换会话路径。包内生产 Core 默认装配也实际启动通过。测试地址只存在于显式 managed_acceptance 标签的独立测试 Core，普通构建排除该文件；线上 HTTPS 与实际 VPN 开启后的网络行为仍待 P6/P7。Flush 的真实计量语义继续归 P5。

1. Login 使用显式匿名客户端，不发送虚假 Bearer；登录成功后验证原生 token、用户及 session，再安装私有 Client。
2. 状态/报告响应保持 64 KiB 上限；配置 JSON 上限 8 MiB，解码 YAML 上限 1 MiB，检查非空、UTF-8、NUL、SHA-256、会话 ID 和绑定版本。70 KiB、1 MiB 及越界用例均通过。
3. P2 注册 `managedLogin`、`managedLoadConfig`、`managedStatus`、`managedFlush`、`managedLogout`、`managedReset` 六个 RPC；P4 追加 `managedSelectProxy`。Dart/Go 常量与结构化响应同步，参数大小/未知字段受限，节点选择须带当前 generation 和 configuration_id，不接受任意 profile 或 YAML。
4. 通用 Core 调用日志不再输出 `$arguments`。公开快照不含 token、YAML 或密码；原始服务错误归一成白名单错误码，不直接渲染到 UI。
5. 生产目标固定为上述 HTTPS 地址，无 RPC host 覆盖；独立 transport 保留证书验证、拒绝跳转、忽略环境代理，并设置 DNS/TCP/TLS/响应超时和连接上限。
6. Android 在绑定 Core 事件监听时启动非 VPN 网络 DNS 观察器，账户 DNS/TCP socket 共用 protect 路径；本地 IPv4/IPv6、DNS、保护失败测试通过。桌面不使用环境代理，但 TUN 开启后的真实路由/系统 DNS 行为仍须 P6 真机确认，不能用单元测试替代。
7. 登录、下载、刷新、退出由单一协调器串行处理，generation 隔离迟到结果；Core 每 60 秒 GET 保活，UI 每 2 秒只读缓存快照。退出立即清空本地状态，并限时撤销远端会话。**`managedFlush` 当前返回 `meter_not_ready`，没有伪造报告；P5 接入真实 Meter 后才实现最终上报并接管保活。**

**完成标准：** 不依赖真实账号即可完成桥接测试；密码/token 不进入日志；未登录不能启动监听；旧回调不能终止新会话。先验证桥接，再开启全局登录门禁，避免再次出现无登录入口的锁死状态。

### P3｜登录页、账户状态与冷启动门禁

**当前状态：P3 阶段验收通过（第六轮 macOS 隔离 App）。** 真实表单覆盖错误密码、重复提交/取消迟到响应、免费/过期/耗尽、VIP 暂存、服务器会话失效；旧 profile/autoRun、返回操作及底层 setup/listener 尝试均不放行。实际 Core 重启后要求重新登录；全新 App 进程保留安装 UUID 但不恢复账户，正常退出后无自有 Core 残留。英语原生画面已检查，四语言沿用通过的 widget 回归；OS 深链接、托盘/热键及 Android 全部系统入口的设备验收仍归 P6，不能随本阶段勾选。

**主要文件：** `lib/application.dart`、`bootstrap.dart`、`pages/home.dart`、`providers/actions/core.dart`、`providers/actions/setup.dart`；新增账户 provider/模型与登录视图，补四种语言 ARB。

1. 应用入口先显示登录状态页，不先渲染可用代理首页。启动可初始化窗口、控制 RPC 和必要资源，但不自动应用本地旧 profile、不执行 `autoRun`、不恢复旧 VPN。
2. 表单支持邮箱/密码、提交中去重、错误提示、重试、退出应用；避免返回键/深链接/旧路由栈绕开门禁。登录请求迟到时必须检查当前 generation 和页面存活。
3. 登录成功后读取服务端会话：免费用户显示升级页面；`profile_required` 进入配置流程；提供升级、重新检查权益和退出登录入口。
4. 不把 token 写进 `SharedState`、preferences、SQLite、备份、启动参数。稳定安装 UUID 存储与账户 token 生命周期分开；换用户时必须清除旧状态。
5. UI 沿用 `material_ui` 和既有布局/本地化模式，不顺带重做整个应用。新增 ARB key 覆盖 `en`、`zh_CN`、`ja`、`ru`，通过生成器生成 getter。
6. 明确异常恢复：应用冷启动重新登录；运行中 Core 重启时建议直接停止并重新登录，直到有经过验证的无漏报恢复方案。不得悄悄用旧 token/旧计数重新授权。

**完成标准：** 冷启动、登录失败、免费用户、已过期/无额度、退出后返回/快捷键均不能连接；VIP 正常进入加载阶段；密码不保存。

### P4｜受管配置加载与移除自定义配置入口

**当前状态：P4 阶段验收通过（2026-09-30，第七轮）。** 配置/入口要求及实际 App 验证见第 9.11、19 节。第七轮确实没有开放连接；第八轮已沿配置所有者接通第 4 项的真实计数、尾账和首次 Meter 授权，详见 P5、第 20 节，不恢复旧 setup/preload 路径。

**主要文件：** `core/managed_configuration.go`、`core/managed/configuration.go`、`profile_store.go`、`coordinator.go`、`managed.go`、`lib/models/managed_configuration.dart`、`views/managed_proxies.dart`、`views/managed_preferences.dart`、导航及 profiles/setup/backup actions。没有新增 Drift 迁移，也没有批量删除用户旧配置。

1. 移除侧栏与移动端底部的 **Profiles/YAML 配置入口**，保留用户选择服务端下发代理节点的 Proxies 页面。更改导航项后，将已持久化的失效 page label 重置到有效页；PageController 初始 index 不能是 -1。
2. 为受管 profile 建立明确标记及归属（用户/会话或配置代次），不用可冲突的随意固定 ID。必要时增加 Drift 迁移并运行生成器；旧用户自建配置不批量删除，只从本产品的自动加载/可选集合隔离。
3. `/config` 取得 `{yaml,version,session}` 后校验 SHA-256，再通过现有 Mihomo 验证入口进行语义验证。采用临时文件与原子替换，失败不污染上一份完整文件；验证错误输出不得泄漏节点密码。
4. 配置准备流程按顺序执行：停止旧代理并处理旧计数 → 下载/校验 → 保存受管配置 → 应用配置成功 → 首次计量授权确认 → 允许连接。现有 `setupConfig` 与 `preloadInvoke` 可并行，受管路径不能在验证/应用前先启动旧监听。
5. 不仅删菜单：检查 URL/文件/二维码导入、订阅深链接、自动更新旧订阅、备份恢复、YAML 预览/导出、资源侧载与 external-controller 等入口。禁止绕回任意旧配置；备份不包含受管秘密。DNS/TUN/系统代理等非订阅设置可保留，但不能改写授权来源。
6. 现有 profile patch/overwrite/脚本可能改变最终配置；为受管 profile 定义允许的本地覆盖范围，至少不允许脚本替换账户线路或恢复未授权 profile。
7. 退出、失效、换用户后移除当前受管配置引用并清理本功能创建的敏感文件；启动清理残留。不要增加通用任意路径删除 RPC，也不要承诺本机管理员无法提取配置。

**完成标准：** UI 无配置管理入口，深链接/旧配置/导出不能绕过；VIP 配置在内核实际成功应用，错误 YAML、缺失配置和换账号均安全停止。

### P5｜接入真实累计计数、每分钟上报与断连

**当前状态：P5 阶段验收通过（第八轮）。** 真实 macOS App 的两次传输、实际一分钟报告、图表清零、显式断连与 PostgreSQL 字节/扣量账本一致；Core 补测 DIRECT 排除、响应丢失、额度耗尽及存量流关闭。具体数字和复跑入口见第 9.12、20 节。

**主要文件：** `core/managed/{meter,coordinator,runtime}.go`、`core/managed_runtime.go`、`core/{hub,lib,managed}.go`、`lib/providers/actions/setup.dart`、`actions/system.dart`、账户模型/provider/页面。

1. 将 `ReadTotals` 接到内核的累计代理上下行统计（核实 `statistic.DefaultManager.TotalTraffic(true)` 在已初始化子模块中的实际含义）。不能积分 UI 每秒速率，也不能让 `onlyStatisticsProxy` 展示选项改变计费范围；明确排除本机直连和控制 API 流量。
2. 普通断开/重连、配置刷新、清空 UI 图表不重置计费累计值。已将 `handleResetTraffic` 改为只更新显示基线，不再调用底层 ResetStatistic；计费直接读取累计原值，`onlyStatisticsProxy` 只影响展示。
3. 每个会话只允许一个上报写入者；保持已确认基线、当前累计、待确认批次三者独立。服务端返回共享余额，客户端展示服务端确认余额与本地保守预估时要区分含义。
4. `managed.Start` 会首先调用 stop 回调、读取 `/session` 并执行一次 Flush；只有配置已经在后端绑定、内核已准备好时才启动它。第一次回调不是业务故障，不要造成 UI 死循环。
5. stop 回调要幂等、短且按 generation 校验，不能同步重入 Meter 的 `Flush` / `Close`。当前 Flush 持有 `reportMu` 时也可能执行 stop，错误接线会产生锁重入/死锁；加入真实回调而非只有原子计数器的测试。
6. 真正停止包含新连接门禁、已有 TCP/UDP、各类 listener 和平台 TUN；不得仅更改 `isStartProvider`、停止 UI 定时器或隐藏“连接成功”。网络恢复只恢复授权状态，不应由迟到的报告擅自重启 VPN。
7. 正常断连和退出时先阻止新流量、关闭存量连接、取得最终稳定计数，再完成有截止时间的最后上报与可选 Logout。随后销毁会话。不得先 reset 计数或先撤销服务端 token。
8. 已改为可取消的报告/采样等待，关闭取消旧 HTTP，不再无界等待 done/WaitGroup。Disconnect 与会话释放各有 2 秒预算，Flutter 正常退出配合原有 3 秒 watchdog；80ms 调用截止测试证明阻塞报告能被取消。截止到达只能报告尾账未确认，不能声称一定结清。
9. `SampleTotals(context.Context)` 已带错误；生产采样在同一 Go 进程读取已初始化 Mihomo 的原子累计，不依赖 UI/IPC 的默认 0。负数、回退、溢出或采样失败保守停止；Core 重启要求重新登录，不重置旧基线后自动续接。
10. 上报失败立即停止是当前库行为，不是继续使用 90 秒宽限。90 秒是额外授权失效边界；Go 定时器不保证系统休眠/Android 挂起期间被调度，恢复路径需重新确认授权后才能打开监听。

**完成标准：** 实际流量计数和账本匹配；重试不重扣；本地额度耗尽/服务端拒绝/网络中断会停止真实连接；关闭/换账号/内核重启不死锁、不误断新会话。

### P6｜覆盖桌面与 Android 的所有启动/恢复入口

**当前状态：代码及可执行自动化已完成；总体验收未完成。** macOS App/实际 Core 运行链、Android Kotlin/JVM 与 arm64 原生库、代理所有权策略均通过。没有 Android 设备/模拟器及 Windows/Linux 实际系统，不能把模拟生命周期或可移植 C++ 策略测试等同于系统实测。待补矩阵见第 20.6 节。

**桌面重点：** `lib/core/desktop/lifecycle.dart`、`service.dart`、`providers/actions/system.dart`、`manager/app_manager.dart`、`common/tray.dart`、`manager/hotkey_manager.dart`、系统代理/DNS 协调器。

保持 `CoreController`/进程 lease 单一所有权。原 `AppStateManager` 直接恢复 listener 的路径已移除，桌面网络类型变化/休眠恢复走停止意图，旧 startListener 只确认已授权的当前实例而不能自行开放。系统代理插件已记录原值并仅恢复仍匹配本应用写入值的字段，外部软件改写后不覆盖；失败立即停 Core。托盘/热键等最终门禁保留，真实 OS 深链接/睡眠/系统设置恢复仍需设备矩阵；强杀后不保证系统代理自动恢复。

**Android 重点：** `core/lib.go`、`lib/core/lib.dart`、`lib/manager/android_manager.dart`、`tile_manager.dart`、`vpn_manager.dart`、`android/app/src/main/kotlin/com/follow/clash/ServiceState.kt`、`ServiceStateMachine.kt`、`ServiceStateHost.kt`、`ServiceController.kt` 和相关 service/receiver。

无 Flutter UI 时由 Go Meter 和原生 ServiceController 的授权看护执行停止，真实串行状态仍由 ServiceStateMachine 负责。Quick Settings/通知直接走原生意图，不等待 Flutter tile 回调；SharedState 只提供 VPN 选项，启动必须带当前 generation/runtime_revision 重新确认，不能恢复旧 token/YAML。权限拒绝、服务丢失、授权拒绝均停止 Core、TUN、绑定服务/通知并尝试尾账；拒绝 startTun 时关闭其拥有的 FD。Always-on VPN、系统 revoke、后台调度等仍须真机验证。

已认证且后台服务仍存在时由 Core 计量，不依赖 Flutter 页面是否显示；UI 重建/新应用会话与仍存活旧 Core 的交界必须显式废止旧授权。若原生进程完全重建，内存 token 不存在，必须等待重新登录。停止回调应走既有 native stop 意图并完成 TUN/通知/服务状态收敛，而不只是关闭 Go listener。

**完成标准：** 每个最终声称支持的平台都有真实设备/系统验证；测试确实覆盖锁屏后台、睡眠唤醒、权限拒绝、系统撤销与快速启停。没有相应设备就明确记为未验收。

### P7｜端到端与安全回归

建立“临时数据库 + 真实 Go HTTP API + 定制 App/Core + 授权测试节点”的联调环境。已有 API 测试直接调用 Gin handler，已有 Meter 测试使用 mock server，两者分别通过**不代表协议已跨进程接通**。

至少在隔离环境完成：注册/准备账户 → 登录 → 获取 VIP 配置 → 连接并产生已知上传下载 → 60 秒报告 → 数据库确认扣减 → 余额到零 → 真实连接关闭 → UI 一致 → 重新登录/续费后的可控恢复。不得用修改生产余额来制造测试条件。

完整验收矩阵见第 8 节。还需运行原网站登录、Cookie 刷新和测试购买回归，确保后端新增表和路由不影响已上线网站；原有浏览器 E2E 会创建账户/订单，必须指向隔离环境后才运行。

**完成标准：** 构建/分析/单元/集成/跨进程及目标平台测试均有证据，且每个失败有处理结果。不存在“保证永远无 bug”的有效验收，交付应列明确切版本、已测矩阵和未覆盖风险。

### P8｜部署、迁移与可回滚发布

1. 先在测试环境完成显式迁移并验证旧数据库升级；当前测试验证的是新建隔离 schema 的重复迁移，不足以证明真实旧库升级无锁表/权限问题。
2. 正式发布前确认授权、备份与恢复演练，记录旧后端二进制/配置版本和新 App 构建号。先后端兼容扩展，再客户端灰度；不要删除网页路由。
3. 管理员提供私有套餐 YAML，确认可达性、套餐线路权限和文件访问权限；不能使用测试 YAML 中的保留地址作为真实节点。
4. 保留后端生产 HTTPS、Origin、Cookie 和数据库 TLS 限制；客户端直连 `demo`，网站继续通过 `test` 的 Next.js 同源代理。Nginx 不缓存原生接口，也不记录认证正文/完整 token。
5. 新二进制启动前执行经审核的迁移。失败回滚先停用新客户端能力、恢复旧二进制；新增表默认保留，不为回滚直接 DROP 已有会话/账本或删除业务数据。
6. 验证客户端升级、不同平台安装、签名/公证及许可证要求。保留 FlClash 原 LICENSE 和署名，并按实际分发方式审查对应源码交付等义务。

**完成标准：** 有迁移记录、真实域名/TLS 验证、灰度验收、回滚步骤；没有把测试权益开关或真实密钥带进发布包。

## 7. 代码定位图与预计改动清单

本节用于定位；“建议新增”不代表文件已经存在。不要把所有路径机械地全部修改，先跟踪调用链。

| 工作 | 现有定位 | 预计新增/调整 |
| --- | --- | --- |
| 启动与账户状态 | `lib/main.dart`、`bootstrap.dart`、`application.dart` | 账户 provider/状态模型、冷启动门禁 |
| 登录及账户 UI | `lib/pages/home.dart`、`lib/views/*`、`arb/intl_*.arb` | 登录视图、权益提示、升级/退出入口、生成本地化 |
| 受管配置与导航 | `views/navigation.dart`、`providers/state/navigation.dart`、`actions/profiles.dart`、`actions/setup.dart`、`models/profile.dart`、`database/*` | 受管 profile 标记与权限、必要迁移、移除导入绕过 |
| 跨语言协议 | `lib/core/{controller,interface,method,event}.dart`、`core/{constant,method,message}.go` | 受管账户 RPC/状态事件及 contract tests |
| 内核会话与计量 | `core/managed/*`、`core/{hub,common}.go` | 账户协调器/桥接、总计数与监听门禁 |
| Android 原生 | `core/lib.go`、`lib/core/lib.dart`、Android ServiceState 系列 | 启停拒绝、后台计量、socket protect 与 TUN 收敛 |
| 桌面恢复与退出 | `core/desktop/*`、`actions/system.dart`、`manager/app_manager.dart`、托盘/热键 | 会话代次、退出最终上报、系统代理清理 |
| 服务端补全 | `backend/internal/api/native.go`、`model/native.go`、`billing/client.go` | 有效授权截止、配置变更策略、异常/并发补测 |
| 新测试 | 现有 `FlClash/test/core/`、`test/providers/`、`test/pages/`、`test/manager/` | 受管会话、登录/导航、计量接线、平台 contract、端到端夹具 |

修改 FlClash 的模型/provider/数据库 schema 时运行 build_runner；修改 ARB 时运行 intl_utils。生成文件由工具产生，不手动复制上一轮撤回内容。保持 `.agents/rules.md` 的注释密度、生命周期和 UI 分层约束。

## 8. 最终验收矩阵（App 全链路仍待；API/Meter/网页历史子层见第 13 节，本轮账户/门禁子层见第 9.8、16 节）

| ID | 场景 | 通过条件 | 验证层 |
| --- | --- | --- | --- |
| A01 | 冷启动且存在旧配置/autoRun | 必须先登录，无代理监听/已建立连接 | App + Core + 平台 |
| A02 | 错误密码、重复点登录、请求迟到 | 明确失败、不重复创建有效状态、旧结果不覆盖新状态 | UI + API |
| A03 | 免费/测试套餐用户 | 升级提示；没有正式权益不得下载/连接 | UI + API |
| A04 | VIP 登录返回 profile_required | 自动继续下载，不能误显示为免费 | 契约 + App |
| A05 | 合法大 YAML、损坏 YAML、缺失文件 | 合法内容加载；非法/缺失不可连接，不回退到旧线路 | API + Core |
| A06 | 所有配置入口及旧 page label | 无用户任意导入/导出绕过，导航 index 有效 | UI + 深链接 |
| A07 | 正常 60 秒报告 | 真实累计代理流量进入对应会话/订阅账本，1 倍扣减 | 跨进程 + DB |
| A08 | 服务器已扣费但响应丢失 | 原批次重试，只扣一次，其间新流量进入下一批 | Meter + API |
| A09 | 多设备共享余额和并发报告 | 不覆盖余额；重复响应返回最新余额 | 并发 DB + App |
| A10 | 零余额、套餐到期、账户停用 | 拒绝新接入并关闭已有 TCP/UDP/TUN；UI 与原生一致 | Core + 真机 |
| A11 | 网络失败/超时/错误响应 | 明确停止，不无限离线授权；恢复不被旧请求擅自启动 | 网络故障注入 |
| A12 | 暂停/重连/清空图表 | 不重置计费累计，不漏报或双扣 | Core + Meter |
| A13 | Core 崩溃、重启、计数回退 | 旧会话不被重新放行，清楚提示重新登录/恢复失败 | 生命周期 |
| A14 | 退出时不足一分钟的流量 | 停止后最终累计被结算；网络失败有明确限制记录 | DB + 退出路径 |
| A15 | 关闭与新登录竞争 | 不死锁；旧回调不能停止新会话；可验证关闭时限 | -race + 故障注入 |
| A16 | 快捷键/托盘/Quick Settings/Always-on | 未认证或授权失效时无法绕过门禁 | 各平台 |
| A17 | Android 锁屏、后台、撤销 VPN 权限 | 后台计量或保守停止，服务状态和 TUN 一致 | Android 真机 |
| A18 | 桌面睡眠/唤醒/网络切换 | 恢复代理前重新确认授权；不遗留坏系统代理 | 桌面系统 |
| A19 | 同设备替换、换用户、订阅新周期 | 旧 token/配置/计数不能用于新用户或新周期 | API + App |
| A20 | TLS、重定向、DNS/代理循环、日志 | 不降级证书校验，不泄露密码/token/YAML | 安全回归 |
| A21 | 网站回归 | 网页登录/刷新 Cookie/退出/测试购买保持原行为 | 隔离网页 E2E |
| A22 | 旧 schema 升级与回滚 | 保留既有用户/订单；新增功能失败可回滚二进制 | 迁移演练 |

为每项记录：平台/系统版本、源码 commit、SDK 版本、命令、结果、必要日志或截图位置、未覆盖项。敏感账户信息与节点密码需脱敏。

## 9. 历史与本轮测试证据、接手命令

### 9.1 已有历史证据，非本轮重跑

上一轮记录显示以下命令通过：

- `python3 scripts/test-native.py`：隔离 PostgreSQL + `go test -race -count=1 -v ./...`；`TestNativePostgres` 的四组子测试通过，原 `TestPostgresIntegration` 因未授权真实数据库测试而跳过。
- 后端 `go vet ./...` 通过。
- `go test -race -count=1 -v ./managed` 的五项测试通过：累计计费/零额度、不确定提交重试、授权过期/计数回退、并发 Flush 与关闭、HTTP/TLS/重定向限制。
- 独立库 `go vet ./managed` 通过。

已有计量测试用原子变量模拟采样和停止回调，主要直接触发 Flush/状态边界；没有证明真实 60 秒调度、平台断连或阻塞回调的可取消性。不得因为看到 PASS 就勾选第 8 节 App 验收项。

### 9.2 接手先执行的只读检查

```bash
cd /Users/stevenlee/Desktop/vpn

git -C FlClash status --short
git -C FlClash rev-parse HEAD
git -C FlClash submodule status
command -v flutter dart go cargo rustc initdb pg_ctl
```

### 9.3 可复跑的隔离业务测试

以下不连接真实 `.env` 目标；仍先检查脚本未被修改。PostgreSQL 脚本适用于有 Unix socket 的本地开发环境，不应以 root 启动 initdb。

```bash
cd /Users/stevenlee/Desktop/vpn
python3 scripts/test-native.py

cd backend
RUN_DB_TESTS=0 GOPROXY=off GOTOOLCHAIN=local go vet ./...

cd ../FlClash/core
GOPROXY=off GOTOOLCHAIN=local go test -race -count=1 -v ./managed
GOPROXY=off GOTOOLCHAIN=local go vet ./managed
```

`GOPROXY=off` 用于已缓存依赖的可复现离线检查；缺依赖时应按允许的下载流程补齐，不能关闭 TLS/GOSUMDB。不要把 `NATIVE_TEST_DSN` 指向真实数据库，脚本会自动生成私有临时 DSN。

### 9.4 补齐工具链后的 FlClash 验证

以下为复跑入口。旧下载/许可证失败见历史第 9.6、14 节；用户在第六轮前已完成 Xcode 首次初始化，第八轮完整 macOS 构建和实际 App 运行已通过。继续使用命令级 `scripts/flclash-env.sh` 选择现有完整 Xcode，不改全局选择或重复接受许可证：

```bash
cd /Users/stevenlee/Desktop/vpn

git -C FlClash submodule status
bash scripts/flclash-env.sh flutter --version
bash scripts/flclash-env.sh dart --version
bash scripts/flclash-env.sh flutter doctor -v
bash scripts/flclash-env.sh flutter pub get --enforce-lockfile

# 模型/provider/schema 变更后执行。
bash scripts/flclash-env.sh dart run build_runner build --delete-conflicting-outputs
# ARB 变更后执行。
bash scripts/flclash-env.sh dart run intl_utils:generate

bash scripts/flclash-env.sh flutter analyze --no-pub
bash scripts/flclash-env.sh flutter test test/core/native_client_contract_test.dart --reporter expanded
bash scripts/flclash-env.sh flutter test --reporter expanded

# 已初始化 Xcode 下第八轮通过；正常入口不带隔离测试参数。
bash scripts/flclash-env.sh flutter build macos --debug --no-pub --target=lib/main.dart
```

Flutter/Dart 命令会触发原生 build hooks。纯 Dart/Flutter 测试需要时可按 `.agents/commands.md` 临时关闭两个 build_assets，但必须恢复为 true；不得将关闭 hooks 的包当作完整构建。完整构建需准备 Go/Rust/子模块，不在这里盲目运行 flutter clean 删除所有缓存。

```bash
# 已有 core 子模块和依赖后。
cd /Users/stevenlee/Desktop/vpn/FlClash/core
CGO_ENABLED=0 go test .
CGO_ENABLED=0 go vet .
go test -race ./managed
```

涉及 Android 的修改按 `.agents/commands.md` 先完成对应构建 hook，再使用 JDK 17 编译相关 Gradle 模块并在真机测试。涉及 Windows/Linux helper 时运行对应 Cargo 测试和目标系统检查。CGO 关闭的 Core 测试不能证明 Android JNI 文件通过编译。

### 9.5 首轮最终实际结果（2026-09-29，历史记录）

| 命令 / 验证 | 结果与证据 |
| --- | --- |
| `python3 scripts/test-native.py` | PASS；临时 PostgreSQL + 全后端 `-race -count=1`；原网页 10 组子测试、原生旧用例、新契约/权限/本机代理用例全部执行，无 SKIP |
| 后端 `go vet ./...` | PASS |
| `go test -race -count=3 -v ./managed` | PASS；10 项顶层测试连续三轮，每轮含 23 项共享/非法字段子用例 |
| 独立库 `go vet ./managed` | PASS |
| 前端 `npm run lint` / `typecheck` / `build` | 全部 PASS，通过仓库 `scripts/node.sh` 执行 |
| 前端 `npm run test:api-proxy` | PASS，11 项、0 失败、0 跳过；真实 Next.js 生产构建 + 独立 Chrome + 本机 mock 后端 |
| Core `CGO_ENABLED=0 GOPROXY=off GOTOOLCHAIN=local go test .` | FAIL，退出 1：缺依赖缓存，且 Mihomo 子模块仍未初始化；不能据此定位业务编译问题 |
| Flutter 契约测试 / analyze | 未执行：找不到 flutter，记录 127/`executed=false`，不算通过 |
| 各平台 App 构建和真实 VPN | 未执行/未验收 |

证据在 `artifacts/p0-p1-20260929/`：`go-results.json`、`frontend-results.json`、`build-baseline.json` 和对应 `.log`；首轮 Go 日志保留为 `initial-*`，新增本机网络夹具后已复跑最终 Go 回归。源码哈希仅固定验证时版本，不代表 Dart 文件已执行。TLS 负面用例中的 bad certificate 是预期拒绝。

### 9.6 第二轮实际结果（2026-09-30）

| 验证 | 实际结果 / 证据 |
| --- | --- |
| SDK 与环境入口 | Flutter 3.47.1、Dart 3.13.1、Rust 1.95.0、JDK 17.0.20 实测；脚本语法、参数边界、工作目录、显式环境覆盖及子进程退出码验证通过 |
| 固定 Mihomo 子模块 | PASS；浅获取后 HEAD 为 `70f0570405c3c2c47bb113b88db95006d239b346`，子模块工作区干净 |
| `python3 scripts/test-native.py` | PASS；后端 13 项顶层测试、无 SKIP，包含原网页 10 组子测试；仅临时 PostgreSQL |
| 后端 `go vet ./...` | PASS |
| `go test -race -count=3 -v ./managed` / `go vet ./managed` | PASS；10 项顶层测试连续 3 轮，共 30 个顶层 PASS |
| 前端 lint / typecheck / build | 全部 PASS |
| Chrome 同源代理回归 | PASS；11 项、0 失败、0 跳过；本机 mock 后端，不访问生产数据库 |
| 新 Dart 契约文件格式 | PASS；最终检查 1 文件、0 改动；仍有缺少包解析配置的 lint include 警告 |
| 全仓只读格式基线 | 退出 1；563 文件、79 个候选格式差异；`--output=none` 未批量改写源码，需完整依赖后复核，未手写生成文件 |
| `flutter pub get --enforce-lockfile` | 下载阶段 800 秒超时；未生成有效 `package_config.json`。离线检查退出 1，至少缺 `animations 3.0.0` 缓存 |
| `flutter test --no-pub ...native_client_contract_test.dart` | 退出 1，无法解析 flutter_test/test 依赖；没有运行契约用例。pubspec 已有 flutter_test，不应重复添加依赖掩盖问题 |
| `flutter analyze --no-pub --no-fatal-infos` | 退出 1；大量包 URI 无法解析及衍生诊断，不能视为有效的业务源码分析结论 |
| 完整 Core `CGO_ENABLED=0 ... go test -count=1 -v .` | 默认代理尝试在下载阶段停止；命令级备用 GOPROXY 尝试也在 600 秒超时。没有 Core 测试 PASS，后续完整 Core vet 未执行 |
| Android Build Tools/CMake、Rust Android target 安装 | 两个安装命令分别在 450 秒超时；不能记为安装完成 |
| `flutter build apk --debug --target-platform android-arm64 --no-pub` | 实际调用，Gradle assembleDebug 阶段 100 秒超时；未得到 `app-debug.apk`，没有安装运行验收 |
| macOS / Android 运行 | 未验收；缺完整 Xcode/CocoaPods，且无已连接 Android 设备或模拟器 |

本轮日志在 `artifacts/p0-p1-20260930/`。主要索引：`regression-results.json`、`flutter-validation-results.json`、`core-results.json`、`android-deps-results.json`、`dependency-observations.json`、`wrapper-results.json`、`wrapper-contract-results.json`。执行命令成功、测试实际执行、平台构建运行是不同证据，不能互相替代。

### 9.7 第三轮实际结果（2026-09-30，最新）

| 验证 | 实际结果 |
| --- | --- |
| `flutter pub get --enforce-lockfile` | PASS，退出 0，生成有效 package_config；没有升级锁文件 |
| 原生契约 + 既有 RPC 专项 | PASS，16 项；`native_client_contract_test.dart` 与 `protocol_contract_test.dart` 使用真实 Flutter 测试入口 |
| `flutter analyze --no-pub --no-fatal-infos` | PASS，零问题；修复一处测试 const 提示后复跑 |
| `flutter test --no-pub --reporter expanded --concurrency=4` | PASS，1,860 项；契约文件最终格式化后再次全量复跑通过，最后日志为 `flutter-test-final.log` |
| Dart 变更文件格式 | 依赖恢复后发现契约文件还需重排，已由 formatter 处理；没有批量改写旧轮 79 个候选文件 |
| `CGO_ENABLED=0 go test -count=1 -v .`（Core） | PASS，126 项顶层测试 |
| Core `go vet .` / `go build -tags=with_gvisor` | PASS；产生独立 macOS arm64 Core 二进制，不是 .app |
| Android arm64/cgo Core `go vet -tags=with_gvisor . ./tun ./platform` | PASS；使用已有 NDK 28.2，首次离线缺模块失败后正常下载并复跑通过；不是 APK 验收 |
| Rust `cargo test --locked --manifest-path plugins/rust_api/rust/Cargo.toml` | PASS，37 项、0 失败/忽略 |
| `python3 scripts/test-native.py` | PASS，临时 PostgreSQL，全后端 -race，13 项顶层测试、无跳过，含原网页 10 组子用例 |
| 后端 vet / managed -race 连续 3 轮 / managed vet | 全部 PASS；managed 每轮 10 项顶层测试，共 30 个顶层 PASS |
| 前端 lint / typecheck / build / Chrome 代理回归 | 全部 PASS；浏览器代理 11 项、0 失败/跳过，临时 mock 后端 |
| `python3 scripts/test-flclash-env.py` | PASS，5 项；语法/用法、参数与 cwd、显式环境覆盖、SDK 路径回退/Rust pin、退出码传递 |
| Xcode version / SDK / 首次启动 | version 为 27.0 / 27A266a；SDK 与首次启动检查返回 69，提示许可证未接受，未代用户同意 |
| 完整 macOS App 构建请求 | 工具执行前拦截，没有进程/退出码/构建产物；不是编译失败或构建成功 |
| Android 额外组件安装请求 | 工具执行前拦截；本轮没有通过其他通道重试安装，Android App 构建运行仍未验收 |

最新证据目录：`artifacts/p0-p1-20260930-r3/`。`pub-result.json`、`core-results.json`、`regression-results.json`、`flutter-results.json`、`flutter-final-results.json` 记录各执行阶段；最后一次完整 Flutter 复跑以 `flutter-test-final.log` 和对应已结束 Job 的退出码 0 为准。Rust、Android NDK vet 和环境测试另有专用日志。`blocked-actions.json` 记录工具拒绝的请求，不伪造为进程日志。原生 build hooks 始终启用；这些测试不等于运行了定制 VPN App。

### 9.8 第四轮 P2–P3 最终验证（2026-09-30）

以下均为本轮实际执行结果，证据目录 `artifacts/p2-p3-20260930/`。前期失败日志保留，最终结论以表中复跑结果为准。

| 验证 | 最终结果 | 证据 |
| --- | --- | --- |
| Go Core / managed | 128 / 26 项顶层测试通过，含结构化 RPC 与本地 HTTP/DNS 集成 | `go-final.json` |
| managed 竞态 | `-race -count=3` 三轮通过 | `managed-race.log` |
| Go vet / 独立 Core 构建 | Core + managed vet、macOS arm64 with_gvisor 构建通过 | `go-vet-final.log`、`core-build.log`、`core-macos-arm64` |
| Android Go 检查 | arm64/cgo + with_gvisor vet 通过 | `android-vet.log` |
| Flutter 专项 / 旧启动回归 | 49 项 / 73 项通过；均纳入全量回归 | `flutter-managed-second.log`、`flutter-legacy.log` |
| Flutter 全量 / 分析 | **1,893 项通过；No issues found** | `flutter-all-final.json`、`flutter-analyze.log` |
| Android 服务门禁独立 JVM | 14 项通过，编译真实 `ManagedServiceGate.kt`，不等于 APK | `android-gate-standalone.log` |
| 隔离后端 | 13 项顶层测试通过，含临时 PostgreSQL 下网页/原生业务回归 | `backend-regression.log` |
| 现有 Next.js 网站 | typecheck、lint、隔离 Chrome 代理测试 11 项通过 | `web-typecheck.log`、`web-lint.log`、`web-proxy-final.log` |
| 完整 Android Gradle JVM 测试 | **未通过：离线缺少 Kotlin DSL 插件 6.4.1，未执行测试** | `android-unit.log` |
| Xcode 首次启动检查 | **退出 69，完整 App 构建运行仍未验收** | `xcode-first-launch.log` 与执行结果；该日志为空，不伪造错误文本 |

上述范围没有完整 .app/APK 安装启动、真实节点流量、真实平台断连或线上 HTTPS 联调。P2–P3 自动化通过不能替代 P0/P4–P8。Rust 37 项、环境脚本 5 项为第三轮历史证据，本轮未重复执行，不计入本轮新增通过项。

### 9.9 第五轮平台构建与门禁验证（2026-09-30）

证据目录：`artifacts/platform-validation-20260930-r5/`。旧测试结果不计为本轮重跑。

| 验证 | 本轮结果 | 证据 |
| --- | --- | --- |
| macOS 完整 Debug App 构建 | 初次退出 69（许可证）；SDK 探测恢复后重建退出 1，缺少 CoreSimulator 框架，Xcode 插件无法加载 | `macos-build-result.json`、`macos-build-recheck.log`、`macos-build-recheck-result.json` |
| macOS 首次初始化 / App 运行 | 首次初始化检查 69；非交互 sudo 初始化请求返回需要密码，未运行初始化。完整 App 启动/登录未执行 | `macos-preflight-final.json`；第 17.4 节 |
| Android 修改模块与完整 JVM 回归 | 联网构建成功，293 个任务执行；common 30、service 23、app 67，共 120 项，失败/错误/跳过均为 0 | `android-gradle-online.log`、`android-results.json`、`junit/` |
| Android 离线强制复跑 | `--offline --rerun-tasks` 再次成功，293 个任务执行；同一 120 项全通过，报告时间戳确认不是复用旧测试结果 | `android-gradle-offline-final.log`、`android-offline-execution.json`、`android-offline-results.json`、`junit-offline/` |
| 正式 Android 门禁 JUnit | 9 项通过，纳入上述 app 的 67 项，不与总数重复相加 | `junit/app/TEST-com.follow.clash.ManagedServiceGateTest.xml` |
| 当前源码独立 Core 构建与真实 IPC | macOS arm64 with_gvisor Core 构建成功；2 个真实进程场景通过：旧配置拒绝、IPC EOF 与 SIGTERM 清理 | `core-macos-arm64`、`core-cold-gate.json` |

独立 Core 场景仅覆盖 Python 宿主 → Unix IPC → 实际 Core，不包含 Flutter App、HTTP 登录、系统代理或 TUN。正式 Android JVM 测试同样不代表 APK/JNI 安装运行。**P4 准入暂不通过：尚缺至少一个完整 App 的隔离登录门禁验收。**

### 9.10 第六轮完整 macOS App 与 P2/P3 验收（2026-09-30）

证据目录：`artifacts/macos-acceptance-20260930-r6/`。本轮失败/停止的获取尝试保留；下表只列实际完成的最终检查。

| 验证 | 最终结果 | 证据 |
| --- | --- | --- |
| Xcode 初始化 / SDK | 两项退出 0，用户已完成首次组件初始化 | `preflight.json` |
| 完整 macOS Debug App | `flutter build macos --debug --no-pub` 通过；集成测试结束后再次以正常 `lib/main.dart` 构建通过 | `macos-build-third.json`、`macos-build-final.log`、`macos-build-final.json` |
| 包内生产 Core | 使用默认 CoreController、真实 Rust IPC 实际启动，旧配置/autoRun 不放行，正常 App/Core 退出 | `app-run-1/result-bundled.json`、`app-run-1/summary.json` |
| 实际 App 登录和生命周期 | 三个独立 App 进程阶段均由 FlutterDriver 判定通过，19 条命名检查记录（含重复冷启动检查）；真实 Gin/临时 PostgreSQL，不是认证 mock | `app-run-1/result-first.json`、`result-reopen.json`、`app-*.log`、`summary.json` |
| 原生画面 | 实际 App 渲染的登录与 VIP 页面截图已检查，无溢出；仅测试账户 | `app-run-1/login-bundled.png`、`free.png`、`vip.png` |
| Flutter 全量 | 1,893 项，失败/跳过均 0；集成测试另列，不混入根测试数 | `flutter-regression.jsonl`、`flutter-regression-result.json`、`verified-counts.json` |
| Flutter 分析 | No issues found；集成 target/driver 同时纳入分析 | `analyze-final.log` |
| Go Core / managed | 128 / 26 项顶层测试通过；vet 及 managed `-race -count=3` 通过 | `go-tests.jsonl`、`go-results.json`、`go-vet.log`、`managed-race.log` |
| 后端隔离回归 | 临时 PostgreSQL 下全后端 race 回归 13 项顶层测试通过；标准套件不编译 opt-in 桌面夹具 | `backend-regression.log`、`backend-regression-result.json` |
| Android 当前源码/依赖 | 联网编译/JVM 通过后 `--offline --rerun-tasks` 复跑通过；120 项、无失败/错误/跳过，逐个 XML 时间戳校验 | `android-online-r6.json`、`android-offline-final.json`、`android-offline-final.log`、`junit-final/` |
| 生产构建隔离 | 包内 Core 构建信息不含 managed_acceptance，二进制不含测试环境变量字符串；最终产物不是 integration_test 入口 | `macos-build-final.json` 和对应构建/断言结果 |

**P4 准入通过，但仅允许进入开发，不是发布批准。** 本轮没有真实节点连接/收费流量、线上 HTTPS、系统代理修改、TUN、Android 设备或其他桌面系统验收。官网浏览器测试仍为第四轮历史证据，不能计作第六轮重跑。

### 9.11 第七轮 P4 最终验证（2026-09-30）

证据目录：`artifacts/p4-20260930-r7/`。以下均为本轮实际执行，失败日志保留，最终以复跑日志和退出码为准。

| 验证 | 最终结果 | 证据 |
| --- | --- | --- |
| Flutter 全量 / 分析 / 生成 | **1,927 项通过，零失败/跳过；No issues found**；intl_utils 与 build_runner 已执行 | `flutter-final.jsonl`、`verified-counts.json`、`analyze-final.log`、`localization.log`、`codegen-final.log` |
| Go Core / managed | **133 / 38 项顶层测试通过**，含真实 Mihomo 应用和受管文件/归属/竞态边界 | `core-confirmed.jsonl`、`verified-counts.json` |
| 竞态 / 静态检查 | managed `-race -count=3` 共 114 次顶层执行通过；Core/managed vet 通过 | `managed-race-confirmed.jsonl`、`core-vet-confirmed.log` |
| Android Core | arm64/cgo + with_gvisor vet 与 **c-shared 实际构建**通过；不是 APK/JNI 设备验收 | `android-core-vet-confirmed.log`、`android-core-build-confirmed.log`、`libclash-android-arm64.so` |
| 后端隔离回归 | 临时 PostgreSQL 下 race 回归 13 项顶层通过，含原网页与原生业务；未访问真实库 | `backend-race.log`、`native-validation.json` |
| 真实 macOS App | bundled / first / reopen 三阶段 driver 退出 0；24 条命名检查，三个 App/Core 正常退出全部确认 | `app-run-2/summary.json`、`result-*.json`、`app-*.log` |
| 实际界面 | 已检查受管节点及偏好页面截图，无配置管理入口或画面溢出；四语言窄屏另由 widget 回归覆盖 | `app-run-2/managed-nodes.png`、`managed-preferences.png` |
| 正常 macOS App / 测试隔离 | 测试后重新构建 lib/main.dart 成功；包内 Core 不含 managed_acceptance 或测试主机环境变量 | `macos-build-final.log`、`production-build-check.json` |

24 条命名检查包含重复冷启动等场景，不应夸大成 24 个独立业务用例。Flutter 总数是正式根测试的可见测试数，不含隐藏的 suite 加载项，也不把真实 App 场景重复加进去。Android 120 项 JVM、Rust 37 项、环境脚本及网站 Chrome 结果仍为前轮历史，本轮未重跑，不计入本轮通过数。没有开启代理、真实节点流量或 `/traffic`，因此 P5 不能勾选。

### 9.12 第八轮 P5–P6 最终验证（2026-09-30）

本轮不是仅检查接口或构建：已在真实 macOS App 内发起两次代理传输、等待真实一分钟报告，并核对隔离 Gin/PostgreSQL 的累计账本。最终结果如下，所有数量按本轮实际日志计算，不累计前轮成绩。

| 验证 | 本轮结果 | 相对 `artifacts/p5-p6-20260930-r8/` 的证据 |
|---|---|---|
| Flutter 静态分析 | 零问题 | `flutter-analyze-pass.log` |
| Flutter 全量（含 proxy 插件） | **1,957 项成功，0 失败/错误/跳过** | `flutter-all-pass.log`、`flutter-final-verification.json` |
| Go Core / managed | **140 / 40 项顶层测试通过，0 跳过**；包含 opt-in 的真实 60 秒测试 | `go-final-all.log`、`core-validation-final.json` |
| managed 竞态检测 | 40 项 × 3 轮 = **120 次通过** | `go-race-final.log` |
| Go vet | desktop Core/managed 及 Android arm64 Core/managed/tun/platform 通过 | `go-vet-final.log`、`android-vet-final.log` |
| 后端私有 PostgreSQL 回归 | **13 个顶层业务测试通过**，包括原网页业务；未使用真实 `.env` | `backend.log`、`native-validation.json` |
| Android Kotlin/JVM | service/app 编译通过；12 个 suite、**126 项测试，0 失败/错误/跳过**，离线强制执行 | `android-first.log`；新鲜 XML 位于 `FlClash/build/**/test-results/testDebugUnitTest/` |
| Android arm64 CGO | with_gvisor vet + c-shared 构建通过；不等于 APK/JNI 设备运行 | `android-core-final.log`、`libclash-android-arm64.so` |
| macOS 真实 App 三阶段 | bundled 3 / first 19 / reopen 5 条命名检查，共 **27 条记录**；三个阶段 App/Core 正常退出均确认 | `macos-e2e/summary.json`、`result-{bundled,first,reopen}.json`、日志/截图 |
| 正常 macOS App 构建 | `lib/main.dart` 整包重建通过，正常 Core 无测试主机入口 | `macos-build-final.log`、`production-build-check.json` |
| Windows 所有权策略 | 可移植 C++17 **5 个断言场景通过**；并非 WinInet/Windows OS 实测 | `windows-policy-build.log`、`windows-policy.log` |
| 工作区 | 原基线文件无丢失，`git -C FlClash diff --check` 通过，HEAD/子模块未变，Mihomo 工作区干净 | `workspace-baseline-comparison.json`、`workspace-final-status.txt` |

最终分钟报告测试实际间隔 **60.001907834 秒**，确认 upload=24,755、download=32,925 字节。独立的真实 macOS App 场景最终上传 **49,394**、下载 **65,812** 字节，4 条报告，PostgreSQL `charged_units=115,206,000`，即 `(49,394+65,812)×1,000`；这是固定倍率的内部计费单位，不是金额。显示清零后的第二次传输包含在最终尾账内；重开 App 后活动会话为 0，账本没有重复计费。

真实链路修复后的验证见 `real-traffic-first.log` 及后续 Go 日志；早期 Flutter `flutter-first.jsonl`、`flutter-confirmed.log` 等失败记录保留。最终通过以本节明确列出的日志为准。修复包括回环白名单、计量首报不确定状态、TUN FD 拒绝清理，以及同步新的 Core 先停/原生服务后停断言；没有跳过失败测试。完整实现与未验证范围见第 20 节。

## 10. 线上配置与部署前检查

后端新增项示例（**只用于管理员配置，不覆盖真实 `.env`**）：

```dotenv
CLIENT_PROFILE_DIR=/etc/asterlink/client-profiles
CLIENT_ALLOW_TEST_ENTITLEMENTS=false
```

私有目录按套餐 ID 提供 `starter.yaml`、`pro.yaml`、`max.yaml` 等实际需要的文件。服务账号只需读取权限，不能在前端 public、Nginx 静态根目录、Git 或安装包中放真实节点密码。后端文件检查不验证节点可达性或完整 Mihomo 语义，必须由接入后的 Core 与真实节点联调补上。

网站现有 `ALLOWED_ORIGINS` 应包含真实前端来源 `https://test.hyshentou.cn`；原生 App 直接使用 HTTPS/Bearer，不靠伪造 Origin。不要关闭生产 Cookie、数据库 TLS 或 CSRF 限制来让测试通过。

显式迁移命令仅在确认目标、备份和授权后执行：

```bash
cd backend
# 后续部署动作；本交接任务没有执行。
go run ./cmd/api -migrate
```

上线还需要确认原生认证/心跳入口的限流容量。当前 Go 不信任任意转发 IP，反向代理后可能按代理地址聚合限流；不能通过无条件信任所有 X-Forwarded-For 解决，应按可信入口设计限流并负载验证。

## 11. 功能验收与正式收费必须分开

完成 App 登录、受管配置、每分钟上报和断连，可以满足**合作式客户端的功能链路**，但不等于能防止修改版客户端逃费。

当前按套餐共享静态 YAML，不是每用户/设备独立代理凭据。用户设备可以提取配置、修改上报量，或用另一个客户端连接。卸载/强杀/离线造成的尾部流量也无法仅靠 App 完全追缴。一分钟服务端报告和跨设备共享余额存在延迟，不是严格实时配额执行；本地每秒保守预估也不能消除多设备同时消耗的窗口。

正式收费发布前另行完成：

1. 可信支付回调/经审计的授权后台，产生真实权益；不能重命名 `paid_test` 或把测试开关开放到生产。
2. 节点侧每用户/设备可撤销凭据、可信流量计量与服务端核算，覆盖缓存 YAML 及修改版客户端。
3. 节点侧配额/到期/撤销执行，必要时采用短期租约或额度预留，定义离线和超用上限。
4. 权威节点账本上线时明确切换口径，客户端报告只作诊断/核对或去重关联，不能同一流量再扣第二次；逐节点倍率以服务端已验证快照计费。
5. 审计、监控、异常上报、数据保留与备份恢复；日志只记录必要标识与错误码，秘密不入日志。

这些是正式运营门槛，不必阻塞 P0–P7 的隔离功能开发，但必须出现在发布验收中，不能省略为“后续优化”。

## 12. 接手任务清单与维护方式

本清单按阶段证据勾选。第六轮完成 P0/P2/P3，第七轮完成 P4，第八轮完成 P5 并实现 P6 平台代码与本机自动化。P6 的设备/系统矩阵尚未齐全，不能将代码通过扩大为全平台运行或发布保证。

- [x] P0 **至少一个平台完整构建/运行通过**：macOS Debug App、默认包内 Core/原生 IPC、隔离启动及 App/Core 正常退出有证据；Android 整包/真机仍不在此结论内。
- [x] P1 **已验收**：公共 Go/Dart 契约、授权时限、配置/权益变化、隔离夹具和网页回归全部通过；不包含后续 App 账户 RPC/受管配置接线。
- [x] P2 **阶段验收通过**：类型化 RPC、账户协调器及 macOS App → Core → 隔离真实 API/数据库链路通过；不含 P5 Meter/最终流量上报。
- [x] P3 **阶段验收通过**：macOS 实际登录/拒绝状态、取消/退出、Core 重启、App 重开、UUID 与旧配置/autoRun 门禁通过；后续平台系统入口仍在 P6 验证。
- [x] P4 **阶段验收通过**：受管配置、受限节点选择、入口收口及失效清理完成；第八轮沿原配置所有者接入 P5，没有恢复通用 setup。
- [x] P5 **阶段验收通过**：真实内核累计、60 秒报告、原批重试、实际断连和稳定尾账已接通；macOS App/Gin/PostgreSQL 账本匹配，Core 补测 DIRECT/额度/存量流/竞态。
- [ ] P6 **实现与本机自动化已完成，设备/系统总验收待补**：Android 原生无 UI 生命周期、桌面恢复/系统代理归属已改；126 项 JVM 与原生库通过，但 Android 设备、Windows/Linux OS、真实系统设置/TUN/睡眠矩阵尚未通过。
- [ ] P7 第 8 节矩阵及网页回归完成，剩余风险记录齐全。
- [ ] P8 测试环境迁移、域名联调、灰度与回滚完成。
- [ ] 正式收费前：第 11 节节点侧控制和真实权益来源完成。

阶段状态和过时说明在原文更新；新的执行内容追加记录，保留并标明历史事实：

```text
日期 / 操作者：
阶段与源码 commit：
实际改动文件：
测试环境与命令：
结果与证据位置：
仍未完成 / 已知风险：
下一步入口：
是否涉及真实环境（默认否）：
```

### 可直接交给下一位开发者的任务说明

> 请先读第 9.12、20 节，继续补 P6 设备/实际 OS 验收和 P7 完整矩阵，而不是重新实现 P5。真实 Meter、分钟报告、原批重试、尾账和显式连接已经接通；保留 generation/runtime_revision、旧实例退休屏障、单一上报者和配置所有者。不要恢复 setupConfig/quickSetup/旧 profile 自动加载，不直接透传 session.can_connect，不清零计费累计。优先准备 Android 设备验证后台/Doze/通知/QS/revoke/Always-on/TUN，再验证 Windows/Linux 系统代理及 macOS 实际休眠/系统设置恢复。已有真实 macOS App/Gin/PostgreSQL 夹具可复跑；线上 TLS/YAML、部署迁移及正式收费另按 P8/第 11 节授权推进。保留全部旧改动及 artifacts，正常包不得带 managed_acceptance。

## 13. 首轮 P0–P1 执行记录（2026-09-29，历史记录）

### 13.1 起点与安全边界

已详细阅读原交接文档、根目录说明及 FlClash 规则/技能。起点和终点 HEAD 均为 `c7be7023d33615cb624148d41414f80a7d96cede`，没有 commit/push。根目录非 Git，使用镜像备份和 SHA-256 保存原 16 个业务文件及会调整的旧文件；备份不含真实 `.env`，`.gitignore` 新增本轮 artifacts 忽略规则。

未访问、部署或重启线上 `test.hyshentou.cn` / `demo.hyshentou.cn`；未迁移真实数据库或修改真实用户/订单标志；未修改系统代理/TUN、全局 Git URL 或 build hooks。

### 13.2 本轮实际改动文件

| 类别 | 路径与完成内容 |
| --- | --- |
| 后端业务，修改 | `backend/internal/api/native.go`、`backend/internal/model/native.go`：5 个状态字段、有效授权最早截止、2 个追加列、权益指纹、配置检查及设备容量；同周期失效尾账仍可结算 |
| 既有后端测试，修改 | `backend/internal/api/native_test.go`：复用隔离 DB 工厂、修正旧测试 YAML 并改回环地址；`integration_test.go`：临时 DSN 分支执行完整网页测试，不加载真实 `.env` |
| 后端测试，新增 | `backend/internal/api/native_contract_test.go`、`native_p1_test.go`、`native_fixture_test.go`：公共快照、精确期限、权限/设备并发、配置重绑/撤回、保活与可控网络夹具 |
| 独立库，修改 | `FlClash/core/managed/client.go`、`meter.go`、`meter_test.go`：新状态校验、请求开始时间保守计时、保留结算拒绝原因、升级原有模拟服务器 |
| 跨端契约，新增 | `FlClash/core/managed/contract_test.go`、`FlClash/test/fixtures/native_client_contract.json`、`FlClash/test/core/native_client_contract_test.dart`；Go 已执行，Dart 未执行 |
| 文档/保护 | 新增 `docs/native-client-protocol.md`；原文更新 `HANDOFF.md`、`docs/native-client-integration.md`、`README.md`；修改 `.gitignore` |

`store.Migrate` 原有 AutoMigrate 已包含模型新增列，没有增加破坏性迁移。锁文件、FlClash 主程序/导航/RPC/平台生命周期及前端业务源码未修改。未改 Flutter 模型/provider/schema/ARB，无需生成器，也未手写生成文件。

### 13.3 实测结论与未完成项

第 9.5 节 PASS 命令已实际完成；最终后端日志含 `TestNativeControlledLoopbackProxy`、`TestNativeP1Postgres` 和不再跳过的 `TestPostgresIntegration`，临时数据库日志确认已停止。Meter 原 5 项与新增 5 项顶层测试连续三轮通过。Chrome 使用独立临时资料与 mock 后端，不读取个人登录资料；11 项通过不等于浏览器已连真实 Go API 或 App 已验收。

P0 未达至少一个平台构建运行标准：子模块 SSH 验证失败、HTTPS 重试超时；固定 Flutter 获取超时；缺完整 Xcode；Rust 版本/PATH 不符；Android SDK 未找到；Core 离线缓存不全。实际失败已保留，没有降级安全验证或改版本制造通过。

P1 的后端/独立库、公共 JSON、隔离数据库/网络夹具、网页回归及策略文档已完成；**仍需补跑 Dart 契约和 Flutter 分析，才完成本阶段全部验证。** Dart 测试只覆盖现有消息容器，没有新增账户 RPC。手动同步的 App 操作、空闲页定时器、真实内核采样/断连、各平台构建、旧生产库升级和线上原生接口均未完成。

### 13.4 当时的下一轮入口（最新状态见第 15 节）

补齐固定 SDK/子模块和完整平台工具链，保留当前 pin，SSH/网络修复走授权途径，不关闭校验。实际运行新增 Dart 契约、Flutter analyze、Core 基线及 macOS 构建后再更新 P0/P1 勾选。之后进入 P2，从 `core/managed/client.go` 的 Login/Config 方法、专用受控 transport、单一账户协调器和类型明确 RPC 开始；遵循本轮尾账、配置同步与保活所有权，不在 Flutter 页面里另建报告计时器。

## 14. 第二轮 P0/P1 遗留推进记录（2026-09-30，历史记录；最新见第 15 节）

### 14.1 已完成的新增推进

在用户安装开发环境后恢复同一工作区。FlClash HEAD 仍为 `c7be7023d33615cb624148d41414f80a7d96cede`，没有 commit/push。核实 Flutter/Dart、Rust、JDK、SDK/NDK 的实际路径和版本；发现 local-agent 的 PATH 没有包含新安装路径，新增命令级环境入口解决。SDK 安装本身已可用，无需把依赖下载失败误判为需要重装 Flutter/Rust。

固定 Mihomo 子模块已从未初始化恢复到准确提交 `70f0570405c3c2c47bb113b88db95006d239b346`。首次完整历史下载慢，主动停止后改为浅获取，最终成功并读回 HEAD；没有改子模块版本、`.gitmodules`、全局 Git URL 或证书/主机密钥校验。

原 P1 后端、独立计量库与网页隔离回归全部重跑通过，准确计数见第 9.6 节。新增 Dart 契约文件由 Dart formatter 修正格式，并以只读格式检查复验；没有改其断言行为，也没有将未运行的用例记录为通过。

### 14.2 本轮准确改动范围

| 路径 | 实际变化 |
| --- | --- |
| `scripts/flclash-env.sh` | 新增；为单次命令设置已安装工具路径、按项目声明选择 Rust、进入 FlClash 后原样执行参数；有语法和调用契约验证 |
| `FlClash/test/core/native_client_contract_test.dart` | 仅格式化已有 SHA-256 expect 断言；不改契约语义 |
| `HANDOFF.md` | 原文更新环境、P0/P1 状态、接手命令、下一轮入口；新增第 9.6、14 节，旧轮记录标明历史 |
| `README.md`、`docs/native-client-integration.md`、`docs/native-client-protocol.md` | 原文更新 SDK/子模块现状、验证限制、日志位置和环境入口 |

共 6 个源码/文档文件。开始前备份 38 个原文件到本轮 `baseline/`，不含真实 `.env`。`pubspec.yaml`、`pubspec.lock`、Go mod/sum、后端/managed 业务代码、共享 JSON、Flutter 模型/provider/ARB 与受跟踪平台源码均未修改；两个 build_assets 保持 true。标准工具产生的依赖缓存、被忽略的 Android 本机 SDK 路径文件与日志不属于发布改动。

### 14.3 剩余阻塞已经具体化

Pub 的详细诊断证明 `animations 3.0.0` 元数据和压缩包请求均返回 HTTP 200；压缩包长度为 37,456,175 字节，下载流未在诊断时限内完成。正常 pub get 也在下载阶段超时。离线解析失败表示本机缓存不足，不证明该版本不存在；未采纳工具的降级建议，未手工伪造包缓存/解析配置。完整 Core 默认/备用代理尝试仍在下载模块，无法给出编译/测试通过证据。

Android 安装路径中的 Platform36/NDK28.2 已确认，但初始 Build Tools 为 34.0.0，CMake 3.22.1 和 Rust `aarch64-linux-android` target 未安装完成。补充安装尝试超时；APK 命令在 assembleDebug 阶段超时，不能声称所有 Gradle/原生组件已编译。macOS 缺完整 Xcode 与 CocoaPods；Android 没有连接设备，也没有已安装模拟器/system image。基于当前工具尝试 Android arm64 不代表缩减最终平台范围。

全仓格式基线仅作记录：在 package_config 缺失时有 79 个候选差异，不能以此为由批量改动上游或生成代码。依赖恢复后重新运行格式、分析和测试，针对真实失败最小修复。

### 14.4 下一轮直接继续的位置

先完成标准包管理器下载，保留锁文件、TLS 和 Go 校验和验证。不要从安装 SDK 或克隆整个上游重新开始，也不要直接开启 P2 登录门禁。可在项目根目录复用：

```bash
bash scripts/flclash-env.sh flutter pub get --enforce-lockfile
bash scripts/flclash-env.sh sdkmanager \
  --sdk_root=/opt/homebrew/share/android-commandlinetools \
  'build-tools;36.0.0' 'cmake;3.22.1'
bash scripts/flclash-env.sh rustup target add --toolchain 1.95.0 aarch64-linux-android

# 依赖安装成功后再运行；这些是待通过的验收命令。
bash scripts/flclash-env.sh flutter test test/core/native_client_contract_test.dart --reporter expanded
bash scripts/flclash-env.sh flutter analyze --no-fatal-infos
bash scripts/flclash-env.sh flutter test --reporter expanded
bash scripts/flclash-env.sh flutter build apk --debug --target-platform android-arm64
```

完整 Core 继续在 `FlClash/core` 执行 `CGO_ENABLED=0 GOTOOLCHAIN=local go test -count=1 .`、`CGO_ENABLED=0 GOTOOLCHAIN=local go vet .`，再按仓库 CI 用已有 NDK 执行 Android arm64/cgo 的 vet。构建成功后仍需真实 Android 设备或专用模拟器安装/启动验证；macOS 路径则先补齐完整 Xcode/CocoaPods。纯 Dart 测试确需临时关闭 hooks 时遵循仓库规则并恢复，不把关闭 hooks 的产物当完整构建。

### 14.5 交付边界与证据

本轮没有接入 P2–P6，没有改动正式权益、账户、订单、真实 `.env` 或线上服务；没有启用系统代理/TUN，没有用个人浏览器资料测试。隔离测试对应的 Go 源码哈希在复核时保持一致；证据索引与差异保存在本轮 artifacts 中。P0/P1 清单在第二轮结束时保持未完成；该结论属于当时状态，第三轮已完成 P1，见下节。

## 15. 第三轮 P0/P1 验证记录（2026-09-30）

### 15.1 已完成的推进

网络调整后，固定 Dart 依赖通过 `--enforce-lockfile` 安装成功，原生和 RPC 契约 16 项首次完整运行通过。随后完成 Flutter 全量 1,860 项、分析零问题；依赖恢复后的 formatter 对原生契约文件作纯格式调整，再次全量复跑通过。P1 的公共字段/包裹层、授权期限、状态矩阵、隔离夹具、Go/Dart 验证及旧网站回归均已满足第 6 节标准，清单已勾选 P1。

P0 新增证据包括：Core 126 项顶层测试、vet、带 with_gvisor 的 macOS arm64 独立 Core 构建；Rust 原生库 37 项测试；已有 NDK 的 Android arm64/cgo Core vet。后端/计量库/网页回归全部重跑通过。Xcode 27.0 已找到，环境脚本解决“已安装但系统仍指向 CommandLineTools”的命令级路径问题；没有改全局设置。

### 15.2 本轮准确改动

| 路径 | 变化 |
| --- | --- |
| `scripts/flclash-env.sh` | 增加有条件的命令级 DEVELOPER_DIR 选择；保留显式覆盖，不同意许可证、不安装组件 |
| `scripts/test-flclash-env.py` | 新增 5 项可复跑环境入口自动测试；不读取真实业务 .env，不更改主机设置 |
| `FlClash/test/widgets/scrollbar_inset_test.dart` | 将可常量构造的测试 variant 改为 const，消除唯一分析提示 |
| `FlClash/test/core/native_client_contract_test.dart` | 用已完成包解析的 Dart formatter 重新格式化；不修改断言语义 |
| `HANDOFF.md`、`README.md`、`docs/native-client-integration.md`、`docs/native-client-protocol.md` | 原文更新当前状态、已通过证据与剩余项；交接新增第 9.7、15 节 |

共 8 个源码/文档文件。起点与终点 FlClash HEAD 均为 `c7be7023d33615cb624148d41414f80a7d96cede`，Mihomo 仍为固定 `70f0570405c3c2c47bb113b88db95006d239b346`；没有 commit/push。开始前备份 716 个文件。后端 internal 与 managed 目录对本轮基线逐文件比较一致；没有重写已通过的 P1 业务代码。锁文件、生成文件、App 主程序、导航、RPC、生命周期和生产配置未改动，两个 build_assets 一直为 true。

### 15.3 为什么 P0 还没有勾选

完整 Xcode 位于 `/Applications/Xcode.app`。显式 DEVELOPER_DIR 后 version 可运行，但 `xcrun --show-sdk-path`、Swift/首次启动检查仍返回 69，内容为尚未同意 Xcode 许可证。本轮已提示用户在终端阅读并确认，没有自动接受协议或索取管理员密码。

另有执行工具限制：Android 额外组件安装请求、完整 macOS App 构建请求返回“因 OpenAI 无法确定请求的安全状态，已拦截此工具调用”。两项请求均没有命令启动或退出码；没有通过变换命令、替换工具或降低安全检查来重试它们。这是实际工具结果，不代表已定位到业务源码编译错误，也不代表普通 VPN 客户端功能不可开发。

因此当前只能确认代码测试、独立 Core 构建与 Native Assets hooks；没有完整 .app/APK 的安装启动证据，不能用 Flutter 单元测试的宿主进程替代 App 验收。macOS 工程是 Swift Package Manager，缺 CocoaPods 不能继续作为未经验证的必需阻塞。Android 完整应用及其他平台也不能随 NDK vet 一起宣称通过。

### 15.4 下一轮只需继续的事项

> 本节是第三轮结束时的历史接续说明；第四轮已完成 P2–P3 的代码和自动化验证，当前接续任务以第 16.6 节为准。P0 整包构建运行门槛仍保留。

用户先在本机终端执行下面的交互命令，阅读并决定是否同意许可证；接受后按 Xcode 提示完成首次启动组件：

```bash
sudo /Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild -license
# 完成上一步后，按需进行首次初始化：
sudo /Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild -runFirstLaunch
```

随后通过正常允许的执行路径核验 `bash scripts/flclash-env.sh xcodebuild -checkFirstLaunchStatus`、完整 macOS 构建、隔离数据目录下的 App 启动/退出和内核随宿主清理。构建前仍核对工作区并保留未跟踪代码；不得把真实用户的旧 profile/autoRun 或系统代理带入基线启动测试。P0 满足至少一个目标平台完整构建运行后再勾选，不承诺仅接受许可证就一定通过后续构建。

P1 不需要重新实现。P0 验收后进入 P2 的单一账户协调器、受控 HTTP transport、Login/Config 与类型明确 RPC，再按 P3–P7 完成产品链路。P1 通过并不自动赋予 App 登录、配置下发、真实计量/断连或节点侧可信收费能力。

### 15.5 证据与安全边界

本轮证据位于 `artifacts/p0-p1-20260930-r3/`，包含 baseline/sha256.json、各测试日志/结果、final-summary.json、final-sha256.txt 和本轮受跟踪差异。日志中的故障注入、bad certificate、主动 panic 等在对应测试 PASS 时属于预期负面用例，不应当误报为真实运行崩溃。

未操作线上域名/服务、真实数据库/用户/订单、真实 .env、系统代理/TUN 或个人浏览器登录资料。测试仅使用临时 PostgreSQL、回环夹具与独立 Chrome。未发布、未上传、未签名公证客户端；所有尚未验证的平台和部署继续保留原验收门槛。

## 16. 第四轮 P2–P3 执行记录（2026-09-30，历史记录；最新见第 17 节）

### 16.1 本轮目标与准确边界

已完整阅读原交接文档和项目规则，按用户要求继续原生 FlClash 客户端，而不是新增 Next.js 网站登录页。完成 Core 账户协调器、受控 HTTP、六个 RPC、登录/账户页、会话及冷启动门禁。**本轮没有把配置下载成功等同于内核已运行，也没有提前连接代理来制造“完成”效果。**

FlClash HEAD 保持 `c7be7023d33615cb624148d41414f80a7d96cede`，Mihomo 保持 `70f0570405c3c2c47bb113b88db95006d239b346`。未 commit/push，未改依赖锁文件、版本 pin 或真实配置。开始前保存 `baseline.tar.gz` 和 `baseline-flclash.diff`；归档 SHA-256 为 `8d284b481bda34bb2515364c529849d02317478cb3be79b01738322b53ed6fc0`。归档包含根交接/README/docs/scripts、FlClash 规则、lib/test/arb/core（排除 Mihomo 子模块）和 pubspec/lock；不包含真实 .env，也不包含 Android/backend。Android 起点为 Git 跟踪的干净版本，原文件可由固定 HEAD 恢复；本轮未修改后端业务源码。

### 16.2 实际改动文件与职责

| 文件组 | 本轮落地内容 |
| --- | --- |
| `core/managed/client.go`、新增 `transport.go` / `coordinator.go` | 匿名登录、配置校验、专属网络、私有账户生命周期和 Core 保活；原 Meter 算法保留 |
| 新增 `core/managed.go` / `managed_desktop.go` / `managed_android.go`，修改 `constant.go` / `method.go` / `hub.go` / `lib.go` | 六个 RPC、严格参数与响应、停止清理、listener/配置/quickSetup/startTUN 门禁 |
| `lib/core/interface.dart` / `controller.dart` / `method.dart` / `lib.dart` | 类型化 RPC、移除参数日志、Android 拒绝启动/失败回滚 |
| 新增 `lib/models/managed_account.dart`、`providers/managed_account.dart`、`pages/managed_account.dart` | 账户 DTO、稳定安装 UUID、冷启动/失联状态机、表单与权益页、取消/退出、四种语言 UI |
| `lib/application.dart` / `bootstrap.dart` / `providers/action.dart` / `providers/actions/core.dart` / `setup.dart` / `manager/app_manager.dart` | 不再自动加载/更新旧 profile，不恢复 autoRun；深链接不导入；Core 重启重新登录；恢复时显式检查账户 |
| Android `ServiceController.kt`、新增 `ManagedServiceGate.kt`、`NetworkObserveModule.kt` / `ServiceModules.kt` | 服务绑定前独立检查 Core 门禁；非 VPN DNS 在登录前可用，观察器由 Core 事件监听生命周期单一持有 |
| 四种 `arb/intl_*.arb` 及生成的 `lib/l10n/*`、`providers/generated/*` | 每语言新增 33 个账户文案；通过 intl_utils/build_runner 生成，不手写生成代码 |
| `lib/models/generated/clash_config.g.dart` | build_runner 同步了原 source enum 已存在的 DOMAIN_WILDCARD、REMATCH_NAME、PROCESS_PATH_WILDCARD、PROCESS_NAME_WILDCARD 四个序列化映射；没有修改 enum 源定义 |
| 新增 `core/managed/coordinator_test.go` / `transport_test.go`、`core/managed_rpc_test.go` | HTTP/DNS、账户并发、代次、期限、大小/哈希、错误脱敏和 RPC/门禁测试 |
| 新增 Dart `test/helpers/managed_fakes.dart`、`test/providers/managed_account_test.dart`、`test/widgets/managed_account_test.dart`、`test/core/managed_protocol_test.dart` | 账户/表单/持久化/迟到响应/四语言窄屏/旧入口拒绝测试 |
| `test/core/lib_test.dart`、`test/providers/action_test.dart` / `setup_action_test.dart`、`test/manager/core_manager_test.dart` | 新增 Android listener 失败测试；旧启停测试显式建立已授权测试前提，不放松生产门禁 |
| 新增 `android/tests/standalone/ManagedServiceGateCheck.kt`、`scripts/test-managed-android-gate.py` | 用已有缓存编译真实服务门禁并执行 14 项 JVM 检查，不下载依赖、不冒充 APK 测试 |
| `HANDOFF.md`、`README.md`、`docs/native-client-integration.md`、`docs/native-client-protocol.md` | 原位更新已过时的“未接线”状态；保留历史记录并新增本节及第 9.8 节 |

表内 Core/Dart/Android 路径均以 `FlClash/` 为根，最后两行根目录文档/脚本除外。初始已存在的 `test/widgets/scrollbar_inset_test.dart`、原生契约文件和 `core/managed/` 未被清理。仅看 Git diff 会遗漏未跟踪的新功能文件，备份/交接必须包含这些路径。

### 16.3 登录、会话与门禁的实际行为

冷启动先初始化控制 Core，然后停止旧 listener 并执行 `managedReset`，成功后才允许提交登录。账户密码从输入框提交后立即清空；仅随机安装 UUID 写入 preferences，token 和下载 YAML 只留在 Go 私有内存。UI/Core 失联或 Core 重启都会忘记旧账户，不用旧 token/计数自动恢复。

登录成功后，免费/非正式付费/过期/额度耗尽/设备超限分别显示对应提示；用户可打开官网、刷新权益、退出或取消登录。VIP 的 `profile_required` 自动进入配置下载，验证后为 `configuration_staged`。服务端 session 内的 `can_connect` 是服务端权益，不是本机门禁；**RPC 顶层 `can_connect` 和 Dart `.canConnect` 在本阶段始终为 false**。配置未经过 Mihomo 应用及 Meter 初次确认，不能连接。

HTTP 操作由单一协调器串行执行；请求携带本地 generation，取消/退出/换账号先改变代次，旧响应不能安装账户、覆盖配置或停止新会话。UI 每 2 秒查询缓存 RPC 不访问后端；Core 在未接 Meter 的阶段每 60 秒 GET `/session`，1 秒检查保守空闲/绝对截止。永久会话失效清空身份，网络失败停止并显示不可用状态；退出立即停止和清空，远端撤销限时 2 秒，失败显示 `logout_unconfirmed`，不复用旧 token。

未登录及配置暂存阶段，旧 profile、autoRun、恢复启动、深链接导入、测速/资源侧载、直接 listener、quickSetup/startTUN 和原生 ServiceController 最终启动入口均被门禁拒绝。Android Core 拒绝时不会继续调用服务启动；服务失败会撤销已启动 listener。上述代码与测试不是已运行 VPN 后断连的证明，运行态/后台/系统代理归 P5/P6。

### 16.4 可复跑命令与本轮修复

从项目根目录执行（生成和 Flutter 测试使用已安装 CommandLineTools，不接受 Xcode 许可证、不关闭 native build hooks）：

```bash
DEVELOPER_DIR=/Library/Developer/CommandLineTools bash scripts/flclash-env.sh dart run intl_utils:generate
DEVELOPER_DIR=/Library/Developer/CommandLineTools bash scripts/flclash-env.sh dart run build_runner build --delete-conflicting-outputs
DEVELOPER_DIR=/Library/Developer/CommandLineTools bash scripts/flclash-env.sh flutter analyze --no-pub
DEVELOPER_DIR=/Library/Developer/CommandLineTools bash scripts/flclash-env.sh flutter test --no-pub
DEVELOPER_DIR=/Library/Developer/CommandLineTools bash scripts/flclash-env.sh python3 ../scripts/test-managed-android-gate.py
DEVELOPER_DIR=/Library/Developer/CommandLineTools python3 scripts/test-native.py
bash scripts/node.sh npm --prefix frontend run typecheck
bash scripts/node.sh npm --prefix frontend run lint
PLAYWRIGHT_CHROME_PATH='/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' bash scripts/node.sh npm --prefix frontend run test:api-proxy

cd FlClash/core
CGO_ENABLED=0 GOPROXY=off GOTOOLCHAIN=local go test -count=1 -timeout=30s . ./managed
DEVELOPER_DIR=/Library/Developer/CommandLineTools GOPROXY=off GOTOOLCHAIN=local go test -race -count=3 -timeout=45s ./managed
CGO_ENABLED=0 GOPROXY=off GOTOOLCHAIN=local go vet . ./managed
CGO_ENABLED=0 GOPROXY=off GOTOOLCHAIN=local go build -tags=with_gvisor -o ../../artifacts/p2-p3-20260930/core-macos-arm64 .
GOOS=android GOARCH=arm64 CGO_ENABLED=1 CC=/opt/homebrew/share/android-commandlinetools/ndk/28.2.13676358/toolchains/llvm/prebuilt/darwin-x86_64/bin/aarch64-linux-android21-clang GOPROXY=off GOTOOLCHAIN=local go vet -tags=with_gvisor . ./managed ./tun ./platform
```

测试中修复了取消 HTTP 请求后登录忙碌状态未恢复、Go 1.21 循环闭包错绑 RPC 风险、安装 UUID 等待期间取消后仍可能提交登录、旧缓存查询覆盖新操作、Android Core 拒绝后仍启动服务等问题。首轮 Flutter 全量因旧启停测试默认允许代理而失败，已在旧测试中显式覆盖授权前提，并新增不覆盖门禁的生产拒绝测试；最终 1,893 项全部通过。新 UI 覆盖四语言 360×720 布局、密码清空、返回键限制和页面销毁后的迟到结果。

网页首跑因默认 Playwright Chromium 未安装失败；使用脚本已支持的 `PLAYWRIGHT_CHROME_PATH` 指向现有 Chrome 后 11 项全通过，不修改网页业务代码或测试断言。早期失败日志保留。DNS/TCP 测试使用自己创建的回环 IPv4/IPv6 HTTP 和 UDP DNS 服务；不对公网发请求、不修改全局代理/TUN。

### 16.5 未完成验证与工具限制

Android 完整 JVM suite 的实际命令为 `./gradlew --offline --no-daemon :common:testDebugUnitTest :service:testDebugUnitTest :app:testDebugUnitTest -x :app:compileFlutterBuildDebug -x :app:copyFlutterAssetsDebug`，按现有 CI 排除不需要的 Flutter App 构建任务。命令退出 1，原因是 Flutter Gradle 脚本依赖的 `org.gradle.kotlin.kotlin-dsl:6.4.1` 不在离线缓存；没有进入该 suite 的测试执行。独立 JVM 14 项只证明真实 ManagedServiceGate 的编译/行为，不能证明 ServiceController、网络观察器、Android 整包或设备生命周期已经运行正常。

Xcode `-checkFirstLaunchStatus` 本轮仍退出 69；此前许可证/首次启动阻塞没有完成解除。没有接受协议、运行完整 App 构建或安装/启动 .app/APK，不能把独立 Core、Flutter 测试宿主和 Native Assets hooks 替代整包验收。也没有执行线上域名/TLS 检查、真实账户登录或节点代理流量测试。

另一次包含源码观察、测试报告统计及清理本轮误建空目录的组合命令，在执行前被工具返回“因 OpenAI 无法确定请求的安全状态，已拦截此工具调用”；该请求无进程退出码，没有更换工具或变形重试清理。误建的空占位文件已在更早的编辑步骤删除，根目录可能残留无业务内容的 `FlClClash/` 空目录。它不参与构建，也不应计作产品代码。

### 16.6 下一轮从哪里继续

以下为第四轮当时的开发建议。第五轮根据用户确认，先补完整平台构建与真实登录验收；未通过前不进入 P4，以第 17 节为准。

先推进 P4：在 Go 私有账户状态内拿到暂存配置，经 Mihomo 语义验证、受管归属及安全原子落盘/应用；保留 generation 和失败不回退原则，再设计受限节点选择 UI。不要直接把当前 `.canConnect => false` 改成信任服务端 session，不能恢复旧 profile 自动应用来绕过尚未实现的受管流程。

随后接 P5：真实代理累计计数、唯一 Meter、首次授权、报告/重试及最终尾账，交接 Core GET 保活所有权，并按 generation 执行真实停止。只有配置成功应用且 Meter 初次确认后才能让本机门禁转为 true。P0 完整构建运行、P6 Android/桌面网络与后台、P7 跨进程 API/App/节点/数据库闭环仍需实际证据。

本轮只修改本地源码、测试、文档及 artifacts；临时 PostgreSQL 已由脚本关闭清理，网站回归使用独立浏览器上下文。未改真实 .env、真实用户/订单/余额或线上服务，未发布、上传、提交或推送。网站部署调用链和原生生产 HTTPS 地址均保持原约定。

## 17. 第五轮平台构建与登录验收推进（2026-09-30，历史记录；最新见第 18 节）

### 17.1 范围、基线与本轮结论

按用户确认，保留第四轮 P2/P3 代码，先补 macOS 完整 App 与真实登录门禁、Android 修改模块编译/JVM，再评估 P4。本轮没有实现 P4/P5，也没有把服务器授权直接当成本机连接权限。

开始前归档 794 个源码/配置文件及原工作区差异，包含未跟踪的账户接线。`baseline.tar.gz` SHA-256 为 `27720aea464f40d01228e807fec0ea33ea285bab972d28a7b6181852f6572f20`，逐文件基线见 `baseline-sha256.json`。不包含真实 `.env` 或密钥库。FlClash/Mihomo 固定提交、锁文件和已有业务实现未改。

**结论：Android 修改模块/JVM 验收已补齐；macOS 完整 App 和真实登录未验收，当前不进入 P4。** 独立 Core 的真实 IPC 验证是新增的子层证据，不替代 Flutter App → IPC → Core → 隔离 API 的整条登录链。

### 17.2 本轮实际新增与更新

| 路径 | 变化 |
| --- | --- |
| `FlClash/android/tests/app/ManagedServiceGateTest.kt` | 新增 9 个正常 Gradle/JUnit 用例：请求契约、13 种拒绝响应、重复/迟到回调、调用异常、两秒虚拟时钟边界和取消；保留原独立 14 项脚本 |
| `scripts/test-core-cold-gate.py` | 新增真实 Core 进程检查，私有 Unix socket/临时 home，播种不含节点凭据的旧 config.yaml；验证未登录的 listener/setup/update 拒绝、reset 代次、文件不变、EOF/SIGTERM 退出和无残留监听 |
| `HANDOFF.md` | 原位更新环境、阶段清单和当前入口，保留第四轮为历史；新增 9.9 与本节，P2/P3 勾选改按整体集成验收口径 |
| `README.md`、`docs/native-client-integration.md`、`docs/native-client-protocol.md` | 同步最新平台结果和 P4 暂不准入的边界 |

除测试与文档外不改生产源码。没有修改原有测试断言来掩盖失败，也没有把初次编译中出现的弃用警告通过升级/降级依赖消掉。新门禁 JUnit 位于既有 `android/tests/app` source set，随正常 CI 执行；独立目录脚本仍保留，不能与 JUnit 数量混加。

### 17.3 Android 已完成的环境修复和验证

去掉第四轮的 `--offline` 后，正常解析固定 Kotlin DSL 6.4.1 及其他 Gradle/Maven 依赖，未替换仓库、关闭 TLS 或改版本。Gradle 使用本机已接受的 Android 许可证安装 Build Tools 36.0.0 和部分插件所需 Platform 35；原 Platform 36、NDK 28.2 保留。首次联网构建执行 293 个任务并成功。

`:service:compileDebugKotlin`、`:app:compileDebugKotlin` 和三个模块的完整 JVM suite 同轮通过。JUnit XML 精确汇总：common 30、service 23、app 67，共 **120 项**，失败/错误/跳过均为 0，app 包含新增门禁 9 项。随后 `--offline --rerun-tasks` 再执行 293 个任务并全通过；校验所有 XML 修改时间晚于本次复跑开始，排除旧报告/UP-TO-DATE 冒充新执行。

可在项目根目录复跑：

```bash
DEVELOPER_DIR=/Library/Developer/CommandLineTools bash scripts/flclash-env.sh \
  ./android/gradlew -p android --offline --no-daemon --console=plain --rerun-tasks \
  :service:compileDebugKotlin :app:compileDebugKotlin \
  :common:testDebugUnitTest :service:testDebugUnitTest :app:testDebugUnitTest \
  -x :app:compileFlutterBuildDebug -x :app:copyFlutterAssetsDebug
```

排除两项 Flutter 任务是已有 JVM CI 的执行范围，不是把 build_assets 改为 false。**该命令没有构建/安装 APK，没有验证 JNI/TUN/通知/后台或真机权限。** 编译及 120 项 JVM 通过不能用于宣称这些平台行为也通过。首次联网/离线最终日志与 XML 分别保存，不覆盖失败历史。

### 17.4 macOS 的实际进展与仍需用户处理的阻塞

本轮最初 `flutter build macos --debug --no-pub` 实际执行后退出 69，并明确提示尚未接受 Xcode 许可证；已将原始工具结果保存，不复用第四轮的空日志解释原因。随后再次探测，`xcrun --sdk macosx --show-sdk-path` 成功返回 MacOSX27.0.sdk，`xcodebuild -checkFirstLaunchStatus` 仍退出 69（输出为空）。没有代替用户接受许可证，也不据此猜测其具体操作。

SDK 探测变化后又实际重跑整包构建。这次退出 **1**，在 Swift Package Manager 解析入口加载 Xcode 插件时失败：`IDESimulatorFoundation` 依赖的 `/Library/Developer/PrivateFrameworks/CoreSimulator.framework/Versions/A/CoreSimulator` 不存在。日志明确建议 `xcodebuild -runFirstLaunch`。这不是已定位到业务 Swift/Dart 编译错误，也不能因涉及 SPM 就改回 CocoaPods/关闭 SPM。

尝试 `sudo -n /Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild -runFirstLaunch`，sudo 返回 **1 / `a password is required`**，实际组件初始化没有运行。下一步须由用户在本机终端完成交互：

```bash
sudo /Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild -runFirstLaunch
# 若仍提示许可证，先由用户阅读并决定是否同意，不使用自动接受参数。
```

完成后重新检查首次初始化、构建完整 Debug App。不得用 CommandLineTools 的独立 Go 编译或 Flutter 单元测试宿主冒充完整 Xcode/App 验收。当前没有新 App 安装启动、真实登录表单操作、Flutter/Core/API 跨层认证证据；没有创建或使用真实账户。

### 17.5 新增真实 Core IPC 证据

当前源码用 Go 构建非提权的 macOS arm64 with_gvisor Core。新脚本分别启动自己拥有的 Core 子进程，使用权限 0600 的 Unix socket 和临时 home；测试旧配置不被自动应用、未登录 RPC 启动被拒绝、reset 不放行、原文件不变，以及宿主 IPC EOF/进程 SIGTERM 两种正常清理。两种场景都通过，子进程退出码为 0，测试端口未留下监听。结束后清理自己创建的临时目录，不读用户实际 App 数据。

```bash
CGO_ENABLED=0 GOPROXY=off GOTOOLCHAIN=local go -C FlClash/core build \
  -tags=with_gvisor -o ../../artifacts/platform-validation-20260930-r5/core-macos-arm64 .
python3 scripts/test-core-cold-gate.py \
  artifacts/platform-validation-20260930-r5/core-macos-arm64 \
  --result artifacts/platform-validation-20260930-r5/core-cold-gate.json
```

此测试不发送登录 HTTP、不连节点、不启用系统代理/TUN，也不启动 Flutter App。`core-cold-gate.json` 保存二进制哈希及两种场景的实际检查项，不能把这些检查称为完整 App 登录测试。

### 17.6 P4 准入条件及交接边界

Android 修改模块编译/JVM 条件已满足。macOS 仍需：完成 Xcode 初始化 → 完整 Debug App 构建 → 隔离应用数据/测试 API → 冷启动且有旧配置/autoRun、错误密码/重复提交/取消、免费/VIP 登录及配置暂存、退出/重新打开、Core 重启和进程清理。测试地址仅允许测试构建隔离注入，不能给生产 RPC 开放任意 host；不要在真实用户数据上直接启动测试 App。

这些实际通过后再重新评估进入 P4，不要求此时就完成后续所有平台 TUN/计量，但不能省略至少一个完整 App 的本轮登录链路。P4/P5 未完成前保持本机门禁关闭，VIP 到达 configuration_staged 后不能连接仍是预期行为。

本轮未重跑第四轮 Flutter 1,893 项、Go 全量/竞态、网站/数据库回归；其原源码保持并保留历史证据。未操作线上域名、真实 .env/数据库/用户/订单、系统代理/TUN、个人浏览器或 Git 提交/推送。构建依赖与 SDK 缓存变化是本轮环境推进，不是产品功能交付。

## 18. 第六轮完整 macOS App 与 P2/P3 阶段验收（2026-09-30，历史记录）

### 18.1 结论与起点

用户自行执行 Xcode `-runFirstLaunch` 并确认 Install Succeeded；本轮重新探测首次初始化和 SDK，均退出 0，没有代用户同意协议、执行 sudo 或改全局 Xcode 设置。保留原 P2/P3，新增的是可复跑的原生集成测试，不是重做账户功能。开始前归档 702 个选定源码/配置文件和原 FlClash 差异，具体范围见本轮 `baseline-sha256.json`，不含真实环境文件/密钥或 Mihomo 子模块副本。

**已补齐上一轮要求的完整 macOS App → IPC → Core → 隔离实际 API/数据库验证，可以进入 P4。** P0 按至少一个平台的构建/运行门槛勾选，P2/P3 按阶段功能勾选。所有承诺支持的平台实际 VPN、系统入口、计量和发布仍需 P4–P8，不能由本结论推导为已具备代理服务。

### 18.2 实际修改及构建依赖

| 路径 | 本轮变化 |
| --- | --- |
| `FlClash/pubspec.yaml` / `pubspec.lock` | 新增 Flutter SDK 的 integration_test dev dependency；锁文件增加 integration_test、flutter_driver、fuchsia_remote_debug_protocol、process 5.0.5、sync_http 0.3.1、webdriver 3.1.0，既有版本不变 |
| `FlClash/integration_test/managed_account_test.dart` | 真实 macOS App 三阶段验收；调用既有 application.main、账户表单/provider、Core 生命周期、原生 IPC 和持久化；仅测试数据/网络目标/系统代理命令隔离 |
| `FlClash/test_driver/managed_account_test.dart` | 标准 FlutterDriver 集成结果收集；进程退出前先取得正式测试结果，不把 App 意外退出当作成功 |
| `FlClash/core/managed_acceptance.go` | 仅显式 managed_acceptance 构建标签包含的测试 transport；只接受 127.0.0.1 指定端口、禁止路径/凭据/query/fragment，仍使用原协调器和 HTTP 库；普通构建没有任意 host 或环境变量开关 |
| `backend/internal/api/desktop_acceptance_test.go` | 仅 desktop_acceptance 标签和私有临时目录启用的 Go 测试；真实 Gin 路由、bcrypt、会话表和随机 schema，提供随机免费/VIP/过期/耗尽账户及受随机控制密钥保护的故障注入；不编入服务二进制 |
| `scripts/test-macos-managed.py` | 自动创建私有 Unix-socket PostgreSQL/API，启动三次原生 App、检查结果及自有 PID 退出、清理临时目录和数据库；日志/截图输出项目 artifacts |
| `FlClash/lib/common/proxy.dart` | 最小 @visibleForTesting 替换入口，用于截获集成测试的系统代理子进程命令；默认仍创建原 Proxy，生产行为不变，无 UI/RPC 开关 |
| `HANDOFF.md`、`README.md`、两份原生接入文档 | 原位更新过时环境和阶段状态；新增 9.10/18、真实证据及 P4 接续范围 |

FlClash HEAD 仍为 `c7be7023d33615cb624148d41414f80a7d96cede`，Mihomo 仍为 `70f0570405c3c2c47bb113b88db95006d239b346`，没有 commit/push。账户、Core/RPC、启动门禁、Android 生产模块没有被重写。两个 native build hooks 始终启用。新测试 SDK 的 Android 插件导致首次离线缺少 AGP 8.11.0，正常联网解析后，当前依赖下联网/离线强制回归均通过；没有改项目 AGP 版本。

SwiftPM 原先声明 LaunchAtLogin main。完整历史获取慢，保留失败/停止记录后从相同官方 HTTPS 获取浅镜像；本机这一个缓存的 remote.fetch 收窄为 main，并用 HTTP/1.1，避免重新获取无关引用。提交和原 cache config 分别记录在 `spm-source.json`、`spm-cache-config-before.txt`；未替换第三方源码、关闭 TLS 或更改全局 Git。第一次被停止的 Xcode 留下自有 Git 子进程，核对 PID/路径后只结束该树，确认缓存闲置再处理。

### 18.3 实际应用验收范围

三个阶段都启动完整 macOS `.app` 和真实原生插件，不是 flutter_tester。`bundled` 阶段用默认 CoreController/启动器及包内生产 Core；`first`、`reopen` 阶段用既有可注入启动器接入相同源码、显式测试标签的独立 Core。Rust IPC、结构化 RPC、账户协调器、HTTP 客户端、Gin 认证/权益和 PostgreSQL 均实际执行，未用 mock 返回登录结果。

`bundled` 验证旧 profile 和 autoRun 存在时仍进入登录页、真实 listener 启动被拒绝，且默认 App/Core 正常退出。`first` 验证错误密码、重复点击、取消后的迟到响应、免费升级、套餐过期、额度耗尽、VIP 自动配置下载/哈希校验到 configuration_staged、返回/旧 setup 请求拒绝、服务端会话失效、真实 Core PID 更换后重新登录。结束时保持已登录状态并调用正常退出动作。`reopen` 是全新 App 进程，使用同一测试持久化数据，确认 UUID 不变但账户未恢复，必须重新登录，再验证退出登录。

FlutterDriver 三次均退出 0，三个 App PID 以及它们记录的 Core PID 均在正常 SystemAction/SystemExitCoordinator 退出后消失。测试还核对旧 config.yaml 未被覆盖、无活动代理连接、未产生 /traffic 请求或账本、公开持久化值不含密码/token/节点内容。`result-*.json` 记录 19 条命名检查，重复冷启动等检查不应夸大为 19 个完全独立业务场景。

测试文件放在 `/tmp/flclash-acceptance-*` 私有目录；SharedPreferences 使用随机 acceptance 前缀，隔离真实业务 key，仅留下不含凭据的测试命名空间记录。脚本删除临时应用文件/测试凭据并停止临时数据库。没有读写真实 .env、线上账户/订单/余额，也没有将随机测试凭据复制到发布包。截图只来自 App 自身 RepaintBoundary；登录和 VIP 原生画面已实际检查，未截取用户桌面。

### 18.4 复跑入口

从项目根目录执行，普通发布/开发构建绝不能携带 managed_acceptance 标签：

```bash
bash scripts/flclash-env.sh flutter pub get --enforce-lockfile
bash scripts/flclash-env.sh flutter build macos --debug --no-pub --target=lib/main.dart

CGO_ENABLED=0 GOPROXY=off GOTOOLCHAIN=local go -C FlClash/core build \
  -tags=with_gvisor,managed_acceptance \
  -o ../../artifacts/macos-acceptance-20260930-r6/core-acceptance .
python3 scripts/test-macos-managed.py \
  --output artifacts/macos-acceptance-20260930-r6/recheck \
  --core artifacts/macos-acceptance-20260930-r6/core-acceptance

# 集成 target 会覆盖 build 中的 App；测试后重新生成正常入口产物。
bash scripts/flclash-env.sh flutter build macos --debug --no-pub --target=lib/main.dart
bash scripts/flclash-env.sh flutter analyze --no-pub
bash scripts/flclash-env.sh flutter test test --no-pub
```

Android 复跑沿用第 17.3 节命令。全新缓存需要先去掉 --offline 正常解析，再离线复跑。最终正常 App 位于 `FlClash/build/macos/Build/Products/Debug/FlClash.app`，本轮已核对包内 Core 构建标签及测试环境字符串不存在；不能把 artifacts 里的 core-acceptance 当生产 Core。

### 18.5 警告、未覆盖边界和 P4 接续

原生测试日志出现 `open returned 1` 前台激活提示、macOS 输入法 mach-port 提示及 integration_test 原生插件未检测提示。实际 App 仍发出 focus/resumed、渲染真实画面，FlutterDriver 成功收集测试结果，并由独立 JSON/PID 检查确认；没有把这些提示当成测试通过的替代证据。`update system proxy failed` 来自故意返回空网络服务列表的隔离命令适配器，不代表实测系统代理失败或通过。第三方 Swift/Gradle 弃用警告保留，不为消警告修改依赖或降低检查。

**重要遗留：现有 macOS Proxy.stop 会清理系统网络服务的代理设置，启动/退出也可能调用。** 本轮明确截获此类系统命令，既没有改变用户主机设置，也没有验收它的真实所有权/恢复行为。P6 必须验证并按本应用所有权清理，避免影响其他软件代理；在此之前不要直接拿用户的真实配置/系统代理运行回归。测试地址是私有回环 HTTP；生产 HTTPS 证书/反向代理、开启 TUN 后 DNS/protect 网络、真实节点、后台/睡眠/快捷入口、Android APK/设备和 Windows/Linux 均未验收。

本轮只确认 P2/P3 所要求的账户、登录和未授权门禁链。P4 下一步处理暂存配置的 Mihomo 语义验证、明确归属、原子保存/应用及受限节点导航；P5 再接唯一 Meter、累计计数、首次确认和尾账。当前 .canConnect 继续为 false，不能因为阶段验收通过就改成服务端 session.can_connect。未实现 P4/P5、未启用实际代理、未签名公证或部署发布。

## 19. 第七轮 P4 受管配置与入口收口（2026-09-30，当前记录）

### 19.1 本轮完成范围与保存基线

按用户要求继续 P4，没有重做已部署的网站或部署线上后端。完整阅读交接及 FlClash 规则后，先保存 674 个选定源码/文档文件、哈希清单和原有 FlClash diff 到 `artifacts/p4-20260930-r7/`。`baseline.tar.gz` SHA-256 为 `d2f1026485538259ae19a0c87a877f1b2a64def8349a3245b1aa86c1188254f9`。这是有明确清单的源码备份，不是整个工作区镜像；不含真实 `.env`、密钥、构建缓存、Mihomo 副本，初始文件后缀过滤没有纳入 ARB，原 Git 差异另存。前六轮的脏工作区和未跟踪代码保留，无 reset/clean/commit/push。

**P4 已完成并通过自动化与真实 macOS App 验收。** VIP 配置不再只停留在私有内存：通过策略及真实 Mihomo 语义验证后安全保存并应用，服务端声明的节点可选，失败和会话变更清理。P5 未接入，顶层 `can_connect=false`、监听/TUN/外控关闭，`managedFlush=meter_not_ready`；不产生占位报告，不声称已经能使用 VPN。

### 19.2 配置加载、归属和原子保存

主路径是 `core/managed/coordinator.go` → `managed_configuration.go` 的 Prepare/Apply → Mihomo `config.ParseRawConfig` / `hub.ApplyConfig`，不是旧 `setupConfig`、脚本拼装或 YAML 结构解码。步骤为停止并清理旧受管状态 → 请求 `/config` → 检查 SHA-256/会话绑定版本/结构策略 → 完整语义验证 → 再核对 generation/context → 私有原子保存 → 应用内核并保持 suspend。缺失组成员、规则目标不存在、循环组、非法节点、多文档/重复字段/别名等不能应用，错误只返回脱敏代码。

新 `core/managed/configuration.go` 定义配置 owner（用户 ID、session ID、generation）、随机 128 位配置实例 ID、版本和公开组列表。生产构造器必须注入配置引擎；Apply 与提交当前快照受协调器锁和 configMu 约束，迟到下载/验证不能跨退出或换用户生效。配置加载完成后移除协调器中的重复 YAML 字符串，Dart 只接收必要元数据，不接收 YAML、token、节点密码或任意磁盘路径。

`core/managed/profile_store.go` 将原始服务端 YAML、版本、配置实例 ID、owner 放入同一个 JSON bundle：`<Core home>/asterlink-managed-v1/active.json`。私有目录 0700、标记/文件 0600；使用 os.Root 限定目录，随机 `.pending-<32hex>.json` 以 O_EXCL 创建，Write/Sync/Close 后 Rename 原子替换，失败清理临时文件。该文件不是旧 `config.yaml`，不进入 Drift profiles，因此无需数据库迁移。保证原子可见与文件同步，不把它描述为已验证断电后目录持久性；未实现目录 fsync 或密码学擦除。

只接受本功能的精确所有权标记，拒绝未标记目录、符号链接命名空间及异常对象。清理只删除该目录中的 active 和严格命名的自有 pending 文件，不递归删除未知内容、不接收任意路径；测试验证外部符号链接目标和旧用户文件不变。退出、重载失败、会话/权益失效、Core 重启或冷启动清除自有数据，绝不离线重读残留来恢复身份。清理失败会显式报错并阻止新登录，而不是忽略后继续放行。

有效配置授权最多 90 秒，按请求起点保守换算；读取缓存不续期。GET 确认失败、配置变化/撤回、余额/期限拒绝会清理当前配置，内核代理/规则、相关 resolver/provider 和外控状态一起收敛。已有 60 秒 Core 保活在 P5 前继续工作；Flutter 2 秒缓存观察只在已登录时启用，避免导航读取触发无账户轮询。此时没有真实代理流量，旧尾账处理仍是 P5 的前置任务。

### 19.3 配置兼容范围：禁止未计量的二次来源

P4 不承诺接受任意 Mihomo YAML。管理员模板需是 **内联 proxies + 显式 select proxy-groups + 内联 rules** 的自包含配置；上限为 YAML 1 MiB、512 个节点、128 个组、每组 512 个成员、10,000 条规则，并限制 AST 深度/节点数和名称。接受的代理类型为 ss/ssr/socks5/http/vmess/vless/trojan/hysteria/hysteria2/tuic/snell，仍需通过 Mihomo 自身字段校验。

支持受限 DNS、mode、ipv6、tcp-concurrent、unified-delay；服务端的端口、LAN、TUN、controller/UI、secret、profile 缓存及 geo 自动更新等运行控制字段被客户端策略丢弃。DNS 监听为空，store-selected/store-fake-ip 关闭，不读取旧选中节点缓存。其他未知顶层字段、remote proxy/rule providers、脚本、独立 listeners/tunnels、外部 TLS 文件、WireGuard/SSH 等未纳入类型、url-test/fallback/load-balance 自动组、GEOIP/GEOSITE/IP-ASN/RULE-SET 等外部数据来源均拒绝并显示 `unsupported_managed_configuration`；不会静默回退到本地订阅。

这是在 Meter 尚未接入时避免健康检查/下载/持久化密钥等另起网络路径的保守策略，不是判定这些 Mihomo 功能本身不安全。P5 后扩大范围必须逐项验证后台流量与计量所有权，不能简单放开所有字段。没有读取线上真实 YAML；部署管理员应先用隔离环境检查模板兼容性及真实节点可达性。客户端文件权限和不提供导出只减少误泄漏，不保证本机管理员或修改版客户端无法提取凭据。

Go 的 `core/go.mod` 最低语言版本从 1.21 调整为 **1.25**，使用 os.Root 的安全文件/原子替换 API；实际构建仍是已安装 Go 1.26.6，没有升级依赖或改变 SDK/Mihomo pin。Android arm64 NDK 构建通过，不据此推导其他平台已验收。

### 19.4 UI、节点选择和绕过路径

新增严格只读 `lib/models/managed_configuration.dart`，拒绝额外秘密字段、无效 ID/版本、归属不匹配、重复组或非法选择，并使用不可变列表。`managedSelectProxy` 必须携带当前 generation、configuration_id、group、proxy；Go 再检查精确 owner 和服务端成员，只允许当前显式 select 组。UI 不把本地数据库节点、任意 GLOBAL/DIRECT 或旧配置 ID 当作候选；实际内核选择改变不等于启动连接。

桌面侧栏/移动导航仅保留账户、服务端节点、偏好；偏好仅语言和主题，不提供旧 Basic/Advanced、配置编辑、资源、脚本或备份入口。失效的持久化 page label 回到账户，PageController 不再使用 -1，并处理标签集合变化及挂载时机。退出/冷启动清空旧 active profile 引用和 groups，但保留用户旧数据库行和文件。

ProfilesAction 的文件/URL/二维码导入、写入/更新/删除/应用、旧订阅自动更新、配置校验/效果清理被拒绝或停止调度；SetupAction 拒绝旧 profile 构造/预览/应用且不恢复缺失 profile；BackupAction 在压缩、发送、解压或数据库写入前拒绝。CoreManager 不再监听旧 profile ID/本地 patch 来自动应用，也不处理外部 provider 自动加载。AppManager 的旧 group 刷新链断开。深链接沿既有门禁拒绝，底层入口另外收口，不依赖隐藏按钮。

托盘移除本地模式和旧节点菜单，受管节点入口只导航到受限页面；模式热键不注册/不执行。旧 setupConfig/updateConfig/getConfig/validateConfig/changeProxy/getProxies、providers/侧载/geo/effect RPC 永久拒绝。JNI quickSetup 同样检查永久 setup 禁用策略，即使 P5 将来授权 listener，也不能借旧 config.yaml 重开。外部控制器、Unix/pipe/UI/DNS 监听均不启用。旧视图/基础模块源码保留用于上游维护及底层测试，不代表它们仍能在产品中导入配置。

### 19.5 实际测试、发现的问题与证据

最终测试和路径见第 9.11 节、`verified-counts.json`、`native-confirmed.json`。新增测试覆盖真实 Mihomo 语义与选择、私有权限和原子替换、命名空间/符号链接/写盘失败、不污染旧数据、取消迟到请求、跨账户 ID、授权缓存不续期、失败清理、导入/导出拒绝、四语言窄屏与安全导航。原允许导入/备份的上层测试改为验证拒绝且无副作用，底层编码/数据库等测试未被批量移除。

真实 macOS 验收使用实际 Application、Flutter/Rust IPC、Go 协调器/Mihomo、Gin 和私有 PostgreSQL，不伪造登录结果。扩展 opt-in `desktop_acceptance_test.go` 的第二个 VIP、语义错误/缺失/替换文件及延迟配置故障；这些控制端点只在显式测试标签和私有目录生效，不编入正式服务。三阶段验证旧 profile/autoRun 不生效、服务端配置落盘和实际节点选择、恶意运行端口不监听、失败/取消/换账户清理、返回导航、服务端失效、真实 Core PID 重启、App 全新进程不恢复登录。

第一次完整 App 验收在 `app-run/` 暴露 HomeBackScope 将根页面返回误当成 `handleClose()`，导致进程提前退出；已修为回到账户页，补真实 widget 回归，再以 `app-run-2/` 完整三阶段通过。前期测试的 eager poll 定时器和旧配置正向预期也已修正；失败日志保留，不把首次失败隐藏为成功。测试脚本现在在失败清理时也保存安全的截图/进度 JSON，不复制测试凭据。

`app-run-2` 的 bundled/first/reopen 均 driver_exit=0，App/Core 退出全部确认；结果共 24 条命名检查，最终 active_sessions=0、reports=0、没有 `/traffic` 请求。所需端口可重新绑定，旧配置未改，持久化不含凭据或节点 YAML。已查看 `managed-nodes.png` 与 `managed-preferences.png`，页面正常且只显示受限内容。测试继续隔离所有系统代理子进程命令，不触碰真实系统代理/TUN。

集成 target 会覆盖 build 中的 App，故结束后已重新构建 `lib/main.dart`。最终路径为 `FlClash/build/macos/Build/Products/Debug/FlClash.app`；包内 Core SHA-256 `391e81674dd4c5f339c47cbd2d3c29ff5ae07e96c5185966447a5d77eef48719`，构建信息无 managed_acceptance，二进制无 ASTERLINK_ACCEPTANCE_BASE。`production-build-check.json` 另记录 HEAD/Mihomo 原 pin 与子模块干净状态。未签名公证、安装发布或部署。

### 19.6 复跑命令与下一步

从项目根目录执行（现有 SDK/依赖缓存和已初始化 Xcode）：

```bash
bash scripts/flclash-env.sh dart run intl_utils:generate
bash scripts/flclash-env.sh dart run build_runner build --delete-conflicting-outputs
bash scripts/flclash-env.sh flutter analyze --no-pub
bash scripts/flclash-env.sh flutter test test --no-pub --concurrency=4
CGO_ENABLED=0 GOPROXY=off GOTOOLCHAIN=local go -C FlClash/core test -count=1 . ./managed
GOPROXY=off GOTOOLCHAIN=local go -C FlClash/core test -race -count=3 ./managed
CGO_ENABLED=0 GOPROXY=off GOTOOLCHAIN=local go -C FlClash/core vet . ./managed
python3 scripts/test-native.py

CGO_ENABLED=0 GOPROXY=off GOTOOLCHAIN=local go -C FlClash/core build \
  -tags=with_gvisor,managed_acceptance \
  -o ../../artifacts/p4-20260930-r7/core-acceptance .
python3 scripts/test-macos-managed.py \
  --output artifacts/p4-20260930-r7/recheck \
  --core artifacts/p4-20260930-r7/core-acceptance
bash scripts/flclash-env.sh flutter build macos --debug --no-pub --target=lib/main.dart
```

第七轮当时交给 P5 的真实累计、唯一 Meter、分钟报告、断连与尾账任务，已在第八轮接通，当前入口及证据改看第 20 节。仍禁止伪造报告、直接透传 session.can_connect 或恢复旧 setup/preload/quickSetup；受管引擎仍是配置所有者，不是第二个进程/Android 服务生命周期所有者。

以上为第七轮历史范围：当时没有真实计量或系统代理归属实现，P5–P8 未勾选。第八轮已完成 P5、实现 P6 并重跑 Android JVM，当前状态以第 12、20 节为准；线上 HTTPS/实际 YAML、设备/OS 全矩阵及正式收费仍未验收。Next.js 源码未改，网站浏览器回归仍为前轮历史，后端原网页业务在私有 PostgreSQL 下重新执行。

## 20. 第八轮 P5–P6 执行记录（2026-09-30）

### 20.1 本轮完成范围与基线

完整阅读原 HANDOFF 及 FlClash 的 AGENTS/.agents 规则后，在用户已有未提交修改之上实现 P5 及 P6 平台代码，没有改写登录体系、创建第二个 Core/服务所有者或恢复旧 Profiles 导入路径。先制作源码基线，再修改代码；所有测试在本机/隔离环境执行，没有访问线上 API、修改生产数据库、提交、推送或部署。

P5 本轮勾选完成；P6 代码与本机自动化完成，但设备/实际 OS 矩阵未齐，阶段总验收仍不勾选。第 0、1.2、2、P4/P5/P6、9.4、12 和前轮下一步说明已原位更新，历史测试数字没有冒充本轮。`docs/native-client-protocol.md` 同步更新九个 RPC、运行代次、公开计量字段和尾账错误语义。

证据根目录：`/Users/stevenlee/Desktop/vpn/artifacts/p5-p6-20260930-r8/`。修改前 `baseline.tar.gz` 保存 653 个选定源码/文档文件，SHA-256 为 `2c78599ad79aa99b2457c5c3aa3b48d957869707cc6c43bdae01a4b9d49d6f27`；额外代理/Android 公共源码基线 `baseline-extra.tar.gz` 保存 27 个文件，SHA-256 为 `8ff50468d745a71ae28b54d0d3c9e60d0ef228b3c80408148bce1c57a6dd178b`。另保留各自 SHA 清单和原 `baseline-flclash.diff`。这是选定源码基线，不是整个工作区/个人资料备份；不含 `.env` 或真实账户凭据。

FlClash HEAD 仍为 `c7be7023d33615cb624148d41414f80a7d96cede`，Mihomo 仍为 `70f0570405c3c2c47bb113b88db95006d239b346` 且没有修改子模块源码；Flutter/Go/Rust/SDK/AGP pin 和正常 build hooks 保持不变。根目录不是 Git 仓库，原生 hygiene 工具对此只给非阻断提示；实际 Git 差异检查在 `FlClash/` 内完成。

### 20.2 P5 真实累计、授权与结算

**实际计数源。** 新增 `core/managed_runtime.go`，生产采样从同一 Go Core 的 Mihomo `statistic.DefaultManager.TotalTraffic(true)` 获取单调累计，固定版本按最终代理链排除 DIRECT；账户 HTTP 使用独立直连 transport。`handleResetTraffic` 只保存显示基线，不重置底层统计。UI 图表开关/清零、普通断连重连、手动配置重载不再破坏计费累计。`SampleTotals(context.Context)` 区分采样失败与合法 0，计数回退、负值或溢出立即停止旧会话。

**唯一报告者与显式连接。** `managed/meter.go`、`managed/coordinator.go`、新增 `managed/runtime.go` 接通 `managedConnect` / `managedDisconnect`。首次 Connect 先停止旧通道、排空数据、读取会话和累计基线、发送真实首报；只有确认后且用户连接意图仍有效才启动 loopback mixed listener。启动前可以真实读到 0 字节，这不同于伪造占位心跳；没有 Meter 的 Flush 仍返回 `meter_not_ready`。Meter 启动后协调器不再重复发送定时 GET；Meter 每秒采样、每 60 秒上报，UI 缓存查询不触发网络保活。

**运行代次和旧实例隔离。** 公开快照增加 `runtime_revision`；Connect 携带当前 generation/revision/port，任何更新的 Stop 都可取消和压过迟到 Connect。每个会话的 `runtimeLease` 退休后不再执行影响新会话的停止回调；新运行等待旧释放屏障。旧 `startListener` 只能确认当前已获授权实例，旧 setupConfig/quickSetup/导入/健康探测绕过仍拒绝。配置所有者没有变成新的进程或 Android 服务所有者。

**真的停流而非仅改 UI。** 每个数据通道实例包装 TCP/UDP：停止先封闭新入口并关闭存量 TCP，拒绝旧实例的 UDP 回写，再等待旧处理和累计稳定。桌面 mixed 和 Android TUN 使用同一个门禁对象。Drain 在 context 内检查旧实例和连续稳定采样，超时拒绝继续，不能把未排空数据当作结清。真实测试通过持续下载验证上报失败、额度耗尽后连接确实关闭；HTTP 重试成功不会自行复活 listener。

**不确定提交和配置重载。** pending 报告的序号与累计数被冻结；响应丢失后原样重试，不增加新序号重复扣量。首报失败也保留 Meter/pending，而不是销毁后以 0 重新开始。手动重载在同一个报告锁里结算旧批次，再 GET 配置、验证和实际应用，保持同一会话序列；新配置应用后仍停止，用户必须再次明确连接。切换账户前先结清旧账，不确定则保留旧会话并停止，不悄悄丢弃。

**有截止时间的尾账。** 报告/采样互斥等待支持 context 取消；关闭取消正在进行的 HTTP，不再无界等待旧 WaitGroup。普通 Disconnect 与会话释放各使用 2 秒预算；Flutter 正常退出沿原 3 秒 watchdog 清理。普通断连未结清为 `traffic_unconfirmed`，可在当前内存会话重试；已关闭会话而尾账未确认是 `final_traffic_unconfirmed`；尾账已确认但服务端撤销未确认是 `logout_unconfirmed`。这些状态分开显示，不能把本地清理当作后端已结账，也不能承诺在硬杀/超时后补传。

### 20.3 桌面与账户界面

`lib/models/managed_account.dart` 新增严格计量视图；解析校验累计/确认基线、上限、运行代次和完整授权结构，不直接透传嵌套 session 的 can_connect。账户页新增“连接/断开连接”，显示实际运行/等待平台状态、服务端余额、本地扣除未确认流量后的预估、累计上下行和 pending；四种现有语言同步生成。

`SetupAction` 对受管账户不再走旧 profile/setupConfig。连接必须等待 Core 首报确认，停止立即发给 Core 而不是排在未完成连接后面；失败不回滚成假“运行中”。`AppStateManager` 移除了睡眠恢复直接 `startListener`，桌面物理网络类型变化也停止；恢复只能重新确认。Core 重启/App 重开不复用 token，实际 App 测试验证重新登录。

`plugins/proxy/lib/src/owned_settings.dart` 串行保存原值并按当前值与本应用写入值比较后恢复。macOS 逐服务处理 HTTP/HTTPS/SOCKS 与 bypass，拒绝不能安全恢复的认证代理和启用 PAC；Linux gsettings/KDE 读回验证、恢复原值或删除本次新增键。停止会使迟到的服务发现/启动失效。Windows WinInet 适配器保存 LAN/RAS 原值、写后读回，恢复时不覆盖外部软件改写的 endpoint/flags/bypass。代理设置失败会停止 Core，而不是展示已连接。

这不是系统级原子锁或崩溃恢复日志：外部软件并发写入仍存在操作系统级竞态，进程被强杀时不能保证恢复；macOS 原先关闭且空 endpoint 的设置只保证恢复为关闭，不承诺无效占位字段逐字符相同。相关所有权单元测试已通过，但本轮没有改动用户实际系统代理/DNS 设置。桌面受管 TUN 未开放，不算已验收能力。

### 20.4 Android 无 UI 生命周期

`ManagedServiceGate.prepare` 读取当前 Core 快照，再以 generation/runtime_revision 和 VPN mixed port 请求确认；只从 SharedState 恢复 VPN 选项，不恢复旧 YAML/token，也不走 quickSetup。`ServiceStateMachine` 继续是唯一串行意图所有者，Quick Settings/通知启动停止直接走原生路径，不依赖 Flutter tile 回调。

`ServiceController` 保留原生授权看护与 Core 事件分发，即使 Flutter 界面解绑仍每秒检查缓存授权。唤醒调度间隙超过 5 秒时保守停止；非 VPN 底层 DNS 列表变化也先停止，再要求新意图确认。授权拒绝、权限拒绝、服务丢失和停止会先停 Core，再收回 TUN/绑定服务/通知、清空原生 runtime 并尝试尾账。失效回调携带旧 RunRequest 时不能误停更新的请求。

Core `startTun` 在拒绝接管时关闭属于这次调用的原始 FD；成功接管后的关闭责任仍由现有 TUN owner 管理，避免重复 close。TUN 创建只能使用当前受管 data plane。Dart Android Stop 顺序变为 Core listener 先停、原生服务后清理；界面运行时间来自原生 runtime，不单凭授权快照显示 TUN 已建立。

离线 Kotlin/JVM 126 项及 arm64 CGO 构建通过，包括没有 Flutter 的准备/拒绝/停止、旧回调、停止压过准备、冷启动不能由 SharedState 恢复授权等。**这些不是 Android 真机证据**：没有已连接 adb 设备或模拟器系统镜像，未安装 APK，未执行实际 VpnService/revoke/Doze/Always-on/后台流量测试。

### 20.5 本轮发现并修复的问题

真实传输最初揭示“端口开放但回环客户端被 IP 白名单拒绝”，已在受管引擎启动时显式限定并允许 loopback，后续实际传输通过。计费方面修复图表清零污染底层累计、首报 ACK 丢失销毁账本、配置 GET 与报告覆盖基线、旧会话回调关闭新实例，以及退出等待无界。平台方面修复恢复入口直接启动、Android 拒绝 TUN 时 FD 泄漏、通知停止依赖 Flutter、系统代理无归属清理。

回归过程中也修正了测试夹具：完全 fake 的 SetupAction 不应在 build 阶段启动生产 Core；Linux 代理 fake 需要真实模拟读回；新快照测试需显式构造顶层和嵌套权限；旧 Android Stop 断言改为先停 Core 再停原生服务。最后全量 1,957 项重新执行成功，静态分析零问题；没有删除/skip 这些测试来获得通过。失败日志保留，最终日志见第 9.12 节。

### 20.6 仍未验证与下一轮入口

1. **Android 设备矩阵：** 实际 APK/JNI 加载、VPN 权限拒绝/撤销、Quick Settings/通知、Always-on、无 Activity 后台、Doze/网络切换、FD/通知清理、真实 UDP/TUN 尾账。先准备真机或明确授权的模拟器，不能将 JVM 通过替代安装运行。
2. **桌面实际 OS：** macOS 真实系统代理/DNS 恢复及休眠、Windows WinInet 编译/运行与系统设置、Linux GNOME/KDE 设置与退出。Windows 本轮仅可移植策略测试；真实系统设置隔离未解除。桌面受管 TUN 仍未支持。
3. **异常与计费上界：** 秒级采样不是逐字节内核硬限速；90 秒授权窗口和后台调度需设备验证。离线退出、强杀、Core 突然消失不保证尾账送达，也不保证 OS 代理恢复。pending/token 不落盘，重开不保证自动补账。
4. **P7/P8 与可信收费：** 本轮未访问生产两个域名、真实节点/服务端 YAML、线上 TLS、历史数据库升级、签名发布或安全穿透矩阵。client_reported 仍不是可信节点计量，正式收费继续遵守第 11 节。Next.js 源码未改，也没有新增浏览器生产回归。

以上不阻止记录 P5 的本地实测完成，但阻止将 P6/P7/P8 标为全平台/发布验收完成。下一轮优先补设备与实际 OS，而不是重写已验证 Meter。

### 20.7 复跑入口与交付产物

从项目根目录执行。所有后端脚本自行建立私有临时 PostgreSQL，不提供真实 DSN；`GOPROXY=off` 使用已有缓存，不能为下载关闭 TLS/GOSUMDB。分钟测试需要真实等待，不应通过缩短 production interval 冒充。

```bash
cd /Users/stevenlee/Desktop/vpn
bash scripts/flclash-env.sh dart run intl_utils:generate
bash scripts/flclash-env.sh dart run build_runner build --delete-conflicting-outputs
bash scripts/flclash-env.sh flutter analyze --no-pub
bash scripts/flclash-env.sh flutter test test plugins/proxy/test --no-pub --concurrency=4
FLCLASH_RUN_MINUTE_TEST=1 CGO_ENABLED=0 GOPROXY=off GOTOOLCHAIN=local \
  go -C FlClash/core test -count=1 -v . ./managed
GOPROXY=off GOTOOLCHAIN=local go -C FlClash/core test -race -count=3 ./managed
CGO_ENABLED=0 GOPROXY=off GOTOOLCHAIN=local go -C FlClash/core vet . ./managed
python3 scripts/test-native.py

bash scripts/flclash-env.sh ./android/gradlew -p android --offline --no-daemon \
  --console=plain --rerun-tasks :service:compileDebugKotlin :app:compileDebugKotlin \
  :common:testDebugUnitTest :service:testDebugUnitTest :app:testDebugUnitTest \
  -x :app:compileFlutterBuildDebug -x :app:copyFlutterAssetsDebug

GOOS=android GOARCH=arm64 CGO_ENABLED=1 GOPROXY=off GOTOOLCHAIN=local \
  CC=/opt/homebrew/share/android-commandlinetools/ndk/28.2.13676358/toolchains/llvm/prebuilt/darwin-x86_64/bin/aarch64-linux-android21-clang \
  go -C FlClash/core vet -tags=with_gvisor . ./managed ./tun ./platform
GOOS=android GOARCH=arm64 CGO_ENABLED=1 GOPROXY=off GOTOOLCHAIN=local \
  CC=/opt/homebrew/share/android-commandlinetools/ndk/28.2.13676358/toolchains/llvm/prebuilt/darwin-x86_64/bin/aarch64-linux-android21-clang \
  go -C FlClash/core build -buildmode=c-shared -tags=with_gvisor \
  -o ../../artifacts/p5-p6-20260930-r8/libclash-android-arm64.so .

CGO_ENABLED=0 GOPROXY=off GOTOOLCHAIN=local go -C FlClash/core build \
  -tags=with_gvisor,managed_acceptance \
  -o ../../artifacts/p5-p6-20260930-r8/core-acceptance .
python3 scripts/test-macos-managed.py \
  --output artifacts/p5-p6-20260930-r8/recheck \
  --core artifacts/p5-p6-20260930-r8/core-acceptance
# 测试后必须回到正常入口；不要分发测试入口 App/Core。
bash scripts/flclash-env.sh flutter build macos --debug --no-pub --target=lib/main.dart
```

正常 App 为 `/Users/stevenlee/Desktop/vpn/FlClash/build/macos/Build/Products/Debug/FlClash.app`。本轮正常包中 `Contents/MacOS/FlClashCore` 的 SHA-256 为 `57c13a6d6fbf36319931d0b92265d9c11e8ff7d34b09955e96a7d4feb7252569`；`go version -m` 验证仅 `-tags=with_gvisor`、`CGO_ENABLED=0`，二进制不含 `ASTERLINK_ACCEPTANCE_BASE` 测试入口。不同重建的哈希可能变化，应重新核对 build info，不要求复现字节级签名。

汇总入口为 `verified-counts.json`、`core-validation-final.json`、`flutter-final-verification.json`、`native-validation.json`、`production-build-check.json`、`macos-e2e/summary.json`。后端测试夹具仅改 `backend/internal/api/desktop_acceptance_test.go`，实际 App 验收在 `FlClash/integration_test/managed_account_test.dart` 和 `scripts/test-macos-managed.py`。截图已采集留作证据，本轮未额外宣称逐张视觉验收。所有源码保持本地未提交状态，未进行 commit/push/deploy。
