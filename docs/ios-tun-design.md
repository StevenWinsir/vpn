# iOS 隧道设计（第 2 轮）

> 对应 `handoff_app_ios.md` 第 4、7、8 节与第 2 轮。状态：**代码完成，真机待验**（无付费 Apple 团队，签名身份 0 个；已配对 iPhone 一台但无法签 `packet-tunnel-provider`）。模拟器不能运行 Packet Tunnel（E-SIM-NE）。

## 1. 资源所有者

| 资源 | 所有者 | 说明 |
| --- | --- | --- |
| 会话 token、Meter、租约、grant | **同一时刻只有一个进程的 Core** | App 未连接时持有；连接时经交接票据移交扩展；断开时交回 |
| utun 接口、路由、系统 DNS | `PacketTunnelProvider`（NE） | `setTunnelNetworkSettings` 按 TunSpec 设置；系统在扩展退出时撤下 |
| utun 原始 fd | NE | 扩展从不关闭它 |
| utun 的 `dup()` 副本 | Go Core | Mihomo 监听器关闭它；启动失败时由 `releaseTun` 按 inode 身份关闭（不重复关闭） |
| IPv6 中继（仅节点无 IPv6 时） | Go Core | 拥有 utun 副本与 socketpair 一端；栈拥有另一端 |
| VPN 配置 | App（`NETunnelProviderManager`） | 不存票据、token、节点；不启用 On-Demand / includeAllNetworks |

## 2. 会话交接（`shared/contracts/handoff-v1.json`）

- 票据字段：`version, api_base, channel, session_id, token, device_id, selected_node_id, generation, last_sequence, issued_at, user`。
  比规划多了 `channel`（`ParseBase` 需要）、`last_sequence`（接收方检查服务器不落后于账本）与 `user`（快照显示邮箱；`/session` 不返回用户）。
- 导出 `ExportHandoff`：先关数据通路并结清（有未确认批次 → `handoff_unsettled`，会话留在原处），再停 keepAlive/目录 watcher，**不撤销** token，状态 `released`。
- 接收 `AdoptHandoff`：`GET /session` 核对 `session_id`（不符 `handoff_mismatch`）、`last_sequence` 不低于票据（否则 `handoff_conflict`），然后与登录后一样运行并重新绑定所选线路。本进程已有会话 → `handoff_busy`；代次不高于本进程已接收过的 → `handoff_stale`；超过 5 分钟或来自未来 → `handoff_expired`。瞬时网络错误不消耗票据，可重试。
- 账本连续性：接收方的 Meter 以服务器已确认状态为基线，`meter.Begin` 原有规则即可保证不漏扣、不重扣（Go 测试 `TestHandoffRoundTrip` 验证 20 MiB 基线 + 1000 B 上 + 80 MiB 下的逐字节结清）。
- 票据只在内存中传递：`startVPNTunnel(options: ["handoff": 票据])` 与 provider message 回复；`String`/`%#v` 打印已脱敏。
- 无法交回（设置里关闭、被其它 VPN 抢占、系统停止）：扩展 `MobileStop` 结账并**撤销** token（比规划的“丢弃”更严格，立刻释放设备名额）；App 下次尝试取回自己交出的票据会被服务器拒绝，回到在线登录。
- 扩展启动失败（已接收但未连上）：扩展 `ExportSession` 放弃会话但不撤销，App 用自己交出的票据取回（`own ticket back` 案例）。
- 扩展无票据启动（设置开关、按需连接）：`handoff_required`，“请在 AsterLink App 内连接”。

## 3. App ↔ 扩展消息（handoff-v1 `provider_messages`）

`{"kind":"ipc","request":{…ipc-v1…}}` → 原样的 ipc-v1 响应；`release` → `{"kind":"released","ticket":…}` 或 `{"kind":"error","code":…}`；`foreground` → `ack`（`MobileSetForeground`）；`route_probe` → `ack`。其它一律 `invalid_request`。Swift 与 Go 用同一份夹具测试。

## 4. TunSpec 与 NE 设置（`tun-spec-ios-v1.json`）

- 模板：`auto-route:false`、`auto-detect-interface:false`、`mtu:1500`、`inet4-address:198.18.0.1/30`、`inet6-address:fdfe:dcba:9876::1/126`、`find-process-mode:off`，规则仍是 `MATCH,node` + `MATCH,REJECT`（UDP 不会 DIRECT）。
- **iOS 规格永远 `ipv6:true`**：固定 Mihomo 在宿主无全局 IPv6 时会清掉 IPv6 地址，`iosTunConf` 把它补回；Swift 拒绝 `ipv6:false` 的规格。
- NE：IPv4 `198.18.0.1/255.255.255.252` + 默认路由；IPv6 `fdfe:dcba:9876::1/126` + `::/0`；DNS `198.18.0.2`、`matchDomains [""]`；`tunnelRemoteAddress 127.0.0.1`（不暴露节点）；MTU 1500（初值，第 5 轮按真机吞吐/内存定值）。
- 找 fd：遍历本进程 fd，`getpeername` 为 AF_SYSTEM 且控制 id 等于 `com.apple.net.utun_control`，且 `UTUN_OPT_IFNAME` 为 `utunN`；0 个或多于 1 个都拒绝；返回 `F_DUPFD_CLOEXEC` 副本。单测发现并修复了 `ctl_info` 结构对齐错误（100 字节）。macOS 非 root 不能建 utun（实测 EPERM），真实扫描只能在真机或 `sudo swift test` 验证。

## 5. 防环与 DNS

- 扩展自身套接字默认不进自身隧道（Apple 行为，**待真机 D-T05 核实**）；Go 控制面在隧道运行时仍按物理接口索引 `IP_BOUND_IF` 绑定。
- 控制面 DNS（第 2 轮决定）：Go 解析器经物理接口直接查询宿主报告的物理网络 DNS（`res_getservers`，在应用隧道设置前冻结）；没有可用的则用 `223.5.5.5, 119.29.29.29, 1.1.1.1, 8.8.8.8`。隧道内的 `198.18.0.0/15` 解析器永不使用。未采用 `DNSServiceGetAddrInfo` 方案。
- Mihomo 的节点域名引导解析沿用 `1.1.1.1`（全平台一致）。

## 6. IPv6：收进隧道再拒绝

节点可承载 IPv6 → 完整代理。否则 utun 与栈之间插入中继（AF_UNIX 数据报 socketpair，关闭 recvmsg_x/sendmsg_x）：IPv6 TCP → RST（RFC 9293 规则），UDP → ICMPv6 1/1（行政禁止，引用至 1280 字节内），ICMPv6/扩展头/组播 → 丢弃；回复限速 200 个/秒；同时 `resolver.DisableIPv6` 使 fake-ip 不返回 AAAA。主机测试覆盖 SYN/ACK/RST/UDP/ICMP/截断/组播与 9000 字节 IPv4 透传、goroutine 零增长。

## 7. Verify 两段

1. 扩展内：探测请求经 `dialThroughNode`（进程内经节点）——证明节点握手与转发。
2. App 内：`RouteProbe` 用 ephemeral `URLSession` 访问 `generate_204`，必须 **204 且空体**，结果以 `route_probe` 消息回报。第 4 轮把“两段都通过才显示已连接”接入 UI。若真机证明扩展流量会进入自身隧道，删除第 2 段。

## 8. 内存

启动时 `MobileTuneMemory(22 MiB, GOGC 60)`；每 5 s 读 `phys_footprint`：≥38 MiB `FreeOSMemory`，≥44 MiB 记录压力。峰值写入扩展日志。`MobileRuntimeStats()` 提供 goroutine/堆数据给 D-CYCLES。全部为初值，真机 D-MEM-idle 后定。

体积：xcframework 设备切片 `go.o` 52.9 MB（无 DWARF），第 1 轮记录为 28.5 MB，差异原因未查明（可能是测量口径不同），第 6 轮发布审计时以链接后体积为准复核。

## 9. 真机验收步骤（有付费团队 + iPhone 后执行）

前提：Apple Developer Program（组织）团队；在团队中注册 App 与 `.tunnel` 两个 Bundle ID 并开启 Network Extension（Packet Tunnel）；`Base.xcconfig` 中 `ASTER_BUNDLE_PREFIX` 改为注册的前缀；Xcode 登录该团队并对两个 target 选择自动签名；iPhone 开启开发者模式。**跑真实节点前在 Mac 上断开公司 VPN 并退出 macOS 版 AsterLink**；手机用自己的 Wi-Fi/蜂窝不受 Mac 公司 VPN 影响。demo 后端需已部署第 1 轮的 `ios` 平台白名单。

1. Xcode 选择 iPhone，运行 Debug。首页点“隧道调试（第 2 轮）”。
2. 输入测试账号（需有效套餐，**管理员 test@test.com 无套餐不能连接**）→ 登录 → 选择第一条线路。
3. 安装 VPN 配置（系统弹窗允许，可能需输入设备密码）→ 连接（交出会话）。设置 → VPN 显示已连接、状态栏出现 VPN。
4. D-T01：Safari 打开任意 HTTPS 站点，后台节点/sink 计数增长；手机未设置任何 HTTP 代理。D-T02：分别用 SS 与 VLESS(Reality) 线路；把节点凭据改错后应连接失败。
5. D-T03：Safari 访问 `http://198.18.0.1` 以外的域名正常；扩展快照 `tunnel_health=up`。D-T04：蜂窝（有 IPv6）下访问 `https://ipv6.google.com`：v6 节点可打开；无 v6 节点立刻失败且 `test-ipv6.com` 显示回退 IPv4，绝无物理 IPv6 出口。
6. D-T05：连接中点“读取扩展快照”应正常（API 经物理网络）；D-T06：`udp:false` 节点上 HTTP/3 站点仍可经 TCP 打开，UDP 不直连。
7. “App 路由探测”应显示通过（204）。
8. D-T07：“断开并取回会话”→ 设置 → VPN 未连接、无图标，App 内核状态 `ready`（未重新登录）。D-T09：连接中在 Xcode 中 kill 扩展进程，系统撤下 VPN，无残留路由。
9. D-CYCLES：连接/断开 20 次，记录 `MobileRuntimeStats` 的 goroutine 与 `lsof` fd 数零增长；D-MEM-idle：空载 5 分钟记录扩展日志中的内存峰值。
10. 把每项按 `case_id / expected / observed / PASS|FAIL|BLOCKED` 写入 `handoff_app_ios.md` 第 15 节。
