# AsterLink Android 原生客户端开发交接计划

> 编制日期：2026-10-15。状态：**规划完成，Android 实现尚未开始**。
> 勘察基线：`a0a8bf2`（PR #27 合并点，Windows 第 8 轮）；Mihomo 固定提交 `70f0570405c3c2c47bb113b88db95006d239b346`（`native/third_party/Clash.Meta`，FlClash 分支）。
> 项目根目录：`/Users/stevenlee/Desktop/vpn`；开发机：Mac mini M4（arm64，32 GiB，macOS 26.6.2）。无 Android 真机，开发与验收以 Android Studio 模拟器为主。
> 规划轮只新增本文件；第 1 轮起的实际进度见第 14 节状态表与逐轮记录。

## 1. 结论与交付边界

**推荐技术栈：Kotlin + Jetpack Compose（Material 3）原生 UI，`android.net.VpnService` 提供 TUN，复用现有 Go Managed Core 和内嵌固定版 Mihomo，用 gomobile 打成 AAR 在 App 进程内调用。共 6 轮开发。**

这是 Android 专属原生客户端，不是把 Next.js 网页套进 WebView，也不是复活已删除的 FlClash/Flutter。网站（Next.js + Mantine）继续负责管理员节点 YAML、套餐、倍率、购买续费；Go/Gin/GORM/PostgreSQL 后端继续是唯一业务控制平面。**后端不需要为 Android 改接口**：`POST /login` 已接受 `platform=android`（`backend/internal/api/native.go:70`），`/capabilities` 已在 `demo.hyshentou.cn` 返回 `contract_versions [1,2]`（本次实测）。

目标闭环与 macOS/Windows 相同：安装 → 登录（可记住密码）→ 同步套餐与有权线路 → 选线 → 系统 VPN 授权 → 真实 TUN 连接 → 速率与用量 → 按管理员倍率扣量 → 节点变更、额度耗尽、套餐到期时停止代理。用户看不到、导不出节点 YAML、地址或凭据。

| 等级 | 达成标准 | 计划轮次 |
| --- | --- | --- |
| 工程可构建 | AAR + APK 可复现构建，模拟器运行，Core hello 通过，CI 绿 | 第 1 轮 |
| 内部功能版 | 真实账号、SS/VLESS 线路、真实 TUN、开发计量与倍率扣量闭环、移动端生命周期可用 | 第 4–5 轮 |
| 可收费发布候选 | 签名 APK、可靠性矩阵、真实受管节点权威计量、真机抽检、外部运营门槛 | 第 6 轮；真机与受管节点缺失时只能标“内部渠道” |

“6 轮”是 6 个有明确验收点的开发批次，不保证 6 次对话无需修复。每轮失败项在本轮修。

**为什么 Android 比 Windows 少两轮：** ① Android 没有 Helper/Service 特权进程，App 沙箱 + 系统 VPN 授权替代了 macOS XPC 与 Windows Named Pipe 那一整套身份校验；② Go Core 的会话、计量、grant、状态机已在两个桌面端真机验证过，Android 只补平台层；③ **模拟器里的 TUN 只改变模拟器自己的网络，不动 Mac 的路由和 DNS**，所以 TUN、计费、生命周期测试都可以由开发者（包括 Claude）在本机自动执行，不必像 Windows 那样每轮等用户在真机上跑。

## 2. 已有工程与 PR 进程：从现状接续

### 2.1 本次核验范围

已完整阅读 `handoff_app.md`（712 行，macOS 5 阶段及真机修复）、`handoff_app_windows.md`（883 行，Windows 8 轮及记录），核对 GitHub PR #1–#27 的状态与 #21/#23 说明，以及以下源码：`native/core` 的 `cmd/asterlink-core`、`internal/{ipc,service,managed,engine,network}` 的入口与构建标签，`shared/contracts/*.json`，`backend/internal/api/native.go` 的平台白名单，固定 Mihomo 中所有 `//go:build android` 文件，`docs/managed-protocols.md`、`docs/windows-client-ui.md`。

本机工具链实测：Go `go1.26.6 darwin/arm64`；OpenJDK 17.0.20（Homebrew）；Homebrew `android-commandlinetools` 已有 `platforms;android-34/35/36`、`build-tools;34.0.0/36.0.0`、`ndk;28.2.13676358`、`cmake`、`platform-tools`；**未安装** Android Studio、`emulator` 包、任何 system image/AVD、gomobile；`/usr/libexec/java_home` 找不到 JDK（未设 `JAVA_HOME`）。`sdkmanager --list` 可见 arm64 镜像：API 26/28/29/34/35/36/36.1/37.0/37.1/37.2，含 `google_apis_ps16k`（16 KB 页）变体，`emulator 37.2.12`。根分区剩余约 42 GiB。

### 2.2 历史 PR 对 Android 的实际意义

| PR | 内容 | Android 接续方式 |
| --- | --- | --- |
| #1、#2、#5、#7 | 管理员节点目录、15 种协议白名单、删除同步、用户线路列表 | 原样复用；协议“能解析”≠ Android 实传通过 |
| #3、#10 | 后端独立构建、去 FlClash、共享契约迁移 | Android 读取同一份 `shared/contracts`，不新建契约副本 |
| #11、#12 | 登录/Keychain/状态机；TUN、唯一 Meter、租约、尾账 | 共用 Go 状态机；Keychain → Android Keystore |
| #13、#15 | capabilities、grant、NodeAgent、预算、节点权威计量 | Core 的 `connectGranted` 已完整，Android 只消费 |
| #16 | Mihomo 日志污染 stdout IPC | Android 无管道（进程内调用），但同样禁止日志进入数据通道；logcat 不得出现秘密 |
| #17 | VLESS 节点无 IPv6 出口“已连接但没网” | Android 用 VpnService 的“地址族阻断”实现早期拒绝（见 7.3） |
| #18 | 系统 DNS 不经隧道 | Android 由 `Builder.addDnsServer` 接管系统解析器；仍需测 Private DNS、Chrome DoH |
| #19 | 首次授权引导、界面不刷新 | Android 的 VPN 授权弹窗、通知权限必须有可见引导，批准后自动继续 |
| #20–#22 | Windows 工具链锁、Go 平台拆分、服务与 IPC | 照搬“构建标签拆分 + 拒绝静默占位”的做法；Android 不需要服务层 |
| #23 | Windows 三页 UI、SessionController、Credential Manager | **Kotlin SessionController 按 C# 版语义移植**（代次、action_error、stale_generation、静默重登一次、内核丢失） |
| #24 | billtest + trafficsink 计量验收 | 直接复用两个 Go 工具：sink 作受控目标；计费公式断言移植到 Android 仪器测试 |
| #25 | 挂起检测、IPv6 /64 签名、泄漏矩阵 | Android 需改造：手机息屏挂起是常态，不能每次都断开（见 7.5） |
| #26 | grant 验收工具 `--grants`、计量来源 UI 标识 | 复用工具与 UI 文案；本地 NodeAgent 可在模拟器里做完整 node_reported 闭环 |
| #27 | Inno 安装器、升级回退、许可清单 | 复用 `collect_licenses` 思路，按 `android/arm64`、`android/arm` 重新收集 |

`handoff_app.md` 前部“计划新增”有历史残留，以末尾交接追加和当前源码为准；NodeAgent/grant 已实现，不要重写。

### 2.3 本次源码核查发现的 Android 必改点

| 位置 | 现状 | Android 要求 |
| --- | --- | --- |
| `internal/managed/platform.go` | `runtime.GOOS` 非 darwin/windows 一律报 `linux` | 增加 `android` → `"android"`；单测覆盖 |
| `internal/network/network_other.go` | 标签 `!darwin && !windows`，**Go 的 android 目标会匹配**（android 隐含 linux 标签），出口绑定为空操作 | 改为 `!darwin && !windows && !android`，新增 `network_android.go`（protect 绑定、宿主提供的网络快照） |
| `internal/engine/platform_unix.go`、`sysdns.go`、`ipv6_route.go` | 标签 `!windows`，android 会链接 macOS 的 `networksetup`、`/sbin/route` 路径 | 标签加 `!android`，新增 `platform_android.go`、`sysdns_android.go`（不改系统 DNS）、`ipv6_android.go` |
| `internal/engine/mihomo.go` | `platformSupported()` 仅 darwin；TUN 由 sing-tun 自建设备、`auto-route` | Android 用 VpnService 交来的 fd：`file-descriptor`、`auto-route:false`、`auto-detect-interface:false` |
| `internal/engine/config.go` | 模板含 `auto-route/auto-detect-interface:true`，地址用 Mihomo 默认 | Android 变体：地址/MTU/DNS 与 Builder 同一常量生成；保留 `MATCH,node` + `MATCH,REJECT` |
| `internal/ipc/server.go` | `ServeWith` 循环里完成解码/版本/分发 | 抽出 `HandleOne(frame) Response`，管道服务与移动绑定共用同一严格解码，**不另写一套命令解析** |
| `Verify`（mihomo.go:174） | 在 Core 进程内发不绑定的 HTTP 探测，依赖“本进程流量也进 TUN” | 因此 Android **不能**用 `addDisallowedApplication(自身)` 防环，必须用 `protect()`（见 7.2） |
| 固定 Mihomo 的 Android 补丁 | `features.Android` 关闭回环检测、`IsSafePath` 恒为 true、`dns/patch_android.go` 的系统解析器需宿主注入 | 防环全靠 protect 正确性，必须实测 T05-loop；节点策略白名单继续拒绝任何文件路径/provider；模板不使用 `system` DNS |
| `tunnel/statistic` | `TotalTraffic(true)` 在 `patch.go`，`patch_android.go` 为空 | 计量口径与桌面一致（仅代理链路），仍需真机级对账 B01 |

## 3. 技术栈选择与 M4 开发方式

### 3.1 明确选择

| 层 | 选择 | 理由与约束 |
| --- | --- | --- |
| 语言 / UI | Kotlin 2.x（K2）+ Jetpack Compose + Material 3 | Google 当前主推的原生 UI；声明式、易做精致动效与深色模式；不引入 WebView、Flutter、RN |
| 架构 | 单 Activity + Navigation Compose；ViewModel + StateFlow（MVVM，单向数据流） | UI 只渲染 Core 快照，与 macOS/Windows 同构 |
| 依赖注入 | 手写 `AppContainer`（构造注入） | App 规模小，不引入 Hilt/kapt，构建更快 |
| 序列化 | kotlinx.serialization（`Long` 字段） | 超过 2^53 的 units 精确；禁止经 `Double` 解析账务数字 |
| 本地存储 | DataStore（偏好）+ Android Keystore AES-GCM（记住密码） | 不用已弃用的 EncryptedSharedPreferences；不存 token |
| 网页入口 | AndroidX Browser（Custom Tabs） | 注册、购买、续费、找回密码跳网站 |
| VPN | `VpnService` + 前台通知；可选 Quick Settings 磁贴 | 系统级 TUN，无需 root；用户一次授权 |
| 代理/业务内核 | 现有 `native/core` + 固定 Mihomo，**gomobile bind** 生成 AAR（JNI） | 会话、计量、grant 只有一份；固定 Mihomo 本就是 FlClash/CMFA 的 Android 分支，带 fd TUN 与 `DefaultSocketHook` |
| 构建 | Gradle Kotlin DSL + 版本目录 `libs.versions.toml` + Wrapper 锁定；Python 构建/测试脚本作单入口 | 与 macOS/Windows 脚本风格一致；CI 与本机同命令 |
| 测试 | JUnit/协程测试（JVM）、Roborazzi 截图测试（JVM）、Compose UI 测试与仪器测试（模拟器）、Go 测试（含交叉编译到 android/arm64 后在模拟器 shell 中运行） | 编译、UI、网络、账务四类证据分开 |
| 分发 | 首版官网直链下载签名 APK（按 ABI 拆分），不上 Google Play | Play 的 VPN 政策声明、国内商店对 VPN 的限制都属外部门槛；不内置自动安装更新 |

**版本锁定（第 1 轮执行，写入 `apps/android/toolchain.lock.json`）：** Go 1.26.6（沿用）；`golang.org/x/mobile` 固定提交；NDK `28.2.13676358`（已安装，默认 16 KB ELF 对齐）；`compileSdk 37`、`targetSdk 36`、`minSdk 26`（Android 8.0）；AGP、Gradle、Kotlin、Compose BOM 取第 1 轮时的稳定版并锁定；JDK 用 Android Studio 自带 JBR 21（或现有 OpenJDK 17，二选一并记录）。本文件不把未核实的版本号写成“最新版”。

**备选与否决：** Flutter/React Native 违背“原生”且会再造 FlClash 的问题；XML View 体系能做但开发效率与动效质量不如 Compose；把 Core 作为独立可执行文件放进 `nativeLibraryDir` 再 fork 运行，需要跨进程传 fd 并回调 `protect()`，复杂度高、收益低；手写 `c-shared` + JNI C 胶水可去掉 x/mobile 依赖，但工作量更大——第 1 轮若 gomobile 对固定 Mihomo 有阻塞，再按一次记录的决策切换。

### 3.2 单进程架构

```text
Next.js / Mantine 管理员页面 ── 配置套餐、节点、倍率
          ▼
Gin API / PostgreSQL ── mTLS ── 受管节点 NodeAgent / 转发器
          ▲ HTTPS（protect 后直连物理网络）
          │
┌──────── com.asterlink.vpn（一个 App 进程）───────────────────────┐
│ Compose UI ─ ViewModel ─ SessionController ─ CoreClient          │
│                                   │ JSON 命令（与 ipc-v1 同格式）  │
│                                   ▼                              │
│ AsterVpnService ◀─回调─ libgojni.so（Go Core + Mihomo）            │
│   Builder.establish() → TUN fd ─────▶ Mihomo TUN（fd）            │
│   protect(fd) ◀──── Go 出站 socket（节点外层、API 控制面）          │
│   ConnectivityManager → 底层网络快照 ──▶ Egress / Signature        │
└──────────────────────────────────────────────────────────────────┘
```

UI 与 VpnService 同进程（不拆 `:vpn` 进程）：系统绑定 VpnService + 前台通知使进程在后台保持存活；UI 崩溃会连带停止 VPN，符合“不留隐藏代理”。**Core 仍是 HTTP 会话、配置、租约、Meter 的唯一所有者**；Kotlin 不调用后端 API（网站链接除外）、不接触 token 与 YAML。

### 3.3 Go ↔ Kotlin 绑定（gomobile 包 `asterlink/core/mobile`）

绑定包必须放在 `native/core` 模块内（Go 的 `internal` 规则不允许外部模块导入）。导出面保持最小：

```go
type Platform interface {           // Kotlin 实现，由 AsterVpnService 持有
    Protect(fd int32) bool           // VpnService.protect
    OpenTun(specJSON string) int32   // Builder 按 spec 建 TUN，返回 detachFd()；失败 -1
    NetworkSnapshot() string         // 底层网络：签名、DNS 服务器、是否有 IPv6、是否计费网络
}
func Start(p Platform, buildJSON string) error // api_base/channel/app_version/数据目录，来自签名包内构建配置
func Call(requestJSON string) string            // 与 ipc-v1 帧体相同：hello/login/read_snapshot/…/shutdown
func NetworkChanged()                           // ConnectivityManager 回调提示，Core 自行判定
func SetForeground(visible bool)                // 仅影响目录长轮询节奏（见 7.6），不改授权
func Stop()                                     // 有界停流、结账、撤销
```

- `Call` 走 `ipc.HandleOne`：严格解码、未知字段拒绝、协议版本、帧上限、generation 校验全部沿用；契约测试直接读 `shared/contracts/ipc-v1.json` 与 `snapshot-v1.json`。
- 命令串行执行（与桌面单管道语义一致）；`read_snapshot` 能否与 `connect` 并发，第 1 轮按 `managed` 的锁语义测试后决定，不凭感觉放开。
- Kotlin 侧所有 `Call` 在 `Dispatchers.IO`；connect 预算 90 s，UI 显示进度并可“取消连接”（与 Windows 相同：取消 = 结束会话，需重新登录，界面如实说明）。
- `OpenTun` 的 spec 只含地址、前缀、MTU、DNS、是否启用 IPv6，**不含任何节点信息**。

### 3.4 Mac 上能做什么、不能替代什么

| 工作 | M4 + 模拟器 | 仍需真机/外部 |
| --- | --- | --- |
| 编写 Kotlin/Go、构建 AAR/APK、单测、截图测试 | 完整 | — |
| VpnService 真实 TUN、DNS、UDP、IPv6、20 次启停、进程被杀 | 可以（arm64 镜像经 Hypervisor 原生运行，只影响模拟器网络） | 厂商 ROM 差异 |
| Doze / 息屏 / Wi-Fi↔蜂窝切换 / 飞行模式 | 可以用 adb 模拟（`dumpsys deviceidle force-idle`、`svc wifi`、`cmd connectivity airplane-mode`） | 真实射频切换、真实省电策略 |
| 计费对账、grant 闭环 | 可以（本地后端 + 本地 NodeAgent + sink） | 真实公网受管节点 |
| 厂商后台杀进程（小米/华为/OPPO/vivo）、低端机性能、真实电量 | **不能** | 收费前至少一台真机或云真机抽检 |

**测试网络注意：** 模拟器流量最终经 Mac 网络出去。跑真实节点用例时必须**断开公司 VPN 并退出 macOS 版 AsterLink**，否则结果被套娃隧道污染（与 macOS/Windows 历史测试同一前提）。

## 4. 目录与模块规划（计划新增，当前不存在）

```text
apps/android/
  settings.gradle.kts / build.gradle.kts / gradle/libs.versions.toml / gradle/wrapper/
  toolchain.lock.json                  # SDK/NDK/AGP/Kotlin/Compose/x-mobile/Go/镜像 的锁定
  .env.example                         # 与 macOS/Windows 相同的三个公开键
  app/src/main/
    AndroidManifest.xml
    java/com/asterlink/vpn/
      AsterApp.kt / AppContainer.kt
      core/        CoreBridge.kt（gomobile 封装） CoreClient.kt Snapshot.kt Commands.kt
      session/     SessionController.kt ConnectionView.kt ErrorText.kt Formatting.kt
      vpn/         AsterVpnService.kt TunSpec.kt NetworkMonitor.kt VpnNotifications.kt ConnectTileService.kt
      security/    CredentialStore.kt（Keystore） DeviceIdentity.kt LoginPreferences.kt
      ui/          theme/ components/ login/ connect/ lines/ account/ navigation/
    res/           values/（zh-CN 默认） values-en/ xml/network_security_config.xml data_extraction_rules.xml
  app/src/test/          # JVM：控制器、格式化、契约、Roborazzi 截图
  app/src/androidTest/   # 模拟器：Compose UI、TUN/计费/生命周期验收（仅 debug 构建）
native/core/
  mobile/                              # gomobile 绑定（宿主可单测）
  internal/network/network_android.go
  internal/engine/{platform,sysdns,ipv6,tun}_android.go
scripts/
  build-native-android.py              # AAR + APK（debug/internal/release）
  test-native-android.py               # --suite foundation|unit|ui|emulator|tun|billing|grants|lifecycle|release
  package-native-android.py            # 签名、拆分 ABI、SHA256SUMS、release-info、许可材料
  android/                             # AVD 创建、adb 生命周期脚本、soak 采样
.github/workflows/android.yml
docs/android-{foundation,tun-design,ui,lifecycle,release}.md
artifacts/native-android/<run-id>/     # 不提交：报告、截图、APK
```

`applicationId`：`com.asterlink.vpn`（与 macOS 产品标识一致）。生成的 AAR 不提交；Gradle 任务 `buildCoreAar` 只调用 `build-native-android.py`，并核对 AAR 内嵌的 core 版本、Mihomo 提交与哈希。

## 5. API、配置与凭据：复用已有契约

### 5.1 原生接口（不新增）

前缀 `https://demo.hyshentou.cn/api/v1/client`，全部由 Go Core 调用：`POST /login`（`platform` 由构建目标决定为 `android`）、`GET /session`、`GET /nodes?revision=`（单长轮询，只观察目录、不续授权）、`GET /config?node_id=&last_sequence=`（client_reported）、`POST /traffic`（唯一 Meter）、`POST /logout`、`GET /capabilities`、`POST /grants`、`GET /grants/:id`、`POST /grants/:id/revoke`。包装层、错误码、日期与整数格式以 `shared/contracts/native-v1.json`、`native-v2.json` 为准。

业务时限沿用后端已有参数：`CLIENT_REPORT_INTERVAL_SECONDS=60`、`CLIENT_LEASE_SECONDS=90`、`CLIENT_IDLE_TIMEOUT_SECONDS=180`、`CLIENT_NODE_WATCH_SECONDS=5`，客户端只用服务端返回值。

### 5.2 `.env` 的归属

保持 `frontend/.env`、`backend/.env` 各管各的；新增 `apps/android/.env`（不提交）和 `.env.example`：

```dotenv
APP_ENV=development
API_BASE_URL=https://demo.hyshentou.cn/api/v1/client
WEBSITE_BASE_URL=https://test.hyshentou.cn
```

它是**构建时输入**：Gradle 用与 macOS/Windows 相同的白名单规则解析（测试用例直接读 `shared/contracts/env-v1.json`），生成 `BuildConfig` 常量并写入 AAR 构建 JSON；改了必须重新构建签名安装。拒绝重复键、未知键、shell 展开、URL 内凭据；Release 必须 HTTPS。开发连本机后端时用 `adb reverse tcp:<port> tcp:<port>` 后写 `http://127.0.0.1:<port>/api/v1/client`，保持“HTTP 仅限回环”的现有契约，**不为模拟器的 10.0.2.2 开新例外**。`network_security_config.xml` 禁明文、只信任系统 CA。

数据库账号、节点密码、NodeAgent 私钥、签名密钥库、用户密码、token 不进入任何 `.env`、源码或 APK。Android 不连接 PostgreSQL。聊天中出现过的数据库密码与节点凭据在对外测试前轮换；本文件不复制它们。

### 5.3 记住密码、设备 ID、备份

- 仅用户勾选“记住密码”时，用 Keystore 中不可导出的 AES-GCM 密钥加密后存入 DataStore，条目按 `API base + 规范化邮箱` 隔离；服务器返回 `invalid_credentials` 时删除该条；“退出登录”删除密码与邮箱；普通退出保留。保存失败只提示，不降级明文。
- 启动时若有记住的密码则**在线**自动登录；不保存 token，进程被杀后必须重新在线登录。
- `device_id`：首次启动生成随机 UUID 存于 App 私有目录；不读 ANDROID_ID、IMEI、MAC。
- `android:allowBackup="false"` + `dataExtractionRules` 排除全部：防止密文与 device_id 被云备份/迁移到另一台设备（Keystore 密钥本就不随迁移，恢复后的密文只会解密失败）。
- 测试账号会因模拟器重置产生新 device_id，可能触发 `device_limit`；验收前在后台清理旧会话或使用专用测试账号。

## 6. Go Core 的 Android 平台层

| 能力 | Android 实现 |
| --- | --- |
| 登录平台 | `ClientPlatform()` 返回 `android`（来自 GOOS，不接受 UI 传入） |
| 出站防环 | `dialer.DefaultSocketHook` 与 `Egress.control` 都调用宿主 `Protect(fd)`；固定 Mihomo 在设置该钩子时忽略接口名/路由标记（CMFA 路径） |
| 控制面 DNS | `Egress.SetResolvers` 填宿主给出的底层网络 DNS 服务器，经 protect 的套接字查询；API 域名不经 fake-ip |
| 物理网络/签名 | `PhysicalDefault`/`Signature` 来自 `NetworkSnapshot()`（ConnectivityManager 默认网络 + LinkProperties，IPv6 只取 /64） |
| 竞争 VPN | 系统保证同一时刻只有一个 VPN；`Conflicts()` 返回空；其它 VPN 抢占时由 `onRevoke` 通知 Core 停流结账 |
| TUN 创建 | Core 先过闸门（会话、配置、Meter 首个 ACK），再调 `OpenTun(spec)`；拿到 fd 写入模板 `tun.file-descriptor`，`ReCreateTun` 后检查监听器确实在读该 fd |
| 系统 DNS | 由 `Builder.addDnsServer` 指向隧道内地址，`sysdns_android.go` 不修改任何系统设置，也就没有崩溃后残留 |
| IPv6 | 连接前经节点做一次 IPv6 探测（复用 `ipv6.go` 意图），结果决定 spec 是否含 IPv6 地址与 `::/0` 路由 |
| Verify | 保持“经 TUN 的真实探测”：Core 进程未 protect 的套接字按系统规则进入 TUN，这正是要验证的路径 |
| 时钟 | Go 单调时钟在 Android（Linux CLOCK_MONOTONIC）休眠时暂停，与 macOS 相同，现有“墙钟 vs 单调钟”判据有效；但触发后的处置要改（7.5） |

## 7. TUN、DNS、生命周期与移动端特有问题

### 7.1 固定运行模板（Android 变体）

```yaml
tun:
  enable: true
  stack: system            # 第 2 轮先验证 system；不行才经一次记录的决策改 gvisor/mixed，不得运行失败自动回退
  file-descriptor: <fd>
  auto-route: false        # 路由由 VpnService.Builder 负责
  auto-detect-interface: false   # Android 11+ 普通 App 不能读 netlink 路由
  inet4-address: [198.18.0.1/30] # 与 Builder.addAddress 同一常量
  inet6-address: [fdfe:dcba:9876::1/126]   # 仅节点可承载 IPv6 时
  mtu: <与 Builder.setMtu 相同>
  dns-hijack: [any:53]
dns: {enable: true, enhanced-mode: fake-ip, fake-ip-range: 198.18.0.1/16, nameserver: [tcp://1.1.1.1#node, tcp://8.8.8.8#node], ...}
rules: [MATCH,node, MATCH,REJECT]
```

管理员 YAML 只贡献一个代理定义；无监听器、controller、provider、脚本、文件路径。`BuildConfig` 增加 `goos == "android"` 分支并沿用“任意主机可测各平台文档”的写法。

**中国大陆网络风险（记录，不在本计划内改）：** 模板的 `default-nameserver`/`proxy-server-nameserver` 为 `1.1.1.1`，节点域名的引导解析在部分大陆网络可能慢或被污染；三端共用该模板，如需改为“宿主提供的底层 DNS 优先”，属于共享改动，须三端回归。

### 7.2 VpnService.Builder 配置

- `addAddress(198.18.0.1, 30)`、`addRoute(0.0.0.0, 0)`；节点支持 IPv6 时再 `addAddress(fdfe:dcba:9876::1, 126)` + `addRoute(::, 0)`。
- `addDnsServer(198.18.0.2)`；`setMtu` 与模板一致；`setSession("AsterLink")`；`setConfigureIntent` 打开 App。
- `setUnderlyingNetworks(null)` 跟随默认网络；`setMetered(false)`（API 29+）让 VPN 的计费属性跟随底层网络——第 2 轮按目标 SDK 文档核实语义后再定。
- **不调用** `allowBypass()`、`setHttpProxy()`、`addDisallowedApplication(自身)`。不排除自身的原因：`Verify` 与 Android 系统解析器都依赖“本进程未 protect 的流量也进 TUN”；控制面与节点外层连接靠 protect 精确放行。
- 局域网：首版不排除私网段（全局单节点，最简单且不泄漏），代价是连接时投屏/打印机等局域网设备不可达，在“帮助”中说明；是否增加“允许局域网”开关（API 33+ `excludeRoute`）留到第 5 轮按测试结果决定，不默认做。
- Manifest：`<service android:name=".vpn.AsterVpnService" android:permission="android.permission.BIND_VPN_SERVICE" ...>` + `android.net.VpnService` intent-filter；`<meta-data android:name="android.net.VpnService.SUPPORTS_ALWAYS_ON" android:value="false"/>`，首版**不支持始终开启 VPN**（避免无登录、无用户确认时被系统拉起，与桌面“不开机自动连接”一致）。
- 前台服务类型、`FOREGROUND_SERVICE_*` 权限、`POST_NOTIFICATIONS`（Android 13+）按第 1 轮锁定的 targetSdk 官方文档确定，不为了“能跑”随意填写类型。通知权限被拒时 VPN 仍可工作（系统钥匙图标仍显示），设置页提示。

### 7.3 必须实现和验证的网络行为

| 项目 | 要求 |
| --- | --- |
| 真 TUN | 不设置系统/WLAN HTTP 代理，任意 App 的 TCP/UDP 经选中节点；节点侧计数或受控 sink 证明路径 |
| 防环 | 节点外层、API、引导 DNS 的套接字全部 protect；固定内核在 Android 关闭了回环检测，所以 T05-loop 是硬性用例 |
| DNS | 系统解析器（getaddrinfo/netd）→ 198.18.0.2 → any:53 劫持 → fake-ip；分别测 Private DNS 关/自动/严格模式与 Chrome DoH；严格模式的 DoT 经节点转发不算泄漏，但须记录 |
| IPv6 | 节点能承载：完整代理；不能承载：Builder 不加任何 IPv6 地址/路由/DNS，系统按地址族**直接阻断** IPv6（`connect` 立即失败，应用回退 IPv4），既不外泄也不出现“握手后假死”。这比 macOS 的 reject 路由更干净 |
| UDP/QUIC | `udp:true` 实传；`udp:false` 必须被 `MATCH,REJECT` 拒绝，绝不 DIRECT |
| 控制面 | VPN 运行中 Core 仍能经物理网络访问 API；API 不可达时按租约停流，不依赖付费节点 |
| 停止清理 | `dumpsys connectivity` 无本 App 的 VPN 网络；fd 关闭；通知移除 |
| 进程被杀 | `am force-stop` / 系统回收 → fd 随进程关闭，VPN 消失，无孤儿转发；重新打开需在线登录 |
| WebRTC | 模拟器 Chrome 打开 WebRTC 泄漏页：公网物理出口不得出现；私网/mDNS 单独记录 |

### 7.4 状态机与操作顺序

沿用 Core 状态：`SignedOut → Authenticating → Ready → Preparing → Connecting → Connected → Stopping/Settling`，任意阶段可进入 `Blocked` 或 `Error`。Android 在 UI 层多一个前置步骤：

```text
点击连接 → VpnService.prepare() 是否需要授权？
   需要 → 系统授权弹窗 → 用户拒绝：保持 Ready，提示“需要允许 VPN 连接”
                       → 用户允许：继续
→ startForegroundService(AsterVpnService) → Core connect（闸门 → Meter 首个 ACK → OpenTun → Verify）
→ 只有 Verify 通过才显示“已连接”
```

切线路/倍率变化/禁用节点：先停新流和存量流 → 等计数稳定 → 结清旧段 → 新配置 → 保持断开，用户确认后再连（不做无缝切换）。“退出登录”= 停流 → 有界结账/撤销 → 清记住的密码。从最近任务划掉 App 不应断开（服务仍在）；通知上的“断开”与 App 内断开等价。

### 7.5 移动端关键改造：息屏挂起后的“授权闸门”

**问题：** 现有 `clockWatch`（`managed/tunnel.go:371`）检测到墙钟与单调钟偏差超过 5 s 就判定“系统休眠”并 `stopTunnel`。桌面休眠少见，这样做合理；但手机息屏后 CPU 频繁深度睡眠，每次被网络包或解锁唤醒都会触发，结果是**手机息屏几分钟后 VPN 就断开，用户必须手动重连**——商业产品不可接受。同时 90 秒租约在长时间息屏后确实已过期，不能直接继续放行。

**方案（仅 Android 启用，桌面行为不变）：** 新增 `managed.Options.ResumePolicy`，Android 取 `gate`：

1. 检测到挂起/租约过期 → 立即关闸：固定 Mihomo 的 tunnel 置为挂起状态（第 5 轮先核实固定提交中 `OnSuspend/OnRunning` 的具体语义）并关闭存量连接；**保留 TUN fd**，所以流量仍被系统送进隧道、只是不被转发——不泄漏、不白用。
2. 在线重验：`/session`，有 pending 时先 `/traffic` 原批重试。
3. 授权有效 → 开闸恢复，UI 显示“已连接”；`can_connect=false`（到期/额度/禁用）→ 按原流程完整停流结账并显示原因；网络不可达 → 保持关闸并带退避重试，UI 显示“等待网络”，超过上限（第 5 轮定值）才完整断开以省电。

该改动触及共享 `managed` 包，必须保持 macOS/Windows 默认 `stop` 策略且跑三端回归。

### 7.6 省电与后台

- Go 的 1 s 看门狗只在 CPU 醒着时运行，不会唤醒设备；真正耗电的是网络请求。`/nodes` 长轮询约每 5 秒一次，后台会让蜂窝射频持续活跃：Android 在 UI 不可见时（`ProcessLifecycleOwner` ON_STOP）调用 `SetForeground(false)` 暂停目录长轮询，回到前台立即刷新。**前提：** 第 5 轮先测清楚“管理员改当前线路/倍率”在后台时是否仍由 `/session`、`/traffic` 的服务端裁决强制停连、最迟多久；若停连只依赖目录 watcher，则不得暂停，改为降低频率并写明。
- Doze/App Standby：用 `adb shell dumpsys deviceidle force-idle` 实测控制面是否被限制；设置页提供“后台运行与省电”引导，跳转系统电池优化设置，由用户自行决定，不擅自申请豁免权限。
- 厂商 ROM 后台清理只能在真机验证，列为发布前外部门槛。

## 8. 计费：口径不变

```text
up_delta      = current_upload   - acknowledged_upload
down_delta    = current_download - acknowledged_download
charged_units = (up_delta + down_delta) * bound_rate_permille
remaining_bytes = max(0, floor((total_bytes * 1000 - used_units) / 1000))
```

1 unit = 1/1000 字节；500‰=0.5×，1000‰=1×；上传、下载都计。例：上传 20 MiB + 下载 80 MiB = 104,857,600 字节，1× 扣 104,857,600,000 units，0.5× 扣 52,428,800,000 units。`line_type=direct`（普通线路）仍是远端代理，不是 Mihomo DIRECT；倍率来自管理员 `rate_permille`，UI 不按类型硬编码。UI 用 2^30 字节显示 GB 时注明单位，物理用量与计费用量分开显示。

`client_reported` 是开发账本，不能防止改客户端少报；商业收费必须走 `node_reported`（每 grant 独立凭据、节点计数、预算预留、撤销与租约截止）。Android 不新建任何账本，Core 原样消费。

## 9. 商业化 UI 设计规范

### 9.1 设计原则

一屏一个主操作（连接）；状态是视觉中心；数字可信（确认余额与预估分开，不填假延迟、不做假测速）；克制（不做地球、地图、炫光、仪表盘）；所有状态都有文字 + 图形，不只靠颜色；首次使用的每个系统授权都有一句话解释。

### 9.2 设计令牌（与 Windows 客户端品牌色一致）

| 令牌 | 浅色 | 深色 |
| --- | --- | --- |
| Primary（品牌蓝） | `#2F6FEB` | `#5B8DEF` |
| 已连接 | `#16A34A` | `#22C55E` |
| 连接中/等待 | `#D97706` | `#F59E0B` |
| 错误 | `#DC2626` | `#F87171` |
| Background / Surface | `#F6F8FB` / `#FFFFFF` | `#0B1220` / `#121A2B` |
| 主文字 / 次文字 / 描边 | `#0F172A` / `#5B6475` / `#E3E8EF` | `#E6EAF2` / `#98A2B3` / `#243049` |

- 关闭 Material You 动态取色，保证品牌一致；提供“跟随系统 / 浅色 / 深色”。
- 字体用系统字体（中文设备为 Noto Sans CJK），不内置字体以控制包体；层级：状态大字 32/40 SemiBold、标题 20/28、正文 15/22、辅助 13/18；数字使用等宽数字（`tnum`），速率跳动不抖动。
- 4/8 dp 网格；页面左右 20 dp；卡片圆角 16 dp、1 dp 描边、几乎无阴影；主按钮药丸形 56 dp 高；连接按钮直径 168 dp；触控目标 ≥ 48 dp。
- 图标：Material Symbols Rounded，只打包用到的矢量。
- 动效 200–300 ms；连接中为环形进度扫描，连接成功一次性柔和缩放 + 颜色过渡；系统关闭动画时全部降级为无动画。
- 启动用 SplashScreen API；自适应图标 + Android 13 单色主题图标。品牌图形目前仓库内没有素材，第 3 轮先做简洁矢量占位，正式 Logo 由用户提供后替换。

### 9.3 页面结构（登录后三个标签：连接 / 线路 / 我的）

```text
┌────────────────────────────────┐
│ AsterLink                 VIP ▸ │  ← 顶栏：品牌 + 套餐徽标
│                                │
│            ╭──────╮            │
│           │   ⏻    │           │  ← 168dp 圆形主按钮，外环表示状态
│            ╰──────╯            │
│        已连接 · 00:12:31         │  ← 状态文字（liveRegion 播报）
│                                │
│ ┌ 当前线路 ──────────────── ▸ ┐ │
│ │ [HK] 香港 01   专线  计费 1.0× │ │  ← 点按进入线路页
│ └────────────────────────────┘ │
│ ┌ ↑ 1.2 MB/s     ↓ 8.4 MB/s   ┐ │  ← 仅连接后出现
│ │ ⌇⌇⌇⌇⌇ 60 秒速率曲线 ⌇⌇⌇⌇⌇⌇   │ │
│ │ 本次用量 312 MB · 计费 312 MB │ │
│ └────────────────────────────┘ │
│ ┌ VIP 套餐 · 2026-12-31 到期    ┐ │
│ │ ██████████░░░  剩余 68.2 GB   │ │  ← 确认余额；预估另起一行小字
│ └────────────────────────────┘ │
├────────────────────────────────┤
│   ⏻ 连接      ☰ 线路      ◉ 我的   │
└────────────────────────────────┘
```

| 页面 | 内容 | 主要操作 |
| --- | --- | --- |
| 登录（入口态） | 品牌标识、邮箱（自动填充提示）、密码（可见切换）、记住密码开关、全宽“登录”、“注册账号/忘记密码”（跳网站）、版本号 | 登录；错误显示在字段下方，网络问题用 Snackbar |
| 连接 | 上图；未选线时按钮下方提示“请选择线路”；无套餐/到期/额度为 0 时按钮置灰并给“购买/续费”按钮 | 连接/断开；连接中可取消 |
| 线路 | 搜索框、分段筛选（全部/专线/普通）、按地区分组的列表；行内：地区徽标、名称、“专线 · 计费 1.0×”、计量来源小字（节点实测/内部测试线路）、选中标记；下拉刷新 | 选择线路；连接中切换先弹“将断开并结算”确认 |
| 我的 | 账户卡片（邮箱首字母头像）、套餐卡片（到期、余额、购买/续费）、外观、后台运行与省电、系统 VPN 设置、关于（版本、内核版本、Mihomo 提交）、复制脱敏诊断、隐私政策/用户协议、退出登录（二次确认） | 跳网站续费；退出登录 |

空态/异常态都要完整设计：无套餐（管理员账号也一样）、已到期、额度用尽、无可用线路、API 离线、需要更新（`client_upgrade_required` → 官网下载）、VPN 授权被拒、被其它 VPN 抢占、连接失败（带原因码对应的中文和一个可执行按钮）、等待网络（闸门关闭中）。

前台通知（低优先级、无声）：标题“AsterLink · 已连接”，内容“香港 01 · ↑1.2 MB/s ↓8.4 MB/s”，操作“断开”。可选 Quick Settings 磁贴：已登录且已选线时一键连接/断开，否则打开 App。

文案：中文为默认语言，英文资源同步建立；状态用词固定为“未连接 / 正在准备 / 正在连接 / 已连接 / 正在重新验证 / 等待网络 / 正在断开并结算 / 无法连接”。错误码→文案沿用 Core 的中文消息，UI 逻辑只看 code。

可访问性：TalkBack 标签齐全、状态变化播报、对比度 ≥ 4.5:1、字体放大 200% 不截断（页面可滚动）、360 dp 宽小屏可用、横屏与平板可用但不专门优化。

## 10. 六轮开发安排与逐轮验收

### 第 1 轮：环境锁定、Go 平台拆分、gomobile 绑定与工程骨架

**目标：** 先证明“Go Core + 固定 Mihomo → AAR → Compose App → 模拟器”整条构建链可复现，避免做完 UI 才发现绑定不可用。

**开发内容：**
- 环境：安装 Android Studio（含 JBR）、`emulator`、`system-images;android-36;google_apis;arm64-v8a`（主力）、`android-36;google_apis_ps16k;arm64-v8a`（16 KB 页）、`android-29;google_apis;arm64-v8a`、`android-26;google_apis;arm64-v8a`（最低版本冒烟），按需 `android-37.0;google_apis;arm64-v8a`。统一一个 `ANDROID_HOME`（不要让 Homebrew 与 Studio 两套 SDK 并存），设置 `JAVA_HOME`。磁盘保持 ≥ 20 GiB 余量。安装固定提交的 gomobile。
- Go：`ClientPlatform` 增加 android；所有 `!windows`/`!darwin && !windows` 标签补 `!android`；新增 android 平台文件，第 1 轮 TUN 尚未实现时 `Start` 明确返回 `tun_unavailable`（不得链接空实现假装成功）；抽出 `ipc.HandleOne`；新增 `native/core/mobile`（Start/Call/Stop/Platform），宿主单测用 `ipc-v1.json` 全部案例。
- Android：Gradle 工程、版本目录、`toolchain.lock.json`、`.env` 解析与 env-v1 用例、Compose 骨架（主题令牌 + 一个显示 Core hello 的页面）、Manifest 基线（备份关闭、明文禁止）。
- 脚本：`build-native-android.py`、`test-native-android.py --suite foundation|unit`；CI `android.yml`（Linux：gomobile bind + assembleDebug + JVM 测试；并保留 macOS/Windows 原有作业）。
- 文档：`docs/android-foundation.md`（版本锁、SDK 布局、16 KB 对齐检查方法、包体基线）。

**验收：** AAR 含 `arm64-v8a`（CI 另含 `x86_64` 供 Linux 模拟器）；`llvm-readelf -l` 显示 LOAD 段 16 KB 对齐；APK 在 API 36 模拟器安装启动并显示 Core 版本与 Mihomo 提交；绑定层对 `ipc-v1.json` 全部案例结果一致；`login` 请求体单测断言 `platform=android`；macOS `go test ./...`、`scripts/test-native.py`、`test-native-macos.py --suite unit` 与 Windows CI 不回归。

**交付：** 可构建骨架、工具链锁、Android CI、记录 APK/so 体积基线。

### 第 2 轮：VpnService 与真实 Android TUN PoC

**目标：** 在模拟器上证明真实 TUN 能创建、转发、停止、被杀后清理，且无回环、无 DIRECT 回落。

**开发内容：** `AsterVpnService`（授权、前台通知、onRevoke、onDestroy 有界停止）、`TunSpec` → Builder、`NetworkMonitor`（ConnectivityManager 回调 → `NetworkChanged`）；Go 侧 fd 模板、`DefaultSocketHook`/`Egress` → `Protect`、IPv6 探测决定 spec、`tunReady` = fd 已被监听器接管、Verify 经 TUN。debug-only 仪器测试 `TunAcceptanceTest`，由 `test-native-android.py --suite tun --allow-network-changes` 驱动（仅改变模拟器网络），授权用 `adb shell appops set com.asterlink.vpn ACTIVATE_VPN allow` 预置。受控节点两种：① Mac 上运行现有 `native/nodeagent` 的 VLESS 参考转发器（模拟器经 `10.0.2.2` 访问，自动化、可进 CI）；② 用户提供的真实 SS 与 VLESS Reality 节点（凭据经 `adb shell run-as` 写入 debug 包私有目录的临时文件，用后删除，不走命令行参数、不进日志）。

**验收：** T01 TCP 经 TUN（节点计数/sink 证明）与“未设系统代理”；T02 SS 与 VLESS(Reality) 真握手，错误凭据失败；T03 系统解析器进 fake-ip、any:53 劫持、Private DNS 三种模式记录；T03-udp `udp:true` 通过、`udp:false` 被拒；T04 IPv6 两种节点（有 v6：完整代理；无 v6：地址族阻断且应用回退 IPv4）——若模拟器无 IPv6 上游则记 BLOCKED，不记 PASS；T05 VPN 运行中 API 可达 + T05-loop；T07 停止后无 VPN 网络；T09 `am force-stop` 后无残留；T-CYCLES 20 次 goroutine/fd 无累积。`stack: system` 若失败，写一次决策记录再换栈。

**交付：** `docs/android-tun-design.md`（资源所有者、防环、DNS/IPv6/UDP 方案、实测结论）、tun 报告 JSON。

### 第 3 轮：登录、记住密码、套餐与线路同步 + 正式 UI

**目标：** 网站管理员配置的数据进入 Android App，三页界面达到商业品质的设计主干。

**开发内容：** Kotlin `CoreClient`（类型化命令）、`SessionController`（按 Windows C# 版移植：代次丢弃、action_error、stale_generation、静默重登一次、内核丢失、连接中轮询 1 s / 其它 2 s、前台/唤醒时 `refresh_catalog` 节流 10 s）；`CredentialStore`、`DeviceIdentity`、`LoginPreferences`；第 9 节全部页面、空态、深色、网站 Custom Tabs、启动页、占位图标；Roborazzi 截图 ≥ 20 个状态（登录、无套餐、到期、额度 0、未选线、已选线、连接中、已连接、错误、线路空态/搜索无结果、我的、深色、200% 字号、360 dp）。契约测试读取 `snapshot-v1.json`、`native-v1.json` 错误码。

**验收：** A01 正确/错误密码、保存/取消/删除密码、重启后在线自动登录、不存 token；A02 `test@test.com` 等管理员无有效套餐时不能连接，到期、0 额度、设备上限都有对应空态；A03 单 watcher、429 退避、旧代次丢弃；A04 后台改名/倍率/停用/删除/套餐白名单在前台约 5 秒内反映（记录实测值）。用本地隔离后端（`scripts/test-native.py` 的 PostgreSQL + `adb reverse`）自动化；再用 demo 服务器 + 用户的测试账号手工确认一次。UI、logcat、诊断文本中搜不到 token、密码、YAML、节点地址。

**交付：** 可用的账号与线路界面、截图集（存 artifacts）、`docs/android-ui.md`。

### 第 4 轮：连接闭环、开发计量与通知

**目标：** 达成用户最关心的主路径：登录 → 选线 → 连接 → 正常上网 → 真实用量 → 按倍率扣量 → 到期/用尽/禁用自动停连。

**开发内容：** 连接按钮接入授权弹窗与 Core connect；只有 Verify 通过才显示“已连接”；速率、60 秒曲线（Canvas，有界采样）、本次物理用量与计费用量、确认/预估余额、pending 提示、计量来源标识；切线路确认对话框；前台通知实时速率与“断开”；可选磁贴。计费验收：把 `asterlink-billtest` 的断言（倍率公式、ACK 丢失原批重试）移植为仪器测试，传输目标用 `asterlink-trafficsink`（公网节点用例需部署在节点可达的服务器，用完即停）；ACK 丢失注入只存在于 debug 构建。node_reported：本地后端 + 本地 NodeAgent + 模拟器 App 跑完整 grant 闭环（B04 无客户端上报仍计费、误调 `/traffic` 被拒）。

**验收：** B01 1× 与 0.5× 逐单位精确（计数与正文的差异单独解释）；B02 丢 ACK 同批重试只扣一次；A04 改倍率/停用节点/套餐到期/额度耗尽均自动停连且尾账结清；模拟器 Chrome 能正常浏览、视频播放；B04 本地闭环通过。真实公网受管节点不存在时 B04-live 记 BLOCKED。

**交付：** Android 内部功能 APK（debug/internal 渠道）、计费报告、`docs/android-billing-acceptance.md`。`client_reported` 仍为开发计量，不解除生产保护。

### 第 5 轮：移动端生命周期与可靠性

**目标：** 手机日常使用（息屏、切网络、被系统回收）下 VPN 稳定，且不因此放宽授权。

**开发内容：** 7.5 的授权闸门（仅 Android 策略）及其单测；7.6 的前后台目录轮询策略（先测后定）；省电引导；`onRevoke`（用 androidTest 内的极简“竞争 VPN”测试 App 抢占）；通知权限拒绝路径；可选“允许局域网”决策；`scripts/android/lifecycle.sh` 与 soak 采样（`dumpsys meminfo`、`top`、fd 数、电量统计 `dumpsys batterystats`）。

**验收：** L01 息屏 10 分钟后解锁，VPN 不需手动重连，期间无转发泄漏，闸门关闭时长与原因有记录；L02 Doze（`force-idle`）进出；L03 Wi-Fi↔蜂窝（`svc wifi disable/enable`）与飞行模式，网络签名变化后先关闸再重验；L04 其它 VPN 抢占 → 停流结账、提示；L05 划掉最近任务不中断、`am kill`/`force-stop` 无残留；L06 重启后不自动连接；L07 20 次启停与 8 小时连接采样（内存/CPU/fd/goroutine 曲线，不填猜测数字）；L08 Private DNS 严格模式、Chrome DoH、WebRTC 页面；macOS/Windows 回归（共享 `managed` 改动）。

**交付：** `docs/android-lifecycle.md`、故障矩阵、性能基线与已知限制（含“厂商 ROM 未验证”）。

### 第 6 轮：打磨、签名发布与交接

**目标：** 普通用户从官网下载即可安装、升级、卸载；视觉与可访问性达标；交付可追溯。

**开发内容：** UI 最终打磨（逐屏截图评审、TalkBack、200% 字号、深色、小屏）；R8 与 gomobile keep 规则；按 ABI 拆分 APK（`arm64-v8a` 必选，`armeabi-v7a` 按包体与需求决定），so 不压缩且 16 KB 对齐；`apksigner` v2/v3 签名，发布密钥库由用户保管（不进仓库，丢失将无法升级，需离线备份）；`package-native-android.py` 产出 APK、`SHA256SUMS`、`release-info.json`（`trusted_release=false` 直到门槛全过）、依赖与许可清单（按 `android/arm64`、`android/arm` 收集 Go 依赖 + Mihomo GPL 源码归档 + AndroidX 许可）；`min_app_version` → App 内“需要更新”跳官网；网站下载页增加 Android 入口（地址与哈希放 `frontend/.env`，可选）。

**验收：** R01 签名 APK 在 API 26/29/36/37 与 16 KB 页镜像干净安装、首次授权、登录连接；R02 同签名覆盖升级保留记住的密码与设置、降级被系统拒绝、卸载后 VPN 配置与数据全部消失；R03 `--suite release` 审计（APK 内无测试代码/测试开关/调试证书、无秘密字符串、`debuggable=false`、ABI 与对齐正确、许可材料齐全）；R04 至少一台真机或云真机抽检（无法获得时记 BLOCKED，并在 release-info 中保持 `trusted_release=false`）。

**交付：** 发布资料、`docs/android-release.md`（安装/升级/卸载/回退/已知限制）、本文件最终进度。

## 11. 统一验收矩阵

每项输出 `case_id / expected / observed / PASS|FAIL|BLOCKED|NOT_RUN / commit / build / emulator(API, ABI, page size) / evidence`。**编制时下表全部 NOT_RUN。**

| ID | 场景 | 通过要求 | 轮次 |
| --- | --- | --- | --- |
| E01 | AAR/APK 可复现构建、16 KB 对齐 | 版本锁一致；Go 真实引擎，非 fakeengine | 1、6 |
| E02 | 共享契约（ipc/snapshot/env/native-v1/v2） | Kotlin 与 Go 同一夹具通过；>2^53 整数精确 | 1、3 |
| A01 | 登录、记住密码、退出登录 | 在线认证；Keystore 隔离；无 token 持久化 | 3 |
| A02 | 管理员无套餐、到期、0 额度、设备上限 | 不下发代理授权，不因角色放行 | 3–4 |
| A03 | 目录 watcher、429、旧代次 | 单 watcher、退避、旧响应丢弃；目录读取不续授权 | 3 |
| A04 | 后台改名/倍率/停用/删除/白名单 | 同步并停旧连接，旧段旧倍率 | 3–4 |
| T01 | 无系统代理 TCP 经 TUN | 节点/sink 证明路径 | 2 |
| T02 | SS AEAD、VLESS Reality 握手 | 真实实传；错误凭据失败 | 2、4 |
| T03 | 系统 DNS、any:53、Private DNS、Chrome DoH | 无未声明的物理出口查询 | 2、5 |
| T04 | IPv6 有/无出口 | 完整代理或地址族阻断 + IPv4 回退 | 2 |
| T05 | 控制面可达、防环 | protect 正确；无自循环 | 2 |
| T06 | UDP/QUIC，`udp:false` | 支持则经节点，不支持则 REJECT | 2 |
| T07 | 停止、被杀、20 次循环 | 无残留 VPN、fd/goroutine 不累积 | 2、5 |
| B01 | 20 MiB 上 + 80 MiB 下，1×/0.5× | 逐单位精确，差异解释 | 4 |
| B02 | ACK 丢失、重放 | 原批重试只扣一次 | 4 |
| B04 | node_reported 无客户端上报 | 节点照常计费；`/traffic` 被拒 | 4（本地）、6（公网受管节点） |
| L01–L08 | 息屏、Doze、切网、抢占、划卡、重启、长测、DNS/WebRTC | 见第 5 轮 | 5 |
| U01 | 截图集、TalkBack、200% 字号、深色、小屏 | 无截断、无假状态、状态可播报 | 3、6 |
| R01–R04 | 安装、升级、卸载、发布审计、真机抽检 | 见第 6 轮 | 6 |
| G01 | 后端、网站、共享策略、macOS、Windows 回归 | 不因 Android 削弱既有保护 | 每轮 |

测试纪律沿用前两份计划书：未运行不能写通过；缺目标/凭据/环境时写 BLOCKED（脚本退出码 3）；不用 fakeengine 结果冒充真实 TUN；默认使用隔离 PostgreSQL，不清空或迁移用户的远端测试库（如需核对 demo 账本，只用只读 doctor 并在记录中写明）。

## 12. 构建与测试命令契约（后续轮次实现，目前不存在）

```sh
# 第 1 轮起（Mac，本机即可）
python3 scripts/build-native-android.py --target aar --configuration Debug
python3 scripts/build-native-android.py --target apk --configuration Debug
python3 scripts/test-native-android.py --suite foundation
python3 scripts/test-native-android.py --suite unit          # Go 宿主测试 + Kotlin JVM 测试

# 第 3 轮起
python3 scripts/test-native-android.py --suite ui             # Roborazzi 截图 + Compose 测试（JVM）
python3 scripts/test-native-android.py --suite emulator --avd asterlink-api36

# 改变模拟器网络的套件（只影响模拟器；跑真实节点前断开 Mac 的公司 VPN 并退出 macOS 版 AsterLink）
python3 scripts/test-native-android.py --suite tun --avd asterlink-api36 --allow-network-changes
python3 scripts/test-native-android.py --suite billing --avd asterlink-api36 --allow-network-changes
python3 scripts/test-native-android.py --suite grants --avd asterlink-api36 --allow-network-changes
python3 scripts/test-native-android.py --suite lifecycle --avd asterlink-api36 --allow-network-changes

# 第 6 轮：签名密钥只从环境或钥匙串读取，不放命令行与源码
python3 scripts/package-native-android.py --channel internal
python3 scripts/test-native-android.py --suite release
```

gomobile 目标：`-target=android/arm64`（发布另加 `android/arm` 视决策），`-androidapi 26`，`-trimpath -ldflags "-s -w -X main.version=..."`；具体额外参数（如固定 Mihomo 是否需要 `-checklinkname=0`）在第 1 轮实测后写入锁文件，不预设。

既有回归入口保留：`scripts/test-native.py`、`test-backend-shared.py`、`test-backend-standalone.py`、`test-node-catalog.py`、`test-native-macos.py`、`test-native-windows.py`。

## 13. 风险与外部门槛

| 风险/门槛 | 处置 |
| --- | --- |
| 无真机：厂商 ROM 杀后台、真实射频切换、低端机性能、Play Protect 对侧载的提示 | 模拟器做全部可自动化验证；收费前至少一台真机或云真机抽检，否则只发内部渠道 |
| gomobile 与固定 Mihomo/Go 1.26 的兼容性 | 第 1 轮最先验证；失败按记录的一次决策改用 `c-shared` + JNI |
| `system` 栈在 fd 模式下的行为 | 第 2 轮实测，必要时一次性决策换栈，不自动回退 |
| 息屏断连体验 | 第 5 轮授权闸门；不得用延长租约或关闭挂起检测来“修复” |
| 包体（Go + Mihomo 单 ABI so 估计数十 MB） | 第 1 轮记录基线；按 ABI 拆分、`-s -w`、R8；不为瘦身删协议 |
| 节点控制权（商业强制计量） | 与桌面相同：只有 NodeAgent 管理的 VLESS(TLS) 参考转发器是权威；SS/Reality/WireGuard 等无节点侧强制，不得进入收费承诺 |
| 分发与合规 | 官网直链 APK；Google Play 需 VPN 政策声明；国内应用商店与地区法规对 VPN 有限制，须法务确认；用户协议、隐私政策、计费说明需上线 |
| 许可 | Mihomo 为 GPL-3.0，Android 同样需随包提供许可与对应源码获取方式；go-lzo（GPL-2.0）组合问题沿用 Windows 第 8 轮待法务确认 |
| 网站运营 | 支付回调验签、幂等开通、退款对账、邮箱验证/找回密码、管理员 MFA、备份告警——不在 App 内重做，但属整体收费门槛 |
| 测试凭据 | 已在对话中出现的数据库密码与节点凭据需轮换；测试只经临时文件注入 debug 包 |

## 14. 交接规则与进度记录

只维护本文件：6 轮状态表 + 每轮一段记录；每轮一个 PR（`worktree-android-roundN-*` 分支），必要修复留在本轮。接手顺序：读本文件当前轮 → `git status` 与上一轮 PR → 相关共享契约与源码 → 运行上一轮验收入口 → 只做当前未完成项。契约变化须同时更新 JSON、Go、Kotlin（以及受影响的 Swift/C#）与测试。

| 轮次 | 状态 | 下一步与前置条件 |
| --- | --- | --- |
| 1 环境与骨架 | DONE（PR StevenWinsir/vpn#28 已合并） | 本机 API 36 arm64 模拟器 5/5 PASS；16 KB/API 29/API 26 镜像留到第 6 轮安装 |
| 2 真实 TUN PoC | DONE（PR #29 已合并） | 本机 API 36 arm64：tun 套件 26 PASS / 2 BLOCKED（T04-ipv6 无 IPv6 上游、T02-real 未提供真实节点）；每次连接的 NAT goroutine 泄漏已在下方独立维护轮修复；VLESS UDP/XUDP 协议缺口已在下方 NodeAgent 维护轮修复，真实节点/TUN 重新验收仍是后续门禁 |
| 3 账号与 UI | DONE（本轮 PR，分支 worktree-android-round3-ui） | 本机 API 36 arm64：account 套件 20/20 PASS、ui 30 张截图、JVM 78、emulator 5/5；demo 服务器 + 用户真实账号的手工确认待用户执行（见第 3 轮记录） |
| 4 连接与计量 | DONE（本轮 PR，分支 worktree-android-round4-billing） | 本机 API 36 arm64：billing 套件 32/32 PASS（20+80 MiB，1×/0.5× 逐单位精确、B02、A04×4、B04 本地 NodeAgent）；tun 26/2 BLOCKED 不回归；demo 服务器手工确认与 Quick Settings 磁贴待用户执行（见第 4 轮记录）；B04-live 无公网受管节点 |
| 5 生命周期与可靠性 | DONE（本轮 PR，分支 worktree-android-round5-lifecycle） | 本机 API 36 arm64：lifecycle 套件 24 PASS / 2 NOT_RUN（L07-8h、L08 Chrome DoH/WebRTC 需人工或长时运行，见 docs/android-lifecycle.md）；授权闸门仅 Android 启用，桌面行为不变；8 小时采样与真机厂商 ROM 待用户/第 6 轮 |
| 6 打磨与发布 | NOT_STARTED | 发布密钥库由用户生成并保管；真机/云真机抽检渠道 |

每轮追加：

```text
轮次 / 日期 / 执行者：
基线 commit / 已有未提交改动：
实际修改与契约变化（不含秘密）：
执行命令 / 实际测试数 / PASS、FAIL、BLOCKED：
APK/AAR 版本 / 模拟器镜像(API、ABI、页大小) / Core 与 Mihomo 版本 / 脱敏证据路径：
未解决问题、复现方法与发布限制：
下一位从哪个文件/验收项开始；回退方法：
```

本次规划交接：已读完 macOS 与 Windows 交接书、PR #1–#27 状态及关键说明，核对了 Core 构建标签、平台白名单、固定 Mihomo 的 Android 补丁、本机 SDK 与可用模拟器镜像，确认后端无需改动、`/capabilities` 在 demo 服务器可达。识别出 Android 必改点（构建标签会误链接 macOS 实现、登录平台、fd 模式 TUN、protect 防环、IPv6 地址族阻断、息屏挂起导致断连）。未创建 Android 工程、未改 Core、未部署、未连接远端数据库。下一位从第 1 轮开始。

### 第 1 轮记录

```text
轮次 / 日期 / 执行者：第 1 轮 / 2026-10-15 / Claude（worktree 分支 worktree-android-round1-foundation）
基线 commit / 已有未提交改动：a0a8bf2（PR #27 合并点）；主目录仅有未跟踪的本文件，已随本轮提交
实际修改与契约变化（不含秘密）：
  Go Core：managed.ClientPlatform 增加 android→"android"；engine 的 platform_unix/sysdns/ipv6_route（及其测试）
    与 network_other 补 !android；新增 engine/{platform,sysdns,ipv6}_android.go（platformSupported=false，
    connect 明确返回 tun_unavailable，不链接 macOS 实现或空实现）、network/network_android.go（protect 绑定、
    宿主网络快照）、network/host.go（HostSnapshot 严格解码，签名规则与桌面相同）；Signature 增加平台钩子。
  IPC：抽出 ipc.HandleOne，ServeWith 与 mobile.Call 共用；对空帧/超 2 MiB 返回稳定错误码。ipc-v1.json 未改。
  新增 native/core/mobile（gomobile 绑定：Start/Call/Stop/Platform，CodeNotStarted="core_not_started"）。
  go.mod：以 tool 指令固定 x/mobile 21e291c（2025-02-18）。最新版会把桌面端 x/crypto/x/net/x/sys 一并升级，
    故选用依赖与现状完全一致的最新提交；桌面二进制链接的依赖版本未变（go version -m 核对）。
  Android 工程 apps/android（AGP 9.4.1、Gradle 9.8.1、Kotlin 2.4.21、Compose BOM 2026.09.00、compileSdk 37、
    targetSdk 36、minSdk 26、JDK 17）；build-logic 的 .env 解析插件（env-v1 全部用例）；Compose 主题令牌与
    显示 Core 版本/Mihomo 提交的骨架页；Manifest 基线（备份关闭、提取规则全排除、禁明文、仅系统 CA）。
  脚本 build-native-android.py（aar/apk）、test-native-android.py（foundation/unit/emulator）；
    CI android.yml（Linux 构建作业 + KVM x86_64 模拟器作业）；docs/android-foundation.md。
执行命令 / 实际测试数 / PASS、FAIL、BLOCKED（本机 macOS arm64）：
  test-native-android.py --suite foundation：18 PASS
  test-native-android.py --suite unit：3 PASS（Go 129 个测试；build-logic 3 个；app JVM 8 个）
  build-native-android.py --target aar --check-reproducible：PASS（两次绑定 libgojni.so 逐字节一致）
  build-native-android.py --target apk：PASS（.so 未压缩，zipalign -P 16 通过）；lint 0 错误
  回归：native/core go test -race ./...（12 包）、go vet（含 fakeengine、windows/amd64、linux/amd64）、
    test-native.py、test-native-macos.py --suite unit（7/7，Swift 46、Go 210）全部 PASS
  test-native-android.py --suite emulator --api 36（本机 AVD asterlink-api36，arm64-v8a，页大小 4096）：5 PASS
    （CoreBindingTest 用真实 AAR 跑 ipc-v1 全部案例与 start/hello/read_snapshot/stop；App 界面显示 Core 0.1.0 与
    Mihomo 70f0570405c3；Go 绑定测试交叉编译到设备运行 5/5，登录请求体 platform=android）
  CI：android.yml 构建作业、ci.yml、windows.yml 均 PASS；模拟器作业首跑暴露 AVD 目录不一致（avdmanager 走
    XDG_CONFIG_HOME），已改为显式 ANDROID_AVD_HOME
APK/AAR 版本 / 模拟器镜像 / Core 与 Mihomo 版本 / 证据路径：
  0.1.0；Core 0.1.0；Mihomo 70f0570405c3c2c47bb113b88db95006d239b346；
  libgojni.so arm64 25.1 MiB（4 个 LOAD 段均 0x4000），AAR 8.8 MiB，Debug APK 36.5 MiB；
  artifacts/native-android/（build-report.json、foundation/unit/emulator 报告、截图；不提交）
未解决问题、复现方法与发布限制：
  公司 VPN 下 services.gradle.org ≈20 KB/s、dl.google.com 单连接 ≈0.5 MB/s；Gradle 发行包经腾讯镜像下载
    并核对官方 SHA-256，SDK 包经本地分段代理下载、由 sdkmanager 自行校验 SHA-1（docs/android-foundation.md）。
  16 KB 页镜像、API 29/26 镜像未安装（第 2、6 轮需要）。
  Kotlin 侧 protect() 在第 1 轮恒为 false（无 VpnService）；Mihomo 外层连接的 DefaultSocketHook 未接（第 2 轮）。
下一位从哪个文件/验收项开始；回退方法：
  第 2 轮从 native/core/internal/engine/platform_android.go（fd 路径）与 apps/android/.../vpn/ 开始；
  回退：撤销本 PR 即恢复桌面端原状（桌面端链接的依赖与行为未变）。
```

### 第 2 轮记录

```text
轮次 / 日期 / 执行者：第 2 轮 / 2026-10-15 / Claude（worktree 分支 worktree-android-round2-tun）
基线 commit / 已有未提交改动：9b1c26d（PR #28 合并点）；无
实际修改与契约变化（不含秘密）：
  新契约 shared/contracts/tun-spec-v1.json（Core → Platform.openTun 的接口描述；Go 产出与 Kotlin 校验同一夹具）。
  Go Core：engine/tunspec.go（TunSpec 由 Mihomo 解析后的 TUN 段推导，地址/前缀/MTU/DNS 单一来源；TunHost 注册）；
    config.go android 分支（auto-route/auto-detect-interface 关闭，mtu 9000）；platform_android.go（fd 交接、
    tunReady=监听器确实在读本次 fd、dialer.DefaultSocketHook→protect）；tunfd_unix.go（fd 所有权：监听器关闭，
    未接管时按 inode 身份关闭，绝不二次关闭）；mihomo.go 增加 attachTun/releaseTun 钩子（桌面为直通）、
    NetworkChanged（刷新 Mihomo 接口缓存与解析器连接）、Verify 把最后一个探测的原因包进 tunnel_unhealthy
    （errors.Is 不变）。network：Android 无 /etc/resolv.conf，控制面解析回落到物理网络自身的 DNS
    （否则登录也会失败）；HostSnapshot 拒收 198.18.0.0/15 作为引导解析器；导出 ProtectSocket。
  mobile：Start/Stop 注册/移除 TUN 宿主；新增 NetworkChanged；tuntest.go 调试钩子仅在 -tags asterlink_tuntest 时存在。
  Android：AsterVpnService（specialUse 前台服务、通知“断开”、onRevoke/onDestroy 有界断开、拒绝无动作启动即
    不支持始终开启 VPN、establish 后等待 VPN 真正成为本应用网络再交 fd）；TunSpec（严格解码、无 DNS 查询的字面量
    校验）；NetworkMonitor（跟随 INTERNET+NOT_VPN 的最佳网络——VPN 起来后 activeNetwork 会变成 VPN 本身）；
    AndroidPlatform/VpnHostRegistry；TunnelSession 接缝（生产=Core connect）；CoreClient.connect/disconnect；
    Manifest：VpnService（BIND_VPN_SERVICE、不导出、SUPPORTS_ALWAYS_ON=false）、FOREGROUND_SERVICE(_SPECIAL_USE)、
    POST_NOTIFICATIONS、CHANGE_NETWORK_STATE。
  测试：cmd/asterlink-testnode（固定 Mihomo 作 SS/VLESS/REALITY 服务端，仅监听回环，example.com 仅在节点处
    映射到回环上的 sink）；androidTestTun/TunAcceptanceTest（仅 -Pasterlink.tunTest=true 编译）；
    test-native-android.py --suite tun；build-native-android.py --tun-test-hooks；verifyCoreAar 拒绝钩子 AAR 与
    普通构建混用；CI android.yml 新增 tun 作业（KVM x86_64 模拟器）。docs/android-tun-design.md。
执行命令 / 实际测试数 / PASS、FAIL、BLOCKED（本机 macOS arm64，公司 VPN 开启，模拟器 API 36 arm64 页 4096）：
  test-native-android.py --suite tun --allow-network-changes --cycles 20：26 PASS / 0 FAIL / 2 BLOCKED
    T01（4 MiB 经节点，节点计数 ≥ 正文）、B-COUNT（×1.0003）、T01a、T01-noproxy、T02-ss/vless/reality/badkey、
    T03-dns-off/opportunistic/strict/any53、T03-udp（SNTP 经节点）、T04-ipv6-fallback（IPv6 connect 0–2 ms 失败）、
    T05、T05-loop（无指向节点/引导解析器的 TUN 流）、T06-udp-reject、T07、T-CYCLES、T09-kill 全部 PASS；
    BLOCKED：T04-ipv6（模拟器上游无 IPv6）、T02-real（未提供真实节点）
  --suite foundation 19 PASS；--suite unit 3 PASS；--suite emulator 5 PASS（普通 AAR）
  回归：native/core go test ./...（13 包）、go vet（windows/linux/fakeengine/android）、test-native.py、
    test-native-macos.py --suite unit（7/7，Go 229、Swift 46）全部 PASS
APK/AAR 版本 / 模拟器镜像 / Core 与 Mihomo 版本 / 证据路径：
  0.1.0；Mihomo 70f0570405c3；arm64 libgojni.so 44.4 MiB（第 1 轮 25.1 MiB 是因为 TUN 代码不可达被链接器裁掉，
  44.4 MiB 才是真实基线）；Debug APK 约 56 MiB（增量打包残留空间时文件可达 100 MiB，条目合计不变）；
  artifacts/native-android/tun/（tun-report.json、instrument.txt、goroutine 剖面；不提交，令牌已脱敏）
未解决问题、复现方法与发布限制：
  ① 每次 TUN 启动泄漏 1 个 goroutine（三端共有，上游）：固定 Mihomo 以 context.TODO() 创建 TUN 监听器，sing-tun
     的 TCP NAT 超时循环只随该 context 结束；20 次循环 +19，其它不增长，fd 持平。需在固定内核中按监听器传可取消
     context，三端回归——已在下方“共享 TUN 生命周期修复”维护轮处理；此处保留 PR #29 原始发现与测量。
  ② NodeAgent VLESS 参考转发器不支持 XUDP，而固定 Mihomo 的 VLESS 客户端 UDP 总是 XUDP：受管 VLESS 节点上的 UDP
     在所有客户端都不通（billtest 用自带客户端所以未暴露）——需在转发器侧支持 XUDP。
  ③ 公网不存在的域名：fake-ip 本地答 A、AAAA 转发上游得 NXDOMAIN，Android 随后拒绝整个域名；真实域名不受影响。
  ④ 公司 VPN 影响：本机“主地址”是公司 VPN 的 utun 地址（本机都不可达），所以测试辅助程序全部只监听回环；
     T02-real（用户真实节点）必须断开公司 VPN 后再跑，否则测的是公司网络而非节点。
  ⑤ 前台服务类型选 specialUse（systemExempted 仅限“在设置中配置的 VPN 应用”，否则抛异常）；第 6 轮真机复核。
下一位从哪个文件/验收项开始；回退方法：
  第 3 轮从 apps/android/.../vpn/TunnelSession.kt（CoreTunnelSession）与 core/CoreClient.kt 开始接登录与快照；
  回退：撤销本 PR（桌面端仅多了直通的 attachTun/releaseTun 与 Verify 错误原因，行为不变）。
```

### 独立维护轮：共享 TUN 生命周期修复（第 2 轮之后）

基线为 PR #29 合并点 `08ab441c8a3b91481ab826d57749b0b08671523b`；全部修改位于新 worktree
`.claude/worktrees/sing-tun-context-cleanup`，分支 `fix/sing-tun-context-cleanup`，没有编辑 main。
本轮只修复每次连接泄漏 `TCPNat.loopCheckTimeout` 的问题，不改受管 VLESS UDP/XUDP 协议。

**修复与可复现性：** 保持 Mihomo gitlink `70f0570405c3c2c47bb113b88db95006d239b346` 与 sing-tun
`v0.4.22` 不变；采用 `native/third_party/patches/0001-sing-tun-listener-context.patch`，其 SHA-256 为
`d425c1a0541c4c78559d7ee9c33e08e2860f54fd6f22636342f0024b53860be7`。
`manifest.json` 固定原始和补丁后文件哈希；`scripts/prepare_mihomo.py` 幂等应用并拒绝错误 pin、未知改动和半应用状态。
监听器独立持有 `WithCancel(Background())`，关闭其它资源前 cancel；构造失败关闭捕获的监听器，避免具名返回值
被 `return nil, err` 覆盖；`tunStack.Start()` 前移交关闭所有权。不是用连接请求的超时控制已连接的 VPN。

三端构建与验收脚本接入准备步骤，裸 Go 构建未准备补丁会因缺少编译标记而失败。
Android AAR 记录补丁身份，Python 自动重建旧 AAR，Gradle 也拒绝缺失/不匹配身份的缓存。
发布源码仍为原始 Mihomo archive + 包含补丁的项目 archive；无 Git 的解包目录也可经哈希校验恢复补丁。
详细构建、测试和回退方法见 `native/third_party/patches/README.md`。

**硬门禁：** Android `TunAcceptanceTest.cycles()` 与 macOS/Windows 共用 `asterlink-tuntest cycleCheck`
均在首次连接前取 NAT worker 基线，任何正增长立即失败，不再从总 goroutine 增长扣除“已知泄漏”；
循环未完成、少于 2 轮或相关剖面不可解析也不能通过。保留其它 goroutine/fd 的原有调度噪声容差。

**本机实测（2026-10-15，macOS arm64；沿用 API 36 arm64-v8a 模拟器，页大小 4096）：**

| 验证 | 结果与证据 |
| --- | --- |
| `test-native-android.py --suite tun --allow-network-changes --keep-emulator --cycles 20`（重新构建 hooks AAR + APK） | 26 PASS / 0 FAIL / 2 BLOCKED；`T-CYCLES` 20/20，goroutine **10→10**，fd **104→104**，已知 NAT 循环 **+0**；before/first/last 三份剖面中的 NAT worker 均为 0 |
| `native/core: go test ./...` | 14 包全部 PASS；新增聚合剖面解析与零泄漏判定回归 |
| `scripts/test-native.py` | PASS，171 个 PASS（含子用例）；隔离 PostgreSQL + race，不访问远程测试数据库 |
| `scripts/test-native-macos.py --suite unit` | 8/8 PASS；Core 245、policy 5、nodeagent 18、Swift 46 个 PASS；Go vet、日志检查、Python env 和补丁回归通过 |
| `scripts/test-mihomo-patches.py` | 6 个准备器测试通过；真实 NAT 20 轮退出、Close 返回错误仍 cancel 两项通过；构造失败的 auto-redirect 用例仅 Linux 执行，本机 SKIP |
| 两个 Go overlay 负向对照（不改工作区源码） | 移除 cancel 时真实 NAT 生命周期测试失败；恢复原始未补丁的 server.go 时 Core 编译失败；均符合预期 |

两个 BLOCKED 仍是 `tun.T04-ipv6`（模拟器无 IPv6 上游）和 `tun.T02-real`（未注入真实节点），**不能记作 PASS**。
驱动默认因 BLOCKED 返回 3；需要在同样环境把这两个不可执行项显式列入
`--expect-blocked tun.T04-ipv6,tun.T02-real` 才可返回 0，报告仍保留 BLOCKED。
CI 中 Windows 原生 x64、Linux/macOS 与 Android KVM 结果由本分支 PR 的 checks 记录；不把交叉编译
或 Windows Server CI 描述为 Windows 10/11 真机 TUN 验收，也没有改变本机 macOS 路由运行特权 TUN 套件。

证据位于本 worktree 的 `artifacts/native-android/tun/tun-report.json`（记录 patch 身份）、
`goroutines-before.txt` / `goroutines-first.txt` / `goroutines-last.txt`，以及
`artifacts/sing-tun-leak/` 的 backend/macOS 日志和两个负向对照。节点配置不提交，节点主机仅用哈希标识。
现有模拟器保留运行。已经安装的 macOS/Windows/Android 程序不会因源码修改自动更新，必须分别重新构建安装。
VLESS UDP/XUDP 不匹配、IPv6 上游验收、真实节点与 Android 第 3–6 轮仍按原计划处理。

### 独立维护轮：NodeAgent VLESS XUDP（PR #30 之后）

基于 `e8dcd7a`，在新 worktree 分支 `fix/nodeagent-vless-xudp` 修复第二个跨平台发现；没有继续开发 Android 第 3 轮，也没有改动 Mihomo pin 或上一轮生命周期补丁。

- 参考 forwarder 支持 VLESS command 3 的 UDP mux 子集，保持 per-grant UUID、可信负载计数、原子整包 epoch 额度、到期和 Remove 存量踢除；每个帧内目标经过 DNS 后私网检查。Global ID 不跨 grant/连接恢复授权。
- `native/core/integration` 用真实 pinned Mihomo 默认 VLESS UDP 验证往返、计数、撤销、到期、并发额度、私网与同 global ID 的 grant 隔离；未修复基线首个 17 B 数据报 EOF，修复后通过。
- `asterlink-billtest --udp-sink` 已改为真实 Mihomo XUDP，无 command-2 回退；旧节点必须 FAIL。普通 command 2 兼容回归保留。
- 本机 NodeAgent/Core 全模块 race 与真实 API＋临时 PostgreSQL＋mTLS＋NodeAgent＋sink 的 grants 验收通过；后者观察到 TCP+XUDP 撤销约 0.8–1 s、空闲过期 14 s、套餐终止约 4 s，多设备竞争没有超过额度。这些是本机测试值，不是生产延迟承诺。

实现与复现见 [docs/nodeagent-xudp.md](docs/nodeagent-xudp.md)，商业能力和 Windows `--udp-sink` 说明同步更新。本轮需要部署重建后的 NodeAgent 才能修复已安装客户端的 UDP；无需因此修改客户端 YAML、添加 packet encoding 或迁移数据库。没有部署服务、连接远端测试数据库、修改系统路由/DNS、停止现有模拟器或改写历史 TUN 报告；真实公网 TLS 节点、三平台 OS TUN、IPv6 上游和 Android 第 3–6 轮仍保留原验收门禁。

### 独立维护轮：XUDP 目标空闲超时隔离（PR #32 之后）

基于 `6a10838`，在独立 worktree / 分支 `fix/nodeagent-xudp-target-idle` 修复 NodeAgent 单个 UDP 目标 reader 空闲超时误关共享 carrier 的问题。只退休超时目标并回收槽位，其他活跃目标继续转发；后续 Keep 经原授权/私网检查路径重新连接。退休与上行发包串行化，避免重复发送/计费；carrier 自身空闲、到期、额度和 Remove 的强制停流不变。

同/跨 session 和真实 pinned Mihomo 旧源码负向对照均复现 EOF；NodeAgent 目标生命周期 race 50 轮、真实 integration 全套 race 20 轮通过。完整实现、命令和证据边界见 [docs/nodeagent-xudp.md](docs/nodeagent-xudp.md)。只需重建部署 NodeAgent，不改 AAR/APK、VpnService、Mihomo 或数据库；没有进入 Android 第 3 轮、修改模拟器/系统网络或重新标注历史 TUN 验收结果。

### 第 3 轮记录

```text
轮次 / 日期 / 执行者：第 3 轮 / 2026-10-15 / Claude（worktree 分支 worktree-android-round3-ui）
基线 commit / 已有未提交改动：027bf72（PR #35 合并点）；无
实际修改与契约变化（不含秘密）：
  无契约 JSON 变化；无 Go Core 变化。后端仅新增测试文件 backend/internal/api/android_world_test.go
    （TestAndroidAccountWorld：仅在套件设置 ASTERLINK_ANDROID_WORLD 时运行，只监听 127.0.0.1，复用 seedNativeWorld，
    /__world/ 回环控制：改名/改倍率/停用/启用/删除线路、套餐到期、占用设备名额）。
  Kotlin：core/Snapshot.kt（snapshot-v1 严格解码，Long，action_error 拆分）；CoreClient 全部会话命令；
    session/SessionController（按 Windows C# 版移植：操作代次丢弃、旧 generation 丢弃、action_error、stale_generation
    重读、native_session_* 静默重登一次、内核丢失→重新登录、连接中 1 s / 其它 2 s 轮询；Android 增加：应用级常驻、
    登录后立即一次 refresh_catalog、回前台节流 10 s 刷新、Core 自行停隧道时同时停前台服务、连接经 TunnelLauncher
    交给 AsterVpnService 并按用户决定时的 generation 连接、取消连接=停 Core）；ConnectionView/NodeList/Formatting/
    Diagnostics（脱敏）；security/（Keystore AES-256-GCM 记住密码，账户作 GCM 关联数据，条目名为账户 SHA-256；
    DataStore 偏好；随机 UUID device_id）；ui/（登录、连接、线路、我的四页 + 空态/错误态、深浅色、SplashScreen、
    Custom Tabs、系统 VPN/电池设置、复制脱敏诊断、退出登录二次确认）；删除第 1 轮 foundation 页。
  依赖：datastore 1.2.1、browser 1.10.0、core-splashscreen 1.2.0、lifecycle-process 2.11.0；测试 robolectric 4.17、
    roborazzi 1.76.0（toolchain.lock.json libraries_round3）。权限、Manifest 基线不变。
  脚本：test-native-android.py 新增 --suite ui、--suite account；CI android.yml 构建作业加 ui，模拟器作业加 account
    （私有 PostgreSQL + adb reverse）。docs/android-ui.md。
执行命令 / 实际测试数 / PASS、FAIL、BLOCKED（本机 macOS arm64，公司 VPN 开启，模拟器 API 36 arm64 页 4096）：
  --suite foundation 19 PASS；--suite unit 3 PASS（Go 168；build-logic 3；app JVM 78：控制器 27、契约/展示 9、截图 30 …）
  --suite ui 2 PASS（30 张截图，每张同时断言所示状态；目视检查登录/连接/阻断/线路/我的/深色/200% 字号）
  --suite emulator 5 PASS（新增 SecurityStoreTest 3：真 Keystore 往返、跨账户与篡改拒绝、盘上无明文、device_id 稳定）
  --suite account 20 PASS / 0 FAIL / 0 BLOCKED（真实 SessionController + Go Core + Keystore + Gin/PostgreSQL）：
    A01 错误密码（并删除旧保存密码）、记住密码、登录后线路 0 ms 出现、盘上无密码/令牌、重启后在线自动登录、
      退出登录删除密码与邮箱、不记住时不保存；
    A02 管理员无套餐不能连接（upgrade_required）、0 额度（quota_exhausted）、打开期间套餐到期约 2.0 s 反映、
      设备上限（device_limit 中文提示）；
    A04 套餐白名单（基础套餐只见普通线路）、改名+倍率 1.5× 约 2.0 s、停用约 2.0 s（选中清除）、启用约 2.0 s、
      删除约 2.0 s；脱敏：UI 状态与诊断无节点地址/凭据/加密方式/密码；logcat 无上述秘密。
    （约 2 s = 2 s 空闲轮询间隔，Core 侧 watcher 已先拿到变化；这些是本机值，不是生产承诺。）
  A03：单 watcher、429 退避在 Core（Go 测试已覆盖）；旧代次/竞态丢弃在 SessionControllerTest 覆盖。
  lint：0 错误。
APK/AAR 版本 / 模拟器镜像 / Core 与 Mihomo 版本 / 证据路径：
  0.1.0；Mihomo 70f0570405c3（生命周期补丁 d425c1a0…）；arm64 libgojni.so 44.4 MiB；Debug APK 57.1 MiB；
  artifacts/native-android/{ui,account,emulator}/（截图、account-report.json、instrument.txt；不提交）
未解决问题、复现方法与发布限制：
  ① 截图在 Robolectric 的 Android 15 沙箱渲染（其 Android 16 沙箱需要 JDK 21，锁文件为 JDK 17）；应用本身仍 target 36，
     并在 API 36 模拟器验证。
  ② 网站没有“忘记密码”、隐私政策、用户协议页面，App 未放这三个链接（WebPage 枚举一处添加即可）。
  ③ 连接按钮已接 VPN 授权与 AsterVpnService，但连接/计费/通知速率的验收属第 4 轮，本轮未声明通过。
  ④ demo 服务器手工确认未做（需要用户自己的测试账号密码，且公司 VPN 下结果不代表国内网络）。步骤见 PR 说明。
  ⑤ 公司 VPN 影响：本轮全部自动化只走本机回环（后端 127.0.0.1 + adb reverse），不受公司 VPN 影响；
     只有连 demo.hyshentou.cn 的手工确认会经过公司网络。
下一位从哪个文件/验收项开始；回退方法：
  第 4 轮从 session/SessionController.connectNow、vpn/TunnelLauncher、ui/connect/ConnectScreen.TrafficCard 与
  VpnNotifications（速率、线路名）开始，按 B01/B02/B04 做计费验收；
  回退：撤销本 PR（Go Core、契约、桌面端无改动；后端只多一个默认跳过的测试）。
```

### 第 4 轮记录

```text
轮次 / 日期 / 执行者：第 4 轮 / 2026-10-16 / Claude（worktree 分支 worktree-android-round4-billing）
基线 commit / 已有未提交改动：f8bb445（PR #36 合并点）；无
实际修改与契约变化（不含秘密）：
  无契约 JSON 变化。
  共享 Go Core（影响 macOS/Windows）：managed 中“守卫停隧道的原因”（quota_exhausted、subscription_expired、
    profile_changed、client_config_unavailable、network_changed…）不再被 keep-alive 下一次成功的 /session 续期清空，
    保留到用户下一次操作（连接/选线/登录）；续期仍清除它自己的瞬时失败。PR #35 当时只修了测试、保留了清空行为；
    本轮实测 2 s 报告周期下 App 读到快照前原因已被清空（A04×3、N02 失败）。新增 TestTheStopReasonSurvivesLaterRenewals
    （无修复时失败）、TestARenewalClearsItsOwnFailureOnly；原 TestWaitStoppedSurvivesTheKeepAliveClearingLastError 由前者替代。
  Android：
    ① AsterVpnService 在 Builder.establish() 前自行调用 VpnService.prepare()：实测 API 36 进程内首次 establish 在没有
       Activity 先调用 prepare() 时返回 null → 生产 connect 全部 tunnel_failed（磁贴、进程重启后连接都会命中）。
    ② 断开结算未完成时的“连接”排队在同一前台服务里、结算完立即执行（原为 vpn_stopping 拒绝）；正在结束的服务不再算
       “运行中”，控制器不会重复发 stop 而取消排队的连接。
    ③ 连接页“本次连接用量 · 计费”改为本次连接（客户端计数在整个登录内累计、可能跨不同倍率）：连接建立时记录基线
       （client_reported=连接前计数；node_reported=0），按连接绑定的唯一倍率整数计费。
    ④ 前台通知实时显示“线路名 · ↑速率 ↓速率”，只由服务发布/移除（先停跟随再移除，杜绝残留）。
    ⑤ 隧道非用户主动结束且 App 不在前台时，独立通知渠道提示原因（Core 中文原因；兜底套餐裁决原因）。
    ⑥ 可选 Quick Settings 磁贴（ConnectTileService，BIND_QUICK_SETTINGS_TILE）：已登录+已选线+已授权时一键连/断，否则打开 App。
    ⑦ 控制器在隧道存在（含 stopping）时 1 s 轮询。
  测试：backend TestAndroidBillingWorld（仅设置 ASTERLINK_ANDROID_BILLING_WORLD 时运行，只监听 127.0.0.1；线路指向
    round-2 testnode；可选真实 NodeAgent+mTLS 节点计量线路；/__world/ 回环控制：改倍率/停用/到期/剩余额度/账本/sink
    统计/“ACK 丢失”——由 world 在真实处理器提交后断开连接注入，App 内没有任何注入开关）；androidTest
    BillingAcceptanceTest（@SuiteDriver，生产 debug APK，无 hooks AAR）；test-native-android.py --suite billing；
    emulator 套件改用 notAnnotation=SuiteDriver 排除套件驱动；foundation 新增磁贴服务检查；CI android.yml 新增 billing 作业
    （4+16 MiB）。docs/android-billing-acceptance.md（含 demo 服务器手工步骤）。
执行命令 / 实际测试数 / PASS、FAIL、BLOCKED（本机 macOS arm64，公司 VPN 开启，模拟器 API 36 arm64 页 4096）：
  --suite billing --allow-network-changes（20 MiB 上 + 80 MiB 下）：32 PASS / 0 FAIL / 0 BLOCKED
    B01 1×：计 20,972,965+83,888,065 B → 104,861,030,000 units；0.5×：20,972,909+83,888,063 B → 52,430,486,000 units；
      sink 精确，服务端账本 = Core 确认计数 = App 显示；B02 丢失 sequence 20 的应答后原批重发、只扣一次；
    A04 改倍率 0.9 s / 停用 1.3 s / 到期 1.4 s 自动停连（原因 profile_changed / client_config_unavailable /
      subscription_expired），整段按原倍率结清；额度耗尽在持续下载 18.7 s 后停连，超扣 1,053,992 B（client_reported
      一个报告周期，如实记录）；A04-rebind 新倍率 2000‰；N01 通知“HK-IEPL-01 · ↑306 B/s ↓2.76 MiB/s”；N02 到期提醒；
    C01 断开后立即连接 583 ms 成功；B04 真实 NodeAgent：0 次客户端上报，/traffic → 409 metering_source_mismatch 账本不动，
      节点独立计费 10 MiB 正文 @500‰ = 5,244,902,500 units（+0.04%）。
  回归：--suite foundation 20 PASS；unit 3 PASS（Go 169、JVM 86）；ui 2 PASS（30 张）；emulator 5 PASS；account 20 PASS；
    tun --cycles 20：26 PASS / 2 BLOCKED（T04-ipv6、T02-real，同第 2 轮），T-CYCLES goroutine 10→10、fd 104→104；
    native/core go test ./...（15 包）PASS；scripts/test-native.py（隔离 PostgreSQL + race）PASS；
    test-native-macos.py --suite unit 8/8 PASS（Core 258、policy 5、nodeagent 77、Swift 46）。
APK/AAR 版本 / 模拟器镜像 / Core 与 Mihomo 版本 / 证据路径：
  0.1.0；Mihomo 70f0570405c3（生命周期补丁 d425c1a0…）；Debug APK 约 57 MiB（增量打包残留时文件更大）；
  artifacts/native-android/{billing,tun,ui}/（billing-report.json、instrument.txt、world.log；不提交）
未解决问题、复现方法与发布限制：
  ① demo 服务器 + 用户真实账号的手工确认、Chrome 浏览/视频、Quick Settings 磁贴点击未自动化（步骤见
     docs/android-billing-acceptance.md）；须断开公司 VPN 再测，否则测的是公司网络。
  ② B04-live（公网受管 TLS 节点）不存在，BLOCKED；client_reported 仍是开发账本，不解除生产保护。
  ③ 停用线路的停连原因为 client_config_unavailable（“线路配置暂不可用”），比 node_unavailable 笼统，属 Core/后端文案，未改。
  ④ 共享 Core 的停连原因保留改动需 Windows CI 回归（本 PR checks）。
下一位从哪个文件/验收项开始；回退方法：
  第 5 轮从 managed 的 ResumePolicy（7.5 授权闸门）与 vpn/AsterVpnService 生命周期开始；
  回退：撤销本 PR（桌面端唯一行为变化是停连原因保留到下一次操作）。
```

### 第 5 轮记录

```text
轮次 / 日期 / 执行者：第 5 轮 / 2026-10-16 / Claude（worktree 分支 worktree-android-round5-lifecycle）
基线 commit / 已有未提交改动：658ed7c（PR #37 合并点）；无
实际修改与契约变化（不含秘密）：
  契约：snapshot-v1.json 新增可选字段 gate / last_gate 与 tunnel_health "gated"（夹具 connected_gated）；仅 Android
    策略产生，Swift/C# 解码忽略未知字段，Kotlin 严格解码已同步。
  共享 Go Core（managed）：Options.ResumePolicy（零值 ResumeStop = 桌面原行为不变；mobile 绑定选 ResumeGate）、
    GateLimit（默认 5 分钟清醒时间）；resume.go：休眠/时钟（lifecycle_reconfirmation_required）、网络签名变化
    （network_changed）、租约失效（lease_expired）→ 关闸（Engine.Suspend：拒新流、关存量流，保留 TUN fd/路由/DNS）
    → 在线重验（Preflight + 计量批次重放/发送或节点计量 GET /session）→ 授权有效则 Resume + 经 TUN 的真实探测后开闸；
    服务器拒绝则照常停流结账；无网/无应答退避重试（2→30 s，NetworkChanged 与回到前台立即重试），超限才断开。
    SetForeground：后台暂停目录长轮询（先测后定：停连依赖 /traffic 应答与 /session，不依赖 watcher）；暂停期间
    目录标记过期，绑定线路前先读一次目录（首轮设备实测发现：用过期目录绑定会被下一次目录判为 profile_changed）。
  engine：Mihomo.Suspend/Resume（tunnel.OnSuspend/OnRunning + 关闭跟踪流）；stopLocked 共用 closeFlows。
  mobile：Start 使用 ResumeGate；新增 SetForeground；NetworkChanged 同时唤醒等待中的闸门。
  Android：连接页/通知显示“正在重新验证/等待网络”（仍可断开）；onRevoke 在主线程先告知控制器 → “VPN 已被系统设置或
    其它 VPN 应用关闭，本次流量已结算”（App 内或后台告警，只一次）；ProcessLifecycleOwner → Core 前后台提示；
    “我的”页显示通知是否允许、是否已豁免电池优化并跳转系统设置（不擅自申请豁免）。“允许局域网”决策：本版不做。
  测试：managed/resume_test.go（13 个，含负向对照）；JVM 95（新增闸门展示、撤销提示）；截图 34（新增 4）；
    androidTest LifecycleAcceptanceTest + 测试 APK 内的竞争 VPN（CompetitorActivity/CompetitorVpnService）；
    TestAndroidBillingWorld 新增 life 用户与 /__world/api（模拟 API 不可达）、/__world/hits（按端点计数）；
    test-native-android.py --suite lifecycle；scripts/android/lifecycle.sh（8 小时采样与手工步骤）；CI android.yml
    新增 lifecycle 作业（--no-reboot、短息屏/短采样）；docs/android-lifecycle.md。
执行命令 / 实际测试数 / PASS、FAIL、BLOCKED（本机 macOS arm64，公司 VPN 开启，模拟器 API 36 arm64 页 4096）：
  --suite lifecycle --allow-network-changes --screen-off-seconds 600：24 PASS / 0 FAIL / 2 NOT_RUN
    N01 拒绝通知仍可连接转发；L01-stall 进程停 16 s → 闸门 0.5 s 重开、同一连接；L01-screen 息屏 600 s 同一连接
    （模拟器 CPU 不深睡，深睡路径由 L01-stall 与 Go 单测覆盖）；L01-gate-holds API 不可达时闸门等待、经隧道下载失败、
    sink +0 字节、VPN 保持，API 恢复后重开；L02 Doze 15 s 内 7–8 次计量上报到达服务器、隧道可下载；BG 后台 12 s
    目录轮询 0 次（前台 8 s 4 次）、后台改倍率 2.0–2.4 s 停连（上报间隔 2 s；生产 60 s）、回前台目录 2 s 追上；
    L03 Wi-Fi↔蜂窝闸门 0.5–0.6 s 重开、飞行模式等待并在恢复后约 2 s 重开；L04 竞争 VPN 抢占 0.9 s 停流结账
    （账本 = Core 确认计数）、提示一次、之后可重连；L05 移除任务/am kill 不影响、force-stop VPN 立即消失；
    L06 重启后无 VPN/进程/服务；L07 20 次循环 fd 126→124、线程 40→41，3 分钟采样 RSS ≈255 MiB 平稳。
    NOT_RUN：L07-8h（需无人值守长跑）、L08 Chrome DoH/WebRTC（需 Chrome 与人工；Private DNS 见第 2 轮 T03）。
  回归：foundation 20、unit 3（Go 186、JVM 95）、ui 2（34 张）、emulator 5、account 20、billing 32（20+80 MiB）全部 PASS；
    tun --cycles 20：26 PASS / 2 BLOCKED（T04-ipv6、T02-real，同前），T-CYCLES goroutine 10→10、fd 104→104；native/core go test ./... PASS；scripts/test-native.py PASS；test-native-macos.py --suite unit 8/8
    （Core 275、Swift 46）。
APK/AAR 版本 / 模拟器镜像 / Core 与 Mihomo 版本 / 证据路径：
  0.1.0；Mihomo 70f0570405c3（生命周期补丁 d425c1a0…）；Debug APK 57.1 MiB；
  artifacts/native-android/lifecycle/（lifecycle-report.json、timeline.json、soak-samples.json、instrument.txt；不提交）
未解决问题、复现方法与发布限制：
  ① 8 小时采样、Chrome DoH/WebRTC、厂商 ROM 后台清理、真实射频切换与真实耗电需真机/人工（docs/android-lifecycle.md）。
  ② 闸门关闭期间 DNS 劫持仍应答（fake-ip A 记录本地；少量转发查询照常经节点，不是代理流量也不计量）。
  ③ 模拟器息屏不进入深睡，“10 分钟息屏”在模拟器上只证明息屏路径；深睡恢复由 L01-stall（进程停顿）与单测证明。
  ④ 共享 managed 改动需 Windows CI 回归（本 PR checks）。
下一位从哪个文件/验收项开始；回退方法：
  第 6 轮从 R01–R04（签名、ABI 拆分、release 审计）与真机抽检开始；
  回退：撤销本 PR（桌面端默认 ResumeStop，唯一共享行为变化为暂停期目录过期时绑定前多读一次目录——桌面从不暂停）。
```

## 附录 A. 源码核验索引

| 主题 | 依据 |
| --- | --- |
| 后端平台白名单 | `backend/internal/api/native.go:62–96` |
| 登录平台来源 | `native/core/internal/managed/platform.go` |
| 构建标签现状 | `native/core/internal/network/network_other.go`、`internal/engine/{platform_unix,sysdns,ipv6_route}.go` |
| 运行模板与 REJECT 尾规则 | `native/core/internal/engine/config.go` |
| Verify 经 TUN、计量口径 | `native/core/internal/engine/mihomo.go:174–214` |
| 控制面出口与解析 | `native/core/internal/network/network.go:52–120` |
| 挂起检测 | `native/core/internal/managed/tunnel.go:371–405`、`lease.go` |
| IPC 串行分发 | `native/core/internal/ipc/server.go`、`internal/service/service.go` |
| 固定 Mihomo 的 Android 支持 | `Clash.Meta/listener/sing_tun/server.go:152–163`（fd）、`config/config.go:317`、`component/dialer/socket_hook.go`、`component/loopback/detector.go`、`constant/path.go`、`dns/patch_android.go`、`tunnel/statistic/patch.go` |
| 协议支持范围 | `docs/managed-protocols.md` |
| UI 语义参照 | `docs/windows-client-ui.md`、`apps/windows/AsterLink.App/Theme`（品牌色 `#2F6FEB`） |

## 附录 B. 第 1 轮外部复核入口（本轮未访问，不作为当前版本证明）

- [VpnService](https://developer.android.com/reference/android/net/VpnService)、[VpnService.Builder](https://developer.android.com/reference/android/net/VpnService.Builder)、[前台服务类型](https://developer.android.com/develop/background-work/services/fgs/service-types)、[16 KB 页大小](https://developer.android.com/guide/practices/page-sizes)、[Doze 与 App Standby](https://developer.android.com/training/monitoring-device-state/doze-standby)。
- [Jetpack Compose](https://developer.android.com/develop/ui/compose)、[Material 3](https://m3.material.io/)、[Android Keystore](https://developer.android.com/privacy-and-security/keystore)、[应用签名](https://developer.android.com/studio/publish/app-signing)。
- [gomobile](https://pkg.go.dev/golang.org/x/mobile/cmd/gomobile)、[Mihomo](https://github.com/MetaCubeX/mihomo)、[Google Play VPN 政策](https://support.google.com/googleplay/android-developer/answer/12564964)。
