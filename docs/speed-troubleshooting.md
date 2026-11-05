# 网速问题排障与长时吞吐回归（handoff_fix.md 第 3 轮）

本文是 handoff_fix.md 第 6.3 节要求的排障文档：用户再报“看视频一会儿就变慢、但不断线”这类问题时，按第 2 节的顺序收集证据；第 3 节说明仓库里的长时吞吐回归工具怎么用、怎么判、阈值是怎么定的；第 4 节是四个平台修复后的已知结论和仍未测的部分；第 5 节是留档的上游细节。

所有修复前的根因和证据都在 handoff_fix.md 第 2 节，这里不重复。

## 1. 修完以后的预期

- 任何平台上，隧道的速度只应受节点和线路影响：不随 CPU 负载、用电池、看视频的时长变化而“掉下去、慢慢恢复、之后一直偏慢”。
- 节点本身的峰值随时段变化（同一节点早上 67 Mbit/s、晚上 18 Mbit/s 都实测过）。判断隧道有没有问题看**稳定性**：同一时段里有没有长时间塌到几百 kbit/s 或基线一半以下的平台期，而不是峰值高低。
- 浏览器和 App 的 QUIC（UDP 443）在 Core 里被固定拒绝，自动回退到 TCP（HTTP/2）。这是有意的（第 4 节）。

## 2. 用户再报网速问题时，按这个顺序收集证据

1. 平台、是否插电（笔记本）、掉速发生的时刻和持续多久。
2. 系统有没有把数据面降级：
   - macOS：`ps -M -p $(pgrep -x asterlink-core)` 看 Core 各线程的 PRI。正常时最高值是 31，忙的线程会被系统临时降到 21～30，这是正常的分时衰减；**所有线程都不高于 20** 才是 F1 的实用级限制。再看 `launchctl print system/com.asterlink.vpn.helper` 的 `spawn type`，应当是 `interactive (4)`；是 `daemon (3)` 说明 Helper 还是旧注册，让用户在“账户”页重新安装后台服务。
   - Windows：任务管理器“详细信息”里 asterlink-core.exe 是否显示“效率模式”，当时是否在用电池。吞吐工具（第 3 节）以管理员身份运行时会直接读出节流状态（`opted_out` 为正常）。
   - Android：`python3 scripts/android/thread-sched.py --samples 3`（只读，见第 4.3 节），以及系统的电池优化设置。
   - iOS：扩展日志里的采样行（`prio_min`/`prio_max`、`footprint`、`releases`、`gc_cpu_pm`）和有没有压力记录，用 `scripts/ios/summarize-samples.py` 汇总。
3. Core 的 CPU 占用，以及同一时刻整机的 CPU 负载（活动监视器或任务管理器）。
4. 浏览器的 `chrome://net-internals/#quic` 或开发者工具里视频分片的协议：修复后应当没有 h3。
5. YouTube“详细统计信息”里的连接速度曲线截图。
6. 同一时刻经隧道下载固定文件的速度，区分是视频服务的问题还是隧道的问题：最直接的是在用户机器上跑第 3 节的工具 10 分钟。
7. 节点类型、节点是否开了 UDP：在管理后台查看，不要交给用户。

## 3. 长时吞吐回归工具 asterlink-throughput

位置：`native/core/cmd/asterlink-throughput`。开发和验收工具，**不进任何发布包**（四个平台的打包只构建各自的 Core 和 App；release_scrub 照常审计发布产物）。

它只是隧道里的一个普通应用：不启动、不停止、不配置 Core，所以测到的就是正式路径（macOS 经 Helper、Windows 经系统服务拉起的 Core），而 F1 和 F4 恰恰只在这条路径上出现。

### 3.1 怎么跑

先在 App 里连接，然后在同一台机器上：

```sh
cd native/core && go build -o /tmp/asterlink-throughput ./cmd/asterlink-throughput
/tmp/asterlink-throughput --url <经隧道可达的大文件> --duration 10m --label "<版本、节点、插电或电池>"
```

- `--url`：被反复下载的文件。可以用流量接收端 asterlink-trafficsink 的 `/down?bytes=268435456&t=<令牌>`（部署方式见 docs/android-billing-acceptance.md），也可以用公开的测速大文件（例如 OVH 的 `proof.ovh.net/files/1Gb.dat`）。查询串只写进内存，报告里会被抹掉。**优先用自己部署的接收端**：公开测速站会按出口 IP 限制并发或频率，限住以后整段窗口为 0，看起来像隧道断流（2026-11-05 实测：OVH 在 4 条并行连接约 10 s 后对节点出口完全不再应答，Cloudflare 的下载接口返回 403，而同一时刻隧道里的其他网站正常）。跑之前先用 curl 单独确认文件能下；工具的报告里 `stalls` 和 `errors` 很多、首字节探测全部失败时，先怀疑测速源。
- 默认 4 条并行连接、前 30 s 预热不判、第 4 分钟开始 2 分钟的 CPU 竞争（每个核一个忙循环进程，普通优先级，和任何应用一样），共 10 分钟。60 分钟长时观看（R3-03）用 `--duration 60m --contention-for 0`，只判平台期。
- 每 10 s 一行写进 `artifacts/throughput/<时间>/throughput.csv`：吞吐、1 s 一次的小请求首字节时间（整条路径的往返，不是 TUN 本地的握手）、请求数、出错和“30 s 没动”被切断重试的次数，以及它能从外部读到的 Core 状态：
  - macOS：Core 的线程数和 PRI 最低、最高值。
  - Windows：优先级类、执行速度节流状态（`system` 由系统决定，`opted_out` 为第 1 轮修复后的高服务质量，`eco` 为节能）和电源（`ac`/`battery`）。普通用户可能读不到 LocalSystem 进程的节流状态，此时报告写明错误，用管理员身份运行即可。
  - Android 和 iOS 不在这里读（见第 4.3、4.4 节）。
- 同目录的 `report.json` 是判定、所用阈值和 Core 汇总。退出码：0 PASS，1 FAIL，3 BLOCKED（太短、没下到任何字节、或竞争阶段不在运行时间内），2 参数错误。

### 3.2 怎么判（handoff_fix.md 第 6.1 节）

- 基线：预热之后、竞争开始之前各 10 s 窗口吞吐的中位数（至少 3 个窗口）。
- 竞争期间各窗口的中位数不低于基线的 70%。
- 竞争撤除后，3 s 滑动平均在 10 s 内回到基线的 90%。
- 全程（含竞争期）没有连续 3 分钟以上低于基线 50% 的平台期。

阈值都是参数（`--min-contention-ratio`、`--recover-ratio`、`--recover-within`、`--plateau-ratio`、`--plateau-for`），报告里记录实际使用的值。改阈值必须在验收记录里写明原因和数据。

### 3.3 工具能抓到 F1 的证据与阈值校准

用替身在同一台 Mac（M4，10 核）上对比，替身是 relay 子命令（逐块 AES-GCM 加解密的转发器），接收端是本机的 asterlink-trafficsink，竞争为 10 个忙循环进程，3 分钟一轮（第 20～60 s 为基线，第 60～120 s 为竞争）：

| 替身 | 调度 | 基线 | 竞争期中位数 | 首字节时间 p50（空闲 → 竞争） | 判定 |
| --- | --- | --- | --- | --- | --- |
| 不限速 | 普通（31） | 36.2 Gbit/s | 23.9 Gbit/s（66%） | 6.6 → 0.2 ms | FAIL |
| 不限速 | `taskpolicy -c utility`（20） | 22.4 Gbit/s | 0.74 Gbit/s（3%） | 0.3 → 约 150 ms | FAIL，报告标出 UTILITY CLAMP |
| 限速 2 Gbit/s | 普通（31） | 1983 Mbit/s | 1646 Mbit/s（83%） | 0.6 → 0.5 ms | **PASS** |
| 限速 2 Gbit/s | `taskpolicy -c utility`（20） | 1975 Mbit/s | 580 Mbit/s（29%） | 0.6 → 最高 84 ms | **FAIL**，报告标出 UTILITY CLAMP |

怎么读：
- 不限速时替身在回环上能跑几十 Gbit/s，它自己就把 CPU 吃满，10 个忙循环进来以后，不管优先级都只能分到一份，所以普通优先级也只剩 66%。这不代表真实 VPN：真实的 Core 在节点速率下不到半个核。所以 R3-01 的替身对比用 `--rate 2000`，比任何节点都快得多，又不至于自己吃满 CPU。
- 限速后两种调度一目了然：普通优先级 83%，实用级 29%。工具按第 6.1 节的 70% 判出前者 PASS、后者 FAIL，就是 F1。
- 第 6.1 节的阈值没有改。校准时只发现一件事：macOS 的 PRI 会随 CPU 占用临时衰减（普通优先级的忙线程会显示 21～30），所以判断限制看“最高值是否 ≤ 20”，不看最低值。

真实节点上的同类数据见第 6 节。

## 4. 四个平台的结论

### 4.1 macOS

第 1 轮起 Helper 以 Interactive 类型运行，Core 线程是 31（docs/macos-release.md 第 5 节）；第 2 轮起 QUIC 被拒绝。用户本机经 Helper 拉起的正式 Core 在 10 个核占满时吞吐为空闲时的 0.96～1.09（handoff_fix.md 第 12 节第 1、2 轮记录）。

### 4.2 Windows

第 1 轮起 Core 在恢复运行前显式退出执行速度节流（docs/windows-service-design.md 第 4 节）。笔记本的性能验收必须插电、用电池各跑一次第 3 节的工具，报告里电源一栏必须分别是 `ac` 和 `battery`，节流一栏必须是 `opted_out`。

### 4.3 Android

见 docs/android-lifecycle.md 的“Scheduling”一节。

### 4.4 iOS

见 docs/ios-lifecycle.md 第 3 节。

### 4.5 QUIC 拒绝与计费（四个平台共用的 Core）

- Core 的规则依次是：UDP 且目标端口 443 → REJECT；其余 → 节点；节点不支持 UDP 时其余 UDP → REJECT。浏览器和 App 的 QUIC 因此走不了节点的 UDP 通道（固定版本 Mihomo 的 UDP 队列满了会静默丢包，跨境 UDP 也常被限速），回退到 TCP。TCP 443 和其他 UDP 端口（DNS、游戏、WebRTC/STUN）不受影响，仍然绝不直连。
- 被 REJECT 或 REJECT-DROP 处理的字节从未离开设备，不计入代理口径（客户端上报的计费数）。这是审查补丁 asterlink-mihomo-v2 的统计口径改动，有单元测试和进程内端到端测试（internal/engine/quic_reject_test.go）。

## 5. 留档的上游细节（不改代码）

- sing-tun v0.4.22 的 system 栈：TCP NAT 表的条目空闲 5 分钟后回收；本地端口在约 5.5 万次新连接后回绕。都不会造成“持续变慢”。
- 同一个栈在 darwin 下只有一个读包循环（batchLoopDarwin），所有 TCP 包往返都要经过它两次。它本身不是缺陷，但它解释了 F1 为什么伤害那么大：整条隧道的时延系于这一个循环的调度等级。
- iOS 在节点不支持 IPv6 时启用用户态 IPv6 中继：每个包多一次用户态拷贝，并关闭批量收发。这是固定开销，不会随时间变慢；只有真机数据证明它是瓶颈时才处理。

## 6. 第 3 轮实测记录

- 替身对比：第 3.3 节。
- Android 模拟器的调度核查：docs/android-lifecycle.md 的 Scheduling 一节。
- 真实节点（用户本机，2026-11-05）：用户事先说明节点当时很慢。经 Helper 拉起的正式 Core（0.1.1 r2-quic，Helper 为 interactive (4)，线程全部 31T，CPU 占用 0.1%）。单条连接约 8 Mbit/s；10 分钟测量开始约 10 s 后，OVH 对节点出口完全不再应答，Cloudflare 下载接口返回 403，Hetzner 只有 0.03～1.4 Mbit/s，而 Cloudflare 的诊断页仍能打开。这一轮因此作废，只说明节点出口和测速源当时的状况，不说明隧道的问题。真实节点上的 10 分钟竞争测量和 60 分钟长测，改在节点恢复正常后再做（handoff_fix.md 第 12 节第 3 轮记录写明了状态）。
