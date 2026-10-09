# AsterLink 原生 macOS 客户端开发交接计划

> 文档：`handoff_app.md` · 编制日期：2026-10-08 · 状态：待开发，非已实现声明。
> 目标：保留现有网站和 Go API，弃用 FlClash 应用层，交付 Apple Silicon 上以 Mihomo TUN 工作的原生 macOS 客户端。
> 排期：最多 5 个交付阶段；每阶段形成一个可验收增量，不把“5 阶段”理解成保证只需 5 次编码或调试。
> 原则：先打通真实 TUN 和现有业务，再补齐收费必需的节点侧控制；不以隐藏 YAML、模拟流量、系统代理或 Debug 包冒充商业成品。

## 0. 接手者先读：范围、现状与决策

### 0.1 本次勘察基线

本地项目为 `/Users/stevenlee/Desktop/vpn`，关联仓库为 `https://github.com/StevenWinsir/vpn`。勘察时本地分支 `main`，HEAD 为 `ad9795f77ad14f1c1e616a17a1a0705fdf622062`。以下结论同时考虑了未提交工作区，不能只凭这个 commit 重现所有状态；本次没有 fetch、提交、推送或部署。

工作区已经存在大量删除及其他未提交改动，`FlClash/`、旧 `HANDOFF.md` 当前不在磁盘上。不要恢复整个旧客户端、覆盖这些改动，或把删除误认为本次计划书造成。旧核心的部分 Go 逻辑仍可在 Git 历史中查阅，迁移时只提取经过审查、许可允许的独立代码与测试。

本机只读观测为 macOS 26.6.2、arm64、Xcode 27.0 / Swift 6.4。最低部署版本建议先定 macOS 15.0、仅 arm64；低版本兼容仍须真机或合适测试机验收，不能由本机新系统运行成功推定。当前 CI 写的是其他 Xcode/Go 版本，第一阶段必须统一可实际获得的构建基线，不凭版本数字推断 GitHub Runner 已安装相应 SDK。

本次仅阅读源码、现有文档和本机 SDK 元数据，编写本计划。没有登录测试账号、连接数据库、启动代理、改变路由/DNS、安装特权服务或执行业务回归。当前无法在线检索外部文档，Apple 分发规则、上游发布与安全公告未进行实时核验；第 1/5 阶段必须对固定版本与实际签名环境复核。附录给出依据与核验入口。

### 0.2 一句话架构决策

**SwiftUI 原生界面 + Swift 特权 Helper（SMAppService / XPC）+ 独立 Go Managed Core（嵌入固定版本 Mihomo）**。连接期间最多 3 个本产品进程，不包括 macOS 自身服务；不使用 Flutter、Dart、Rust UI 桥、Electron、Tauri、WebView 套壳或本地 HTTP 管理服务器。

首版采用 Developer ID 签名、公证的站外 DMG 分发，暂不走 Mac App Store。Mihomo 直接管理 macOS 的 utun/TUN 数据通路；不把命令行内核塞进 `NEPacketTunnelProvider`，也不同时维护两套隧道实现。

**为什么这样选：** SwiftUI 适合原生界面，Swift Helper 适合 XPC、签名身份和系统服务授权，Go Worker 适合直接复用现有 Go 协议/计量经验及 Mihomo。三个进程换取清晰的权限与故障隔离，避免首版同时处理 Go→Swift 静态链接、运行时 ABI 和 Network Extension 数据包桥接。并非宣称这是所有 VPN 的唯一架构。

### 0.3 两条验收线，不能混为一谈

| 验收线 | 必须达到 | 可以怎样发布 |
| --- | --- | --- |
| 原生开发闭环（第 3 阶段） | 真实登录、节点同步、SS/VLESS TUN、真实累计流量、整数倍率扣量、官方客户端到期停连 | 受控内部测试，明确 `client_reported`，不解除现有生产保护 |
| 收费客户端与代理闭环（第 5 阶段） | 上述能力 + 节点侧每用户授权、独立计量、撤销/到期/配额执行、签名公证与完整故障测试 | 仅在网站支付、安全、运维等外部商业门槛也通过后收费 |

客户自己的机器不是可信计费设备。攻击者能修改客户端、读取内存、绕过 UI 或缓存代理凭据；给客户端上报加 HMAC、把 YAML 加密后在 App 解密，均不能变成独立可信计量。管理员录入的共享 SS 密码或共享 VLESS UUID，也不会因网站账号到期而自行失效。

**首要外部依赖：必须拥有节点服务器的管理能力，或供应商提供可验证的每用户凭据、用量和撤销接口。** 目前源码不能证明已有该能力。没有这个条件，仍可完成原生开发版，但“抗绕过收费闭环”应记录为阻塞，不得宣布整个商业服务已完成。

## 1. 业务范围与最小产品

### 1.1 本次必做

原生 App 提供邮箱密码登录、可选记住密码、套餐及确认余额、可选节点列表、线路类型与倍率、连接/断开、实时上下行速率、累计流量、简单状态/错误展示。注册、购买、续费、找回密码继续跳转网站，不在 App 重做电商系统。

每次启动必须在线认证并同步；记住密码只免重复输入，不提供离线授权。只显示当前套餐有权使用的节点。管理员身份不等于有免费代理权益；`test@test.com` 可以作为现有管理员测试标识，但仍需有效套餐才能做客户连接测试，不能绕过鉴权，也不假设其密码已知。

节点 UI 不展示 server、port、password、UUID、私钥、完整 YAML、订阅导出地址。只有 Managed Core 取得当前选中节点的连接资料；普通列表保持纯元数据。不得承诺设备所有者绝对无法提取配置。

支持专线与普通线路，两者都经远端代理。数据库 `line_type=direct` 表示“普通线路”，**不是 Clash 的 `DIRECT` 绕过**。倍率来自管理员设置：专线 1×、普通 0.5×只是初始业务示例，不得按类型硬编码。名称为“专线”也不能当作已验证的低延迟或带宽保证。

套餐过期、账号禁用、额度耗尽、权限撤回、会话失效时不允许继续使用付费代理。开发阶段由官方客户端执行；收费阶段还必须由真实节点拒绝新连接并终止已建立的 TCP/UDP 转发。

### 1.2 明确不做

不做其他操作系统、Intel universal 包、自动选优、多节点并发、任意规则编辑、导入订阅、用户自定义代理、复杂图表、自动更新框架、复杂设备指纹、开机自动连接、P2P/多跳、离线代理、App 内支付。保留简单菜单栏入口即可，关闭窗口不等于退出 App；菜单“退出”必须停连。

首版不承诺系统级 Kill Switch。正常断开或不可恢复故障后恢复普通网络；这与“过期后不能用代理”不同。隧道运行中禁止自动回退 `DIRECT`，但不能把它宣传为“内核崩溃瞬间全机绝不直连”。未来如销售此隐私保证，需另行设计并验收系统级阻断，不在本计划里顺手修改全局 PF 规则。

### 1.3 页面只保留三块

| 页面 | 内容 | 不得出现 |
| --- | --- | --- |
| 登录 | 邮箱、密码、记住密码、登录、网站入口 | 明文日志、任意 API host 输入、管理员权限开关 |
| 主窗口 | 套餐到期/余额、节点名称/地区/类型/倍率、连接按钮、当前状态、速率及累计 | YAML 编辑、导出、假测速、“初始化完成即已连接” |
| 简单设置/关于 | Helper 状态、版本与 core revision、网站、脱敏诊断、退出登录 | 绕过 TLS、任意 core 路径、执行 shell、用户自定义 runtime YAML |

## 2. 实际仓库能力与差距

### 2.1 可以复用的已有资产

| 资产 | 已观察内容 | 新客户端处理方式 |
| --- | --- | --- |
| `frontend/package.json` | Next.js 16、Mantine 8、React 19 的依赖声明，Node >=22 | 保持现有网站；范围声明不等于已核验最新版本 |
| `backend/go.mod` | Go directive 1.24.0，Gin/GORM/PostgreSQL，独立 `backend/nodepolicy` | 保持后端技术栈，构建工具链单独固定 |
| `backend/internal/api/native.go` | 独立不透明 Bearer 会话、套餐/设备检查、配置、累计计量、退出 | 第一版严格适配，不误用网页 Cookie/JWT |
| `backend/internal/api/node_watch.go` | `/client/nodes` 元数据与 revision 长轮询，最长约 5 秒，约 1 秒数据库复查 | 直接复用，不增加 WebSocket 服务 |
| `backend/internal/api/node_catalog.go` | 单个选中节点秘密、符合套餐的节点元数据、固定代理分组与 MATCH 规则 | 配置只交给 Core，UI 只拿结构化快照 |
| `backend/internal/billing/client.go` | 单调累计计数、序号、整数倍率与溢出检查 | 保留开发账本及回归，不以浮点重写 |
| `backend/internal/model/native.go` | NativeSession / ClientTrafficReport | 账本明确仍为 `client_reported` |
| `backend/nodepolicy/policy.go` | 受限多协议字段白名单，固定 core revision | 新 Core 与 API 使用同一策略源 |
| `backend/internal/api/testdata/native_client_contract.json` | 现存 8 个契约案例，后端测试已直接读取这个副本 | 迁为共享契约；不是所有夹具都随 FlClash 删除而丢失 |
| `scripts/test-native.py` | 隔离临时 PostgreSQL 测试入口 | 优先使用，不默认碰用户测试库 |

已有策略包括 SS、VLESS 等 15 类协议的受限子集，并不是只支持 SS。首次 TUN 实传验收以 SS AEAD 和实际部署的 VLESS TLS/Reality 组合为必过项；其他协议保留解析兼容但不能仅凭白名单就宣称 TUN/UDP/商业节点实测通过。依据：`docs/managed-protocols.md` 与实际 `backend/nodepolicy/policy.go`。

### 2.2 当前必须处理的断点

1. **旧客户端已删除，流水线尚未彻底脱钩。** `scripts/sync-backend-shared.py` 仍把 `FlClash/core/nodepolicy` 与旧 Flutter fixture 当规范源；根 CI 仍大量构建 FlClash。第一阶段迁移规范源与脚本，不能通过关闭校验让 CI 假绿。
2. **旧文档有滞后。** README 部分协议描述比共享策略旧，`docs/native-client-protocol.md` 仍引用被删除的 HANDOFF/Flutter 路径。接手以源码和新共享契约优先，历史测试数量不作为新 Swift App 通过证据。
3. **目前没有新的 SwiftUI App/Helper/TUN 实现。** 不复用旧系统代理模式的“已连接”语义。
4. **生产模式明确禁止现有代理下发。** `backend/internal/config/config.go:99–108` 禁止 production 下启用节点目录或文件配置，原因是缺少节点侧计量与凭据撤销。不得删判断或用 production 环境伪装 development 来上线。
5. **当前配置版本受整个可见目录影响。** `node_catalog.go:91–92` 把目录元数据哈希放入配置；即使改的是另一可见节点，也可能触发当前 `profile_changed`。首版沿用保守停连/重同步，不宣称仅影响被修改线路。后续拆分 selected-profile revision 属独立兼容改动。
6. **目录监听不保活、不续租。** `/nodes` 只读；不能用长轮询成功替代 `/session` 或 `/traffic` 的授权确认。

## 3. 技术栈与职责边界

### 3.1 macOS 进程图

```text
AsterLink.app（SwiftUI，普通用户权限）
  ├─ Keychain：可选记住密码；UI 不持久化会话 token
  └─ 类型化 XPC（审核调用方签名、audit token、登录用户）
       ↓
AsterLinkHelper（Swift，launchd / SMAppService 特权服务）
  ├─ 唯一 worker 的启动/停止、签名/版本检查、权限及生命周期
  └─ 私有双向管道（长度前缀 + 有界 JSON，非公网/回环 HTTP）
       ↓
AsterLinkCore（Go executable，内嵌固定 Mihomo）
  ├─ 唯一账户/API/节点监听/授权/计量状态机
  ├─ 仅当前选中节点的受限配置 + 客户端固定 TUN/DNS 模板
  └─ macOS utun → Mihomo → SS/VLESS 远端节点 → 目标网络

现有 Next.js / Mantine → 同源 /api/v1 → Go/Gin → PostgreSQL
生产新增：真实节点上的访问控制/计量适配器 → Go/Gin 内部 Agent API
```

SwiftUI 不直接用 `/traffic`，也不另开第二套会话定时器。Core 负责与后台交互，Swift 侧通过 XPC 取得脱敏快照。Go API client 使用独立 `net/http` transport；Swift 原生 `URLSession` 仅在确有公开信息请求时使用，不为凑技术栈重复实现账户协议。

Core 是在一个 Go Worker 进程中嵌入 Mihomo，不是 Worker 再启动另一个 `mihomo` 子进程。Mihomo 现有累计统计、显式停止和代理适配器调用封装在 `EngineAdapter` 中；锁定提交上若缺少需要的 API，只增加经测试的小补丁，不重新 fork 整套 UI。

### 3.2 技术选择表

| 模块 | 选择 | 约束 |
| --- | --- | --- |
| UI | Swift / SwiftUI、Observation、少量 AppKit（菜单栏与生命周期） | 简单 MVVM；UI 更新留在 MainActor |
| 并发 | Swift actor + Go 单一 session coordinator | 一次操作一个 generation，不并发切换计费归属 |
| 系统服务 | ServiceManagement `SMAppService.daemon`、launchd | 用户首次明确授权；不在每次连接索取 sudo 密码 |
| App ↔ Helper | NSXPCConnection / NSXPCInterface | 只允许固定命令/安全 DTO，审核调用方代码签名 |
| Helper ↔ Core | 私有 pipe、有界帧、协议版本/请求 ID | token/密码不放 argv、环境变量、日志 |
| 隧道 | 固定版本 Mihomo 原生 TUN | 首轮验证 `system` 栈；不支持的能力不能悄悄回退系统代理 |
| 账号保存 | Security.framework Keychain | 非同步 generic password，按精确 API base + 账号隔离 |
| 图表 | 数字指标；需要曲线时用系统 Swift Charts | 只维护短时内存窗口，不新建分析数据库 |
| 测试 | XCTest / XCUITest、Go test/race、现有隔离 PostgreSQL | 实际执行数、失败日志、机器版本随交接保存 |
| 发布 | Xcode archive、Developer ID、notarytool、stapler、DMG | 手动下载安装更新即可，不首发 Sparkle |

Network Extension 可以成为日后 App Store/平台化路线，但 `NEPacketTunnelProvider` 的 packetFlow 并不等于给现有 Mihomo CLI 一个普通 utun；还涉及库化、数据包桥接、生命周期、签名授权与审核。本轮不并行开发。根特权路线不适用于未来直接照搬到 iOS。

### 3.3 特权 Helper 的不可省略要求

Helper 只允许 `hello/login/connect/disconnect/refresh/select/logout/status/shutdown` 等受限操作，不接受任意程序路径、shell 字符串、完整用户 YAML、下载 URL 或可执行插件。校验 XPC audit token 对应的实际签名身份、预期 Team ID、Bundle ID 和当前登录用户；不能只相信传入 PID、进程名或“同一个 Team”。实现须在首轮完成签名身份验证实验。

新安装或版本升级的 Worker 从已验证包中原子安装到 root 所有、普通用户不可写的版本目录；拒绝符号链接与路径穿越，校验固定 manifest 哈希及签名，再启动。不能让 root 执行用户家目录里可以在校验后被替换的二进制。Helper 自身遵循 SMAppService 的受签名包注册方式，升级时先停旧会话再重新注册/核验，不动态 patch 已签名 bundle。

同一 Helper 只允许一个前台登录用户拥有一个 Worker。第二实例、快速用户切换、旧 XPC 连接失效均需确定性拒绝或先停旧连接。Helper 不自动重启一个已丢失授权的连接；worker 崩溃、UI 明确退出、授权看门狗超时后先停止数据通路，再清理自己的资源。

初版最小权限边界是 UI 非 root、特权服务范围受控；Worker 处理 TUN 时仍是高权限代码，必须关闭所有外部脚本、文件加载、providers、任意控制监听和用户自定义配置。不要声称进程拆分已让整个内核变成低权限。

## 4. 目录、配置与协议单一来源

### 4.1 目标目录（下列新增路径是计划，不是已存在）

```text
apps/macos/
  AsterLink.xcodeproj/
  App/                         # SwiftUI + Keychain + XPC client
  Helper/                      # Swift launchd service + signature verification
  Shared/                      # Swift IPC DTO / error mapping
  Resources/                   # 固定资源、图标、license manifest
  Config/                      # Generated.xcconfig，生成物不提交
  Tests/                       # Swift 单元测试和 UI 测试
  .env.example                 # 只有公开构建参数
native/core/
  cmd/asterlink-core/           # 一份 Go executable
  internal/{ipc,managed,engine,meter,network}/
  go.mod / go.sum
shared/nodepolicy/             # 从现存 backend 快照迁出并核对来源
shared/contracts/
  native-v1.json               # 保留原 8 案例的语义
  native-v2.json               # 第四阶段 Agent/grant 能力，独立夹具
  ipc-v1.json
backend/nodepolicy/            # 保留可独立部署的生成副本
backend/internal/api/testdata/ # 保留规范契约生成副本
scripts/
  build-native-macos.py        # 新构建入口
  test-native-macos.py         # 单入口，显式区分 unit / tun / release
  sync-backend-shared.py       # 改为从 shared/ 生成并检查
artifacts/native-macos/<run-id>/  # 不提交：脱敏日志、测试报告
handoff_app.md                # 本文件，唯一进度入口
```

Mihomo 使用目前策略声明的提交 `70f0570405c3c2c47bb113b88db95006d239b346` 作为审查起点，不把它直接称作最新或已通过新客户端验收。第一阶段落实源码来源、LICENSE、实际版本/补丁/hash、依赖锁与 TUN 可构建性；不可因为原子模块已删而下载一个 `latest` 二进制替代。必要升级必须同步策略和实测。

共享策略在 `shared/nodepolicy` 唯一维护。继续生成 `backend/nodepolicy`，保持只复制完整 backend 目录即可部署。新 Core 可引用 shared 模块；CI 必须检查生成副本、协议样例、锁定 core schema 的一致性。旧 `backend/shared-sources.json` 也要更新，禁止两个副本独立演化。

### 4.2 `.env` 的边界

| 文件 | 保存什么 | 生效方式 |
| --- | --- | --- |
| `frontend/.env` | `API_INTERNAL_URL`、前端运行配置等 | 沿用项目实际读取方式；不把秘密放 NEXT_PUBLIC_* |
| `backend/.env` | 数据库、服务端密钥、业务开关和可配置计时参数 | 服务端重启/受控部署后生效，不下发原文 |
| `apps/macos/.env`（新增） | App 的公开 API base、网站 base、开发/发布模式 | 构建时读取、验证后生成配置，修改后重新构建签名安装 |
| Keychain / Core 内存 | 用户密码（可选）/ 临时 token / 当前节点秘密 | 不进入任何 `.env`、源码、安装 manifest 或日志 |

新客户端示例（仅公开信息）：

```dotenv
APP_ENV=development
API_BASE_URL=https://demo.hyshentou.cn/api/v1/client
WEBSITE_BASE_URL=https://test.hyshentou.cn
```

只允许白名单键，拒绝重复键、shell 插值、符号链接、非法路径和发布包中的非 HTTPS；测试 HTTP 仅允许显式 Debug 回环目标。解析 `.env` 不是 `source .env`。发布包不能从当前目录、用户环境或旧 FlClash JSON 自动覆盖 API 地址，避免把记住的密码发到攻击者服务器。开发地址与测试 host 注入不得进入 Release。

API 域名可公开；前后端 `.env` 不能改变已安装 App 的编译配置。节点 YAML/套餐/倍率是服务器动态业务数据，不要求重新发布 App。版本、Bundle ID、签名身份和 core 完整性约束应有受版本控制的构建配置/锁文件，不把一切安全常量都做成用户随意修改的运行开关。

目前 `report=60s / lease=90s / idle=180s / nodes-watch=5s` 在 Go 代码内有固定值，不是现成环境变量。第二阶段将这些业务时限改为统一可编辑参数，新增 `CLIENT_REPORT_INTERVAL_SECONDS`、`CLIENT_LEASE_SECONDS`、`CLIENT_IDLE_TIMEOUT_SECONDS`、`CLIENT_NODE_WATCH_SECONDS`，保持默认行为，启动时验证范围及关系。服务端通过响应提供实际值；客户端不在 UI/Helper/Core 各硬编码一套。改变协议时限必须同步测试。

数据库凭据只使用现有受限 `.env`，本文件不复制聊天中的密码、样例代理密码或真实 YAML。已在聊天公开的数据库/节点凭据在对外测试前轮换；生产数据库需要验证证书的 TLS、最小权限和网络访问控制。

## 5. HTTP / IPC 契约

### 5.1 现有原生 API：第一至第三阶段直接复用

基础地址：`https://demo.hyshentou.cn/api/v1/client`。带身份的请求使用 `Authorization: Bearer <opaque-token>`；JSON、HTTPS、禁止跨域重定向携带凭据、禁用系统代理自动继承、响应不缓存。网页 `GET /api/v1/client/bootstrap` 使用 Cookie，**不是**这个原生 Bearer 会话的配置接口。

| 方法与路径 | 请求 | 当前响应 / 重点 |
| --- | --- | --- |
| POST `/login` | `{email,password,device_id,platform:"macos",app_version}` | `{token,user,session}`；登录成功不代表套餐可连接 |
| GET `/session` | Bearer | 直接 session 对象，没有 `session` 外层 |
| GET `/nodes` | 可选 `?revision=<64位hex>` | `{session_id,revision,reason,nodes}`；相同 revision 约 5 秒长轮询，不是 304 |
| GET `/config` | `?node_id=<uuid>`；已绑定会话必须加 `last_sequence=<已确认序号>` | `{yaml,version,session,nodes,node_id}`，目录模式下才有后两项 |
| POST `/traffic` | `{sequence,upload_bytes,download_bytes}` | `{session,replayed}`；严格累计，不接受 rate/user/node 自报字段 |
| POST `/logout` | JSON `{}` | `{logged_out:true}`；先本地停流并尽力结账，再撤销 |

错误统一 `{error:{code,message}}`。客户端以稳定 code 映射中文，不以 message 字符串做控制逻辑；错误回执也不得泄漏 raw YAML。

核心状态至少包括：`session_id`、`server_time`、`expires_at`、`subscription_expires_at`、`authorization_expires_at`、`can_connect`、`reason`、`profile_version`、`remaining_bytes`、`total_bytes`、`used_units`、`last_sequence`、`upload_bytes`、`download_bytes`、`rate_permille`、`report_interval_seconds`、`lease_seconds`、`session_idle_timeout_seconds`、`metering_source`。

`expires_at` 是账户原生会话截止，不是套餐截止。状态里的 `can_connect=true` 是服务端授权，不是 utun 已建好；只有本机权限、配置验证、有效授权及真实转发健康检查都通过，才显示“已连接”。HTTP 200 也可能带 `can_connect=false`，尤其是尾账。

| code/状态 | 必须行为 |
| --- | --- |
| `invalid_credentials` / `native_session_required` / `native_session_expired` | 停流、清临时身份，重新在线登录 |
| `upgrade_required` / `paid_vip_required` / `subscription_expired` / `quota_exhausted` | 不连接，显示对应套餐操作；不拿旧配置继续 |
| `profile_required` | 同步并绑定有权节点，不当作网络故障 |
| `profile_changed` / `client_config_unavailable` | 停旧流、结算、刷新；用户再次确认连接 |
| `subscription_changed` | 禁止把旧用量写入新周期，重新登录 |
| `device_limit` | 告知设备上限；不能重建随机 UUID 绕过 |
| `traffic_sequence` / `traffic_conflict` | 保留待确认证据、停连，不伪造新序号补偿 |
| HTTP 超时 / TLS 失败 / 5xx / 429 | 结束未授权连接尝试或停流；带抖动退避，不能忽略错误续租 |

### 5.2 时间与并发规则

Core 是唯一 HTTP 状态所有者。未启动 Meter 时按返回间隔 GET `/session` 保活；开发计量运行后由唯一 Meter 发 `/traffic` 并接续租约，不能再叠加另一个保活写入者。独立 `/nodes` watcher 仅观察目录，不续授权。UI 每秒或两秒读取快照，不触发账务 API。

租约以服务端截止时间和服务端时间计算剩余长度，再从**请求开始时的本机连续单调时钟**保守计时，网络耗时必须消耗租约。绝对会话/套餐期限仍是上限。使用包含睡眠时间的连续时钟，睡眠唤醒事件也必须先关门再重新验证；不能用会在睡眠时停止推进的计时器扩大授权。时钟回拨、响应迟到、旧 generation 响应均不得续新会话。

目录正常监听保持最多一个未完成请求，客户端超时须大于服务端 5 秒，例如 10 秒。重连用带抖动退避，不创建无限 goroutine。当前约 12 次/分钟/客户端的监听会消耗 API 限流预算；生产需要按可信会话/账号合理限流并限制并发长轮询，不能单纯无限抬高同一出口 IP 的阈值。

### 5.3 本机 IPC 最小契约

协议版本独立于 App 版本。每个命令包含 `request_id`、`protocol_version`、`generation`；选择操作还需当前配置 ID/revision。迟到回复必须丢弃，重复 disconnect/logout 幂等。初始化 Hello 核对 GUI/Helper/Core 兼容范围，版本不兼容时不得连接。

命令限定为：`Login`、`ReadSnapshot`、`RefreshCatalog`、`SelectNode`、`Connect`、`Disconnect`、`Logout`、`Shutdown`。Keychain 保存发生在 SwiftUI；Login 一次性传入的密码只经受限 IPC 进入 Core。token 只留 Core 内存，Helper 不落盘。普通 Snapshot 不包含 password/token/yaml。

Snapshot 建议字段：`generation/state/user/entitlement/nodes/selected_node_id/catalog_revision/profile_version/tunnel_health/metering/last_error/core_version`。`metering` 区分本地累计、实时速率、确认余额、估算余额和 pending，不把估算余额当最终账单。

XPC DTO 限制允许解码的类型；Helper↔Core 帧上限建议 2 MiB，单份配置仍不超过现有 1 MiB 上限，其他普通命令限制更小。读写限时、EOF、长度溢出、无效 JSON、未知命令均有负测。不得暴露 Mihomo `/configs` 或通用 RPC 给任意本机程序。

## 6. TUN 与连接生命周期

### 6.1 固定配置策略

服务器继续管理“节点定义”；客户端自行提供审查后的 TUN、DNS、运行目录与路由模板。不能将管理员节点 YAML 原样当 root runtime 配置执行。Core 先验证配置 SHA-256，再做共享 nodepolicy 校验、Mihomo 语义解析，最后组合固定模板；SHA-256 是一致性校验，不代替 HTTPS 或发布签名。

首轮以锁定 Mihomo 的 `system` TUN 栈实测，验证 macOS TCP/UDP/DNS。如果固定版本需要不同栈，只允许在技术验证后作一次明确架构决策并更新构建锁，不能运行失败就自动退回 mixed/system proxy。具体 TUN YAML 字段必须从固定提交和测试确认，不把网上其他 Clash 分支的样例直接搬入。

全局模式首版只选一个付费节点，不做 DIRECT 兜底与自动备用节点。禁用 mixed/socks/http 对外监听、外部 controller、allow-lan、任意 provider、脚本、外部插件可执行文件和任意磁盘路径加载。普通 UI 不提供关闭安全策略的选项。

### 6.2 必须解决的网络问题

| 项目 | 实现与验收要求 |
| --- | --- |
| 真 TUN | utun 存在、IPv4/IPv6 路由按策略进入隧道；无需应用设置 HTTP/SOCKS proxy 的流量也被接管 |
| 外层节点连接 | 绑定正确物理出口或使用内核受支持的路由排除，避免代理连接再次进入自己的 TUN |
| API 控制面 | 独立 transport，不跟随系统代理；在 TUN 工作时也能不依赖付费节点直达 API；物理出口绑定需同时覆盖 v4/v6 |
| DNS | 业务 DNS 由受控 resolver 经选中节点处理；明文 53、DoH、DoT 的预期路径均记录并测试；bootstrap DNS 例外只限控制面/节点建立所需 |
| IPv6 | 连接期间 IPv6 完整代理，或对未支持的 IPv6 明确阻断；不能静默绕过，更不能自动全局关闭系统 IPv6 |
| UDP/QUIC | 对 SS/VLESS 实际支持的 UDP 路径测试转发、DNS和 QUIC；不支持则受控拒绝，不回退 DIRECT |
| 网络切换 | Wi-Fi/有线变化、接口索引/IP改变先关闭旧授权数据通路，再重新发现物理出口与在线鉴权 |
| 路由/DNS恢复 | 只清理本会话拥有的 utun、路由和 DNS 变更；检查所有权/代次，不恢复过时快照覆盖用户或其他 VPN 的新设置 |
| 竞争软件 | 已有其他 VPN 或路由不明确时提示冲突并拒绝连接，首版不承诺叠加使用 |

bootstrap DNS、控制 API、loopback、是否放行局域网必须有明确的排除清单和测试；不要通过宽泛公网 CIDR DIRECT 规则“解决循环”。DNS 域名/IP变化按 TTL 与接口变化重新验证，不能写死示例代理 IP 或开发数据库地址。

连接测试至少包含受控 TCP 客户端、UDP echo/QUIC、DNS测试和浏览器 WebRTC/STUN。WebRTC 暴露公网物理出口视为连接期间泄漏；本地私网地址或 mDNS 主机名暴露与公网出口泄漏分开判断，不承诺 TUN 能消除所有浏览器指纹。

### 6.3 状态机与固定操作顺序

```text
SignedOut → Authenticating → Ready → Preparing → Connecting → Connected
                                ↑                         ↓
                                └──── Stopping / Settling ┘
任何阶段 → Blocked（到期/额度/权限）或 Error（网络/Helper/内核）
Logout / Quit → 停止数据通路 → 有界结账/撤销 → 清理 → SignedOut / Exit
```

开发模式连接：同步会话和目录 → 选定 node_id → 获取/验证选中配置 → 检查 Helper → 初始化唯一 Meter 并取得首个有效计量 ACK → 开启 TUN → 真实受控转发验证 → 显示 Connected。首报使用实际已初始化累计值，没有发生流量时合法为零增量，不在 Meter 尚不存在时制造占位报告；ACK 丢失则保持关闭并原批重试。启动失败任何一步都不得留下仍能转发的隐藏监听或旧配置。

权威模式改为获取并验证 ready grant + 当前有效 `/session` 授权，不调用开发 `/traffic`；本地 Meter 只显示用量。权威模式切换/退出先停止本机数据通路，再撤销旧 grant，等节点确认或安全租约截止处理尾账与预算，然后才能分配下一段。两种模式分别走固定流程，不能将新 grant 与旧客户端账本交错。

切节点或倍率/配置变化：封闭新 TCP/UDP → 终止旧流 → 等累计稳定 → 确认旧尾账 → 用已确认 last_sequence 获取新配置 → 校验并绑定 → 保持断开 → 用户明确连接。首版不做无缝切换。更换套餐周期或账号必须新登录，旧尾账不能扣新周期。

关闭窗口继续留在菜单栏；明确退出 App 则停止连接。显式退出登录还清 Keychain 中当前保存项；普通退出可保留用户主动记住的密码，但撤销内存会话。Helper 或 Core 意外退出不能通过自动重启继续旧授权；进程重开必须在线登录。

睡眠前尽力停流/结账；硬杀和断电不保证执行清理回调。唤醒、崩溃后先检查残留和本机网络，再认证。开发计费不能承诺硬杀后准确追回尾账；商业计费由节点账本兜底。

## 7. 流量、倍率与商业强制控制

### 7.1 保持现有整数口径

```text
up_delta   = current_upload   - acknowledged_upload
down_delta = current_download - acknowledged_download
charged_units = (up_delta + down_delta) * bound_rate_permille
remaining_bytes = max(0, floor((total_bytes * 1000 - used_units) / 1000))
```

1 unit = 1/1000 字节，500‰=0.5×、1000‰=1×。使用有符号 64 位受检算术，执行已有范围/溢出限制；不使用浮点金额或每批单独四舍五入。UI 显示 GiB 时使用 2^30 字节并明确单位。

例：上传 20 MiB + 下载 80 MiB，物理代理用量为 104,857,600 bytes。1× 扣 104,857,600,000 units，相当于 100 MiB 套餐量；0.5× 扣 52,428,800,000 units，相当于 50 MiB。不要把“实际传输了 100 MiB”和“扣了 50 MiB”混在同一指标中。

### 7.2 开发版客户端计量

从固定内核读取真实累计计数，不用每秒速率积分、不读网卡总流量、不以图表计数作为账本。确认锁定 core 的计数 API 是否排除 DIRECT/控制面，并通过真实传输验证；旧文档中的 `TotalTraffic(true)` 调用及相关补丁需要在新锁定源码中重新核对，不能假设上游同名 API 语义相同。

同一会话累计与 sequence 不因断开、刷新 UI 或切线路归零。core 计数器如有受控重建，只有旧段已停稳并确认后才可用显式 epoch/offset 延续累计；意外回退、溢出或不确定重启直接停连并重新登录，不偷偷套一个偏移掩盖漏量。

一次只允许一个 pending 报告。响应丢失时原 sequence 与原累计内容重试；不能新建序号或重新取不同累计替换 pending。后端相同批次 replay 不二次扣费，不同内容同序号必须拒绝。当前同一订阅周期的旧配置尾账仍可结算，但不能据此继续连接。

计费只包含经付费节点的双向用量。控制 API/loopback 不计费；经代理的 DNS 和显式连通性探测按实际口径计费并在说明中写清。计数层不同会包含不同协议字节，不能拿下载文件大小与内核/节点计数要求无条件相等。受控测试必须记录计数层、正文、代理握手/封装范围及可解释差异。

### 7.3 收费版需要节点侧独立权威

保持一个 Go/Gin 控制平面和 PostgreSQL；不为首版引入 Redis、Kafka、Kubernetes 或微服务拆分。实际代理节点上增加一个小型 Go NodeAgent 或供应商适配器。**Agent 必须接入真实转发器，能识别用户、读可信累计、禁止新连接并关闭已有流；仅定时向 API 发心跳的空 Agent 不合格。**

首选评估可管理的 Xray-core 节点适配（VLESS 与明确验证的 SS 方案），但本次未检查其当前源码/版本，不能把它的统计/动态用户 API 当作已证明能强制关闭存量连接。第一阶段应选择一个具体服务器实现并证明以下能力；若不具备则补适配或更换供应商，而不是省略功能。SS2022 多用户需要协议和服务端共同支持；普通共享 AEAD SS 可采用经评估的独立端口/凭据等隔离方案，不能在不支持多用户的同一端口随意发随机密码。

| 能力 | 最低证明 |
| --- | --- |
| 用户归属 | user/subscription-period/session/grant/node 可确定关联，凭据不是全体共享 |
| 实际认证 | 正确凭据连接、错误/其他账号凭据拒绝 |
| 可信累计 | 转发层双向累计，客户端完全不报流量时仍能入账 |
| 撤销 | 旧凭据在新连接与已建立 TCP/UDP 上均失效；不是仅删用户列表 |
| 到期/离线 | 节点按短期授权绝对截止强制执行，控制面断开不无限放行 |
| 配额 | 节点执行有界字节预算，多个设备/节点不会各自花同一余额 |
| 重启恢复 | Agent/转发器重启有 epoch 与持久化检查点，不漏扣/双扣，不先无授权放行 |

第三方静态节点没有这些能力时只能标记为开发/非权威节点，不向收费套餐下发。协议支持矩阵应拆成“解析支持 / TUN 实传通过 / 节点权威控制通过”三列；一个协议能解析不代表已适合销售。

### 7.4 第四阶段的最小增量契约（计划新增，当前不存在）

不破坏开发 v1 流程，新增能力协商与 grant 路径。普通 App 只多一个 Core 内部适配层；网站管理员维护节点的 UI 基本不变，增加受管能力状态即可。字段/错误/例子写入 `shared/contracts/native-v2.json` 并由前后端共同测试后实现。

下表客户端接口都以 `/api/v1/client` 为完整前缀；Agent 接口使用表中完整路径。这里 v2 指契约能力版本，不表示已存在 `/api/v2` 路由。

| 新增接口 | 最小请求 / 响应约定 |
| --- | --- |
| GET `/capabilities` | 公开非秘密能力：`contract_versions`、`metering_modes`、`min_app_version`、`core_policy_revision` |
| POST `/grants` | Bearer + `Idempotency-Key`，请求 `{node_id}`；服务端绑定用户、订阅周期、版本、倍率和预算；返回 `{grant_id,state}`，可为 202 pending |
| GET `/grants/:id` | 只允许所属用户/会话查询；pending 不可连接；ready 返回 `{grant_id,yaml,version,node_id,session}`，仅当前 grant 的凭据，`Cache-Control:no-store` |
| POST `/grants/:id/revoke` | 幂等撤销；先本地停流，服务端推进撤销状态并等待节点确认/租约截止 |
| POST `/internal/v1/node-agents/sync` | mTLS Agent 身份、已应用 revision/撤销回执；下发待安装 grant、撤销及有界预算租约 |
| POST `/internal/v1/node-agents/usage` | mTLS + `{grant_id,epoch,sequence,upload_bytes,download_bytes}`；累计幂等回执，不接受自报 user/rate/套餐 |

新增状态码须固定到夹具，至少包括 `grant_pending`、`grant_expired`、`agent_unavailable`、`metering_source_mismatch`、`client_upgrade_required`。未知能力必须拒绝收费连接，不静默降级共享配置。现有登录可复用，身份令牌仍是不透明 Bearer；不为宣传“JWT”改掉安全的现有协议。

`GET /session` 在权威模式返回 `metering_source=node_reported` 和当前 grant 的有效期限/服务端余额，并定期维持官方会话授权。`GET /nodes` 仍只观察目录。旧 `POST /traffic` 只服务开发 `client_reported` 模式，权威会话调用必须拒绝为 `metering_source_mismatch`，**不能同时按客户端与节点报告扣两次**。商业模式图表仍本地采样，但账单只取节点账本。

现有 NativeSession 需要追加模式/grant 关联，保留开发计数语义；`nativeSnapshot`、原生授权中间件和配置流程必须分别处理两种模式。生产 `/config` 不再泄露静态共享秘密；只允许经 grant 的单用户资料。开发/生产模式由服务端配置与节点能力决定，不能信任客户端传入的 source 或 app_version 来开放收费权限。

最小新增持久化：NodeAgent 身份/能力与版本；ConnectionGrant（归属、周期、节点/版本、倍率快照、到期、凭据密文、预算、状态）；NodeUsageReport / 游标（grant+epoch+sequence 唯一、累计、增量、扣量）。同一数据库事务写用量、游标、预算与订阅扣量，按现有锁顺序扩展并测试死锁/竞争。不得持锁等待 Agent 网络回包：先持久化 pending/预算预留，节点确认后原子转 ready。

另需不可变的 SubscriptionPeriod 周期账目：现有 `model.Subscription` 对 user_id 唯一，不能把它当历史周期表。grant 和节点账本绑定 period_id；旧周期迟到用量只更新旧周期账，不更新当前新周期的 UsedUnits。追加迁移把现有订阅初始化为对应周期快照，不重置既有余额；数据库迁移先备份并有幂等升级测试。

### 7.5 配额、倍率变化与故障预算

仅靠“每 60 秒上报后再断开”会产生高速超用，不能承诺严格配额。服务端应按订阅周期在事务内分配小额加权预算，使 `已结算用量 + 未释放预留预算 <= 套餐额度`，节点只在已获预算内转发。原始字节预算可取 `floor(可用units / rate_permille)`；双向用量都消耗预算。

同一用户多节点/多设备共享同一预算池。报告确认后结转已用部分；未用预留只有拿到最终游标/撤销确认或经过安全到期对账后才能释放，不能因 API 超时就立即返还给另一个设备。重试不能重复预留或重复扣费。转发器缓冲中的在途字节必须确定处理规则和经实测的上界，不宣传不现实的零字节瞬时切断。

倍率修改从新 grant/计费段生效，旧段按已绑定倍率结算；没有在计费中途读取“最新倍率”重算历史。节点删除、禁用、套餐到期、账号禁用均撤销相关 grants；节点按已下发的绝对截止本地关闭，不依赖 App 自觉退出。续费同周期不归零计数，换周期生成新的归属，旧尾账仍写旧周期留存账目而不污染新额度。

建议第一版 grant 最大租约沿用 90 秒，节点同步/计量周期选有界短间隔并固定测试；撤销正常在线目标不超过 5 秒，断网最迟在已授权租约截止关闭。它们是要验证的目标，不是当前已实现保证。到期和预算本地执行不等同于“管理员保存瞬间全世界连接立即断开”。

节点崩溃要防止尾部用量消失：累计检查点/WAL、未决预算保留与重启恢复一起实现；无法恢复的记录进入明确的待对账状态，不能安静归零或无依据全额扣用户。生产就绪判断只能在支持的节点上实际验收后改变，不能新增一个默认 true 的开关取代现有保护。

## 8. 五阶段开发与交接

每阶段只维护一个任务清单和本文件末尾的进度记录；不额外建立繁复项目管理流程。负责人可以一人兼多角，但责任与验收证据不能省略。

### 阶段 1：去旧依赖、固定契约、验证原生特权 TUN

**目标：** 尽早证明所选架构能在当前 Mac 创建真实 TUN 并干净退出，而不是先做完 UI 才发现权限或内核不可用。

**任务：**
- [x] 核对当前工作区边界；迁 `shared/nodepolicy` 与共享 fixture，更新同步脚本、manifest、后端独立部署测试；不恢复整个 FlClash。
- [x] 新建 SwiftUI App、Helper、Go Core 最小工程与 IPC Hello；固定 Bundle ID、协议版本、Go/core/SDK 来源与许可证记录。
- [x] `.env.example` 与构建配置校验，Debug/Release 分离；构建脚本不读取后端数据库秘密。
- [~] SMAppService 用户授权、XPC 调用方校验、单 Worker 与签名/路径验证；拒绝任意本地进程调用。（代码与 Helper↔Core 部分已测；真实 launchd 注册与 XPC 拒绝伪调用方需在批准后的真机验证）
- [~] 在隔离/受控网络下用测试专用 SS 节点演示 TUN TCP 转发和关闭恢复；测试入口不能混入 Release。
- [ ] 确认可管理商业节点或供应商能力（等待用户回答，见交接条目）；选定一种具体转发器及 SS/VLESS 用户控制方案，记录无法满足项。

**验收：** 不设置系统 HTTP/SOCKS 代理也能改变受控测试请求的出口；取消 Helper 授权不能创建 TUN；拒绝假签名/错误版本/第二实例；退出后没有本应用遗留路由/DNS。共享策略/契约与后端基础测试可在无 FlClash 目录下运行。

**交付：** 可构建空壳 App + 受控 TUN PoC、迁移后 CI 基础检查、架构/签名/NodeAgent 能力结论、真实证据。失败在这里处理，不跳到 UI 装饰。

### 阶段 2：真实登录、套餐节点、Keychain 与状态机

**目标：** 管理员网站配置的数据进入新 App，用户只能操作合法节点，具备清晰的连接准备状态。

**任务：**
- [ ] Core 实现现有 v1 登录/session/nodes/config/logout，使用共享 fixture 精确验证包装层与错误。
- [ ] SwiftUI 三块最小界面；网站购买/续费入口指向测试网站；UI 不持有节点秘密。
- [ ] Keychain 可选记住密码，按 API base+邮箱隔离；重开在线认证；保存失败禁止明文降级。
- [ ] 固定设备 UUID，账号切换/登出取消旧请求；generation 防迟到恢复；管理员无套餐不可代理。
- [ ] `/nodes` 单长轮询、前台/启动/恢复同步；独立保活；管理员变更后同步列表并使旧配置失效。
- [ ] 实现 ConfigStore/EngineAdapter 只接收当前选中节点、哈希与白名单验证；不暴露原始 YAML。
- [ ] 新增后端业务时限的 env 解析与边界测试，保持 60/90/180/5 秒默认值；安全范围仍受代码校验。

**验收：** 正确/错误密码、无套餐、到期、0额度、设备上限、权限不足、Keychain 拒绝、API不可用均行为正确；管理员改名称/倍率/启停/套餐后正常网络下目标 5 秒内反映（记录实际延迟）。`/nodes` 单独成功不能维持过期会话。读取 UI、日志及普通偏好文件找不到 token/YAML/password。

**交付：** 可安装的真实账户/节点版本；接口字段与中文错误映射；未经授权的状态始终不能调用 TUN Start。

### 阶段 3：完整 TUN、真实开发计量与恢复

**目标：** 完成用户最关心的“登录→选节点→连接→真实流量→正确倍率扣量→到期停连”。此时仍明确属于开发账本。

**任务：**
- [ ] 固定全局 TUN/DNS 模板；IPv4/IPv6、UDP、外层出口、控制面直连与循环检测。
- [ ] SS AEAD、实际选用的 VLESS TLS/Reality 节点真实转发；不把 YAML parse 成功当握手成功。
- [ ] 唯一累计 Meter、速率图、确认/估算余额、分钟上报、pending 原批幂等重试。
- [ ] 断开/换节点/改倍率/禁用时先停流结账；错误 ACK 不丢 pending；会话代次与 Core 重启保护。
- [ ] 睡眠/唤醒、接口变化、API断网、UI/Core/Helper崩溃、正常退出和残留恢复。
- [ ] 完善 TUN 权限集成测试入口，必须显式指定受控目标和授权，不在普通单元测试里修改真实机器网络。

**验收：** 用同层可对账的受控上传/下载验证 0.5×、1×和中途换倍率，账本精确匹配整数计算；重试/并发不二次扣费；额度/到期/删除/网络确认失败时官方客户端停止代理；连接期间 DNS/IPv6/UDP/WebRTC 无未声明公网出口泄漏。断开后本机正常网络可恢复，连续至少 20 次连接/断开没有资源累积。

**交付：** 原生开发闭环 App、后台账本证据、已解释的协议计数差异、故障矩阵。`metering_source` 仍是 `client_reported`，不得解除 production guard。

### 阶段 4：节点权威计量、独立凭据与强制停用

**目标：** 用户修改 App、不报流量、拿已提取凭据连接，也不能逃避节点侧授权/计费限制。

**任务：**
- [ ] 先固定 capability/grant/Agent 契约、计数口径、错误码、迁移与回滚方式，再开发；不让 Swift/App 和后端各猜字段。
- [ ] 实现选定 NodeAgent 转发器适配，至少在自控 SS 和 VLESS 目标上验证真实用户归属、读取计数、关闭存量 TCP/UDP。
- [ ] 新增最小 grant/NodeUsage 持久化和 mTLS；只在 Agent 确认安装后返回 ready 凭据；不持 DB 锁等待网络。
- [ ] 原子预算预留、累计幂等、epoch/检查点、异常重启恢复与待对账状态；倍率按段绑定。
- [ ] Core 在 `node_reported` 模式消费 grant；本地速率仅展示，后端不再用 `/traffic` 扣这类会话。
- [ ] 生产节点能力校验替换旧“一律拒绝”的交付路径，但保留对非权威/共享节点的拒绝；测试权益不能进入 production。

**验收：** 关闭全部客户端流量上报后仍准确计费；抓取已授权测试凭据交给独立测试程序，撤销/到期后新连接与现有 TCP/UDP 都失效；多设备/节点预算竞争不超分；伪造 Agent、重复/乱序/回退计数、API断网、Agent重启均不能绕过或双扣。任何一个协议没验证其强制控制，不能给它标记“生产可用”。

**交付：** 真实受管节点与权威账本、生产能力证明、回滚/迁移记录。没有节点管理权或关键适配能力时明确 BLOCKED，App 可保留开发版，不把这部分移出收费门槛。

### 阶段 5：回归、签名安装与发布交接

**目标：** 非开发人员在正常 macOS 安全机制下安装、授权、使用、升级和卸载，出现问题可定位和回退。

**任务：**
- [ ] 单入口构建/测试脚本，CI 不再依赖 FlClash/Flutter/Rust；保留网站/后端回归，不混入测试节点或测试开关。
- [ ] Developer ID 从内到外逐个签名 App/Helper/Core，启用适当 Hardened Runtime，验证没有 `get-task-allow` 和不必要 entitlement。
- [ ] 公证、staple、DMG 制作，干净用户/测试机 Gatekeeper 安装；不得全局关 SIP/Gatekeeper 或通过删除 quarantine 冒充发布测试。
- [ ] 升级先断开，处理 Helper 版本不一致；卸载指引清理已注册服务/本产品资源，不动其他 VPN。
- [ ] 长时运行、20 次启停、至少 10 次睡眠/网络切换、Core硬杀与多实例测试；记录原生崩溃率/内存/CPU基线，不填写猜测指标。
- [ ] 日志脱敏、隐私/许可证/对应源码交付、密钥轮换、节点计量报警、备份与回滚演练；明确网站的真实支付/MFA/恢复等外部发布阻塞项。

**验收：** 在签名且公证的 Release 包上完成第 9 节矩阵；没有真实数据库/代理密码、测试证书、测试授权、任意 API 覆盖入口；核心版本/哈希与发布 manifest 一致。发布回退停用新 grant/新客户端时不恢复不安全共享模式。

**交付：** DMG、校验文件、签名/公证证明、release manifest、脱敏测试报告、安装升级卸载说明、回滚方案、已知限制和许可证材料。只有开发版通过时不得把此阶段状态写“商业可上线”。

## 9. 统一测试与最终验收矩阵

各测试输出 `case_id / expected / observed / pass|fail|blocked / build / machine / evidence`。不得把“未运行”写成“通过”；历史 Flutter 证据仅可作测试设计参考。

| ID | 场景 | 必须结果 | 阶段 |
| --- | --- | --- | --- |
| C01 | v1 login/session/config/traffic/logout 夹具 | 包装层、日期、整数、错误码与现存协议一致 | 1–2 |
| C02 | 正确/错误密码、保存/取消/删除密码 | Keychain 隔离，重开在线登录，无明文兜底 | 2 |
| C03 | 普通账户调用管理 API、管理员无套餐连接 | 均不能绕过角色/订阅检查 | 2 |
| C04 | 免费/到期/额度0/测试权益关闭/设备上限 | 不下发可用代理授权，不启动 TUN | 2–4 |
| C05 | 长轮询断开、重复启动、429、账号切换迟到回复 | 只有一个 watcher/报告者，旧回复不能改新状态 | 2–3 |
| C06 | 改/删/禁用节点或变更倍率/套餐白名单 | 元数据同步、旧连接停止、旧段按旧倍率结清 | 2–4 |
| T01 | 无系统代理的 TCP 实传 | 通过 utun 和所选节点，出口可证明 | 1、3 |
| T02 | SS AEAD 与所选 VLESS TLS/Reality 组合 | 真握手/实传；错误凭据拒绝；TLS验证有效 | 3–4 |
| T03 | UDP echo、DNS、QUIC | 已承诺能力实传；不支持的路径拒绝且不直连 | 3 |
| T04 | IPv4/IPv6、53/DoH/DoT、WebRTC/STUN | 无未声明公网出口/DNS泄漏；明确例外 | 3、5 |
| T05 | API在TUN期间可达、节点/API DNS变化 | 控制面不依赖付费节点，不路由自循环 | 3 |
| T06 | 休眠唤醒、时钟回拨、Wi-Fi/有线切换 | 重新在线验证前不复用旧授权 | 3、5 |
| T07 | Core/UI/Helper 崩溃、权限拒绝、其他VPN冲突 | 确定性停流/提示，资源可恢复；无静默后台代理 | 3、5 |
| B01 | 同样双向字节量分别按500‰/1000‰ | 精确整数扣量，UI物理量与套餐量分开 | 3–4 |
| B02 | ACK丢失、重复批、不同内容同序号、乱序、回退 | 幂等或拒绝，不双扣、不伪造累计 | 3–4 |
| B03 | 中途换节点/倍率、修改/续费/更换周期 | 正确分段与周期归属，不按新倍率重算历史 | 3–4 |
| B04 | 独立测试客户端完全不报流量 | 仍由节点正确计费 | 4 |
| B05 | 提取测试凭据后撤销/过期/额度耗尽 | 新连接与已存在TCP/UDP均失效，测出最迟时间 | 4 |
| B06 | 多设备/多节点并发争用最后额度 | 预算不超分，不因超时释放造成重复消费 | 4 |
| B07 | Agent伪造/重放/重启、API或数据库故障 | mTLS拒绝、持久化游标恢复、租约到期停流 | 4–5 |
| S01 | 伪装本地 XPC 调用方、替换 Worker、符号链接 | 拒绝提权与任意程序/配置执行 | 1、5 |
| S02 | 检查UI/日志/偏好/构建产物/崩溃报告 | 无 token、密码、完整配置及数据库秘密 | 2–5 |
| R01 | 干净用户安装签名公证DMG、首次Helper授权 | 不靠开发机特殊安全设置即可运行 | 5 |
| R02 | 升级、拒绝升级、卸载、版本不一致 | 不复用旧授权，不留下可转发残留服务 | 5 |
| R03 | Go/API/网站/共享策略/原生回归 | 原有安全与计费测试不因移除FlClash而被静默删掉 | 每轮 |

### 测试环境与命令契约

日常测试用隔离 PostgreSQL、本地受控代理/目标与测试 Keychain service，不读取日常浏览器资料。现有 `python3 scripts/test-native.py` 为后端入口；`go test ./...` / `go vet ./...` 在各 Go module 内运行。网络相关特权测试必须在明确同意的测试机上执行，准备好恢复步骤，不让 CI 的普通测试动开发者全局网络。

以下命令是**要求新脚本在相应阶段提供的入口，现在尚未实现**：

```sh
# 工程创建后：不启动系统隧道
python3 scripts/test-native-macos.py --suite unit
python3 scripts/build-native-macos.py --configuration Debug

# 受控测试机上明确授权才执行，不传真实账号密码作为 argv
python3 scripts/test-native-macos.py --suite tun --allow-network-changes

# 完成签名配置后：构建、校验与公证，秘密从安全凭据存储获取
python3 scripts/build-native-macos.py --configuration Release --notarize
python3 scripts/test-native-macos.py --suite release
```

脚本应提前检查环境、版本、隔离数据库与权限，输出实际执行测试数，失败以非零退出；权限测试不获授权应写 BLOCKED，不换成 mock 后报成功。构建/测试产物写带 run-id 的 artifacts，凭据只从 Keychain/受限环境读取，日志与报告去敏。

## 10. 收费发布检查与风险

### 10.1 客户端上线前的硬门槛

- [ ] 真实 TUN，不是 system proxy；SS 与所承诺 VLESS 组合的 TCP/UDP/DNS/IPv6 矩阵通过。
- [ ] 每用户/grant 凭据、节点权威双向计量、存量连接撤销、到期及预算限制都在真实节点验证。
- [ ] 开发与权威账本互斥，production 不接受静态共享节点或测试套餐。
- [ ] App/Helper/Core 的发布签名、公证、安装升级卸载、XPC 身份与 root 二进制完整性通过。
- [ ] 明确披露首版不含系统级 Kill Switch、自动选路或零中断热切换，不夸大隐藏配置/线路质量。

### 10.2 网站和运营的外部依赖

原生客户端 5 阶段不等于整个收费网站自动完成。真实支付渠道回调验签、金额/订单/币种核对、幂等开通、退款/对账；邮箱验证/找回密码、密码变更撤销会话、管理员 MFA；HTTPS、数据库 verify-full TLS/网络隔离、备份恢复、日志告警、容量与限流；用户协议、隐私、计费解释、地区服务与合规审查，均需有负责人和证据。

App 不接触 PostgreSQL。测试数据库账号不得打包到安装文件。不要因用户允许开发测试就自动迁移/清空其远端库；需要读诊断时使用现有只读 doctor，写入/迁移必须明确选择环境、备份并记录。

### 10.3 主要风险与处置

| 风险 | 处置 |
| --- | --- |
| 节点没有管理权或只是共享凭据 | 第1阶段列为关键依赖，第4阶段阻塞收费；不虚构节点控制能力 |
| 管理 API删用户但旧 TCP/UDP 仍活着 | 必做存量连接 kill 的实际测试，不能只查配置已更新 |
| 长轮询与数据库锁/限流在多用户下放大 | 保留单实例可观测上限，按会话限流和压测；规模需要时再优化 |
| Helper提权攻击、用户可写二进制 | audit-token签名校验、root-owned工件、受限IPC，专项负测 |
| 睡眠/网络切换扩大授权或路由回环 | 连续时钟+恢复门禁、独立控制面出口、真实网络测试 |
| 纯客户端崩溃尾账丢失 | 不夸大开发账本；商业采用节点权威、预留预算和持久化游标 |
| 开源许可证与闭源/收费设想冲突 | 查固定版本及迁移代码的LICENSE，交付必要源码/构建材料；分进程不自动免除许可义务 |
| 签名账号或SDK/Runner不具备条件 | 第1阶段核实，第5阶段无公证证据则只标内部构建 |

## 11. 程序员无缝交接规则

接手顺序固定为：读本文件当前阶段 → 看 Git 状态及上次变更范围 → 读 shared/contracts 和相关源码 → 运行最近阶段验收入口 → 只做当前未完成项。不得用历史截图替代当前构建的验收。

职责建议：macOS 工程师负责 App/Helper/签名和平台生命周期；Go 工程师负责 Managed Core、API、账本与 NodeAgent；测试/发布负责人负责真实 TUN、节点撤销及安装矩阵。可以由同一人承担，但四类证据（UI、授权、账务、网络）仍分开记录。

每轮结束必须更新下面表格及一段交接条目。实现契约变更应同时更新共享 JSON、生产实现与测试，不能仅在聊天里约定。故障或阻塞要留下重现方法，而不是“应该能用”。

| 阶段 | 状态 | 负责人 | commit / 构建 | 证据与阻塞 |
| --- | --- | --- | --- | --- |
| 1 原生TUN技术验证 | IN_REVIEW（PR 待 CI） | Claude Code 会话 | 分支 `worktree-stage1-native-foundation` | 共享源迁移、骨架、签名/XPC 验证已完成；真实 TUN 实传 BLOCKED（需受控测试节点+root 授权）；Helper 实际 SMAppService 注册待用户在系统设置批准；节点控制能力待用户确认 |
| 2 账号与节点接入 | NOT_STARTED | 待分配 | — | 依赖阶段1 |
| 3 开发计量闭环 | NOT_STARTED | 待分配 | — | 依赖阶段2，仍是client_reported |
| 4 节点权威收费闭环 | NOT_STARTED | 待分配 | — | 必须具备真实节点管理能力 |
| 5 签名发布与回归 | NOT_STARTED | 待分配 | — | 所有硬门槛及网站外部发布条件 |

每次交接追加：

```text
阶段 / 日期 / 负责人：
基线commit与既有未提交改动：
本轮实际改动文件及功能：
契约/迁移/配置变化（不含秘密）：
执行命令、实际测试数、通过/失败/BLOCKED：
构建版本、机器/系统/core版本及脱敏证据路径：
未解决问题与可复现步骤：
下一位从哪个阶段/清单项开始、前置条件：
停止/回退方法及数据安全注意事项：
```

### 阶段 1 交接条目（第 1 轮）

```text
阶段 / 日期 / 负责人：阶段 1 / 2026-10-08 / Claude Code 会话
基线commit与既有未提交改动：origin/main ad9795f。主目录工作区的未提交改动（旧 ci.yml 修改、FlClash 删除、README、docs/macos-client-build.md、scripts/build-macos-dmg.py、test-macos-dmg.py）未被带入本分支，也未被覆盖；它们是旧 Flutter DMG 流程，已由本分支的新流程取代，合并后可丢弃。
本轮实际改动文件及功能：
  - shared/nodepolicy、shared/contracts/native-v1.json 由 FlClash 迁出；scripts/sync-backend-shared.py 与 backend/shared-sources.json 改以 shared/ 为规范源；backend 生成副本字节不变。
  - 删除 FlClash/（含 Flutter/Rust/Android/Helper）；旧 Go 托管核心整体迁至 native/legacy-core（仅为保住 managed 账本与 Mihomo 真流量验收测试，阶段 2 由新 Core 重新实现后删除）；Mihomo 子模块迁至 native/third_party/Clash.Meta（仍锁定 70f0570…）。
  - native/core：Go 最小 Core（长度前缀有界 JSON IPC、Hello/Shutdown、严格解码）。
  - apps/macos：SwiftPM（非 xcodeproj，见偏差）SwiftUI App + Helper(XPC 监听/调用方签名要求/单连接/控制台用户) + 共享库(环境解析、签名策略、WorkerStore 校验与安装、帧编码)。
  - native/tunpoc：测试专用 TUN PoC（独立模块，不入包）。
  - scripts/build-native-macos.py、test-native-macos.py、native_env.py、test-native-env.py；CI 删 Flutter/Rust，新增 native-core 与 macos 作业。
契约/迁移/配置变化（不含秘密）：新增 shared/contracts/ipc-v1.json、env-v1.json（Go/Swift/Python 共用）；apps/macos/.env.example 仅三个公开键；后端 API 与数据库无变化。
执行命令、实际测试数、通过/失败/BLOCKED（本机 macOS 26/arm64、Xcode 27.0、Swift 6.4、Go 1.26.6）：
  - test-native-macos --suite unit：Go IPC 18、nodepolicy 5、go vet、Python 环境契约 3、Swift 15，全部 pass。
  - --suite bundle：Debug 包构建+codesign 校验；Helper(--self-test-root，仅 DEBUG 编译)安装已验证 Worker→静态签名校验→Go Core Hello→第二次启动被拒；篡改字节、同哈希但不同签名身份、版本字符串路径穿越均被拒，6/6 pass。
  - --suite tun：BLOCKED（未授权/非 root/无测试节点）——这是正确行为，不是通过。
  - 回归：python3 scripts/test-native.py（隔离 PostgreSQL 后端全量）PASS；test-backend-shared、test-backend-standalone PASS；legacy-core go test ./... PASS。
构建版本、机器/系统/core版本及脱敏证据路径：artifacts/native-macos/<run-id>/（不提交）；Mihomo 70f0570405c3c2c47bb113b88db95006d239b346。
未解决问题与可复现步骤：
  1. 真实 TUN 实传未执行：需要一个测试 SS 节点（主机/端口/密码）和你同意在这台 Mac 上临时改路由/DNS，然后按 docs/macos-tun-poc.md 运行。
  2. SMAppService.daemon 注册/XPC 伪调用方拒绝/取消授权不能创建 TUN：需要在系统设置“登录项”里批准，未在无人值守下执行。ad-hoc 签名能否通过注册、以及 Team ID 级校验，须用 Developer ID 重测。
  3. 节点控制能力（阶段 4 前置）：尚不知道节点服务器是否由你管理。倾向评估 Xray-core 适配（VLESS+明确验证的 SS），但其动态用户/统计/断开存量连接能力未核验。
  4. 偏差：用 SwiftPM + 脚本组装 .app，而非 xcodeproj（CI 可复现、无需 xcodegen）；阶段 5 做公证/DMG 时再评估是否转 Xcode 工程。
  5. 发现：锁定 Mihomo 分支的 executor.ApplyConfig 不启动监听器，TUN 须显式 listener.ReCreateTun（已写入 docs/macos-tun-poc.md）。
  6. 构建基线：CI 沿用 macos-15 + Xcode_26.3 + Go 1.26.6，以 PR 的 CI 结果为准。
下一位从哪个阶段/清单项开始、前置条件：先完成阶段 1 剩余项（上述 1、2、3），再进入阶段 2（Core 实现 v1 登录/session/nodes/config，并把 legacy-core/managed 迁入 native/core 后删除 legacy-core）。
停止/回退方法及数据安全注意事项：本阶段不改后端数据库；tunpoc 异常退出的恢复步骤见 docs/macos-tun-poc.md；回退本 PR 即恢复旧结构（FlClash 目录在版本历史中）。
```

当前交接结论：**阶段 1 代码部分已完成并待 CI；真实 TUN 实传、Helper 真机注册、节点控制能力三项仍需用户配合。**（以下为原勘察结论，保留作历史记录。） 建议下一位先完成共享源码脱钩与真实特权 TUN PoC，同时核实节点控制能力；不要先重做网站，也不要继续修补旧 FlClash UI。

## 附录 A. 源码依据与核验入口

以下路径在编制时已读取；行号仅定位勘察版本，后续应随改动更新。冲突时以源码/共享契约及新增实测证据优先，而不是滞后叙述。

| 结论 | 依据 |
| --- | --- |
| 当前栈 | [frontend/package.json](frontend/package.json):15–35；[backend/go.mod](backend/go.mod):1–17 |
| 网页/原生/管理路由分离 | [server.go](backend/internal/api/server.go):79–114 |
| 不透明 token、真实套餐/授权窗口、现有计量 | [native.go](backend/internal/api/native.go):26–53、55–165、177–265、307–431 |
| 元数据长轮询，读目录不更新last_seen | [node_watch.go](backend/internal/api/node_watch.go):39–136 |
| 单节点秘密、倍率、目录hash改变配置 | [node_catalog.go](backend/internal/api/node_catalog.go):19–25、47–101 |
| 累计计量/幂等账本与整数范围 | [billing/client.go](backend/internal/billing/client.go):8–35；[model/native.go](backend/internal/model/native.go):5–47 |
| 生产保护与env现状 | [config.go](backend/internal/config/config.go):30–114 |
| 固定内核策略源 | [nodepolicy/policy.go](backend/nodepolicy/policy.go):20–21；[托管协议说明](docs/managed-protocols.md) |
| 当前契约副本仍存在 | [native_contract_test.go](backend/internal/api/native_contract_test.go):23–38；[fixture](backend/internal/api/testdata/native_client_contract.json) |
| 旧源依赖仍需迁移 | [sync-backend-shared.py](scripts/sync-backend-shared.py):11–20、57–71；[CI](.github/workflows/ci.yml) |
| 当前非TUN/非权威计费的历史说明 | [README](README.md):5–15、120–126；[计量边界](docs/admin-node-metering.md):30–40 |
| 本机ServiceManagement API确实存在 | 已安装Xcode SDK `ServiceManagement.framework/Headers/SMAppService.h`，读取到 daemon 注册、注销和状态API；这不是签名分发测试 |

外部复核入口（本次未联网验证其最新内容）：[Apple SMAppService](https://developer.apple.com/documentation/servicemanagement/smappservice)、[Apple Network Extension](https://developer.apple.com/documentation/networkextension)、[Apple macOS 公证](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution)、[Mihomo 源码](https://github.com/MetaCubeX/mihomo)。商业节点适配器以选定服务器的固定版本源码及真实能力测试为准；开源许可最终核对该版本 LICENSE，而不是只引用网页摘要。
