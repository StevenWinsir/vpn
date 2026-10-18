# AsterLink iOS 原生客户端开发交接计划

> 编制日期：2026-10-16。状态：**第 1 轮完成（模拟器范围）；第 2 轮代码完成、真机待验（2026-10-16）；第 3 轮完成（模拟器全程，2026-10-17）；第 4 轮完成：模拟器与 harness 范围（2026-10-17），真机计费待验；真机项因无付费 Apple 团队为 BLOCKED**。实际进度见第 15 节。
> 勘察基线：`1c9d64b`（PR #39 合并点，Android 第 6 轮）；Mihomo 固定提交 `70f0570405c3c2c47bb113b88db95006d239b346`（`native/third_party/Clash.Meta`）+ 生命周期补丁 `d425c1a0…`。
> 项目根目录：`/Users/stevenlee/Desktop/vpn`；开发机：Mac mini M4（arm64，32 GiB，macOS 26.6.2，Xcode 27.0 / iOS 27.0 SDK）。
> 规划轮只新增本文件：没有改代码，没有部署，没有连接远端数据库，也没有提交或推送。第 1 轮起的实际进度写在第 15 节。

## 1. 结论与交付边界

**推荐技术栈：Swift 6 + SwiftUI（Observation）做原生 UI；用 `NetworkExtension` 的 `NEPacketTunnelProvider` App Extension 提供系统级 TUN；复用现有 Go Managed Core 和内嵌的固定版 Mihomo，经 gomobile 打成 `AsterCore.xcframework`，在 App 与隧道扩展进程内调用。共 6 轮开发。**

这是 iOS 专属的原生客户端：不套 WebView，不用 Flutter/React Native，也不复活 FlClash。网站（Next.js + Mantine）继续负责管理员的节点 YAML、套餐、倍率和购买续费。Go/Gin/GORM/PostgreSQL 后端仍是唯一的业务控制平面。后端只需要一处小改动：把 `ios` 加进登录平台白名单（`backend/internal/api/native.go:70`，第 1 轮做），接口本身不变。

iOS 的目标闭环与 macOS、Windows、Android 一致：
1. 安装后登录，可以记住密码；
2. 同步套餐和有权使用的线路，用户选线；
3. 首次安装系统 VPN 配置，建立真实 TUN 连接；
4. 显示速率和用量，按管理员设定的倍率扣量；
5. 节点变更、额度耗尽、套餐到期时停止代理。

用户看不到、也导不出节点 YAML、地址或凭据。

### 1.1 三个硬前提（在第 1 轮开始前就要决定）

| # | 前提 | 原因 | 缺失时的后果 |
| --- | --- | --- | --- |
| P1 | **付费 Apple Developer Program 账号**。要上架 App Store 的 VPN，必须以**组织（Organization）身份**注册（需要 D-U-N-S 编码）。 | Network Extension 的 `packet-tunnel-provider` 能力只对付费团队开放，免费的 Personal Team 不能签这项 entitlement。App Review Guidelines 5.4 要求提供 VPN 服务的 App 由组织开发者提交（提交前按当时版本复核）。 | 扩展无法在任何真机上运行。本机 `security find-identity` 实测：**0 个签名身份**。 |
| P2 | **至少一台 iPhone**（iOS 17 以上，最好 iOS 26/27），用 USB 或同一 Wi-Fi 连接这台 Mac。 | **iOS 模拟器不能运行 Packet Tunnel Provider**（Apple 历来如此；保存 VPN 配置会报 `NEConfigurationErrorDomain`）。第 1 轮会在 iOS 27 模拟器上实验一次并记录结果。 | 真实 TUN、DNS、IPv6、计费、生命周期这几项真机验收全部为 BLOCKED，只能交付“模拟器可验证的部分 + 代码完成”。 |
| P3 | **磁盘空间 ≥ 30 GiB**。 | 本机根分区只剩约 **13 GiB**。iOS 27 模拟器运行时约 8–10 GiB，DerivedData 和 xcframework 还要几 GiB。目前 `~/.android` 占 7.4 GiB，Android SDK 占 17 GiB。 | 装不上模拟器运行时，第 1 轮无法开始。由用户决定清理哪些（例如已完成验收的 Android 镜像），本计划不擅自删除。 |

**这对“模拟器中完成测试”意味着什么：** 模拟器能完成 UI、登录、钥匙串、Go Core、目录同步、计费账本（经“进程内注入”测试线束，见 8.8）和大部分状态机测试。但 **iOS 系统的 TUN 数据通路只能在真机上验收**。两类证据在报告中分开记录，模拟器的结果不会写成“iOS TUN 已通过”。

### 1.2 交付等级

| 等级 | 达成标准 | 轮次 |
| --- | --- | --- |
| 工程可构建 | xcframework + App + 隧道扩展可复现构建；模拟器启动并显示 Core 版本；CI 绿 | 第 1 轮 |
| 内部功能版 | 真实账号、线路同步、真机真实 TUN（SS/VLESS）、开发计量与倍率扣量闭环、移动端生命周期 | 第 2–5 轮（真机项依赖 P1、P2） |
| 可收费发布候选 | TestFlight/App Store 构建、发布审计、真机矩阵、受管节点权威计量、审核与法务门槛 | 第 6 轮；任一硬门槛未过就保持 `trusted_release=false`，只发内部 TestFlight |

“6 轮”指 6 个有明确验收点的开发批次，不保证 6 次对话不需要修复。每轮的失败项在本轮内修复。

**为什么是 6 轮，而不是更少：** iOS 和 Android 一样没有特权 Helper，Go 的会话、计量、grant 和授权闸门也都已验证过，这些都减少工作量。但 iOS 多出两件 Android 没有的事：
1. **App 与隧道扩展是两个进程**，而扩展只在 VPN 开着时存在。因此需要一个“会话所有权交接”协议（第 4.2 节），否则会出现两个账本所有者。
2. **扩展有 50 MiB 的内存上限，模拟器又跑不了扩展**，内存和数据通路都必须靠真机证据。

## 2. 已有工程与 PR 进程：从现状接续

### 2.1 本次核验范围

已完整阅读三份交接书：
- `handoff_app.md`（723 行，macOS 5 阶段及真机修复）
- `handoff_app_windows.md`（896 行，Windows 8 轮）
- `handoff_app_android.md`（941 行，Android 6 轮及维护轮）

用 `gh pr list` 核对了 PR #1–#39 的状态（#4、#6、#8、#9 关闭未合并，其余均已合并），并读了 #28、#39 的说明。

核对过的源码：
- `native/core/mobile/mobile.go`（gomobile 绑定面）
- `native/core/internal/{engine,network,managed,meter}` 的构建标签与平台文件
- `managed/platform.go`
- `meter.Begin` 的重基线逻辑
- `backend/internal/api/native.go` 的平台白名单
- `shared/contracts/*.json`
- 固定 sing-tun `v0.4.22` 的 `tun_darwin.go`（`FileDescriptor` 路径）
- Mihomo `listener/sing_tun/server.go` 的 fd 处理
- `apps/macos/Sources`（Swift 代码，可移植部分见 3.3）
- `docs/android-ui.md`

本机工具链实测：
- Xcode 27.0（27A266a），装有 iOS 27.0 与 iOS Simulator 27.0 SDK，模拟器 SDK 里有 `NetworkExtension.framework`。
- **没有安装任何模拟器运行时**（`simctl list runtimes` 为空）。
- Go `1.26.6 darwin/arm64`；未安装 gomobile（Android 用的 `x/mobile 21e291c` 已经以 tool 指令锁在 `native/core/go.mod`）。
- 签名身份 0 个，描述文件 0 个，磁盘剩余约 13 GiB。

### 2.2 历史 PR 对 iOS 的实际意义

| PR | 内容 | iOS 接续方式 |
| --- | --- | --- |
| #1、#2、#5、#7 | 管理员节点目录、15 种协议白名单、删除同步、用户线路列表 | 原样复用；协议“能解析”≠ iOS 实传通过 |
| #3、#10 | 后端独立构建、去 FlClash、共享契约 | iOS 读取同一份 `shared/contracts`，不建契约副本 |
| #11、#12 | 登录/Keychain/状态机、TUN、唯一 Meter、租约、尾账 | Go 状态机共用；macOS 的 Swift `CredentialStore`/`CoreModels` 可作为移植起点 |
| #13、#26 | capabilities、grant、NodeAgent、节点权威计量、grant 验收工具 | `connectGranted` 已完整，iOS 只消费；B04 用本地 NodeAgent 复测 |
| #16 | Mihomo 日志污染 stdout IPC | iOS 是进程内调用，不经管道；但日志同样不得进入 `os_log`/诊断中的秘密 |
| #17 | VLESS 节点无 IPv6 出口导致“已连接但没网” | **iOS 最容易重犯**：NE 不配 IPv6 时 IPv6 会走物理网卡（泄漏）。改用“IPv6 进隧道 + 引擎早期拒绝”（8.4） |
| #18 | 系统 DNS 不经隧道 | NE 的 `NEDNSSettings(matchDomains:[""])` 接管系统解析器；仍需测 Safari、加密 DNS、私有 DNS 描述文件 |
| #19 | 首次授权引导、界面不刷新 | iOS 首次“添加 VPN 配置”的系统弹窗必须有可见引导，批准后自动继续 |
| #20–#22、#28 | 平台构建标签拆分、拒绝静默占位、`ipc.HandleOne`、gomobile 绑定 | **同一套做法照搬**：GOOS=ios 同样隐含 `darwin` 标签（见 2.3） |
| #23、#36 | Windows/Android SessionController（代次、action_error、静默重登一次、内核丢失） | Swift `SessionController` 按 Android Kotlin 版（最新语义）移植 |
| #24、#37 | billtest + trafficsink、Android 计费验收世界 `TestAndroidBillingWorld` | 后端世界改为平台无关后给 iOS 复用；模拟器可直连 Mac 回环，无需 `adb reverse` |
| #29、#30 | Android fd 模式 TUN、`tun-spec-v1`、sing-tun NAT goroutine 泄漏补丁 | iOS 同样是“宿主交 fd”模式；补丁对 darwin/ios 同样生效；T-CYCLES 零增长门禁沿用 |
| #32、#34 | NodeAgent VLESS XUDP、目标空闲隔离 | 已在节点侧修复，iOS 无需改动 |
| #35、#37 | 停连原因保留到下一次用户操作 | iOS UI 依赖此行为显示“为什么断开” |
| #38 | `ResumeGate` 授权闸门、`SetForeground` 后台暂停目录轮询 | **iOS 直接选用 ResumeGate**（息屏/睡眠同样常见），前后台提示由 App 发给扩展（8.6） |
| #39 | Android 签名发布、release 审计、网站 `/download` 页 | 审计思路复用；iOS 分发走 TestFlight/App Store，不是直链安装包 |

`handoff_app.md` 前部的“计划新增”有历史残留，应以末尾的交接追加和当前源码为准。NodeAgent/grant 已经实现，不要重写。

### 2.3 本次源码核查发现的 iOS 必改点

| 位置 | 现状 | iOS 要求 |
| --- | --- | --- |
| `managed/platform.go` | `runtime.GOOS` 为 `ios` 时落到 `default` → 报 `"linux"` | 增加 `ios` → `"ios"`；单测覆盖 |
| `backend/internal/api/native.go:70` | 白名单只有 android/macos/windows/linux | 增加 `"ios"` + 后端测试 + `native-v1.json` 增一个案例；**demo 服务器要重新部署后，iOS 才能在那里登录** |
| `network/network_darwin.go`（`//go:build darwin`） | **GOOS=ios 也满足 `darwin` 标签**，会把 `/sbin/route`、`os/exec` 链进 iOS（沙箱内不可执行） | 改为 `darwin && !ios`，新增 `network_ios.go`（宿主网络快照、可选的按物理接口绑定） |
| `engine/platform_unix.go`、`sysdns.go`、`ipv6_route.go`（`!windows && !android`） | 同样会被 ios 匹配：`networksetup`、`route -reject`、`/Library` 路径 | 标签加 `!ios`；新增 `engine/{platform,sysdns,ipv6}_ios.go`（第 1 轮先让 connect 明确返回 `tun_unavailable`，不链接空实现冒充成功） |
| `engine/tunfd_unix.go`（`!windows`） | Android 的 fd 所有权逻辑，ios 也会编译进来 | 复用；但 iOS 交给 Go 的必须是 `dup()` 后的 fd（NE 持有原 fd，见 8.2） |
| `mobile/mobile.go` | 注释与 `Platform` 面向 Android；`Start` 固定 `ResumeGate` | Platform 接口不改（Kotlin 不受影响）；iOS 另外需要 `SetResolver`、`ExportSession`/`AdoptSession` 等新导出函数（4.2、8.3） |
| `shared/contracts/tun-spec-v1.json` | `ipv6:false` 的语义是“Android 按地址族阻断” | iOS 语义不同（必须把 IPv6 收进隧道再拒绝），**新增 `tun-spec-ios-v1.json`**，不改 Android 夹具 |
| `engine.Verify`（经 TUN 的真实探测） | 依赖“Core 自己未 protect 的流量会进入 TUN” | iOS 上**隧道扩展进程自身的流量默认不进自己的隧道**（第 2 轮真机核实），所以 Verify 改为两段（8.5） |
| Mihomo `log` 包 | 会写 stdout | iOS 无管道；扩展里 stdout 指向 /dev/null；诊断经脱敏后才进入 `os_log` |

## 3. 技术栈选择与 M4 开发方式

### 3.1 明确选择

| 层 | 选择 | 理由与约束 |
| --- | --- | --- |
| 语言 / UI | Swift 6（Xcode 27 工具链），SwiftUI + Observation（`@Observable`），少量 UIKit 桥接（`SFSafariViewController`、触感反馈） | Apple 主推的原生 UI；声明式、易做深色模式、动态字体和动效；不用 WebView/Flutter/RN |
| 最低系统 | **iOS 17.0**（`@Observable` 需要 17；`fetchLastDisconnectError` 需要 16）。主测 iOS 27 模拟器，真机以手头设备为准 | 第 1 轮锁定；若用户设备更旧再讨论，不为旧系统牺牲架构 |
| 架构 | 单 `WindowGroup`，`TabView`（连接/线路/我的）+ `NavigationStack`；MVVM 单向数据流：UI 只渲染 Core 快照 | 与 macOS/Windows/Android 同构 |
| 业务包 | 本地 Swift Package `AsterLinkKit`（快照解码、SessionController、错误文案映射、格式化、env 校验、交接票据），App 与扩展共用 | 业务逻辑可在模拟器上单测；不改 `apps/macos` 的 SwiftPM，避免 macOS 回归 |
| VPN | `NETunnelProviderManager`（App 侧配置与启停）+ `NEPacketTunnelProvider`（扩展 `AsterLinkTunnel`） | 系统级 TUN，无需越狱；用户首次允许添加 VPN 配置 |
| 代理/业务内核 | 现有 `native/core` + 固定 Mihomo，**`gomobile bind -target=ios,iossimulator`** → `AsterCore.xcframework` | 会话、计量、grant 只有一份；Android 已证明 gomobile 与固定 Mihomo 兼容；sing-tun darwin 已支持 `FileDescriptor` |
| 序列化 | `Codable`，账务字段一律 `Int64` | 超过 2^53 的 units 必须精确；禁止经 `Double` |
| 本地存储 | Keychain（记住密码、device_id）；`UserDefaults` 只存非敏感偏好（主题、记住的邮箱） | 不保存 token；不开 iCloud 钥匙串同步 |
| 网页入口 | `SFSafariViewController` | 注册、找回密码跳网站；**购买入口受 App Store 规则约束**（见 14） |
| 图表 | Swift Charts（系统框架） | 60 秒速率曲线，有界采样，不引入第三方 |
| 通知 | UserNotifications（扩展在后台停连时发本地通知） | 与 Android N02 等价；通知权限被拒时 VPN 仍工作 |
| 本地化 | String Catalog（`.xcstrings`），zh-Hans 为默认语言，同步建立英文 | 状态词与 Android 一致 |
| 工程描述 | 提交 `AsterLink.xcodeproj`（Xcode 16+ 的同步文件夹，pbxproj 改动小）+ `.xcconfig` | App Extension、entitlements、描述文件都需要 Xcode 工程；不引入 XcodeGen/Tuist 等第三方工具 |
| 测试 | Swift Testing/XCTest（单元，模拟器）、XCUITest（UI 与截图，模拟器）、Go 测试（宿主 + iOS 模拟器交叉运行）、真机 XCUITest（`xcodebuild test -destination id=<iPhone>`） | 编译、UI、网络、账务四类证据分开记录 |
| 构建/测试入口 | Python 脚本 `build-native-ios.py`、`test-native-ios.py`（风格与另三端一致），底层是 `xcodebuild`/`simctl`/`devicectl` | CI 与本机同一套命令 |
| 分发 | 内部用 TestFlight；正式上架 App Store（组织账号、非中国大陆区） | iOS 没有官网直链安装；企业证书分发给公众违反 Apple 条款，不采用 |

**否决的备选：**
- 把 Mihomo 作为独立可执行文件放进扩展再 fork：iOS 禁止 fork/exec。
- 手写 `c-archive` + C 桥接：可以去掉 x/mobile 依赖，但工作量更大。只有第 1 轮证明 gomobile 对 ios 目标有阻塞时，才作一次记录在案的决策切换。
- 用 `NEAppProxyProvider`/`NEDNSProxyProvider` 代替 packet tunnel：不是 TUN，与另三端原理不一致。
- 改用 sing-box/libbox：会出现第二套内核与计量口径。

### 3.2 Mac + 模拟器能做什么，不能替代什么

| 工作 | M4 + iOS 模拟器 | 必须真机 / 外部条件 |
| --- | --- | --- |
| 编写 Swift/Go、构建 xcframework/App/扩展、单测、XCUITest、截图 | 完整（模拟器构建不需要签名身份） | — |
| 登录、记住密码、套餐线路同步、空态、管理员后台改动反映（A01–A04） | 完整：模拟器直接访问 Mac 回环上的隔离后端（`http://127.0.0.1:<port>`，符合 env 契约中“HTTP 仅限回环”） | demo 服务器手工确认需用户账号 |
| Go Core 的连接状态机、计量、倍率、ACK 丢失、grant 闭环 | **可以**：调试专用“进程内注入”线束在模拟器 App 进程里运行真实 Core + 真实 Mihomo + 本机 testnode/sink/NodeAgent（8.8），只缺 utun 入口 | 真 TUN 入口的计费对账 |
| NE 扩展启动、`setTunnelNetworkSettings`、utun fd、系统 DNS、IPv6、Safari 浏览 | **不能**（模拟器不运行 Packet Tunnel） | **真机 + 付费账号** |
| Darwin 内核上的“宿主交 fd”路径 | 可选：macOS 上用 root 测试宿主创建 utun 并按 TunSpec 配置后交 fd（`asterlink-tuntest --fd-host`，需 sudo 并断开公司 VPN）。它只证明 Go fd 路径，不代表 NE | — |
| 扩展 50 MiB 内存上限 | 只能**近似**：在模拟器进程内跑同一 Core，压测并读 `phys_footprint` | jetsam 只在真机执行，必须真机压测 |
| 息屏睡眠、Wi-Fi↔蜂窝、飞行模式、被其它 VPN 抢占、从设置/控制中心关闭 | App 侧逻辑可用假 provider 测；Go 闸门逻辑有单测 | 真机 |

**测试网络注意：** 模拟器流量走 Mac 的网络。做真实节点用例时必须**断开公司 VPN 并退出 macOS 版 AsterLink**（与另三端同一前提）。真机测试时 iPhone 用自己的 Wi-Fi/蜂窝，不受 Mac 公司 VPN 影响；但如果访问 Mac 上的 testnode/sink，要确认 Mac 的局域网地址对手机可达。

### 3.3 可以直接移植的现有代码

- `apps/macos/Sources/AsterLinkShared/CoreModels.swift`：快照 DTO 是起点，需要补齐 `gate`/`last_gate`、`tunnel_health:"gated"` 等 Android 第 5 轮新增字段。
- `ClientEnvironment.swift`：env-v1 校验，可直接移植。
- `CredentialStore.swift`：Keychain 封装，改为 `kSecAttrAccessibleWhenUnlockedThisDeviceOnly`。
- **SessionController 以 Android 版为准**（`apps/android/.../session/SessionController.kt`）：macOS Swift 版（282 行）早于 Windows/Android 补充的语义（静默重登一次、内核丢失、闸门、停连原因保留）。
- 视觉令牌与文案：`docs/android-ui.md`、`apps/android/.../ui/theme`、`values/strings.xml`。

## 4. 运行架构：两个进程，同一时刻只有一个会话所有者

### 4.1 进程图

```text
Next.js / Mantine 管理员页面 ── 配置套餐、节点、倍率
          ▼
Gin API / PostgreSQL ── mTLS ── 受管节点 NodeAgent / 转发器
          ▲ HTTPS（扩展/App 自身流量不进隧道，直达物理网络）
          │
┌──── AsterLink.app（普通 App 进程，前台时才运行）──────────────────┐
│ SwiftUI ─ SessionController ─ CoreRouter                           │
│                                 ├─ 未连接：本进程 AsterCore（账户模式：│
│                                 │   login / session / nodes，无引擎）│
│                                 └─ 已连接：sendProviderMessage 转发  │
│                                     ipc-v1 文档给扩展，读回快照        │
│ Keychain（记住密码、device_id）   NETunnelProviderManager（启停）    │
└───────────────────────────┬──────────────────────────────────────┘
          startVPNTunnel + 交接票据（只在内存中经系统 XPC 传递）
┌───────────────────────────▼─── AsterLinkTunnel.appex（NE 扩展）───┐
│ PacketTunnelProvider ─ AsterCore（完整模式：引擎 + Meter + 闸门）  │
│   setTunnelNetworkSettings(TunSpec) → 找到 utun fd → dup → Mihomo  │
│   NWPathMonitor → NetworkChanged；sleep/wake → 闸门；内存预算        │
└──────────────────────────────────────────────────────────────────┘
```

**不变量：Core 仍是 HTTP 会话、配置、租约、Meter 的唯一所有者，而且任何时刻只有一个进程里的 Core 持有该会话 token 并对后端写入。** Swift 不直接调用后端业务 API（网站链接除外），也不接触 token 和 YAML。

### 4.2 会话所有权交接（iOS 新增，第 2 轮实现）

**为什么需要：**
- 扩展只在 VPN 开着时存在，所以未连接时的登录和线路浏览只能由 App 进程的 Core 完成。
- App 在后台会被挂起，所以连接后的计量上报和租约只能由扩展的 Core 完成。

两个 Core 都存在，但必须**严格交接、绝不同时写入**。

| 步骤 | 规则 |
| --- | --- |
| 交出（App→扩展） | 用户点“连接”。App Core 停止保活和目录 watcher，确认无在途请求，然后 `ExportSession()` 生成票据：`{api_base, session_id, token, device_id, selected_node_id, generation, issued_at}`。票据只经 `startVPNTunnel(options:)` 或启动后的首条 provider message 在内存中传递，**不写盘、不进 `providerConfiguration`、不进 App Group、不进日志**。App Core 进入 `released` 状态，只转发请求。 |
| 接收（扩展） | `AdoptSession(ticket)` 先 `GET /session` 核对 `session_id`、套餐与授权，用服务端的 `last_sequence`/累计值作为已确认状态，然后走原有 connect 流程：配置、Meter 首个 ACK、TUN、Verify。现有 `meter.Begin` 已支持“上一段结清 + 服务端一致”时把新引擎的计数重新取基线，无需新偏移逻辑。 |
| 交回（扩展→App） | 用户在 App 内断开：App 发 `release`，扩展停流、结账（`Settle`），在回复中带回票据，再 `cancelTunnel`；App Core `AdoptSession` 后继续保活与浏览，用户保持登录。 |
| 无法交回 | 从设置/控制中心关闭、被其它 VPN 抢占、扩展崩溃、App 已被杀：扩展尽力结账后**丢弃** token（服务端按 idle 超时回收）。下次打开 App 在线重新登录：有记住的密码就静默登录，否则回到登录页。这与另三端“进程重开必须在线登录”一致。 |
| 系统自行启动扩展 | 收到无票据的 `startTunnel`（设置里打开开关、按需连接）时，以“请在 AsterLink App 内连接”失败退出。不启用 On-Demand，不启用“始终开启”（与 Android `SUPPORTS_ALWAYS_ON=false` 一致）。 |
| 退出登录 | 已连接时，App 经扩展执行 `logout`（停流→结账→撤销）后断开；未连接时由 App Core 直接执行。 |

**新增契约 `shared/contracts/handoff-v1.json`：** 包含票据字段、`adopt`/`release` 消息、拒绝案例（session_id 不一致、存在待确认批次、generation 过旧、票据过期、重复 adopt）。Go 与 Swift 用同一份夹具测试。该接口只在 `mobile` 包导出，不进入 ipc-v1，因此桌面端和 Android 的命令集不变。

**为什么不让扩展用密码重新登录：** 那样需要把密码交给扩展，还会产生第二个会话，触发设备上限与账本分段问题。交接 token 只是把同一个会话从一个进程移到另一个进程，账本连续。

### 4.3 App ↔ 扩展的通道

- 通道：`NETunnelProviderSession.sendProviderMessage(_:)`，消息体就是 ipc-v1 的 JSON 文档（与 Android `Call` 相同）。扩展内经 `ipc.HandleOne` 解码，所以没有第二个命令解析器。
- 消息上限：沿用 2 MiB；快照通常只有几 KB。
- 轮询节奏：已连接时 App 前台每 1 s 读一次快照，其它时候 2 s（同 Android）；App 在后台时不轮询。
- 其它规则：同一时刻只有一个在途命令；连接预算 90 s，可取消（取消 = 结束会话，界面如实说明）。
- 扩展不提供任何其它消息类型（不能执行任意 YAML、路径、URL）。

## 5. 目录与模块规划（计划新增，当前不存在）

```text
apps/ios/
  AsterLink.xcodeproj
  Config/            Base.xcconfig、Debug/Release.xcconfig、Generated.xcconfig（由 .env 生成，不提交）
  toolchain.lock.json  # Xcode/SDK/模拟器运行时/Go/x-mobile/最低 iOS 的锁定
  .env.example         # 与另三端相同的三个公开键
  AsterLink/           # App：AsterLinkApp.swift、ui/{theme,components,login,connect,lines,account,onboarding}、
                       #      vpn/TunnelManager.swift（NETunnelProviderManager）、Resources/（xcassets、xcstrings）
                       #      AsterLink.entitlements、PrivacyInfo.xcprivacy
  AsterLinkTunnel/     # 扩展：PacketTunnelProvider.swift、TunnelSettings.swift（TunSpec→NE 设置）、
                       #       UtunDescriptor.swift（找 fd）、PathMonitor.swift、MemoryBudget.swift、
                       #       AsterLinkTunnel.entitlements、PrivacyInfo.xcprivacy
  Packages/AsterLinkKit/  # Snapshot、Commands、CoreRouter、SessionController、CredentialStore、DeviceIdentity、
                          #  Formatting、ErrorText、Diagnostics、HandoffTicket、ClientEnvironment + Tests
  AsterLinkUITests/    # XCUITest：截图集、account/billing/lifecycle 驱动
  AsterLinkHarness/    # 仅 Debug 测试目标：进程内注入线束（8.8），Release 构建不包含
native/core/
  mobile/                                  # 现有绑定，新增 iOS 导出函数
  internal/network/network_ios.go
  internal/engine/{platform,sysdns,ipv6,ipv6filter}_ios.go
  internal/managed/handoff.go              # ExportSession / AdoptSession
shared/contracts/handoff-v1.json、tun-spec-ios-v1.json
scripts/
  build-native-ios.py                      # xcframework / app（simulator|device）/ archive
  test-native-ios.py                       # --suite foundation|unit|ui|account|billing|device-tun|device-billing|lifecycle|release
  package-native-ios.py                    # archive、导出 IPA、许可材料、release-info、SHA256SUMS
  ios/                                     # simctl/devicectl 辅助、内存采样
.github/workflows/ios.yml
docs/ios-{foundation,tun-design,ui,billing-acceptance,lifecycle,release}.md
artifacts/native-ios/<run-id>/             # 不提交：报告、截图、xcresult、IPA
```

Bundle ID 建议：App 用 `<反向域名>.asterlink`，扩展用 `<反向域名>.asterlink.tunnel`。**第 1 轮在用户的 Apple 团队里注册后再锁定**；最好用用户拥有的域名（如 `cn.hyshentou`），不擅自占用 `com.asterlink`。

## 6. API、配置与凭据：复用已有契约

### 6.1 原生接口（不新增）

前缀 `https://demo.hyshentou.cn/api/v1/client`，全部由 Go Core 调用：

| 接口 | 用途 |
| --- | --- |
| `POST /login` | `platform=ios` 由 GOOS 决定，不接受 UI 传入 |
| `GET /session` | 刷新授权与余额 |
| `GET /nodes?revision=` | 单个长轮询，只观察目录，不续授权 |
| `GET /config?node_id=&last_sequence=` | 获取选中节点配置 |
| `POST /traffic` | 开发计量上报，只由唯一的 Meter 发送 |
| `POST /logout` | 退出登录 |
| `GET /capabilities` | 能力协商 |
| `POST /grants`、`GET /grants/:id`、`POST /grants/:id/revoke` | 节点权威计量的授权路径 |

包装层、错误码、日期与整数格式以 `native-v1.json`、`native-v2.json` 为准。业务时限只用服务端返回值（后端 `.env` 的 `CLIENT_REPORT_INTERVAL_SECONDS=60`、`CLIENT_LEASE_SECONDS=90`、`CLIENT_IDLE_TIMEOUT_SECONDS=180`、`CLIENT_NODE_WATCH_SECONDS=5`）。

### 6.2 `.env` 的归属

`frontend/.env`、`backend/.env` 各管各的。新增 `apps/ios/.env`（不提交）和 `.env.example`：

```dotenv
APP_ENV=development
API_BASE_URL=https://demo.hyshentou.cn/api/v1/client
WEBSITE_BASE_URL=https://test.hyshentou.cn
```

它是**构建时输入**：`build-native-ios.py` 按 `env-v1.json` 的白名单规则校验后生成 `Generated.xcconfig`，再写入 App 与扩展两个 `Info.plist`，扩展只信任自己包内的值。改了要重新构建、签名、安装。

校验规则：
- 拒绝重复键、未知键、shell 展开、URL 内凭据。
- Release 必须 HTTPS，且 ATS 不加任何例外。
- 开发时连本机后端写 `http://127.0.0.1:<port>/api/v1/client`（模拟器共享 Mac 回环）。只有 Debug 构建的 ATS 允许回环 HTTP，Release 审计会检查这一点。

数据库账号、节点密码、NodeAgent 私钥、签名证书/描述文件、App Store Connect API 密钥、用户密码、token 一律不进入 `.env`、源码或 IPA。iOS 不连接 PostgreSQL。对话中出现过的数据库密码与节点凭据在对外测试前必须轮换，本文件不复制它们。

### 6.3 记住密码、设备 ID

- **记住密码**：只在用户打开“记住密码”时保存为 Keychain 通用密码项。`service=<bundle>.credentials`，`account=SHA-256(API base + 规范化邮箱)`，`kSecAttrAccessibleWhenUnlockedThisDeviceOnly`，不同步 iCloud。
  - 服务器返回 `invalid_credentials` 时删除该项；“退出登录”删除密码和记住的邮箱；普通退出保留。
  - 保存失败只提示，不降级为明文。
- **不保存 token**：App 重启后在线自动登录。
- **device_id**：首次启动生成随机 UUID，存 Keychain（`ThisDeviceOnly`）。iOS 删除重装后 Keychain 项通常保留，所以 device_id 稳定，避免 Android 那种重装后触发 `device_limit` 的问题；换新手机不迁移。不读 IDFV/IDFA/MAC。
- **不需要 App Group**：token 不共享，构建配置各自内置；扩展停连原因由 `NEVPNConnection.fetchLastDisconnectError` 交给 App。

## 7. Go Core 的 iOS 平台层

| 能力 | iOS 实现 |
| --- | --- |
| 登录平台 | `ClientPlatform()` → `ios` |
| 构建 | `gomobile bind -target=ios,iossimulator -iosversion 17.0 -trimpath -ldflags "-s -w -X …version"`，x/mobile 沿用 `21e291c`；产物写入 `core-build.json`（Core 版本、Mihomo 提交、补丁哈希、每个切片的 SHA-256），构建脚本拒绝身份不符的旧框架 |
| 出站防环 | 扩展进程自身的套接字默认不进入本隧道（第 2 轮真机核实）；`Protect` 恒返回 true；如核实失败，再用 `IP_BOUND_IF` 绑定 `NWPath` 给出的物理接口索引 |
| 控制面与引导 DNS | **风险**：隧道设置 DNS 后，扩展内经系统解析器（getaddrinfo）的查询可能落到 198.18.0.2 并形成回环。方案：`network.SetResolver(host)`，宿主用 `DNSServiceGetAddrInfo` 按物理接口索引做限定范围查询；Go 侧 API 客户端与 Mihomo 的 `proxy-server-nameserver` 都经它或经现有 `tcp://1.1.1.1` 直连。第 2 轮二选一并记录 |
| 物理网络与签名 | `NetworkSnapshot()` 来自 `NWPathMonitor`：接口类型、是否 expensive/constrained、IPv4/IPv6 是否可用、物理接口索引，签名规则与另三端相同 |
| 竞争 VPN | 系统保证同一时刻只有一个 VPN；其它 VPN 抢占时扩展收到 `stopTunnel(.superceded)` → 停流结账 |
| TUN 创建 | Core 先过闸门（会话、配置、Meter 首个 ACK），再调 `OpenTun(spec)`：Swift 应用 NE 设置 → 找到 utun fd → `dup` 后返回；Go 写入模板 `tun.file-descriptor`，`ReCreateTun` 后确认监听器在读该 fd |
| 系统 DNS | 由 NE 的 `NEDNSSettings` 接管，`sysdns_ios.go` 不改任何系统设置，没有崩溃残留 |
| 时钟与挂起 | 选用 `ResumeGate`（Android 第 5 轮实现）；NE 的 `sleep`/`wake` 回调与墙钟/单调钟偏差一起触发关闸、重验 |
| 内存 | 扩展启动时设置 `debug.SetMemoryLimit`（初值约 20–24 MiB 堆）、`SetGCPercent`（初值 50–80），并定期 `FreeOSMemory`；具体值第 2、5 轮按真机 `phys_footprint` 实测定 |

## 8. TUN、DNS、生命周期与 iOS 特有问题

### 8.1 固定运行模板（iOS 变体）

```yaml
tun:
  enable: true
  stack: system            # 第 2 轮真机先验证；不行才经一次记录在案的决策改 gvisor（需评估内存），不得失败后自动回退
  file-descriptor: <dup 后的 fd>
  auto-route: false        # 路由由 NEPacketTunnelNetworkSettings 负责
  auto-detect-interface: false
  inet4-address: [198.18.0.1/30]           # 与 NEIPv4Settings 同一常量（tun-spec-ios-v1）
  inet6-address: [fdfe:dcba:9876::1/126]   # iOS 始终设置（见 8.4）
  mtu: <与 NE 设置相同；第 2 轮按内存与吞吐在 1500–9000 间定值>
  dns-hijack: [any:53]
dns: {enable: true, enhanced-mode: fake-ip, fake-ip-range: 198.18.0.1/16, nameserver: [tcp://1.1.1.1#node, tcp://8.8.8.8#node], ...}
rules: [MATCH,node, MATCH,REJECT]
```

管理员 YAML 只贡献一个代理定义：没有监听器、controller、provider、脚本、文件路径，也没有 geo 数据库（省内存）。`BuildConfig` 增加 `goos == "ios"` 分支，沿用“在任意主机上都能测各平台模板”的写法。

### 8.2 NEPacketTunnelNetworkSettings 与 utun fd

- **IPv4**：`NEIPv4Settings(addresses:["198.18.0.1"], subnetMasks:["255.255.255.252"])`，`includedRoutes=[.default()]`。
- **IPv6**：`NEIPv6Settings(addresses:["fdfe:dcba:9876::1"], networkPrefixLengths:[126])`，`includedRoutes=[.default()]`。**始终包含**，原因见 8.4。
- **DNS**：`NEDNSSettings(servers:["198.18.0.2"])`，`matchDomains=[""]`（接管全部域名）。
- **MTU** 与模板一致；`tunnelRemoteAddress` 用固定占位地址，不写节点地址（避免在系统 VPN 设置里暴露节点）。
- **不启用** `includeAllNetworks`：开启后扩展自身流量也要进隧道，AirDrop/CarPlay 等也会受影响，复杂度高。代价是“VPN 启动前已存在的长连接可能不迁移”。在已知限制中如实说明，第 5 轮测量影响。`excludeLocalNetworks` 保持默认，局域网直达，不算公网泄漏。
- **找 utun fd**：`setTunnelNetworkSettings` 完成后，遍历本进程 fd，找出 `getsockopt(SYSPROTO_CONTROL, UTUN_OPT_IFNAME)` 能得到 utunN 名、且控制名为 `com.apple.net.utun_control` 的那个。这与 WireGuard for iOS 的做法相同，不用私有 KVC。常量在 Swift 里自行定义，单测覆盖“找不到”和“多个候选”的情况。
- **fd 所有权**：NE 持有原 fd，交给 Go 的是 `dup()` 副本，由 Go/sing-tun 负责关闭，`releaseTun` 不得二次关闭（复用 `tunfd_unix.go` 的 inode 身份判断）。这样换线、闸门重开时可以重建 Mihomo 监听器而不破坏 NE 接口。第 2 轮真机验证关闭副本后 NE 接口仍可用。

### 8.3 网络行为验收要求

| 项目 | 要求 |
| --- | --- |
| 真 TUN | 不设置任何 HTTP 代理，Safari 与任意 App 的 TCP/UDP 都经选中节点；用节点计数或受控 sink 证明路径 |
| 防环 | 节点外层连接、API、引导 DNS 不进入自身隧道（T05-loop 是硬性用例） |
| DNS | 系统解析器 → 198.18.0.2 → any:53 劫持 → fake-ip；分别测 Safari、系统 DNS 设置、已安装加密 DNS 描述文件的设备（记录行为） |
| UDP/QUIC | `udp:true` 实传（Safari HTTP/3）；`udp:false` 必须被 `MATCH,REJECT` 拒绝，绝不 DIRECT |
| 控制面 | VPN 运行中扩展仍能经物理网络访问 API；API 不可达时按租约关闸/停流 |
| 停止清理 | 设置 → VPN 显示未连接；系统状态栏无 VPN 图标；扩展进程退出 |
| 进程被杀 | 扩展被 jetsam/崩溃时，系统撤下隧道，无孤儿转发；App 显示原因，并要求在线登录 |

### 8.4 IPv6：iOS 必须“收进隧道再拒绝”

Android 的 VPN 不配 IPv6 时，系统直接阻断该地址族。**iOS 不配 `NEIPv6Settings` 时，IPv6 流量会走物理网卡，形成泄漏。**

因此 iOS 始终把 `::/0` 收进隧道，再按节点能力处理：
- **节点能承载 IPv6**（连接前经节点做一次 IPv6 探测，复用 `ipv6.go` 的意图）：完整代理。
- **节点不能承载**：在 `ipv6filter_ios.go` 的 TUN 入口包过滤层，对 IPv6 TCP SYN 立即回 RST、对 UDP 回 ICMPv6 不可达。这一步在 system 栈完成本地握手**之前**发生，所以不会出现 PR #17 的“握手后假死”，Happy Eyeballs 会立刻回退 IPv4。同时 fake-ip 不返回 AAAA。

单元测试用构造的数据包覆盖 SYN/UDP/ICMPv6 三类。真机用 IPv6 网络（多数蜂窝网络有 IPv6）验收 T04；没有 IPv6 上游时记 BLOCKED，不记 PASS。

### 8.5 Verify：两段探测

现有 Verify 依赖“Core 自己的流量进 TUN”，但在 iOS 扩展里这个前提大概率不成立。改为两段，**两段都通过才显示“已连接”**：
1. **扩展内**：经 Mihomo 隧道（进程内）对受控探测地址做真实请求，证明节点握手与转发。
2. **App 内**（App 是普通进程，流量会进入 utun）：用 `URLSession` 的 ephemeral 配置请求同一探测地址，期望得到受控响应（不是任意 200），证明系统路由与 DNS 确实经过隧道。结果回报给扩展。

App 不在前台时（例如闸门在后台重开）只做第 1 段，界面在下次前台时补做第 2 段。第 2 轮真机核实扩展流量是否真的绕过隧道；如果实际会进入隧道，就保留原 Verify 并删除第 2 段。

### 8.6 状态机、闸门与前后台

沿用 Core 状态：`SignedOut → Authenticating → Ready → Preparing → Connecting → Connected → Stopping/Settling`，任意阶段可进入 `Blocked` 或 `Error`，另有 `Gated`（重新验证/等待网络）。iOS 在 UI 层多两个前置步骤：

```text
点击连接 → 是否已安装 VPN 配置？
   否 → 引导页说明 → saveToPreferences → 系统弹窗“添加 VPN 配置”（可能要求输入设备密码）
        → 拒绝：保持 Ready，提示“需要允许添加 VPN 配置” → 允许：继续
→ App Core ExportSession → startVPNTunnel(票据) → 扩展 AdoptSession → connect（闸门 → Meter 首个 ACK → OpenTun → Verify 两段）
→ 只有 Verify 通过才显示“已连接”
```

- **闸门**：`ResumeGate` 原样启用。触发源包括扩展的 `sleep`/`wake`、`NWPathMonitor` 签名变化、租约失效。关闸时拒绝新流、关闭存量流，但保留 utun（流量进隧道但不转发，不泄漏、不白用）；在线重验通过后开闸，服务器拒绝则照常停流结账。
- **前后台**：App 的 `scenePhase` 变为 active/background 时发 `foreground` 提示给扩展。扩展只在“最近 N 秒内收到前台提示”时运行目录长轮询，否则暂停。这样 App 被杀后，扩展会自动进入后台节奏，省电省流量。停连仍由 `/session`、`/traffic` 的服务端裁决强制执行（Android 第 5 轮已证明不依赖 watcher）。
- **切线路、倍率变化、禁用节点**：先停新流和存量流 → 结清旧段 → 新配置 → 保持断开，用户确认后再连。
- **从设置/控制中心关闭**：扩展停流结账后丢弃 token（4.2），App 下次打开时通过 `fetchLastDisconnectError` 显示原因。

### 8.7 内存预算（扩展 50 MiB 硬上限）

公开资料显示，iOS 15 起 Packet Tunnel 扩展的 jetsam 上限约 50 MiB，而且 Apple 说明该值会变化、不应硬编码。Go 系隧道的常见问题是：`SetMemoryLimit` 只限 Go 堆，而 jetsam 按整个进程的内存足迹判断。

对策：
- 模板去掉一切不需要的功能（无 geo、无规则集、无 provider、无日志落盘）。
- Mihomo 连接跟踪与缓冲设上限。
- 用 `MemoryBudget.swift` 每 5 s 读 `phys_footprint`；超过软阈值（例如 38 MiB）主动 GC + `FreeOSMemory`；超过硬阈值（例如 44 MiB）拒绝新连接，直到回落。
- 诊断记录峰值。

验收：真机上同时运行 Safari 多标签 + 大文件下载 + 视频，30 分钟内不被 jetsam（D-MEM）。模拟器上用同一 Core 做近似压测，作为趋势参考，不作为通过证据。

### 8.8 模拟器上的“进程内注入”测试线束（只用于测试）

- **用途**：让计费、倍率、grant、状态机的验收能在模拟器上用**真实 Core + 真实 Mihomo + 真实后端 + 真实节点**完成。
- **做法**：测试 App `AsterLinkHarness` 用 `-tags asterlink_harness` 构建 Core。连接时不打开 utun，改为在进程内把受控客户端的 TCP/UDP 连接交给 Mihomo `tunnel.HandleTCPConn/HandleUDPPacket`。计量仍读 Mihomo 代理链路的累计值，口径与 TUN 相同。
- **边界**：线束缺少 utun 入口、系统 DNS 和 IPv6 路径，这些只在真机验收。报告把这类用例标记为 `harness`，与真机 `device` 分开，不互相替代。
- **隔离**：Release 审计检查 IPA 内不得出现线束符号，Gradle/Xcode 也拒绝把线束框架与正式框架混用（与 Android `verifyCoreAar` 相同的思路）。

## 9. 计费：口径不变

```text
up_delta      = current_upload   - acknowledged_upload
down_delta    = current_download - acknowledged_download
charged_units = (up_delta + down_delta) * bound_rate_permille
remaining_bytes = max(0, floor((total_bytes * 1000 - used_units) / 1000))
```

- 单位：1 unit = 1/1000 字节；500‰ = 0.5×，1000‰ = 1×；上传、下载都计。
- 例子：上传 20 MiB + 下载 80 MiB = 104,857,600 字节。1× 扣 104,857,600,000 units；0.5× 扣 52,428,800,000 units。
- 线路类型：`line_type=direct`（普通线路）仍是远端代理，不是 Mihomo DIRECT。倍率来自管理员的 `rate_permille`，UI 不按线路类型硬编码。
- 显示：用 2^30 字节换算 GB 时注明单位；物理用量与计费用量分开显示。

`client_reported` 是开发账本，防不住改客户端少报。商业收费必须走 `node_reported`（每个 grant 独立凭据、节点计数、预算预留、撤销与租约截止）。iOS 不新建任何账本，Core 原样消费。交接（4.2）只在上一段已结清且服务端一致时发生，所以不会产生“新进程从 0 计数导致漏扣或重扣”。

## 10. 商业化 UI 设计规范（iOS）

### 10.1 设计原则

- 一屏一个主操作（连接），连接状态是视觉中心。
- 数字可信：确认余额与预估余额分开显示，不填假延迟，不做假测速。
- 克制：不用地球、地图、炫光、仪表盘。
- 所有状态同时用文字和图形表达，不只靠颜色。
- 每个系统授权（添加 VPN 配置、通知）都先用一句话解释用途。
- **遵循 iOS 人机界面指南（HIG）**：原生 TabView、导航大标题、系统分组背景、SF Symbols、标准手势与返回，不模仿 Android。

### 10.2 设计令牌（与 Windows/Android 品牌一致）

| 令牌 | 浅色 | 深色 |
| --- | --- | --- |
| Primary（品牌蓝，也作 `AccentColor`） | `#2F6FEB` | `#5B8DEF` |
| 已连接 | `#16A34A` | `#22C55E` |
| 连接中/等待 | `#D97706` | `#F59E0B` |
| 错误 | `#DC2626` | `#F87171` |
| 背景 / 卡片 | `#F6F8FB` / `#FFFFFF`（或 `systemGroupedBackground`） | `#0B1220` / `#121A2B` |
| 主文字 / 次文字 / 分隔线 | `#0F172A` / `#5B6475` / `#E3E8EF` | `#E6EAF2` / `#98A2B3` / `#243049` |

**字体与排版**
- 系统字体（SF Pro + 苹方），不内置字体。
- 全部使用动态字体文本样式：状态大字 `.largeTitle` 半粗、卡片标题 `.headline`、正文 `.body`、辅助 `.footnote`。
- 速率、用量等数字使用 `.monospacedDigit()`，跳动时不抖动。

**布局与控件**
- 8 pt 网格；页面左右 20 pt；卡片圆角 16 pt（`.continuous`），描边 1 pt，几乎无阴影。
- 主按钮为胶囊形，高 52 pt；连接按钮直径 168 pt，外环表示状态；触控目标 ≥ 44 pt。
- 图标用 SF Symbols（`power`、`globe.asia.australia`、`list.bullet`、`person.crop.circle`、`bolt.horizontal`、`arrow.up`/`arrow.down`），并用分层/多色渲染。

**动效与反馈**
- 动效 0.2–0.35 s 弹簧：连接中为环形扫描；连接成功时一次柔和缩放 + 颜色过渡，并伴随 `.sensoryFeedback(.success)` 触感。
- 用户开启“减弱动态效果”时退化为淡入淡出。

**外观与图标**
- 提供“跟随系统 / 浅色 / 深色”。
- App 图标按 iOS 26+ 的分层图标规范准备（浅色、深色、着色）。仓库目前没有品牌素材：第 3 轮先做简洁矢量占位，正式 Logo 由用户提供后替换。启动画面用纯色背景 + 品牌标识。

### 10.3 页面结构（登录后三个标签：连接 / 线路 / 我的）

```text
┌──────────────────────────────────┐
│ AsterLink                 VIP ›  │  ← 导航栏：品牌 + 套餐徽标
│                                  │
│              ╭──────╮            │
│             │   ⏻   │            │  ← 168 pt 圆形主按钮，外环表示状态
│              ╰──────╯            │
│         已连接 · 00:12:31         │  ← 状态文字（VoiceOver 播报变化）
│                                  │
│ ┌ 当前线路 ──────────────────── › ┐│
│ │ 🇭🇰 香港 01   专线 · 计费 1.0×   ││  ← 点按进入线路页
│ └────────────────────────────────┘│
│ ┌ ↑ 1.2 MB/s        ↓ 8.4 MB/s   ┐│  ← 仅连接后出现
│ │ ▁▂▃▅▃▂▅▇▅▃ 60 秒速率（Swift Charts）││
│ │ 本次用量 312 MB · 计费 312 MB    ││
│ └────────────────────────────────┘│
│ ┌ VIP 套餐 · 2026-12-31 到期      ┐│
│ │ ██████████░░░  剩余 68.2 GB     ││  ← 确认余额；预估另起一行小字
│ └────────────────────────────────┘│
├──────────────────────────────────┤
│    ⏻ 连接      ☰ 线路      ◉ 我的   │  ← 原生 TabView
└──────────────────────────────────┘
```

| 页面 | 内容 | 主要操作 |
| --- | --- | --- |
| 登录（入口态） | 品牌标识、邮箱（`textContentType(.username)`，支持“密码”App 自动填充）、密码（可切换可见）、记住密码开关（整行可点）、全宽“登录”、“注册账号/忘记密码”（跳网站）、版本号 | 登录；字段错误显示在字段下方，网络问题用顶部横幅 |
| 首次连接引导（sheet） | 三步说明：为什么要添加 VPN 配置、系统会弹窗、可能需要输入设备密码；“继续”按钮 | 批准后自动继续连接，不需要用户再点一次 |
| 连接 | 上图；未选线时提示“请选择线路”；无套餐/到期/额度为 0 时按钮置灰，显示“前往官网”或说明文字（受第 14 节规则约束） | 连接/断开；连接中可取消 |
| 线路 | `.searchable` 搜索、分段控件（全部/专线/普通）、按地区分组；行内显示地区、名称、“专线 · 计费 1.0×”、计量来源小字、选中对勾；下拉刷新 | 选择线路；连接中切换先弹“将断开并结算”确认 |
| 我的 | 账户卡（首字母头像、邮箱）、套餐卡（到期、余额）、外观、通知权限状态、VPN 配置状态（可移除）、关于（版本、Core 版本、Mihomo 提交）、开源许可与源代码、复制脱敏诊断、隐私政策/用户协议、退出登录（二次确认） | 退出登录 |

**空态与异常态要完整设计：**
- 无套餐（管理员账号也一样）、已到期、额度用尽、无可用线路；
- API 离线、需要更新（`client_upgrade_required`）；
- 拒绝添加 VPN 配置、被其它 VPN 抢占；
- 后台停连（套餐到期/额度用尽/线路停用，经 `fetchLastDisconnectError` 显示原因）；
- 连接失败（错误码对应的中文 + 一个可执行按钮）、等待网络（闸门关闭中）。

**状态词**固定为：未连接 / 正在准备 / 正在连接 / 已连接 / 正在重新验证 / 等待网络 / 正在断开并结算 / 无法连接。错误码到文案沿用 Core 的中文消息，UI 逻辑只看 code。

**可访问性**
- VoiceOver 标签齐全，状态变化有播报；对比度 ≥ 4.5:1。
- 动态字体放大到 AX3 不截断（页面可滚动）。
- iPhone SE（375 pt 宽）与 Pro Max 均可用；横屏与 iPad 可用但不专门优化。

**本版不做**：灵动岛/实时活动、桌面小组件、控制中心控件、Siri 快捷指令、自动选优、测速。这些可作为后续增强，不进入本计划。

## 11. 六轮开发安排与逐轮验收

### 第 1 轮：环境锁定、Go iOS 平台拆分、xcframework、工程骨架、后端 ios 平台

**目标：** 先证明“Go Core + 固定 Mihomo → xcframework → SwiftUI App + NE 扩展 → 模拟器”整条构建链可复现，再写 UI。

**开发内容：**
- **环境**
  - 用户先清出 ≥ 30 GiB 磁盘（P3）。
  - 安装 iOS 27 模拟器运行时（`xcodebuild -downloadPlatform iOS`）；如需最低版本冒烟，另装 iOS 17/18 运行时（空间允许时）。
  - 创建模拟器：`asterlink-iphone`（iPhone 17 Pro 级）、`asterlink-se`（小屏）。
  - 安装锁定提交的 gomobile；写入 `toolchain.lock.json`。
  - 有 P1 时：在 Apple 团队中注册 Bundle ID、开启 Network Extension 能力、登记真机 UDID。
- **Go**
  - `ClientPlatform` 增加 ios。
  - 所有 `darwin`/`!windows && !android` 平台文件补 `!ios`。
  - 新增 ios 平台文件，第 1 轮 TUN 未实现时明确返回 `tun_unavailable`。
  - `mobile` 包通过 iOS 交叉编译。
  - `gomobile bind` 产出含 `ios-arm64` 与 `ios-arm64-simulator` 两个切片的 xcframework，并记录体积与两次构建是否逐字节一致。
- **后端**
  - `native.go` 白名单加 `ios` + 测试 + `native-v1.json` 增加案例。
  - 由用户部署到 demo 服务器（部署前先说明影响：只放宽一个平台值）。
- **iOS 工程**
  - Xcode 工程、App + `AsterLinkTunnel` 扩展 + `AsterLinkKit` 包。
  - `.env` → xcconfig，并跑 env-v1 全部用例。
  - 主题令牌、显示 Core 版本/Mihomo 提交的骨架页。
  - entitlements（`packet-tunnel-provider`）；`PrivacyInfo.xcprivacy` 初稿。
- **脚本**：`build-native-ios.py`、`test-native-ios.py --suite foundation|unit`；CI `ios.yml`（macOS runner：无签名构建模拟器包 + 单测；保留另三端作业）。
- **实验 E-SIM-NE**：在模拟器上调用 `NETunnelProviderManager.saveToPreferences`，记录实际错误。这是事实记录，不算失败。
- **文档**：`docs/ios-foundation.md`（版本锁、模拟器与磁盘布局、签名前置条件、包体基线）。

**验收：**
- xcframework 两个切片齐全；Go `ipc-v1.json` 全部案例在模拟器 XCTest 中经真实框架通过；登录请求体单测断言 `platform=ios`。
- App 在 iOS 27 模拟器启动并显示 Core 版本与 Mihomo `70f0570405c3`；扩展目标能编译。
- 回归：`native/core go test ./...`、`test-native.py`、`test-native-macos.py --suite unit`、Android `--suite unit` 与 Windows CI 不回归。

**交付：** 可构建骨架、工具链锁、iOS CI、体积基线、P1/P2 状态记录。

### 第 2 轮：Packet Tunnel 扩展、iOS TUN 数据通路与会话交接

**目标：** 证明真机上的真实 TUN 能创建、转发、停止、被杀后清理，且无回环、无 DIRECT 回落、无 IPv6 泄漏；同时实现单一所有者交接。

**开发内容：**
- **扩展**：`PacketTunnelProvider`（无票据就拒绝启动、`startTunnel`/`stopTunnel`/`sleep`/`wake`/`handleAppMessage`）、`TunnelSettings`（TunSpec → NE 设置，含 `tun-spec-ios-v1.json`）、`UtunDescriptor`（找 fd + dup）、`PathMonitor`、`MemoryBudget`。
- **Go**：`platform_ios.go`（fd 交接、`tunReady`）、`ipv6filter_ios.go`（早期拒绝）、`network_ios.go`、`SetResolver`、`handoff.go`（Export/Adopt）+ `handoff-v1.json`、Verify 两段。
- **App 侧最小连接控制**（调试界面即可）：安装 VPN 配置、交接、启停、读取快照。
- **测试工具**：复用 `cmd/asterlink-testnode`（SS/VLESS/REALITY 服务端，固定 Mihomo）与 `asterlink-trafficsink`。真机经局域网访问 Mac，或部署在用户可控服务器上（用完即停）；用户提供的真实节点凭据经调试包的一次性 provider message 注入，不进命令行、仓库或日志。
- **可选**：macOS `asterlink-tuntest --fd-host`，用 iOS 模板跑 T01/T03/T04/T07（需 sudo 并断开公司 VPN）。

**验收（真机，D 前缀）：**
- D-T01 TCP 经 TUN（节点计数/sink 证明）、不设代理；D-T02 SS 与 VLESS(Reality) 真握手，错误凭据失败。
- D-T03 系统 DNS 进 fake-ip、any:53 劫持；D-T04 IPv6 两种节点（有 v6：完整代理；无 v6：早期拒绝且 Safari 回退 IPv4）。
- D-T05 API 可达 + T05-loop；D-T06 `udp:false` 被拒；D-T07 停止后无 VPN；D-T09 扩展被杀后无残留。
- D-CYCLES 20 次启停：goroutine 与 fd 零增长（沿用 NAT worker 零增长门禁）；D-MEM-idle 记录空载内存足迹。
- **模拟器/宿主**：handoff 契约全部案例、IPv6 过滤包级单测、fd 扫描单测、`tun-spec-ios-v1` Go/Swift 一致。
- **缺少 P1/P2 时**：全部 D 项记 BLOCKED，本轮标为“代码完成，真机待验”，第 3、4 轮照常推进。

**交付：** `docs/ios-tun-design.md`（资源所有者、fd 所有权、防环、DNS/IPv6/UDP、Verify 两段、内存基线、实测结论）、真机 tun 报告。

### 第 3 轮：登录、记住密码、套餐与线路同步 + 正式 UI（模拟器全程可验）

**目标：** 网站管理员配置的数据进入 iOS App，三页界面达到商业品质的设计主干。

**开发内容：**
- `CoreRouter`：未连接时调本进程 Core，已连接时调扩展。
- `SessionController`：按 Android 版移植——操作代次丢弃、旧 generation 丢弃、`action_error`、`stale_generation`、`native_session_*` 静默重登一次、内核丢失、连接中 1 s / 其它 2 s 轮询、回前台节流 10 s 刷新目录。
- `CredentialStore`/`DeviceIdentity`（Keychain）。
- 第 10 节全部页面、空态、深色、动态字体、`SFSafariViewController`、启动画面、占位图标。
- XCUITest 截图 ≥ 24 个状态：登录、无套餐、到期、额度 0、未选线、已选线、连接中、已连接、闸门、错误、线路空态/无搜索结果、我的、深色、AX3 字号、SE 小屏。每张截图同时断言所示状态。
- 后端测试世界通用化：把 `TestAndroidAccountWorld`/`TestAndroidBillingWorld` 的控制逻辑抽成平台无关的 client world（保留 Android 入口名或做兼容），iOS 套件直接访问 `127.0.0.1`。

**验收（模拟器，真实 Core + Keychain + Gin/PostgreSQL 隔离实例）：**
- A01 正确/错误密码、保存/取消/删除密码、重启后在线自动登录、不存 token。
- A02 `test@test.com` 等管理员无有效套餐时不能连接；到期、0 额度、设备上限都有对应空态。
- A03 单 watcher、429 退避、旧代次丢弃。
- A04 后台改名/倍率/停用/删除/套餐白名单约 2–5 s 内反映（记录实测值）。
- UI、诊断文本、`log collect` 中搜不到 token、密码、YAML、节点地址。
- 再用 demo 服务器 + 用户的测试账号手工确认一次（需要先部署第 1 轮的后端改动）。

**交付：** 账号与线路界面、截图集、`docs/ios-ui.md`。

### 第 4 轮：连接闭环、开发计量与通知

**目标：** 打通用户最关心的主路径：登录 → 选线 → 连接 → 正常上网 → 真实用量 → 按倍率扣量 → 到期/用尽/禁用时自动停连。

**开发内容：**
- 连接按钮接入首次配置引导、交接与 `startVPNTunnel`；只有 Verify 两段都通过才显示“已连接”。
- 速率、60 秒 Swift Charts 曲线、本次物理用量与计费用量（连接时记录基线，按本连接绑定的倍率整数计算）、确认/预估余额、pending 提示、计量来源标识。
- 切线路确认对话框；App 内断开即交回会话（保持登录）。
- 扩展在后台停连时发本地通知；回前台显示 `fetchLastDisconnectError` 的原因。
- 计费验收：
  - **模拟器（harness）**：把 `asterlink-billtest` 的断言移植为 XCUITest/XCTest 驱动的 `--suite billing`，用真实后端 + testnode + sink；ACK 丢失由后端测试世界注入，App 内没有注入开关。
  - **node_reported**：本地后端 + 本地 NodeAgent + 线束跑完整 grant 闭环。
  - **真机**：同样的 20+80 MiB 流量经真实 TUN（`--suite device-billing`）。

**验收：**
- B01 1× 与 0.5× 逐单位精确（计数与正文差异单独解释）；B02 丢 ACK 后原批重试只扣一次。
- A04 改倍率/停用节点/套餐到期/额度耗尽均自动停连且尾账结清。
- C01 断开后立即重连成功，交接往返无账本冲突。
- B04 无客户端上报仍由节点计费、`/traffic` 被拒。
- 真机：Safari 正常浏览、视频播放；后台停连通知出现一次。
- 真实公网受管节点不存在时 B04-live 记 BLOCKED；缺少 P1/P2 时 device-billing 记 BLOCKED。

**交付：** iOS 内部功能版（模拟器包；有 P1/P2 时为真机开发包）、计费报告、`docs/ios-billing-acceptance.md`。`client_reported` 仍是开发计量，不解除生产保护。

### 第 5 轮：移动端生命周期、内存与可靠性

**目标：** 手机日常使用下（息屏、切网络、App 被杀、系统内存压力）VPN 稳定，且不因此放宽授权。

**开发内容：**
- 闸门在 iOS 上的接线：`sleep`/`wake`、路径变化、租约失效，以及等待网络时的退避与上限。
- 前台提示租约（8.6）。
- App 被杀/重开后读取扩展快照恢复界面。
- 从设置/控制中心关闭、被其它 VPN 抢占（测试包里的极简竞争 VPN 扩展）、通知权限被拒等路径。
- 内存治理定值（8.7）。
- 采样工具：真机 `phys_footprint`、CPU、fd、goroutine，电量用 Xcode Energy/Instruments 记录。

**验收（真机为主，D 前缀；模拟器项标 S）：**
- D-L01 息屏 10 分钟后解锁，不需手动重连，期间无转发泄漏，闸门关闭时长与原因有记录。
- D-L02 Wi-Fi↔蜂窝切换、飞行模式：先关闸再重验。
- D-L03 其它 VPN 抢占 → 停流结账、提示。
- D-L04 从设置关闭 → 结账，App 显示原因。
- D-L05 划掉 App 不断开；重开后界面恢复为“已连接”且无需登录。
- D-L06 重启手机后不自动连接。
- D-MEM 30 分钟高负载不被 jetsam。
- D-L07 20 次启停 + 8 小时连接采样（不填猜测数字）。
- S-L01 App 侧 provider 状态机与交接失败路径单测。
- 共享 `managed` 若有改动，跑 macOS/Windows/Android 回归。

**交付：** `docs/ios-lifecycle.md`、故障矩阵、内存与性能基线、已知限制（含 `includeAllNetworks` 未启用的影响）。

### 第 6 轮：打磨、签名、TestFlight 与发布交接

**目标：** 普通用户通过 TestFlight（以及审核通过后的 App Store）即可安装、升级、卸载；视觉与可访问性达标；交付可追溯。

**开发内容：**
- **UI 打磨**：逐屏截图评审、VoiceOver、AX3、深色、SE 小屏、英文。
- **正式素材**：App 图标与启动画面（需用户提供 Logo）。
- **合规材料**：隐私清单（Required Reason API，含 Go 运行时与 Mihomo 实际调用的条目，按构建产物扫描确认）、加密出口合规 `ITSAppUsesNonExemptEncryption` 与问卷准备、App 隐私“营养标签”草稿（只收集邮箱与用量）。
- **审核准备**：审核备注与审核用测试账号（有效套餐 + 普通线路），购买入口策略按第 14 节落地为构建开关。
- **打包脚本** `package-native-ios.py`：`xcodebuild archive` → `-exportArchive`（App Store Connect / Ad Hoc），产出 IPA、dSYM（私有）、`THIRD_PARTY_LICENSES`、CycloneDX 清单、`SOURCE.txt` + GPL 源码包、`release-info.json`（`trusted_release=false` + `open_gates`）、`SHA256SUMS`。签名用 Xcode 自动签名或 App Store Connect API 密钥，密钥只从钥匙串/环境读取。
- **`--suite release` 审计**：
  - 签名与 entitlements（无 `get-task-allow`）、扩展 Bundle ID 与 entitlement 一致；
  - 无线束/测试符号、无秘密字符串（含植入秘密的自检）；
  - HTTPS 端点、ATS 无例外、隐私清单存在、许可材料齐全、Core 身份与 `core-build.json` 一致。
- **网站**：`/download` 页增加 iOS 入口（App Store/TestFlight 公开链接放 `frontend/.env` 的服务端变量，可选）。

**验收：**
- R01 TestFlight 构建在真机干净安装、首次添加 VPN 配置、登录连接。
- R02 覆盖升级保留记住的密码与 VPN 配置；删除 App 后 VPN 配置随之移除，重装为空白登录页（Keychain 中 device_id 保留属预期，需写入隐私说明）。
- R03 release 审计全部 PASS。
- R04 至少两种机型/系统组合抽检（无法获得时记 BLOCKED，`trusted_release=false`）。

**交付：** 发布资料、`docs/ios-release.md`（TestFlight/App Store 流程、升级、回退、已知限制、审核注意事项）、本文件最终进度。

## 12. 统一验收矩阵

每项输出 `case_id / expected / observed / PASS|FAIL|BLOCKED|NOT_RUN / commit / build / env(simulator|harness|device, 机型, iOS 版本) / evidence`。**编制时下表全部 NOT_RUN。**

| ID | 场景 | 通过要求 | 证据环境 | 轮次 |
| --- | --- | --- | --- | --- |
| E01 | xcframework/App/扩展可复现构建 | 版本锁一致；真实引擎，非 fakeengine/线束 | 宿主 | 1、6 |
| E02 | 共享契约（ipc/snapshot/env/native-v1/v2/handoff/tun-spec-ios） | Swift 与 Go 同一夹具通过；>2^53 整数精确 | 模拟器 | 1–3 |
| E03 | 模拟器 NE 实验 | 如实记录错误（预期不可运行） | 模拟器 | 1 |
| H01 | 会话交接：交出/接收/交回/丢弃/重复/过期 | 任一时刻单一写入者；账本连续；拒绝案例全部拒绝 | 模拟器 + Go | 2、4 |
| A01 | 登录、记住密码、退出登录 | 在线认证；Keychain 隔离；无 token 持久化 | 模拟器 | 3 |
| A02 | 管理员无套餐、到期、0 额度、设备上限 | 不下发代理授权，不因角色放行 | 模拟器 | 3–4 |
| A03 | 目录 watcher、429、旧代次 | 单 watcher、退避、旧响应丢弃 | 模拟器 + Go | 3 |
| A04 | 后台改名/倍率/停用/删除/白名单 | 同步并停旧连接，旧段旧倍率 | 模拟器 + 真机 | 3–4 |
| T01–T09 | TCP、握手、DNS、IPv6、防环、UDP、停止、被杀 | 见第 2 轮 | **真机** | 2 |
| B01 | 20 MiB 上 + 80 MiB 下，1×/0.5× | 逐单位精确，差异解释 | 线束 + 真机 | 4 |
| B02 | ACK 丢失、重放 | 原批重试只扣一次 | 线束 | 4 |
| B04 | node_reported 无客户端上报 | 节点照常计费；`/traffic` 被拒 | 线束（本地 NodeAgent）；公网受管节点 6 | 4、6 |
| L01–L07 | 息屏、切网、抢占、设置关闭、划卡、重启、长测 | 见第 5 轮 | **真机** | 5 |
| M01 | 扩展内存 | 高负载 30 分钟不被 jetsam；峰值记录 | **真机**（模拟器仅参考） | 2、5 |
| U01 | 截图集、VoiceOver、AX3、深色、小屏、英文 | 无截断、无假状态、状态可播报 | 模拟器 | 3、6 |
| R01–R04 | 安装、升级、删除、发布审计、多机抽检 | 见第 6 轮 | 真机 + 宿主 | 6 |
| G01 | 后端、网站、共享策略、macOS、Windows、Android 回归 | 不因 iOS 削弱既有保护 | 宿主/CI | 每轮 |

**测试纪律（沿用前三份计划书）：**
- 未运行不能写通过；缺目标、凭据、设备或账号时写 BLOCKED（脚本退出码 3）。
- 不用 fakeengine 或线束结果冒充真实 TUN。
- 默认使用隔离 PostgreSQL，不清空、不迁移用户的远端测试库；如需核对 demo 账本，只用只读 doctor，并在记录中写明。

## 13. 构建与测试命令契约（后续轮次实现，目前不存在）

```sh
# 第 1 轮起（Mac 本机，模拟器构建不需要签名）
python3 scripts/build-native-ios.py --target xcframework --configuration Debug
python3 scripts/build-native-ios.py --target app --destination simulator --configuration Debug
python3 scripts/test-native-ios.py --suite foundation
python3 scripts/test-native-ios.py --suite unit            # Go 宿主测试 + 模拟器上的 Swift 单元测试

# 第 3 轮起（模拟器 + 本机隔离后端）
python3 scripts/test-native-ios.py --suite ui --simulator asterlink-iphone
python3 scripts/test-native-ios.py --suite account --simulator asterlink-iphone
python3 scripts/test-native-ios.py --suite billing --simulator asterlink-iphone   # 进程内注入线束（只影响 App 进程）

# 真机套件（需要 P1/P2；跑真实节点前断开 Mac 的公司 VPN 并退出 macOS 版 AsterLink）
python3 scripts/test-native-ios.py --suite device-tun --device <udid> --allow-network-changes
python3 scripts/test-native-ios.py --suite device-billing --device <udid> --allow-network-changes
python3 scripts/test-native-ios.py --suite lifecycle --device <udid> --allow-network-changes

# 第 6 轮：签名材料只从钥匙串/环境读取，不放命令行与源码
python3 scripts/package-native-ios.py --channel testflight
python3 scripts/test-native-ios.py --suite release
```

保留的回归入口：`scripts/test-native.py`、`test-backend-shared.py`、`test-backend-standalone.py`、`test-node-catalog.py`、`test-native-macos.py`、`test-native-windows.py`、`test-native-android.py`。

## 14. 风险与外部门槛

| 风险/门槛 | 处置 |
| --- | --- |
| **无付费开发者账号 / 无 iPhone**（P1/P2） | 第 1 轮前决定；没有就只能交付模拟器可验证部分，真机项 BLOCKED，不得宣称 iOS TUN 可用 |
| **模拟器不能运行 Packet Tunnel** | 架构上把“可在模拟器验证”的部分最大化（Core 在 App 进程、进程内注入线束）；真机证据单独记录 |
| **扩展 50 MiB 内存上限** | 第 2 轮测空载基线，第 5 轮高负载定值；超标时优先裁剪功能、限连接数、调 GC，不得靠关闭计量或闸门省内存 |
| **iOS 无 IPv6 设置即泄漏** | 始终收 `::/0` + 早期拒绝（8.4），T04 硬性用例 |
| **扩展自身 DNS 可能经隧道回环** | 限定物理接口的 dnssd 查询，或沿用直连 `tcp://1.1.1.1`；第 2 轮定 |
| **`system` 栈在 NE fd 模式下的行为** | 第 2 轮真机实测，必要时一次性决策换 gvisor（并复核内存），不自动回退 |
| **App Store 审核 5.4（VPN）** | 必须以组织身份提交；使用 `NEVPNManager`/NE API；声明收集的数据；不得出售/披露用户数据；提交前按届时条款复核 |
| **App Store 3.1.1（数字服务购买）** | VPN 套餐属数字服务。App 内引导到网站购买，在多数地区受反引导规则限制（美国区等有例外，规则仍在变化）。首版建议 iOS 不放购买/续费链接，只显示套餐状态与“请到官网管理套餐”的说明；是否接入 IAP 由用户商业决策，属本计划外的独立工作。构建开关 `IOS_PURCHASE_ENTRY=none|link` 控制，默认 `none` |
| **中国大陆区 App Store** | 不允许未经许可的 VPN 上架；只能选择其它地区上架。法规与地区合规须法务确认 |
| **GPL-3.0（Mihomo）与 App Store 分发条款的兼容性** | 业界普遍认为 GPL 与 App Store 使用条款存在冲突（有先例）。**必须在上架前由法务确认**：可能的方案包括获得权利人额外授权、提供完整对应源码与可重建方式，或换用许可兼容的内核；未确认前 `trusted_release=false`。go-lzo（GPL-2.0）沿用 Windows/Android 的待确认项 |
| **加密出口合规** | VPN 使用非豁免加密的可能性高，按 App Store Connect 问卷如实申报，必要时准备年度自查报告 |
| **长连接不迁移（未启用 includeAllNetworks）** | 写入已知限制；第 5 轮测量；如要销售“全流量保证”，需另行设计 `includeAllNetworks` 方案并全面回归 |
| **节点控制权（商业强制计量）** | 与另三端相同：只有 NodeAgent 管理的 VLESS(TLS) 参考转发器是权威节点；SS/Reality/WireGuard 等没有节点侧强制，不得进入收费承诺 |
| **网站运营** | 支付回调验签、幂等开通、退款对账、邮箱验证/找回密码、隐私政策/用户协议页面（iOS 审核必需）、管理员 MFA、备份告警——不在 App 内重做，但属于整体收费门槛 |
| **测试凭据** | 对话中出现过的数据库密码与节点凭据需轮换；真机测试凭据只经调试包一次性注入 |

## 15. 交接规则与进度记录

只维护本文件：6 轮状态表 + 每轮一段记录。每轮一个 PR（`worktree-ios-roundN-*` 分支），必要的修复留在本轮。

接手顺序：
1. 读本文件当前轮；
2. 看 `git status` 与上一轮 PR；
3. 读相关共享契约与源码；
4. 运行上一轮的验收入口；
5. 只做当前未完成项。

契约变化必须同时更新 JSON、Go、Swift（以及受影响的 Kotlin/C#）与测试。

| 轮次 | 状态 | 下一步与前置条件 |
| --- | --- | --- |
| 1 环境与骨架 | DONE（模拟器范围；PR #40 已合并）| 本机 foundation 11/11、unit 3/3、simulator 6 PASS + E-SIM-NE 实测记录；P1/P2 缺失，真机项 BLOCKED；demo 服务器需部署后端 `ios` 白名单后才能登录 |
| 2 扩展与真实 TUN | 代码完成，真机待验（分支 worktree-ios-round2-tunnel）| 主机/模拟器项全部 PASS；D-T01–D-T09、D-CYCLES、D-MEM-idle 全部 BLOCKED（0 个签名身份；已配对 iPhone 一台）；真机步骤见 `docs/ios-tun-design.md` 第 9 节 |
| 3 账号与 UI | DONE（模拟器范围；分支 worktree-ios-round3-account-ui）| unit 42 个 Swift + 182 个 Go；ui 44 用例/45 截图；account 19 项真实 Core+Keychain+隔离后端全 PASS；demo 手工确认 NOT_RUN（需用户测试账号与已部署后端） |
| 4 连接与计量 | DONE（模拟器与 harness 范围；分支 worktree-ios-round4-connect-loop）| unit Go 182 + Swift 63；ui 48 用例/50 截图；account 19 项回归全 PASS；billing --harness both：B01/B02/B04/A04/C01 在 Go 宿主与 iOS 模拟器进程里各 28 项全 PASS（真实 Core + Mihomo + 后端 + 节点 + NodeAgent）；device-billing 与扩展侧整条连接 BLOCKED（无付费 Apple 团队） |
| 5 生命周期与可靠性 | NOT_STARTED | 主要依赖真机 |
| 6 打磨与发布 | NOT_STARTED | 需要组织开发者账号、Logo、隐私政策/用户协议页面、GPL 法务结论 |

每轮追加：

```text
轮次 / 日期 / 执行者：
基线 commit / 已有未提交改动：
实际修改与契约变化（不含秘密）：
执行命令 / 实际测试数 / PASS、FAIL、BLOCKED（分 simulator / harness / device）：
App/扩展/xcframework 版本 / 模拟器或真机（机型、iOS 版本）/ Core 与 Mihomo 版本 / 内存峰值 / 脱敏证据路径：
未解决问题、复现方法与发布限制：
下一位从哪个文件/验收项开始；回退方法：
```

**本次规划交接：**
- 已读完 macOS、Windows、Android 三份交接书，核对了 PR #1–#39，以及 Core 的构建标签、mobile 绑定、meter 重基线、后端平台白名单、sing-tun darwin 的 fd 路径、本机 Xcode/SDK/模拟器/签名/磁盘状况。
- 识别出 iOS 的必改点：GOOS=ios 隐含 darwin 标签会误链接 macOS 实现、登录平台与后端白名单、两个进程的会话交接、NE 的 IPv6 泄漏行为、扩展自身流量与 DNS、Verify 前提、50 MiB 内存上限，以及模拟器不能运行 NE 的验证边界。
- 外部前提：付费（组织）开发者账号、真机、磁盘空间；以及 App Store 的 VPN、数字购买、GPL 等商业门槛。
- 未创建 iOS 工程、未改 Core 或后端、未部署、未连接远端数据库。下一位从第 1 轮开始。

### 第 1 轮记录

```text
轮次 / 日期 / 执行者：第 1 轮 / 2026-10-16 / Claude（worktree 分支 worktree-ios-round1-foundation）
基线 commit / 已有未提交改动：1c9d64b（PR #39 合并点）；本分支改动尚未提交
前置条件：P1（付费账号）、P2（iPhone）缺失，用户决定先按模拟器范围推进，真机项记 BLOCKED；
  P3：用户同意后执行 go clean -cache（释放约 23 GiB，可用 10→33 GiB），安装 iOS 27.0 模拟器运行时（24A434，8.05 GB）
实际修改与契约变化（不含秘密）：
  Go Core：engine/{platform_unix,sysdns,ipv6_route}.go（及两个测试）与 network/network_darwin.go 补 !ios
    （GOOS=ios 隐含 darwin 标签）；新增 engine/{platform,sysdns,ipv6}_ios.go（platformSupported=false，
    connect 明确返回 tun_unavailable）、network/network_ios.go（宿主快照 + IP_BOUND_IF）；
    managed.ClientPlatform 增加 ios→"ios"（单测）。
  后端：native.go 登录白名单加 "ios"；native_test.go 新增 login_platform_allow_list（五个平台登录成功且会话记录平台，
    "ipados"/空值 400）。偏离规划：native-v1.json 未加 ios 案例——白名单已由后端测试覆盖，改共享夹具会牵动三端契约测试。
  iOS：apps/ios（Xcode 工程：AsterLink、AsterLinkTunnel、AsterLinkTests，同步文件夹 + 共享 scheme；Config/ 的 xcconfig、
    Info.plist、entitlements；AsterLinkKit 包；toolchain.lock.json；.env.example）；扩展第 1 轮一律拒绝启动。
  脚本：build-native-ios.py（xcconfig/xcframework/app）、test-native-ios.py（foundation/unit/simulator）；CI ios.yml；
    docs/ios-foundation.md。无契约 JSON 变化。
执行命令 / 实际测试数 / PASS、FAIL、BLOCKED（本机 macOS arm64，Xcode 27.0，公司 VPN 开启）：
  test-native-ios.py --suite foundation：11 PASS
  test-native-ios.py --suite unit：3 PASS（ios/arm64 交叉编译；Go 162 个测试；AsterLinkKit 4 个，含 env-v1 全部用例）
  test-native-ios.py --suite simulator（iPhone 18 Pro，iOS 27.0）：5 PASS + E-SIM-NE（INFO）
    模拟器内 2 个测试 0 失败：CoreBindingTests 经真实链接的 Core 跑 ipc-v1 全部 8 例（夹具中 not_implemented 一例
    在公开接口未启动时为 core_not_started，与 Android 规则相同）、启动后 hello 身份与 signed_out、停止后再次拒绝；
    测试包无 Go 符号（宿主 App 有）；App 启动后截图显示“Core 0.1.0 / Mihomo 70f0570405c3”。
  E-SIM-NE：模拟器拒绝保存 Packet Tunnel 配置——NEVPNErrorDomain code=5“IPC failed”（系统日志为
    NEConfigurationErrorDomain Code=11）。结论：iOS TUN 只能真机验收，与规划一致。
  负向对照：旧白名单下新后端测试失败（400 invalid_client_login）；同一字符串扫描在 darwin asterlink-core 中找到
    /sbin/route 与 networksetup（各 1 次），而 iOS 真机切片中为 0。
  回归：native/core go test ./...（15 包）PASS；go vet windows/linux/android 无问题；
    scripts/test-native.py（隔离 PostgreSQL + race）172 PASS / 0 FAIL；test-native-macos.py --suite unit：8/8 PASS
    （Core 275、policy 5、nodeagent 77、Swift 46；vet、日志检查通过）
  CI：ios.yml 未运行（需推送）；记 NOT_RUN。
产物 / 环境 / 版本 / 证据路径：
  AsterCore.xcframework：ios-arm64 28,532,752 B（arm64），ios-arm64_x86_64-simulator 55,844,320 B；bind 约 44 s，
    无额外参数；core-build.json 记录两个切片 SHA-256、Mihomo 70f0570405c3 + 补丁 d425c1a0…；
  模拟器 Debug App 约 48 MiB，扩展 492 KiB（第 1 轮不含 Core）；
  artifacts/native-ios/{foundation,unit,simulator}/（报告 JSON、xcresult、截图、日志；不提交）
未解决问题、复现方法与发布限制：
  ① 真机全部 BLOCKED（P1、P2）。② demo.hyshentou.cn 的后端须由用户部署本分支的白名单改动后，iOS 才能在那里登录。
  ③ CI 的 Xcode 26.3 能否打开 objectVersion 77 工程、其 build 号，都待首次 CI 运行记录。
  ④ Bundle ID 前缀为占位 com.asterlink.dev，注册 Apple 团队后再定。
  ⑤ 本机 gomobile 网络：bind 期间 go mod tidy 访问 sum.golang.org，公司 VPN 下暂未遇到失败；脚本未加重试（Android 有）。
下一位从哪个文件/验收项开始；回退方法：
  第 2 轮需要 P1 + P2；若仍缺失，可先做第 3 轮（模拟器全程可验）。第 2 轮从 AsterLinkTunnel/PacketTunnelProvider.swift、
  engine/platform_ios.go（fd 交接）与 managed 的会话交接（4.2）开始。
  回退：撤销本分支即可；桌面端只多了 !ios 标签（macOS/Windows/Android 构建所选文件不变），后端只多了一个允许的平台值。
```

### 第 2 轮记录

```text
轮次 / 日期 / 执行者：第 2 轮 / 2026-10-16 / Claude（worktree 分支 worktree-ios-round2-tunnel）
基线 commit / 已有未提交改动：d826dd7（PR #40 合并点）；无
前置条件：P1 仍缺（security find-identity：0 个签名身份）；P2 部分满足（devicectl 显示已配对 iPhone14,5 一台，
  但无付费团队无法签 packet-tunnel-provider）；用户决定先按“代码完成，真机待验”推进。公司 VPN 开启。
实际修改与契约变化（不含秘密）：
  契约：新增 shared/contracts/handoff-v1.json（票据字段、11 个拒绝案例、8 个 adopt 场景、provider 消息与回复）、
    tun-spec-ios-v1.json（iOS 规格永远 ipv6:true、NE 设置映射、13 个拒绝案例）。ipc-v1 不变（交接不是 ipc 命令）。
    相对规划：票据多 channel、last_sequence、user 三个字段；新状态 "released"。
  Go：managed/handoff.go（ExportHandoff/AdoptHandoff/ParseHandoff，脱敏打印）；mobile/handoff.go（ExportSession、
    AdoptSession、TuneMemory、FreeMemory、RuntimeStats）；engine：iOS 模板（config.go）、platform_ios.go（fd 交接、
    中继、Verify 第一段经节点进程内拨号）、ipv6filter.go + ipv6relay_unix.go（IPv6 早期拒绝）、ipv6_ios.go、
    iosTunConf；network_ios.go 控制面引导解析器回退；service.Reply/ErrorFor 导出；apiclient 新增 8 个 handoff_* 文案。
  Swift：AsterLinkKit 新增 TunnelSpec/NetworkSettingsPlan、ProviderMessage/Reply、UtunDescriptor、HostNetworkReport +
    PathMonitor、MemoryBudget、RouteProbe、DeviceIdentity、CoreRequest.login/selectNode/command、SnapshotHead，以及 C 目标
    CAsterResolv（res_getservers）。扩展：PacketTunnelProvider（无票据拒绝、adopt→connect、release、stop、wake、内存看守）、
    TunnelSettings、TunnelPlatform（openTun：应用设置→找 utun→dup）；扩展现链接 AsterCore。App：TunnelController
    （安装配置、交接启动、release 取回、provider 消息、fetchLastDisconnectError）、IOSPlatform 报告网络、调试页
    TunnelDebugView（仅 Debug）。
  脚本：test-native-ios.py 新增 device-tun 套件（前置不满足时全部 BLOCKED，exit 3）、simulator.extension_core 检查、
    iOS 文件拆分包含 ipv6filter/ipv6relay。文档：docs/ios-tun-design.md。
执行命令 / 实际测试数 / PASS、FAIL、BLOCKED（本机 macOS arm64，Xcode 27.0，公司 VPN 开启）：
  go test ./...（native/core 全部 15 个包）PASS；managed/apiclient -race PASS；go vet ios/linux/windows/android 无问题
  新增 Go 测试：handoff 12 个（含 app→扩展→app 往返：一次会话、无重复登录与撤销、20 MiB 基线后上 1000 B/下 80 MiB
    逐字节结清）、iOS 模板与 tun-spec-ios 契约、IPv6 拒绝包级 2 个、中继 2 个（9000 B IPv4 透传、IPv6 不达栈、限速、
    goroutine 零增长、关闭 fd）、mobile handoff/内存 2 个
  test-native-ios.py --suite foundation：11 PASS；--suite unit：3 PASS（Go 182 个、AsterLinkKit 15 个）
  --suite simulator（iPhone 18 Pro，iOS 27.0）：6 PASS + E-SIM-NE INFO（模拟器内 4 个测试：ipc-v1、交接经真实框架、
    App 指向所嵌扩展的 Bundle ID）；扩展二进制含 MobileStart/MobileAdoptSession
  --suite device-tun：device.iphone PASS，device.signing_identity BLOCKED，D-T01..D-T09/D-CYCLES/D-MEM-idle 10 项 BLOCKED
  UTUN-SCAN：macOS 非 root 创建 utun 被拒（EPERM），真实 fd 扫描只在真机验证；选择规则单测覆盖
  回归：test-native-macos.py --suite unit 8/8 PASS（Core 295、policy 5、nodeagent 77、Swift 46；vet、日志检查通过）；
    scripts/test-native.py（隔离 PostgreSQL + race）exit 0，172 个 PASS / 0 个 FAIL；CI 见 PR
产物 / 环境 / 版本 / 证据路径：
  AsterCore.xcframework 设备切片 52,865,920 B（go.o 52.6 MB，无 DWARF；第 1 轮记录 28.5 MB，原因未查明），模拟器切片
    104,256,656 B；Debug 模拟器扩展 AsterLinkTunnel.debug.dylib 约 90 MiB（含 Core，Debug 未剥离）；
  artifacts/native-ios/{foundation,unit,simulator,device-tun}/（不提交）
未解决问题、复现方法与发布限制：
  ① 全部 D 项 BLOCKED（P1）。② 以下只能真机核实：扩展自身流量绕过隧道（决定 Verify 第二段去留）、system 栈在 NE fd
    模式下可用、关闭 dup 副本后 NE 接口仍可用、socketpair 中继在真机的吞吐与内存、MTU 1500 与内存阈值初值。
  ③ 系统侧停止时扩展撤销 token（比规划“丢弃”更严格）；用户在连接中取消也会撤销，App 需重新登录（第 3 轮静默重登）。
  ④ 调试页只用于验证交接，正式 UI 在第 3 轮；“两段 Verify 都通过才显示已连接”在第 4 轮接入。
  ⑤ xcframework 体积差异待第 6 轮发布审计复核。
下一位从哪个文件/验收项开始；回退方法：
  有付费团队后按 docs/ios-tun-design.md 第 9 节跑真机 D 项并补记录；否则直接进入第 3 轮（模拟器全程可验）。
  回退：撤销本分支。桌面端与 Android 只多了导出的 service.Reply/ErrorFor、Manager 的 handoff 方法与新文案，
  原有路径行为不变（上方回归）。
```

### 第 3 轮记录

```text
轮次 / 日期 / 执行者：第 3 轮 / 2026-10-17 / Claude（worktree 分支 worktree-ios-round3-account-ui）
基线 commit / 已有未提交改动：a1df84b（PR #41 合并点）；无
实际修改与契约变化（不含秘密）：
  契约：无变化。后端：android_world_test.go 抽出 serveNativeAccountWorld，新增 TestClientAccountWorld（环境变量
    ASTERLINK_CLIENT_WORLD[_PORT]），与 Android 的 TestAndroidAccountWorld 共用同一世界。
  AsterLinkKit：Snapshot（全字段 Int64）、CoreClient、SessionState/ConnectionView/NodeList、Formatting、ErrorText、
    Ports、Diagnostics、SessionController（@MainActor @Observable，Android 版语义：操作代次、旧代次丢弃、action_error、
    stale_generation、静默重登一次、内核丢失、1 s/2 s 轮询、回前台 10 s 节流）；DirectCoreTunnel 为第 4 轮交接接口留的接缝。
  App：Platform（AppConfig→env-v1、Keychain 密码库、UserDefaults 偏好、设备 ID、Core 宿主）、登录/连接/线路/我的页、
    资源目录（AccentColor、占位 AppIcon 三外观、启动画面）、仅 Debug 的 UIFixtures；删除第 1 轮的 FoundationView。
  工程：新增 AsterLinkUITests 目标与 AsterLinkUI scheme（默认 AsterLink scheme 不跑 UI 用例）。
  脚本：test-native-ios.py 新增 --suite ui、--suite account；ios.yml 增加 ui 套件。docs/ios-ui.md。
执行命令 / 实际测试数 / PASS、FAIL、BLOCKED（本机 macOS arm64，Xcode 27.0，iPhone 18 Pro 模拟器 iOS 27.0，公司 VPN 开启）：
  foundation 11 PASS；unit：Go 182 个、AsterLinkKit 42 个（新增 27 个：快照契约解码、>2^53 整数、格式化、ConnectionView、
    线路过滤、SessionController 脚本化 15 个）全 PASS；simulator 6 PASS + E-SIM-NE（INFO，同第 1 轮）。
  ui（simulator）：44 个用例，每个先断言状态再截图，截图 45 张（登录 5、连接 19、线路 7、我的 6、深色 4、AX3 4）。
  account（simulator + 真实 Core + 真实 Keychain + Gin/隔离 PostgreSQL，回环）：19 项全 PASS——A01 错误/正确密码、保存、
    不保存、重启后在线自动登录、退出遗忘、文件中无密码/Bearer；A02 管理员无套餐、0 额度、套餐过期（2.0 s）、设备上限；
    A04 套餐过滤、改名+倍率（2.1 s，原绑定配置被丢弃）、停用（2.0 s）、启用（2.0 s）、删除（2.0 s）；脱敏；模拟器日志无密码/节点秘密。
  回归：go vet、native/core go test（unit 套件）PASS。
产物 / 环境 / 版本 / 证据路径：artifacts/native-ios/{ui,account,unit,simulator,foundation}/（不提交）；Core 0.1.0、Mihomo 70f0570405c3。
发现并修复的问题（记录以便复现）：
  ① 无签名的模拟器二进制没有 application-identifier，Keychain 返回 -34018；account 套件改用 ad-hoc 签名（不需要 Apple 团队）。
  ② 模拟器的系统深色外观在测试中没有传到 App（系统设置本身也仍是浅色），所以深色截图改由 App 自己的“外观”偏好
    （调试环境变量 ASTER_APPEARANCE）驱动，并加了像素亮度断言防止“标了深色其实是浅色”。
  ③ 个别 SwiftUI 容器的 accessibilityIdentifier 不下传（需要 .contain / 显式元素），已在界面与用例里处理。
未解决问题、复现方法与发布限制：
  ① demo.hyshentou.cn 手工确认：NOT_RUN（需要部署第 1 轮的后端 ios 白名单，并由用户用测试账号登录一次）。
  ② 无 iPhone SE 镜像，小屏用例 NOT_RUN；动态字体最大测到 AX3。
  ③ 连接按钮在 iOS 上如实返回 tun_unavailable（App 进程不持有隧道）；首次“添加 VPN 配置”引导、两段 Verify、
    交接启动在第 4 轮。④ 网站尚无 /privacy、/terms、找回密码页，界面不放这些入口（第 6 轮门槛）。
  ⑤ 英文本地化、正式图标与启动画面留给第 6 轮；现为矢量占位。⑥ A03（单 watcher、429 退避）由 Go 测试与 Swift
    “旧代次丢弃”单测覆盖，没有为它另做 iOS 端到端用例。⑦ 真机项仍全部 BLOCKED（P1、P2 同第 2 轮）。
下一位从哪个文件/验收项开始；回退方法：
  第 4 轮从 AsterLinkKit/Ports.swift 的 TunnelControl 接缝开始：把 DirectCoreTunnel 换成基于 TunnelController 的实现，
  接入首次配置引导、交接、两段 Verify 与通知。回退：撤销本分支；后端只多了一个测试世界入口。
```

### 第 4 轮记录

```text
轮次 / 日期 / 执行者：第 4 轮 / 2026-10-17 / Claude（worktree 分支 worktree-ios-round4-connect-loop）
基线 commit / 已有未提交改动：a7b059a（PR #42 合并点）；无
实际修改与契约变化（不含秘密）：
  契约：无变化（handoff-v1、ipc-v1 不变）。Go：无改动。
  AsterLinkKit：TunnelBridge（按会话所有者路由 ipc，扩展不可达返回 tunnel_unreachable）、TunnelWatch（扩展自行停连的判定）、
    TunnelNotifier/NotificationAlerts（本地通知，固定标识，只带原因文字）、TunnelControl 增加 configured/foreground/lastEndReason
    且 stop() 返回“是否换了主”；SessionController：首次配置说明（needsVPNSetup、continueVPNSetup）、tunnelGone（扩展不可达→取回会话、
    用扩展给出的原因）、released 状态视为会话丢失并静默重登一次、换主后首个快照不比 generation（newOwner）、断开后重读 App Core；
    Formatting.chargedUnits（逐单位、溢出饱和）、ConnectionUsage.chargedUnits、CoreSnapshot.State.released、新文案。
  App：NetworkTunnelControl（安装/启用配置→交出→startVPNTunnel→等 connected→桥指向扩展→Verify 第二段→才返回已连接；停止=release+停+取回）、
    ProviderChannel、VPNSetupSheet、TunnelController 补 lastDisconnectNotice/stopAndWait 等、AppModel 接线与前后台提示。
    扩展：连接成功后每 5 s 的结束监视（只一次：本地通知 + cancelTunnelWithError 带原因；App 要求的停止不报）。
  脚本/CI：test-native-ios.py 新增 --suite billing；ios.yml 增加该步骤（0 或 3 通过）。docs/ios-billing-acceptance.md。
执行命令 / 实际测试数 / PASS、FAIL、BLOCKED（本机 macOS arm64，Xcode 27.0，iPhone 18 Pro 模拟器 iOS 27.0，公司 VPN 开启）：
  foundation 11 PASS；unit：Go 182、AsterLinkKit 63（新增 21 个）全 PASS；simulator 6 PASS + E-SIM-NE（INFO，同前）；
  ui：48 用例、50 张截图全 PASS（新增首次配置说明页、拒绝、被系统收走、探测失败）；
  account（模拟器 + 真实 Core + Keychain + 隔离后端）：19 项全 PASS，用以确认请求改走 TunnelBridge 后无回归；
  billing：Go 账本 7/7 与交接 5/5（B01 逐单位 1×=104,857,600,000、0.5×=52,428,800,000 units；B02 丢 ACK 只扣一次；H01 往返）PASS，
    Swift 21 PASS；device-billing BLOCKED。
  billing --harness both（无需联网、不改网络设置；Postgres 为私有实例）：host 与 simulator 各 28 项 PASS（B01 1×/0.5× 逐单位：
    104,858,514,000 / 52,429,257,000 units，sink 实收 20 MiB 上 + 80 MiB 下逐字节相等；B02 丢 ACK 原批重试只扣一次；A04 改倍率/停用/
    到期/额度耗尽均约 1 s 内自行停连、尾账按旧倍率结清；C01 立即重连 5 次；B04 NodeAgent 记账、客户端上报 409 且账本不动）；
    负向对照（引擎上行计数减半）如期 FAIL，已还原。foundation 新增 4 项线束隔离检查，simulator 新增 2 项（生产框架无 HarnessRun）。
产物 / 环境 / 版本：artifacts/native-ios/{unit,ui,account,billing,simulator,foundation}/（不提交）；Core 0.1.0、Mihomo 70f0570405c3。
未解决问题、复现方法与发布限制：
  ① 线束只覆盖 Go 侧：Swift 的 SessionController/桥靠 AsterLinkKit 单元测试与 ui/account 套件，不在线束里；utun、系统 DNS、IPv6、扩展进程、
    通知仍只能真机。线束的模拟器运行是 `simctl spawn` 的命令行（apps/ios/Harness/main.swift，测试专用框架在 .runtime/ios/harness），不是 App 内的 XCTest。
  ② 真机全部 BLOCKED（P1/P2）：扩展侧整条连接、Verify 第二段、后台通知出现一次、fetchLastDisconnectError 是否带回码都未在真机验证。
  ③ 模拟器拒绝保存 NE 配置的错误码与用户拒绝同为 NEVPNErrorDomain 5，所以模拟器上会提示“需要允许添加 VPN 配置”。
  ④ 扩展自行停连会撤销会话（既有行为），App 回来后依赖记住的密码静默重登。⑤ 本轮没有部署后端，demo 手工确认仍 NOT_RUN。
下一位从哪个文件/验收项开始；回退方法：
  有付费团队后按 docs/ios-tun-design.md 第 9 节与 docs/ios-billing-acceptance.md 第 3 节核真机项；然后进入第 5 轮（闸门/生命周期）。
  回退：撤销本分支；Go 与后端无改动。
```

## 附录 A. 源码核验索引

| 主题 | 依据 |
| --- | --- |
| 后端平台白名单 | `backend/internal/api/native.go:70` |
| 登录平台来源 | `native/core/internal/managed/platform.go` |
| 构建标签现状（ios 会匹配 darwin） | `internal/network/network_darwin.go`、`internal/engine/{platform_unix,sysdns,ipv6_route,tunfd_unix}.go` |
| gomobile 绑定面 | `native/core/mobile/mobile.go`（Platform、Start、Call、Stop、NetworkChanged、SetForeground） |
| 计量重基线（交接的依据） | `native/core/internal/meter/meter.go:125–150`（`Begin`） |
| 授权闸门 | `native/core/internal/managed/resume.go`、`manager.go:57–60,187` |
| sing-tun darwin fd 路径 | `sing-tun@v0.4.22/tun_darwin.go:81–112`；Mihomo `listener/sing_tun/server.go:155–166` |
| TUN 规格契约（Android 语义） | `shared/contracts/tun-spec-v1.json` |
| 可移植的 Swift 代码 | `apps/macos/Sources/AsterLinkShared/{CoreModels,ClientEnvironment,CredentialStore,SessionController}.swift` |
| UI 语义参照 | `docs/android-ui.md`、`apps/android/app/src/main/java/com/asterlink/vpn/ui/` |

## 附录 B. 外部复核入口（第 1 轮重新核对，不作为当前版本的证明）

- Apple：[NetworkExtension](https://developer.apple.com/documentation/networkextension)、[NEPacketTunnelProvider](https://developer.apple.com/documentation/networkextension/nepackettunnelprovider)、[App Review Guidelines（5.4 VPN、3.1.1 购买）](https://developer.apple.com/app-store/review/guidelines/)、[隐私清单](https://developer.apple.com/documentation/bundleresources/privacy-manifest-files)、[加密出口合规](https://developer.apple.com/documentation/security/complying-with-encryption-export-regulations)、[WWDC25 Filter and tunnel network traffic](https://developer.apple.com/videos/play/wwdc2025/234/)。
- 本次检索到的旁证：[NE 扩展内存上限讨论（Apple 论坛 73148）](https://developer.apple.com/forums/thread/73148)、[libXray 50 MB jetsam 案例](https://github.com/XTLS/libXray/issues/102)、[Go 隧道内存治理案例](https://github.com/p1neappleXpress/OpenFlux/pull/136)、[模拟器无法运行 NE 的说明](https://kean.blog/post/vpn-configuration-manager)。
- 工具：[gomobile](https://pkg.go.dev/golang.org/x/mobile/cmd/gomobile)、[Mihomo](https://github.com/MetaCubeX/mihomo)、[WireGuard for iOS（utun fd 查找做法参考）](https://github.com/WireGuard/wireguard-apple)。
