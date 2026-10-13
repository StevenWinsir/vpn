# AsterLink Windows x64 原生客户端开发交接计划

> 编制日期：2026-10-13。状态：**规划完成，Windows 实现尚未开始**。
> 勘察基线：`99b5e9b9816d141d670230e51b0f52c7e251f631`，对应已合并 PR #19；Mihomo 固定提交：`70f0570405c3c2c47bb113b88db95006d239b346`。
> 项目根目录：`/Users/stevenlee/Desktop/vpn`。本轮只新增本文件；不修改业务代码、不部署、不连接或迁移远端数据库、不提交或推送 Git。

## 1. 结论与交付边界

**推荐：C# + .NET 10 LTS 候选基线 + WPF/XAML 原生 GUI，C# Windows Service 管理特权生命周期，复用现有 Go Managed Core 和内嵌 Mihomo，通过 Wintun 实现 Windows TUN。共 8 轮开发。** .NET 的具体补丁、Windows 支持范围、安装器和驱动分发版本在第 1 轮核实并锁定；本文件不把未查证的版本称为“最新版”。

这是 Windows 专属桌面客户端，不是把 Next.js 网页套进浏览器。Next.js + Mantine 网站继续承担管理员节点维护、套餐购买、订单和账户管理；Go/Gin/GORM/PostgreSQL 后端继续作为唯一业务控制平面。Windows 不重新实现第二套套餐或计费后台，不恢复已移除的 FlClash/Flutter/Rust GUI。

目标闭环：用户安装 → 首次特权服务授权 → 登录/可选记住密码 → 同步套餐与可用线路 → 选择线路 → 真实 TUN 连接 → 展示速率和用量 → 按绑定倍率结算 → 节点变更、额度耗尽或套餐到期时停止代理。普通用户不看到、不导出节点 YAML、地址或凭据。

必须分开三个交付等级：

| 等级 | 达成标准 | 计划节点 |
| --- | --- | --- |
| 工程可构建 | Windows x64 GUI、Service、Core 的构建和基础 IPC 可验证 | 第 1–3 轮 |
| 内部功能版 | 真实账号、SS/VLESS 测试线路、TUN、客户端计量与倍率扣量闭环；限制明确 | 第 5 轮，缺少真实测试环境时不得宣称已验收 |
| 可收费发布候选 | 网络与安全回归、真实受管节点的权威计量/撤销、签名安装包及外部运营门槛全部通过 | 第 8 轮；任一硬门槛未过则仍为内部版 |

“8 轮”是 8 个有明确验收点的开发批次，不是保证 8 次聊天无需修复即可完成。每轮失败项在本轮修复；不为了轮数而跳过真实 Windows 测试。第 5 轮即可开展内部试用，不必等所有商业运营工作结束才看到可用界面。

## 2. 已有工程与 PR 进程：从现状接续，不从零重做

### 2.1 本次核验范围

先完整阅读了根目录 [handoff_app.md](handoff_app.md) 的 711 行，再查看本地源码、构建脚本、共享契约和 GitHub PR #1–#19 的说明；重点核对了原生重构、节点权威计费与后续真机修复。PR #13、#19 的合并状态经 GitHub 元数据确认，当前本地 HEAD 与 #19 合并提交一致。

勘察开始和写入前工作区均干净。实际主机为 Apple M4 / arm64 / macOS 26.6.2 / 32 GiB；`/opt/homebrew/bin/go` 实测为 `go1.26.6 darwin/arm64`。Runner 默认 PATH 未包含 Go；.NET 在该 PATH 和检查过的常用路径中未找到，这不等于已全面证明机器上没有安装 .NET。未验证任何 Windows 主机或 Runner 可用性。

本次未执行 Windows 构建、驱动安装、TUN、数据库或商业节点测试。用户已反馈 macOS DMG 安装和基础功能可用；这与历史记录一起作为现状输入，**不折算为 Windows 已通过测试**。

### 2.2 历史 PR 对 Windows 的实际意义

| PR | 已阅读的开发内容 | Windows 接续方式 |
| --- | --- | --- |
| [#1](https://github.com/StevenWinsir/vpn/pull/1) | 管理员 YAML、登录、套餐、倍率和客户端报告账本 | 复用业务，不将开发账本误当防作弊计费 |
| [#2](https://github.com/StevenWinsir/vpn/pull/2) | 15 种托管协议的共享字段白名单和校验 | 沿用策略；“解析支持”不等于每种协议实传和商用均通过 |
| [#3](https://github.com/StevenWinsir/vpn/pull/3) | 后端可独立目录构建 | 保持共享源同步和 backend-only 回归 |
| [#4](https://github.com/StevenWinsir/vpn/pull/4)、[#6](https://github.com/StevenWinsir/vpn/pull/6)、[#8](https://github.com/StevenWinsir/vpn/pull/8)、[#9](https://github.com/StevenWinsir/vpn/pull/9) | 旧 Flutter TUN、PF、UDP 旁路、授权重启、监听器误判自身网卡 | 历史经验，不是当前主线客户端架构；本地主线合并历史没有这些 PR，不能把 PF 防护当成现版本已有能力 |
| [#5](https://github.com/StevenWinsir/vpn/pull/5)、[#7](https://github.com/StevenWinsir/vpn/pull/7) | 管理员删除、元数据目录监听、网页用户节点列表 | 复用现有接口；无需新建 WebSocket 或另一份节点库 |
| [#10](https://github.com/StevenWinsir/vpn/pull/10) | 去 FlClash，SwiftUI/Helper/Go Core，共享契约迁移 | Go 业务与契约继续共用；Windows 重写平台层和 GUI |
| [#11](https://github.com/StevenWinsir/vpn/pull/11) | 真实登录、Keychain、套餐节点、状态机 | 迁移界面契约和行为；Windows 用 Credential Manager 替代 Keychain |
| [#12](https://github.com/StevenWinsir/vpn/pull/12) | TUN、唯一 Meter、尾账、租约和恢复 | 保留账务/状态机，替换 macOS 路由、DNS、特权实现 |
| [#13](https://github.com/StevenWinsir/vpn/pull/13) | capabilities、grant、NodeAgent、预算、节点权威计量 | **代码已存在**，Windows 消费现有能力，不重新设计一套 grant API |
| [#14](https://github.com/StevenWinsir/vpn/pull/14) | DMG、发布审计、升级卸载；修复曾使 CI 根本未运行的 YAML 错误 | Windows 单独建立真实执行的 CI；构建绿灯不代替安装/网络验收 |
| [#15](https://github.com/StevenWinsir/vpn/pull/15) | NodeAgent 转发计数并发时序修复 | 保留重复执行的 race 与计数回归 |
| [#16](https://github.com/StevenWinsir/vpn/pull/16) | Mihomo 日志污染 stdout IPC，连接时 Worker 被杀 | Windows 使用独立私有 IPC 句柄并用真实 logger 做回归 |
| [#17](https://github.com/StevenWinsir/vpn/pull/17) | VLESS 无 IPv6 出口导致“已连接但没网” | 验证早期拒绝与浏览器 IPv4 回退；不能照搬 macOS reject 路由 |
| [#18](https://github.com/StevenWinsir/vpn/pull/18) | 系统 DNS 未进入 TUN、LAN DNS 与浏览器 DoH、恢复日志 | 必须测试 Windows 系统解析器及多网卡，不能只测 Go resolver |
| [#19](https://github.com/StevenWinsir/vpn/pull/19) | 首次授权引导和界面状态未刷新 | Windows UAC/服务状态必须有可见反馈，授权完成自动继续初始化 |

`handoff_app.md` 前面的“计划新增”“阶段待完成”有历史残留，应结合后面的交接追加与当前源码判断。尤其 NodeAgent/grant 已实现，不可按旧段落再写一遍；正式签名、真实商业节点、部分网络矩阵也不可因代码存在就勾选通过。

### 2.3 可复用能力和仍未完成的边界

网站保持 Next.js/Mantine；后台保持 Gin/GORM/PostgreSQL。原生登录接口已经接受 `platform=windows`。共享 Go Core 已有 API 客户端、登录会话、套餐授权、目录 watcher、单节点配置校验、generation 状态机、开发 Meter、grant 模式和脱敏 Snapshot。

后端已支持 `client_reported` 与 `node_reported` 两种互斥会话来源；production 对静态共享交付仍有保护，只允许满足受管条件的节点授权路径。参考 NodeAgent 当前是有限的 VLESS 转发器，不能把 VLESS Reality/XTLS、SS、Trojan、Mux 等顺带认定为有权威适配。VLESS UDP 与 Mihomo 的完整互通、真实公网受管节点 TUN 链路仍须验收。依据：[阶段 4 说明](docs/macos-stage4.md)、[凭据生成](backend/internal/nodes/credential.go)、[v2 契约](shared/contracts/native-v2.json)。

`native/legacy-core` 是现有协议和浏览器到账本回归依赖的 Go 模块。本次 Windows 开发不删除它，不把历史测试随旧 GUI 一并丢弃。

## 3. 技术栈选择与 M4 开发方式

### 3.1 明确选择

| 层 | 推荐技术 | 选择原因与限制 |
| --- | --- | --- |
| Windows GUI | C#、WPF/XAML、MVVM；少量 CommunityToolkit.Mvvm，版本锁定后使用 | Windows 专属原生桌面界面，可控布局、键盘和辅助功能；不引入浏览器内核 |
| 运行时 | .NET 10 LTS 候选，`win-x64` self-contained 发布 | 不要求客户单独安装 .NET；具体支持的 Windows 版本及补丁在第 1 轮核实 |
| 特权服务 | C# .NET Worker / Windows Service + 必要 Win32 P/Invoke | 与 GUI 共用 DTO；SCM、Named Pipe、Job Object、签名和恢复职责清晰 |
| 代理与业务内核 | 现有 `native/core` Go 模块 + 内嵌固定 Mihomo | 会话、计量、grant 只维护一份，不在 C# 再实现业务账本 |
| TUN | 固定 sing-tun/Mihomo Windows 路径 + 兼容的、已签名 Wintun 分发物 | 不自研 NDIS 驱动；驱动来源、架构、哈希和分发许可必须锁定 |
| 本机 IPC | 受限本地 Named Pipe；Service→Core 私有管道 | 无 localhost HTTP 管理端口，无可导入任意 YAML 的 RPC |
| 凭据 | Windows Credential Manager 的当前用户 Generic Credential | 明确选择“记住密码”，不保存明文，不让 SYSTEM 代存为所有用户共享凭据 |
| 安装发布 | 固定版本 Inno Setup 构建签名 `Setup-x64.exe`，Authenticode + 时间戳 | 首版不做 Store/MSIX、自更新服务、差分升级；安装器负责服务安装和一致性 |
| 测试 | Go tests/race、.NET 单元测试、少量 Windows UI Automation、受控 TUN 验收脚本 | 编译、UI、系统网络、账务四类证据分开 |

WPF 属于 Windows 原生桌面技术，但 C# 通常运行于 .NET；这里的“原生”不是宣称所有代码都是无运行时的机器码。首版不启用 WPF NativeAOT、激进 trimming 或自动解压单文件发布，优先可复现、易定位的目录式 self-contained 包。

备选决策：WinUI 3 也可做原生 UI，但会增加 Windows App SDK/部署整合工作，首版不需要这些额外依赖；Avalonia 便于 Mac 上预览，但本项目优先 Windows 专属体验和系统集成；Electron/Tauri/Wails/网页套壳可复用前端，却不符合本次不依赖 WebView 的选择；C++/Qt 会增加语言与封装工作。除第 1 轮证明存在明确阻塞，不在中途并行维护两套 GUI。

### 3.2 交叉编译能做什么，不能替代什么

**Mac mini M4 作为主开发机完全可行，但不承诺全部 WPF 构建、Windows 驱动和验收都仅在 macOS 上完成。** 推荐“Mac 编码与 Go 交叉编译 + Windows x64 构建/调试节点”的工作方式。

| 工作 | M4/macOS | Windows x64 环境 |
| --- | --- | --- |
| 编辑 C#/XAML/Go、契约设计、代码审查 | 主场景 | 可选 |
| 共享 Go 逻辑测试、非 Windows .NET 逻辑测试 | 可以；需安装相应工具链 | 同样运行 |
| Go Core 生成 Windows PE | 完成平台拆分后，目标 `windows/amd64`、`GOAMD64=v1`，优先 `CGO_ENABLED=0` | 原生构建再交叉核对 |
| WPF/XAML、Windows Service 完整发布 | 不将跨目标参数视为已验证的完整发行路径 | **规范构建与调试环境** |
| Authenticode、安装器、UAC、SCM、Credential Manager | 不能用 macOS 测试结果替代 | **必须实测** |
| Wintun、路由、DNS、休眠、Windows 防护兼容 | 不能本机验证 Windows 内核行为 | **必须实测** |

Mac 上的 Windows ARM64 虚拟机可辅助编辑和部分 x64 用户态界面验证，但 **x64 程序仿真不等于 x64 内核驱动可用**。不得在 ARM64 Windows 下拿 x64 Wintun 包的结果代替 x64 验收，也不为“方便本机测试”悄悄新增 ARM64 产品。完整 x64 仿真可作为慢速辅助实验，不作为性能和发布基线。

优先准备一台 Intel/AMD x64 Windows 测试机，或在 x64 宿主上运行 Windows 10/11 x64 虚拟机，通过远程桌面从 Mac 操作。Windows CI 用于构建、单测和受限集成；Runner 若实际为 Windows Server，就不能标作 Windows 10/11 兼容性已通过。特权网络测试只在可恢复、明确授权的隔离测试机运行。

### 3.3 支持范围与锁定清单

> **第 1 轮已修订：** 联网核对 .NET 10 官方支持表（2026-09-28 更新）后，Windows 10 22H2 不在其中，故产品目标改为 **Windows 11 x64 24H2/25H2**，Windows 10 22H2 仅在启用 ESU 时尽力而为、不作承诺。见 [docs/windows-foundation.md](docs/windows-foundation.md)。下面保留原规划文字供对照。

原规划建议产品起点为 **Windows 10 22H2（19045）x64 兼容目标 + Windows 11 x64 经测试的具体版本**，不覆盖所有早期 Windows 10，也不承诺尚未测试的未来 Windows。操作系统生命周期、ESU 条件和 .NET 对具体 OS 的支持是第 1 轮核对项；能够启动不等于该系统适合安全商用。若运行时与旧 OS 的支持要求冲突，明确调整发行支持矩阵，不默默采用失去维护的运行时。

第 1 轮锁定：Go 工具链、Mihomo 提交、sing-tun 版本、Wintun 来源/版本/架构/哈希/签名、.NET SDK 与运行时补丁、NuGet 依赖锁、Windows SDK、安装器、IPC 版本、最低 OS 构建和 CI 镜像。现有 Mihomo 与 Go 依赖不随意升级。发布包默认兼容基础 x64 指令集，不使用开发机特有的 CPU 优化作为客户最低要求。

本轮网页检索不可用，未联网核实 Microsoft、Wintun、安装器的当前文档、安全公告和支持矩阵；附录提供官方复核入口。第 1 轮须形成固定版本证据，不能把这些外部条目标为本轮已验证。

## 4. 最小运行架构：三个产品进程，唯一业务所有者

```text
Next.js / Mantine 管理员页面
          │ 配置套餐、节点、倍率
          ▼
Gin API / PostgreSQL ── mTLS ── 受管节点 NodeAgent / 转发器
          ▲ HTTPS                         ▲ 实际代理流量
          │                               │
AsterLink.exe ── 本地受限 Named Pipe ── AsterLink.Service.exe
  WPF，普通用户                               SCM，Session 0
  只消费脱敏状态                              管进程/权限/恢复
                                                │ 私有 IPC 句柄
                                                ▼
                                      asterlink-core.exe
                                      Go 会话/目录/计量/grant
                                      内嵌 Mihomo → Wintun
```

运行时不另起第四个 Mihomo exe，不运行 Next.js/Node.js/WebView，不增加 updater 守护进程。Windows Service 本身不重复登录 API、保活或上报流量；**Core 是 HTTP 会话、配置、租约和 Meter 的唯一所有者**。C# Service 是窄接口的生命周期监督者，不是第二套代理控制平面。

Service 首版使用经审查的特权账户运行，建议以 LocalSystem 完成权限验证，再按实际所需权限收敛；Core 随之处于高权限边界，不能声称它被低权限沙箱隔离。管理员授权集中于安装/修复/升级，日常 GUI 以普通用户运行，连接时不反复弹 UAC。

安装后服务启动只做本产品残留恢复和等待 GUI，不凭磁盘缓存自动恢复代理。一次只允许一个 Windows 用户会话拥有一个 Core/TUN；跨用户争用拒绝或先停止原会话，不共享原用户 token。普通锁屏与用户切换区分：锁屏可维持仍有效的既有连接；注销、切换所有者、Core 重启必须关闭旧授权。首版不提供开机自动连接、故障自动连接或无缝切节点。

### 4.1 文件与模块规划

以下为**计划新增/拆分**，不是当前已有工程目录：

```text
apps/windows/
  AsterLink.sln
  global.json / NuGet.config / 锁定的 NuGet 依赖
  .env.example                       # 与 macOS 相同的三个公开构建键
  AsterLink.App/                      # WPF Views、ViewModels、主题、托盘
  AsterLink.Service/                  # SCM、管道鉴权、WorkerSupervisor、恢复
  AsterLink.Shared/                   # DTO、帧、版本、构建配置校验
  AsterLink.Tests/                    # 逻辑、契约与安全负测
  Packaging/setup.iss                 # 安装、升级、卸载
native/core/
  cmd/asterlink-core/                 # 平台入口、私有句柄、退出处理
  internal/network/*_windows.go       # 物理出口/网卡身份/路由/解析器
  internal/engine/*_windows.go        # TUN、DNS、IPv6 与恢复
  internal/managed/                   # 继续共享，不能复制成 Windows 专版
shared/contracts/                    # 复用现有 JSON；增量变更需 Go/Swift/C# 同测
scripts/
  build-native-windows.py
  test-native-windows.py
  package-native-windows.py
.github/workflows/windows.yml        # 基础 CI
.github/workflows/release-windows.yml # 受保护的发布工作流
artifacts/native-windows/<run-id>/    # 不提交，脱敏报告/安装包/清单
```

安装位置：`%ProgramFiles%\AsterLink\` 的受保护版本目录；服务恢复状态在 `%ProgramData%\AsterLink\`；普通偏好在 `%LocalAppData%\AsterLink\`。不要在用户可写 Downloads、Temp、工作目录或 PATH 中寻找特权 Core/DLL。产品自有目录的写权限分别约束为 SYSTEM/Administrators 或所属用户，不向 Everyone 开写。

### 4.2 本机 IPC、身份与进程完整性

GUI→Service 的 Named Pipe 只接受本地客户端，设置明确 DACL，校验真实进程的用户 SID、登录会话和已安装 GUI 身份；不能仅比较调用方提交的 PID、文件名或管道名称。Service 使用实际客户端进程/令牌信息，必要的 impersonation 完成身份检查后立即 Revert，再做特权操作。SYSTEM 服务与交互用户不是同一用户，不能误用 `CurrentUserOnly` 当作完整授权策略。

GUI 也要验证管道服务器来自 SCM 登记的真实服务进程及受保护安装路径，防止伪服务先占管道套取密码。拒绝远程管道、未批准用户、错误签名、低完整性调用和不兼容版本。签名校验与进程句柄、路径检查绑定，避免仅查询一个可被复用的 PID；管理员能控制设备的情况不在客户端防篡改保证内。

Service 只启动受保护目录内、版本与签名/哈希均匹配的 Core。服务可执行路径必须正确引用，拒绝 reparse point、路径穿越、不可信替换和继承的任意启动参数。GUI 不得传入任意 Core 路径、shell 命令、磁盘配置文件或运行时 API 覆盖地址。

Service→Core 使用明确限制继承范围的私有管道句柄，帧通道与 stdout/stderr 分离。日志不得占用帧句柄；只修改 Mihomo 日志等级不构成隔离。使用 Job Object 管理 Worker，设置 kill-on-job-close，Job 句柄不继承给子进程，避免监督者死掉后 Worker 继续代理。正常退出先有界停流/结账；超时再终止 Job 并恢复本产品网络资源。

协议沿用 [ipc-v1.json](shared/contracts/ipc-v1.json)：4 字节大端长度 + 单个 JSON，最大 2 MiB；普通命令使用更小限制。保留 request_id、protocol_version、generation、超时、取消、幂等停止和旧响应丢弃。Core/Service/GUI 版本不兼容必须提示修复，不能勉强接通。

当前业务命令存在“IPC `ok=true`，但 Snapshot 附带 `action_error`”的返回形式，见 [service.go](native/core/internal/service/service.go)。C# 必须同时处理传输结果、action_error 和状态；不能用 `ok=true` 或按钮点击完成就显示“已连接”。

## 5. 必须先拆的平台耦合与一个现存配置风险

| 当前源码 | 已确认的问题 | Windows 开发要求 |
| --- | --- | --- |
| [Core 入口](native/core/cmd/asterlink-core/main.go):12、36–46、80 | 无条件导入 Unix，使用 Dup/Dup2 与 POSIX 信号 | 平台文件拆分；Windows 使用私有句柄/SCM 生命周期，不以空实现绕过编译 |
| [network_other.go](native/core/internal/network/network_other.go) | `!darwin` 只是测试占位，出口绑定 no-op，PhysicalDefault 不支持 | 新增 Windows 实现；其它平台 tag 改为排除 Windows，不静默落回占位 |
| [mihomo.go](native/core/internal/engine/mihomo.go):70–105 | 非 darwin 拒绝 TUN，网卡识别依赖 utun 名称 | 分离平台 adapter，以 Windows 网卡 GUID/LUID/索引和实际路由验证就绪 |
| [sysdns.go](native/core/internal/engine/sysdns.go) | networksetup、mDNSResponder、/Library 路径 | macOS 实现保留，Windows 用自己的 DNS/恢复适配 |
| [ipv6.go](native/core/internal/engine/ipv6.go):126–149 | `/sbin/route -reject` 是 macOS 路径 | 复用能力探测意图，不复制命令；实现 Windows 早期拒绝或完整 IPv6 代理 |
| [manager.go](native/core/internal/managed/manager.go):187 | 登录硬编码 `Platform: "macos"` | 从受信平台构建身份确定 `windows`；不要让客户端自行伪造平台获得权限 |
| [network.go](native/core/internal/network/network.go) | 竞争 VPN 检测和网络签名有 utun/macOS 假设 | 按实际路由归属区分自身 TUN、其它 VPN 与无害虚拟网卡 |
| [config.go](native/core/internal/engine/config.go):59 | 固定运行模板只有 `MATCH,node` | 增加末尾拒绝规则或等价强制拒绝，并验证不支持 UDP 时不直连 |

最后一项的静态依据是固定内核 [tunnel.go](native/third_party/Clash.Meta/tunnel/tunnel.go):685–687 会跳过不支持 UDP 的适配器，710 行在规则耗尽后选择 DIRECT。这是本轮源码检查发现的风险，**不是本轮在用户机器上复现出的泄漏结果**。旧 PR 的防护不能假定已在新模板继承。

处理方式：先按原 hash/共享策略验证服务端单节点文档，再在 Core 生成的本地受控运行模板中追加 `MATCH,REJECT` 或等价策略。不得擅自改变服务端规范化文档与版本计算，导致 macOS/API 契约失配。必须有 `udp:false` 的 IPv4/IPv6 UDP 负测和 `udp:true` 正测，不能只检查 YAML 文本包含 REJECT。

每次共享代码修改都跑 macOS 和 Linux 基础回归。平台抽象解决真实能力差异，不为方便编译把错误吞掉，也不把整个 Core 分叉成两个长期漂移的账本实现。

## 6. API、配置与凭据：复用已有契约

### 6.1 原生接口清单

下列客户端路径的前缀统一为 `https://demo.hyshentou.cn/api/v1/client`，不是网页 Cookie 登录，不是新建 `/api/v2`。

| 接口 | 用途与不可改变的语义 |
| --- | --- |
| POST `/login` | email/password/device_id/platform/app_version；返回不透明 Bearer token、user、session；Windows 平台后端已经支持 |
| GET `/session` | 刷新授权、期限、确认余额和计量来源；使用服务端时间与租约参数 |
| GET `/nodes?revision=…` | 套餐过滤后的元数据目录；单长轮询，不下发节点秘密；成功不等于续授权 |
| GET `/config?node_id=…&last_sequence=…` | 仅开发 client_reported 路径，获取当前选中节点文档；使用已确认序号 |
| POST `/traffic` | 开发模式唯一 Meter 发 sequence 和双向累计计数；响应包含 session/replayed |
| POST `/logout` | 有界撤销；本地先停数据通路 |
| GET `/capabilities` | 使用已存在的契约能力、计量模式和最低版本信息 |
| POST `/grants` | node_reported 创建授权，Idempotency-Key 重试保持不变；pending 不允许连接 |
| GET `/grants/:id` | ready 才可消费当前 grant 的独立凭据和有效 session |
| POST `/grants/:id/revoke` | 本地先停流，然后幂等撤销；等待节点确认或安全租约处理尾账 |

接口包装层按 [native-v1.json](shared/contracts/native-v1.json)、[native-v2.json](shared/contracts/native-v2.json) 和现有 apiclient 测试读取，不统一猜成 `{data:…}`。C# 主界面只消费 [snapshot-v1.json](shared/contracts/snapshot-v1.json)，不直接向 API 拉配置和扣费。

目录默认等待 5 秒、服务端有每秒查询回退；客户端现有 watcher 超时为 40 秒，覆盖后端可配置的最长 30 秒窗口，不从旧文档照抄 10 秒。保持一个 watcher；429/5xx/断网使用有界带抖动退避，前台刷新去重，不以高频轮询代替正确状态同步。上线前测限流和并发长轮询容量。

管理员修改可见目录中其它节点也可能影响当前 profile hash；首版保留已有保守停连策略，不承诺“只影响当前所选线路”或零中断热切换。名称、倍率、启停、套餐白名单和删除均需实测同步。正常网络下以约 5 秒内反映为验收目标并记录观测值，不宣传“保存瞬间全球生效”。

### 6.2 `.env` 的归属和生效方式

保留 `frontend/.env`、`backend/.env`；新增 `apps/windows/.env` 和无秘密的 `.env.example`。Windows 示例与已有 macOS 公开构建契约一致：

```dotenv
APP_ENV=development
API_BASE_URL=https://demo.hyshentou.cn/api/v1/client
WEBSITE_BASE_URL=https://test.hyshentou.cn
```

Windows `.env` 是**构建时输入**：白名单解析后写入签名包内的受保护构建配置。编辑源码目录 `.env` 后重新构建、安装才生效，不让客户修改同目录 `.env` 就绕过 Release 安全策略。`APP_ENV` 采用现有 `development/release` 契约；后端的 `APP_ENV=production` 是另一层配置，不能混淆。

复用 [env-v1.json](shared/contracts/env-v1.json) 的有效/无效输入；其历史名称含 macOS，可在多端同步变更时澄清描述，不能只为 Windows 改规则。拒绝重复键、未知键、shell 展开、URL 内凭据/查询/错误路径；Release 必须 HTTPS，开发 HTTP 仅允许回环。

Service 必须核对 Core 登录命令中的 API base/channel 与受保护构建配置一致，不能信任 GUI 任意提供的地址。前后端需要调整的业务常量继续放各自环境配置；但证书信任、帧长度、路径权限等安全约束不能开放为任意用户可调开关。

后端已有四个参数，不需要 Windows 再造一套：`CLIENT_REPORT_INTERVAL_SECONDS=60`、`CLIENT_LEASE_SECONDS=90`、`CLIENT_IDLE_TIMEOUT_SECONDS=180`、`CLIENT_NODE_WATCH_SECONDS=5`。客户端使用 API 实际响应值；调整后按后端部署方式重启/重载。节点与套餐数据由管理员/API 同步，不能塞进 App `.env`。

数据库账号、节点密码、NodeAgent mTLS 私钥、签名私钥、用户密码、token 不得进入 Windows 或前端公开环境变量。Windows 不连接 PostgreSQL。对话中披露过的测试密钥不写入本文件，外部分发和真实运营前必须轮换。

### 6.3 记住密码和用户身份

密码只在用户主动选择时保存到当前 Windows 用户的 Credential Manager，目标名以产品 + API base + 规范化邮箱隔离。普通关闭/退出可保留主动保存项，但不保留可直接恢复连接的 Bearer token；重新打开必须在线登录。显式退出登录清除对应记住密码项；保存失败提示用户，不回落到明文 JSON、注册表、日志或 LocalMachine 范围的共享秘密。

device_id 使用随机 UUID，按产品安装/当前用户的既定范围稳定存储，升级不随意变化，不采集 MAC 地址拼硬件指纹。不同 Windows 用户不得共享密码和会话。UI 登录时不可避免短暂接触密码，减少复制并及时释放引用，不夸大托管内存的绝对清零能力。

普通 Snapshot、日志、诊断包、崩溃上报不含原始 YAML、节点地址/端口、password、token、UUID 私密凭据。客户端确实需要在 Core 内存中消费节点秘密；“用户界面不展示”不是设备所有者永远无法提取，真正防滥用仍依赖节点授权与配额。

## 7. Windows TUN、DNS、路由与生命周期

### 7.1 固定运行配置与权限

管理员只管理节点定义。Core 校验文档一致性、共享 nodepolicy、Mihomo 语义之后，组合审查过的 TUN/DNS/路由模板；不把管理员整个 YAML 当 SYSTEM 配置执行。不开放任意 provider、脚本、插件路径、controller、mixed/http/socks 对外监听或 allow-lan。

优先验证锁定内核的 `system` TUN 栈；第 2 轮须做 Windows 实传 PoC。若该组合确有兼容阻塞，只经明确记录的一次技术决策更换支持的栈并更新测试，不能失败后悄悄退成 system proxy。当前固定版本 `ApplyConfig` 不等于启动 TUN，适配器必须正确执行 listener 创建并检查真实接口和路由。

核对固定 sing-tun 的 Windows `strict-route`、DNS 配置和 Wintun 加载行为后再使用。严格路由不是万能防泄漏或 Kill Switch 标签。驱动/DLL 来源和完整性在安装/启动时验证，加载只允许受保护的明确路径或经审查的内嵌路径，不联网临时下载，不从当前目录/PATH 搜索。保持 Secure Boot 和正常驱动签名检查，不引导用户关闭系统安全防护。

### 7.2 平台实现要求

| 项目 | 必须实现和验证的行为 |
| --- | --- |
| 真实 TUN | 不配置 WinINET/WinHTTP/浏览器 HTTP 代理，普通 TCP/UDP 客户端仍经选中节点；验证网卡、路由和受控出口 |
| 接口身份 | 使用 GUID/LUID 与当前索引关联，处理重启/重新插网卡后的索引变化；不能仅依靠显示名称 |
| 物理出口 | 通过 Windows IP Helper/套接字能力定位实际路由；对节点外层和 API 的 v4/v6 分别绑定或按受控路由排除，实测 IP_UNICAST_IF/IPV6_UNICAST_IF 等调用语义 |
| 解析器 | 核实 Go 在 Windows 上实际使用的 resolver 路径；不能假定设置 PreferGo/Resolver.Dial 就必然拦住系统解析 |
| 控制面 | HTTPS API、节点 bootstrap DNS 必须在 TUN 工作时仍可直达；不依赖付费节点，API 重定向不能携带秘密，不绕过证书验证 |
| 动态地址 | API/节点域名 TTL、A/AAAA、物理网卡切换都触发必要重评估；不永久硬编码测试域名的 IP |
| 系统 DNS | 覆盖 DHCP 路由器 DNS、手工公共 DNS、多网卡并行解析、系统加密 DNS与浏览器 DoH；真实 Windows DNS Client 路径测试，不只跑 Go 纯解析器 |
| IPv6 | 节点可承载则完整代理；不能承载则在浏览器不会“假握手成功后假死”的阶段拒绝并能回退 IPv4；不能仅关闭模板 IPv6 让系统走物理出口，也不能全局关闭系统 IPv6 |
| UDP/QUIC | 支持的组合真实转发；不支持明确拒绝，不回落 DIRECT；非标准 STUN 端口也测试 |
| 竞争 VPN | 按有效路由/资源冲突拒绝叠加，不删除其它 VPN 路由；不把所有 Hyper-V/WSL/虚拟网卡都当冲突，也不把自己的 TUN 当网络切换 |
| 资源恢复 | 只清理本产品本代次拥有的接口、路由、DNS/过滤规则；清理失败进入需恢复状态，不能继续新连接 |

DNS 与路由修改必须只有一个明确的所有者：先审查 sing-tun 已做的操作，再决定适配层补什么，避免两个模块重复修改和各自恢复。优先使用 Windows 结构化 API，避免解析本地化 `netsh` 文本；不得以全局 `netsh reset`、禁用网卡或清空系统防火墙来做“恢复”。

如实测证明仅路由/strict-route 无法满足连接期间的物理出口防绕行，使用最小的 Windows Filtering Platform 用户态过滤策略补足，或者阻塞该发布能力；不自研内核过滤驱动。规则要绑定本产品资源和生命周期，保留必要控制面例外，并覆盖显式绑定物理地址的普通应用。不得把“禁几个 STUN 端口”写成 WebRTC 防护。

首版**不承诺断线后持续阻断整机网络的系统级 Kill Switch**。正常断开后恢复本机上网；异常退出后不得有隐藏付费代理继续转发，但也不能承诺任何直接互联网包都永久阻断。连接期间无未声明泄漏与断开后的网络策略是两个不同验收维度，界面/宣传必须一致。

### 7.3 所有权日志和崩溃恢复

在首次修改系统网络前，先原子持久化受保护的恢复记录：格式版本、会话代次、网卡 GUID/LUID、原 DHCP/手工 DNS 状态、创建的路由/过滤规则标识、已应用状态。记录不含节点凭据；权限仅 SYSTEM/Administrators。必要处做持久化刷盘，不能只把 JSON 写进缓冲区就声称可恢复断电。

恢复时比较“当前值仍为本产品设置”与资源归属，用户或其它 VPN 中途改过的配置不得用旧快照覆盖。损坏记录不可猜测删除全局资源，应阻塞重连并给出产品范围的修复入口。Core 正常退出、Service 检测 Worker 死亡、SCM 服务重新启动、升级/卸载都调用同一恢复逻辑。

建议服务开机启动但不自动连接：开机只回收被确认已结束的本产品会话残留，再进入空闲，降低 macOS 历史“重启后等下次打开 App 才恢复 DNS”的风险。不得删除其它产品共用的驱动安装或同类网卡；Wintun 清理须核实本产品拥有的实例。

### 7.4 状态机与操作顺序

```text
SignedOut → Authenticating → Ready → Preparing → Connecting → Connected
                                 ↑                         ↓
                                 └── Stopping / Settling ──┘
任何阶段 → Blocked（权益/额度/版本）或 Error（网络/服务/内核）
```

开发连接：刷新有效 session/目录 → 验证选中配置 → 平台预检 → 初始化唯一 Meter 并取得首个有效 ACK → 开启 TUN/DNS/必要防护 → 真实转发探测 → Connected。Meter 未初始化时不得伪造报告；ACK 丢失保持关门，原批重试。

权威连接：刷新能力/session → 创建并等待 ready grant → 校验 grant 凭据/期限/节点 → 平台预检和 TUN → 实传探测 → Connected；不得在该模式同时调用开发 `/traffic` 扣费。本地计数只供速率和用量展示。

停止/切节点/改倍率：先阻止新流并关闭存量流 → 等计数稳定 → 完成旧段有界结账或撤销 → 清理本代资源 → 同步新配置，保持断开等待用户明确连接。网络探测使用受控的预期响应及路径/流量证据，不能仅凭任意 HTTP 200（可能是认证门户）宣称代理成功。

租约按服务端截止时间和请求开始时的本机时间保守计算，网络耗时消耗租约。Windows 使用经确认包含睡眠时间的连续计时来源，并接入电源/会话变化事件；不能照抄某个计时器就假设其睡眠语义。睡眠/休眠恢复先停旧通路，再在线验证；时钟回拨、迟到响应、旧 generation、Core 重启不得延长授权。

关闭窗口保留托盘连接，首次明确告知；点击“退出”停止代理并有界结账；点击“退出登录”额外清保存密码。GUI 崩溃/管道关闭须使 Service 停止该会话；Service 崩溃由 Job 约束 Worker，重启后只恢复网络不恢复旧授权。测试真实硬杀和断电，不以 finally 回调替代证据。

## 8. 计费：保持已有整数与授权边界

### 8.1 口径不变

```text
up_delta   = current_upload   - acknowledged_upload
down_delta = current_download - acknowledged_download
charged_units = (up_delta + down_delta) * bound_rate_permille
remaining_bytes = max(0, floor((total_bytes * 1000 - used_units) / 1000))
```

1 unit = 1/1000 字节；500‰ = 0.5×，1000‰ = 1×。Go 与 C# DTO 使用准确的整数类型及溢出检查，测试超过 2^53 的值，不经浮点 JSON/图表反算账单。UI 可把字节换成 GiB（2^30），必须标明单位。

例：上传 20 MiB + 下载 80 MiB，共 104,857,600 字节。1× 扣 104,857,600,000 units，即 100 MiB 套餐量；0.5× 扣 52,428,800,000 units，即 50 MiB 套餐量。上传下载都算，不只扣下载。

“普通直连线路”的元数据 `line_type=direct` 仍然是远端代理线路，**不等于 Mihomo DIRECT**。倍率由管理员的 `rate_permille` 决定，不能在 UI 按专线/普通线路硬编码。专线标签也不是客户端可以凭名称保证超低延迟的证据。

### 8.2 开发账本与权威账本不能混用

客户端开发 Meter 读取固定内核的代理累计量；不是网卡总流量、文件正文大小、每秒速率积分或 UI 曲线总和。同一会话断开/切线路不重置 sequence 和累计；只能在旧段确认后按已定义的 epoch/offset 规则接续，意外计数回退则停连。

只允许一个 pending 报告。响应丢失时原 sequence、原累计内容重试，不用新的序号或新的累计替代未确认批。旧段按旧倍率，旧订阅周期尾账不能扣新周期；数据库事务、幂等和预算继续由现有后端负责。

控制 API、回环管理流量不计费；经付费节点的 DNS、连通性探测及普通系统背景流量依实际口径计费，需说明。Mihomo 与 NodeAgent 的统计层不同，不能要求两者无条件等于文件正文长度；受控测试应记录正文、握手/封装、统计边界和可解释差异。

`client_reported` 不能防止修改客户端少报，硬杀前未确认尾账也不能保证追回。商业模式必须复用 `node_reported`：每 grant 独立凭据、真实节点累计、预算预留、存量 TCP/UDP 撤销和租约截止。用户完全不发客户端用量报告，仍应由节点计费。

当前后端已经有 grant、NodeUsage、SubscriptionPeriod 等表和接口，不要为 Windows 新建重复账本。Windows+macOS 多设备共享额度的竞争要一起测；超时不能立即释放尚未安全结清的预算，否则另一设备可能重复消费。生产能力由后端和受管节点决定，不相信客户端传入的 metering_source 或测试标志。

## 9. 商业化 UI：三页、一条主路径，不做通用 Clash 控制台

### 9.1 视觉与布局

沿用产品名 AsterLink。默认明亮、低饱和界面，深色主题作为同一套资源字典的可选模式；不依赖 Mica/透明特效才能正常显示，以保证 Windows 10 的一致体验。主窗口建议约 1000×680 DIP，最低 800×560 DIP，侧栏约 180–200 DIP；100%/125%/150%/200% DPI、长中文/英文、键盘导航都要验收。

采用 8 DIP 间距节奏、16–24 DIP 内容留白、适度 8–12 DIP 圆角、明确字号层级。使用系统字体与少量一致的矢量图标；只给主操作一个强调色，连接状态同时用文字和图形表达，不只靠红绿。优先原生窗口行为、清晰焦点和辅助功能，不重画一套难用的标题栏。

不做炫光、地球旋转、复杂地图、假测速、夸张仪表盘或隐藏费用。动态效果只服务状态过渡，遵守减少动画偏好。小型双向速率图直接用 WPF 图形绘制有界采样，首版不引入大型图表依赖，也不无限积累历史点。

### 9.2 页面与用户操作

| 页面 | 内容 | 用户的主要操作 |
| --- | --- | --- |
| 首次使用/登录 | 服务安装或修复状态、邮箱、密码、记住密码、登录；登录失败和权限取消有明确说明 | 完成一次必要授权，登录；批准后自动继续初始化，不需要猜测“刷新”有没有用 |
| 连接 | 大而明确的状态区、当前线路卡片、连接/断开按钮；套餐名称、到期时间、确认余额；上下行速率、代理累计用量、轻量曲线 | 选一条线路、连接；套餐失效明确禁用并给购买/续费入口 |
| 线路 | 名称、地区、普通/专线、倍率、可用权限；搜索和简单类型筛选，选择状态清晰 | 选择有权限节点；管理员修改后同步，不提供 YAML/订阅导出 |
| 账户与设置 | 账户/套餐摘要、网站入口、主题、版本/服务状态、脱敏诊断、退出登录 | 在默认浏览器购买/续费；复制安全诊断；断开后修复服务 |

首次使用/登录是入口态，登录后仅“连接 / 线路 / 账户与设置”三页。支付、套餐编辑、节点 YAML、管理员功能都留在网站。普通客户端不提供代理组编辑、规则模式、局域网分享、端口设置或协议参数编辑。

无权限/无套餐/到期/余额为零/无节点/API 离线/服务缺失/连接失败都设计完整空态。普通节点列表不泄露配置；未测延迟显示“未检测”，不能填随机 ms。首版可以不展示延迟，避免后台全量测速额外消耗流量和引入接口。

已连接时“更换线路”提示会先断开和结算；结算 pending 与服务器确认余额清楚区分。不能显示“连接成功”却没有真正转发，也不能把估算剩余量当已确认账单。操作进行中按钮去重，但应能取消连接并最终回到确定状态。

## 10. 八轮开发安排与逐轮验收

### 第 1 轮：锁定环境、原生工程和可行性

**目标：** 先证明工具链、目标 OS 与三组件方案可落地，避免先做完 UI 再发现没有可用的 Windows 构建环境。

**开发内容：** 固定版本清单和 Windows 支持矩阵；准备/确认 Windows x64 构建与测试节点；建立最小 WPF 窗口、Service 工程、共享 DTO 和测试工程；Windows CI 能实际运行并保留产物。审查 Wintun 分发方式、签名与许可；确定 Go 平台拆分清单和发布身份。只加入公开 `.env.example`，不安装未知下载源的工具。

**验收：** Windows x64 原生构建并打开最小窗口；核实实际 OS 与 CPU 架构；C# 能正确读取共享 Snapshot 整数/日期/错误夹具；Mac 可运行已有共享逻辑测试。未取得 Windows 环境时，该轮环境项记 BLOCKED，而不是拿 ARM64 仿真或 mock 填通过。

**交付：** 最小工程、工具链锁、Windows 基础 CI、真实环境清单、TUN PoC 的测试前置条件。此时无需完整美化，也不允许已有未完成 TUN 路径显示“已连接”。

### 第 2 轮：Go 平台拆分与真实 Windows TUN PoC

**目标：** 从 M4 交叉编译出 Windows x64 Core，并在授权的 x64 Windows 上证明真实 TUN 能创建、转发、停止恢复。

**开发内容：** 拆 Unix/stdout/信号、macOS DNS/IPv6/网卡耦合；实现 Windows 物理出口和接口识别，修复登录 platform；完成私有句柄协议基础；核实 Wintun 加载和固定 `system` 栈。先用测试专用受控 SS/VLESS 目标跑小型 PoC，不把测试入口装入 Release。补末尾拒绝规则和不支持 UDP 的负测；固定 IPv6/DNS 的 Windows 方案与恢复所有者。

**验收：** Mac 生成真实 Windows amd64 PE，不含 fakeengine；Windows 不设置系统 HTTP 代理即可完成受控 TCP 请求和出口验证；TUN 失败、权限不足、驱动不匹配时明确失败；停止恢复本产品资源；真实日志不污染 IPC。基础支持 UDP 的线路和拒绝不支持 UDP 的路径均验证。所有共享改动跑 macOS 回归。

**交付：** 可交叉构建的 Core、真实 TUN PoC、Windows 网络适配设计和最小恢复证据。若栈或驱动不合适，在这里作一次明确调整，不拖到发布轮。

### 第 3 轮：Windows Service、受限 IPC 与安装授权骨架

**目标：** 普通用户 GUI 能安全使用特权 TUN，不要求每次以管理员运行 App。

**开发内容：** SCM 服务、Named Pipe 双向身份校验、受保护 Worker 路径、版本门、Job Object、唯一所有者；Core EOF/崩溃处理；ProgramData 网络恢复日志与开机只恢复不连接。建立开发安装器，清楚区分内部签名与正式可信发行；UAC 取消、服务未安装、启动失败、需要修复都有反馈。

**验收：** 普通用户→真实 Service→真实 Core 完成 Hello/连接/停止；拒绝假 GUI、假 Service、远程管道、其它用户、旧版本、超长帧和可写目录中的假 Core/DLL；杀 Service 后没有活着的孤儿代理；重新启动仅恢复，不复用旧 token。安装取消不留下半套可转发服务。

**交付：** 安全的三进程链路和可安装开发骨架，服务权限/进程/恢复负测报告。若身份或签名验证条件不足，保留 BLOCKED，不通过放宽 DACL 解决。

### 第 4 轮：正式界面、登录、记住密码、套餐和节点同步

**目标：** 网站管理员配置的数据通过现有 API 进入 Windows App，用户操作简单、状态可信。

**开发内容：** 实现第 9 节三页、登录引导和托盘；GUI 只读 Core Snapshot；接入 Credential Manager 与稳定 device_id；真实账号登录、权限、目录监听、网站购买入口、公开构建配置校验。处理 generation、重复操作、action_error、离线和额度/到期空态。

**验收：** 正确/错误密码、保存/取消/删除密码、凭据库不可用、管理员无套餐、设备上限、无节点、0 额度、到期都正确；`test@test.com` 也必须有有效权益才可代理，不能因管理员身份放行。后台修改名称/倍率/启停/白名单/删除能在正常网络下及时同步；只读目录不能维持过期租约。UI 和诊断不含 YAML/token/密码。

**交付：** 真实账户与节点界面、DPI/键盘/空态截图和 API 契约测试。此轮完成设计主干，不增加地图、全量测速、客户端支付等功能。

### 第 5 轮：完整 TUN 与真实开发计量闭环

**目标：** 达成用户优先需求：登录 → 选节点 → 连接可上网 → 真实用量 → 正确倍率扣减 → 到期/用尽停连。

**开发内容：** 将平台 TUN 接入共享状态机和唯一 Meter；连接探测、上下行曲线、确认/估算余额、pending 提示；切节点/改倍率/断开/登出先停流结算。真实 SS AEAD 与实际使用的 VLESS TLS/Reality 组合分别验证；协议能解析不能代替握手证据。

**验收：** 受控上传/下载在 0.5×、1× 下与既有整数账本一致；ACK 丢失、同批重试、不同内容同序号、计数回退都不双扣或静默漏掉；普通线路仍经代理。后台禁用/删除、套餐到期和额度耗尽使官方客户端关闭新旧流。公网浏览器请求确实可用，不以 Core 内部探测单独代替用户体验。

**交付：** Windows x64 内部功能安装包、前台→真实 API→隔离数据库→真实代理链路报告。此时 client_reported 节点仍标开发计量，不解除生产保护。

### 第 6 轮：Windows 网络可靠性、防泄漏与故障恢复

**目标：** 消除 macOS 真机曾暴露的同类问题，并证明异常情况下网络能恢复。

**开发内容：** DHCP/手工 DNS、多网卡、IPv4/IPv6、UDP/QUIC/WebRTC、浏览器 DoH 与源地址绑定矩阵；必要的有界连接态 WFP 防护；网络切换/睡眠/休眠/时钟回拨/用户切换、GUI/Core/Service 硬杀、断电恢复；重复启停和长时采样。优化只针对实测问题，不新增系统优化器。

**验收：** 两类 OS 上普通浏览器与系统解析器走预期路径，节点无 IPv6 时不出现假连接/长时间假死；API 和节点外层不自循环；明确例外之外无连接期间的公网物理出口泄漏。至少 20 次启停、10 次睡眠/网络切换组合和一次 8 小时连接采样作为目标，记录真实 CPU/内存/句柄/线程曲线，不填猜测性能数字。重启后不需用户手工修全局网络。

**交付：** 网络/故障矩阵、脱敏证据、实测性能基线和已知限制。未测的系统/驱动/网络组合不列入支持承诺。

### 第 7 轮：复用现有 grant，验证节点侧商业强制控制

**目标：** Windows 不只是“会按规则自报”，而是接入已有服务端权威收费机制。

**开发内容：** 复用 `connectGranted`、能力协商、ready grant、撤销、节点确认余额，补 Windows 全链路测试和 UI 来源标识。优先以可管理的 VLESS + TLS 节点完成商业最小闭环；节点的真实协议/传输组合必须与参考转发器能力吻合。公网不使用明文 VLESS 测试配置。

**验收：** 完全关闭客户端流量上报，节点仍计费；在授权的测试环境将测试凭据用于独立客户端，撤销/到期/额度耗尽后，新连接和已建立 TCP/UDP 都失效；Windows+macOS 多设备竞争最后额度不超分；API 断连/NodeAgent 重启/乱序重复用量不会无授权放行或双扣；node_reported 不接受 `/traffic` 再扣一次。

**交付：** Windows↔真实受管节点↔后台账本证据和生产模式能力矩阵。SS 可继续作为已验证功能的开发线路；没有 SS/Reality/XTLS 权威适配时不得向收费套餐承诺这些组合的强制控制。没有节点管理权/合格供应商时本轮商业验收 BLOCKED，但不否定第 5 轮内部功能成果。

不为 Windows 重写 NodeAgent，也不把缺失的多个服务器协议适配塞入“一轮必完成”的承诺。确需将 SS 等加入销售范围时，单独补齐并通过同样的服务器能力门槛；未经验证的能力不进入首发支持列表。

### 第 8 轮：正式签名、安装升级卸载与发布交接

**目标：** 非开发用户在正常 Windows 安全设置下安装使用、升级回退和卸载，问题可定位。

**开发内容：** 对 GUI/Service/Core 逐件 Authenticode 签名与时间戳，再签安装器；核对 Wintun 原有驱动签名与分发完整性；self-contained 离线包、SHA256SUMS、版本/依赖清单、许可证和对应源码材料。升级先断开和结清，再停止旧服务、验证新文件、替换/启动；失败回退到可验证旧版本，不自动恢复旧授权。首版只做手动下载升级，不增加常驻更新器。

**验收：** Windows 10/11 x64 干净机、普通用户安装时输入管理员凭据、取消 UAC、升级中断、签名失效、Worker 版本不匹配、卸载全部验证；安装后 GUI 不以提权账户偷偷运行。保持 Secure Boot/Defender/适用的内存完整性设置正常，记录防护拦截而非建议全局关闭。签名不保证 SmartScreen 永远无提示，须如实验收。

卸载先让当前用户侧清理本产品保存密码（经明确选项），提权卸载器不遍历其它 Windows 用户的凭据库；只删除自身服务/版本文件/所属网络资源，不移除其它 VPN 共用驱动。保留诊断或用户偏好与否有清晰选择，卸载失败不能宣称已恢复网络。

**交付：** 已签名安装包、签名与系统测试证据、哈希/manifest/SBOM 或等价依赖清单、许可证与必要对应源码、升级卸载说明、回退方案、已知限制。证书/真实节点/系统验收未完成时只发布内部通道，不能用 `production_ready=true` 开关代替证据。

## 11. 统一验收矩阵

每项输出 `case_id / expected / observed / PASS|FAIL|BLOCKED|NOT_RUN / commit / build / machine / evidence`。机器记录包括 OS 版本/构建、OS 架构、CPU、关键安全设置、Core/Wintun/.NET 版本。**本文件编制时，下表所有 Windows 测试均为 NOT_RUN。**

| ID | 场景 | 通过要求 | 轮次 |
| --- | --- | --- | --- |
| W01 | Mac 交叉构建 + Windows 原生构建 | 产物 PE x64，真实引擎；macOS 仍可构建，非 Windows 占位不被误用 | 1–2 |
| W02 | 共享 DTO/环境契约 | 日期、包装、action_error、超过 2^53 的整数准确；未知/重复配置拒绝 | 1、4 |
| S01 | Named Pipe 假客户端/假服务/远程/跨用户 | 身份验证拒绝，凭据不泄露，不触发特权执行 | 3 |
| S02 | 替换 Core/DLL、路径穿越、reparse、旧版本 | 启动前拒绝；不能从用户可写目录加载 | 3、8 |
| S03 | 帧截断/超长/迟到/乱序/日志污染 | 有界关闭或固定错误，真实 logger 不污染控制帧 | 2–3 |
| A01 | 登录、保存密码、显式登出 | 在线认证；密码按用户/API/账号隔离，无明文兜底 | 4 |
| A02 | 管理员无套餐、到期、0 额度、设备上限 | 不下发可用代理权限，不因角色绕过 | 4–5 |
| A03 | 目录 watcher、429、网络恢复 | 单 watcher、退避、旧代次丢弃；目录读取不续授权 | 4 |
| A04 | 改名/倍率/停用/删除/套餐白名单 | 同步并按当前保守策略停旧连接，旧段旧倍率 | 4–5 |
| T01 | 无系统代理 TCP，SS 与 VLESS 实传 | 真实 TUN→选中节点→受控目标，错误密码/证书失败 | 2、5 |
| T02 | UDP/QUIC/STUN，支持与不支持组合 | 支持则通过节点；不支持明确拒绝，绝不 DIRECT | 2、5–6 |
| T03 | 系统 DNS、DHCP LAN DNS、手动 DNS、多网卡 | 业务查询无未声明的物理出口；控制 bootstrap 单独识别 | 6 |
| T04 | IPv6 有/无出口、真实 AAAA、浏览器 DoH | 完整代理或早期拒绝+IPv4 回退，不外泄也不假连接 | 5–6 |
| T05 | WebRTC 与显式物理源地址绑定 | 无未声明公网直连；私网/mDNS 与公网地址泄漏分开记录 | 6 |
| T06 | API/节点域名变更、v4/v6 出口 | 控制面可达、不自循环；不用宽泛 DIRECT 网段救场 | 2、6 |
| T07 | Wi-Fi/有线/多网卡/其它 VPN | 正确判断物理变化与冲突，不将自身 TUN 当故障 | 6 |
| T08 | 睡眠、休眠、时间回拨、迟到授权 | 恢复先关门，旧租约不被延长，不自动恢复代理 | 6 |
| T09 | GUI/Core/Service 硬杀、断电、恢复记录损坏 | 无孤儿代理；只恢复本产品资源，不覆盖用户后来改动 | 3、6 |
| B01 | 上传20MiB+下载80MiB，500‰/1000‰ | 受检整数精确扣量；正文与协议统计差异单独解释 | 5 |
| B02 | ACK 丢失、重放、同序号不同内容、累计回退 | 原批重试或拒绝，不双扣，不悄悄重置累计 | 5 |
| B03 | 切节点/中途改倍率/跨套餐周期 | 旧段旧倍率旧周期，新段不继承未确认旧账 | 5、7 |
| B04 | node_reported 无客户端报告 | 节点照常计费；误调 `/traffic` 被拒，账本不重复 | 7 |
| B05 | 提取测试凭据后到期/撤销/超额 | 新连接和已有 TCP/UDP 均受节点强制失效，记录时延 | 7 |
| B06 | Windows+macOS 多设备预算竞争 | 不超分，不因超时提前释放造成重复消费 | 7 |
| B07 | Agent/控制 API 故障与重启 | 租约/预算本地执行，游标与对账可恢复 | 7 |
| U01 | DPI、键盘、长文本、空态、托盘退出 | 三页易操作，无裁切/假状态；关闭窗口和退出语义一致 | 4、8 |
| R01 | 正常安全设置的干净机安装 | 签名/驱动/SCM/首次授权真实通过，不要求关闭安全功能 | 8 |
| R02 | 升级取消/回退/卸载/跨用户凭据 | 无版本混用和网络残留，不动其它用户/VPN | 8 |
| R03 | 20 次启停、10 次恢复、8 小时采样 | 无不可解释资源累积，记录真实性能与失败次数 | 6、8 |
| R04 | 日志、诊断、安装包与源码归档 | 无真实账号密码/token/YAML/数据库和签名秘密；许可材料齐全 | 4–8 |
| G01 | 后端、网站、共享策略、macOS 回归 | 保留既有测试，不因 Windows 支持而削弱生产保护 | 每轮 |

DNS 测试优先用受控域名、权威查询日志与物理出口抓包证明路径；公共“DNS 泄漏网站”显示解析器国家/品牌不同并不单独证明泄漏，Anycast/递归解析器所在地也不一定等于节点所在地。浏览器公网出口、系统解析器、独立 UDP 客户端至少交叉验证，不依赖一个网站分数。

普通 CI 不修改开发者全局网络。特权测试必须有明确的 `--allow-network-changes`、受控节点/目标和恢复方案；缺权限/目标则 BLOCKED，不换 fakeengine 后报 PASS。受控账号/节点凭据从安全测试存储注入，不作为 argv 或提交到夹具。数据库默认使用隔离实例；不清空或迁移用户的远端测试库。

## 12. 构建与测试命令契约

以下 Windows 脚本是**后续轮次需实现的接口，目前不存在**。命令是交付规范，不是本轮已经执行成功的记录。脚本须检查实际 OS/架构/工具链/签名条件，报告实际测试数量；零测试不能通过，失败非零退出，BLOCKED 使用明确状态（可沿用退出码 3）。

```sh
# Mac：第 1–2 轮实现后，检查环境与交叉构建真实 Go Core。
python3 scripts/build-native-windows.py --target core --configuration Debug
python3 scripts/test-native-windows.py --suite shared

# Windows x64：第 3 轮起的完整本机构建和基础测试。
py -3 scripts/build-native-windows.py --target all --configuration Debug
py -3 scripts/test-native-windows.py --suite unit
py -3 scripts/test-native-windows.py --suite service

# 授权的隔离 Windows x64 测试机：不在日常办公网络自动执行。
py -3 scripts/test-native-windows.py --suite tun --allow-network-changes
py -3 scripts/test-native-windows.py --suite billing
py -3 scripts/test-native-windows.py --suite recovery --allow-network-changes
py -3 scripts/test-native-windows.py --suite grants

# 第 8 轮：发布秘密由受保护签名环境提供，不放命令行或源码 .env。
py -3 scripts/build-native-windows.py --target all --configuration Release
py -3 scripts/package-native-windows.py --channel release
py -3 scripts/test-native-windows.py --suite release
```

Go 交叉构建的核心目标为 `GOOS=windows GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=0`；这需要先修复第 5 节阻塞，**不能把今天的代码直接改环境变量就当 Windows 客户端**。如特定依赖需要额外构建条件，第 1–2 轮写入锁定清单，不假定所有依赖都可零配置跨编译。

完整 GUI/Service 构建规范采用 Windows x64 的 `dotnet publish ... -c Release -r win-x64 --self-contained true`，明确禁用不适用的 trimming/AOT；安装器打包两个 .NET 组件、Core 和所需驱动分发物。NuGet 使用锁定还原，不在每次构建自动升级依赖。

既有回归入口继续保留：[test-native.py](scripts/test-native.py)、[test-backend-shared.py](scripts/test-backend-shared.py)、[test-backend-standalone.py](scripts/test-backend-standalone.py)、[test-node-catalog.py](scripts/test-node-catalog.py)、[test-native-macos.py](scripts/test-native-macos.py)。只读/单测与真实 TUN 套件分开，在适当平台运行，不将 `go test ./...` 执行成功写成系统集成通过。

## 13. 收费发布门槛与不可省略的外部依赖

| 门槛 | 发布前需要的事实 |
| --- | --- |
| Windows 目标环境 | 真实 x64 Windows 10/11 支持矩阵通过，不只有 Server CI 或 ARM 仿真 |
| 节点控制权 | 自有或供应商提供可验证的每用户授权、可信计数、存量流撤销、租约与预算执行 |
| 账务可信 | node_reported 全链路通过，客户端不上报仍结算；开发与生产账本互斥 |
| 网络安全 | TUN、DNS、IPv6、UDP、WebRTC、崩溃恢复与声明的连接态防护通过 |
| 分发身份 | 合适的代码签名身份、时间戳、驱动签名与安全设置验证；证书费用/获取由项目负责人安排 |
| 许可 | 核对固定 Mihomo/迁移代码、Wintun、NuGet/Go/安装器依赖许可，附 notices 与必要的对应源码/构建材料 |
| 网站运营 | 支付回调验签/金额核对/幂等开通/退款对账，账号恢复与管理员 MFA、TLS、备份恢复、告警、容量与服务条款等 |

开源软件可以用于商业服务，但收费不免除许可证义务；GUI 与 GPL 内核分进程也不自动证明无需履行对应许可要求。以固定版本 LICENSE 和实际组合方式核对，必要时取得专业法律意见，不承诺“隐藏源码即可闭源销售”。

网站运营依赖不在 Windows UI 里重做，但属于整个服务收费前的外部发布门槛。已有本机测试或用户基础使用成功不代表线上支付、安全运维和真实商业节点已经通过。

在受管节点尚未就绪时，允许继续开发/验证 SS、VLESS 功能版，但必须标注为内部测试；不能把非权威静态节点伪装成已安全收费节点。套餐过期不仅应让 App 停止获取配置，还必须让真实节点拒绝旧凭据与存量流，才满足商业强制控制。

## 14. 轻量交接规则与进度记录

只维护本文件的 8 轮状态和每轮一段记录；每轮可以一个 PR，必要修复留在当前轮，不另建繁复流程或重复计划书。先看工作区状态，读相关共享契约，再做当前未完成项；源码/契约变动需同步实现和测试。

| 轮次 | 状态（本次） | 下一步与前置条件 |
| --- | --- | --- |
| 1 环境与工程 | DONE_WITH_BLOCKERS（见下方第 1 轮记录） | 取得授权的 x64 Windows 11 测试机后才能进入第 2 轮 TUN PoC |
| 2 平台与 TUN PoC | NOT_STARTED | 从已列出的 Unix、platform、DNS/路由耦合开始拆分 |
| 3 服务与安全 IPC | NOT_STARTED | 确定发布身份/安装位置，完成真实三进程授权链 |
| 4 UI 与账号节点 | NOT_STARTED | 复用 Snapshot 和登录契约，测试账号具备明确的测试权益 |
| 5 TUN 与开发计量 | NOT_STARTED | 真实可用 SS/VLESS 及受控上传/下载/UDP 目标 |
| 6 可靠性与网络矩阵 | NOT_STARTED | 可恢复的 Windows x64 测试机、双栈/多网卡/浏览器环境 |
| 7 节点权威计量 | NOT_STARTED | 自控或合格供应商节点，真实 grant/TUN 联调 |
| 8 签名安装发布 | NOT_STARTED | 签名身份、干净机、许可材料与所有硬门槛证据 |

每轮交接追加以下一段即可：

```text
轮次 / 日期 / 执行者：
基线 commit / 已有未提交改动：
实际修改与契约变化（不含秘密）：
执行命令 / 实际测试数 / PASS、FAIL、BLOCKED：
安装包版本 / OS、架构、Core、驱动版本 / 脱敏证据路径：
未解决问题、复现方法与发布限制：
下一位从哪个文件/验收项开始；回退与网络恢复方法：
```

本次规划交接：已完成需求、711 行 macOS 交接书、PR #1–#19 说明和当前关键源码勘察；识别 Windows 平台耦合、登录平台硬编码、UDP 规则回落风险、API 包装差异和已有权威计量的真实边界；确定 8 轮路线。未创建 Windows 工程、未改 Core、未部署、未运行 Windows 或远端数据库测试。下一位从第 1 轮开始，不要先重做网站，不要跳过 Windows x64 测试环境确认。

### 第 1 轮记录（2026-10-13，Claude）

```text
轮次 / 日期 / 执行者：第 1 轮 / 2026-10-13 / Claude (Sonnet 5.5)
基线 commit / 已有未提交改动：99b5e9b（PR #19 合并点）；工作区仅有未跟踪的本文件，已并入本轮 PR
实际修改与契约变化（不含秘密）：
  新增 apps/windows（Shared / App(WPF) / Service(Worker) / Tests）、toolchain.lock.json、global.json、NuGet.config、
  .env.example；新增 .github/workflows/windows.yml、scripts/test-native-windows.py、docs/windows-foundation.md、
  docs/windows-go-port-baseline.md。未改 Go Core、后端、网站、共享契约、macOS 工程；契约文件只被读取。
执行命令 / 实际测试数 / PASS、FAIL、BLOCKED：
  macOS arm64：dotnet restore/build（全解决方案，含 WPF 与 Service 的编译）；dotnet test = 69 PASS / 0 FAIL；
  python3 scripts/test-native-windows.py --suite foundation = 8 PASS；
  GOOS=windows go build ./cmd/asterlink-core = 4 个编译错误（预期，基线）；go build ./internal/... = 通过。
  Windows 实机：W01 PE/启动、窗口、Service probe 的证据由 windows.yml 在 windows-2025 (Windows Server 2025 x64) 上产出；
  Windows 10/11 客户端实机 = NOT_RUN / BLOCKED（无测试机；GitHub 托管 runner 没有 x64 的 Windows 客户端镜像）。
安装包版本 / OS、架构、Core、驱动版本 / 脱敏证据路径：无安装包；Wintun 0.14.1 仅记录官方哈希，未下载、未验签。
未解决问题、复现方法与发布限制：
  1) 决策变更：.NET 10 官方支持表无 Windows 10 22H2 → 目标改为 Windows 11 24H2/25H2，Win10 22H2 仅 ESU 尽力而为。
  2) 内部包 compile 通过但 network_other.go 的 !darwin 标签会让 Windows 悄悄链接占位实现——第 2 轮必须改标签并加测试。
  3) 需要用户提供/确认：x64 Windows 11 测试机（或 x64 宿主上的可回滚虚拟机）、受控 SS/VLESS 测试节点。
  4) Inno Setup 商用需购买授权，第 8 轮前决定安装器工具。
下一位从哪里开始：docs/windows-go-port-baseline.md 的“第 2 轮清单”；先下载并校验 Wintun，再拆 main.go。
回退与网络恢复方法：本轮不触碰网络、驱动或服务；回退 = 删除 worktree 分支。
```

## 附录 A. 源码核验索引

下列路径在本轮实际阅读或定位，行号以本文基线为准；开发后应以新代码为准更新本表。

| 主题 | 主要依据 |
| --- | --- |
| 历史阶段与真机修复 | [handoff_app.md](handoff_app.md)，尤其阶段追加与末尾 #16–#19 对应记录 |
| Web / 后端栈 | [frontend/package.json](frontend/package.json)、[backend/go.mod](backend/go.mod) |
| 固定 Core / Windows 缺失 | [native/core/go.mod](native/core/go.mod)、[Core 入口](native/core/cmd/asterlink-core/main.go)、[network_other.go](native/core/internal/network/network_other.go) |
| macOS 特定网络代码 | [network_darwin.go](native/core/internal/network/network_darwin.go)、[sysdns.go](native/core/internal/engine/sysdns.go)、[ipv6.go](native/core/internal/engine/ipv6.go)、[mihomo.go](native/core/internal/engine/mihomo.go) |
| 登录平台与租约 | [manager.go](native/core/internal/managed/manager.go):187、[lease.go](native/core/internal/managed/lease.go)、[native.go](backend/internal/api/native.go):57–118 |
| 单节点模板 / UDP 回落 | [config.go](native/core/internal/engine/config.go):31–61、[固定 tunnel.go](native/third_party/Clash.Meta/tunnel/tunnel.go):655–710 |
| HTTP 与 IPC 语义 | [apiclient/client.go](native/core/internal/apiclient/client.go)、[service.go](native/core/internal/service/service.go)、[ipc-v1.json](shared/contracts/ipc-v1.json)、[snapshot-v1.json](shared/contracts/snapshot-v1.json) |
| 节点目录 / 版本失效 | [node_catalog.go](backend/internal/api/node_catalog.go)、[node_watch.go](backend/internal/api/node_watch.go) |
| 权威计费及协议局限 | [native-v2.json](shared/contracts/native-v2.json)、[credential.go](backend/internal/nodes/credential.go)、[docs/macos-stage4.md](docs/macos-stage4.md) |
| 环境与生产保护 | [config.go](backend/internal/config/config.go):78–173、[macOS 公开示例](apps/macos/.env.example)、[env-v1.json](shared/contracts/env-v1.json) |
| 发布/测试现状 | [ci.yml](.github/workflows/ci.yml)、[release-macos.yml](.github/workflows/release-macos.yml)、[docs/macos-release.md](docs/macos-release.md) |

## 附录 B. 第 1 轮外部复核入口（本轮未访问，不作为当前版本证明）

- WPF 与 Windows 桌面 .NET：[Microsoft WPF 文档](https://learn.microsoft.com/dotnet/desktop/wpf/)、[.NET 支持策略](https://dotnet.microsoft.com/platform/support/policy)、[.NET 支持 OS 资料](https://github.com/dotnet/core/tree/main/release-notes)。核对拟固定 SDK、self-contained 安全更新与目标 OS，而不是只看框架名称。
- Windows 原生权限与进程：[Named Pipe 安全](https://learn.microsoft.com/windows/win32/ipc/named-pipe-security-and-access-rights)、[Job Objects](https://learn.microsoft.com/windows/win32/procthread/job-objects)、[Windows Services](https://learn.microsoft.com/windows/win32/services/services)、[Credential Management](https://learn.microsoft.com/windows/win32/secauthn/credentials-management)。
- 网络与驱动：[Wintun](https://www.wintun.net/)、[IP Helper](https://learn.microsoft.com/windows/win32/iphlp/ip-helper-start-page)、[Windows Filtering Platform](https://learn.microsoft.com/windows/win32/fwp/windows-filtering-platform-start-page)、[Windows on Arm](https://learn.microsoft.com/windows/arm/overview)。核对驱动架构/签名、DNS/strict-route 实际行为与 M4 辅助测试限制。
- 分发：[SignTool](https://learn.microsoft.com/windows/win32/seccrypto/signtool)、[Inno Setup](https://jrsoftware.org/isinfo.php)。核对所选版本的发行许可、架构限制、签名实践和安全公告。

这些入口供后续有联网条件的开发者核验。没有读到当前文档、没有固定二进制来源、没有真实 Windows 测试时，保持相应条目未完成，不用推测填补验收记录。
