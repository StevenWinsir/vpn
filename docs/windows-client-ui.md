# Windows 客户端第 4 轮：正式界面、登录、记住密码、套餐与线路同步

> **2026-10-28 更新（handoff_app_standardization.md 第 4 轮）**：界面已重做为侧栏 + 内容区（连接/线路/账户），色板与图标改为网站品牌；“特权服务”统一称“后台服务”；**关闭窗口在任何状态下都只隐藏到托盘**（不再“未连接时关闭即退出”），托盘菜单为“状态行 / 打开 AsterLink / 连接·断开 / 退出”；设置页的系统事实与自检列表、版本中的渠道/内核/服务器已移除；诊断报告为六行（D9）；设计预览场景与可见文字导出见 `scripts/test-native-windows.py --suite ui`。下文第 2、4 节中与此不同的描述以本段为准，全文在该交接书第 6 轮统一改写。

状态：代码完成；CI 与 Windows 真机验收结果见 `handoff_app_windows.md` 的第 4 轮记录。**未在任何 Windows 真机上用真实账号验收之前，A01–A04、U01 都不得标 PASS。**

## 1. 结构：GUI 只渲染 Core 的快照

```text
WPF 视图 ──绑定──▶ AppViewModel ──▶ SessionController ──▶ CoreClient ──▶ ICoreTransport
(Views/*.xaml)     (展示字符串、命令)   (阶段、登录、轮询、    (类型化命令,    └ ServiceCoreTransport：已校验的
                                        记住密码、重新登录)     action_error)      服务管道 → AsterLink.Service → Core
```

- `AsterLink.Shared/Client/`（跨平台纯逻辑，macOS/Linux 上同样跑单测）：`SessionController`（阶段 Starting/SignedOut/SigningIn/SignedIn）、`CoreClient`、`ConnectionView`/`NodeList`/`ErrorText`/`DiagnosticsReport`、`Formatting`、`ICredentialStore`/`ILoginPreferences`/`IDeviceIdentity` 及基于文件的偏好与设备 ID。
- `AsterLink.Platform/`：`CredentialManagerStore`（Credential Manager）、`ServiceCoreTransport`（第 3 轮的 `ServiceConnection` 之上：先 hello，再转发命令）。
- `AsterLink.App/`：视图、主题、托盘、预览。视图没有任何授权判断；是否可以连接完全来自快照里的 `entitlement.account_authorized` 和选线状态。

GUI 不直接访问后端 API，也不保存 token：登录只发送一次 `login` 命令（`api_base`/`channel` 取自受保护的构建配置，服务端的 `CommandPolicy` 再核对一遍）。

## 2. 用户可见行为

| 页面 | 内容 |
| --- | --- |
| 登录（入口态） | 邮箱、密码、记住密码、登录；特权服务未就绪时显示唯一有用的一个动作（安装/修复/启动），完成后自动继续启动，不需要“刷新” |
| 连接 | 状态（文字 + 圆点）、当前线路、连接/断开/取消连接、套餐名称与到期、已用比例、确认余额；连接后显示上下行速率、曲线、本次累计、预估与确认余额、倍率 |
| 线路 | 搜索（名称/地区/类型）、全部/专线/普通线路筛选、倍率、已选/已校验标记；连接中选线会先确认“断开并结算” |
| 账户与设置 | 账户与套餐摘要、网站入口（购买/续费/控制台/订单）、浅色/深色/跟随系统、特权服务卡片、版本与脱敏诊断、退出登录（清除保存的密码）、退出 |

关键语义：

- **记住密码**只在用户勾选时写入当前 Windows 用户的 Credential Manager，目标名 `AsterLink/<API base>|<规范化邮箱>`；换 API 地址不会取到旧密码。保存失败只提示，不回落到文件、注册表或日志。下次启动只用它在线登录；**不保存 token**。服务器拒绝时（`invalid_credentials`）自动删除这条过期密码。显式“退出登录”删除密码和记住的邮箱；普通“退出”保留。
- **device_id**：每个 Windows 用户随机 UUID，保存在 `%LocalAppData%\AsterLink\device-id`，不取硬件信息，升级不变。
- **管理员也必须有权益**：`test@test.com` 无有效套餐时快照 `account_authorized=false`，“连接”按钮禁用并显示原因，控制器也不会发出 `connect`。
- **action_error**：命令返回 `ok=true` 但带 `action_error` 时展示其文字，状态以快照为准；传输失败与业务失败分开处理。
- **代次与旧响应**：只读轮询若与用户操作交错则丢弃；更旧代次的快照丢弃；`stale_generation` 会重新读取状态并提示；Busy 期间按钮去重（双击连接只发一条命令）。
- **目录同步**：Core 内有唯一的目录 watcher；GUI 每 2 秒（已连接 1 秒）读一次快照，窗口激活、系统唤醒时再 `refresh_catalog`（10 秒节流）。目录读取不续授权。
- **取消连接**：连接命令已在 Core 内，唯一可靠的回到确定状态的方法是结束会话（关闭管道 → 服务停止 Core 并结算/撤销），随后需要重新登录。界面如实写明。
- **托盘**：关闭窗口且处于连接中 → 隐藏到托盘并首次提示；否则关闭即退出。托盘菜单“退出（停止代理）”才会断开、结算并退出。单实例：第二次启动会唤起第一个窗口，而不是让服务结束第一个会话。
- **服务探测不打断会话**：已登录时设置页的服务卡片不再新开管道连接（第二个连接会让服务关闭当前会话）；已登录时不提供修复/重装。

## 3. 构建配置与安全约束（未变）

`apps/windows/.env` 仍是构建时输入，安装后位于受保护目录的 `build-config.json`。GUI 从自身所在安装树读取它；读不到时登录页给出明确提示并禁用登录，不会猜测服务器地址。诊断、横幅、日志都不含密码、令牌、YAML、节点地址。

## 4. 设计预览与证据

`AsterLink.exe --preview <目录>` 用脚本化的 Core（虚构数据，不连服务、不碰凭据库、不联网）把 22 个页面/状态渲染成 PNG：登录、服务缺失、需要修复、未选线、已选线、已连接（含曲线）、节点计量、无套餐（用户/管理员）、已到期、额度用尽、连接错误、最小窗口 800×560、线路页（空态/无匹配/已连接提示）、设置页、深色、200% 缩放。CI 的 `windows-ui-preview` 产物保留 14 天。

`--self-test` 现在还会载入四个页面并布局，XAML/资源错误会使该步骤失败。

## 5. 验收状态（见交接书矩阵）

| ID | 本轮证据 | 状态 |
| --- | --- | --- |
| A01 登录、保存密码、显式登出 | 控制器单测（假 Core + 内存凭据库）；Credential Manager 真实读写测试在 Windows CI | 真实账号 NOT_RUN |
| A02 管理员无套餐/到期/0 额度/设备上限 | 展示与禁用逻辑单测；预览截图 | 真实后端 NOT_RUN |
| A03 目录 watcher、429、恢复 | Core 的 watcher 沿用第 2/3 轮；GUI 侧轮询、旧代次丢弃有单测 | 真实后端 NOT_RUN |
| A04 改名/倍率/停用/删除/白名单同步 | 快照改变后的列表重建有单测 | 真实后台修改 NOT_RUN |
| U01 DPI/键盘/空态 | 预览图（100%/200%、最小窗口）；键盘可达性靠 RadioButton/Button 原生焦点 | 真机键盘、125%/150% NOT_RUN |

## 6. 已知限制

1. “连接”已接到真实 Core 与第 2 轮的 TUN 引擎：在真实 Windows 上点击会真的创建 TUN。完整连接闭环、计量扣量属第 5 轮，不属本轮验收。
2. 取消连接会结束会话（需重登）。
3. 未接 `SERVICE_CONTROL_SESSIONCHANGE`（第 3 轮遗留）。
4. 没有延迟测速（按交接书首版不做）。
