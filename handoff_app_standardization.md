# AsterLink 客户端：品牌统一、桌面端美化与后台常驻、文案商业化 技术规划交接书

> 文件：handoff_app_standardization.md
> 编写日期：2026-10-28
> 代码基线：项目 /Users/stevenlee/Desktop/vpn，main，HEAD 02d55a2（首页首屏改版合并后）。以本次读取的 apps/macos、apps/windows、apps/ios、apps/android 全部界面与展示层源码，native/core/internal/apiclient/messages.go，frontend/brand、frontend/scripts/brand-icons.mjs、frontend/src/app/globals.css，scripts/build-native-macos.py、scripts/package-native-macos.py、scripts/release_suite.py，apps/windows/Packaging/setup.iss 以及 .github/workflows 为准。
> 文档性质：实施规划与验收约定。本文不代表任何功能已开发或已测试，全文不含代码示例。

## 1. 目标、范围与轮次总览

需求方的三项要求与本文章节对应如下：

| 编号 | 要求 | 章节 | 轮次 |
| --- | --- | --- | --- |
| A1 | 四个平台 APP 的软件图标、启动画面、托盘/菜单栏图标、应用内品牌标识与网站 favicon 高度统一（macOS 当前图标为空白） | 第 4 节 | 第 1 轮（图标）、第 2 轮（移动端应用内色板） |
| A2a | macOS、Windows 客户端界面重新设计，达到商业发布标准；只改界面，底层功能不动 | 第 5、6 节 | 第 3、4 轮 |
| A2b | 桌面端后台常驻：macOS 点关闭后缩到右上角菜单栏、不占 Dock；Windows 点关闭后缩到右下角系统托盘 | 第 5.5、6.4 节 | 第 3、4 轮 |
| A3 | 四个平台所有用户可见文案去掉开发期信息、内核与底层技术表述，统一为商业化用语 | 第 7 节 | 第 2 轮（共享层与移动端）、第 3、4 轮（桌面端）、第 6 轮（全覆盖守卫） |
| A4 | 最终交付给用户的所有安装包与发布文件中不得包含任何底层内核信息（许可文本、源码说明、组件清单、内核版本与提交号等）；这些资料由需求方以纸质形式提供（2026-10-28 追加） | 第 8 节 | 第 5 轮 |

采用 **6 轮**实施。每轮一个 worktree 分支和一个 PR，分支命名为 `worktree-appstd-roundN-<主题>`，每轮的记录追加到本文第 14 节。轮次顺序的考虑：先建立唯一的品牌资产管线（后续每一轮都要用到同一套图标与色板）；再处理跨平台共享的文案层和改动面较小的移动端（为桌面端重设计提供已定稿的文案与色板）；然后分别重做 macOS 与 Windows（两轮互不依赖，但共用第 5.1 节的桌面设计语言，先做 macOS 定下视觉基准）；界面与文案定稿后再统一清理四个平台的发布包（第 5 轮，避免前几轮的打包改动与之冲突）；最后做全平台回归与守卫全覆盖。

| 轮次 | 可验收结果 | 依赖 |
| --- | --- | --- |
| 第 1 轮 | 品牌资产生成管线；四平台软件图标、启动画面、托盘/菜单栏/通知/快捷开关图标全部来自网站同一源文件；macOS 图标不再空白；资产一致性测试进 CI | 无 |
| 第 2 轮 | 色板一致性测试；iOS、Android 应用内色板与品牌标识换成网站品牌；共享错误文案（Go Core）与 iOS、Android 全部文案商业化；跨平台文案守卫工具（先覆盖 Core 与移动端） | 第 1 轮 |
| 第 3 轮 | macOS 全新界面（主窗口、菜单栏面板、首次授权引导）；关闭即缩到菜单栏且不占 Dock；macOS 文案与 DMG 内说明商业化；macOS 设计预览出图 | 第 1、2 轮 |
| 第 4 轮 | Windows 全新界面；关闭即缩到系统托盘；托盘菜单与通知；Windows 文案与安装程序商业化；设计预览更新 | 第 1、2 轮（建议在第 3 轮之后，复用其视觉基准） |
| 第 5 轮 | 发布包去敏：许可、源码说明、组件清单、发布信息移出公开发布目录与应用包；停止向发布构建写入内核提交号；四个平台的发布审计改为“公开目录与应用包内不得出现” | 前 4 轮 |
| 第 6 轮 | 文案守卫覆盖四平台、安装包与发布目录；跨平台视觉一致性评审；文档更新；需求方实测清单 | 前 5 轮 |

本期不做：
- 任何底层功能改动：Go Core 的状态机、计费与上报、授权、线路选择逻辑、IPC 协议、macOS Helper 与 Windows 服务、凭据存储、轮询与刷新时机、退出时的结算流程，全部保持原样。Core 中唯一改动的是错误码对应的中文提示文字（第 7.2 节），错误码本身、触发条件与处理方式不变。第 5 轮只改打包位置与发布构建参数（第 8 节），不改任何 Core 或客户端代码逻辑。
- iOS、Android 的页面结构与交互重设计（只换图标、色板、品牌标识与文案）。
- 开机自启动、自动更新、托盘/菜单栏以外的新功能。
- 网站前端与后端（网站已在 handoff_ui.md 中完成；后端返回的提示文字客户端从不显示，见第 2 节）。
- 更换品牌标识本身的造型（沿用网站 frontend/brand/mark.svg，见 D1）。
- 开源许可声明与源码提供：按需求方决定（D10、D15）不在应用内、安装包内或公开发布目录中提供，由需求方以纸质说明书寄送用户；本计划只负责把这些资料从交付物中移出，并保存在不对外发布的内部目录中供需求方编写纸质材料。

## 2. 已核实的现状与必须衔接的位置

| 位置 | 当前事实 | 对本次的影响 |
| --- | --- | --- |
| frontend/brand/mark.svg | 网站唯一品牌源：64 视图框，品牌绿 #0d7b61 圆角方块（圆角 15）+ 白色描边盾牌（描边 4.2，圆头圆角）+ 中心圆点；frontend/scripts/brand-icons.mjs 用 Playwright 浏览器把它渲染成 favicon、apple-icon、manifest 图标和 BrandMark 组件，生成结果入库，scripts/theme.test.mjs 校验一致 | 四个 APP 的图标必须从这同一个文件生成，复用其渲染与 ICO 打包能力 |
| frontend/src/app/globals.css 的 tokens 块 | 网站浅色/深色两套 --al-* 令牌：品牌 #0d7b61（深色下文字/图标用 #3dc89a），背景 #f7f8fa / #121a18，卡片 #ffffff / #19221f，正文 #172d2b / #dbe5e2，警示与危险色等 | APP 色板的唯一依据（第 4.2 节） |
| macOS 打包 scripts/build-native-macos.py | 手写 Info.plist，没有 CFBundleIconFile，Resources 中没有任何图标文件 → Finder、Dock、DMG 中都是空白通用图标 | 第 1 轮补图标文件与 Info.plist 键；release_suite.py 增加图标检查 |
| macOS apps/macos/Sources/AsterLinkApp | SwiftUI，WindowGroup 主窗口 480 宽单列（GroupBox 堆叠）+ 设置弹窗；MenuBarExtra 使用系统符号 bolt.shield，菜单只有邮箱、打开、退出；applicationShouldTerminateAfterLastWindowClosed 已返回 false（关窗不退出），但 Dock 图标始终存在；session.launch 与 Helper 注册监听挂在主窗口视图的生命周期上 | 第 3 轮：重做界面、改为动态 Dock 策略、把生命周期挂钩移出窗口 |
| macOS 设置弹窗 | 显示“特权服务（Helper）”“握手”“重新安装/移除特权服务”“Helper x · 协议 v · 内核运行”“应用 · 通道 · 内核 · Mihomo · 服务器” | 第 7.3 节逐项处理 |
| Windows apps/windows/AsterLink.App | WPF（.NET 10，Win10 2004 起），侧栏三页（连接/线路/账户与设置），浅/深/跟随系统三档主题已存在；蓝色强调色 #2F6FEB；窗口图标与托盘图标在运行时画成“彩色圆片 + A”，exe 没有 ApplicationIcon，安装程序没有 SetupIconFile | 第 1 轮换图标；第 4 轮重做界面 |
| Windows 关闭逻辑 App.xaml.cs | 仅当连接活跃（IsLive）时关闭才隐藏到托盘，否则直接退出；首次隐藏弹一次气泡（TrayNoticeShown 存在本地偏好） | 第 4 轮改为任何状态下关闭都进托盘 |
| Windows 设置页 | 显示操作系统/架构/运行时/原生 x64 事实、自检“通过/失败”列表、“特权服务”、“渠道”、“内核”、“服务器”、错误代码 | 第 7.3 节逐项处理 |
| Windows 设计预览 PreviewRunner | `--preview <目录>` 用脚本化 Core 把每个页面/状态/深色/200% 渲染成 PNG，CI 上传为 windows-ui-preview 工件；`--self-test` 逐页布局检查 | 第 4 轮更新场景并增加可见文字导出，作为验收证据 |
| iOS | 图标为蓝色电源键（浅/深/着色三张 1024 图）；BrandMark 为蓝底 + 系统符号 power；AccentColor、LaunchBackground、Theme.swift 均为蓝色系；UI 测试 ScreenshotTests 在 CI 中逐屏断言并截图 | 第 1 轮换图标与启动图；第 2 轮换色板与文案 |
| Android | 自适应图标前景是“四角星”占位矢量，背景 #2F6FEB，monochrome 复用前景；通知小图标与快捷开关图标是 ic_vpn_key；启动画面用前景图；Compose 主题 Theme.kt 蓝色系；strings.xml 有中文与英文两套；Roborazzi 截图测试在 CI 中运行 | 同 iOS |
| Go Core apiclient/messages.go | 所有错误与授权原因的中文提示由 Core 按错误码生成，四个客户端直接显示；后端返回的 message 字段客户端从不显示 | 共享文案只需在这一处改写（第 7.2 节）；manager_test、connection_test、contract_v3_test 中有子串断言需同步 |
| 各平台本地文案 | macOS 直接写在 Views.swift / HelperClient / SessionController；Windows 写在 XAML、AppViewModel、Presentation.cs、ServiceHealth.cs、ServicePanel.cs、SessionController.cs；iOS 写在视图与 AsterLinkKit/ErrorText.swift；Android 集中在 strings.xml（中、英） | 第 7.3 节逐平台清单 |
| 单位 | 四个 APP 均显示 GiB，并带“1 GiB = 2³⁰ 字节”说明；网站用户页已统一为 GB（基数 1024³，handoff_ui.md D6） | APP 跟随网站改为 GB，换算基数不变 |
| 主工作区未提交改动 | main 工作区有需求方自己的未提交改动：apps/ios 的 project.pbxproj 与 App-Info.plist（键顺序重排）、删除的 HANDOFF.md、两份未跟踪文档 | worktree 从 HEAD 建立，不受影响；任何一轮都不得覆盖或提交这些改动 |
| handoff_bandwidth.md | 已规划、未开工，改动集中在 Go Core 的出站限速与合同头 | 与本文唯一可能的交集是 native/core 目录，但不涉及 messages.go；两者谁后合并谁 rebase |

## 3. 关键决策（默认值，需求方可在第 1 轮开工前推翻）

| 编号 | 决策 | 理由 |
| --- | --- | --- |
| D1 | 品牌图形完全沿用网站 frontend/brand/mark.svg（品牌绿底板 + 白色盾牌），这是全项目唯一的手工维护源文件；各平台只改变“底板形状与留白”，不改图形本身；只有图形没有底板的版本（菜单栏、通知、单色图标用）由生成脚本从同一文件中剥离底板得到，不另存手工文件 | 需求原文“和 next.js 前端的 icon 高度统一”；以后换 Logo 只需替换一个文件并重新生成 |
| D2 | APP 色板以网站 globals.css 的令牌为唯一依据（第 4.2 节映射表），四个客户端原有的蓝色 #2F6FEB 系全部退役；连接成功状态色使用品牌绿（与网站“成功”语义一致），状态始终同时用文字或图形表达，不只靠颜色 | 统一品牌；网站已经过对比度评审 |
| D3 | 不引入任何第三方界面库：macOS 只用 SwiftUI/AppKit，Windows 只用 WPF 自带控件与自写样式；Windows 界面图标使用与网站同源的 Tabler 图形（MIT 许可）转成矢量几何资源；macOS 使用系统 SF Symbols | Windows 有锁定依赖与签名清单，macOS SwiftPM 当前零依赖；网站同样使用 Tabler，Windows 图形语言与网站一致，macOS 与 iOS 保持平台原生 |
| D4 | 桌面端关闭窗口在任何状态下（已连接、未连接、未登录、启动失败）都只隐藏到菜单栏/托盘，进程继续运行；真正退出只有三个入口：菜单栏/托盘菜单的“退出”、macOS 的 ⌘Q（应用菜单可见时）、设置页的“退出 AsterLink”；退出仍走现有的“断开并结算后再退出”流程 | 需求原文；保持现有退出流程不变 |
| D5 | macOS 不使用 LSUIElement（永不显示 Dock），而是动态切换：主窗口打开时为普通应用（显示 Dock 图标、应用菜单、可 ⌘Tab），主窗口关闭后切换为仅菜单栏模式（Dock 图标消失）；从菜单栏或再次打开应用时恢复 | 满足“关闭后不占 Dock”，同时保留窗口打开时用户熟悉的 Dock、菜单与快捷键；需求方若希望任何时候都不出现在 Dock，可改为 LSUIElement，界面方案不变 |
| D6 | 首次关闭窗口时各给一次非阻塞提示（Windows 托盘气泡；macOS 菜单栏图标下方的轻量浮层），说明应用仍在后台运行以及从哪里重新打开；之后不再提示。不在关闭时弹出确认框 | 防止用户误以为程序已退出；不打断关闭动作 |
| D7 | 文案规范沿用网站 handoff_ui.md 的 D4–D6：人称用“你”；品牌表述用“安全连接 / 网络加速”，不用“翻墙”“科学上网”“代理”；“VPN”只出现在系统强制的场景（iOS/Android 添加 VPN 配置、系统 VPN 设置）；流量单位统一 GB（基数 1024³）；线路类型统一为“专线 / 普通线路”；倍率显示与网站一致为“N×”，称“线路倍率” | 网站与四个客户端说同一种话 |
| D8 | 版本信息只显示“版本 x.y.z”；不显示内核版本、Mihomo 提交、构建渠道、服务器地址、协议版本。development 渠道的构建允许在“关于”里显示一个中性的“开发版”标记（文案守卫以豁免标记放行），release 渠道构建绝不显示 | 生产用户看不到开发信息；开发与测试仍能区分构建 |
| D9 | “复制诊断信息”保留，但报告内容精简为只含：应用版本、操作系统版本、连接状态、最近一次错误码与时间。删除内核/Core 版本、Mihomo 提交、协议版本、服务器主机名、构建渠道、代次、闸门、隧道健康度、配置就绪等全部底层字段；字段名用中性中文（如“应用版本”“连接状态”“错误码”）。四个平台的诊断报告格式保持一致 | 用户会把报告粘贴给客服，粘贴出来的文字同样是用户可见内容，必须遵守“不出现任何底层信息”；错误码是客服排障的最少必要信息 |
| D10 | 四个客户端内**不提供任何开源许可或第三方组件信息**：删除 iOS 的“开源许可与源代码”入口及其页面、Android 的“开源许可与源代码”条目、说明文字与“打开下载页”按钮；macOS、Windows 不新增此类入口。应用内任何位置不出现 Mihomo、Clash、sing-tun、GPL、组件名称、源码仓库地址 | 需求方要求（2026-10-28）：出于软件安全考量，应用内不得显示任何底层信息；开源许可声明由需求方在随附的纸质说明书中提供，不在本计划范围内 |
| D15 | 所有交付给用户的文件（应用包、安装包、公开发布目录）中不包含任何许可文本、源码说明、源码归档、组件清单、发布信息与内核提交号：这些文件内容不变，统一移入各平台不对外发布的 release-private 目录；应用包内的同类文件与键直接删除；各平台发布审计由“必须存在”翻转为“公开交付物中不得出现、内部目录中必须完整”（第 8 节） | 需求方要求（2026-10-28）：出于安全考量，最终打包中不得包含任何底层内核信息；许可与源码资料由需求方以纸质形式寄送用户，内部目录为其编写纸质材料与追溯提供依据 |
| D16 | 编译后的 Core 二进制里由 Go 运行时必需的函数名表与模块信息带出的包路径（含上游组件名）不做混淆，作为已知残留记录在案；发布构建统一去除调试符号与版本控制信息，Windows 的 Core 构建补上去除调试符号的选项 | 彻底消除需要引入代码混淆工具链，会改变构建工具链并可能影响移动端绑定、反射与崩溃分析，属于底层构建变更；需求方需要时单独立项评估 |
| D17 | 二进制与组件的文件名（asterlink-core、AsterCore、Windows 安装目录下的 core 目录等）保持不变；系统网络适配器名称本来就是 AsterLink | 这些名称不含上游组件名；改名会牵动 macOS Helper 与 Windows 服务的二进制校验、安装布局与完整性清单，属于底层功能 |
| D11 | macOS Helper 与 Windows 系统服务在界面上统一称为“后台服务”（macOS 系统设置里该开关本身就叫“允许在后台”）；正常时只显示一行“后台服务运行正常”或完全不显示，异常时显示一句原因和一个修复按钮；“握手”“刷新”重复按钮、协议版本、错误代码从界面移除，“移除后台服务”收进“故障排查”折叠区并二次确认 | 去掉开发调试入口，同时不丢失用户自助修复能力（修复动作调用的仍是现有函数） |
| D12 | 计量来源（节点实测/客户端上报）相关的标签与说明从所有界面移除；计费行为与数据完全不变 | 这是内部运营信息；网站用户页同样不展示 |
| D13 | 永远不对普通用户提“管理员”：如“管理员尚未配置可用线路”改为“暂无可用线路”；管理员账号自身的“管理员账号”标记保留 | 商业口径 |
| D14 | 主题：Windows、iOS、Android 已有“跟随系统/浅色/深色”三档，macOS 新增同样的三档（本地偏好，默认跟随系统） | 四端与网站一致 |

## 4. 要求 A1：品牌与图标统一

### 4.1 唯一来源与生成管线
- 手工维护的只有 frontend/brand/mark.svg 一个文件。新增一个仅供开发使用的生成脚本（放在 frontend/scripts，与网站的 brand:icons 并列，例如 npm 脚本名 brand:apps），复用 brand-icons.mjs 已导出的解析、浏览器渲染与 ICO 打包能力，不新增 npm 依赖。
- 脚本先从源文件得到两种形态：带底板的“完整标识”与剥离底板、只保留盾牌图形的“图形”。剥离规则与网站脚本对源文件结构的假设一致（第一个元素是底板），结构不符时直接报错。
- 输出全部入库，CI 不重新生成。脚本同时写一份生成清单（列出每个输出文件、尺寸、源文件的 SHA-256），放在 apps 下一个公共位置（例如 apps/brand-manifest.json）。
- 新增一个 Python 一致性检查脚本（scripts/test-brand-assets.py，只用标准库，可在 Linux 上跑），加入 ci.yml 的 native-core job：源文件哈希与清单一致；清单里每个文件都存在、尺寸正确；PNG 头、ICO 目录、ICNS 目录结构合法；iOS 浅色图标无透明通道；Android 矢量文件可解析且只引用允许的颜色。第 2 轮再加入色板一致性检查（第 4.2 节）。
- 小尺寸可读性：网站 favicon 的 16 像素版本已证明当前描边粗细可用；生成脚本对 16、20、24 像素版本沿用网站同样的处理，不另做手工像素修整。如评审认为托盘 16 像素过细，只允许在生成脚本里对小尺寸统一加粗描边，不允许出现第二份手工源文件。

### 4.2 色板映射（第 2 轮加入一致性测试）
APP 的颜色角色与网站令牌一一对应，浅色、深色各取对应值：

| APP 角色 | 网站令牌 | 用途 |
| --- | --- | --- |
| 品牌强调 brand | --al-brand | 链接、选中项图标、强调文字（深色下为 #3dc89a） |
| 主按钮底 brandFill / 悬停 | --al-brand-fill / --al-brand-fill-hover | 主操作按钮、连接按钮已连接态 |
| 主按钮文字 onBrand | --al-on-brand | |
| 品牌浅底 brandSoft / brandTint | --al-brand-soft / --al-brand-tint | 选中行、标签、已连接卡片底 |
| 背景 / 卡片 / 次级面 | --al-bg / --al-surface / --al-surface-2 | 窗口底、卡片、侧栏 |
| 边框 / 强边框 | --al-border / --al-border-strong | |
| 正文 / 次要 / 弱化 | --al-text / --al-text-2 / --al-text-3 | |
| 警示文字 / 警示底 / 警示边 | --al-warn-text / --al-warn-bg / --al-warn-border | 连接中、待同步、套餐将到期 |
| 危险文字 / 危险底 | --al-danger-text / --al-danger-bg | 错误、已到期、流量用尽 |
| 焦点环 | --al-focus | 键盘焦点 |
| 图表网格 / 坐标 | --al-chart-grid / --al-chart-axis | 速率曲线 |

- 带透明度的网站值（如深色 brand-soft）在各平台用同样的透明度表达；平台无法表达渐变时（网站深色 banner 是渐变），取渐变起点色。
- 各平台的令牌定义必须集中在一个文件：iOS Theme.swift、Android ui/theme/Theme.kt 与 res/values(-night)/colors.xml、Windows Theme/Light.xaml 与 Dark.xaml、macOS 第 3 轮新建的主题文件。界面代码只引用角色名，不出现颜色字面量。
- 一致性测试：test-brand-assets.py 解析 globals.css 的 tokens 块与上述各平台文件，按映射表逐项比对数值（允许大小写与 ARGB/RGBA 写法差异），任一不一致即失败。网站改色后 APP 测试会立即提醒同步。

### 4.3 macOS 图标
- 图标形态遵循 macOS 图标网格：1024 画布中央约 824 见方的连续圆角方块（品牌绿底板），四周透明留白，带系统风格的轻微投影；盾牌图形在底板中的比例与网站标识相同。不得把网站的方形 favicon 直接铺满 1024 画布（在 Dock 中会比其他应用大一圈且没有标准圆角）。
- 生成 16、32、128、256、512 及各自 2 倍共 10 张图，打包成 AppIcon.icns（ICNS 容器可直接内嵌 PNG，生成脚本自行打包，不依赖只在 macOS 上才有的 iconutil，以便清单检查在 Linux 上也能验证结构）。
- build-native-macos.py 把 AppIcon.icns 复制进 Contents/Resources，并在 Info.plist 写入图标文件键；文件在签名之前放入，确保包含在签名封印内。
- release_suite.py 新增一项检查：Info.plist 有图标键、对应文件存在且为合法 ICNS、包含 1024 尺寸。
- macOS 26 起系统对不符合新网格的旧式图标会放进统一的圆角底板里显示；按上面的网格生成可以避免图标被“套框”。第 1 轮在本机 macOS 26 上目视确认 Dock、Finder、启动台、⌘Tab 的显示效果并截图；若仍被套框，第 6 轮评估是否补一份 Icon Composer 格式资源（需要 Xcode 的资源编译器，属于构建流程变更，届时单独确认）。
- 菜单栏图标：从“图形”生成模板图像（纯黑 + 透明，系统自动适配浅深色菜单栏与高亮态），18 点高，1 倍与 2 倍；准备“未连接（描边盾牌）”与“已连接（实心盾牌）”两个状态，出错状态用描边盾牌加一个小的感叹标记。替换现在的系统符号 bolt.shield。
- DMG：卷图标使用同一 ICNS（第 3 轮随 DMG 说明文字一起处理，见第 5.8 节）。

### 4.4 Windows 图标
- AsterLink.ico：16、20、24、32、40、48、64、96、256 九个尺寸，256 用 PNG 压缩，其余同样内嵌 PNG（Windows 10 起全面支持）；形态与网站 favicon 相同（圆角品牌方块 + 白色盾牌），因为 Windows 不对应用图标做遮罩。
- 托盘图标：生成三套小尺寸 ICO（16、20、24、32，覆盖 100%–200% 缩放）：未连接为中性灰底板 + 白色盾牌、已连接为品牌绿底板 + 白色盾牌、出错为品牌底板右下角加红色圆点。三者在浅色与深色任务栏上都清晰。托盘加载时按当前 DPI 请求最接近的尺寸，DPI 变化时重新加载，不再在运行时用 GDI 画“A”。
- 接入点：AsterLink.App 项目的 ApplicationIcon（决定 exe、开始菜单快捷方式、任务栏、Alt+Tab 与“应用和功能”中的图标），主窗口 Icon 使用同一文件；托盘使用上面三套；setup.iss 增加 SetupIconFile，卸载显示图标已指向 exe 无需改；安装向导的小图（WizardSmallImageFile）使用品牌标识生成的 55 与 110 像素位图。
- 发布清单与签名：ICO 作为编译资源嵌入 exe，不新增安装文件，不影响服务对安装文件的完整性清单；若第 4 轮决定额外提供独立图标文件，必须同步加入发布清单与 release-check。

### 4.5 iOS 图标与启动画面
- AppIcon 三张 1024：浅色为品牌绿满版方块 + 白色盾牌（不带圆角与透明，系统负责遮罩）；深色为透明底 + 品牌深色强调色（#3dc89a）盾牌（系统提供深色底）；着色为透明底 + 灰度盾牌。
- LaunchMark 换为完整标识（品牌方块 + 盾牌），LaunchBackground 换为网站背景色（浅 #f7f8fa、深 #121a18），AccentColor 换为品牌强调色（浅、深两值）。
- 只替换资源目录中的文件，不需要改 project.pbxproj（需求方本地对 pbxproj 有未提交改动，这样可避免冲突）。
- iOS 26 的 Liquid Glass 分层图标（Icon Composer 格式）不在本期；资源目录格式在 iOS 26 上仍被接受，第 1 轮在模拟器主屏幕截图确认浅色、深色、着色三种外观。

### 4.6 Android 图标
- 自适应图标：背景为品牌色 icon_background（#0d7b61）；前景为“图形”转成的矢量（描边、圆头、圆角属性原样保留），按网站标识中图形与底板的比例换算到 108dp 画布的可见区内，保证在圆形、圆角方形、水滴等各种遮罩下都不被裁切；monochrome 层使用同一图形（Android 13 主题图标）。最低版本为 26，只需 anydpi 自适应图标，不再需要位图 mipmap。
- 通知小图标与快捷设置开关图标：新增一个只含图形的白色剪影矢量（系统只使用透明度），替换 ic_vpn_key；通知中心、状态栏、快捷开关都显示品牌盾牌。
- 启动画面：继续使用 SplashScreen 兼容库，动画图标改为新前景，图标底色为品牌色，窗口底色为网站背景色。
- 生成脚本直接输出 Android 矢量 XML（源文件中的圆形转为等价路径），不经过位图。

### 4.7 应用内品牌呈现
- 每个平台有且只有一个“品牌标识”组件，图形数据来自生成结果（与网站 BrandMark 组件同源）：iOS 的 BrandMark 不再画系统符号 power，Android 的 BrandMark 改用新前景并去掉 1.5 倍放大的补偿，macOS 与 Windows 在第 3、4 轮新建。
- 出现位置：登录页标题区、侧栏顶部（桌面端）、关于页、菜单栏面板头部（macOS）、空状态插图中不使用品牌标识（避免滥用）。
- 字标“AsterLink”与品牌标识并排时，字重、间距与网站头部一致（半粗体，标识与文字间距约为标识高度的 0.4 倍）。

## 5. 要求 A2：macOS 界面重设计与菜单栏常驻

### 5.1 桌面设计语言（macOS 与 Windows 共用）
- **信息架构**：左侧栏 + 右侧内容区。侧栏顶部为品牌标识与字标；中部三项导航：连接、线路、账户；底部为连接状态胶囊（颜色圆点 + 文字）与当前账户邮箱。登录前为独立的登录视图，不显示侧栏。
- **连接页（首页）**：视觉中心是一个大尺寸圆形连接按钮（直径约 150–170）。未连接为中性描边圆环 + 盾牌图形；连接中为品牌色圆弧沿圆环转动；已连接为品牌色实心圆 + 白色盾牌并带一圈柔和光晕；出错为危险色描边。按钮下方一行大号状态文字（未连接/正在连接…/已连接/无法连接）和一行说明。再下方三张等宽信息卡：当前线路（名称、地区、专线/普通线路标签、线路倍率，“更换”跳到线路页）、套餐（套餐名、剩余天数、剩余流量与进度条、续费入口）、实时速率（上传、下载数值 + 最近 60 秒曲线）。无套餐、未选线路、连接失败等情况用卡片顶部的提示条说明原因并给出唯一一个下一步操作。
- **线路页**：顶部搜索框 + 分段筛选（全部/专线/普通线路）+ 刷新；列表按地区分组并显示每组数量（与 iOS、Android 一致）；每行显示名称、地区、类型标签、线路倍率、选中标记（图形 + “已选”文字）；悬停、选中、键盘焦点三种状态清晰可辨。已连接时切换线路仍弹出现有的确认（文案改写）。
- **账户页**：账户卡（首字母头像、邮箱、套餐摘要）与网站入口（续费或升级、订单记录、网站控制台）；外观（跟随系统/浅色/深色）；通用说明（关闭窗口后继续在菜单栏/托盘运行）；后台服务状态（第 D11 条）；关于（版本、复制诊断信息、官网）；底部为“退出登录”与“退出 AsterLink”。
- **登录视图**：双栏。左栏为品牌区（品牌底色或深色品牌面板，品牌标识、产品名、一句与网站首屏一致的价值表述，不出现无法兑现的宣传，遵守 handoff_ui.md 的 D12）；右栏为登录卡（邮箱、密码、显示密码、记住密码、登录按钮）与“注册账号 / 忘记密码 / 购买套餐”链接。首次需要授权后台服务时，登录卡上方显示分步引导卡（第 5.7、6.5 节）。
- **视觉规格**：8 点栅格；卡片圆角 12、按钮与输入框圆角 8；主按钮高 36–40；页面标题 24–26、区块标题 15–16、正文 13–14、说明 12；卡片使用网站的 surface 与 border 令牌，浅色下有极轻阴影、深色下无阴影只靠边框分层。
- **动效**：状态切换与悬停 150–250 毫秒；连接中的圆弧转动；系统开启“减少动态效果”时全部改为无动画的即时切换。
- **无障碍**：全部可用键盘完成（Tab 顺序：导航 → 主操作 → 次要操作），焦点环使用品牌焦点色；所有图标按钮有可读名称；状态不只靠颜色；正文对比度达到 WCAG AA（与网站相同的令牌，已在网站评审）。
- **数据边界**：界面只展示快照里已有的字段，不新增任何数据或接口调用；所有按钮调用的都是现有的会话方法，启用/禁用条件与现在完全相同。

### 5.2 macOS 实现思路
- 视图拆分：把 Views.swift 按页面拆成多个文件（登录、侧栏外壳、连接、线路、账户、引导卡、菜单栏面板、通用组件、主题），全部仍在 AsterLinkApp 目标内，不动 AsterLinkShared 与 AsterLinkHelper。
- 外壳使用 NavigationSplitView（侧栏带系统毛玻璃材质），窗口默认约 960×640，最小约 820×560；保留系统红绿灯，标题栏与工具栏合并（隐藏窗口标题文字，侧栏顶部显示品牌）。
- 主窗口场景从 WindowGroup 改为单实例 Window，并移除“新建窗口”菜单项，避免出现多个主窗口；关闭窗口状态恢复，避免下次启动时恢复到奇怪的位置或尺寸（窗口尺寸本身可以记住）。
- 设置弹窗取消，内容并入“账户”页；⌘, 打开主窗口并切到账户页。
- 主题：新增本地偏好（跟随系统/浅色/深色），通过窗口外观覆盖实现，菜单栏面板同样遵循。
- 图表继续使用系统 Charts 框架，配色改用品牌令牌。

### 5.3 macOS 视觉要点
- 字体使用系统字体（中文自动回退到苹方），不打包字体文件。
- 品牌色只用于主操作、选中与已连接状态；其余界面保持 macOS 原生的中性灰阶，避免整窗大面积品牌绿。
- 侧栏使用系统侧栏材质，内容区使用网站背景令牌；深色模式下与系统深色保持协调（不出现纯黑或与系统差异过大的底色）。

### 5.4 菜单栏面板
- MenuBarExtra 改为窗口式面板（约 300 宽），图标使用第 4.3 节的模板图像并随连接状态切换。
- 面板内容自上而下：品牌标识 + 状态文字；中号连接开关（与主窗口按钮同样的启用条件，调用同样的方法）；当前线路一行（点击打开主窗口线路页）；剩余流量一行；底部“打开 AsterLink”“退出”。未登录时只显示“未登录”“打开 AsterLink 登录”“退出”。
- 面板的打开不应额外触发同步：现在“应用变为活跃”会触发一次刷新，而在仅菜单栏模式下打开面板也可能让应用变为活跃。第 3 轮必须核对并保证刷新只在“主窗口被激活”和“系统唤醒”两个时机发生，与现状一致，不因面板而增加接口调用（验收见第 10 节）。

### 5.5 关闭即缩到菜单栏、不占 Dock
- 行为：点击主窗口红色关闭按钮或按 ⌘W，窗口关闭，应用切换为仅菜单栏模式，Dock 图标消失，⌘Tab 中不再出现，连接与轮询照常进行。
- 再次显示主窗口的入口：菜单栏面板“打开 AsterLink”、点击面板中的线路或提示、在 Finder/启动台/聚焦搜索里再次打开应用（系统会发出“重新打开”事件，必须处理并显示主窗口）、⌘, 。每次显示主窗口前先切回普通应用模式，再打开窗口并把应用激活到最前（macOS 14 起激活策略更严格，需在模式切换完成后的下一轮事件循环再激活，第 3 轮实测确认窗口确实出现在最前且获得焦点）。
- 最小化（黄色按钮）保持系统默认行为（缩到 Dock），不做特殊处理。
- 退出：⌘Q（主窗口打开时）、菜单栏面板“退出”、账户页“退出 AsterLink”，都走现有的 applicationShouldTerminate 延迟退出流程（先断开并结算，再退出），不改动。
- 首次关闭提示（D6）：只在第一次关闭主窗口时，在菜单栏图标下方显示一个不抢焦点的小浮层约 6 秒：“AsterLink 仍在菜单栏运行，点击此图标即可重新打开。”是否已提示记录在本机偏好。
- 注销/关机：系统发起的退出同样走现有的终止流程，第 3 轮验证不会因为仅菜单栏模式而卡住注销。

### 5.6 生命周期与窗口解耦
- 现在 session.launch、Helper 注册监听、“应用活跃时刷新”和“唤醒时刷新”都挂在主窗口内容视图上：窗口关闭后监听被取消，再打开时又重新挂上。改为在应用委托（或应用级对象）中启动一次、全程有效：启动只调用一次 launch（其本身已对重复调用做了保护），Helper 注册监听全程运行，唤醒刷新全程有效，“应用活跃刷新”改为“主窗口成为关键窗口时刷新”。
- 这只是改变“谁来调用”，调用的方法、参数与时机语义不变；验收要求对比改造前后同一操作序列下 Core 收到的命令序列一致（第 10 节）。

### 5.7 首次授权后台服务的引导
- 保留现有逻辑（注册 → 打开系统设置对应页 → 自动检测批准 → 继续启动），把 HelperSetupCard 重做为分步引导卡：三个步骤带完成标记，主按钮“启用”或“再次打开系统设置”，等待批准时显示进度。
- 文案改写示例见第 7.3 节；“必须从‘应用程序’文件夹运行”的提示保留但改写为面向用户的说法。

### 5.8 DMG 与安装说明
- DMG 卷名保持 AsterLink，加入卷图标；“安装说明.txt”改写为面向用户的三段（安装、首次打开时允许后台服务、卸载），删除“特权服务”“预演/--apply”“详见 docs/...”等开发表述；卸载脚本文件名保留（发布审计依赖它），其运行时输出的中文提示同步改写，参数行为不变。
- 修改后 release_suite 的 DMG 内容检查与密钥扫描必须仍然全部通过。

### 5.9 不得改动的边界
AsterLinkShared（SessionController、CoreClient、凭据、设备标识、签名与命令策略、IPC）、AsterLinkHelper、Go Core、构建脚本中与签名/清单/许可证相关的步骤，以及 DEBUG 开发模式（ASTERLINK_DEV_* 环境变量与 DirectCoreTransport）一律不动。允许改动的只有 AsterLinkApp 目标内的视图与应用外壳、展示用的 Formatting、构建脚本中“放入图标与 Info.plist 图标键”这一步、DMG 说明文字与卷图标。

### 5.10 macOS 设计预览（验收工具）
- macOS 目前没有任何界面截图测试。仿照 Windows 的 PreviewRunner，在 AsterLinkApp 中增加仅 DEBUG 构建可用的预览模式（启动参数指定输出目录）：用一个脚本化的 CoreTransport 注入固定快照，逐个场景把主窗口、菜单栏面板渲染为 PNG。
- 渲染方式使用真实的离屏窗口承载视图后抓取位图（SwiftUI 的纯图像渲染器无法绘制文本框、列表等由 AppKit 承载的控件，会出现占位符）。
- 同时遍历窗口的无障碍树，导出每个场景的全部可见文字为 JSON，供文案守卫做运行时检查。
- test-native-macos.py 增加 ui 套件：构建 Debug 包、运行预览、检查场景齐全、对导出文字跑禁用词检查；加入 ci.yml 的 macos job，并上传 PNG 作为工件。场景清单与 Windows 预览对齐（见第 10 节第 3 轮）。

## 6. 要求 A2：Windows 界面重设计与托盘常驻

### 6.1 设计
- 信息架构、页面内容、视觉规格、动效与无障碍完全遵循第 5.1 节，与 macOS 版保持同一套布局与文案，只在平台习惯处不同（字体、标题栏、托盘）。
- 字体：Segoe UI Variable（Windows 11）回退 Segoe UI，中文明确指定 Microsoft YaHei UI，避免中文回退到宋体。
- 图标：第 D3 条的 Tabler 图形，集中在一个资源字典中按名称引用，颜色随主题。
- 窗口外观：保留系统标题栏（保证贴靠布局、无障碍与触摸行为正常）；深色主题时通过系统窗口属性让标题栏变深色；Windows 11 22H2 及以上为窗口启用 Mica 背景材质，侧栏透出材质，内容区使用卡片底色；Windows 10 回退为纯色背景。所有系统属性调用前做版本判断，失败时静默回退，不影响启动。
- 默认窗口约 1000×680，最小 820×560（现状最小 800×560，略放宽以容纳三张信息卡；在最小尺寸下信息卡改为两行排列，不出现横向滚动）。

### 6.2 实现思路
- 改动集中在 AsterLink.App：Views 下的四个页面、MainWindow、Theme 下的令牌与样式、TrayIcon、App.xaml.cs 的关闭与托盘逻辑、AppViewModel 中只负责展示的属性与文字。
- 连接按钮、圆环动画、信息卡、分组列表、提示条、分段控件等做成可复用的样式或控件模板，放在 Theme/Styles.xaml（或拆分为多个资源字典），全部颜色使用 DynamicResource 引用令牌以保证主题实时切换。
- 线路按地区分组在视图层基于现有行数据完成（不改变服务器顺序之外的任何逻辑：组内保持服务器给出的顺序，组的顺序按每组第一条线路在原列表中的位置）。
- 现有 AutomationId 一律保留（自检、无障碍和今后的界面自动化依赖它们），新增元素同样命名。
- `--self-test`、`--preview`、`--service-check`、`--cleanup-user-data` 等命令行模式保持可用，自检的页面列表随视图拆分同步更新。

### 6.3 托盘菜单与托盘图标
- 左键单击：显示并激活主窗口（现状）。右键菜单：第一行为不可点击的状态行（如“已连接 · 东京专线 01”或“未登录”）；“打开 AsterLink”；“连接/断开”（启用条件与现状相同）；分隔线；“退出”。
- 菜单外观：WinForms 默认菜单风格较旧，使用自定义渲染颜色表让菜单跟随当前主题（浅色/深色）与品牌色悬停态，圆角与间距贴近 Windows 11 原生菜单。
- 托盘提示文字最长 63 个字符（系统限制，现有截断逻辑保留），内容改为“AsterLink · 已连接”这一类。
- 图标按第 4.4 节三态切换。

### 6.4 关闭即缩到托盘
- 行为：点击“×”、按 Alt+F4、任务栏右键“关闭窗口”时，在任何状态下都取消关闭并隐藏窗口（同时从任务栏消失），进程、连接与同步照常。删除现在“未连接时直接退出”的分支。
- 首次提示（D6）：第一次隐藏时弹一次托盘通知：“AsterLink 已在后台运行。点击任务栏右下角的 AsterLink 图标即可重新打开；如果没有看到，请点击‘^’显示隐藏的图标。”（Windows 11 默认把新托盘图标放在溢出区，必须提示。）沿用现有 TrayNoticeShown 偏好。
- 重新打开：托盘左键、托盘菜单“打开”、再次从开始菜单启动（现有单实例激活机制已支持）。
- 退出：托盘菜单“退出”、账户页“退出 AsterLink”，走现有 ExitAsync（先断开并结算再退出），不改动。
- 注销与关机：窗口一律取消关闭后，必须确认系统注销/关机时不会因为取消关闭而阻塞会话结束。第 4 轮在应用级的会话结束事件中标记“正在退出”，让窗口关闭不再被取消，并让现有退出流程在系统允许的时间内完成；验证注销不出现“此应用阻止关机”。

### 6.5 首次安装服务的引导
- 现有服务状态卡（未安装/未运行/需要修复/需从安装目录启动）按第 5.1 节做成引导卡：一句原因、一个主按钮（安装/启动/修复，仍触发现有的提权流程）、“重新检测”。
- 状态与动作文字全部改写（第 7.3 节），不显示错误代码与英文异常信息；错误码保留在诊断报告里（D9），英文异常信息不进入报告。

### 6.6 安装程序
- SetupIconFile 与向导小图使用品牌资产（第 4.4 节）。
- 安装界面语言：当前只有英文语言文件，自定义消息为中英双语。默认改为简体中文界面（使用 Inno Setup 自带的简体中文语言文件；若锁定版本未自带，则提交一份经审阅的语言文件），自定义消息只保留中文并按第 7 节改写；需求方若需要保留英文可选，作为第二语言保留。
- 不改变安装、升级、回滚、卸载的任何步骤与校验，release-check 与发布套件必须全部通过。

### 6.7 不得改动的边界
AsterLink.Service、AsterLink.Platform、AsterLink.Shared 中除展示文字之外的全部内容（SessionController 的流程、服务客户端、管道、凭据、恢复存储、升级策略、自检逻辑）、Go Core、发布清单与签名脚本一律不动。AsterLink.Shared 中允许改动的只有纯展示文字（Presentation.cs 的状态文字、ServiceHealth.cs 的说明文字、SessionController 中给用户看的提示字符串、Formatting 的单位显示），并同步更新对应测试断言。

## 7. 要求 A3：文案商业化

### 7.1 规范
1. 说用户能理解、能操作的话：每条提示回答“发生了什么”和“我该做什么”，不解释内部原因。
2. 不出现任何底层实现：内核、Core、Mihomo、Clash、隧道、代理、特权服务、Helper、握手、协议版本、构建渠道、通道、代次、批次、账本、会话交接、节点计量、客户端上报、计量方式、幂等、请求标识、数据通路、转发、校验、清单、构建、签名、令牌、进程、运行时、架构、退出码、错误代码。
3. 不出现开发期信息：开发版（D8 豁免除外）、调试、联调、测试线路、内部、第 N 轮、handoff、.env 及任何文件路径、仓库地址。
3a. 不出现任何开源组件与许可信息（D10）：开源许可、第三方组件、GPL、Apache、sing-tun、源代码、GitHub 等字样与入口一律删除，**没有任何豁免**。
4. 不对普通用户提“管理员”（D13）；需要人工帮助时说“联系客服”。
5. 单位与数字：流量用 GB（基数 1024³），倍率写“N×”并称“线路倍率”，日期与网站一致，数字与单位之间一个半角空格；删除所有“1 GiB = 2³⁰ 字节”类说明。
6. 用量表述：用户界面只说“已用/剩余流量”“正在同步用量”，不出现“结算”“确认批次”“服务器已确认”等记账术语；退出、切换线路时的说明改为“将断开当前连接”。
7. “VPN”只用于系统强制场景（iOS、Android 添加 VPN 配置与系统 VPN 设置）；Windows 与 macOS 界面不出现。
8. Android 英文资源与中文同步改写，英文同样遵守以上规则。
9. 复制到剪贴板的诊断报告同样属于用户可见内容，按 D9 精简，四个平台（macOS AppDiagnostics、Windows DiagnosticsReport、iOS AsterLinkKit/Diagnostics、Android 对应实现）格式一致，并纳入文案守卫。

### 7.2 共享层：Go Core 提示文字（第 2 轮）
只改 native/core/internal/apiclient/messages.go 中的中文文字，错误码、默认兜底文字的逻辑、TerminatesSession 与 InvalidatesConfig 的判定全部不变。需要改写的条目与改写方向如下（最终文字以 PR 评审为准）：

| 错误码 | 现文案的问题 | 改写方向 |
| --- | --- | --- |
| client_config_unavailable | “线路配置”“联系管理员” | 线路暂不可用，请稍后重试或联系客服 |
| traffic_sequence、traffic_unconfirmed、handoff_unsettled | “流量批次”“服务器确认”“结清” | 正在同步用量，请稍后重试 |
| traffic_conflict、invalid_core_counters、invalid_traffic | “批次冲突”“计数” | 用量同步异常，请重新登录（已断开的要说明已断开） |
| node_catalog_disabled | “服务端尚未开启” | 线路列表暂不可用，请稍后重试 |
| bad_response | “无法识别的数据” | 服务暂时异常，请稍后重试 |
| lease_expired | “授权已过期” | 正在重新验证账户 |
| config_rejected | “安全校验” | 该线路暂不可用，请更换线路 |
| tun_unavailable | “隧道组件” | 当前版本无法建立连接，请前往官网下载最新版本 |
| tunnel_failed、tunnel_unhealthy | “隧道”“联系管理员” | 连接失败/该线路暂时无法访问网络，已断开，请更换线路或稍后重试 |
| invalid_api_base | “服务器地址配置” | 应用安装不完整，请从官网重新下载安装 |
| invalid_request | “请求格式” | 操作失败，请稍后重试 |
| grant_expired、grant_not_found、connection_expired | “连接授权”“在线状态” | 连接已失效，请重新连接 |
| grant_timeout、agent_unavailable | “线路节点”“节点” | 线路响应超时/暂不可用，请更换线路或稍后重试 |
| grant_required | “节点授权” | 当前版本不支持该线路，请升级到最新版本 |
| metering_source_mismatch、handoff_invalid、handoff_expired、handoff_stale、handoff_mismatch、handoff_conflict | “计量方式”“会话交接”“账本” | 登录状态已变化，请重新登录 |
| handoff_busy | “接管” | 当前已有登录，请先退出后再试 |
| idempotency_conflict | “请求标识” | 操作冲突，请重试 |
| lifecycle_reconfirmation_required | “时钟变化” | 设备休眠或网络变化，已断开，请重新连接 |

其余条目（邮箱或密码不正确、套餐到期、流量用完、设备上限、线路已满等）已符合规范，保持不变。同步更新 manager_test、connection_test、contract_v3_test 中的子串断言；各平台把 Core 文字当作固定值的测试与预览样例（如 Windows PreviewRunner 的“隧道启动失败”样例、iOS/Android 的快照夹具）同步更新。

### 7.3 各平台本地文案清单
以下为本次读取时发现的主要问题，实施时以 grep 结果为准，逐条处理：

**macOS（第 3 轮）**
- 设置页整块：“设置与关于”“特权服务（Helper）”“握手”“重新安装/移除特权服务”“Helper x · 协议 v · 内核运行”“应用 · 通道”“内核 · Mihomo”“服务器 主机名”“复制脱敏诊断”→ 按 D8、D9、D11 重做为账户页的“后台服务”“关于”区块。
- 连接区：“本次登录已经线路传输”“预估剩余套餐流量（服务器已确认 …，有批次待确认）”“本线路由节点侧独立计量……”“线路倍率”“节点侧独立计量”“线路配置已校验”帮助提示 → 按 7.1 第 5、6 条与 D12 改写或删除。
- “管理员尚未配置可用线路”→ D13。“已就绪，未连接”→“未连接”。“出错”→“无法连接”。
- Helper 相关提示（HelperConnection、HelperClient、HelperUpgradePolicy、SessionController 中的“特权服务……”“内核已意外退出……”“无法建立与特权服务的安全连接”等）→ 改为“后台服务”说法或“连接已中断，请重新登录”这一类。
- 启动失败页“应用配置缺失或无效，已拒绝启动”→“应用安装不完整，请从官网重新下载安装”。
- AppDiagnostics 的报告（“复制诊断信息”）按 D9 精简：删除 core、mihomo、api 主机、generation、config_ready 等行，字段改为中性中文。
- DMG“安装说明.txt”与卸载脚本提示（第 5.8 节）。

**Windows（第 4 轮）**
- 设置页“版本与诊断”中的操作系统/系统架构/进程架构/运行时/原生 x64 事实与自检“通过/失败”列表 → 从界面移除（自检本身照常运行，仅供 `--self-test` 使用，不进入诊断报告）。
- “应用 · 渠道 · 内核”“服务器 主机名”→ D8。“特权服务”全部 → “后台服务”。“错误代码：…（…）”→ 移除。“服务 x · 内核 x · 渠道 · 内核状态”→ 移除。
- 线路页说明“线路由管理员在网站配置……不提供配置文件或订阅导出”→ 删除。行内“节点计量”“已校验”→ 删除（已选标记保留）。
- 连接页“实线：下行 虚线：上行”→ 改为图例或直接在曲线旁标注“下载/上传”；“本线路由节点侧独立计量……”→ D12；“1 GiB = 2³⁰ 字节，以服务器确认值为准”→ 删除；“有批次待服务器确认”→“正在同步用量”。
- Presentation.cs：状态说明“流量正在经所选线路转发”“正在建立隧道并验证转发”“正在校验线路与授权”“正在关闭数据通路并结算本次流量”→“已为你建立安全连接”“正在连接…”“正在准备连接…”“正在断开…”一类；MeteringLabelOf 的两条文字不再在界面使用；ErrorText.ForService 全部改写为后台服务说法，“请求格式不正确”→“操作失败，请稍后重试”。
- ServiceHealth.cs、ServicePanel.cs 的状态与动作文字；“需要管理员授权”保留为“需要管理员权限”（这是 Windows 用户账户控制的通用说法，不涉及“管理员”角色，属于豁免）。
- 托盘：“退出（停止代理）”→“退出”；首次隐藏通知（第 6.4 节）。
- 登录页“记住密码（保存在本机 Windows 凭据管理器）”→“记住密码”；凭据管理器相关警告保留系统名称但精简。
- “操作失败（异常类型名），请重试”→ 不显示异常类型名。
- DiagnosticsReport（Presentation.cs）按 D9 精简：删除 core、service、api、generation、config_ready 等行，字段改为中性中文，与其他平台格式一致。
- 安装程序消息（第 6.6 节）。

**iOS（第 2 轮）**
- 登录页页脚“版本 · Core · Mihomo”“开发版 · 主机名”→ D8。
- 我的页“Core 版本”“Mihomo”→ 删除；“流量以 GiB（2³⁰ 字节）计，按线路倍率扣减”→“流量按线路倍率计算”；“开源许可与源代码”按钮、LicensesView 页面与弹出逻辑整体删除（D10）；“复制诊断信息”的报告按 D9 精简。
- 线路页“节点计量”标签与无障碍描述 → D12；“普通”→“普通线路”（筛选分段可保留“普通”二字以节省宽度，由评审决定，但列表与标签统一）；“计费 N×”→“线路倍率 N×”。
- 连接页“本次用量 · 计费”保留含义，改为“本次已用 X · 按倍率计 Y”或评审认可的更短说法。
- AsterLinkKit/ErrorText.swift：“代理内核无法启动”“内核已停止”“隧道已建立，但系统流量没有经过隧道”“请求格式不正确”“管理员尚未配置可用线路”“本次流量已结算”系列、状态说明中的“隧道”“数据通路”“结算”“在线确认授权；确认前不转发任何流量”“隧道保持但不转发流量”→ 按 7.1 改写；“VPN 已被系统设置或其它 VPN 应用关闭”这类涉及系统 VPN 的说法保留“VPN”。
- 隧道扩展 PacketTunnelProvider 中会被系统显示的失败文字（“隧道组件无法启动”“会话交接失败”“隧道启动失败”）同步改写。
- DEBUG 专用的 TunnelDebugView、UIFixtures 与“调试”分区只存在于 Debug 构建，文案守卫豁免；第 2 轮确认 Release 构建中确实不包含（检查 Release 产物中不含其标识）。

**Android（第 2 轮）**
- strings.xml（中、英）：login_footer、login_footer_starting、login_dev_channel、env_from_example（仅 Debug，确认 Release 不显示，否则豁免标记）、status_detail_connecting/stopping/waiting_network、metering_node、metering_client、traffic_pending、plan_units_note、about_licenses、about_licenses_body、about_licenses_open（连同账户页中的条目与“打开下载页”按钮整体删除，D10）、about_core_version、about_mihomo、about_server、about_channel、notice_core_start_failed、notice_core_lost、notice_no_lines，以及所有含“结算”的说明；账户页“关于”中的内核版本、Mihomo、服务器、构建渠道各行删除，只保留应用版本；诊断报告按 D9 精简。
- 通知渠道名称、前台服务通知文字、快捷开关标签：检查 VpnNotifications 与 ConnectTileService 中的字符串资源，按规范改写。
- 线路类型与倍率标签同 iOS。

### 7.4 文案守卫
- 新增跨平台静态扫描脚本（scripts/test-app-copy.py，只用标准库），扫描对象：Core messages.go 的值；iOS 与 AsterLinkKit 中含中文的字符串字面量；Android 所有 strings.xml 的值；macOS AsterLinkApp 中含中文的字面量；Windows XAML 中 Text/Content/ToolTip/Header/AutomationProperties.Name 属性与 C# 中含中文的字面量；macOS DMG 说明与卸载脚本提示；Windows setup.iss 的消息段。另对上述字面量做英文技术词扫描（Core、Mihomo、Clash、Helper、GiB、channel、generation 等，大小写敏感，排除无障碍标识等不可见字符串）。
- 禁用词表即第 7.1 节第 2、3、3a 条（3a 不接受任何豁免标记），外加“联系管理员”“管理员尚未”“结算”“2³⁰”“1024³”。
- 豁免方式与网站 copy-guard 相同：在该行或上一行写入固定标记即可放行，并写明原因（例如 D8 的开发版标记、Windows“需要管理员权限”）；整文件豁免只允许用于 DEBUG 专用文件，清单写在脚本内。
- 运行时守卫：Windows 预览、macOS 预览导出每个场景的可见文字；iOS UI 测试与 Android 截图测试在每个场景收集屏幕上的全部文字；全部经过同一份禁用词表。
- 第 2 轮脚本先覆盖 Core 与移动端，第 3、4 轮分别加入 macOS、Windows，第 6 轮加入安装包、DMG 与发布目录，并在 ci.yml native-core job 中运行。

## 8. 要求 A4：发布包去敏（第 5 轮）

### 8.1 现状（本次读取的打包脚本）
| 平台 | 应用包内 | 公开发布目录（现状） | 已有的内部目录 |
| --- | --- | --- | --- |
| macOS | .app 的 Resources 中有 THIRD_PARTY_LICENSES.txt 与 SOURCE.txt；Info.plist 有 AsterLinkMihomoRevision 键；AppDiagnostics 源码里内置一个默认提交号；Core 构建经链接参数写入 Mihomo 提交号；DMG 中的“卸载 AsterLink.command”是可直接打开阅读的脚本，含开发注释 | DMG、THIRD_PARTY_LICENSES.txt、SOURCE.txt、本仓库与 Mihomo 的源码归档、SHA256SUMS、release.json | 无 |
| Windows | 安装包内为 app、service、core 三个目录及 build-config.json、manifest.json（只含哈希与公开地址）；Core 构建没有去除调试符号 | Setup.exe、SHA256SUMS、release-info.json、dependencies.cdx.json、THIRD_PARTY_LICENSES.txt、SOURCE.txt、源码归档 | 无 |
| iOS | IPA 中的 Core 框架经链接参数写入 Mihomo 提交号 | IPA、THIRD_PARTY_LICENSES.txt、dependencies.cdx.json、SOURCE.txt、源码归档、release-info.json、SHA256SUMS | release-private（dSYM） |
| Android | APK 中的 Core 库经链接参数写入 Mihomo 提交号 | APK、THIRD_PARTY_LICENSES.txt、dependencies.cdx.json、SOURCE.txt、源码归档、release-info.json、SHA256SUMS | release-private（R8 映射、网站下载配置） |

### 8.2 处理方案
1. **公开发布目录**只保留安装包本身（DMG / Setup.exe / IPA / APK）和只列出这些安装包的 SHA256SUMS。
2. **内部目录**：THIRD_PARTY_LICENSES.txt、SOURCE.txt、源码归档、dependencies.cdx.json、release-info.json / release.json 全部移入各平台的 release-private 目录（macOS、Windows 新建同名目录），文件内容与生成方式不变；目录内放一份说明，写明“内部资料，不得发布”。原来在 release-private 中的 dSYM、R8 映射、网站下载配置保持不变。
3. **应用包内**：macOS 构建不再把两份文本放进 Resources，Info.plist 删除 AsterLinkMihomoRevision 键，AppDiagnostics 删除内置默认提交号（D9 之后已不需要）。
4. **内核提交号**：四个平台的发布构建（Release 配置及内部发布包）不再通过链接参数写入 Mihomo 提交号，Core 握手结果中的该字段为空字符串，字段本身与 IPC 协议不变；提交号继续记录在 release-private 的发布信息中以便追溯。开发构建可以保留。凡是断言“提交号非空”的测试改为只在开发构建断言。
5. **构建选项**：发布构建统一使用去除调试符号、去除路径、不嵌入版本控制信息的选项；Windows 的 Core 构建补上去除调试符号（现在缺少），只影响二进制体积与可读信息，不影响功能。
6. **DMG 中的卸载脚本**：删除注释里的开发说明、内部文档引用与技术细节，用户可见提示按第 7 节改写；参数与行为不变。Windows 卸载逻辑在安装程序内部，没有明文脚本暴露。
7. **审计翻转**：macOS release_suite（R-LICENSE、R-PLIST 等）、iOS test-native-ios.py 的 release 套件、android_release_suite.py、Windows release-check.ps1 与发布套件中原本要求许可与源码文件“必须存在且含 GPL 文本”的检查，改为两部分：其一，公开目录与展开后的应用包内不存在上述文件，并对公开目录全部文件、展开后的应用包内全部文本类文件与 Info.plist 做字符串扫描，不得出现 mihomo、Mihomo、Clash、metacubex、GPL、GNU GENERAL PUBLIC、sing-tun 等标记；对二进制文件单独统计命中并与 D16 的已知残留基线比对，出现基线以外的新命中即失败。其二，release-private 中这些文件仍然完整（保证纸质材料有据可依）。
8. **CI 工件**：各工作流上传发布目录的步骤分成“公开交付物”和“内部资料”两个工件，名称写明用途，避免把内部资料当作安装包转发。
9. **网站下载**：核对网站下载配置只指向公开目录中的安装包（Android 的下载配置文件本身已在 release-private 中）。

### 8.3 不改动的部分
签名、公证、完整性清单、服务对安装文件的校验、版本规则、源码归档与许可清单的生成逻辑全部不变，只改变“放在哪里”和“是否写入应用包”。Helper 与 Windows 服务对 Core 二进制的校验依赖文件哈希而非提交号，第 5 轮需在四个平台的 bundle/服务套件中确认这一点。

## 9. 兼容、发布与回滚

- **无协议与数据变更**：后端、数据库、网站不变；Core 只改提示文字，旧客户端与新后端、新客户端与旧后端的兼容性完全不受影响。
- **图标缓存**：macOS 替换图标后 Finder/Dock 可能显示旧缓存，验收时用 LaunchServices 重新注册或重启 Dock；Windows 图标缓存同理，升级安装后快捷方式图标应自动刷新，第 4 轮在升级场景中验证。
- **升级路径**：Windows 新安装包覆盖旧版本时，旧版本若在托盘运行，安装程序现有的“请先退出”检测仍然生效（其提示文字改为中文并说明从托盘退出）。macOS 替换应用时，仅菜单栏模式的旧进程需要先从菜单栏退出，DMG 说明写明。
- **发布**：各平台按现有发布流程出包；本次不改签名、公证、清单与版本规则。
- **内部资料**：第 5 轮起各平台的 release-private 目录只供需求方内部使用（编写纸质说明书、追溯、崩溃分析），任何时候都不上传官网、不随安装包分发。
- **回滚**：回退到上一版安装包即可（注意上一版的发布目录仍含许可与源码文件，回滚时只取安装包本身）；本次新增的本地偏好（macOS 主题、首次关闭提示标记）对旧版本无影响。

## 10. 六轮实施与逐轮验收

每轮通用要求：受影响平台的 CI job 全绿（macOS、Windows、iOS、Android、native-core、web-core 中涉及的部分），并确认本平台自己的检查项确实出现在 PR 的检查列表中（工作流配置错误会让整个 job 不运行而 PR 仍显示通过）；PR 中附关键界面浅色、深色截图；未执行的项目如实标为 NOT RUN 或 BLOCKED 并写明原因。

### 第 1 轮：品牌资产管线与四平台图标

工作内容：
1. 生成脚本、生成清单与 test-brand-assets.py（资产部分），加入 CI。
2. macOS：AppIcon.icns、Info.plist 图标键、release_suite 图标检查、菜单栏模板图标（替换系统符号，面板本身第 3 轮再改）。
3. Windows：AsterLink.ico 与 ApplicationIcon、窗口图标、托盘三态图标（替换运行时绘制）、安装程序图标与向导小图。
4. iOS：AppIcon 三外观、LaunchMark、LaunchBackground、AccentColor。
5. Android：自适应图标前景/背景/单色、通知与快捷开关图标、启动画面。
6. iOS、Android 的 BrandMark 组件改用新图形。

验收：
- 修改 mark.svg 中任意一个颜色后不重新生成，test-brand-assets.py 失败；还原后通过。
- macOS：构建出的应用在 Finder、Dock、启动台、⌘Tab 与“系统设置 › 登录项与扩展”中显示品牌图标（本机 macOS 26 截图）；release 套件 R-ICON 通过；菜单栏图标在浅色与深色菜单栏下清晰。
- Windows：exe、开始菜单、任务栏、Alt+Tab、“应用和功能”、安装向导、托盘在 100%、150%、200% 缩放下显示品牌图标且不模糊；托盘图标在连接、断开、出错三态间正确切换（CI 的安装与 GUI 冒烟步骤截图，或在授权测试机上验证，做不到则 BLOCKED）。
- iOS：模拟器主屏幕浅色、深色、着色三种外观截图；启动画面截图；Release 构建资源中不再含旧图标。
- Android：Pixel 系列模拟器上圆形与方形遮罩、主题图标（单色）各截一张；通知栏与快捷开关显示品牌盾牌；Roborazzi 截图更新且差异只出现在品牌区域。
- 所有平台的现有测试照常通过（图标替换不影响功能）。

### 第 2 轮：移动端色板、共享文案与文案守卫

工作内容：
1. 色板一致性检查加入 test-brand-assets.py；iOS Theme.swift、AccentColor，Android Theme.kt 与 colors.xml 按第 4.2 节映射改写；逐屏核对浅色与深色对比度。
2. Core messages.go 改写（第 7.2 节）与相关 Go 测试。
3. iOS、AsterLinkKit、隧道扩展、Android（中、英）文案改写（第 7.3 节），单位改为 GB，删除计量来源标签。
4. scripts/test-app-copy.py（覆盖 Core 与移动端），加入 CI；iOS UI 测试与 Android 截图测试加入运行时禁用词检查。
5. 同步更新 iOS ScreenshotTests、AsterLinkKit 测试、Android PresentationTest 与 ScreenshotTest 中的文字断言。 iOS 中与“开源许可”页面相关的 UI 测试与无障碍标识一并删除。

验收：
- 色板一致性测试通过；把网站任一映射令牌改值后测试失败。
- iOS UI 测试全部场景、Android 全部截图场景通过运行时禁用词检查；截图人工评审：品牌绿替换完整，无残留蓝色，已连接/警示/错误状态可辨。
- Go Core 全部测试通过；错误码、TerminatesSession、InvalidatesConfig 的行为测试无任何改动。
- 静态守卫对 Core 与移动端零命中（豁免项逐条列在 PR 中）。
- iOS Release 构建与 Android Release 构建中不包含调试界面（检查产物）。
- iOS 与 Android 的“我的”页不再有任何开源许可入口；四个平台诊断报告按 D9 只含应用版本、系统版本、连接状态、错误码与时间（单元测试断言报告不含 Core、Mihomo、主机名等字段）。
- 计费、生命周期相关的既有测试套件（iOS billing/lifecycle、Android BillingAcceptance/Lifecycle 在 CI 能跑的部分）全部通过，证明只改了显示。

### 第 3 轮：macOS 界面重设计与菜单栏常驻

工作内容：
1. 主题令牌文件、外壳（侧栏 + 内容）、登录、连接、线路、账户各页、引导卡、菜单栏面板（第 5.1–5.4、5.7 节）。
2. Window 单实例场景、动态 Dock 策略、重新打开处理、首次关闭提示、主题偏好（第 5.5 节、D14）。
3. 生命周期挂钩迁出窗口（第 5.6 节）。
4. macOS 文案（第 7.3 节）、DMG 说明与卷图标（第 5.8 节）。
5. 设计预览与 test-native-macos.py ui 套件，加入 CI 并上传 PNG；文案守卫加入 macOS。

预览场景（与 Windows 对齐）：登录（浅/深）、登录需要授权后台服务、连接就绪、未选线路、已连接（浅/深）、无套餐、套餐到期、流量用尽、连接失败、最小窗口尺寸、线路（浅/深）、线路为空、搜索无结果、已连接时的线路页、账户（浅/深）、后台服务异常、菜单栏面板（未登录/未连接/已连接，浅/深）。

验收：
- 点击红色关闭按钮或 ⌘W：窗口关闭，Dock 图标消失，⌘Tab 中不出现，菜单栏图标仍在；已连接状态下连接保持（在关闭前后各记录一次 Core 快照，状态与线路不变，用量继续增长）。
- 从菜单栏“打开 AsterLink”、在 Finder 双击应用、⌘,：Dock 图标恢复，窗口出现在最前并获得键盘焦点；任何操作都不会出现第二个主窗口。
- 首次关闭出现提示浮层，第二次不再出现；删除偏好后再次出现。
- 菜单栏面板的连接开关与主窗口按钮在相同状态下启用/禁用一致，操作结果一致。
- ⌘Q、菜单栏“退出”、账户页“退出”均先断开再退出（沿用现有流程的日志/快照证据）；在仅菜单栏模式下执行系统注销不被阻塞。
- 命令序列对比：用 DEBUG 的直连 Core 模式录制同一脚本化操作（启动 → 登录 → 选线路 → 连接 → 关窗 → 打开面板 3 次 → 重新打开窗口 → 断开 → 退出）下 Core 收到的命令序列，与改造前基线比对，除时间外一致；特别确认打开菜单栏面板不增加刷新命令。
- 预览全部场景生成、运行时禁用词检查零命中；浅色、深色截图人工评审；VoiceOver 能读出导航、连接按钮状态与各卡片内容；键盘可完成登录、连接、切换线路。
- 既有 unit、bundle、release 套件全部通过；release 套件的 DMG 检查与密钥扫描通过。
- 无法在 CI 完成的真实连接、真实授权批准、注销场景：在本机按清单实测并截图，做不到的标 BLOCKED（本机无开发者证书时，后台服务批准只能用 ad-hoc 内部包验证）。

### 第 4 轮：Windows 界面重设计与托盘常驻

工作内容：
1. 令牌与样式重建、Tabler 图形资源、外壳与四个页面、引导卡、窗口外观（深色标题栏、Mica 与回退）（第 6.1、6.2、6.5 节）。
2. 关闭即隐藏的统一行为、首次通知、会话结束处理（第 6.4 节）。
3. 托盘菜单重做与主题化（第 6.3 节）。
4. Windows 文案（第 7.3 节）与 AsterLink.Shared 中的展示文字及测试断言；文案守卫加入 Windows。
5. 安装程序图标、中文界面与消息改写（第 6.6 节）。
6. PreviewRunner 场景更新（与第 3 轮清单对齐）并导出每个场景的可见文字；自检页面列表更新。

验收：
- 未登录、未连接、已连接三种状态下分别点“×”与 Alt+F4：窗口隐藏、任务栏按钮消失、托盘图标保留、进程仍在；已连接时连接与用量继续（快照证据）。
- 首次隐藏出现托盘通知且文案包含“^”隐藏图标说明；第二次不出现。
- 托盘左键、托盘菜单“打开”、开始菜单再次启动均恢复同一个窗口；托盘“连接/断开”与主窗口按钮行为一致；托盘“退出”与账户页“退出 AsterLink”先断开再退出。
- 注销与关机时 AsterLink 不出现在“阻止关机”列表中，退出流程在系统时限内完成（授权测试机实测，CI 无法覆盖时标 BLOCKED）。
- 预览：全部场景在 100% 与 200%、浅色与深色下生成，最小尺寸下无横向滚动与文字截断（人工评审 + 可见文字导出中不出现省略号截断的关键字段）；运行时禁用词检查零命中；Windows 10（CI 运行器若为 Server 则以其为准）与 Windows 11 外观各截一套（Mica 只在 11 上出现，10 上回退正常）。
- 讲述人能读出导航、连接状态、按钮名称；键盘可完成全部主流程。
- 既有 Windows CI 全部步骤通过：构建（警告即错误）、单元与合同测试、自检、预览、服务套件、发布套件（测试签名安装、升级、回滚、卸载）；安装向导为中文且使用品牌图标。

### 第 5 轮：发布包去敏

工作内容：
1. 四个平台的打包脚本按第 8.2 节拆分公开目录与 release-private 目录（macOS package-native-macos.py、Windows package-native-windows.py 与 windows_release.py、iOS package-native-ios.py、Android package-native-android.py），SHA256SUMS 只列公开安装包。
2. macOS 构建不再把许可与源码说明放进应用包，Info.plist 删除提交号键，AppDiagnostics 删除内置提交号。
3. 四个平台发布构建停止写入 Mihomo 提交号；Windows Core 构建补去除调试符号；发布构建统一不嵌入版本控制信息。
4. DMG 卸载脚本注释与提示清理。
5. 四个平台发布审计翻转（第 8.2 节第 7 条），建立 D16 二进制残留基线文件并在审计中比对。
6. 工作流的发布工件拆分为公开与内部两个。
7. docs 中各平台发布文档描述的目录结构同步更新（第 6 轮统一校对）。

验收：
- 四个平台的公开发布目录中只有安装包与 SHA256SUMS；release-private 中许可、源码说明、源码归档、组件清单、发布信息齐全且内容与改动前同一提交生成的版本一致（逐文件比对）。
- 展开 DMG 与 .app、Setup.exe 的安装结果（或安装包内的 stage 目录）、IPA、APK：不存在许可与源码文件；Info.plist 无提交号键；文本类文件与 plist 的字符串扫描零命中；二进制命中不超出 D16 基线。
- 发布构建的 Core 握手结果中提交号为空；开发构建不受影响；登录、连接、计费等既有套件在四个平台全部通过（证明 Core 与客户端不依赖该字段）。
- macOS Helper 与 Windows 服务对 Core 的校验在新包上通过（bundle 套件与服务套件）。
- 四个平台翻转后的发布审计在 CI 中通过；故意把一份许可文件放回公开目录或应用包时审计失败。
- CI 的公开工件中不含任何内部资料。
- 做不到的平台项（例如 Windows 正式签名、iOS 真机安装）如实标为 BLOCKED。

### 第 6 轮：全平台回归、守卫全覆盖与发布准备

工作内容：
1. 文案守卫覆盖 DMG 说明、卸载脚本提示、安装程序消息与公开发布目录，确认四平台与 Core 零命中，豁免清单最终评审。
2. 跨平台视觉一致性评审：把四个平台的登录、连接（已连接）、线路、账户/我的四个画面与网站对应页面的浅色、深色截图拼成对照表（放入 PR 与 docs），逐项核对品牌标识、品牌色、状态色、术语、单位、倍率写法一致。
3. 文档更新：docs/windows-client-ui.md、docs/ios-ui.md、docs/android-ui.md 的设计令牌与图标章节，新增 macOS 界面说明，docs/macos-release.md 与 docs/windows-release.md 中的安装说明与截图；生成脚本用法写入文档。
4. 需求方实测清单（第 12 节）交付。

验收：
- 所有平台 CI 全绿；test-brand-assets.py、test-app-copy.py 全部通过。
- 对照表评审通过，记录发现的差异及处理结论。
- 各平台 Release/内部发布包实际安装一次（能做到的平台），通读全部界面文案一次并截图存档。

## 11. 测试资产与证据要求

| 测试 | 位置 | 轮次 |
| --- | --- | --- |
| 品牌资产与色板一致性 | scripts/test-brand-assets.py，ci.yml native-core | 1、2 |
| 静态文案守卫 | scripts/test-app-copy.py，ci.yml native-core | 2–6 |
| 发布包去敏审计（公开目录与应用包内无许可/源码/内核信息，内部目录完整，二进制残留不超基线） | release_suite.py、test-native-ios.py release、android_release_suite.py、release-check.ps1 与 Windows 发布套件 | 5 |
| macOS 图标检查 | release_suite.py 新增 R-ICON | 1 |
| macOS 设计预览、可见文字守卫 | test-native-macos.py --suite ui，ci.yml macos job，上传 PNG | 3 |
| macOS 生命周期命令序列对比 | DEBUG 直连 Core 录制脚本，本机运行，记录进 PR | 3 |
| Windows 设计预览、可见文字守卫、自检 | PreviewRunner 与 windows.yml 现有步骤 | 4 |
| iOS UI 截图与运行时文案检查 | AsterLinkUITests/ScreenshotTests，ios.yml | 1、2 |
| Android 截图与运行时文案检查 | ScreenshotTest（Roborazzi），android.yml | 1、2 |
| Core 提示文字 | native/core 现有 Go 测试（更新断言） | 2 |
| 各平台既有全部套件 | 现有位置 | 每轮 |

截图证据：macOS 存于 artifacts/native-macos 下的 ui 报告目录并作为工件上传；Windows 沿用 windows-ui-preview 工件；iOS、Android 沿用各自报告工件。人工评审的结论（通过/问题清单）写入第 14 节记录。

## 12. 需求方实测清单（接续网站的 U-23）

| 编号 | 内容 |
| --- | --- |
| U-24 | 在 macOS 上安装新版本：Finder、Dock、启动台中图标与网站标签页图标一眼可辨为同一品牌 |
| U-25 | macOS：已连接状态下点关闭按钮，Dock 图标消失、右上角菜单栏图标保留，浏览网页确认网络仍可用；从菜单栏重新打开窗口 |
| U-26 | macOS：重启电脑前在仅菜单栏模式下直接关机或注销，系统不提示 AsterLink 阻止关机；再次打开后可正常登录 |
| U-27 | Windows 10 与 Windows 11 各一台：安装向导为中文且有品牌图标；关闭窗口后在右下角托盘找到 AsterLink（Windows 11 可能在“^”中），右键菜单可连接、断开、退出 |
| U-28 | Windows：已连接状态下关闭窗口，网络仍可用；注销或关机不被阻止 |
| U-29 | iPhone 与 Android 手机：主屏幕图标、启动画面、通知栏/快捷开关图标与网站一致；iOS 深色与着色图标、Android 主题图标显示正常 |
| U-30 | 通读四个客户端的全部页面与常见错误提示（断网、套餐到期、流量用尽、线路已满），确认没有任何开发或技术表述、符合运营口径（这是需求方的最终确认） |
| U-31 | 在纸质说明书中提供开源组件许可声明与源码获取方式（需求方自行负责，D10、D15，资料取自各平台 release-private 目录）；确认四个客户端内任何位置都找不到许可、组件名称或源码相关内容 |
| U-32 | 拿到四个平台对外发布的安装包后，确认发布目录中只有安装包与校验文件；只把公开目录上传到官网，release-private 目录不对外提供 |

## 13. 完成定义

- 第 1 节 A1–A4 全部实现，六轮 PR 均已合并到 main，各平台 CI 全绿。
- 四个平台的软件图标、启动画面、托盘/菜单栏/通知图标与应用内品牌标识全部由 frontend/brand/mark.svg 生成，色板与网站令牌一致，并有测试防止漂移；macOS 图标不再空白。
- macOS、Windows 新界面达到第 5.1 节的设计规格，浅色与深色、键盘与读屏均经过评审；底层模块无功能性改动（以第 5.9、6.7 节的边界和命令序列对比为证）。
- 关闭主窗口后：macOS 缩到菜单栏且不占 Dock，Windows 缩到系统托盘；连接保持；退出流程不变；注销与关机不被阻塞。
- 四个客户端与 Core 的用户可见文案不含第 7.1 节禁用内容，静态与运行时守卫进入 CI。
- 应用包、安装包与公开发布目录中不含许可、源码说明、组件清单与内核提交号，发布审计进入 CI；D16 的二进制残留已记录基线并告知需求方。
- 文档与需求方实测清单已更新交付。
- 每轮的实施记录如实写入第 14 节，未测试项不得标为通过，受环境限制无法完成的项标为 BLOCKED 并写明原因。

## 14. 实施记录

（每轮完成后在此追加：日期、分支、PR、完成内容、偏离本文之处及原因、测试结果逐项 PASS/FAIL/BLOCKED/NOT RUN 与证据位置、遗留问题。）

### 第 1 轮：品牌资产管线与四平台图标

- 日期：2026-10-28。分支 `worktree-appstd-round1-brand-icons`（已删除），PR [#87](https://github.com/StevenWinsir/vpn/pull/87)，squash 合并为 27628bc，合并前 18 项检查全部通过。
- 完成内容：
  - 生成管线：`frontend/scripts/brand-apps.mjs`（npm 脚本 `brand:apps`），复用 `brand-icons.mjs` 的解析与 ICO 打包；输出 `apps/brand-manifest.json`（源文件与每个输出文件的 SHA-256、尺寸）。`scripts/test-brand-assets.py` 加入 ci.yml 的 native-core job。
  - macOS：`apps/macos/Branding/AppIcon.icns`（1024 画布内 824 圆角方块 + 投影，10 个条目，脚本自行打包）；`build-native-macos.py` 在签名前把 Branding 下文件复制进 Resources 并写入 `CFBundleIconFile`；菜单栏三态模板图（未连接描边、已连接实心、出错带感叹号，18 点 1x/2x）替换 `bolt.shield`，随连接状态切换；`release_suite.py` 新增 R-ICON。
  - Windows：`AsterLink.ico`（9 个尺寸）作为 ApplicationIcon 和主窗口图标；三套托盘 ico 嵌入程序集，按 `SystemInformation.SmallIconSize` 加载，显示设置变化时重新加载，删除运行时 GDI 绘制；`setup.iss` 增加 SetupIconFile 与 55/110 像素向导小图（BMP）；`test-native-windows.py` 增加对应断言。
  - iOS：AppIcon 浅色（满版方块、无透明）、深色（透明底、#3dc89a 盾牌）、着色（透明底、白色盾牌）；LaunchMark、LaunchBackground（#f7f8fa / #121a18）、AccentColor（#0d7b61 / #3dc89a）；`BrandMark` 改用 LaunchMark 资源。未改 pbxproj。
  - Android：自适应图标前景与单色层改为盾牌矢量（缩放 1.4，位于安全区内）；新增 `ic_notification` 白色剪影用于通知与快捷开关，删除 `ic_vpn_key`；icon_background 改为 #0D7B61，窗口背景色改为网站令牌；`BrandMark` 使用新前景并去掉 1.5 倍补偿。
- 偏离本文之处：
  - 第 14 节记录本轮未随 PR 提交（本文件当时未入库），改在后续提交中一并入库。
  - 清单中额外记录每个输出文件自身的哈希，用于发现手工改动。
  - 色板（Theme.swift、Theme.kt）仍为蓝色，按第 4.2 节留到第 2 轮；因此本轮后移动端启动画面与应用内主色暂时不一致。
- 测试结果：
  - PASS：`scripts/test-brand-assets.py`；改动 mark.svg 一个颜色而不重新生成时失败，还原后通过（本机）。
  - PASS：`swift build`（apps/macos）；用 ad-hoc 签名的临时应用包启动，Dock 图标与菜单栏图标显示品牌图形（本机截图，未留存入库）。
  - PASS：PR 上 CI 全部 18 项，含 Windows 构建/测试/安装/GUI 冒烟、iOS 构建、Android 构建与模拟器验收、native-core（含新增的品牌资产检查）、macOS 应用包构建。
  - 过程中的失败：Android 构建因 colors.xml 注释含 `--` 失败，已修复（3dd5343）；Windows 服务套件 S01-fake-gui-refused 与客服小组件测试各偶发一次，重跑通过。
  - NOT RUN：macOS 完整签名构建上的 R-ICON（需要子模块与完整发布流程，本机未跑；CI 的 macOS job 通过，但未确认该 job 是否执行 R-ICON）；iOS 模拟器主屏幕浅色/深色/着色截图；Android 圆形/方形遮罩与主题图标截图；Windows 100%/150%/200% 缩放与托盘三态切换的目视确认；macOS 26 上 Finder、启动台、⌘Tab 与“登录项与扩展”的显示（本机系统不是 macOS 26，仅确认 Dock 与菜单栏）。这些项在第 6 轮的跨平台评审或需求方实测（U-24、U-27、U-29）中补做。
- 遗留问题：
  - main 合并后的 CI 中“客服小组件”测试再次偶发失败（调用序列断言过早），与本轮无关，已单独修复，见 PR [#88](https://github.com/StevenWinsir/vpn/pull/88)。
  - 第 2 轮起各平台的色板一致性检查进入 test-brand-assets.py。


### 第 2 轮：移动端色板、共享文案与文案守卫

- 日期：2026-10-28。分支 `worktree-appstd-round2-palette-copy`（已删除），PR [#89](https://github.com/StevenWinsir/vpn/pull/89)，squash 合并为 84779dd，合并前 18 项检查全部通过。
- 完成内容：
  - 色板：iOS `Theme.swift`、Android `Theme.kt`（新增 `BrandTokens`，浅色 `LightTokens`、深色 `DarkTokens`）按第 4.2 节映射改写，蓝色 #2F6FEB 系退役。已连接状态使用品牌绿；填充面（主按钮、连接圆盘、头像）用 `brandFill`，文字与图标用 `brand`（深色为 #3dc89a）；警示与错误各自成套（文字、底、边）。`Banner` 改用对应底色。iOS 的 `Color` 初始化增加透明度参数以表达网站的半透明深色值。
  - 色板测试：`scripts/test-brand-assets.py` 新增 `PALETTE` 映射，解析 globals.css 的 tokens 块，逐项比对 iOS（18 个角色）和 Android（18 个字段）在浅色与深色下的数值，另比对 AccentColor、LaunchBackground、Android 的 colors.xml，并检查旧蓝色已不在主题文件中；新增 11 组文字/底色对比度检查（WCAG AA 4.5）。本机验证：把网站 `--al-warn-text` 改一位后测试失败，还原后通过。
  - Core 文案：`messages.go` 按第 7.2 节改写，并顺带处理 `profile_changed`、`vpn_conflict`（去掉“VPN”）、`session_mismatch`、`connection_not_found` 等表中未列出的条目；错误码、兜底文字、`TerminatesSession`、`InvalidatesConfig` 未改。同步更新 `contract_v3_test.go`、`connection_test.go` 的断言，以及 Windows `PreviewRunner.cs` 与 `ClientTests.cs` 中沿用旧 Core 文字的样例。
  - iOS、AsterLinkKit、隧道扩展、Android（中、英）：按第 7.3 节改写；流量单位改为 GB（`Formatting` 的单位表改为 B/KB/MB/GB，基数不变），删除全部“1 GiB = 2³⁰ 字节”说明；删除节点计量/内部测试线路标签、Core/Mihomo/服务器/渠道各行；“计费 N×”改为“线路倍率 N×”，“本次用量·计费”改为“本次已用 X · 按倍率计 Y”；“普通”线路标签改为“普通线路”（筛选分段仍为“普通”）；登录页脚只显示“版本 x.y.z”，development 构建另显示“开发版”（带豁免标记）；iOS `LicensesView` 与 Android 许可对话框、入口、相关 UI 测试和截图（原 36 号）全部删除；Android `BuildFacts.envFromExample` 字段保留但不再显示。
  - 诊断报告（D9）：iOS `Diagnostics.report(appVersion:osVersion:state:)` 与 Android `Diagnostics.build(appVersion, osVersion, statusLabel, snapshot)` 统一为六行（标题、时间、应用版本、系统版本、连接状态、错误码，无错误时为“无”）。Core 的错误结构没有发生时间，所以“时间”是生成报告的时间。两个平台各有单元测试断言报告不含 Core、Mihomo、代次、API 地址等字段。
  - 文案守卫：`scripts/test-app-copy.py`（加入 ci.yml native-core）扫描 Core `messages.go`、iOS/AsterLinkKit/隧道扩展的字符串字面量（自带解析器，处理插值，跳过注释与 `#if DEBUG` 区域）、Android 两套 strings.xml 与含中文的 Kotlin 字面量。禁用词表为第 7.1 节加上“管理员（账号/权限除外）”“结算”“2³⁰”“1024³”等；英文技术词大小写敏感，Android 英文资源按整词不区分大小写。运行时守卫：iOS `AsterLinkUITests/CopyGuard.swift` 在每次 `capture` 前检查屏幕全部文字，Android `ui/CopyGuard.kt`（src/test）在每次 `shoot` 前遍历未合并语义树检查；脚本会比对两处词表与自身一致。本机 549 个字面量，3 处豁免，0 问题。
  - 其他：`scripts/ios_release.py` 的调试标识增加 `open-tunnel-debug`、`LicensesView`、`licenses-sheet`、`open-licenses`，Release 套件会检查产物中没有这些标识；`scripts/test-native-android.py` 增加 `failure_excerpt`，Gradle 单元测试失败时输出失败用例。
- 豁免项（均带 `copy-guard` 标记）：
  1. iOS `LoginView.swift` 与 Android `login_dev_channel`（中、英）：D8 的“开发版”标记。
  2. Android `role_admin`（中、英）：账号自身的“管理员账号”标记（D13）。
  3. iOS UI 测试守卫另放行 Debug 构建独有的“调试”区块文字（`#if DEBUG`，Release 产物审计已检查不含）。
- 偏离本文之处：
  - 第 7.3 节要求“登录页脚等处删除后”的启动证明：Android 的 emulator 套件与发布包冒烟原先靠登录页显示 Core/Mihomo 版本判断 Core 已加载，现改为“出现登录界面且无启动失败提示、进程存活、无崩溃”，证明力略弱于看到版本号。
  - Android 的 Release 产物没有新增专门的调试界面检查（资源字符串不在 dex 中），改由静态守卫保证 strings.xml 不含许可与调试文字。
- 测试结果：
  - PASS（本机）：AsterLinkKit `swift test` 87 项（含新增诊断测试）；Go `apiclient`、`managed` 测试；`test-brand-assets.py`；`test-app-copy.py`（改入禁用词后失败，还原后通过）。
  - NOT RUN（本机）：iOS App/UI 测试、Android Gradle 测试、Go `engine`/`mobile` 包（工作树缺 Mihomo 子模块），以上由 CI 验证。
  - PASS（CI）：合并前 18 项全部通过，含 iOS 49 个 UI 用例（含运行时文案检查）、Android 单元 106 项与 35 个截图状态、模拟器与发布包冒烟、Windows、后端、网站。
  - 过程中的失败：iOS UI 守卫把 Debug 区块的“隧道调试”文字当违规（已放行）；Android 一处断言仍找旧文字、UI 套件仍要求“licenses”截图、模拟器与发布冒烟仍找页脚版本号（均已改）；Windows 的 `TestLoginSuccessAndSnapshotRedaction`、后端 `TestConsoleUserListScale`、Android 通知速率 N01 偶发失败，重跑通过，与本轮无关。
  - NOT RUN：iOS/Android 截图的人工评审（品牌绿替换是否完整、无残留蓝色、已连接/警示/错误状态可辨），本轮只做了令牌数值与对比度的自动检查，人工评审留到第 6 轮的跨平台评审或需求方实测 U-29。
- 遗留问题：
  - 第 3、4 轮分别把 macOS、Windows 加入 `test-app-copy.py`；Windows 的 Light.xaml 仍是蓝色，按计划在第 4 轮随界面重做，其色板检查也在那时加入 `PALETTE`。
  - macOS 的 `HelperClient` 等处仍有 Core 以外的旧文案，第 3 轮处理。

### 第 3 轮：macOS 界面重设计与菜单栏常驻

- 日期：2026-10-28。分支 `worktree-appstd-round3-macos`（已删除），PR [#91](https://github.com/StevenWinsir/vpn/pull/91)，squash 合并为 ef08f99，合并前 10 项检查全部通过。
- 完成内容：
  - 界面：`Views.swift` 拆为 `Theme`（令牌）、`Components`（品牌标识、卡片、按钮、提示条、状态胶囊、连接按钮、速率图）、`Presentation`（状态文字、主操作与启用条件、阻断原因、线路分组）、`MainWindow`（外壳与侧栏）、`LoginView`（双栏登录与后台服务引导卡）、`ConnectPage`、`LinesPage`、`AccountPage`、`MenuBarPanel`（菜单栏面板与首次关闭提示）。登录前为独立双栏视图（左栏为网站登录页的深色品牌区与首页价值表述），登录后为侧栏（品牌、连接/线路/账户、状态胶囊与邮箱）加内容区；连接页为 156 点圆形连接按钮（未连接中性圆环、连接中品牌色圆弧转动、已连接品牌色实心加光晕、出错危险色圆环，“减少动态效果”时不转动）与三张信息卡（当前线路、套餐、实时速率与 60 秒曲线），每种阻断只给一个提示和一个下一步；线路页为搜索、全部/专线/普通线路分段、刷新与按地区分组的列表（组内保持服务器顺序）；账户页含账户卡与网站入口、外观三档、后台常驻说明、后台服务（D11）、关于与复制诊断、退出登录与退出 AsterLink。
  - 品牌：应用内品牌标识用 `mark.svg` 的原始路径数据绘制（自带 SVG 路径解析），`test-brand-assets.py` 校验路径、圆角、描边宽度与圆点和 `mark.svg` 一致；macOS 色板（第 4.2 节全部角色加 `brandFillHover`、`chartGrid`、登录品牌区四色）加入一致性检查，并检查 `Theme.swift` 以外的视图文件不出现颜色字面量。
  - 窗口与常驻：主窗口改为单实例 `Window`（960×640，最小内容 820×560，去掉“新建窗口”，关闭状态恢复），设置弹窗取消，⌘, 打开主窗口的账户页；`MenuBarExtra` 改为窗口式面板（300 宽：状态、与主窗口同条件的连接开关、当前线路、剩余流量、打开/退出，未登录时只有“未登录”“打开 AsterLink 登录”“退出”）。关闭主窗口后切换为仅菜单栏模式（Dock 图标与 ⌘Tab 消失），菜单栏面板、Finder/启动台再次打开（`applicationShouldHandleReopen`）、⌘, 都先切回普通模式、下一轮事件循环再打开并激活窗口；首次关闭时在菜单栏图标下方显示 6 秒不抢焦点的提示（`closeHintShown` 偏好）。外观偏好（跟随系统/浅色/深色）对主窗口与面板同时生效。
  - 生命周期（第 5.6 节）：`launch`、后台服务注册监听、批准后继续启动、唤醒刷新移到 `AppDelegate`，全程有效；“应用变为活跃时刷新”改为“主窗口成为关键窗口时刷新”，打开面板不再可能触发刷新。退出仍走原 `applicationShouldTerminate` 延迟退出流程。
  - 文案：`HelperClient`、`HelperConnection`、`HelperUpgradePolicy`、`SessionController` 中的“特权服务”“内核”“握手”及英文系统错误详情全部改为“后台服务”说法或一句可操作的话（`HelperUpgradePolicy` 文案不再带两个版本号）；启动失败页改为“应用安装不完整”；`AppDiagnostics` 按 D9 输出与 iOS/Android 相同的六行，删除内置 Mihomo 提交号、通道、服务器字段（Info.plist 的提交号键仍由第 5 轮删除）；流量单位改为 GB，日期改为 yyyy-MM-dd；“节点侧独立计量”“线路配置已校验”“服务器已确认/批次”等删除，改为“预估剩余 · 正在同步用量”“本次已用 X · 按倍率计 Y”。
  - DMG：`安装说明.txt` 改写为安装、首次允许后台服务（含关闭后在菜单栏运行）、升级与卸载三段，不再出现特权服务、预演/--apply、文档路径；DMG 加卷图标（`.VolumeIcon.icns` 加卷根目录自定义图标标记，先做可写映像再转压缩只读映像）。卸载脚本的屏幕提示改写为用户说法，参数与行为不变。`release_suite` 的 R-DMG 增加卷图标与安装说明检查。
  - 系统菜单：Info.plist 声明 `CFBundleDevelopmentRegion`/`CFBundleLocalizations` 为 zh_CN，应用菜单、编辑、窗口等系统菜单显示中文（此前为英文）；R-PLIST 增加该检查。
  - 设计预览：DEBUG 构建的 `AsterLink --preview <目录>` 用脚本化 Core 驱动真实的 `SessionController`，把 28 个场景（登录浅/深、首次授权、连接就绪、未选线路、已连接浅/深、无套餐、到期、流量用尽、连接失败、最小尺寸、线路浅/深、无线路、无搜索结果、已连接时线路页、线路最小尺寸、账户浅/深、后台服务异常、未允许后台、菜单栏未登录/就绪/已连接各浅深）渲染为 PNG，并通过系统辅助功能接口导出每个场景的全部可见文字。`test-native-macos.py --suite ui` 构建 Debug、运行预览、检查场景齐全、期望文字出现，并用 `test-app-copy.py` 的同一套禁用词（桌面端另加“VPN”）检查运行时文字；已加入 ci.yml 的 macos job，预览上传为 `macos-ui-preview` 工件。`test-app-copy.py` 静态扫描加入 macOS 应用与共享模块。
- 偏离本文之处：
  - 关闭主窗口时的单实例与“再次打开”使用 SwiftUI 的 `openWindow`（从菜单栏图标视图取得），不直接操作 NSWindow；`HelperClient` 增加注册状态与安装位置两个注入参数（默认值即原行为），仅供设计预览模拟“未启用/待批准”。
  - 新增两个确认：已连接时更换线路（与 iOS、Windows 一致，确认后调用原 `select`）与退出登录；点击已选中的线路不再重复发出选择命令（原来会重复发送，已连接时会因此断开）。
  - `.toolbar(removing:)` 之外，侧栏宽度用最小 200 点保证字标不换行。
  - 预览使用离屏窗口 `cacheDisplay` 抓图，毛玻璃材质与模糊在图中呈现为平面色，实机为系统材质。
- 测试结果：
  - PASS（本机）：`swift test` 54 项；`test-native-macos.py --suite ui` 4 项（28 个场景，593 条文字，0 命中；在预览样例里写入“内核”后 UI-COPY 失败，还原后通过）；`--suite bundle` 6 项；`--suite release` 12 项通过、2 项 BLOCKED（Gatekeeper 与公证需要 Developer ID，与以往相同），含新的卷图标、安装说明、中文系统菜单检查；`test-app-copy.py`（795 条，5 处豁免，0 问题；把改造前的 `Views.swift` 放回时报 15 处）；`test-brand-assets.py`。
  - PASS（本机，命令序列对比，第 10 节）：用 DEBUG 直连 Core 模式加一个记录命令的脚本化 Core，在改造前（origin/main 84779dd）与改造后的构建上执行同一操作序列（启动并自动登录 → 选线路 → 连接 → 点红色关闭按钮 → 打开菜单栏 3 次 → 重新打开窗口 → 断开 → 退出）。两者 Core 收到的非轮询命令完全一致：login、select_node、connect、refresh_catalog（各自只在窗口重新回到前台时一次）、disconnect、logout；`read_snapshot` 轮询次数随时长不同。打开菜单栏面板 3 次不产生任何命令。为免触碰本机钥匙串，两次构建都在临时副本上打了同一个测试补丁（后台服务视为已启用、凭据放内存并预置测试密码），补丁未提交。
  - PASS（本机实机，同一次运行）：关闭窗口后 Dock 图标消失、菜单栏图标变为已连接实心盾牌、连接与用量持续，首次关闭出现提示浮层，第二次关闭不再出现；从 Finder 再次打开应用后 Dock 图标恢复、窗口回到最前；⌘,（应用菜单“设置…”）打开账户页；外观切换即时生效；仅菜单栏模式下收到系统退出事件时先登出再退出。
  - 过程中发现并修复：线路行与菜单栏行最初用 `accessibilityElement(children: .ignore)` 覆盖了按钮本身，辅助功能无法“按下”它们（VoiceOver 无法选线路），改为保留按钮元素只设置朗读文字；自动登录后系统的密码自动填充浮层残留，登录视图消失时清除焦点。
  - NOT RUN：真实后台服务的系统批准流程与真实连接（本机无 Developer ID，仅 ad-hoc 包）；系统注销/关机时不被阻塞（不能注销本机会话，仅验证了仅菜单栏模式下的退出事件）；VoiceOver 实际朗读与完整键盘操作（只检查了辅助功能树中的名称与按下动作）；浅色、深色截图的人工评审结论留给需求方（CI 工件 `macos-ui-preview`）；macOS 26 上 Icon Composer 套框问题仍按第 4.3 节留到第 6 轮。
  - 注：测试时直接从后台 shell 执行 .app 内的可执行文件，SwiftUI 不会在启动时弹出主窗口（从 Finder 或 `open` 启动正常），这是测试启动方式造成的，与用户使用无关。
- 遗留问题：
  - docs/macos-release.md 等文档中的“特权服务”“设置”描述按第 6 轮统一更新。
  - 卸载脚本注释中的开发说明按第 5 轮清理（本轮只改了屏幕提示）。

### 第 4 轮：Windows 界面重设计与托盘常驻

- 日期：2026-10-28。分支 `worktree-appstd-round4-windows`（已删除），PR [#92](https://github.com/StevenWinsir/vpn/pull/92)，squash 合并为 f1c3d23，合并前 12 项检查全部通过。
- 完成内容：
  - 令牌与样式：`Theme/Light.xaml`、`Dark.xaml` 按第 4.2 节每个角色一个画刷（角色名与 macOS 相同，另加网站的分段选中色 `seg-active-*` 与登录品牌区 `story-*`），蓝色系退役；`Styles.xaml` 重建全部控件模板（卡片含独立阴影层、主/次/文字/危险/图标按钮、导航项、分段控件、输入框含占位提示、复选框、折叠区、进度条、细滚动条、提示框），统一 8 DIP 栅格、卡片 12 与控件 8 圆角、键盘焦点为品牌焦点色描边。`Icons.xaml` 由网站同一 npm 包（Tabler Icons 3.48.0，MIT）的轮廓图形生成 26 个几何资源；`Brand.xaml` 用 `mark.svg` 的原始路径绘制品牌标识与连接按钮中心的盾牌。`test-brand-assets.py` 新增 Windows 色板（浅/深各 24 个角色）、品牌几何与“主题文件以外无颜色字面量”检查。
  - 外壳与页面（与第 3 轮 macOS 同一布局与文案）：侧栏（品牌标识与字标、连接/线路/账户、状态胶囊、邮箱、管理员账号标记）+ 内容区（页标题、横幅提示）。连接页：顶部至多一条提示（购买套餐/选择线路/刷新三选一），164 DIP 圆形连接按钮（未连接中性圆环、连接中品牌色圆弧转动且遵守系统“动画效果”开关、已连接品牌色实心加光晕、出错危险色），状态与说明，三张信息卡（当前线路、套餐、实时速率与 60 秒曲线），卡片由 `CardGrid` 自适应排列（默认窗口三列，最小窗口两行，不出现横向滚动）。线路页：搜索、全部/专线/普通线路、按地区分组（组内保持服务器顺序，`NodeList.Group`）、选中标记加“已选”文字、各种空状态各给一个下一步。账户页：账户卡与网站入口、外观三档、通用（后台运行说明）、后台服务（正常一行；异常为引导卡；“故障排查”折叠区）、关于（版本、复制诊断信息、访问官网）、退出登录与退出 AsterLink。登录页：双栏（网站深色品牌叙述 + 登录卡，含显示密码按钮），后台服务未就绪时在登录卡上方显示引导卡（第 6.5 节：一句原因、一个主按钮、“重新检测”）。
  - 窗口外观：`WindowBackdrop` 设置深色标题栏（DWM 属性 20），Windows 11 22H2 起启用 Mica 并让侧栏透出，Windows 10 或调用失败时保持纯色；主题切换时同步重设。字体为 Segoe UI Variable / Segoe UI，中文固定为 Microsoft YaHei UI。窗口默认 1000×680、最小 820×560。“跟随系统”时随 Windows 浅深色切换实时变化。
  - 关闭与托盘（第 6.4 节）：任何状态下“×”、Alt+F4、任务栏“关闭窗口”都取消关闭并隐藏到托盘（删除“未连接时直接退出”分支）；首次隐藏弹一次托盘通知，含“^”隐藏图标说明（沿用 TrayNoticeShown）。系统注销/关机时 `SessionEnding` 标记正在退出，在至多 4 秒内运行与“退出”相同的断开与同步流程（嵌套消息循环），之后由 WPF 结束程序；做不完时由服务在管道断开后停止连接（原有兜底）。托盘菜单：状态行（如“已连接 · 东京专线 01”）、打开 AsterLink、连接/断开（启用条件与原来相同）、退出；自绘渲染跟随浅深色、品牌色悬停，Windows 11 上为圆角；托盘图标三态与提示文字只在变化时更新。
  - 文案（第 7.3 节）：`Presentation.cs` 状态说明、`ErrorText.ForService`（不再显示英文异常信息）、`ServiceHealth.cs`、`ServicePanel`（“特权服务”全部改为“后台服务”，删除服务/内核/渠道/内核状态与错误代码行，动作为“安装/修复/启动（需要管理员权限）”）、`SessionController` 的提示、凭据管理器警告精简、“操作失败（异常类型名）”改为“操作失败，请重试”；删除设置页的系统事实与自检列表、“应用·渠道·内核”“服务器”、线路页说明、“节点计量”“已校验”、计量说明与“1 GiB = 2³⁰ 字节”；单位改为 GB（基数 1024 不变），日期为 yyyy-MM-dd；版本只显示“版本 x.y.z”（development 构建加“开发版”，带豁免标记）。`DiagnosticsReport` 改为与其他三个平台相同的六行（D9），系统版本写为“Windows 11 (10.0.26100)”这类。
  - 文案守卫：`test-app-copy.py` 加入 Windows：XAML 的 Text/Content/Header/ToolTip/Title/无障碍名称与说明/占位提示与 `sys:String`，App 与 Shared 展示层中含中文的 C# 字面量（插值内容也一并检查），以及安装程序的自定义消息与其中文语言文件。
  - 安装程序（第 6.6 节）：界面改为简体中文，只保留中文一种语言。Inno Setup 6.7.1 不自带官方简体中文，`Packaging/Languages/ChineseSimplified.isl` 取自厂商源码仓库 jrsoftware/issrc 标签 is-6_7_1 的社区译本（与该版本 Default.isl 的消息逐条对齐，无缺项），UTF-8 带 BOM；为符合第 7.1 节改写了其中 9 条（架构、管理员身份、内部错误、错误代码、签名文件等措辞），文件头逐条列出。自定义消息只保留中文并改写：“请先从托盘退出”的说明含“^”，安装/卸载失败只显示错误码，服务动词的原始输出只写安装日志。安装、升级、回滚、卸载步骤与校验未改。
  - 设计预览（第 10 节）：`--preview` 改为 24 个页面场景（与 macOS 对齐：登录与三种后台服务状态、最小尺寸、连接页 9 种状态、线路页 6 种、账户页 4 种），每个场景浅色与深色各一张，3 个代表场景另有浅/深 200%，另加托盘菜单（未登录/未连接/已连接，浅/深，用真实渲染器绘制）共 60 张；再在真实桌面上显示带系统标题栏的窗口截屏两张（浅/深，记录是否启用 Mica）。同时导出每个场景的全部可见文字、无障碍名称与提示（`texts.json`）以及单行文字被截断的清单。`test-native-windows.py --suite ui` 检查场景齐全、200% 尺寸、最小尺寸、期望文字、运行时禁用词（与静态守卫同一词表，另加“VPN”）与无截断，已加入 windows.yml。`--self-test` 的页面列表改为 login/connect/lines/account，并向窗口发送真实的 WM_CLOSE（“×”、Alt+F4 与任务栏关闭发出的同一消息），断言窗口隐藏而非关闭。
- 偏离本文之处：
  - 线路页筛选分段使用“普通线路”全称（与 macOS 一致），不缩写为“普通”。
  - 托盘菜单与首次通知未做“已连接”以外的额外状态图形；状态以文字行表达。
  - 预览中的高页面（账户页）不是一张长图，而是顶部与滚动到底部各一张：CI 屏幕高 768，带框窗口会被系统限制在屏幕内。
  - 设置页移除的“自检”列表仍在 `--self-test` 中运行并写入报告，不进入界面与诊断报告（第 7.3 节原意）。
  - Tabler 图形为 MIT 许可，已在资源文件头注明来源；按 D10 不在应用内展示，需求方纸质许可清单需包含 Tabler Icons（第 5 轮整理 release-private 时一并核对）。
- 测试结果：
  - PASS（本机 macOS，.NET SDK 10.0.401）：整个解决方案 Release 构建，0 警告（警告即错误）；`AsterLink.Tests` 341 项（新增 GB 单位、按倍率计、日期、地区分组、状态文字无内部术语、六行诊断、后台服务文字无内部词等）；`test-brand-assets.py`（含 88 项 Windows 检查）；`test-app-copy.py` 1,344 条、6 处豁免、0 问题（把改造前的 Presentation.cs、AppViewModel.cs、SettingsView.xaml 放回时报 42 处）。
  - PASS（CI，Windows Server 2025 build 26100 运行器，PR #92 最后一次运行）：Windows job 全部步骤，含构建、单元与合同测试、自检（窗口渲染、四页布局、`hides_on_close: true`、`backdrop: mica`）、设计预览与 ui 套件（场景全部生成、1,354 条可见文字 0 命中、0 处截断、真实桌面窗口两张均为 Mica）、Core/计费/授权套件、服务套件（真实安装服务、GUI→服务→Core、攻击用例）、发布套件（测试签名的中文安装包安装、重装、拒绝、回滚、卸载）。其余平台检查见 PR。
  - NOT RUN / BLOCKED：在真实 Windows 10、Windows 11 桌面上手动点击“×”、Alt+F4、任务栏关闭后从托盘恢复，托盘右键菜单的连接/断开/退出，首次托盘通知的实际显示，开始菜单再次启动唤起同一窗口（CI 只验证了 WM_CLOSE 的隐藏行为与菜单绘制）；系统注销/关机时不出现在“阻止关机”列表（需授权测试机）；已连接状态关闭窗口后连接与用量持续（需真实节点）；讲述人朗读与完整键盘操作（只保证了无障碍名称与焦点样式）；Windows 10 上的外观（CI 运行器为 Windows 11 内核的 Server 2025，Mica 已启用，Windows 10 的纯色回退未截图）；100%/150% 以外缩放与托盘图标在 150% 下的目视确认；浅色、深色截图的人工评审结论留给需求方（CI 工件 `windows-ui-preview`）。以上对应需求方实测 U-27、U-28。
- 遗留问题：
  - docs/windows-client-ui.md 只在开头加了更新说明，全文按第 6 轮统一改写。
  - 安装程序语言文件如升级 Inno Setup 需重新对齐消息并复核措辞。

### 第 5 轮：发布包去敏

- 日期：2026-10-28。分支 `worktree-appstd-round5-release-scrub`，PR [#93](https://github.com/StevenWinsir/vpn/pull/93)。
- 完成内容：
  - 共享审计 `scripts/release_scrub.py`（只用标准库）：公开目录检查（只允许安装包与只列安装包的 `SHA256SUMS`）、内部目录完整性检查、展开后的应用包扫描与自检。文本类文件（含二进制 plist，解码后扫描）不得出现 mihomo、clash、metacubex、sing-tun、GNU General Public（不区分大小写）与整词 GPL；二进制按 ASCII 与 UTF-16LE 统计 mihomo、Mihomo、MIHOMO、clash、Clash、metacubex、sing-tun、GNU GENERAL PUBLIC（标记前不能紧跟字母，避免把 .NET 运行时自带的 `…ExtensionClash` 这类标识符算进去）；固定的内核提交号和构建机路径（本检出目录与用户主目录）在任何文件中都不允许出现。`--self-test` 逐项植入（公开目录中的许可文件、plist 提交号键、许可文字、基线外二进制标记、UTF-16 标记、提交号、构建路径、zip 内的许可）并要求都被发现，已加入 ci.yml native-core job，各平台审计也各跑一次。
  - D16 基线 `scripts/release-scrub-baseline.json`：按平台与路径列出允许的残留。Core 二进制（macOS `asterlink-core`、Windows `core/asterlink-core.exe`、iOS 两个可执行文件、Android `libgojni.so`）允许 Go 包路径类标记；客户端二进制（macOS App 与 Helper、Windows 的 `AsterLink.Shared.dll`/`AsterLink.Service.dll`、Android `classes*.dex`）只允许 mihomo/Mihomo，来自 IPC 字段名 `mihomo_revision`。基线外的文件或标记一律失败；提交号与构建路径不能写进基线。
  - 目录拆分（第 8.2 节）：四个平台的公开目录（macOS `release/`、Windows `--release-dir`、iOS 与 Android `release/`）只剩安装包与 `SHA256SUMS`；许可、`SOURCE.txt`、两个源码归档、组件清单、`release-info.json` / `release-manifest.json`、Windows 的 `manifest.json` 副本移入同级 `<目录>-private`（macOS、iOS、Android 为 `release-private/`，Windows 为 `installer-private/`），文件内容与生成方式不变，另有 `README-内部资料.txt`（“内部资料，不得发布”）与内部目录自身的 `SHA256SUMS`。macOS 的公证回执也写入内部目录。原 iOS 的 dSYM、Android 的 R8 映射与 `website-env.txt` 位置不变。
  - 应用包内：macOS 构建不再把许可与源码说明放进 Resources（改写到构建目录的 `legal/`，由打包脚本移入内部目录），Info.plist 删除 `AsterLinkMihomoRevision`。
  - 内核提交号：Release 构建（macOS Release 与 `--internal`、Windows 全部 stage、iOS 打包、Android Release APK）不再写入；Debug 与测试构建照常写入。记录文件增加 `core_revision_stamped` / `revision_stamped`，iOS 与 Android 的打包和构建脚本据此判断能否复用已有框架/AAR（发布包不会用到开发版 Core，开发构建也不会复用发布版）。提交号仍记录在内部目录的发布信息中。
  - 构建选项：Go Core 统一 `-trimpath -buildvcs=false -ldflags "-s -w …"`（Windows 补上 `-s -w`）；gomobile 通过固定的中性路径 `/tmp/asterlink-src`（指向检出目录的符号链接）绑定，模块信息里不再出现构建机目录；macOS Release 的 App 与 Helper 在签名前 `strip -S -x`（与 Xcode Release 的“剥离产品”一致）；Windows 发布时 `DebugType=none`、关闭 Source Link、产品版本不再带“+提交号”。
  - DMG 卸载脚本：注释改为面向用户的三行说明，删除开发说明、文档与源码路径引用；参数与行为不变（R02-UNINSTALL 通过）。
  - 审计翻转：macOS `release_suite` 删除 R-LICENSE，新增 R-PUBLIC、R-PRIVATE、R-UNSTAMPED、R-SCRUB-BUNDLE、R-SCRUB-SELF，R-PLIST 检查无提交号键，R-MANIFEST 读取内部目录并校验两个 `SHA256SUMS`，R-DMG 扫描挂载后的整个 DMG 与卸载脚本；iOS `R03.files` 与 `R03.licenses` 改读内部目录，`R03.core_identity` 改为“两个二进制都不含提交号”，新增 `R03.scrub` 与 `R03.scrub_self`；Android 新增 `release.R03-public`、每个 APK 的 `release.R03-<abi>-scrub` 与 `release.R03-scrub-self`，许可与源码检查改读内部目录；Windows `release-check.ps1` 增加 `-PrivateDir`（默认 `<ReleaseDir>-private`），新增 R04-public-installer-only 与 R04-private-complete，`test-native-windows.py --suite release` 新增 `release.scrub_public/private/stage/self`（扫描安装包携带的整个 stage，即装进 Program Files 的全部文件）。
  - CI 工件：公开工件只含安装包与 `SHA256SUMS`（`asterlink-internal-adhoc-dmg`、`ios-unsigned-ipa`、`windows-installer-test-signed`；Android 的测试签名 APK 本来就不保留）；内部资料另传名字带 `INTERNAL` 的工件（`macos-release-private-INTERNAL`、`ios-release-private-INTERNAL`、`android-release-private-INTERNAL`、`windows-release-private-INTERNAL`；CI 中不含源码归档以节省空间）；`release-macos.yml` 把正式发布的公开与内部资料分成两个工件。
  - 文档：四份发布文档中描述目录结构的段落与用例表已同步，全文校对留到第 6 轮。
- 偏离本文之处：
  - 第 8.2 节第 4 条原想只改链接参数：Go 会把被 `-X` 覆盖的默认字符串仍留在二进制里（实测去掉链接参数后 Core 中仍有一处提交号），所以 `main.go` 与 `mobile.go` 中 `mihomoRevision` 的默认值改为空字符串，开发构建由脚本写入。这是构建参数默认值，不是逻辑改动；握手字段与 IPC 协议不变。
  - 本文未列出、实测发现后一并处理的三处：Windows 自带 PDB 中的 Source Link 映射写有仓库地址、内核子模块地址与两个提交号，.NET 程序集的产品版本带“+提交号”；macOS Release 的 Swift 二进制带构建机源码路径；gomobile 的模块信息带构建机目录。处理方式见上文“构建选项”。
  - Windows 的内部目录名为 `installer-private`（跟随现有 `--release-dir` 目录名加 `-private`），不叫 `release-private`。
  - 卸载脚本里供沙箱测试用的两个环境变量（`ASTERLINK_UNINSTALL_ROOT`、`ASTERLINK_UNINSTALL_SKIP_KEYCHAIN`）属于脚本行为，保留。
- 测试结果：
  - PASS（本机）：`release_scrub.py --self-test`；`test-native-macos.py --suite release` 16 项通过、2 项 BLOCKED（Gatekeeper 与公证需要 Developer ID，与以往相同），含新增 R-PUBLIC、R-PRIVATE、R-UNSTAMPED（Core `--version` 只输出 `0.1.0`）、R-SCRUB-BUNDLE、R-SCRUB-SELF；在公开目录放回一份许可文件后 R-PUBLIC 失败。用改动前（f1c3d23）的构建与打包脚本在同一检出上再打一次包，内部目录中的 `THIRD_PARTY_LICENSES.txt`、`SOURCE.txt`、两个源码归档与旧公开目录中的同名文件逐字节相同，发布清单的键与值相同（DMG 与二进制哈希除外）；用新扫描器扫描旧应用包报 4 处（Info.plist 提交号键、两份文本、Core 中的提交号）。剥离后的 Release 应用能正常启动。`--suite bundle` 6 项通过（Debug 的 Core 仍报告提交号，后台服务握手与篡改拒绝不受影响）。
  - PASS（本机）：`package-native-ios.py` + `test-native-ios.py --suite release`，R03 全部 13 项通过，4 项 BLOCKED（需付费 Apple 账号与真机，与以往相同）；首次运行时 `R03.scrub` 发现两个可执行文件中各有 12 处构建机路径（gomobile 模块信息），改为中性路径绑定后通过。
  - PASS（本机，.NET SDK 10.0.401 交叉发布 win-x64 的 stage）：扫描 708 个安装文件无问题；首次扫描发现 3 个 PDB 中的 Source Link 映射（含提交号）与 Microsoft 资源文件中的 `…Clash` 标识符，前者通过不生成 PDB 解决，后者通过“标记前不能紧跟字母”排除。`scrub_release` 用模拟的公开/内部目录跑通，在公开目录放回 `SOURCE.txt` 后 `scrub_public` 与 `scrub_stage` 失败。
  - PASS（本机）：Go `cmd/...`、`mobile`、`internal/ipc` 测试；`test-app-copy.py`（1,344 条，0 问题）；全部工作流 YAML 可解析。
  - CI：见 PR [#93](https://github.com/StevenWinsir/vpn/pull/93) 的检查列表（合并时补记结果）。
  - NOT RUN（本机）：Android 打包与审计（本机无 JDK/Android SDK，由 CI 验证）；Windows 安装程序构建与 `release-check.ps1`（需要 Windows，由 CI 验证）。
  - BLOCKED：Windows 正式签名、iOS 签名与真机安装、macOS Developer ID 与公证，与以往相同。
- 遗留问题：
  - D16 残留（已记入基线）：Core 二进制中的 Go 包路径（含 `github.com/metacubex/mihomo`、`sing-tun`、子模块目录名 `Clash.Meta`）与模块信息；客户端二进制中的 IPC 字段名 `mihomo_revision`。需求方需要时另行评估混淆方案。
  - 网站下载页（frontend `download-view.tsx`）的“开源许可与源代码”段仍写有内核名称与许可，并说许可与源码“随安装包一同发布”，与 D15 不再一致；网站不在本计划范围内，是否修改由需求方决定。
  - 发布文档全文校对按计划在第 6 轮完成。
