# AsterLink iOS 界面与账号层（第 3 轮）

> 范围：登录、记住密码、套餐与线路同步、正式三页界面。连接按钮在第 3 轮只走到 App 自己的 Core（如实返回 `tun_unavailable`）；第 4 轮起接入首次配置说明页、与扩展的交接和两段 Verify，见 `docs/ios-billing-acceptance.md`。

## 1. 结构

```text
AsterLinkKit（Swift 包，宿主 `swift test` 可测）
  Snapshot / SessionState / ConnectionView / Formatting / ErrorText   快照解码与展示规则（Int64，不经 Double）
  CoreClient                                                         ipc-v1 的类型化命令；校验 request_id 与 generation
  SessionController (@MainActor @Observable)                          Android 版的移植：见 §2
  Ports                                                              CoreHost / CredentialStore / LoginPreferences / DeviceIDProvider / TunnelControl
AsterLink（App）
  Platform.swift       AppConfig（Info.plist→env-v1 校验）、MobileTransport、Keychain 密码库、UserDefaults 偏好、设备 ID
  AppModel / RootView  启动 Core→登录页或三标签；外观；网站内置浏览器（SFSafariViewController）
  LoginView / ConnectView / LinesView / AccountView
  UIFixtures.swift     仅 Debug：截图套件用的“预置状态”（Release 不编译）
AsterLinkTests/AccountAcceptanceTests   真实 Core + Keychain + 隔离后端
AsterLinkUITests/ScreenshotTests        44 个界面用例
```

UI 只渲染 Core 的快照；授权结论来自 `entitlement`，没有任何按角色放行的逻辑（管理员无套餐照样“无法连接”）。

## 2. SessionController 与 Android 一致的语义

操作代次（竞争的轮询结果丢弃）、旧 generation 快照丢弃、`action_error`、`stale_generation` 重读、`native_session_*` 过期后**只静默重登一次**（密码被拒不重试）、内核丢失即结束会话、连接 1 s / 其它 2 s 轮询、回前台 10 s 节流刷新目录、取消连接＝结束整个会话、停连原因（额度/套餐/线路）经提醒接口给出。
iOS 的差异：没有 Keystore 而用 Keychain；回前台来自 `scenePhase`；不存在 VPN 授权弹窗这一步（第 4 轮的“添加 VPN 配置”引导）。

## 3. 记住密码与设备 ID

- 仅在勾选“记住密码”时保存。Keychain 通用密码项：`service = <bundle>.credentials`，`account = SHA-256(API base + 小写邮箱)`，`kSecAttrAccessibleWhenUnlockedThisDeviceOnly`，不同步 iCloud；Keychain 里不出现邮箱明文。
- 服务器返回 `invalid_credentials` 删除该项；“退出登录”删除密码和记住的邮箱；保存失败只提示“本次未记住密码”，**不降级为明文**。
- 不保存 token。重开 App 用记住的密码在线登录。
- device_id：随机 UUID，Keychain（本机，AfterFirstUnlock）。**注意**：未签名的模拟器二进制没有 application-identifier，Keychain 返回 `-34018`，所以 `--suite account` 用 ad-hoc 签名（`CODE_SIGN_IDENTITY=-`，不需要 Apple 团队）。无签名的纯构建（CI）里“记住密码”会提示失败而不是崩溃。

## 4. 页面（第 10 节规范的落地）

| 页面 | 内容 |
| --- | --- |
| 登录 | 品牌、邮箱（`username`）、密码（`password`，可显隐）、记住密码（整行可点，`isToggle`）、登录、注册账号（网站 `/register`）、版本/Core/Mihomo。字段错误显示在字段下，其它错误为顶部横幅 |
| 连接 | 168 pt 主按钮（外环＋文字＝状态，减弱动态效果时不转）、状态词、详情、当前线路卡（专线/普通＋计费倍率）、速率与 60 秒曲线（Swift Charts）、本次物理/计费用量、套餐卡（确认余额；预估另起一行、`pending` 提示） |
| 线路 | 搜索、全部/专线/普通、按地区分组、选中对勾、节点计量小字、下拉刷新；已连接时切换先确认“将断开并结算” |
| 我的 | 账户卡、套餐卡（到期、剩余天数、余额）、外观、通知与 VPN 配置状态、关于（版本/Core/Mihomo）、开源许可、复制脱敏诊断、退出登录（二次确认） |

空态：无套餐（管理员也一样）、到期、额度 0、设备上限、未选线、正在同步、无线路、搜索无结果、API 离线、需要更新、被动停连原因。
**购买入口**：按第 14 节默认 `none`——只显示“请到官网管理套餐”，没有购买/续费链接。网站目前没有 `/privacy`、`/terms`、找回密码页面，所以界面不放这些入口（第 6 轮门槛）。
**未做（按计划留给后续）**：首次“添加 VPN 配置”引导页（第 4 轮，与连接一起）、英文本地化与正式图标/启动画面（第 6 轮；现为矢量占位）。

## 5. 如何验证

```sh
python3 scripts/test-native-ios.py --suite unit      # Go 宿主测试 + AsterLinkKit（含 SessionController 的脚本化测试）
python3 scripts/test-native-ios.py --suite ui        # 44 个界面用例（45 张截图），截图在 artifacts/native-ios/ui/screenshots/
python3 scripts/test-native-ios.py --suite account   # 真实 Core + Keychain + 隔离 Gin/PostgreSQL（需要 go、initdb、pg_ctl）
```

`ui` 套件的每个用例先断言状态再截图，不是“渲染了就算过”。`account` 套件把 Android 的 `AccountAcceptanceTest` 逐项移植（后端世界 `TestClientAccountWorld` 与 Android 的 `TestAndroidAccountWorld` 共用同一实现）。

## 6. 已知限制

- 连接按钮在 iOS 上返回“此设备暂时无法建立系统隧道”：这是第 3 轮的真实行为，不是故障。
- 没有 iPhone SE（375 pt）模拟器镜像，小屏用例记 NOT_RUN；动态字体最大测到 AX3。
- 用 `ASTER_UI_FIXTURE` 的截图不经过 Core；Core 与后端的行为由 `account` 套件证明。
