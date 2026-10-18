# iOS 移动端生命周期、内存与可靠性（第 5 轮）

本轮把“手机日常使用”会遇到的事件（息屏、切网、App 被划掉、被其它 VPN 抢占、从设置关闭、通知权限被拒）各自对应到明确的行为，并把能在 Mac 上证明的部分写成测试。**这些事件本身只有真机才能触发**：没有付费 Apple 团队就不能签 `packet-tunnel-provider`，所以全部 D 项为 BLOCKED，本文件第 5 节给出有团队后的逐项步骤。

## 1. 本轮改了什么

| 位置 | 内容 |
| --- | --- |
| `AsterLinkKit/Lifecycle.swift` | `ForegroundLease`（前台提示租约）、`TunnelStopReason`（系统停止原因 → 用户文案）、`GateLog`（闸门关闭/重开日志）、`ResourceSample`（每分钟资源采样行） |
| `PacketTunnelProvider.swift` | 前台提示改为租约；系统自行停止时按原因发一次本地通知；记录 sleep/wake、闸门事件；每 60 s 写一行资源采样 |
| `SessionController` + `NetworkTunnelControl` | App 被杀/重开后**接回**仍在运行的隧道（不重新登录）；前台心跳；通知权限被拒时提示 |
| `TunnelNotifier` | 同一次停止只发一条通知（扩展先发，App 后到不覆盖、不再响） |
| `scripts/ios/summarize-samples.py` | 把设备日志里的采样行汇总成内存峰值/句柄/goroutine 增长/闸门时长，超限给非零退出码 |
| `scripts/test-native-ios.py --suite lifecycle` | S-L01 单测 + 汇总脚本自检；D 项如实 BLOCKED |

**Go Core 与共享契约没有改动**（`ResumeGate`、退避、`SetForeground`、`RuntimeStats` 都是前几轮已有的），所以 macOS/Windows/Android 不需要回归；`handoff-v1.json` 不变。

## 2. 行为表

| 事件 | 行为 | 证据 |
| --- | --- | --- |
| 息屏/睡眠 | 扩展 `sleep()`/`wake()` 写日志；Core 的时钟监视发现挂起并**关闸**（保留 utun，不转发）；`wake()` 触发 `NetworkChanged`，让关闸后的重验立刻进行，不等退避。重验通过才开闸，被服务器拒绝则停流结账 | 闸门逻辑为 Android 第 5 轮已验的 Go 代码；日志：`gate closed reason=…`、`gate reopened … closed_ms=…` |
| 切网/飞行模式 | `NWPathMonitor` 变化 → `NetworkChanged` → 闸门按网络签名关闭并重验；连不上服务器时界面显示“等待网络”，退避有上限（Core 内） | 同上 |
| 前台提示 | App 前台时每 10 s 重发一次“可见”；扩展把它当 30 s 租约，到期自动回到后台节奏（暂停目录长轮询）。App 被杀/被挂起无法说“后台”，租约自然过期 | `ForegroundLeaseTests`、`LifecycleFlowTests.theForegroundHintIsRepeated…` |
| App 被划掉后重开 | 隧道仍在运行（会话属于扩展）。重开时 `launch()` 先 `resume()`：隧道为 connected 且扩展 Core 报告 connected 且有用户，则直接接回，**不用密码重新登录**（否则同一设备会开出第二个会话）；半启动或无会话的隧道会被关闭，再走普通登录 | `LifecycleFlowTests`（接回、接回后仍可断开交回、无会话时关闭并继续、无隧道时普通启动） |
| 其它 VPN 抢占（`superceded`） | 扩展停流结账；发一次通知“VPN 已被其它 VPN 应用取代…”；App 下次读取时发现 `tunnel_unreachable`，取回会话或要求重新登录并显示原因 | `TunnelStopReasonTests`；真机 D-L03 |
| 从设置关闭 | `userInitiated`：扩展结账，不发通知（用户自己刚做的）；App 打开后显示“VPN 已被系统设置或其它 VPN 应用关闭，本次流量已结算”。配置被禁用/移除、用户切换、App 更新各有单独文案 | 同上；真机 D-L04 |
| 重启手机 | 不启用 On-Demand、不启用始终开启：重启后不会自动连接。系统自行启动扩展（无票据）会以 `handoff_required` 失败退出 | 代码约束，真机 D-L06 |
| 通知权限被拒 | 连接成功后在连接页提示“通知已关闭：VPN 在后台因额度或套餐断开时，你不会收到提醒”；VPN 本身照常工作，原因在下次打开 App 时仍会显示 | `refusedNotificationsAreSaidUpFrontOnceConnected` |

## 3. 内存与采样

- 扩展每 5 s 读 `phys_footprint`：≥38 MiB 触发 Go 回收，≥44 MiB 记录压力（第 2 轮已有，初值）。**本轮没有真机数据，所以阈值与 Go 堆上限（22 MiB / GOGC 60）仍是初值，没有“定值”**。
- 每 60 s 写一行（只有数字）：

  ```text
  sample t=600 footprint=33554432 peak=37748736 fds=41 threads=19 goroutines=88 heap=12582912 sys=26214400 gc=140
  ```

- 汇总：`python3 scripts/ios/summarize-samples.py tunnel.log`。判定：峰值 ≥ 44 MiB、句柄比第二个样本多 8 个以上、goroutine 多 20 个以上即非零退出（FAIL）；没有采样行退出码 2（什么也没证明）。
- 电量：用 Xcode 的 Energy Log / Instruments（Energy Efficiency 模板）连接设备记录，不由脚本推断。

## 4. 已知限制

- **未启用 `includeAllNetworks`**：VPN 启动前已建立的长连接可能不会迁移进隧道，直到它们重连。影响程度未测（第 5 轮真机项）。
- 从设置关闭时系统不给 App 传原因，所以文案是“被系统设置或其它 VPN 应用关闭”这一类概括。
- 扩展被 jetsam 杀死时没有机会写日志、结账或通知：此时由服务器按 idle 超时回收，App 重开后因 `tunnel_unreachable` 要求重新登录。内存治理的目的就是不让这种情况发生，但其阈值待真机数据。
- 采样日志在设备上要通过 `log collect` 取回，扩展不会自己上传任何东西。

## 5. 真机验收步骤（需要付费 Apple 团队 + iPhone）

**前提（本轮 BLOCKED 的原因）**：免费 Apple ID 的 Personal Team **不能**签 Network Extension（Packet Tunnel），连 App 本身也一样，因为 App 与扩展的 entitlements 都包含 `packet-tunnel-provider`。所以“未签名 IPA + 自签”在没有付费团队时装不上能用的 VPN；侧载工具对此无能为力。需要：付费团队、在团队内注册 App 与 `.tunnel` 两个 Bundle ID 并开启 Network Extension，Xcode 自动签名，iPhone 开启开发者模式，参见 `docs/ios-tun-design.md` 第 9 节前置条件。

**测试前准备（Mac 与公司 VPN）**：真机用手机自己的 Wi-Fi/蜂窝，不受 Mac 公司 VPN 影响；但构建、从 Mac 取日志都在 Mac 上，公司 VPN 开着也行。**只有跑 Mac 上的网络类脚本（如 macOS 版 AsterLink、`--allow-network-changes`）前才需要断开公司 VPN**。账号：用有有效套餐的测试账号，管理员 `test@test.com` 无套餐不能连接。demo 后端须已部署带 `ios` 白名单的版本。

取日志（每项结束后）：

```bash
xcrun devicectl list devices
log collect --device-udid <UDID> --last 30m --output /tmp/asterlink.logarchive
log show /tmp/asterlink.logarchive --predicate 'subsystem == "asterlink.tunnel"' --style compact > /tmp/tunnel.log
python3 scripts/ios/summarize-samples.py /tmp/tunnel.log
```

| 项 | 步骤 | 通过要求 |
| --- | --- | --- |
| D-L01 | 连接后 Safari 打开一个站点，锁屏 10 分钟，解锁 | 不需要点连接；锁屏期间在节点/sink 侧看不到转发；日志有 `gate closed reason=…` 与 `gate reopened … closed_ms=…` |
| D-L02 | 连接后 Wi-Fi→蜂窝、再开关飞行模式 10 s | 每次先出现“重新验证/等待网络”，恢复后自动继续；无需手动重连 |
| D-L03 | 连接后在设置里启动另一个 VPN（如另一台设备上的配置或第二个 VPN App） | 本 VPN 断开；收到一条“已被其它 VPN 应用取代”通知；打开 App 显示原因；服务端 `/traffic` 尾账已结算 |
| D-L04 | 设置 → VPN → 关闭 AsterLink | 打开 App 显示“被系统设置…关闭，本次流量已结算”；可直接重新连接或重新登录 |
| D-L05 | 连接中，上滑划掉 App，等 1 分钟，再打开 | 设置里 VPN 仍为已连接；App 直接显示“已连接”，**没有登录页**；日志有 `foreground lease expired`；断开按钮能正常断开 |
| D-L06 | 连接中重启手机 | 开机后 VPN 为未连接，不自动连接；打开 App 需重新登录或用记住的密码 |
| D-MEM | 连接后同时：Safari 多标签 + 大文件下载 + 视频，持续 30 分钟 | 扩展没被杀（设置里一直是已连接）；汇总脚本退出码 0；记录 `footprint_peak_mib` |
| D-L07 | 连接/断开 20 次；另做 8 小时连接（可插电） | 汇总的 fds 与 goroutine 不增长；记录 `gate` 次数与最长关闸时长；不写猜测值 |
| 通知拒绝 | 设置 → AsterLink → 通知 → 关闭，再连接 | 连接页出现“通知已关闭…”提示，VPN 正常 |

把每项按 `case_id / expected / observed / PASS|FAIL|BLOCKED` 写进 `handoff_app_ios.md` 第 15 节，并附汇总脚本的 JSON。
