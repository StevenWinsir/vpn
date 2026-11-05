# AsterLink VPN：客户端长时间观看视频后网速骤降（不断线）修复交接书（四个平台）

> 文件：handoff_fix.md
> 编写日期：2026-11-04。同日补充了 Windows、Android、iOS 的核查（第 2.5～2.8 节），修复范围从 macOS 扩展到四个平台，仍是 3 轮。
> 目标：每个平台的数据面都应当像普通 VPN 一样稳定。看视频、开网页、做别的重负载工作时，速度只受节点和线路影响，不能因为客户端被系统降级、内部丢包或内存回收而变慢。
> 代码基线：项目 /Users/stevenlee/Desktop/vpn，main，HEAD cfff197（#110 合并后）。工作区里有用户未提交的改动：HANDOFF.md 删除、iOS 工程文件、frontend.zip、handoff_bandwidth.md、支付接入技术文档.md。这些都不要动。
> 文档性质：问题核实结论、修复规划与验收约定。本文不代表任何修复已经开发或测试，全文不含代码示例。

## 1. 结论摘要

用户的现象是：macOS 客户端连上节点后，一开始网速正常。看一会儿 YouTube，网速突然掉下来，卡顿但不断线；过一会儿慢慢恢复，但之后一直偏慢。换了几个节点都一样。

逐层排查后的结论：**问题存在，根因在客户端一侧，与节点和网站后端无关。** macOS 上有两个缺陷（F1、F2），叠加后正好表现为这个现象。另外还发现一个计费小缺陷（F3），修复时必须一起处理。

用户随后要求把 Windows、Android、iOS 也查一遍。结果：
- F2 和 F3 在共享 Core 里，四个平台都有。
- Windows 有一个与 F1 同类的问题（F4），只在用电池时出现。
- iOS 有一个内存回收策略的风险项（F5）。
- Android 在代码层面没有发现独有问题，需要真机核查。

| 编号 | 缺陷 | 所在层 | 核实程度 |
| --- | --- | --- | --- |
| F1（主因） | macOS 的特权 Helper 以 launchd 默认类型运行，系统对它施加“轻度资源限制”。它拉起的 Go Core（整个转发数据面）因此全部线程只拿到实用级（utility）调度优先级 20，而普通应用和系统网络守护进程是 31～47。CPU 一有竞争，转发就被饿住：延迟暴涨，吞吐断崖下跌 | macOS Helper / 打包脚本 | **已在用户本机实测确认**（第 2.2 节） |
| F2（放大因素，YouTube 专属） | 内核规则把浏览器的 QUIC（UDP 443，YouTube 默认使用）原样交给节点的 UDP 通道。Mihomo 的 UDP 路径在队列满时**静默丢包**，不会像 TCP 那样反压。跨境 UDP 还常被运营商 QoS 限速。QUIC 被丢包和限速后会大幅降速，恢复也慢 | 共享 Go Core 配置模板（四个平台共用） | 机制已在代码中逐行确认。用户节点是否开了 UDP 无法从这里看到，需第 2 轮真机 A/B 验证。QUIC 路径在 docs/macos-stage4.md:127 中明确记为“未验证” |
| F3（计费缺陷，修 F2 时必须处理） | 被 REJECT 的 UDP 包会被计入“代理流量”并上报计费。现有的“节点不支持 UDP → MATCH,REJECT”路径今天就在给从未发出去的 UDP 包计费 | 固定版本的 Mihomo 统计模块 | 已在代码中确认（第 2.4 节） |
| F4（Windows，与 F1 同类） | Windows 11 22H2 起，系统把没有窗口的后台服务归为 Utility 服务质量：**用电池时**只用最省电的频率，并调度到能效核。我们的 Core 由 LocalSystem 服务拉起，自己没有窗口，也没有显式声明服务质量，正好落在这一类。笔记本拔掉电源后，转发会被降频，在混合架构 CPU（Intel 12 代以后、骁龙）上还会被挤到能效核 | Windows 服务拉起 Core 的代码 | **已按微软官方文档确认机制**（第 2.5 节），尚未在 Windows 真机上实测 |
| F5（iOS，风险项） | 数据包隧道扩展给 Go 设了很紧的内存预算：堆软上限 22 MiB，GC 目标 60%。整个进程占用一旦超过 38 MiB，**每 5 秒强制回收并归还内存一次**。大流量时占用可能一直停在阈值以上，CPU 就反复花在回收上，转发周期性变慢。占用超过系统上限（约 50 MiB）时扩展会被系统杀掉，那就是断线了 | iOS 扩展与 Core 的内存策略 | 代码已确认；**没有任何真机数据**（docs/ios-lifecycle.md:33 写明阈值都是初值），影响大小未知 |

各平台的结论汇总见第 2.8 节。修复仍采用 **3 轮**：

| 轮次 | 可验收结果 | 依赖 |
| --- | --- | --- |
| 第 1 轮 | 桌面端系统降级（F1、F4）。macOS：Helper 以 Interactive 类型运行，Core 以用户发起级服务质量拉起，覆盖升级路径。Windows：Core 在恢复运行前就显式声明高服务质量，退出系统节能节流。两个平台都在打包检查和安装态检查里加守护 | 无 |
| 第 2 轮 | 共享 Core 的 F2、F3：固定规则拒绝 UDP 443，被拒绝的流量不计费。再加 iOS 的 F5：先测，再把强制回收改为限频，扩展的采样日志补上线程优先级和 GC 开销 | 无（可与第 1 轮并行，但合并顺序是第 1 轮先） |
| 第 3 轮 | 长时吞吐回归工具（与“谁拉起内核”无关的外部测量）；四个平台的实测核查，包括 Android 线程调度和 iOS 真机；文档；用户实测清单 U-52 … U-60 | 前 2 轮 |

每轮一个 worktree 分支和一个 PR，分支名为 worktree-fix-roundN-*，每轮的记录追加到本文第 12 节。

## 2. 问题核实

### 2.1 排查范围与排除项

| 层 | 检查内容 | 结论 |
| --- | --- | --- |
| 网站后端 | 计量上报（CLIENT_REPORT_INTERVAL_SECONDS=60，CLIENT_LEASE_SECONDS=90）、租约续期、配额判定；backend/internal 里有没有任何限速逻辑 | **排除**。后端不在数据面上。上报失败只会让客户端退避重试，租约到期会断开连接（lease_expired），而不是降速。后端和 Core 都没有令牌桶或限速代码（handoff_bandwidth.md 尚未实施，全仓库只有 nodepolicy 中 hysteria 的 up/down 字段） |
| Core 计量 | native/core/internal/meter/meter.go | **排除**。计量每秒只读一次 Mihomo 的累计计数器（statistic.DefaultManager.TotalTraffic），不在包转发路径上 |
| Core 看门狗 | native/core/internal/managed/tunnel.go monitor：每 1 秒采样，每 3 秒算一次网络签名（会执行一次 /sbin/route），每 60 秒上报一次 | **排除**。开销可以忽略：本机 Core 运行数小时累计 CPU 约 1 分 22 秒。所有异常都走“断开”或“重新确认”，不会降速 |
| macOS 界面 | SessionController 已连接时每 1 秒读一次快照 | **排除**。只是 IPC 读取，不触碰数据面 |
| Core 日志管道 | Mihomo 日志重定向到 stderr，Helper 把 stderr 接到空设备 | **排除**。不存在管道写满导致阻塞的问题 |
| Mihomo 固定补丁 | native/third_party/patches 只修了 sing-tun 监听器的 context 生命周期 | **排除**。不改变数据面行为 |
| TUN 模板 | native/core/internal/engine/config.go：system 栈、MTU 采用 Mihomo 默认的 9000、fake-ip、规则为“MATCH,node”加“MATCH,REJECT” | TCP 部分没有问题。**UDP 443 的处理就是 F2** |
| sing-tun system 栈 | stack_system.go（v0.4.22）：darwin 下只有一个读包循环（batchLoopDarwin），所有 TCP 包往返都要经过它两次（改写地址后回注 utun） | 这本身不是缺陷，但它解释了 F1 为什么伤害这么大：整个隧道的时延都系于这一个低优先级的循环 |
| 节点 | 用户确认节点不限速。不同节点都复现，说明与具体节点无关 | 与结论一致：根因在客户端 |

另外看到两个与本现象无关的上游细节：sing-tun 的 TCP NAT 表在空闲 5 分钟后回收条目；端口号在约 5.5 万次新连接后回绕。都不会造成“持续变慢”，本次不处理，只在第 3 轮文档里留档。

### 2.2 F1 的证据（在用户这台 Mac 上实测，2026-11-04）

机器是 Mac16,10（Apple M4，4 个性能核加 6 个能效核）。当时 AsterLink.app、Helper 和 Core（/Library/Application Support/AsterLink/Workers/0.1.0/asterlink-core）都在运行。

1. **Core 的全部 19 个线程优先级都是 20**（ps -M 显示 20T）；Helper 的 3 个线程也是 20T。作为对照：AsterLink.app 主线程 46T；系统的 mDNSResponder 是 31T/37T；configd 主要是 31T/37T；本机终端拉起的普通进程是 31T。
2. launchctl print system/com.asterlink.vpn.helper 显示 **spawn type = daemon (3)**。系统网络守护进程 com.apple.mDNSResponder 和 com.apple.configd 都是 **interactive (4)**。
3. 打包出来的 Helper plist（/Applications/AsterLink.app/Contents/Library/LaunchDaemons/com.asterlink.vpn.helper.plist，由 scripts/build-native-macos.py:171 生成）只有 Label、BundleProgram、MachServices、AssociatedBundleIdentifiers 四个键，**没有 ProcessType**。本机 man launchd.plist 的原文是：不设置 ProcessType 时，“系统会对任务施加轻度资源限制，限制其 CPU 使用和 I/O 带宽”；Interactive 类型“与应用享有相同的资源限制，即没有限制”。
4. Helper 用 Foundation 的 Process 拉起 Core（apps/macos/Sources/AsterLinkHelper/WorkerSupervisor.swift:68），没有指定服务质量。子进程继承了 Helper 被限制的调度等级。用 taskpolicy -c utility 拉起的进程同样是 20T，说明 20 对应的就是实用级上限。
5. **影响量化（本机复现）**：在 /tmp 写了一个只用标准库的转发器作为数据面替身（逐块 AES-GCM 加密，模拟 AEAD 代理），分别在普通优先级和实用级上限下测试。用 0、4、6、10、16 个忙循环进程制造 CPU 竞争：

| 竞争进程数 | 普通（31）吞吐 | 实用级（20）吞吐 | 普通往返延迟 p50 / p99 / 最大 | 实用级往返延迟 p50 / p99 / 最大 |
| --- | --- | --- | --- | --- |
| 0 | 32.7 Gbit/s | 21.2 Gbit/s | 0.14 / 0.23 / 0.29 ms | 0.15 / 0.25 / 0.64 ms |
| 4 | 34.1 Gbit/s | 18.5 Gbit/s | 0.12 / 0.22 / 0.34 ms | 0.12 / 0.21 / 1.08 ms |
| 6 | 31.6 Gbit/s | 17.2 Gbit/s | 0.12 / 0.30 / 3.6 ms | 0.12 / 0.26 / 3.1 ms |
| 10 | 26.5 Gbit/s | **0.75 Gbit/s** | 0.12 / 7.9 / 13 ms | **10.5 / 34.5 / 170 ms** |
| 16 | 19.4 Gbit/s | **0.75 Gbit/s** | 0.11 / 0.18 / 0.23 ms | **10.4 / 82 / 155 ms** |

怎么解读这张表（交接时请如实沿用，不要夸大）：
- 没有竞争或竞争较轻时，实用级只损失约 35%～45% 的峰值吞吐（被调到能效核），往返延迟几乎不变。
- CPU 一旦被占满（竞争进程数约等于或超过核数），实用级的转发**吞吐跌到约 1/35，每一跳的延迟中位数从 0.1 ms 涨到 10 ms，尖峰 150 ms 以上**。普通优先级在同样的竞争下几乎不受影响。
- 跨境代理的往返时间本来就有 100～200 ms，每个包再多 10～150 ms 的不稳定排队，TCP 和 QUIC 的拥塞控制就会持续降速，这正是“突然卡、慢慢恢复、但一直偏慢”。浏览器播放高码率视频、Spotlight 建索引、备份、编译等，都会在短时间里把 CPU 占满。Mihomo 的 UDP 队列在这段时间满了就丢包（F2），所以 YouTube 受害最重。
- 替身转发器跑在本机回环上，不包括真实跨境链路。它证明的是“这个调度等级在竞争下会让转发掉速”。“用户那次卡顿正好发生在竞争时刻”需要第 1 轮真机 A/B 来归因（第 8 节）。

为什么之前所有测试都没发现：开发直连模式（apps/macos/Sources/AsterLinkApp/DirectCoreTransport.swift，只在 DEBUG 下存在）由用户进程直接拉起 Core，优先级 31。特权验收工具（asterlink-tuntest、billtest）是在终端里用 sudo 跑的，同样是 31。只有正式安装、经 Helper 拉起的 Core 才会被限制，而这条路径从来没做过长时吞吐测试（仓库里没有任何吞吐随时间变化的测试，soak 只看资源有没有增长）。

### 2.3 F2 的证据（代码核实）

1. 规则模板（native/core/internal/engine/config.go:78）只有“MATCH,node”和“MATCH,REJECT”。节点声明支持 UDP 时（管理员 YAML 中 udp 为真，订阅源里很常见），浏览器的 QUIC（UDP 443）全部经节点的 UDP 通道转发。SS 是原生跨境 UDP；VLESS/VMess/Trojan 是把 UDP 装进 TCP（xudp/packetaddr）。
2. Mihomo 的 UDP 入口（native/third_party/Clash.Meta/tunnel/tunnel.go:99～112）按哈希分到若干个容量为 64 的队列，队列满就直接丢包。每个 NAT 会话的发送通道（tunnel/connection.go:129～145，容量 128）满了也直接丢包。TCP 在同样的情况下会反压发送方，不会丢包。
3. QUIC 自己有拥塞控制。丢包和运营商对跨境 UDP 的 QoS（典型表现就是“开始很快，持续大流量后突然被限，之后缓慢恢复但上不去”）都会让它大幅降速。如果 UDP 被装进 TCP，QUIC 的拥塞控制又叠在节点那条 TCP 上，时延抖动会被放大。
4. docs/macos-stage4.md:127 明确记载：VLESS UDP 与 Mihomo 对接、QUIC/WebRTC“未验证”。

QUIC 被拒绝后，Chrome、Safari 和 YouTube App 都会自动改走 TCP（HTTP/2 over TLS）经节点转发。这是业内代理客户端普遍采用的“禁用 QUIC”做法。

### 2.4 F3 的证据（代码核实）

1. Core 的计费口径是 TotalTraffic(true)（native/core/internal/engine/mihomo.go:231）。它在 Mihomo 统计模块里的定义是“出口链最后一跳不是 DIRECT 的字节”（tunnel/statistic/manager.go:70～86）。
2. REJECT 的 UDP 连接（adapter/outbound/reject.go:33～38）是一个“写入即报告成功、实际丢弃”的空连接。它照样包在统计追踪器里，写入的字节会以链名“REJECT”计入代理上传，也就是会计费。
3. 结果：今天节点不支持 UDP 时，被“MATCH,REJECT”挡下的 QUIC、STUN 等 UDP 包已经在计费，只是量小（浏览器发几次 QUIC 初始包后就会回退）。第 2 轮新增“拒绝 UDP 443”后，被拒绝的量会变大，所以**必须在同一轮把 REJECT 和 REJECT-DROP 排除出计费口径**，不能先上规则、后修计费。
4. 现有测试没有覆盖“被拒绝的 UDP 不计费”（Core 的 harness 和 billtest 里的 reject 场景只针对节点上报模式的上报拒绝）。

### 2.5 Windows（F4 的证据）

1. 拉起方式：AsterLink.Service 以 LocalSystem 身份运行（ServiceVerbs.cs:398）。它在 AsterLink.Platform/WorkerProcess.cs:123 用 CreateProcess 以挂起状态创建 Core，标志只有挂起、扩展启动信息、Unicode 环境、无窗口，**没有指定优先级类，也没有设置任何服务质量**。之后把 Core 放进作业对象（第 129 行），作业只带“随句柄关闭而终止”的标志（第 194 行），没有 CPU 速率或优先级限制，然后才恢复运行（第 143 行）。
2. 所以 Core 的优先级类是普通，和 macOS 的 20T 不同，不存在“一直被压低”的问题。
3. 但服务质量是另一回事。微软官方文档《Quality of Service》（learn.microsoft.com/en-us/windows/win32/procthread/quality-of-service，2025-07 版）的等级表写明：
   - Utility 等级对应“后台服务”，从 Windows 11 22H2 开始生效，**用电池时选择最省电的 CPU 频率，并调度到能效核**。
   - 窗口应用按可见性分等级（前台为 High），而 Core 自己没有窗口，也不是有窗口进程的后代（它的父进程是服务）。
   - 用 SetProcessInformation 的 ProcessPowerThrottling 显式关闭“执行速度节流”的进程，归入 High。
4. 结论：在 Windows 11 22H2 及以后的笔记本上，**用电池时** Core 会被降频；在混合架构 CPU 上还会被放到能效核上转发。插电时这个表格不生效。这与 macOS 的 F1 属于同一类问题：系统把 VPN 数据面当成“可以慢一点的后台任务”。
5. 没有核实的部分：这台 Mac 上没法跑 Windows，所以降频幅度和对吞吐的实际影响要在第 1 轮的 Windows 笔记本上实测（R1-11、R1-12）。台式机或一直插电的机器不受影响，这也可能是 Windows 用户反馈较少的原因。
6. 其余各层的排查结论与 macOS 相同。Windows 的网络签名用 GetAdaptersAddresses 读取，不起子进程。日志、计量、看门狗都不在包转发路径上。

### 2.6 Android

1. 数据面运行在 App 进程里。AsterVpnService 以前台服务身份运行（AndroidManifest.xml:58 中类型为 specialUse，AsterVpnService.kt:204 调用 startForeground），并由系统以 BIND_VPN_SERVICE 绑定，所以即使用户切到 YouTube App，这个进程仍处于前台服务级别，不会被放进后台 CPU 组。
2. Go 库在 CoreBridge.start（CoreBridge.kt:32）里第一次加载，这个调用经 Dispatchers.IO 线程发起（AppContainer.kt:71），是普通线程优先级。Linux 线程的 nice 值随创建线程继承，所以 Go 的线程是普通优先级。代码里没有任何地方降低线程优先级。
3. 没有给 Go 设置内存上限，Android 也没有 iOS 那种约 50 MiB 的扩展上限。
4. VpnService 的配置是 MTU 9000、底层网络跟随系统默认、非计费网络（AsterVpnService.kt:221～226），与普通 VPN 一致。
5. 结论：**代码层面没有发现 Android 独有的降速问题**，只受 F2、F3 影响（共享 Core）。剩下两个只能在真机上确认的风险：
   - 一些国产系统（如 MIUI、ColorOS、鸿蒙）的后台管理即使对前台服务也会限频或冻结。App 已经提供了“电池优化”设置入口（MainActivity.kt:108），但没有对用户说明为什么要设置。
   - 线程的实际调度组和 nice 值还没在真机上看过。可以在设备外部用 adb 查看，不需要改代码（第 3 轮 R3-06）。

### 2.7 iOS（F5 的证据）

1. 数据面运行在数据包隧道扩展进程里。Core 通过 gomobile 在扩展内运行，直接读 utun 的描述符。如果节点不支持 IPv6，中间会多一层用户态 IPv6 中继（native/core/internal/engine/ipv6relay_unix.go）。
2. 内存策略（apps/ios/Packages/AsterLinkKit/Sources/AsterLinkKit/MemoryBudget.swift，PacketTunnelProvider.swift:221～246）：
   - 启动时把 Go 堆软上限设为 22 MiB，GC 目标设为 60%。
   - 每 5 秒读一次整个进程的内存占用：达到 38 MiB 就强制完整回收并归还内存（MobileFreeMemory，即 Go 的 FreeOSMemory）；达到 44 MiB 也一样，另外记一条压力日志。
   - 强制回收没有限频。大流量下占用如果一直停在 38 MiB 以上，就会每 5 秒做一次完整回收，并把刚归还的内存重新申请回来。堆软上限偏紧时，GC 本身也会更频繁，把 CPU 从转发上抢走。
3. 有没有真的发生，取决于大流量时的实际占用。仓库里**没有任何真机数据**：docs/ios-lifecycle.md:33 写明阈值和堆上限都是初值，真机长时验收（D-MEM）因为没有付费开发者账号一直是 BLOCKED。所以 F5 只能算风险项，必须先测再改。
4. 扩展进程的线程调度等级由系统决定，代码里没有降低它的地方，但也没有任何测量。iOS 无法在设备外用 ps 查看，只能由扩展自己采样后写进日志（第 2 轮改动）。
5. IPv6 中继只在节点不支持 IPv6 时启用。它让每个包多一次用户态拷贝，也关闭了批量收发，是固定的 CPU 开销，不会“随时间变慢”。这一轮只记录不改，等第 3 轮的真机数据说明它确实是瓶颈再说。

### 2.8 四个平台结论汇总

| 平台 | 被系统降级 | QUIC 丢包（F2）/ 被拒流量计费（F3） | 平台独有风险 | 本次处理 |
| --- | --- | --- | --- | --- |
| macOS | **是（F1，已实测）**：Helper 和 Core 全部 20T | 是 | 无 | 第 1 轮修复，第 2 轮修复 |
| Windows | **是（F4，按官方文档确认）**：仅限用电池时，Utility 服务质量 | 是 | 无 | 第 1 轮修复，第 2 轮修复 |
| Android | 代码中未发现：前台服务，普通优先级 | 是 | 国产系统的后台管理；真机调度未测 | 第 2 轮修复；第 3 轮真机核查和用户指引 |
| iOS | 代码中未发现，但没有测量 | 是 | **F5 内存回收策略（风险项）**；IPv6 中继的固定开销 | 第 2 轮修复 F2、F3，并先测后改 F5；第 3 轮真机核查 |

## 3. 关键决策（默认值，用户可以在第 1 轮开工前推翻）

| 编号 | 决策 | 理由 |
| --- | --- | --- |
| D1 | Helper 的 launchd 类型定为 Interactive，不用 Adaptive 或 Standard | Adaptive 跟着 XPC 事务在后台和交互之间切换，但 Core 的转发与 XPC 活动无关，空闲时反而会被降到 Background，更糟。VPN 数据面正是文档说的“响应能力依赖于它”的场景。空闲时 Core 只有每秒一次的看门狗，能耗可以忽略 |
| D2（第 1 轮实测后修订） | ~~Helper 拉起 Core 时明确指定用户发起级服务质量~~。实测这样做无效：在 utility 限制下，声明 userInitiated 或 userInteractive 的子进程仍是 20T；没有限制时声明与否都是 31T。改为：Helper 每次握手报告自己和 Core 是否仍被限制（scheduling_limited）。版本相同但仍被限制时，App 给出与版本不一致相同的“重新安装”提示；本版本注册过一次后仍被限制，就不再拦截连接 | 子进程的服务质量突破不了继承来的限制，唯一有效的是 plist。自检能抓到“升级后 launchd 仍沿用旧注册”的情况（例如重启后用新二进制、旧 plist 运行），保护机制避免把用户锁在“重新安装”的死循环里 |
| D3 | 不改 TUN 栈类型，不改 MTU | system 栈在 macOS 上 CPU 开销最低。换成 gvisor 或 mixed 会把 TCP 搬到用户态，CPU 更紧，正好与 F1 相反。拒绝 QUIC 后 UDP 流量很少，MTU 9000 对它没有实际影响。改这两项会增加四个平台的回归面，收益却没有证据 |
| D4 | 拒绝 UDP 443 是写死在 Core 里的固定规则，对所有节点、所有平台生效；不做管理员开关，也不进管理员 YAML 或数据库 | Core 的规则本来就是固定、经过审查的模板，管理员 YAML 只贡献一个节点定义。不新增开关，就没有后端、网站和合约改动，profile_version 也不变。QUIC 类节点（hysteria2、tuic）上，浏览器的 TCP 流量同样能良好转发 |
| D5 | 只拒绝 UDP 目标端口 443；UDP 53（已被 dns-hijack 先接管）、其他 UDP（游戏、WebRTC、STUN）保持原样 | 范围最小，风险最低。WebRTC 和 STUN 的现有策略（支持就转发，不支持就拒绝，绝不直连）不变 |
| D6 | 计费修正放在 Mihomo 的统计模块里：链名为 REJECT 或 REJECT-DROP 的字节与 DIRECT 一样不计入代理口径。做法是扩展现有经过哈希锁定审查的补丁，不在 Core 里做减法 | Core 只能看到活跃连接的快照，已关闭连接的分链字节拿不到，在 Core 里做减法不可能精确。补丁只改两处判断，行为容易审查 |
| D7 | 不在用户界面展示调度等级或任何内核信息 | 遵守 handoff_app_standardization.md 的“应用里不出现引擎信息”约定。守护只放在测试和脚本里 |
| D8 | Windows 只做一件事：在 Core 挂起期间、恢复运行之前，显式关闭它的“执行速度节流”，让它归入 High 服务质量。不提高优先级类（不用高或实时），不改作业对象，不动服务进程本身 | 官方文档给出的就是这个开关，它只改变服务质量，不会让 Core 抢占用户前台程序。放在恢复运行之前，可以保证 Core 的第一条指令就在正确的等级上执行。服务进程不在数据面上，不需要改 |
| D9 | iOS 的 F5 先测后改。第 2 轮必改的只有两处：强制回收限频（只在占用比上次回收时明显增长，且距上次回收至少 30 秒时才做）；采样日志补上线程优先级和 GC 开销。22 MiB 堆上限和 38/44 MiB 阈值没有真机数据就不动 | 放宽内存会直接增加被系统杀掉（断线）的风险，比变慢更糟。限频只会减少无效回收，不会让占用上限变高 |
| D10 | Android 不改数据面代码。国产系统的后台管理问题，靠说明来解决：App 设置里已有“后台运行与省电”入口（strings.xml:93～94、160～161，目前只提到“断开”），第 3 轮把提示改成同时提到“变慢”，并在网站使用教程（/guide 的 Android 部分）补一段国产系统的放行步骤 | 前台服务和 VPN 绑定已经是 Android 上能拿到的最高常驻级别。厂商的后台管理不是 App 能用代码绕过的，只能引导用户放行 |

## 4. 桌面端调度修复设计（第 1 轮：macOS 的 F1 与 Windows 的 F4）

### 4.1 macOS 改动点

1. scripts/build-native-macos.py 生成 Helper 的 launchd plist 时，增加 ProcessType 并设为 Interactive。其他键不变。
2. （按修订后的 D2）Helper 用 SchedulingProbe 读自己和 Core 的任务基础优先级，低于 31 就在握手回复里报告 scheduling_limited，由 HelperUpgradePolicy 决定提示和是否拦截。
3. 开发直连模式（DirectCoreTransport）不用改。它本来就是 31，而且只在 DEBUG 下存在。但要在 docs/macos-release.md 里写明：直连模式和 sudo 跑的验收工具都看不到 F1，正式性能验收必须经 Helper 拉起。

### 4.2 macOS 升级路径（重点）

- launchd 在注册守护进程时读取 plist。已安装的用户只升级 App、不重新注册的话，**可能仍按旧类型运行**。现有机制是：App 和 Helper 的版本字符串不一致，HelperUpgradePolicy 就提示“后台服务需要更新，请在‘账户’页点击‘重新安装’”。这一轮的发布必须提升版本号，让这条路径一定被触发。
- 必须实测确认：重新安装（先注销再注册）之后，launchctl print 显示 interactive。只升级 App 不重新安装时实际是什么类型，也要如实记录。如果重新安装后仍然不是 interactive，要查清楚 SMAppService 的缓存行为，并在本轮修好重新安装流程（例如确保真的先注销再注册）。
- 重新安装只允许在未连接状态下进行（HelperUpgradePolicy.mayChangeService），这条约束不变。

### 4.3 macOS 需要留意的副作用

- 能耗：空闲时 Core 只有每秒一次的看门狗和每 3 秒一次的 route 查询。Interactive 不会阻止系统睡眠（这一轮不加电源断言）。要对比改动前后空闲 10 分钟的“能耗影响”和唤醒次数，记录在案。
- 睡眠和唤醒：clockWatch 的停顿判定（10 秒）和睡眠后的重新确认（T08）行为不能变。
- launchctl print 里看到的 jetsam thread limit = 32：高负载下要记录 Core 的线程数，确认远低于这个值。如果接近，要记录为风险。Go 的网络 I/O 走事件循环，正常情况下线程数稳定在二十个左右，本机实测是 19。
- Helper 本身变成 Interactive，它的唯一工作是在 XPC 上转发命令，空闲时没有负载。

### 4.4 macOS 守护

- scripts/test-native-macos.py 的 S-PLIST 检查（第 146 行附近）改为同时断言 ProcessType 为 Interactive。
- 如果这个测试套件能经真实 Helper 拉起 worker，就增加“运行中的 Core 所有线程优先级都不低于 31”的断言。不能的话，作为第 1 轮的手工验收项，并在记录里写明原因。
- release_scrub 和品牌素材检查不受影响，但要跑一遍确认 plist 新键没有触发误报。

### 4.5 Windows（F4）

1. 改动点：AsterLink.Platform/WorkerProcess.cs 在 Core 挂起期间（加入作业对象之后、ResumeThread 之前），对 Core 进程调用 SetProcessInformation 的 ProcessPowerThrottling，把“执行速度节流”的控制位置上、状态位清零，也就是显式退出节流、归入 High。所需的结构体和常量加在 AsterLink.Platform/Native.cs，与现有的作业对象声明放在一起。
2. 失败处理：这个调用失败（例如 Windows 10 早期版本不认识这个信息类）时，记录一条不含敏感信息的诊断日志，Core 照常启动，不能因此连接失败。Windows 10 本来就没有 Utility 等级，影响有限。
3. 不改的东西：优先级类保持普通；作业对象的限制不变；服务进程不改；Core 的 Go 代码不改。--recover-network 一类的短命辅助进程如果也走这条路径，可以一起设置，不强求。
4. 守护：AsterLink.Tests.Windows 增加一项，拉起 worker 后用 GetProcessInformation 读回 Core 的节流状态，断言“控制位已置、状态位为零”。只能在 Windows 主机上运行的测试，沿用现有的 Windows 专用测试项目和 CI 作业。
5. 升级：Windows 的 Core 每次连接都由服务重新拉起，服务二进制随安装包更新，不存在 macOS 那种“需要重新注册”的问题。但要实测一次覆盖安装后的第一次连接就生效。
6. 能耗：退出节流会让用电池时的功耗略高，但只在 Core 真正有转发负载时才明显。要对比空闲 10 分钟的电量消耗，如实记录。

## 5. F2、F3 与 F5 修复设计（第 2 轮：共享 Core 的四个平台，加上 iOS 扩展）

### 5.1 规则

- config.go 的规则列表变为三条，按顺序：网络为 UDP 且目标端口为 443 → REJECT；MATCH,node；MATCH,REJECT。第三条的原有用途（节点不支持 UDP 时的兜底）和注释保留，并补充说明第一条的用途。
- 第一条用 Mihomo 自带的 AND、NETWORK、DST-PORT 组合（固定版本的 rules/parser.go 都支持）。不要依赖嗅探或域名。
- REJECT 的 UDP 处理是空连接，不做 DNS 解析（reject.go:40～45），所以被拒绝的 QUIC 不会产生经节点的 DNS 查询。
- IPv4 和 IPv6 都适用（规则与地址族无关）。iOS 的 IPv6 中继过滤和 macOS 的 IPv6 拒绝路由不受影响。
- 四个平台的生成文档都会变，所以 config_test.go、config_platform_test.go、ios_test.go、tunspec_test.go 中锁定的配置都要同步更新，并新增断言：UDP 443 规则必须排在 MATCH,node 前面。
- 管理员 YAML 的严格解析（nodepolicy、ParseProfile）不变，profile_version 不变，合约版本不变，后端和网站零改动。

### 5.2 计费口径（F3）

- 扩展 native/third_party/patches 下的审查补丁：统计模块里“代理口径”的判断，从“不是 DIRECT”改为“不是 DIRECT，也不是 REJECT 或 REJECT-DROP”。上传和下载两个方向都要改。总口径（TotalTraffic(false)）不变。
- 补丁机制有完整的哈希锁：manifest.json 里每个文件的原始和打补丁后哈希，再加补丁本身的哈希。要更新补丁编号（例如从 asterlink-sing-tun-context-v1 升到 v2）、manifest、scripts/test-mihomo-patches.py 的期望值，并像 sing-tun 那边的 AsterLinkContextPatchVersion 一样，加一个编译期守护常量，并在 Core 里引用它。这样一个没打新补丁的源码树根本编译不过，不会悄悄带着旧计费口径发版。
- 补丁里要附带测试，证明：经 REJECT 写入的 UDP 字节不进代理口径；经节点的字节照常计入；DIRECT 行为不变。
- SOURCE.txt 和许可证说明里引用补丁的地方，按新编号同步更新（build-native-macos.py 会写入补丁编号和哈希）。
- 计费是用户最敏感的部分。PR 描述里要单独写一段计费影响分析：只会少计从未发出的字节，不会少计任何实际经节点转发的字节。

### 5.3 回退行为（必须实测）

- Chrome 和 Safari 打开 YouTube 时，QUIC 被拒绝后要在可接受的时间内回退到 TCP。验收标准：首个视频开始播放的时间与“节点不支持 UDP”的现有情况相当，播放中不出现错误提示。
- Android 的 YouTube App、iOS 的 Safari 和 YouTube App 同样要回退。
- 拒绝策略默认用 REJECT。如果实测发现某个平台回退明显偏慢，可以评估 REJECT-DROP 并记录对比。两者在计费上已经由 5.2 统一排除。

### 5.4 iOS 内存回收策略（F5，先测后改）

1. 先测：在 iOS 模拟器（Core 在模拟器进程里，现有 harness 已能跑）经隧道持续下载 30 分钟，速率接近高码率视频，同时混入 UDP 443 和普通网页流量。每 5 秒记录一次占用、Go 堆、GC 次数，以及强制回收的次数。模拟器的内存计算方式与真机不同，所以结果只能判断趋势；能用真机就以真机为准。
2. 必改两处（与测量结果无关，见 D9）：
   - 强制回收限频：只有占用比上次回收后又明显增长，并且距上次回收至少 30 秒，才再做一次。达到 44 MiB 的压力日志照旧，但同样限频，避免刷屏。
   - 扩展每 60 秒的采样日志（ResourceSample）增加两项：本进程所有线程的最低和最高调度优先级（用 Mach 的线程信息接口在扩展内读取），以及 Go 在 GC 上花掉的 CPU 占比（从 Go 运行时的指标里读取）。这样第 3 轮真机验收时能直接判断 F1 同类问题和 F5 在 iOS 上是否存在，不需要任何开发者工具。
3. 视测量结果决定的改动：如果持续流量下占用长期停在 38 MiB 以上，或者 GC 的 CPU 占比长期超过 25%，就在记录里写明数据，再提出阈值调整方案，交给用户决定是否在本轮实施。调整的前提是峰值离 50 MiB 的系统上限至少还有 6 MiB 余量。
4. MemoryBudget 的单元测试要覆盖限频：连续多次超过阈值时只触发一次回收；超过 30 秒且占用继续增长时再触发。
5. 不改 IPv6 中继（第 2.7 节第 5 条）。

## 6. 第 3 轮：长时吞吐回归工具与跨平台核查

### 6.1 工具要求

- 测量端必须**与“谁拉起内核”无关**：工具只作为隧道里的普通应用去测，不自己启动 Core。只有这样，才能测到经 Helper 或系统服务拉起的正式路径（F1 恰恰只在这条路径上出现）。可以扩展 asterlink-tuntest 增加纯测量子命令，或者另起一个小工具，由实施方决定。
- 测量对象：通过隧道向既有的测试节点和流量接收端（asterlink-testnode、asterlink-trafficsink 的部署方式沿用以往验收文档）持续下载，每 10 秒记录一次吞吐和往返时间，输出 CSV。
- 内置“CPU 竞争阶段”：在运行中段，用与核数相同数量的忙循环施压一段固定时间，然后撤除。判定指标：竞争期间吞吐不低于基线的 70%；撤除后 10 秒内恢复到基线的 90%；全程没有“持续若干分钟低于基线 50%”的平台期。阈值可以在第 3 轮根据实测校准，但校准过程要记录。
- 工具同时采集 Core 进程的线程优先级分布（macOS）或进程优先级与节能状态（Windows），写进报告。
- 工具不进用户发布包，归入开发和验收工具，并遵守 release_scrub 规则。

### 6.2 跨平台核查

- Windows：F4 已在第 1 轮修复。本轮用吞吐回归工具在笔记本上分别测插电和用电池，确认用电池时不再掉速。
- Android：在真机上（没有真机就用模拟器，并注明模拟器的调度组与真机不同）连接后切到 YouTube App 播放，用 adb 查看 App 进程所有线程的 nice 值和调度组，确认 Go 线程都是普通优先级、进程处于前台服务组。息屏 10 分钟后再看一次。只要出现后台组或 nice 值大于 0 的 Go 线程，就记录为新缺陷，并在本轮提出修复方案。同时按 D10 更新省电提示文字和网站教程。
- iOS：用第 2 轮新增的采样日志做真机长时观看（没有真机签名就按惯例标 BLOCKED，写明原因，并先用模拟器数据顶上）。重点看线程优先级是否明显低于普通级，以及 GC 开销和回收次数。
- 第 2.1 节提到的 sing-tun 两个上游细节（空闲 5 分钟回收、端口回绕），以及第 2.7 节的 IPv6 中继开销，写进 docs/macos-tun-poc.md 或新的排障文档留档，不改代码。

### 6.3 文档

- docs/macos-release.md：Helper 类型、升级必须重新安装、性能验收必须经 Helper 拉起。
- docs/windows-service-design.md 或 docs/windows-reliability.md：Core 显式退出节能节流的原因（引用微软服务质量文档中 Utility 等级的说明）、失败时的降级行为、性能验收必须在用电池时也做一次。
- docs/ios-lifecycle.md：强制回收限频规则、采样日志新增的两项、第 2 轮和第 3 轮测到的数据。阈值如果仍是初值，要继续写明。
- docs/android-lifecycle.md：前台服务和线程调度的实测结果；国产系统后台管理的说明。
- docs/managed-protocols.md 或 TUN 设计文档：固定拒绝 UDP 443 的原因与影响；被拒绝流量不计费。
- docs/ci-troubleshooting.md 或新的“网速问题排障”小节：用户再报网速问题时，按什么顺序收集证据（第 9 节）。

## 7. 兼容、发布与回滚

- 发布顺序：第 1 轮可以单独发 macOS 和 Windows 新版。第 2 轮改的是共享 Core（还有 iOS 扩展），四个平台要各自发版，任意组合都能工作：新 Core 配旧后端没问题，旧客户端也不受影响，因为后端没有任何改动。
- 新旧客户端混用：只有“旧客户端仍在为被拒绝的 UDP 计少量费用”这一差异，属于修复前的现状。
- 回滚：
  - 第 1 轮：macOS 撤掉 plist 键和服务质量设置（用户需要再次重新安装 Helper 才能恢复旧类型，不过没有理由回滚）；Windows 撤掉退出节流的调用。
  - 第 2 轮：恢复旧规则和旧补丁版本；iOS 恢复不限频的回收。
  - 都没有数据迁移。
- macOS 的版本号必须提升，以触发 Helper 版本不一致的重新安装提示（第 4.2 节）。

## 8. 三轮实施与逐轮验收

每轮的证据都分 PASS、FAIL、BLOCKED 三种，并附命令输出或截图。没有实测的项目一律不能写 PASS。在公司 VPN 开着的机器上做的测试，要注明对结果的影响（Core 会拒绝叠加在其他 VPN 上）。

### 第 1 轮：桌面端调度等级（macOS 的 F1、Windows 的 F4）

| 编号 | 验收项 | 通过标准 |
| --- | --- | --- |
| R1-01 | 打包产物的 Helper plist | plutil 显示 ProcessType = Interactive，其余键不变；S-PLIST 断言通过 |
| R1-02 | 全新安装后的 launchd 类型 | launchctl print system/com.asterlink.vpn.helper 显示 spawn type = interactive (4) |
| R1-03 | 从旧版升级 | 只升级 App：如实记录当时的类型和提示；点击“重新安装”后：interactive (4)。连接状态下不允许重新安装 |
| R1-04 | Core 线程优先级 | 连接后 ps -M 显示 Core 所有线程不低于 31，不再出现 20T；Helper 同样 |
| R1-05 | 竞争下的转发（替身） | 用第 2.2 节的方法复测，或者由实施方把它整理成仓库内的开发工具（不进发布包）。正式 Core 在 10 个以上竞争进程下，往返时延 p50 不超过 1 ms，吞吐不低于无竞争时的 50% |
| R1-06 | 真机 A/B（用户的真实节点） | 同一节点、同一时段：旧版和新版各播放 YouTube 4K 视频 30 分钟（Chrome），每 2 分钟经隧道下载一个固定大文件并记录速度；中间加一次 2 分钟的 CPU 满载。新版在满载期间速度不低于自身基线的 70%，满载结束后 10 秒内恢复。旧版的表现如实记录：出现掉速，归因就成立；没出现，也照实写，并把 F2 的验证交给第 2 轮 |
| R1-07 | 能耗与睡眠 | 空闲 10 分钟的能耗影响与旧版相当；合盖睡眠再唤醒后的重新确认（T08）行为不变 |
| R1-08 | 计费不变 | 一段固定下载前后，客户端的累计数、后端 client_traffic_reports 和本地计数一致，与旧版口径相同 |
| R1-09 | macOS 既有套件 | macOS 现有的构建、单元、安装态和发布检查全部通过 |
| R1-10 | Windows 节流状态 | 新增的 Windows 测试通过：读回 Core 的节流状态为“控制位已置、状态位为零”。覆盖安装后的第一次连接即生效 |
| R1-11 | Windows 用电池时的转发 | 在 Windows 11 22H2 以上的笔记本上（最好是混合架构 CPU）拔掉电源，旧版和新版各做一次 R1-06 的流程。新版满足 R1-06 的标准；旧版的表现如实记录。没有这类机器就标 BLOCKED 并写明原因 |
| R1-12 | Windows 失败降级 | 模拟退出节流调用失败：Core 照常启动并连接成功，诊断日志有一条记录且不含敏感信息 |
| R1-13 | Windows 既有套件 | Windows 现有的构建、单元、Windows 专用测试和发布检查全部通过；release_scrub 没有新的命中 |

### 第 2 轮：拒绝 QUIC、被拒绝流量不计费、iOS 内存回收

| 编号 | 验收项 | 通过标准 |
| --- | --- | --- |
| R2-01 | 四个平台的生成配置 | 配置测试断言三条规则的顺序与内容；Windows、Android、iOS 各自的平台差异保持不变 |
| R2-02 | 补丁哈希锁 | prepare_mihomo 检查通过；用未打新补丁的源码构建会失败（守护常量生效）；test-mihomo-patches 通过 |
| R2-03 | 统计口径单元测试 | REJECT 和 REJECT-DROP 的上传、下载都不进代理口径；经节点的照常计入；DIRECT 不变 |
| R2-04 | 端到端计费 | 对着本地测试节点发起大量 UDP 443 和 TCP 混合流量：后端记账的字节只包含 TCP 和非 443 的 UDP；“节点不支持 UDP”场景下，被拒绝的 UDP 记账为 0 |
| R2-05 | 浏览器回退 | macOS 的 Chrome 和 Safari、Windows 的 Chrome 和 Edge 打开 YouTube：起播时间与“节点不支持 UDP”的现状相当，没有错误页；在 chrome://net-internals 里确认走的是 TCP |
| R2-06 | 移动端回退 | Android 的 YouTube App 和 Chrome、iOS 的 Safari 和 YouTube App 都能正常起播（iOS 真机受签名限制时，按以往惯例标 BLOCKED 并写明原因） |
| R2-07 | 不误伤 | DNS 解析、WebRTC/STUN（非 443 端口）、游戏类 UDP 回显的行为与修复前相同，仍然绝不直连 |
| R2-08 | 真机 A/B（用户的真实节点） | 第 1 轮的新版加本轮改动：YouTube 4K 观看 30 分钟，速度曲线不再出现“掉下去、慢慢恢复、之后一直偏低”的平台期 |
| R2-09 | 既有套件 | 四个平台的 CI 全部通过。Android 计费作业已知会偶发失败（记忆中的 B01、B04、N01、C01），遇到时按以往做法重跑并在记录里注明，不能当成本轮的缺陷 |
| R2-10 | iOS 回收限频单元测试 | MemoryBudget 的新测试覆盖第 5.4 节第 4 条的两种情形 |
| R2-11 | iOS 采样日志 | 模拟器（有真机就加真机）运行时，60 秒一次的采样行包含线程优先级的最低值和最高值，以及 GC 的 CPU 占比；summarize-samples 脚本能解析新字段，旧字段不变 |
| R2-12 | iOS 持续流量测量 | 第 5.4 节第 1 条的 30 分钟测量完成：记录占用峰值、强制回收次数（新旧策略各一次）、GC 的 CPU 占比；新策略下强制回收次数明显少于旧策略，扩展没有被系统杀掉。数据如果触发第 5.4 节第 3 条，附上阈值调整方案，等用户决定 |

### 第 3 轮：长时吞吐回归、跨平台核查与文档

| 编号 | 验收项 | 通过标准 |
| --- | --- | --- |
| R3-01 | 吞吐回归工具 | 在 macOS 上测经 Helper 拉起的正式安装版，满足 6.1 的判定；同一台机器上用替身方式重现修复前的版本，工具能判出 FAIL（证明工具真的能抓到 F1） |
| R3-02 | Windows | 同一台笔记本插电和用电池各跑一次工具，都满足 6.1 的判定；报告里有 Core 的优先级类和节流状态 |
| R3-03 | 长时观看 | macOS 和 Windows 各连续 60 分钟高码率播放，CSV 没有平台期；Core 的线程数、句柄数、内存增长都在 soak 现有阈值内 |
| R3-04 | 文档 | 第 6.3 节列出的文档都已更新，排障顺序可以照着执行 |
| R3-05 | 用户实测清单 | 第 10 节的 U-52 … U-60 写入并交给用户 |
| R3-06 | Android 调度实测 | 按第 6.2 节用 adb 查看：YouTube 在前台播放时和息屏 10 分钟后，Go 线程 nice 值都不大于 0，进程不在后台组。记录设备型号和系统版本；模拟器结果要注明 |
| R3-07 | Android 省电说明 | App 的“后台运行与省电”提示同时提到断开和变慢（中英文都改）；网站 /guide 的 Android 部分有国产系统的放行说明，教程的现有页面检查和截图回归通过 |
| R3-08 | iOS 真机长时观看 | 用第 2 轮的采样日志连续 30 分钟高码率播放：线程优先级不低于普通级，扩展没被杀，GC 的 CPU 占比和回收次数如实记录。没有真机签名就标 BLOCKED 并写明原因 |

## 9. 用户再报网速问题时的证据收集顺序（写入第 3 轮排障文档）

1. 先确认平台、是否插电（笔记本），以及掉速发生的时刻。
2. macOS：ps -M 查看 asterlink-core 的线程优先级，launchctl print 查看 Helper 的类型。Windows：任务管理器里 Core 进程是否显示“效率模式”，以及当时是否在用电池。Android：adb 查看线程 nice 值和调度组，以及系统的电池优化设置。iOS：扩展日志里的采样行（线程优先级、占用、GC 开销）和有没有压力记录。
3. Core 的 CPU 占用，以及同一时刻的整机 CPU 负载。
4. 浏览器的 chrome://net-internals 里 QUIC 是否还在被使用（第 2 轮之后应当没有）。
5. YouTube “详细统计信息”里的连接速度曲线截图。
6. 同一时刻经隧道下载固定文件的速度，用来区分是视频服务的问题还是隧道的问题。
7. 节点类型、节点是否开了 UDP（在管理后台查看，不要交给用户）。

## 10. 用户部署后的实测（接在现有 U 系列之后）

| 编号 | 内容 |
| --- | --- |
| U-52 | macOS 升级到新版后，按提示在“账户”页重新安装后台服务，然后连接成功 |
| U-53 | macOS：YouTube 4K 连续观看 30 分钟以上，没有“突然掉速、慢慢恢复、之后一直偏慢”的现象 |
| U-54 | macOS：观看时同时做别的重负载操作（大量网页、编译、拷贝），视频不卡顿或只有短暂波动 |
| U-55 | Windows：同 U-53，插电时做一次 |
| U-56 | Android、iOS：YouTube App 正常起播，长时间观看稳定 |
| U-57 | 修复前后各用 1 小时：同样的使用方式下，网站上显示的流量没有异常增减 |
| U-58 | 视频通话、游戏等非网页类 UDP 应用的行为与修复前相同 |
| U-59 | Windows 笔记本拔掉电源：同 U-53 和 U-54 |
| U-60 | Android（尽量用国产系统手机）：按 App 提示设为“不限制”后，切到 YouTube App 观看 30 分钟、息屏再亮屏，速度稳定且没有断开 |

## 11. 完成定义

- F1：经 Helper 拉起的正式 Core 线程优先级不低于 31，有打包守护和安装态守护；升级路径已经实测。
- F4：Windows 的 Core 显式退出节能节流，有自动测试守护；用电池时的转发已经实测，或者写明 BLOCKED 的原因。
- F2：四个平台的 Core 都拒绝 UDP 443，浏览器和 App 的回退已经实测。
- F3：被拒绝的流量不计费，有单元和端到端证据，补丁哈希锁已经更新。
- F5：iOS 的强制回收已经限频，采样日志能反映线程优先级和 GC 开销，有持续流量下的测量数据；阈值是否调整由数据和用户决定。
- Android：真机调度已经核查，省电说明已经更新。
- 第 3 轮的吞吐回归工具能区分修复前和修复后，并留在仓库里作为以后的回归手段。
- 所有 BLOCKED 项都写明原因和后续由谁验证。
- 修完以后，任何平台上的速度都只应受节点和线路影响。如果还有掉速，按第 9 节收集证据，作为新缺陷处理。

## 12. 实施记录

（每轮完成后在此追加：分支、PR、合并提交、各验收项 PASS/FAIL/BLOCKED 及证据位置。）

### 第 1 轮（2026-11-04，分支 worktree-fix-round1-desktop-qos）

**改动**
- macOS：
  - scripts/build-native-macos.py：Helper plist 加 ProcessType = Interactive。
  - 新增 AsterLinkShared/SchedulingProbe.swift。HelperReply 增加可选字段 scheduling_limited（旧 Helper 不带这个字段，视为未知，不拦截）。
  - Helper 的 hello、startWorker 和 Debug 自检都报告这个字段。
  - HelperUpgradePolicy 增加 schedulingLimited 结论和 refusesConnect 保护。
  - HelperConnection.start 按它拦截。HelperClient 在每次注册成功或待批准时记下当前版本号。
- Windows：
  - 新增 AsterLink.Platform/PowerThrottling.cs，并在 Native.cs 增加声明。
  - WorkerProcess 在加入作业对象之后、恢复运行之前退出执行速度节流，失败时只记录 PowerThrottlingError。
  - CoreHost 在失败时记一条警告日志。
  - 提供读回接口 ReadPowerThrottling。
- 测试与守护：
  - macOS 新增 SchedulingTests（8 项）。
  - bundle 套件新增 S-PLIST-TYPE 和 S-QOS。S-QOS 把构建出的 Helper 自检作为本用户 launchd 任务运行：按发布类型运行时，Helper 和真实 Core 都不受限；不设类型的对照组必须受限。
  - Windows 的 WorkerTests 新增两项：读回节流策略；无效句柄只报错不抛异常。
- 文档：docs/macos-release.md 第 5 节、docs/windows-service-design.md 第 4 节。

**本轮实测发现（已写入修订后的 D2）**
- 在本机用户级 launchd 任务里实测各类型，每项都是父进程和子进程的优先级：
  - 不设或 Standard：daemon (3)，20T/20T。
  - Interactive：interactive (4)，31T/31T。
  - Adaptive 或 Background：空闲时 4T/4T。
- 用 Foundation 的 Process 设置服务质量，突破不了 taskpolicy 的 utility 或 background 限制。
- 进程可以从任务信息里读到自己是否受限：受限时任务基础优先级是 20，最高可用优先级也是 20；正常时是 31 和 63。

**验收**

| 编号 | 结果 | 证据 |
| --- | --- | --- |
| R1-01 | PASS | release 构建 r1-upgrade 的 plist 有 ProcessType = Interactive；bundle 套件 S-PLIST-TYPE PASS |
| R1-02 | PASS（重新安装路径） | 重新安装后，launchctl print 显示 spawn type = interactive (4)，parent bundle version 为 0.1.1。没有在一台完全没装过的机器上做全新安装 |
| R1-03 | 部分 PASS | 用户本机从 0.1.0 升级到 0.1.1（内部 ad-hoc 构建，开发通道，生产 API）。升级前：daemon (3)，Helper 20T。重新安装后：interactive (4)，Helper 31T。只升级 App 时 App 显示了什么，用户没有反馈，所以未记录。连接中禁止重新安装：沿用原有单元测试，本轮未改动 |
| R1-04 | PASS | 重新安装后，Core 的 14 个线程全部 31T，Helper 31T。连接真实节点并在 CPU 满载时，Core 仍全部 31T（修复前本机是 19 个线程全部 20T） |
| R1-05 | PASS（替身） | 第 2.2 节的替身测量就是修复前后两种优先级的对比。正式 Core 在竞争下的表现见 R1-06 |
| R1-06 | 部分 PASS | 新版、用户的真实节点、OVH 10 MB 文件、每轮 4 条并行连接，每阶段 4 轮取中位数：空闲 67.7 Mbit/s，10 个核心全部占满时 66.5 Mbit/s（98%），结束后 68.8 Mbit/s。用户选择跳过旧版对照，所以没有修复前的真实节点数据。没有做 30 分钟 YouTube 观看。附带观察：空闲时单条连接偶尔整段卡在 2～3.5 Mbit/s，而同时其他连接有 50 Mbit/s，与 CPU 无关。Cloudflare 测速接口在约 30 次请求后返回空响应，判断为该接口限流（未核实），这组数据已作废 |
| R1-07 | NOT RUN | 没有做空闲 10 分钟能耗对比和睡眠唤醒测试。空闲时 Core 的 CPU 占用约 0.6% |
| R1-08 | NOT RUN（无改动面） | 本轮没有改 Go Core 和计费代码。Go IPC 337 项、策略、节点代理测试都通过，但没有单独做一次和后端的计费对账 |
| R1-09 | PASS | unit 8/8（Swift 63 项，含新增的 8 项）；bundle 8/8；release 16 PASS、2 BLOCKED（Gatekeeper 和公证需要开发者 ID，与以往相同） |
| R1-10 | 待 CI | 本机没有 .NET。WorkerTests 新增的读回测试在 CI 的 Windows 作业中运行 |
| R1-11 | BLOCKED | 没有 Windows 笔记本，无法测用电池时的转发 |
| R1-12 | 待 CI（单元级） | 无效句柄只报错不抛异常的测试；真实系统调用失败的情形没有办法人为制造 |
| R1-13 | 待 CI | Windows 构建和测试 |

**意外事件（如实记录）**：为了备份用户已装的 /Applications/AsterLink.app（0.1.0，11-02 的内部 ad-hoc 构建），执行 mv 时报“找不到文件”，之后 /Applications、备份目录和废纸篓里都没有这个 App，原因未查清。同一次构建的安装包 artifacts/native-macos/prod1/release/AsterLink-0.1.0-INTERNAL-adhoc-arm64.dmg 仍在，可以用它恢复。用户机器上现在装的是本轮的 0.1.1 测试构建（r1-upgrade，修复保护记录之前的版本；那次修复只影响“已重新安装”的记录）。

**留给用户和后续轮次**
- R1-11：Windows 笔记本拔电测试。
- R1-07：睡眠唤醒和能耗。
- 正式发版时用新版本号构建。
- 单条连接偶发卡在低速的现象，交给第 2 轮（拒绝 QUIC 后复测）和第 3 轮的吞吐工具继续观察。


### 第 2 轮（2026-11-05，分支 worktree-fix-round2-quic-reject）

**改动**
- 共享 Core 规则（native/core/internal/engine/config.go）：
  - 新增常量 RejectQUICRule，内容为 AND,((NETWORK,UDP),(DST-PORT,443)),REJECT，排在 MATCH,node 和 MATCH,REJECT 之前。四个平台共用。
  - 测试：config_test、tunspec_test、ios_test 锁定三条规则的顺序。新增 TestQUICIsRejectedBeforeTheNode，覆盖两类节点：
    - 支持 UDP 的节点：UDP 443 → REJECT，TCP 443 → 节点，UDP 53、3478、4433、8443、19302 → 节点。
    - 不支持 UDP 的节点：UDP 443 和其他 UDP 端口都 → REJECT，TCP 443 → 节点。
- Mihomo 审查补丁：
  - 由 0001-sing-tun-listener-context.patch（asterlink-sing-tun-context-v1）改为 0001-asterlink-mihomo.patch（asterlink-mihomo-v2）。v1 的 sing-tun 部分逐字节保留。
  - 新增 tunnel/statistic/manager.go 的计费判断：DIRECT、REJECT、REJECT-DROP 都不计入代理口径。新增回归测试 asterlink_reject_test.go。
  - manifest 新增两个文件的原始和补丁后哈希。
  - 编译期守护常量 statistic.AsterLinkRejectPatchVersion，在 internal/engine/mihomo_patch.go 中引用。
  - scripts/test-mihomo-patches.py 同时运行 statistic 包的回归测试。补丁 README 已更新。
- 进程内端到端测试（internal/engine/quic_reject_test.go）：
  - 按生产规则配置 Mihomo，经 tunnel.HandleUDPPacket 送入 20 个 1200 字节的 UDP 443 包。
  - 断言：流量由 REJECT 出站承载；全部流量统计增加 24000 字节；计费口径不变。
- iOS（F5）：
  - MemoryBudget.observe 增加时钟参数，强制回收按 D9 和第 5.4 节限频，并累计 releases。
  - ResourceSample 增加 releases、prio_min、prio_max、gc_cpu_pm 四个字段。旧格式的行仍能解析。
  - Go 的 mobile.RuntimeStats 增加 gc_cpu_fraction。
  - summarize-samples.py 输出新字段，只报告不判定。
  - 文档：docs/ios-lifecycle.md 第 3 节、docs/ios-tun-design.md 第 8 节和第 34 行（规则说明）。

**验收**

| 编号 | 结果 | 证据 |
| --- | --- | --- |
| R2-01 | PASS | 四个平台生成的配置都被固定版本的 Core 解析，规则依次是 AND→REJECT、MATCH→node、MATCH→REJECT。Windows、Android、iOS 原有的平台差异测试全部通过 |
| R2-02 | PASS | prepare_mihomo --check 通过，id 为 asterlink-mihomo-v2。把 manager.go 换回原文后，go build ./internal/engine 失败，报 undefined: statistic.AsterLinkRejectPatchVersion。test-mihomo-patches 全部通过（哈希锁的 6 项、sing-tun 的 3 项，其中 1 项只能在 Linux 上运行而跳过，以及 statistic 的 2 项） |
| R2-03 | PASS | TestAsterLinkRejectIsNotBilled、TestAsterLinkBilledChains |
| R2-04 | PASS（进程内）/ NOT RUN（真实后端） | TestQUICPacketsAreRejectedAndNotBilled 走的是真实规则、REJECT 出站和统计模块。把计费判断临时改回“只排除 DIRECT”后，测试报出“proxy totals 0/0 -> 24000/0”，证明它能抓到这个缺陷。没有用真实后端做对账：本轮没有改后端，也没有改 Core 的上报代码 |
| R2-05 | PASS（Chromium 内核，macOS） | 用户本机，同一节点（出口 IP 不变）。旧内核时，内置浏览器（Chrome 152 内核）访问 Cloudflare 诊断页显示 http=http/3。换成新内核后显示 http=http/2，YouTube 4K 视频正常起播，几张截图里画面在持续推进，视频分片的 nextHopProtocol 没有 h3。用户自己的 Chrome、Safari 和 Windows 上的浏览器没有测 |
| R2-06 | NOT RUN | 没有在 Android 或 iOS 设备上测 YouTube App 的回退。iOS 真机受签名限制，按惯例为 BLOCKED |
| R2-07 | PASS（规则级） | TestQUICIsRejectedBeforeTheNode 覆盖 UDP 53、3478、4433、8443、19302 和 TCP 443。没有做真实 WebRTC 或游戏测试 |
| R2-08 | 部分 | 新内核加第 1 轮修复，在用户本机、真实节点上的结果：Core 的 16 个线程全部 31T；每轮 4 条并行下载，10 个核心全部占满时与空闲时的速度比为 0.96～1.09，边播 4K 边测时为 1.01。但当晚的绝对吞吐只有约 18 Mbit/s，第 1 轮早上同一节点、同一方法测到约 67 Mbit/s。本轮没有改动 TCP 路径（只多了一条只匹配 UDP 的规则），Core 的 CPU 占用不到单核的 50%（机器共 10 核），所以判断是节点或线路的时段差异，但没有核实。没有做 30 分钟观看测试 |
| R2-09 | 本地 PASS / 待 CI | macOS unit 8/8（Go IPC 338 项）。bundle 8/8。release 15 PASS、2 BLOCKED（开发者 ID）、1 FAIL：R02-UNINSTALL 因用户的 AsterLink 正在运行而拒绝执行卸载，属于测试环境原因，第 1 轮 App 未运行时该项通过。Go Core 全部包、AsterLinkKit 89 项通过。Windows、Android、iOS 的完整构建待 CI |
| R2-10 | PASS | memoryReleaseIsRateLimitedOnAPlateau、pressureFollowsGrowthNotTheClock、memoryBudgetThresholds |
| R2-11 | PASS（单元级） | 采样行能完整往返解析；本进程的线程优先级可以读出；旧格式的行能读；汇总脚本输出新字段。没有在模拟器或真机的扩展里实际采样 |
| R2-12 | NOT RUN | 本轮没有做 30 分钟持续流量的内存测量，交给第 3 轮的 R3-08（真机）或模拟器。所以阈值保持初值不变（D9） |

**留给后续**：R2-06、R2-12、用户在自己的 Chrome 和 Safari 上观看 30 分钟（U-53）、同一时段的吞吐对比，以及第 1 轮记录里的单条连接偶发低速，都交给第 3 轮的吞吐工具。用户机器上现在装的是本轮的 0.1.1 构建（r2-quic，带补丁 v2）。
