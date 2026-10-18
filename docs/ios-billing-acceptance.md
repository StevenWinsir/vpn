# iOS 连接闭环与计量验收（第 4 轮）

> 范围：登录 → 选线 → 连接（App → 扩展交接）→ 用量与扣量显示 → 自动停连与通知 → 断开交回。
> 计量口径不变（`handoff_app_ios.md` 第 9 节）：`charged_units = (上传 + 下载) × rate_permille`，1 unit = 1/1000 字节。

## 1. 连接路径（App 侧）

```text
点击连接
  → 未添加 VPN 配置？先弹说明页（VPNSetupSheet）；“继续”才触发系统的“添加 VPN 配置”弹窗
  → NetworkTunnelControl.connect
      安装/启用配置 → App Core ExportSession（Core 进入 released）→ startVPNTunnel(票据)
      → 扩展 AdoptSession → connect（闸门、首个 ACK、utun、Verify 第一段）
      → 系统报告 connected → TunnelBridge 改指扩展
      → read_snapshot 必须是 connected → Verify 第二段（App 内 204 探测，结果回报扩展）
      → 两段都通过才返回 .replied，界面才显示“已连接”
  → 任一步失败：交回会话并给出原因（拒绝 VPN 配置 / 路由探测失败 / 连接超时 / 扩展给出的 Core 原因）
```

- **TunnelBridge**：连接后所有 ipc 请求经 provider message 发给扩展 Core；无法到达时返回稳定码 `tunnel_unreachable`，控制器据此取回会话（成功则继续，服务器已撤销则静默重登一次）。
- **两个 Core 的 generation 各自计数**，所以会话换主后的第一份快照不做“旧代次”比较（`newOwner`）。
- **断开**：`client.disconnect()` 在扩展里关数据通路并结账 → `release` 取回票据 → 停隧道 → App Core `AdoptSession` → 桥回指 App。
- **切线路**：沿用第 3 轮确认框；先走上面的断开，再 `select_node`，保持未连接。
- **前后台**：scenePhase 变化发 `foreground` 提示给扩展。

## 2. 后台停连与通知

扩展在连接成功后每 5 s 读自己 Core 的快照（`TunnelWatch`）：数据通路已不在（额度用完、套餐到期、线路停用/改动、租约丢失）且不是 App 要求的停止（disconnect/logout/release/stopTunnel 先置位）时，**只一次**：
1. 发本地通知（固定标识 `asterlink.tunnel.ended`，正文只有原因文字，不含账号、线路、流量数字）；
2. `cancelTunnelWithError`，错误码与中文原因由 App 经 `fetchLastDisconnectError` 读回。

App 回到前台时，下一次请求发现隧道已不在，显示扩展给出的原因（没有时才显示“VPN 已被系统设置或其它 VPN 应用关闭”）。App 侧的通知与扩展的同标识，所以同一次停连只出现一条。通知权限在首次连接成功后才请求，说明页已提前交代；被拒绝时 VPN 照常工作。

## 3. 验收结果（2026-10-17，本机 macOS arm64，Xcode 27.0，iPhone 18 Pro 模拟器 iOS 27.0，公司 VPN 开启）

运行 `python3 scripts/test-native-ios.py --suite billing --harness both`（退出码 3 = 有 BLOCKED，没有 FAIL；不带 `--harness` 时 harness 用例为 NOT_RUN，CI 就是这样跑的，因为 CI 镜像没有 Postgres）：

| 项 | 环境 | 结果 |
| --- | --- | --- |
| B01 逐单位（Core 账本）：20 MiB 上 + 80 MiB 下 = 104,857,600 B，1× → 104,857,600,000 units，0.5× → 52,428,800,000 units | host（Go） | PASS（`TestExactIntegerChargeAtBothRates`） |
| B01 Swift 端：同一公式、溢出饱和、每个连接按自己的基线与倍率 | host（Swift） | PASS（`MeteringArithmeticTests`） |
| B02 丢 ACK 后原批重试只扣一次 | host（Go） | PASS（meter 与 managed 各一例） |
| H01 交接往返账本连续（20 MiB 基线、1000 B 上、80 MiB 下逐字节结清） | host（Go） | PASS（`TestHandoffRoundTrip`） |
| 交出/取回/换主/系统收走/Core 自行停连/拒绝弹窗/路由探测失败等 App 侧状态机 | host（Swift，两个脚本化 Core + 真桥） | PASS（`TunnelFlowTests`、`TunnelBridgeTests`、`TunnelWatchTests`） |
| 首次连接说明页、拒绝、被系统收走、探测失败四个界面 | simulator（XCUITest，固定状态） | PASS（ui 套件 48 例、50 张截图） |
| 回归：A01–A04 账号套件（真实 Core + Keychain + 隔离后端，请求现经 TunnelBridge） | simulator | PASS（19 项） |
| **B01 逐单位（真实 Mihomo + 真实后端）**：同一 20 MiB 上 + 80 MiB 下经节点到受控 sink，1×（专线 SS）与 0.5×（普通 VLESS）；sink 实收与客户端发送逐字节相等，服务器扣量 = (上 + 下) × 倍率，Core 确认计数 = 服务器计数 | harness（Go 宿主）与 harness（模拟器进程） | PASS（两处各 `B01-n1/n2`：1× 扣 104,858,514,000 units、0.5× 扣 52,429,257,000 units；上行比 sink 实收多 440 B、下行多 474 B，来自 HTTP 报文头与 Verify 探测，host 与模拟器两处完全一致） |
| **B02** 丢一次真实 ACK：同一批原样重发，只扣一次 | harness（两处） | PASS（`B02-retry`、`B02-once`；丢弃由后端世界注入，App 内没有开关） |
| **A04** 管理员改倍率 / 停用线路 / 套餐到期 / 额度耗尽：约 1 s 内自行停连，尾账按旧倍率结清，旧段旧倍率，之后不可再连 | harness（两处） | PASS（`A04-rate`、`-rebind`、`-disable`、`-disable-gone`、`-expire`、`-expire-blocked`、`-quota`、`-quota-blocked`） |
| **C01** 断开后立即重连 5 次，旧引擎的收尾不影响新连接 | harness（两处） | PASS（每次 4–13 ms，1.5 s 后仍为已连接） |
| **B04** node_reported：真实 NodeAgent 记账、客户端上报被拒（409 `metering_source_mismatch`，账本不动）、无 /traffic 上报 | harness（两处） | PASS |
| 运行输出与报告不含密码、节点秘密、sink 令牌 | harness | PASS（`log_clean`） |
| 负向对照：把引擎的上行计数故意减半 | harness（Go 宿主） | 如期 FAIL（`B01-*-sink-and-body`、`B01-*-ui`），已还原 |
| **device-billing**（真实 TUN 的 20+80 MiB） | device | **BLOCKED**：无付费 Apple 团队 / 签名身份 |
| **扩展侧的整条连接、通知真正在后台出现一次、fetchLastDisconnectError 能否带回码** | device | **BLOCKED**（模拟器不能运行 Packet Tunnel） |

## 3.1 进程内注入线束（规划 8.8）怎么工作

```text
python3 scripts/test-native-ios.py --suite billing --harness host|simulator|both
```

- **引擎**：`engine.Harness`（Go 标签 `asterlink_harness`）实现 `managed.Engine`，套用生产的 iOS 配置文档，但不开 TUN；受控客户端的 TCP 连接以回环对子形式交给 Mihomo 的 `tunnel.HandleTCPConn`（utun 监听器对每条流做的同一调用），经生产代理链到生产节点。计量读同一个“仅代理”计数器，所以被扣的就是穿过节点的字节。
- **场景**：`internal/harness` 用生产的 `managed.Manager`（ResumeGate，Core 自己的时钟）逐项移植 Android 的 BillingAcceptanceTest，后端是同一个 Gin/PostgreSQL 世界（`TestAndroidBillingWorld`，平台无关，每次运行新建，因为场景会改套餐和线路），节点是 `asterlink-testnode`，字节落到 `asterlink-trafficsink`，B04 用真实 NodeAgent（mTLS）。
- **两处运行**：`host` 是 Go 宿主测试；`simulator` 把同一份 Core 用 `-tags asterlink_harness` 绑成**仅模拟器切片、放在 `.runtime/ios/harness/` 的测试专用 xcframework**，再用 `swiftc` 编一个小命令行（`apps/ios/Harness/main.swift`），由 `simctl spawn` 在 iOS 模拟器进程里调用 `MobileHarnessRun`。
- **隔离**：这个框架和命令行都不被 Xcode 工程引用；foundation 套件检查生产 Core 的依赖里没有 `internal/harness`、带标签才有（所以检查会失败）、工程里没有 Harness、生产 xcframework 的二进制里没有 `HarnessRun`；simulator 套件在构建后再查一次。
- **它不覆盖的**（只能真机）：utun、系统 DNS、IPv6、扩展进程、通知。报告里这些用例的环境写 `harness`，从不写 `device`。
- **不覆盖 Swift 侧**：Swift 的 SessionController / 桥由 AsterLinkKit 的 63 个测试与 ui、account 套件覆盖；线束用 Go 直接驱动 Manager，所以“界面显示的每连接用量”在这里按同一公式在 Go 里核对（`B01-*-ui`），不是截图。

## 4. 已知限制与待真机核实

- **模拟器无法区分“用户拒绝系统弹窗”与“模拟器不支持 NE”**：两者都是 `NEVPNErrorDomain code=5`，所以模拟器上点“继续”会显示“需要允许添加 VPN 配置”。真机上的实际拒绝码待核实。
- 扩展能否在后台用 `UNUserNotificationCenter` 借用 App 的通知授权、`cancelTunnelWithError` 的 `userInfo` 能否原样回到 App，都只能真机核实；读不到时回落到通用文案，不会显示乱码。
- 扩展自行结束隧道后会撤销会话（`stopTunnel` 的既有行为），所以 App 回来后会用记住的密码静默重登一次；未记住密码则回到登录页。
- Verify 第二段依赖“App 流量会进入 utun”；若真机证明扩展流量也进隧道且第二段多余，可按规划 8.5 删除。
- `client_reported` 仍是开发计量，不解除生产保护。
