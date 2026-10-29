# macOS 客户端界面与菜单栏常驻

> 来源：`handoff_app_standardization.md` 第 3 轮（界面重设计、常驻）与第 6 轮（本文整理）。只描述展示层；Core、Helper、IPC、计费与退出结算全部沿用原实现（第 5.9 节的边界），改动前后用脚本化 Core 对比过命令序列。
> 面向开发者，所以这里可以写内部名称；**用户界面**里没有 Helper、内核、协议等字样，由 `scripts/test-app-copy.py` 守卫。

## 1. 代码结构（`apps/macos/Sources/AsterLinkApp`）

| 文件 | 内容 |
| --- | --- |
| `Theme.swift` | 全部颜色令牌（浅/深各一套，角色名与网站 `--al-*` 一一对应）。视图文件不得出现颜色字面量（`test-brand-assets.py` 检查） |
| `Components.swift` | 品牌标识（用 `frontend/brand/mark.svg` 的路径数据绘制）、卡片、按钮、提示条、状态胶囊、连接按钮、速率曲线 |
| `Presentation.swift` | 状态文字、主操作与启用条件、阻断原因、线路分组——纯函数，有单元测试 |
| `MainWindow.swift` | 登录后的外壳：侧栏（品牌、连接/线路/账户、状态胶囊、邮箱）+ 内容区 |
| `LoginView.swift` | 双栏登录页与“后台服务”引导卡 |
| `ConnectPage.swift` `LinesPage.swift` `AccountPage.swift` | 三个页面 |
| `MenuBarPanel.swift` | 菜单栏面板与首次关闭提示 |
| `AsterLinkApp.swift` | 单实例主窗口、`AppDelegate`（Dock 策略、再次打开、生命周期） |
| `PreviewRunner.swift` | DEBUG：设计预览（`AsterLink --preview <目录>`） |

`AsterLinkShared`（`SessionController` 等）没有界面代码，与 Windows 共用同一套语义。

## 2. 页面

- **登录**：左栏为网站登录页同款深色品牌区，右栏为登录卡；后台服务未启用时在登录卡上方显示引导卡（一句原因、一个“启用”按钮，批准后自动继续）。
- **连接**：156 点圆形连接按钮（未连接中性圆环、连接中品牌色圆弧转动、已连接品牌色实心加光晕、出错危险色圆环；“减少动态效果”时不转）；其下状态文字与说明；三张卡：当前线路、套餐、实时速率与 60 秒曲线。每种阻断（无套餐、未选线路、到期、流量用尽、连接失败）只给一条提示和一个下一步。
- **线路**：搜索、全部/专线/普通线路、刷新、按地区分组（组内保持服务器顺序）；已选项有图形加“已选”文字；已连接时换线先确认。
- **账户**：账户卡与网站入口、外观（跟随系统/浅色/深色）、后台常驻说明、后台服务（正常一行；异常为引导卡；“故障排查”折叠区含“移除后台服务”，二次确认）、关于（只显示“版本 x.y.z”）、复制诊断信息（六行，D9）、退出登录、退出 AsterLink。

窗口：单实例 `Window`（960×640，最小 820×560）；⌘, 打开主窗口的账户页。

## 3. 关闭即缩到菜单栏，不占 Dock

- 不使用 `LSUIElement`。主窗口打开时是普通应用（Dock 图标、应用菜单、⌘Tab）；**关闭主窗口后** `AppDelegate` 切到 `accessory` 策略：Dock 图标与 ⌘Tab 消失，菜单栏图标留下，进程、连接与用量照常。
- 恢复入口：菜单栏面板的“打开 AsterLink”、Finder/启动台再次打开（`applicationShouldHandleReopen`）、⌘,。三者都先切回普通模式、下一轮事件循环再 `openWindow` 并激活，所以不会出现第二个主窗口。
- 首次关闭时在菜单栏图标下方显示 6 秒的轻量提示（偏好键 `closeHintShown`；删除该键后会再出现一次）。
- 退出只有三个入口：菜单栏“退出”、⌘Q、账户页“退出 AsterLink”，都走原有“先断开并同步，再退出”的 `applicationShouldTerminate` 延迟流程。仅菜单栏模式下收到系统注销事件时同样先登出再退出，不阻塞注销。
- 生命周期挂钩（`launch`、后台服务批准监听、唤醒刷新）在 `AppDelegate`，与窗口是否打开无关。“应用变为活跃时刷新”改为“主窗口成为关键窗口时刷新”，打开菜单栏面板不触发任何刷新命令（命令序列对比已验证）。

## 4. 设计预览与验收

```sh
python3 scripts/test-native-macos.py --suite ui     # Debug 构建 + 28 个场景 PNG + 可见文字导出 + 文案守卫
```

输出在 `artifacts/native-macos/<run>-ui/`（`texts.json` 为每个场景的全部可见文字，由系统辅助功能接口读取）；CI 的 macos job 上传为 `macos-ui-preview`。预览用离屏窗口抓图，毛玻璃在图里是平面色，实机是系统材质。

## 5. 已知限制

- 真实后台服务批准、真实连接、注销/关机不被阻塞、VoiceOver 实际朗读：需要 Developer ID 包与真人真机，见需求方清单 U-24–U-26（`handoff_app_standardization.md` 第 12 节）。
- macOS 26 的图标“套框”问题需要 Icon Composer 资源，未做；按第 4.3 节的网格生成的 `.icns` 在本机 Dock 与 Finder 中显示正常。
