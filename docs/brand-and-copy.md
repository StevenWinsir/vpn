# 品牌资产、色板、文案守卫与发布去敏（四个客户端）

> 来源：`handoff_app_standardization.md`（2026-10-28，六轮）。本文是日常维护手册；决策与取舍在该交接书第 3 节（D1–D17）。

## 1. 唯一来源

手工维护的只有 `frontend/brand/mark.svg`（网站 favicon、网站 BrandMark 与四个 APP 的图标共用）。颜色与间距令牌的唯一来源是 `frontend/src/app/globals.css` 的 `--al-*`。

更换 Logo 或改色后：

```sh
cd frontend
npm run brand:icons && npm run brand:apps     # 需要本机 Chrome：PLAYWRIGHT_CHROME_PATH="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
cd .. && python3 scripts/test-brand-assets.py # 全部 PASS 后提交变更的文件
```

`brand:apps` 生成并入库（CI 不重新生成），同时写 `apps/brand-manifest.json`（源文件与每个输出的 SHA-256、尺寸）：

| 平台 | 输出 |
| --- | --- |
| macOS | `apps/macos/Branding/AppIcon.icns`（1024 画布内 824 圆角方块 + 投影）、菜单栏三态模板图（未连接/已连接/出错，18 点 1x/2x） |
| Windows | `AsterLink.App/Assets/AsterLink.ico`（9 个尺寸）、`tray-{idle,live,error}.ico`、`Packaging/wizard-small-{55,110}.bmp` |
| iOS | `Assets.xcassets` 的 AppIcon 浅色/深色/着色、LaunchMark |
| Android | 自适应图标前景与单色层、`ic_notification`（通知与快捷开关） |

应用内的品牌标识：iOS/Android 用同一份生成资源；macOS（`Components.swift`）与 Windows（`Theme/Brand.xaml`）直接用 `mark.svg` 的路径数据绘制。

`scripts/test-brand-assets.py`（标准库，在 CI 的 native-core job 运行）证明：源文件哈希与清单一致、每个输出未被手改且结构合法、各平台确实引用了这些资源、**四个平台的色板与网站令牌逐项一致**（浅色与深色）、关键文字/底色对比度 ≥ 4.5、视图文件里没有颜色字面量。网站改任何一个映射令牌而 APP 不跟，测试立即失败。

## 2. 文案守卫

规范见交接书第 7.1 节（不出现内核、隧道、特权服务、协议、计量、结算、管理员、开源许可、GiB 等；桌面端另不出现 VPN）。

```sh
python3 scripts/test-app-copy.py                 # 静态扫描（CI native-core）
python3 scripts/test-app-copy.py <公开发布目录>   # 另检查其中的文件名
```

扫描对象：Core `messages.go`；iOS/AsterLinkKit/隧道扩展与 macOS 的中文及视图英文字面量；Android 中英文 `strings.xml` 与 Kotlin 中文字面量；Windows XAML 的可见属性与 C# 中文字面量、安装程序消息与语言文件；macOS DMG 的“安装说明.txt”与“卸载 AsterLink.command”（含 echo 的英文句子）；系统可见的元数据（iOS 与 macOS 的显示名、Windows 服务显示名与说明、安装程序产品与发布者、.NET 程序集公司与产品）；公开发布目录的文件名（`release_scrub.public_problems` 对四个平台都调用）。

运行时守卫使用同一份词表：iOS UI 测试（`CopyGuard.swift`）、Android 截图测试（`CopyGuard.kt`）、macOS 与 Windows 设计预览导出的可见文字，以及 `release_suite.py` R-DMG 对挂载后 DMG 内文本的检查。静态脚本会比对 iOS/Android 两份词表是否与自身一致，改词表先改 `scripts/test-app-copy.py`。

**豁免**：该行或上一行写 `copy-guard` 加原因。第 6 轮评审后全部豁免如下（脚本报 6 条被豁免的字面量）：

| 位置 | 原因 |
| --- | --- |
| macOS `LoginView.swift`、`AccountPage.swift`，iOS `LoginView.swift`，Windows `AppViewModel.DevelopmentMarker`，Android `login_dev_channel`（中、英） | D8：只有开发渠道构建才显示的中性“开发版”标记；release 构建绝不显示 |
| Android `role_admin`（中、英） | D13：管理员账号自身的标记，普通用户看不到 |

“管理员权限/管理员账号”不需要豁免：词表对这两个词组做了放行（Windows 的“需要管理员权限”是用户账户控制的通用说法）。

整文件豁免只有 DEBUG 专用文件（iOS `TunnelDebugView.swift`、`UIFixtures.swift`，macOS `DirectCoreTransport.swift`、`PreviewRunner.swift`），发布审计证明它们不进 Release 产物。无任何开源/许可相关豁免（D10）。

已知且接受的残留（不是用户界面文字）：macOS Helper 的文件名 `AsterLinkHelper`、二进制与组件文件名（D17）；Core 二进制里的 Go 包路径（D16，见下）。

## 3. 发布去敏

`scripts/release_scrub.py` 与 `scripts/release-scrub-baseline.json`：公开发布目录只含安装包与只列安装包的 `SHA256SUMS`；许可、`SOURCE.txt`、源码归档、组件清单、发布信息在同级 `<目录>-private`（Windows 为 `installer-private`）；应用包与安装包内的文本文件不得出现内核与许可标记，二进制只能带基线登记的残留，提交号与构建机路径任何位置都不许出现。各平台发布套件都调用它；`python3 scripts/release_scrub.py --self-test` 在 CI 里证明审计能发现植入的泄露。

## 4. 四个平台界面对照（第 6 轮评审）

对照图（网站、macOS、Windows、iOS、Android 的登录、连接/总览、线路、账户，浅色与深色）与逐项结论见 `handoff_app_standardization.md` 第 14 节第 6 轮记录。术语约定：APP 用“线路”“账户”“线路倍率 N×”“专线/普通线路”，网站导航沿用“节点列表”“账号设置”（网站不在本期范围，差异已登记）。
