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

> **第 1 轮已修订：** .NET 10 官方支持表（2026-09-28 更新）未列出 Windows 10 22H2（“未列出”指微软不测试，不代表已知不可用）。结论：**同一份 win-x64 程序同时面向 Windows 10 22H2（19045，需更新到最新）与 Windows 11 24H2/25H2**，不使用任何 Windows 11 专属接口，也不为 Windows 10 做牺牲 Windows 11 的写法；编译目标 `net10.0-windows10.0.19041.0` 让越界调用在编译期报错（CA1416）。**任一系统只有在该系统真机跑通对应测试后才对外承诺**，目前尚无任何客户端系统实测通过。见 [docs/windows-foundation.md](docs/windows-foundation.md)。下面保留原规划文字供对照。

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
| 1 环境与工程 | DONE（Windows Server 2025 CI 通过；Windows 10/11 客户端实机 NOT_RUN） | 第 2 轮先在用户的 Windows 10 家庭版 22H2 真机做 TUN PoC；Windows 11 后补 |
| 2 平台与 TUN PoC | DONE（PR #21：CI 全绿；Windows 10 家庭版 22H2 真机 SS 实传 0 FAIL；Windows 11 NOT_RUN） | 见第 2 轮记录的未解决项 |
| 3 服务与安全 IPC | DONE（PR #22；CI 全绿；Windows 10 家庭版 22H2 真机：e2e 32 例 0 FAIL + 人工 UAC 取消/批准通过；Windows 11 NOT_RUN） | 见第 3 轮记录的未解决项；第 4 轮可在此链路上做真实登录界面 |
| 4 UI 与账号节点 | DONE（PR #23；CI 10 项全绿；Windows 10 家庭版真机：安装服务、登录、记住密码、选线、连接、上网、DNS、WebRTC 正常（用户报告）；Windows 11 NOT_RUN） | 第 5 轮；A02 的设备上限/0 额度/到期、A04 的后台改动同步未逐项记录，第 5 轮继续核对 |
| 5 TUN 与开发计量 | DONE（PR #24；CI 全绿；Windows 10 家庭版真机：B01 1× 逐单位精确、B02 丢 ACK 同批重试只扣一次、A04 改倍率/套餐到期/停用节点/额度耗尽均自动停连；0.5× 倍率、VLESS、受控 UDP、Windows 11 NOT_RUN） | 第 6 轮；若有 0.5× 线路补测 B01；client_reported 仍为开发计量 |
| 6 可靠性与网络矩阵 | DONE（PR #25；CI 全绿；Windows 10 家庭版真机：harness 20 例 0 FAIL、T05-bind 通过（无需 WFP）、硬杀 Core/GUI/Service 恢复通过、一次睡眠恢复通过；**8 小时采样、休眠、10 次睡眠/切网、断电、浏览器页面、Windows 11 均 NOT_RUN**） | 第 7 轮；8 小时采样等可随时用 docs/windows-reliability.md 补测 |
| 7 节点权威计量 | CODE_DONE / 商业验收 BLOCKED（本 PR；验收工具与 UI 来源标识完成；macOS 回环用真实后端+PostgreSQL+mTLS+真实 NodeAgent 二进制+真实 sink 跑通 B04/B05/B06；**没有受管节点，demo.hyshentou.cn 未测；Windows 真机 B04-tun、跨机 B06、Mihomo VLESS-UDP、Windows 11 均 NOT_RUN**） | 在自控服务器部署 NodeAgent（带 TLS）并登记，然后按 docs/windows-grants-acceptance.md 在 Windows 真机跑 `--grants`、`--tun`；通过后进入第 8 轮 |
| 8 签名安装发布 | CODE_DONE / 发布 BLOCKED（本 PR；Inno Setup 安装器、升级回退、卸载、测试签名流水线、发布资料齐备；**无可信证书、无干净机 R01/R02、无受管节点；仅内部渠道**） | 取得代码签名证书后 `--sign thumbprint\|pfx`，并实现服务/GUI 侧 Authenticode 校验；在干净 Windows 10/11 机器按 docs/windows-release.md 第 8 节验收；Wintun 再分发法务复核 |

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
  Windows CI（run 38369734130，windows-2025 = Windows Server 2025 Datacenter 10.0.26100，AMD64）：锁定还原/构建/69 测试
  通过；发布产物 PE32+ x64；GUI --self-test 窗口 rendered=true（1000×680）、.NET 10.0.12、is_native_windows_x64=true；
  Service --probe 通过。证据为该 run 的 artifact windows-foundation-reports（保留 7 天）。
  Windows 10/11 客户端实机 = NOT_RUN / BLOCKED（无测试机；GitHub 托管 runner 没有 x64 的 Windows 客户端镜像）。
安装包版本 / OS、架构、Core、驱动版本 / 脱敏证据路径：无安装包；Wintun 0.14.1 仅记录官方哈希，未下载、未验签。
未解决问题、复现方法与发布限制：
  1) 决策变更：同一二进制面向 Windows 10 22H2 与 Windows 11 24H2/25H2；OS 下限 19041 由编译器强制；
     各系统须真机通过后才承诺。用户已提供一台 Windows 10 家庭版 22H2 真机（无远程桌面宿主、无 Hyper-V），
     第 2 轮先在其上测试；Windows 11 另需一台机器或 x64 宿主上的虚拟机。
  2) 内部包 compile 通过但 network_other.go 的 !darwin 标签会让 Windows 悄悄链接占位实现——第 2 轮必须改标签并加测试。
  3) 测试机：已有 Windows 10 家庭版 22H2 真机（测试前建还原点、不连公司 VPN、备第二种上网方式）；
     Windows 11 机器或 x64 虚拟机仍待提供。SS 测试节点已由用户准备，凭据经环境变量注入，不进仓库。
  4) Inno Setup 商用需购买授权，第 8 轮前决定安装器工具。
下一位从哪里开始：docs/windows-go-port-baseline.md 的“第 2 轮清单”；先下载并校验 Wintun，再拆 main.go。
回退与网络恢复方法：本轮不触碰网络、驱动或服务；回退 = 删除 worktree 分支。
```

### 第 2 轮记录（2026-10-13，Claude）

```text
轮次 / 日期 / 执行者：第 2 轮 / 2026-10-13 / Claude (Sonnet 5.5)
基线 commit / 已有未提交改动：b19d2e8（PR #20 合并点）；PR #21，工作区干净
实际修改与契约变化（不含秘密）：
  Go Core 按平台拆分：cmd/asterlink-core（Windows 继承匿名管道句柄 ASTERLINK_IPC_IN/OUT，缺句柄失败退出）、
  internal/network（IP_UNICAST_IF、GetAdaptersAddresses、竞争 VPN 分类，network_other.go 标签改为 !darwin && !windows）、
  internal/engine（platform/sysdns/ipv6 的 unix 与 windows 实现；Mihomo 创建 TUN 的失败从日志取出）。
  运行模板末尾增加 MATCH,REJECT（macOS 同样生效）；Windows 增加 strict-route 与 device=AsterLink。
  登录 platform 由构建目标决定。asterlink-tuntest 增加 Windows 实现。新增 docs/windows-tun-design.md、docs/windows-tun-poc.md、
  scripts/test-native-windows.py --suite core、windows.yml 的 Go 步骤。未改后端、网站、共享契约、macOS 工程文件。
执行命令 / 实际测试数 / PASS、FAIL、BLOCKED：
  CI（PR #21，全部 12 项通过）：Windows Server 2025 原生 Go 测试 134 PASS / 0 FAIL；macOS 本机 136 PASS；
  windows/amd64 PE 构建 + vet；内嵌 wintun.dll 哈希与签名（Status=Valid，签名者 WireGuard LLC）；
  Core 无句柄时退出码 1，harness 无 --allow-network-changes 时退出码 3。
  真机（Windows 10 家庭版 22H2，用户报告；SS 节点 aes-256-gcm，udp:true；公司 VPN 已断开；asterlink-tuntest.exe
  SHA-256 584291C1…E96B，即 CI 产物）：第一次 --cycles 2：15 例 0 FAIL 1 BLOCKED；
  第二次 --cycles 5 --udp-reject-test --kill-test：17 例 0 FAIL 1 BLOCKED。BLOCKED 仅为 B-COUNT（本轮未提供受控下载地址，计量属第 5 轮）。
  PASS：T01a T02 T01 T01-noproxy T03-dns-adapter T03-dns-system T03-dns-any53 T02-udp T04-ipv6 T04-ipv6-fallback T05 T05-loop
  T07 T02-udp-reject T09-kill T-CYCLES（两次 T-CYCLES：goroutine 11→13 / 12→16，句柄 456→457 / 450→452）。
安装包版本 / OS、架构、Core、驱动版本 / 脱敏证据路径：
  无安装包。Wintun 0.14.1（sing-tun v0.4.22 内嵌，amd64 DLL SHA-256 e5da8447…dafce）。证据为用户粘贴的控制台输出，
  未随仓库保存；tun-report.json 未回传。机器事实（winver 构建号）未回传，OS 版本以用户报告为准。
未解决问题、复现方法与发布限制：
  1) 未测：Windows 11；浏览器/DoH、DHCP 与手工 DNS、多网卡、WebRTC、睡眠/休眠、断电（均为第 6 轮）；计量（第 5 轮）。
  2) T04：节点可承载 IPv6（mode=tunnel），IPv6 字面量 HTTP 经节点成功，但按主机名的 tcp6 请求在隧道内失败（本机基线有 IPv6）。
     没有外泄，应用回退 IPv4；原因疑为 fake-ip 未配置 IPv6 地址池导致 AAAA 无有效应答，需第 6 轮确认，并据此决定是否声称“IPv6 完整代理”。
  3) T09-kill 通过，说明本轮“无持久状态、无恢复记录”的假设在该机成立；仍需在 Service 场景（第 3 轮）和 Windows 11 重复。
  4) 启动时日志有 “dial node mihomo --> 1.1.1.1:53 ... operation was canceled”：DNS 双上游竞速后败者被取消，无功能影响。
  5) 该机器 WinINET 残留 ProxyServer=127.0.0.1:7890（ProxyEnable=0，来自旧代理软件）；Core 前后未改动。
  6) 许可与杀软：内存加载 Wintun 的许可与误报风险留给第 8 轮；本次未报告 Defender/SmartScreen 拦截。
  7) 测试用节点密码出现在对话记录中，需由节点方更换；未写入仓库。
下一位从哪里开始：第 3 轮，docs/windows-tun-design.md 第 10 节与第 8 节（私有句柄协议由 Service 创建管道并启动 Core）；
  Core 的可执行路径即 WFP 放行路径，Service 只能从受保护目录启动它。
回退与网络恢复方法：docs/windows-tun-poc.md 末节；Core 被杀后适配器、路由、DNS、过滤器由系统清除（T09-kill 已在 Windows 10 家庭版证实）。
```

### 第 3 轮记录（2026-10-13，Claude）

```text
轮次 / 日期 / 执行者：第 3 轮 / 2026-10-13 / Claude (Sonnet 5.5)
基线 commit / 已有未提交改动：dae18bf（PR #21 合并点）；新 worktree worktree-windows-round3-service-ipc，工作区干净
实际修改与契约变化（不含秘密）：
  新增 apps/windows/AsterLink.Platform（Win32：管道 DACL/首实例、调用方身份、Job Object、挂起启动+显式句柄表、SCM、ACL/reparse、
  恢复记录、UAC 提权）、AsterLink.Tests.Windows；AsterLink.Shared/Service（路径、清单、ACL 策略、对端策略、命令白名单、所有者闸门、
  版本门、恢复策略、服务状态分类，均为跨平台纯逻辑）；Service 改为真实 PipeListener/ClientSession/CoreHost + install/uninstall/repair/status；
  GUI 增加“特权服务”卡片与 --service-check；scripts/package-native-windows.py（内部渠道 stage）、scripts/windows/e2e-service.ps1、
  pipe-probe.ps1；test-native-windows.py 增加 --suite service；windows.yml 增加服务套件与 test-machine kit 产物；
  docs/windows-service-design.md。未改 Go Core、后端、网站、共享契约、macOS 工程。
执行命令 / 实际测试数 / PASS、FAIL、BLOCKED：
  macOS：dotnet build 全解决方案（含 WPF、Platform、Tests.Windows）0 警告；dotnet test AsterLink.Tests = 218 PASS（原 69 + 新 149）；
  scripts/test-native-windows.py --suite foundation = 9 PASS。本地 worktree 未初始化 Clash.Meta 子模块，Go 套件由 CI 执行（Go 代码本轮未改）。
  Windows CI 结果见 PR 的 Windows client CI 运行（以该运行的 artifact windows-foundation-reports / windows-internal-stage 为准）。
  Windows 10 家庭版 22H2（build 19045，用户报告）：e2e-service.ps1 = 32 例 0 FAIL 2 NOT_RUN；人工 UAC 检查（普通用户启动 stage 的 GUI → 取消
  → 批准 → 打开安装副本 → “特权服务就绪”，服务/内核 0.1.0，internal，signed_out）通过。真机首次测试曾暴露两个 CI 测不到的问题并已修复：
  普通用户无法 OpenProcess SYSTEM 服务进程导致 server_rejected（现以 SCM 的 PID/路径/账户绑定为必要条件，能打开进程时再加查）；
  GUI 开着时卸载在锁住的 AsterLink.dll 处半途失败（现安装/卸载前先拒绝）。
  未能产生的用例：S01-remote（需第二台电脑）、S01-other-user（需第二个交互用户）、Windows 11 —— NOT_RUN。
安装包版本 / OS、架构、Core、驱动版本 / 脱敏证据路径：
  内部渠道 stage 0.1.0（未签名，不是可信发行）；Core 以 -X main.version=0.1.0 构建；Wintun 未变（内嵌 0.14.1）。
未解决问题、复现方法与发布限制：
  1) 内部渠道无 Authenticode：信任来自受保护目录 ACL；release 清单被拒绝，直到第 8 轮实现签名校验。
  2) Windows 11 真机未测；Windows 10 已通过（见上）。
  3) 注销/切换用户只靠“GUI 进程消失 → 管道关闭 → Core 停止”，未接 SERVICE_CONTROL_SESSIONCHANGE。
  4) 安装器是开发骨架（service 可执行文件的 verb），Inno Setup 授权与正式安装器见第 8 轮。
  5) 卸载不清除各用户的 Credential Manager 项（第 4 轮引入保存密码后，在第 8 轮提供当前用户侧的清除选项）。
下一位从哪里开始：第 4 轮，在 ServiceConnection 之上实现登录界面（密码只经 login 命令，api_base/channel 必须取自受保护构建配置，
  CommandPolicy 已强制）；Snapshot 与 action_error 的处理见 IpcJson.ParseSnapshotReply。
回退与网络恢复方法：本轮不触碰路由/DNS/网卡。回退 = 以管理员运行 stage 目录中的 `AsterLink.Service.exe uninstall`
  （或 e2e 脚本的 finally 已自动卸载）；PR 未合并时删除 worktree 分支即可。
```

### 第 4 轮记录（2026-10-13，Claude）

```text
轮次 / 日期 / 执行者：第 4 轮 / 2026-10-13 / Claude (Sonnet 5.5)
基线 commit / 已有未提交改动：90157db（PR #22 合并点）；新 worktree worktree-windows-round4-ui-login-sync，工作区干净
实际修改与契约变化（不含秘密）：
  AsterLink.Shared/Client（SessionController、CoreClient、ConnectionView/NodeList/ErrorText/DiagnosticsReport、Formatting、
  凭据/偏好/设备 ID 抽象与文件实现）；AsterLink.Platform（CredentialManagerStore、ServiceCoreTransport）；
  AsterLink.App 重写为登录页 + 连接/线路/账户与设置三页 + 托盘 + 单实例 + 浅/深色主题 + --preview 设计预览；
  --self-test 增加四页加载检查；windows.yml 增加预览步骤与 windows-ui-preview 产物；docs/windows-client-ui.md。
  未改 Go Core、后端、网站、共享契约、macOS 工程。
执行命令 / 实际测试数 / PASS、FAIL、BLOCKED：
  macOS：dotnet build 全解决方案 Debug/Release 0 警告；dotnet test AsterLink.Tests = 303 PASS（原 218 + 新 85）；
  scripts/test-native-windows.py --suite foundation = 9 PASS。WPF 界面在 macOS 上只能编译，不能运行。
  Windows CI 与预览截图：见 PR 的 Windows client CI 运行。
  Windows 10 家庭版 22H2 真机（用户报告，无逐项记录/截图）：首次安装服务、登录、记住密码、选择节点、连接成功、正常上网、DNS 与 WebRTC 正常。
  未逐项验证：设备上限、0 额度、到期、后台改名/倍率/停用/删除的同步、Windows 11 —— NOT_RUN。
安装包版本 / OS、架构、Core、驱动版本 / 脱敏证据路径：内部渠道 stage 0.1.0（未签名）；Core、Wintun 未变。
未解决问题、复现方法与发布限制：
  1) “连接”已接到真实 Core 与第 2 轮 TUN 引擎，真机点击会真的创建 TUN（路由/DNS 被修改）；计量闭环属第 5 轮。
  2) 取消连接 = 结束会话（需重新登录），因为命令已在 Core 内。
  3) A01 与主路径已由真机确认；A02/A04 的边界场景、键盘与 125%/150% DPI 仍待测。
  4) CredentialManagerStore 使用 CRED_PERSIST_LOCAL_MACHINE（本机、不漫游、按 Windows 用户隔离）；卸载时清除各用户保存密码仍是第 8 轮。
下一位从哪里开始：真机跑 A01–A04（docs/windows-client-ui.md 第 5 节）；通过后进入第 5 轮（TUN 接入共享状态机与唯一 Meter）。
回退与网络恢复方法：本轮 GUI 不修改网络；真机若点了“连接”，退出 AsterLink 即停止 Core，路由/DNS 由系统随 Core 退出清除（第 2 轮 T09-kill 已证实）。
```

### 第 5 轮记录（2026-10-13，Claude）

```text
轮次 / 日期 / 执行者：第 5 轮 / 2026-10-13 / Claude (Sonnet 5.5)
基线 commit / 已有未提交改动：8c9d304（PR #23 合并点）；新 worktree worktree-windows-round5-tun-metering，工作区干净
核查结论（重要）：TUN→共享状态机→唯一 Meter 在 Core 里早已是同一条路径（managed.Manager + engine.Mihomo + meter.Meter，macOS 与 Windows 共用；
  第 4 轮 GUI 经 Service/IPC 调用的就是它）。GUI 已展示速率、曲线、确认/估算余额与 pending。因此本轮没有再造第二套计量，
  缺口是“真实引擎对真实账本”的证据：tuntest 的 B-COUNT 一直 BLOCKED，倍率/ACK 丢失/管理员改动只有假引擎+假 API 的测试。
实际修改与契约变化（不含秘密）：
  新增 native/core/cmd/asterlink-trafficsink（受控目标：精确长度下载/上传，令牌常量时间比较，计数）；
  新增 native/core/cmd/asterlink-billtest（用生产 Manager+Mihomo 对真实 API：登录→选线→连接→子进程受控传输→断开结账→
  按公式逐单位核对；--ack-loss 在真实服务器上丢弃一次 /traffic 回包并核对同批重试且只扣一次；--watch-stop-seconds 观察管理员改节点/
  套餐/额度后自动停连；Windows 下传输子进程使用 exe 副本以免被 TUN 防火墙豁免）；apiclient 增加 WrapTransport（仅验收工具使用）；
  managed 增加“管理员删除/停用节点、套餐不再覆盖节点”时连接中停流并按旧倍率结清尾账的测试；
  scripts/test-native-windows.py 增加 --suite billing；windows.yml 增加 billing 步骤并保留 billtest/sink 产物；
  docs/windows-billing-acceptance.md。未改后端、网站、共享契约、macOS 工程、C# 代码。
执行命令 / 实际测试数 / PASS、FAIL、BLOCKED：
  macOS：native/core go test ./... 全部通过；billing 单元部分（meter/managed/apiclient/billtest/trafficsink，-race）108 PASS 0 FAIL；
  windows/amd64 PE 构建（core/tuntest/billtest/trafficsink）与 go vet 通过；billing.live = BLOCKED（需要 VPN-free 的提权 Windows 主机）。
  本机公司 VPN 在线，未尝试真实 TUN；未连接远端数据库、未消耗任何账号额度。Windows CI 结果见 PR。
  Windows 10 真机（用户运行 asterlink-billtest，build 19045，管理员，公司 VPN 已断开，节点 SS，账号“轻量计划”）：
    bill-1：15 例 0 FAIL。B01 倍率 1000‰：上传 20 MiB + 下载 80 MiB，sink 确认收发精确一致；计数 上 21,368,464 + 下 84,151,298 =
      105,519,762 字节 → 扣 105,519,762,000 单位，与公式逐单位一致，余额公式一致；计数比正文多 上 +1.89%、下 +0.32%（协议帧+背景流量）。
      B02：真实服务器丢弃一次 /traffic 回包，重试批次 {sequence 11, 上 25,716,607, 下 92,734,490} 原样重发，只扣一次。
    watch-2：改倍率 → 7 秒内停连（profile_changed），重连绑定新倍率 PASS。watch-3：套餐到期 → 25 秒内停连（subscription_expired），重连被拒 PASS。
    watch-1：停用节点 → 约 9 秒内停连，重连被拒；FAIL 仅因期望码列表过窄，实际原因码 client_config_unavailable（服务器无可用配置，
      已有中文提示）属稳定码，已加入期望并修正文档，隧道行为本身正确。
    watch-4（额度耗尽）：FAIL，3 分钟后仍连接。该线路约 0.4 MiB/s，余额 72 MB 3 分钟内几乎用不完；当时 harness 不记录已驱动字节数，
      不能证明产品缺陷也不能证明正常。已给 harness 加上进度与诊断，待重跑。
    watch-4 重测（余额约 78 MB，--watch-drive）：驱动约 15 MB 后进度正常递减，1 分 10 秒时自动停连，reason=quota_exhausted，
      尾账已结清（pending=false），重连被拒。注意：停连时本地预估余额仍约 58 MB，说明触发者是服务器侧裁决（可能期间改了后台额度或同账号
      有其它用量），而非本地预估耗尽；两条路径的原因码相同，本地预估路径由 managed 单元测试覆盖，真机未单独区分。
    未测：0.5× 倍率（本账号 3 条线路倍率相同）。Windows 11：NOT_RUN。
未解决问题、复现方法与发布限制：
  1) 真机报告尚无；B01 误差口径：计数器=经节点的负载含 HTTP 帧，与文件正文不相等，报告会写出实测差异。
  2) 受控 UDP 目标与 VLESS(TLS/Reality) 组合未验证；SS 仍是唯一有真机证据的协议。
  3) 工具以 sink 令牌 + 测试账号密码环境变量运行；测试账号将消耗套餐额度；sink 须放在测试机之外、节点可达的服务器，用完即停。
  4) client_reported 节点仍是开发计量，不解除生产保护；硬杀前未确认尾账不能追回（第 7 轮 node_reported 解决）。
下一位从哪里开始：第 6 轮（网络可靠性）。docs/windows-billing-acceptance.md 可随时重跑回归。
回退与网络恢复方法：工具退出（含 Ctrl+C）时关闭隧道并结账；被硬杀时适配器/路由/DNS/过滤器随进程消失（第 2 轮 T09-kill）。
```

### 第 6 轮记录（2026-10-13，Claude）

```text
轮次 / 日期 / 执行者：第 6 轮 / 2026-10-13 / Claude (Sonnet 5.5)
基线 commit / 已有未提交改动：0dc89f1（PR #24 合并点）；新 worktree worktree-windows-round6-reliability，工作区干净
实际修改与契约变化（不含秘密）：
  Core：clockWatch 增加“看门狗停摆 >10 s 即视为挂起”判据（Windows 的单调时钟被认为包含睡眠，原“墙钟 vs 单调钟”判据在 Windows 上
  可能永不触发，休眠恢复后隧道不会被重新确认）；network.Signature 对 IPv6 只取 /64 前缀（隐私临时地址轮换不再造成长连接中途 network_changed）。
  asterlink-tuntest：新增 T03-lan-dns（对每块网卡配置的 DNS 发 53 端口查询，必须被拦截/劫持）、T05-bind（绑定物理网卡源地址的套接字
  不得直连；结果决定是否需要 WFP 策略）、T07-signature、--soak-seconds/--soak-interval（R03 长时采样，输出 soak.csv）。
  scripts/windows/reliability-check.ps1（Kill Core/Gui/Service 与睡眠/休眠/断电后的 Verify）、soak-sampler.ps1（已安装产品的 8 小时采样）；
  windows.yml 增加 PowerShell 语法检查与空闲 Verify 步骤。docs/windows-reliability.md（矩阵、决策、限制）。
  未改后端、网站、共享契约、macOS 工程、C# 代码。未新增 WFP 策略（无 T05-bind 实测证据，决策规则见文档第 5 节）。
结论性发现：round 2 的“主机名 tcp6 在隧道内失败”不是缺陷——固定内核在 fake-ip 且无 fake-ip-range6 时对所有 AAAA 返回空，应用走 A 记录经 IPv4；
  IPv6 字面量仍按节点能力代理/快速拒绝。产品口径：暂不支持“仅 IPv6 的站点按域名访问”。
执行命令 / 实际测试数 / PASS、FAIL、BLOCKED：
  macOS：native/core go test ./...（含新增挂起检测、签名、soak 判定、DNS 分类用例）通过；GOOS=windows go vet 通过。
  Windows 10 家庭版 22H2（build 19045，用户 2026-10-14 运行，公司 VPN 已断开，节点 SS udp:true）：harness --cycles 20 --udp-reject-test --kill-test
  = 20 例 0 FAIL 1 BLOCKED（B-COUNT 未提供下载地址）；新增 T03-lan-dns（1 个解析器被拦截）、T05-bind（绑定物理网卡地址的套接字被阻断，
  本机无需 WFP）、T07-signature 均 PASS；T-CYCLES 20 次 goroutine 12->31、句柄 460->460。reliability-check：Kill Core/Gui/Service 各 PASS
  （无孤儿 Core、适配器与路由消失、DNS 与直连恢复、Service 自动回到 Running、不自动重连）；一次睡眠恢复后 Verify PASS。
  未执行：Windows 11、8 小时采样、休眠、10 次睡眠/切网、有线/Wi-Fi 切换、断电、浏览器 DoH/WebRTC 页面（均 NOT_RUN）。
  睡眠只验证了结束状态，没有记录窗口是否出现“系统休眠”提示，因此不能证明是新的停摆检测而不是网络签名检查关闭了隧道。
  观察：硬杀 Core 后下一次启动出现一次 “create adapter: Cannot create a file when that file already exists [retrying 1/3]”，重试成功、无残留。
安装包版本 / OS、架构、Core、驱动版本 / 脱敏证据路径：内部渠道 0.1.0；Wintun 未变。无真机证据。
未解决问题、复现方法与发布限制：docs/windows-reliability.md 第 4 节。20 次启停、10 次睡眠/网络切换、8 小时采样均未执行。
下一位从哪里开始：真机跑 docs/windows-reliability.md 第 2 节的命令并回填；T05-bind 若 FAIL，按第 5 节做最小 WFP；通过后进入第 7 轮。
回退与网络恢复方法：新增工具不改网络（harness 需 --allow-network-changes，退出即清理）；回退 = 删除 worktree 分支。
```

### 第 7 轮记录（2026-10-14，Claude）

```text
轮次 / 日期 / 执行者：第 7 轮 / 2026-10-14 / Claude (Sonnet 5.5)
基线 commit / 已有未提交改动：5b6757b（PR #25 合并点）；新 worktree worktree-windows-round7-grant-enforcement，工作区干净
核查结论（重要）：connectGranted、能力协商、ready grant、撤销、节点确认余额在共享 Core 里早已完整（macOS 阶段 4），Windows 经 Service/IPC 原样消费；
  后端 /traffic 对 node_reported 会话返回 409 metering_source_mismatch，NodeAgent 参考 VLESS 转发器（TCP+UDP，可选 TLS）已有撤销/到期/预算强制。
  因此本轮没有重写任何 grant 逻辑或 NodeAgent，缺口是“用户不配合时节点是否真的强制”的验收证据与 UI 来源标识。
实际修改与契约变化（不含秘密）：
  asterlink-billtest 新增 --grants：独立 VLESS 客户端（只用 grant 凭据，无 Core/TUN/上报），用例 G-CAPS、G-NODES、B04-reject（/traffic、/config 被拒且账本不动）、
    B04-node-bills（无任何上报，节点仍按 sink 实测负载×倍率计费，并核对剩余额公式）、B05-revoke（TCP+UDP 在限定秒数内被切断，凭据随后被拒）、
    B05-idle-expiry（无任何 API 流量，节点自行到期）、--hold-seconds（运营者在后台结束套餐/停用节点/压低额度/停 API/杀 Agent，节点必须切断）、
    B06-race（N 设备同刻抢最后额度，不得超卖；--race-at 对齐 Windows+macOS）、--tun（B04 经生产 Manager+真实 TUN，Core 零 /traffic）。
  asterlink-trafficsink 新增 --udp-listen（凭 token 回显）；release 后等节点确认再申请下一个 grant（预算在节点结清后才归还）。
  scripts/test-native-windows.py 新增 --suite grants；windows.yml 新增 grants 步骤（live 部分在托管 runner 上按设计 BLOCKED）并上传 billtest 三平台构建；
  scripts/test-native.py 额外构建 billtest/sink，backend/internal/api/billtest_e2e_test.go 用真实 Agent 回环验证工具本身。
  UI：连接页“当前线路”卡片与线路页标明计量来源（节点实测/客户端上报·内部测试线路）；NodeRow.MeteringLabelOf + 单测。
  docs/windows-grants-acceptance.md：用例、运行方法、生产模式能力矩阵。未改后端、网站、共享契约、macOS 工程、Service。
执行命令 / 实际测试数 / PASS、FAIL、BLOCKED：
  macOS：scripts/test-native-windows.py --suite grants：Go 114 例（-race）0 FAIL，NodeAgent 模块 18 例 0 FAIL，billtest 三平台构建通过；live = BLOCKED（无受管节点）。
  macOS 回环（真实后端+PostgreSQL+mTLS+NodeAgent+sink+billtest --grants，scripts/test-native.py -run TestBilltest）：
    G-CAPS、G-NODES、B04-reject、B04-node-bills（12,582,914 B → +6,292,218,500 单位 @500‰，仅比正文多 0.01%）、B05-revoke（TCP+UDP 约 1 s 内切断）、
    B05-idle-expiry（最后一次 API 后 14 s 切断=空闲超时）、B05-expire（套餐结束后约 4 s 切断，凭据被拒）、B06-race（3 设备/6 MiB：1 获授权、2 被拒 quota_exhausted，
    已用恰为 6,291,456,000/6,291,456,000 单位）全部 PASS。假节点对照：不计费/不切撤销流/接受 /traffic/超卖 均被判 FAIL。
  C# 未在本机编译（本机无 dotnet）；以 CI 为准。Windows 真机：未执行任何 round-7 用例（NOT_RUN）。
  本机公司 VPN 在线：未尝试真实 TUN；独立客户端用例不改路由/DNS，真机时仍建议无 VPN 网络。
未解决问题、复现方法与发布限制：
  1) 商业验收 BLOCKED：当前测试账号下没有由 NodeAgent 管理的节点（G-NODES 会如实报 BLOCKED）；需自控服务器部署 Agent（TLS）并登记。
  2) 能力矩阵：只有“VLESS(TCP/UDP)+TLS 参考转发器”是权威；SS/Reality/XTLS/Mux/Trojan/WireGuard 均无节点侧强制，不得进入收费套餐承诺（见 docs/windows-grants-acceptance.md）。
  3) Mihomo 发起的 VLESS-UDP 经参考转发器的端到端未验证；B04-tun 用 TCP 受控目标，尚无受控 UDP 目标。
  4) Windows 10/11 真机 B04-tun、Windows+macOS 跨机 B06、真实公网 TLS 节点、控制面/Agent 故障的真节点演练均 NOT_RUN。
  5) B06 在真实栈中表现为“首个 grant 预留全部剩余预算，其余被拒”，即不超卖但也不会让多设备分享尾量；是否符合产品预期请业务确认。
下一位从哪里开始：部署带 TLS 的 NodeAgent → 按 docs/windows-grants-acceptance.md 跑 --grants（可在 Windows 或 macOS），再在无 VPN 的 Windows 真机跑 --tun；通过后进入第 8 轮。
回退与网络恢复方法：独立客户端用例不改网络；--tun 同第 5 轮（退出即关闭隧道并结账，硬杀后适配器/路由/DNS 随进程消失）；回退 = 删除 worktree 分支。
```

### 第 8 轮记录（2026-10-14，Claude）

```text
轮次 / 日期 / 执行者：第 8 轮 / 2026-10-14 / Claude (Sonnet 5.5)
基线 commit / 已有未提交改动：ed773bf（PR #26 合并点）；新 worktree worktree-windows-round8-release，工作区干净
实际修改与契约变化（不含秘密）：
  安装器：apps/windows/Packaging/setup.iss（Inno Setup 6.7.1，x64os、Windows 10 2004+、一次 UAC）。Inno 只做载体和界面：把 stage 解到自己的临时目录，调用
    服务的 install --stage 验证式动词；不直接往 Program Files 拷文件。GUI 运行时安装/卸载被拒绝（静默模式直接失败）；安装后可选以“原始非提权用户”启动。
  升级/回退（ServiceVerbs.Install）：降级默认拒绝（exit 7，--allow-downgrade 才放行，UpgradePolicy 数值比较）；旧目录改名为 AsterLink.previous，
    新版验证并运行后才删除；任一步失败即恢复并重新验证旧版、重启其服务（RollBack），不恢复登录；中断留下的 .previous 在下次安装时恢复或清理。
  卸载：卸载器先询问（默认“否”；静默用 /REMOVEUSERDATA=1）是否删除“当前用户”的保存密码与偏好，由已安装 GUI 以原始非提权用户执行
    --cleanup-user-data（CredEnumerate 仅限 AsterLink/ 前缀，不碰其他应用；偏好目录含 device id）；随后从安装目录外的副本运行 uninstall 动词。
    提权卸载器不遍历其他 Windows 用户的凭据库。
  签名：scripts/windows/sign-files.ps1（none/test/thumbprint/pfx；密钥只从环境变量读，真实模式强制时间戳且要求 Valid）；顺序=签自有二进制→写 manifest→
    Inno SignTool 签 Setup 与卸载器。test 模式是自签名证书，只验证流水线，任何机器都不信任。manifest 渠道仍为 internal，--channel release 仍被拒绝：
    服务端尚不校验 Authenticode，测试签名不会被误当发布。
  发布资料：package-native-windows.py --installer --release-dir → Setup、SHA256SUMS、release-info.json（trusted_signature=false、open_gates）、manifest、
    依赖清单（CycloneDX 形，无第三方 NuGet）、第三方许可、SOURCE.txt 与两份 GPL 源码归档。docs/windows-release.md 为升级/卸载/回退/限制说明。
  测试：AsterLink.Tests +升级分类/清理选择单测；Tests.Windows +CredentialManager 清理；scripts/windows/release-check.ps1 与 test-native-windows.py --suite release；
    windows.yml 安装 Inno 并真实静默安装/重装/被拒绝/回退/降级拒绝/卸载（保留与删除用户数据），上传测试签名安装包。toolchain.lock.json 记录 Inno 6.7.1 决定。
  未改后端、网站、共享契约、macOS 工程、Core。
执行命令 / 实际测试数 / PASS、FAIL、BLOCKED：
  macOS：dotnet build 全解决方案 0 警告 0 错误；AsterLink.Tests 318 例 0 FAIL；test-native-windows.py --suite release（可移植部分）与 --suite foundation 通过，
    安装器构建/真实安装 = BLOCKED（非 Windows）。公司 VPN 在线对本轮无影响：安装/升级/卸载不改路由、DNS、网卡。
  Windows Server 2025 CI：见 PR（结果以 CI 为准）。Windows 10/11 真机：R01/R02 均 NOT_RUN。
安装包版本 / OS、架构、Core、驱动版本 / 脱敏证据路径：内部渠道 0.1.0；Wintun 0.14.1 内嵌；artifact windows-installer-test-signed（自签名，仅测试）。
未解决问题、复现方法与发布限制：docs/windows-release.md 第 6、7 节。要点：无可信证书与时间戳；服务/GUI 尚不校验 Authenticode；R01（干净机、真实 UAC、默认安全设置）、
  R02（其他用户凭据、真实旧版本升级、断电中断）NOT_RUN；Wintun 预编译二进制再分发条款和 Inno 商业许可需法务/采购确认；round 7 受管节点仍缺失；Windows 11 NOT_RUN。
下一位从哪里开始：拿到证书后 sign-files.ps1 的 thumbprint/pfx 模式；实现 PeerPolicy/InstallVerifier 的 Authenticode 校验并放开 release 渠道；在干净 Windows 10/11 跑 release-check.ps1 -Install 与手工 R01。
回退与网络恢复方法：本轮不改网络；升级失败自动回到旧版；回退本 PR = 删除 worktree 分支，或卸载后安装旧版 Setup。
CI 修复（2026-10-15，PR #27 Windows job 的 2 个 FAIL）：
  1) E99 unins000.exe 不存在：install 动词原在 ssPostInstall 运行，此时 Inno 已写好 {app}\setup\unins000.exe，动词把整个安装根改名为 .previous 后删掉，
     卸载器随之消失（添加/删除程序也会坏）；且 Inno 6.7.1 源码显示 ssPostInstall 里的 Abort 被吞掉，安装失败仍 exit 0。改为 ssInstall 先
     ExtractTemporaryFiles 再跑动词（失败 exit 3，Inno 什么都没记录），动词也会把旧根里的 setup\ 复制到新根（GUI 开发安装按钮同样走该动词）。
     release-check 新增 I01-uninstaller-registered，I03/I05 也检查卸载器。
  2) R04 许可缺失：收集脚本写死 darwin/arm64（漏了 Windows 独有的 go-ole）；现在按 windows/amd64 收集，识别 LICENSE.gpl 这类变体，
     metacubex/chacha v0.1.5 用 scripts/third_party_licenses/ 中注明来源的上游 MIT 文本。go-lzo 为 GPL-2.0，需法务确认与 GPL-3.0 组合。
  3) 修复 1/2 后 CI 跑到卸载阶段，又暴露两处（均以 Inno 6.7.1 源码核实）：
     U01 卸载器 exit 0 但服务/目录/ProgramData 仍在：Inno 第一阶段 {app}\setup\unins000.exe 在 usUninstall 期间一直存活，uninstall 动词的
     “安装目录内有进程在运行”检查把它当成占用而拒绝，静默模式下错误框被抑制，Inno 照常删掉自己的记录。动词现在忽略 <root>\setup 下的进程；
     动词失败时 setup.iss 调 Abort，在 Inno 删除卸载入口前停止，可重试。
     U02 exit 1：卸载器里不允许 ExecAsOriginalUser（Inno 直接报致命错误）。改用 Exec：以批准卸载的账户运行清理，常见的管理员审批模式下就是同一用户；
     标准用户输入另一管理员密码时清理的是该管理员的 AsterLink 项（已写入 docs/windows-release.md 第 3 节）。
```

## 后续维护：NodeAgent XUDP（Android 第 2 轮发现，PR #30 之后）

`fix/nodeagent-vless-xudp` 在独立 worktree 修复 NodeAgent 的 VLESS command-3 UDP mux 支持，保留 command 2；不改变 Windows App/Service、Mihomo pin 或上一轮 goroutine 修复。真实 pinned Mihomo UDP 回归现位于 `native/core/integration`，Windows CI 显式运行（loopback、无 TUN）；覆盖往返、per-grant 计数、额度、到期、撤销和私网拒绝。

第 7 轮旧 `--udp-sink` 使用自写 command-2 客户端，不能作为 Mihomo XUDP 兼容证据。现已改用真实 pinned Mihomo，不静默回退；报告标记 `UDP(XUDP/pinned Mihomo)`，旧 command-2-only 节点应 FAIL。真实 API＋临时本机 PostgreSQL＋NodeAgent 的 grants 验收重新通过，但不重写旧 Windows 10 真机记录，也不声称完成新 Windows 10/11 TUN UDP、公网 TLS 节点或跨机器竞争验收。

部署需更新 NodeAgent 和验收工具；现有客户端已经默认发 XUDP，无需为本修复改节点 YAML、packet encoding 或数据库。完整设计、限制与复现见 [docs/nodeagent-xudp.md](docs/nodeagent-xudp.md)，操作步骤见 [docs/windows-grants-acceptance.md](docs/windows-grants-acceptance.md)。

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
