# AsterLink VPN：网站“使用教程”板块技术规划交接书

> 文件：handoff_guide.md
> 编写日期：2026-11-03
> 代码基线：项目 /Users/stevenlee/Desktop/vpn，main，HEAD 1dc51f0（#106 合并后）。以本次读取的工作区源码与 docs/ 为准。
> 文档性质：实施规划与验收约定。本文不代表任何功能已开发或已测试，全文不含代码示例。

## 1. 目标、范围与轮次总览

在网站用户中心加入“使用教程”板块：

1. **入口**：左侧栏“下载客户端”的紧下方新增“使用教程”，位置在“下载客户端”与“账号设置”之间。
2. **内容**：点进去后按平台切换 Windows、macOS、Android、iPhone（iOS）四套教程，全部围绕我们自己的专属客户端来写：下载安装、首次授权、登录、选线路、连接、查看用量、断开与退出、升级卸载、常见问题。每一步配截图，让完全不懂技术的新用户照着做就能用起来。

架构不变：教程是纯网站静态内容，不读写后端、不新增接口、不新增数据库表，与客户端计费、节点、租约、计量没有任何交集。**本期不改任何 APP 的产品代码**；为了拿到干净的教程截图，只允许改动 APP 的截图测试代码（第 4 节）。

采用 **3 轮**实施。每轮一个 worktree 分支和一个 PR，分支命名为 worktree-guide-roundN-*，每轮的记录追加到本文第 15 节。

| 轮次 | 可验收结果 | 依赖 |
| --- | --- | --- |
| 第 1 轮 | 页面与文字教程：/guide 路由、侧栏入口、平台切换与深链、四个平台完整文字步骤与常见问题、内容数据模块与单元测试、下载页互链。此时已可上线（纯文字版） | 无 |
| 第 2 轮 | APP 截图：从四个客户端现有的界面截图套件挑选场景，统一处理成网站用图（尺寸、格式、带哈希的文件名、清单），页面上加入截图、编号高亮、点击放大；截图隐私与文案检查 | 第 1 轮 |
| 第 3 轮 | 系统画面补齐与收尾：真机拍摄的系统弹窗截图（VPN 授权、安装拦截、后台批准等）按同一流程入库；全站回归、暗色与手机布局复核、维护文档、用户实测清单 | 前 2 轮；系统截图需要需求方真机配合 |

本期不做：
- 在下载页上架 macOS / Windows 安装包。docs/macos-release.md 与 docs/windows-release.md 写明桌面端目前只有临时签名 / 未受信签名的内部通道，“不可分发给用户”；教程只如实说明“从下载页获取，未列出时联系客服”，见 D6。
- 管理员在后台在线编辑教程（教程与 APP 版本强绑定，随代码发布更合适，见 D3）。
- 视频教程、动图、英文版。
- APP 内加“使用教程”入口（需要改四个 APP 并重新发版，可作为后续单独任务）。
- 修改现有下载页的“开源许可与源代码”段落（与 handoff_app_standardization.md D10 的口径不一致，属于另一件事，第 13 节登记为观察项）。

## 2. 已核实的代码基础与必须衔接的位置

### 2.1 网站（frontend，Next.js 16 + React 19 + Mantine 8）

- **侧栏**：frontend/src/components/site-shell.tsx 顶部的 links 数组定义用户中心导航，顺序为 总览、节点列表、套餐与订阅、订单记录、推荐有礼、我的工单、下载客户端、账号设置。“使用教程”插在“下载客户端”之后。Sidebar 里对推荐有礼、我的工单有按 /meta 开关过滤的逻辑，“使用教程”不受任何开关控制，始终显示。管理员的导航是 userLinks 加上管理后台两项，所以管理员也能看到。
- **工作区判定**：同文件 SiteShell 里的 workspace 白名单（'/dashboard' … '/download'、'/account'、'/pay/result'）决定是否渲染左侧栏。新路由 /guide 必须加入该白名单，否则页面没有侧栏。
- **高亮规则**：current() 对普通链接是“路径完全相等”。因此教程必须是单一路由 /guide，平台用查询参数区分（与 /tickets 用 ?id= 保持侧栏高亮的做法一致，handoff_ticket.md 2.2），不能用 /guide/ios 这类子路由。
- **下载页**：frontend/src/app/download/page.tsx 是服务端组件，每次请求读取 frontend/.env 中的 ANDROID_* 与 IOS_*（frontend/src/lib/downloads.ts 的 androidDownload、iosDownload，任一字段缺失或格式不对就隐藏下载）。下载页**没有包 RequireAuth**，未登录也能访问并显示侧栏；页面里只有 Android 与 iPhone 两张卡，没有桌面端。
- **登录守卫**：其余用户页（如 tickets、account）用 RequireAuth 包裹。
- **文案守卫**：frontend/scripts/copy-guard.test.mjs（源码扫描，npm run test:copy）与 frontend/tests/copy-words.ts（运行时，ui-copy.spec、home.spec 使用）维护同一份禁用词：开发、联调、测试、模拟、演示、后端、前端、数据库、快照、服务端、计量、心跳、联系管理员、管理员配置，以及广告法相关的“最快、零日志、不限速、翻墙、科学上网”等。放行短语只有“未受信任的开发者”“信任该开发者”（苹果系统原文）。新页面全部用户可见文字都受它约束。
- **测试与 CI**：.github/workflows/ci.yml 的 Web validation 依次跑 lint、webpack 构建、typecheck 和一串 npm run test:*（node --test，纯函数模块用 --experimental-strip-types 直接测 TS）。浏览器用例分三个配置：playwright.payment.config.ts（ui-copy、theme、home、support-widget 等，由 scripts/test-payment-web.py 驱动）、playwright.catalog.config.ts、playwright.config.ts（发布演练）。
- **图片**：网站目前没有任何 next/image 或 img 用法，public 下只有 icons。部署方式是 next start 后面挂 nginx（docs/deployment.md），未启用 standalone。
- **主题**：网站有浅色/深色切换（handoff_ui.md 第 2 轮），颜色令牌在 globals.css 的 --al-*。
- **客服**：support-widget.tsx 的 useSupportAccess 决定是否提供 Crisp 在线客服；工单入口受 /meta 的 tickets_enabled 控制，且需要登录。

### 2.2 四个客户端的界面事实（教程内容的依据）

术语以 APP 为准：APP 用“线路”“账户/我的”“线路倍率 N×”“专线/普通线路”；网站侧栏叫“节点列表”“账号设置”（docs/brand-and-copy.md 第 4 节已登记这一差异）。桌面端界面里不出现“VPN”字样，移动端有（系统弹窗本身就叫 VPN）。

| 平台 | 系统要求 | 安装方式 | 首次授权 | 主界面 | 常驻与退出 |
| --- | --- | --- | --- | --- | --- |
| Windows | Windows 10 2004（19041）及以上 / Windows 11，仅 x64 | AsterLink-Setup-x64-版本.exe，一次 UAC；可勾选“启动 AsterLink” | 登录页上方“后台服务”引导卡：安装/修复/启动（需管理员权限）+“重新检测” | 侧栏：连接 / 线路 / 账户 | 关闭窗口任何状态都隐藏到托盘（首次有托盘通知，含“^”隐藏图标说明）；真正退出只有托盘“退出”和账户页“退出 AsterLink” |
| macOS | macOS 15 及以上，DMG 为 arm64（Apple 芯片） | 打开 DMG，把 AsterLink 拖到“应用程序” | 登录页“首次使用”卡点“启用”→ 系统设置 › 通用 › 登录项与扩展 › “允许在后台”打开 AsterLink，回到 App 自动继续 | 侧栏：连接 / 线路 / 账户 | 关闭主窗口后只留菜单栏图标、Dock 消失；退出：菜单栏“退出”、⌘Q、账户页“退出 AsterLink” |
| Android | Android 8.0 及以上（minSdk 26），arm64-v8a | 浏览器下载 APK，允许该浏览器“安装未知应用” | 首次连接系统询问 VPN 连接请求→“允许”；Android 13 起询问通知权限 | 底部三标签：连接 / 线路 / 我的 | 通知栏常驻显示状态与速率；有快捷开关；“我的 → 后台运行与省电”；不支持系统“始终开启” |
| iPhone | iOS 17 及以上，仅 iPhone | 用 Safari 打开下载页点“在 iPhone 上安装”（itms-services），仅限已登记设备（Ad Hoc） | 首次连接先出现 App 内说明页“首次连接需要添加 VPN 配置”→ 继续 → 系统弹窗“想要添加 VPN 配置”→“允许”→ 输入锁屏密码 | 底部三标签：连接 / 线路 / 我的 | 后台断开（额度、到期）以通知提醒；“我的”页可看 VPN 配置状态 |

四端共同的界面语义（教程按这些写）：
- 登录：邮箱、密码（可显隐）、“记住密码”（只存在本机系统钥匙串/凭据库）、忘记密码与注册跳网站。
- 连接页：大圆形连接按钮，状态词为 未连接 / 正在准备 / 正在连接 / 已连接（带时长）/ 正在断开 / 无法连接；下面是当前线路卡、套餐卡（剩余流量、到期、“正在同步用量”）、实时速率曲线；连接后显示“本次已用 X · 按倍率计 Y”。
- 每种阻断只给一条提示和一个下一步：无套餐、未选线路、套餐到期、流量用尽、设备数达到上限（“已达到同时在线设备上限，请先断开其他设备”）、连接失败、需要更新。
- 线路页：搜索、全部/专线/普通线路筛选、按地区分组、线路倍率 N×、已选标记；已连接时换线会先确认“将断开当前连接”。
- 账户/我的页：账户与套餐信息、外观、关于（只显示版本）、“复制诊断信息”（六行，不含隐私）、退出登录（会删除记住的密码）。

### 2.3 现成的 APP 截图来源（第 2 轮的素材）

四个客户端都已有用虚构数据渲染界面的截图套件，不连服务、不碰真实账号，邮箱为 user@example.com / vip@example.com，线路名为“香港专线 01”“东京专线 01”等虚构名称：

| 平台 | 入口 | 产出 |
| --- | --- | --- |
| macOS | scripts/test-native-macos.py --suite ui（Debug 构建的 AsterLink --preview） | 28 个场景 PNG（login-light、login-service-setup、connect-ready、connect-connected-light、lines-light、account-light、menubar-ready-light、menubar-connected-light 等）+ texts.json（每个场景全部可见文字） |
| Windows | scripts/test-native-windows.py --suite ui（只能在 Windows 上跑；CI 的 windows-ui-preview 产物保留 14 天） | 24 个场景浅/深各一张、3 张 200%、托盘菜单 6 张，另有带真实标题栏的窗口截屏 2 张；texts.json |
| iOS | scripts/test-native-ios.py --suite ui（ScreenshotTests，ASTER_UI_FIXTURE 预置状态） | 约 50 张，含 test01_login、test06_connectReady、test15_connectConnected、test25_lines、test32_account、test47 首次 VPN 配置说明页 |
| Android | scripts/test-native-android.py --suite ui（Roborazzi） | 30+ 张，含 01-login、10-connect-ready、12-connect-connected、17-lines、20-account、16-connect-vpn-denied |

已知的坑（第 2 轮必须处理）：
- Android 的 ScreenshotTest 用 channel = development 的构建信息，登录页页脚会出现“开发版”标记；iOS 的 UI 测试跑在 Debug 构建，同样可能出现。教程截图里不得出现“开发版”，需要在截图测试里增加“发布外观”的教程专用场景（只改测试代码），或改用不含页脚的场景。
- iOS 模拟器截图带状态栏（时间、运营商），拍摄前统一覆盖为固定值（9:41、满电、满信号）。
- 这些截图套件只覆盖 APP 自己的界面。**系统画面**（Android 的 VPN 连接请求与“安装未知应用”、iOS 的“添加 VPN 配置”与安装确认、macOS 的“登录项与扩展”与安装拦截、Windows 的 UAC 与 SmartScreen）无法自动生成，只能真机拍摄（第 3 轮）。

## 3. 关键决策（默认值，需求方可在第 1 轮开工前推翻）

| 编号 | 决策 | 理由 |
| --- | --- | --- |
| D1 | 路由为 /guide，平台用查询参数 platform（取值 windows、macos、android、ios），非法或缺省时按访客设备自动选择 | 侧栏高亮按路径全等匹配；查询参数便于下载页、客服、工单回复直接发深链 |
| D2 | /guide **不要求登录**，与 /download 一致：未登录可访问，侧栏照常显示（**需求方 2026-11-03 已确认**）；页面不得包 RequireAuth | 用户原话“登录后在左边栏可以看到”同样满足；新用户在注册前、或 APP 登录不上时也能看教程，客服也能直接发链接 |
| D3 | 教程内容是代码库里的结构化数据（一个纯 TS 数据模块），随网站发布，不进数据库、不做后台编辑 | 截图与 APP 版本绑定，必须和代码一起评审；纯数据便于单元测试（结构完整、图片存在、文案守卫） |
| D4 | 一个页面 = 平台切换 + 该平台的分节步骤；只渲染当前平台（切换时卸载其他平台），图片懒加载 | 控制首屏体积；新手只需要看自己的设备 |
| D5 | 截图只用浅色版；深色模式下给截图加细边框与圆角，不另做深色图 | 体积减半，维护量减半；截图本身就是“设备画面”，与网站主题无关 |
| D6 | 桌面端教程照写；“下载”一步按实际可用性显示：下载页有该平台安装包时给“前往下载”按钮，否则显示“安装包请联系客服获取” | 桌面端公开发布尚未批准（签名/公证 BLOCKED），网站不能自己“变”出下载链接；可用性复用 downloads.ts 的判断，不重复写规则 |
| D7 | 截图上的“点这里”提示用网页自己画的编号圆圈/高亮框（坐标以百分比存在数据里），不在图片上 P 标注 | 换图不必重新标注；圆圈颜色跟随主题；编号同时出现在文字步骤里，读屏用户靠文字也能完成 |
| D8 | 图片由脚本从截图套件产物统一处理：裁切、缩放、转 WebP、文件名带内容哈希、写清单；处理后的图片与清单入库，CI 不重新生成，只校验 | 与 brand:apps 的做法一致（docs/brand-and-copy.md）；带哈希的文件名可以长缓存且换图立即生效 |
| D9 | 页面图片用 next/image 并关闭运行时优化（unoptimized），尺寸取自清单 | 图片已预处理；不依赖服务器上的 sharp，也不占 Next 进程 CPU；明确宽高避免布局跳动 |
| D10 | 桌面端教程正文不出现“VPN”“内核”“协议”“TUN”“Helper”等词，统一用 APP 的说法（“后台服务”“连接”）；移动端可以写 VPN（系统弹窗原文） | 与 APP 文案规范（handoff_app_standardization.md 7.1）一致，用户看到的词和 APP 里一模一样 |
| D11 | 截图下方统一注明“示意图，线路名称与数据以实际为准” | 截图里的线路、套餐、流量都是虚构数据 |
| D12 | 只做简体中文 | 网站与 APP 主语言 |

## 4. 内容规划（四个平台）

### 4.1 统一的页面骨架

每个平台的教程都按同一套章节组织，方便用户换设备时找得到：

1. **适用设备**：一行写清系统版本与芯片要求（2.2 表格），不满足时提示“暂不支持该设备”。
2. **开始之前**（四个平台共用一段，放在平台切换上方）：① 注册账号（链接 /register）；② 购买套餐（链接 /plans）；③ 同一账号可同时在线的设备数以套餐为准（总览页可看在线设备）；④ APP 里用和网站相同的邮箱密码登录。已登录访客可把 ①② 显示为“已完成”式的轻提示，不做强校验（D2 下页面不依赖登录状态，状态只用于提示文案，取不到时一律按未登录显示）。
3. **第一步 下载安装**
4. **第二步 首次授权**（平台差异最大的一步）
5. **第三步 登录**
6. **第四步 选择线路**（解释专线/普通线路与“线路倍率”：倍率 1.5× 表示用 1 GB 按 1.5 GB 扣流量）
7. **第五步 连接与断开**（状态词、计时、本次用量）
8. **第六步 查看套餐与用量**（剩余流量、到期、“正在同步用量”的含义；续费去网站）
9. **后台运行与退出**（托盘/菜单栏/通知栏/省电设置）
10. **升级与卸载**（与下载页现有说明保持一致，下载页那段文字可改为链接到这里，避免两处维护）
11. **常见问题**（折叠面板）
12. **仍未解决**：先在 APP“账户/我的”页点“复制诊断信息”，再到“我的工单”提交（tickets_enabled 时显示；未登录点击会先去登录）或打开在线客服（useSupportAccess 可用时显示）。

### 4.2 Windows 要点

- 下载安装：运行安装程序；若出现“Windows 已保护你的电脑”，点“更多信息 → 仍要运行”（仅未签名的版本会出现，签名正式版后删除这一条，见 13 节）；同意 UAC 弹窗（普通用户需管理员输入密码）；结束页可勾选“启动 AsterLink”。
- 首次授权：登录页顶部若出现“后台服务”卡片，点卡片上的按钮（安装/修复/启动）并同意 UAC，完成后自动继续；不出现则跳过。
- 登录：勾选“记住密码”下次自动登录；点“退出登录”会清除保存的密码。
- 常驻：点窗口右上角“×”不会退出，程序缩到右下角托盘；托盘图标可能被收在“^”里；托盘菜单可直接连接/断开；要彻底退出用托盘“退出”。
- 卸载：设置 → 应用 → AsterLink → 卸载；询问是否删除保存的密码与偏好时默认“否”；选“是”后重新安装会被当成新设备，可能暂时占用一个在线设备名额。
- 常见问题：托盘找不到图标、提示后台服务异常（账户页“后台服务”区按提示修复）、升级时提示先退出（从托盘退出后重试）、不支持 ARM 版 Windows。

### 4.3 macOS 要点

- 下载安装：打开 DMG，把 AsterLink 拖进“应用程序”，**必须从“应用程序”里打开**（直接在 DMG 里运行时登录页会提示先拖过去）。若系统提示无法验证、阻止打开，到“系统设置 › 隐私与安全性”点“仍要打开”（仅未公证版本会出现）。
- 首次授权：登录页“首次使用”卡片点“启用”→ 系统设置自动打开“通用 › 登录项与扩展”→ 在“允许在后台”里打开 AsterLink 开关 → 回到 App 自动继续，不用点刷新。说明这是 macOS 对所有后台服务的统一要求。
- 钥匙串提示：若出现“AsterLink 想要使用钥匙串中的……”，点“始终允许”（内部版本每次升级可能问一次；正式签名版不会）。
- 常驻：关闭窗口后 Dock 图标消失，屏幕顶部菜单栏的 AsterLink 图标还在，连接不受影响；从菜单栏图标“打开 AsterLink”找回窗口；彻底退出用菜单栏“退出”或 ⌘Q。
- 升级：先断开 → 用新版本替换“应用程序”里的 AsterLink → 打开；若提示“后台服务需要更新”，到“账户”页按提示重新安装后台服务。
- 卸载：用 DMG 里附带的卸载程序（与 docs/macos-release.md 第 5 节一致）。
- 常见问题：Intel 芯片的 Mac 暂不支持；找不到菜单栏图标（菜单栏太满被刘海遮挡）；“允许在后台”被关掉后无法连接（账户页“后台服务”区重新允许）。

### 4.4 Android 要点

- 下载安装：用手机浏览器打开下载页下载 APK → 点打开 → 系统提示“禁止安装未知应用”时，进设置允许该浏览器安装 → 返回安装。可核对下载页给出的 SHA-256（写成可选的进阶说明，不放在主流程里）。
- 首次授权：首次点连接时系统弹出“连接请求”，点“确定/允许”；Android 13 及以上会询问是否允许通知，建议允许（通知栏显示连接状态、实时速率与断开原因）。
- 连接后：通知栏常驻 AsterLink 通知；可把 AsterLink 快捷开关加到下拉菜单。
- 后台与省电：“我的 → 后台运行与省电”按提示设为“不限制”；不同品牌手机（小米、华为、OPPO、vivo 等）入口名称不同，给一个通用说法而不逐品牌截图。说明不支持系统“始终开启的 VPN”。
- 升级：直接下载新版本覆盖安装，密码与设置保留；不能装比现有更旧的版本。
- 常见问题：拒绝了 VPN 授权（APP 会提示，重新点连接再允许）、被其他 VPN 应用顶掉（APP 显示被系统收回，重新连接）、提示“应用版本过低”（回下载页装新版）、只支持 64 位手机。

### 4.5 iPhone 要点

- 下载安装：**必须用 Safari** 打开下载页 → 点“在 iPhone 上安装” → 系统询问时点“安装” → 回桌面等待图标下载完成。说明“仅限已登记的设备安装”：若点击后提示无法安装，联系客服登记设备（客服话术与登记流程见 docs/ios-release.md 第 4 节，属于运营动作，教程里只给结论）。若首次打开提示未受信任，到“设置 → 通用 → VPN 与设备管理”信任（与下载页现有说法一致，放行词已存在）。
- 首次授权：首次点连接 → App 内说明页“首次连接需要添加 VPN 配置”→ 点“继续”→ 系统弹窗“AsterLink 想要添加 VPN 配置”→ 点“允许”→ 输入锁屏密码或面容 ID。说明 AsterLink 不会保存锁屏密码。
- 通知：建议允许通知，套餐到期或流量用尽导致断开时会提醒。
- 连接后：状态栏出现“VPN”图标即为已连接；也可在“设置”里看到 VPN 开关，但请在 App 内连接/断开（系统开关绕过了线路选择，统一建议用 App）。
- 升级与卸载：回下载页再装一次即覆盖；长按图标删除即卸载，VPN 配置一并移除。
- 常见问题：用了微信/Chrome 打开下载页导致无反应（换 Safari）、拒绝了添加 VPN 配置（“我的”页 VPN 配置显示“尚未添加”，重新点连接）、iPad 不支持。

### 4.6 通用常见问题（四端共用，按平台替换按钮名称）

无法连接且提示没有套餐 / 套餐到期 / 流量用尽（去网站购买或续费，回到 APP 会自动同步）；提示同时在线设备已满（先在其他设备断开或退出）；连上了但打不开网页（换一条线路再试，仍不行复制诊断信息提交工单）；忘记密码（网站“忘记密码”）；换手机或电脑（新设备登录即可，旧设备退出登录释放名额）；流量为什么比预期用得快（线路倍率）。

### 4.7 写作规范

- 一步只做一件事；动词开头，按钮名称用中文引号括起来并与 APP 完全一致（从 2.3 的 texts.json 与 strings.xml 核对，不凭记忆写）。
- 不写任何技术名词：内核、协议、TUN、端口、DNS、配置文件、节点地址；也不写引擎名称。
- 不写无法证明的承诺（文案守卫的广告法词表）。
- 不出现真实线路名、真实服务器地址、真实邮箱。
- 系统界面文字以中文系统原文为准，如与守卫冲突（含“开发”二字等），把原文短语加入两份放行清单并注明“系统原文”，不得关闭守卫。

## 5. 数据模型（教程数据模块）

放在 frontend/src/lib/ 下的纯 TS 模块（例如 guides.ts），不依赖 React，便于 node --test 直接测试。结构约定：

- **平台**：标识（windows/macos/android/ios）、显示名、图标名、适用设备说明、“下载可用性键”（android、ios 对应 downloads.ts；windows、macos 暂无，D6）。
- **章节**：标识（用作页面锚点，同一平台内唯一）、标题、步骤列表、可选的“提示/注意”块。
- **步骤**：序号由顺序决定（不手写）、标题、正文（纯文本，允许极少量强调与站内链接，链接只能指向站内白名单路径：/register、/plans、/download、/dashboard、/tickets、/forgot-password、/guide）、可选截图键、可选高亮列表。
- **截图键**：指向图片清单中的条目；清单提供文件路径、宽、高、设备类型（phone / desktop / menubar）、替代文字。
- **高亮**：编号 + 百分比矩形（左、上、宽、高，0–100），可选形状（圆/框）。
- **常见问题**：问题、回答，可按平台覆盖按钮名称。

规则：第 1 轮截图键允许为空（纯文字版），第 2 轮起清单存在才渲染；高亮必须依附于截图；正文里提到的编号必须与高亮编号对得上（测试校验）。

## 6. 页面与交互

### 6.1 路由与渲染

- frontend/src/app/guide/page.tsx：服务端组件，force-dynamic，读取 androidDownload / iosDownload 的“是否可用”（只传布尔与版本号给客户端，不传 SHA-256 等无关字段），metadata 标题“使用教程”。客户端视图用 Suspense 包裹（读取查询参数需要，与 tickets 页一致）。
- frontend/src/components/guide-view.tsx：客户端组件，负责平台切换、步骤渲染、截图放大、FAQ 折叠。
- site-shell.tsx：links 数组在“下载客户端”之后加入“使用教程”，图标用 Tabler 的书本类图标（如 IconBook2，与现有 IconDownload 风格一致）；workspace 白名单加入 /guide。

### 6.2 平台选择

- 优先级：查询参数 platform → 访客设备自动识别 → 默认 Windows。
- 自动识别只在客户端挂载后进行（避免服务端与客户端渲染不一致）：iPhone → ios，Android → android，Mac → macos，Windows → windows。iPadOS 的 Safari 报告为 Mac，会被识别为 macOS，这是可接受的（iPad 本来就不受支持，macOS 教程首行会写明设备要求）。
- 切换平台时用 router.replace 更新查询参数（不新增历史记录、不滚动到顶部），这样复制地址栏就是该平台的深链。
- 平台切换用 Mantine 的 SegmentedControl 或 Tabs，手机宽度下四个选项一行放得下（只放图标 + 短名称：Windows、macOS、Android、iPhone）。

### 6.3 版式

- 页面头部：标题“使用教程”、一句说明、“开始之前”四项。
- 桌面宽度：左侧本平台章节目录（锚点，滚动时高亮当前章节，可选），右侧正文；每一步左文右图，手机截图限制显示宽度（约 260–300 像素），桌面截图占满正文宽度。
- 手机宽度：单列，图在文字下方；不得出现横向滚动（沿用 tests/layout.ts 的 overflowing 检查）。
- 步骤用 Mantine Timeline 或带编号的卡片呈现，编号大而醒目。
- 截图外框：手机图圆角 + 细边，桌面图细边 + 轻阴影；深色模式下边框用 --al-* 令牌，不写颜色字面量。
- 点击截图打开放大层（Mantine Modal），显示原尺寸图与替代文字，Esc 关闭。
- 高亮圆圈绝对定位在图片容器上，按百分比放置，随图片缩放；圆圈内是步骤编号，配色用品牌色，动画遵守“减少动态效果”。
- “下载”步骤：可用时按钮链接到 /download 对应卡片（下载页给两张卡加 id 锚点），不可用时显示中性提示“安装包请通过在线客服或工单获取”。

### 6.4 可访问性

- 标题层级：h1 页面标题，h2 章节，h3 步骤。
- 每张图都有描述画面的替代文字（例如“AsterLink 登录页，邮箱和密码输入框下方是记住密码开关和登录按钮”），高亮圆圈对读屏隐藏，因为文字步骤已写明操作。
- 平台切换、FAQ 折叠、放大层都可键盘操作。

### 6.5 互链

- 下载页每张平台卡下方加一行“安装后不会用？查看使用教程”，链接到 /guide?platform=对应平台；底部“安装、升级与卸载”两张说明卡保留，但各加一句“详细图文步骤见使用教程”。
- 不改首页与顶部导航（不在需求内；后续若要加入首页 FAQ 链接是一行改动）。

## 7. 截图生产流程（第 2、3 轮）

### 7.1 选图清单

在 frontend 下维护一份“教程截图选单”（JSON，入库），每条写明：目标键、来源平台、来源场景名（如 macOS 的 connect-connected-light、Android 的 12-connect-connected）、裁切区域（可选）、输出显示宽度、替代文字。每个平台约 8–12 张：登录、首次授权（APP 内卡片/说明页）、连接页未连接、连接页已连接、线路页、线路倍率特写（可用裁切）、账户/我的页、常驻（菜单栏面板、托盘菜单）、典型阻断提示（无套餐、设备已满）。

### 7.2 处理脚本

- 新增 npm 脚本（例如 guide:images），沿用 brand:apps 的实现方式：Playwright 驱动本机 Chrome（PLAYWRIGHT_CHROME_PATH）在页面里完成解码、裁切、缩放、WebP 编码，不新增 npm 依赖。
- 输入：各平台截图套件的输出目录（以参数传入；Windows 用 CI 的 windows-ui-preview 产物解压目录；手动系统截图用 frontend 下的原图暂存目录，原图不入库或只入库处理后的结果）。
- 输出：frontend/public/guide/平台/目标键.内容哈希前8位.webp；以及图片清单（每条：键、文件、宽、高、设备类型、来源场景、来源文件 SHA-256、输出 SHA-256、字节数、替代文字）。清单入库，教程数据模块通过键引用。
- 尺寸：手机图输出宽 600 像素左右（约 2 倍显示宽度），桌面图输出宽 1440 像素以内；WebP 质量约 82；单张预算 ≤ 200 KB，全部 ≤ 4 MB。
- 来源检查（脚本内置，失败即退出）：该场景有 texts.json 时（macOS、Windows），扫描可见文字：不得含“开发版”、不得含 example.com 以外的域名、不得含文案守卫词；Android、iOS 没有 texts.json，依赖截图测试里已有的 CopyGuard 断言，并在第 2 轮验收里人工逐张过目。
- 旧哈希文件在重新生成时删除，清单之外的文件不得留在 public/guide 下（测试校验）。

### 7.3 各平台拍摄注意

- macOS：本机跑 --suite ui；用 -light 场景；菜单栏面板单独成图。预览用离屏窗口，毛玻璃是平面色，属已知差异可接受。
- Windows：取 100% 浅色场景；托盘菜单图；带真实标题栏的两张可用于“窗口长什么样”的总览图。
- iOS：拍摄前把模拟器状态栏覆盖为固定值；选用无“开发版”标记的场景，必要时在 ScreenshotTests 增加教程专用用例（只改测试代码，使用发布外观的预置状态）。
- Android：ScreenshotTest 的构建信息为 development，登录页会有“开发版”；增加一组教程专用截图（发布渠道构建信息、vip@example.com、浅色、常规字号），文件名统一加 guide- 前缀，不影响现有 30 张的断言。

### 7.4 系统画面（第 3 轮，真机）

需要需求方在真机上拍摄（拍摄清单与要求写入第 3 轮交付的维护文档）：

| 平台 | 画面 |
| --- | --- |
| Windows | SmartScreen“Windows 已保护你的电脑”与“仍要运行”；UAC 弹窗；托盘“^”展开后的图标位置 |
| macOS | 拖入“应用程序”窗口；“隐私与安全性”里的“仍要打开”；“登录项与扩展”中“允许在后台”的 AsterLink 开关；钥匙串“始终允许” |
| Android | 浏览器下载完成提示；“安装未知应用”设置页；系统“连接请求”对话框；通知权限对话框；通知栏中的 AsterLink 通知 |
| iPhone | Safari“是否安装”确认；系统“想要添加 VPN 配置”弹窗；状态栏 VPN 图标 |

拍摄要求：系统语言简体中文、浅色模式；状态栏无个人通知、无真实邮箱与手机号、无运营商以外的个人信息；只截与步骤相关的区域；文件交给开发方后走同一处理脚本（来源标记为 manual，清单里写拍摄设备与系统版本）。未拿到的画面，该步骤保持纯文字（第 1 轮起就支持），**不得**用网上图片或他人截图代替。

## 8. 测试设计

### 8.1 单元测试（新增 npm run test:guide，加入 ci.yml 的 Web validation）

- 结构：四个平台齐全，每个平台都有 4.1 规定的必备章节；章节锚点平台内唯一；每个平台至少一条 FAQ。
- 链接：正文链接全部在站内白名单内。
- 截图：引用的键都在清单里；清单里的文件都存在、字节数与 SHA-256 一致、尺寸与清单一致、单张与总量不超预算；public/guide 下没有清单外的文件；高亮矩形都在 0–100 且不越界；正文提及的编号与高亮编号一致；每张图有非空替代文字。
- 文案：教程数据里的全部文字过一遍禁用词（直接复用 copy-guard 的词表，避免第三份清单）；桌面平台另禁“VPN、内核、协议、TUN、Helper、Mihomo、Clash”；四个平台都禁“开发版”。
- 平台选择函数（纯函数）：各种 User-Agent 与查询参数组合的结果。

### 8.2 源码文案守卫

copy-guard.test.mjs 的“扫描覆盖用户页面”断言中加入 app/guide/page.tsx、components/guide-view.tsx 与教程数据模块，确保新文件确实在扫描范围内。

### 8.3 浏览器用例（新增 tests/guide.spec.ts，加入 playwright.payment.config.ts）

- 侧栏：登录普通用户后，“使用教程”紧跟在“下载客户端”之后、在“账号设置”之前；管理员同样可见；点击进入后该项高亮。
- 未登录直接访问 /guide 可以打开（D2），侧栏显示，不跳转登录页；登录后同样可见。
- 深链：/guide?platform=ios 打开即为 iPhone；切换到 Android 后地址栏变为 platform=android，刷新后仍是 Android；非法取值回落为自动识别。
- 自动识别：用 iPhone、Android、Mac、Windows 的 User-Agent 各开一个上下文，默认平台正确。
- 下载可用性：夹具里配置 Android 下载、不配置 iOS 时，Android 教程显示“前往下载”，iPhone 与桌面端显示“联系客服获取”。
- 图片（第 2 轮起）：当前平台的图片全部加载成功（naturalWidth > 0），其他平台图片未请求；点击放大、Esc 关闭。
- 布局：390 宽与 1280 宽下无横向溢出；深色模式截图存档。
- 运行时文案：ui-copy.spec 的页面清单加入 /guide（标题形如“使用教程 · ”，可见文字无禁用词）；theme.spec 的页面列表加入 /guide；support-widget.spec 的公开页面列表酌情加入。
- 下载页互链：每张平台卡下的“查看使用教程”链接指向正确平台。

### 8.4 不需要的测试

不涉及后端、数据库、APP 产品代码，因此不跑 Go 测试、发布演练与 APP 套件的全量回归；第 2 轮改动的 APP 截图测试只需跑对应的 --suite ui 证明新增用例通过、原有用例不变。

## 9. 性能、缓存与安全

- 首屏只加载当前平台的文字，图片懒加载；目录与 FAQ 不额外请求。
- 图片文件名带内容哈希，可在 nginx 对 /guide/ 静态图片设置长缓存（写进第 3 轮的部署说明，属可选优化，不改不影响功能）。
- 页面无表单、无写操作；模拟登录（只读）状态下访问无影响。
- 不引入第三方脚本、外链图片或视频；所有图片同源。
- next.config.ts 的全局安全头不变。

## 10. 兼容、发布与回滚

- 纯前端改动：后端、数据库、四个 APP 均不需要升级，旧后端也完全兼容（页面不调用任何新接口）。
- 发布：frontend 目录 npm ci、npm run build、重启 next 进程（与 docs/deployment.md 现有流程相同）；不需要新增 .env 参数。
- 回滚：回到上一版前端即可，侧栏入口与 /guide 一并消失，无数据残留。

## 11. 三轮实施与逐轮验收

### 第 1 轮：页面与文字教程

范围：
- 教程数据模块（第 5 节）与四个平台完整文字内容（第 4 节），截图键全部留空。
- /guide 页面、guide-view 组件、平台选择、目录、FAQ、“开始之前”、“仍未解决”区块、下载可用性提示。
- 侧栏入口与 workspace 白名单。
- 下载页互链与锚点。
- test:guide 单元测试、copy-guard 覆盖断言、guide.spec 浏览器用例（不含图片部分）、ui-copy/theme 页面清单更新、ci.yml 增加 test:guide。

验收：

| 编号 | 检查 | 通过标准 |
| --- | --- | --- |
| G-01 | 侧栏位置 | 普通用户与管理员登录后，“使用教程”紧跟“下载客户端”之后；进入后高亮 |
| G-02 | 访问控制 | 未登录可打开且有侧栏、不跳转登录页；登录后同样可见 |
| G-03 | 平台切换与深链 | 四个平台可切换；查询参数可直接打开、刷新保持、非法值回落 |
| G-04 | 自动识别 | 四种设备 UA 默认平台正确 |
| G-05 | 内容完整 | 每个平台都有第 4.1 节全部章节，按钮名称与 APP 现有文字逐一核对一致（附核对表：教程中的每个按钮名 → 来源文件） |
| G-06 | 下载可用性 | Android/iOS 按 .env 实际可用性显示按钮或客服提示；桌面端显示客服提示 |
| G-07 | 文案 | test:copy、test:guide 通过；运行时 ui-copy 无禁用词；桌面端无“VPN”等词 |
| G-08 | 布局 | 390 与 1280 宽无横向溢出；浅/深色截图存档 |
| G-09 | 回归 | lint、webpack 构建、typecheck、全部 test:* 通过；CI 全绿 |

### 第 2 轮：APP 截图、高亮与放大

范围：
- 截图选单、guide:images 处理脚本、图片与清单入库（第 7.1–7.3 节）。
- Android、iOS 截图测试中新增教程专用用例（只改测试代码），去掉“开发版”标记。
- 页面渲染截图、外框、高亮圆圈、点击放大、“示意图”注释。
- test:guide 的图片相关断言、guide.spec 的图片相关用例。

验收：

| 编号 | 检查 | 通过标准 |
| --- | --- | --- |
| G-10 | 素材来源 | 每张图都能在清单里追溯到具体的截图套件场景与来源文件哈希；重新运行脚本得到相同输出（同一输入下字节一致或清单一致） |
| G-11 | 隐私与文案 | 逐张人工过目并在第 15 节记录：无“开发版”、无真实邮箱/域名/线路名、无禁用词；有 texts.json 的平台脚本检查通过 |
| G-12 | 体积 | 单张 ≤ 200 KB、总量 ≤ 4 MB；切换平台时只请求该平台图片 |
| G-13 | 高亮准确 | 每个高亮圆圈落在对应按钮上（四个平台各附一张页面截图作证据）；正文编号与圆圈一致 |
| G-14 | 交互 | 放大层可开可关、键盘可用；减少动态效果时无动画 |
| G-15 | APP 测试不回归 | 涉及的 Android/iOS --suite ui 通过，原有截图用例数量与断言不变 |
| G-16 | 回归 | 同 G-07、G-08、G-09 |

### 第 3 轮：系统画面、回归与文档

范围：
- 接收需求方真机拍摄的系统画面（第 7.4 节），经处理脚本入库并接到对应步骤。
- 全站回归：桌面与手机宽度、浅色与深色、四个平台逐页截图存档。
- 维护文档 docs/user-guide.md：内容数据结构、怎样新增一步、怎样在 APP 界面改版后更新截图（重跑哪个套件、怎么更新选单与清单）、系统画面拍摄要求、桌面端正式签名后要删掉的步骤（SmartScreen、“仍要打开”、钥匙串提示）。
- 可选：部署说明中补充 /guide/ 图片的 nginx 长缓存建议。
- 第 12 节用户实测清单。

验收：

| 编号 | 检查 | 通过标准 |
| --- | --- | --- |
| G-17 | 系统画面 | 已收到的画面全部入库并符合拍摄要求；未收到的逐项列为 BLOCKED（写明缺哪张），对应步骤保持纯文字且页面无破图 |
| G-18 | 全站回归 | CI 全绿；四个平台 × 两种宽度 × 两种主题截图存档 |
| G-19 | 文档 | docs/user-guide.md 能让没参与开发的人按步骤更新一张截图并通过 test:guide |
| G-20 | 新手走查 | 找一位未接触过本产品的人，只看教程在一台设备上完成“注册 → 购买 → 安装 → 连接”，记录卡住的地方并修订文案（无法安排时记 NOT RUN，交由 U-49） |

## 12. 用户部署后的实测（追加到现有 U 系列之后）

前提：生产环境已部署本功能；准备一个有有效套餐的普通账户。

| 编号 | 步骤 | 预期 |
| --- | --- | --- |
| U-47 | 在电脑浏览器登录网站，查看左侧栏 | “下载客户端”下方有“使用教程”，点击进入，默认平台与这台电脑的系统一致 |
| U-48 | 用 iPhone 的 Safari 和 Android 手机浏览器分别打开网站的“使用教程” | 默认显示各自平台的教程；图片清晰、无横向滚动；“前往下载”按钮可用 |
| U-49 | 请一位不熟悉本产品的人，只看教程在自己的手机上完成安装与连接 | 不需要额外讲解即可连上；卡住的步骤记录下来反馈修订 |
| U-50 | 在 Windows 与 Mac 上各照教程完成一次安装、首次授权、连接、关闭窗口后从托盘/菜单栏找回 | 每一步的按钮名称、画面与教程一致 |
| U-51 | 复制一条带 platform 参数的教程链接，通过工单回复或客服对话发给用户 | 对方打开直接是该平台的教程 |

任何一步与预期不符，截图并记录设备型号、系统版本、APP 版本，记入第 15 节。

## 13. 风险与观察项

- **截图过期**：APP 界面改版后教程图会与实际不符。缓解：清单记录来源场景与哈希，维护文档写清更新步骤；之后 APP 改界面的交接书应把“更新教程截图”列为收尾项。
- **桌面端安装拦截步骤是临时的**：SmartScreen、“仍要打开”、钥匙串提示只在未签名/未公证版本出现；正式签名发布后要删除，否则会误导用户（docs/user-guide.md 列出这几处）。
- **iPhone 仅限已登记设备**：教程无法替代设备登记流程，用户点安装失败时只能找客服；这是 Ad Hoc 分发方式的限制，不是教程问题。
- **品牌差异**：各品牌 Android 手机的省电设置入口差异大，教程只给通用说法；需要时可以后续按工单反馈补充常见品牌。
- **观察项（不在本期处理）**：下载页“开源许可与源代码”段落仍提到代理内核名称，与 APP 去敏口径（handoff_app_standardization.md D10）不一致，是否调整由需求方另行决定。

## 14. 完成定义

- 用户登录后，左侧栏“下载客户端”紧下方有“使用教程”，点击进入后可在 Windows、macOS、Android、iPhone 四个平台之间切换，默认显示访客当前设备的平台，带平台参数的链接可直接分享。
- 每个平台都有从下载安装到连接、断开、常驻、升级卸载、常见问题的完整图文步骤；APP 界面截图来自现有截图套件（虚构数据），关键按钮有编号高亮，截图可放大；系统画面按需求方提供的真机截图补齐，缺的步骤保持纯文字且无破图。
- 教程文字与 APP 实际按钮名称一致，通过网站文案守卫，桌面端不出现技术词与“VPN”。
- 没有新增后端接口、数据库变更、.env 参数或 APP 产品代码改动；CI 全绿。
- 第 11 节全部验收项有 PASS 证据，或有写明原因的 BLOCKED / NOT RUN 记录；生产环境结论以需求方按第 12 节实测的结果为准，与开发方结果分开记录。

## 15. 实施记录

（每轮完成后在此追加：分支、PR、改动文件、验收编号与结果、证据位置、遗留问题。）

### 第 1 轮：页面与文字教程（2026-11-03）

- 分支 worktree-guide-round1-text，PR 见合并记录；基线 main 1dc51f0。
- 改动：frontend/src/lib/guides.ts（教程数据与平台选择）、components/guide-view.tsx、app/guide/page.tsx、site-shell.tsx（侧栏与白名单）、support-widget.tsx（/guide 可用在线客服；新增 useSupportOffered）、download-view.tsx（id 锚点与互链）、globals.css；测试 scripts/guide.test.mjs、tests/guide.spec.ts，copy-guard 覆盖断言、ui-copy / theme 页面清单、playwright.payment.config.ts、ci.yml、package.json（test:guide）。无后端、数据库、.env、APP 产品代码改动。
- 验收（本地，私有 Postgres + 真实 Next.js + Chrome）：G-01 PASS（用户与管理员，homea / themeroot）；G-02 PASS；G-03 PASS；G-04 PASS（四种 UA）；G-05 PASS（核对表见下）；G-06 PASS（Android 可用显示“前往下载”，iPhone 与桌面端显示“安装包请联系客服获取”）；G-07 PASS（test:copy、test:guide、ui-copy）；G-08 PASS（390 与 1280 无横向溢出，四个设备名都在视口内；浅/深色截图在 browser-artifacts/screens/guide-*.png 与 theme-*-guide.png）；G-09 本地 lint、typecheck、test:* 通过，test:api-proxy 的 3 个浏览器用例因本机缺 Playwright headless shell 失败（与本轮无关，CI 覆盖），CI 结果见 PR。
- 本地 playwright 命中的 spec：guide、ui-copy、theme、support-widget 全部通过。
- 设计取舍：未登录也显示“提交工单”按钮（点击先去登录，工单未启用时不显示）；在线客服按钮仅登录的普通用户且客服开启时显示；管理员不显示工单按钮。教程里“已选”标记：桌面端为“已选”，Android 为“已选择”。
- 遗留：截图键与高亮仅有类型，页面尚不渲染图片（第 2 轮）。

G-05 按钮名称核对表（教程用词 → 来源）：

| 教程中的名称 | 来源 |
| --- | --- |
| 后台服务 卡片；重新检测 | windows/AsterLink.App/Controls/ServiceGuide.xaml、Views/AccountView.xaml |
| 安装 / 修复 / 启动（需要管理员权限）；打开 AsterLink | windows/AsterLink.App/ServicePanel.cs |
| 记住密码、忘记密码、显示密码 | windows Views/LoginView.xaml；macos LoginView.swift；android strings.xml；ios LoginView.swift |
| 打开 AsterLink、连接 / 断开、退出（托盘） | windows/AsterLink.App 托盘菜单；macos MenuBar 面板 |
| 退出 AsterLink | windows Views/AccountView.xaml；macos AccountPage.swift |
| 首次使用：允许 AsterLink 在后台运行；启用；再次打开系统设置；允许在后台 | macos LoginView.swift、HelperClient.swift |
| 重新安装后台服务；移除后台服务；后台服务需要更新 | macos AccountPage.swift、HelperConnection.swift |
| 线路；全部 / 专线 / 普通线路（Android：普通）；搜索线路或地区；线路倍率；已选（Android：已选择） | 各端 LinesPage / LinesView / strings.xml |
| 将断开当前连接；切换线路？；断开并切换 | ios LinesView.swift；android strings.xml |
| 本次已用 X · 按倍率计 Y；正在同步用量；剩余；到期 | 各端 ConnectPage / ConnectView / strings.xml |
| 未连接 / 正在准备 / 正在连接 / 已连接 / 正在断开 / 无法连接 | android strings.xml；各端连接页 |
| 复制诊断信息；退出登录；后台运行与省电；续费或升级；注册账号 | android strings.xml；ios AccountView.swift；macos / windows 账户页 |
| 首次连接需要添加 VPN 配置；继续；尚未添加；VPN 配置 | ios VPNSetupSheet.swift、AccountView.swift |
| 已达到同时在线设备上限 | ios UIFixtures.swift 与 Core 提示文案 |
| 系统原文（SmartScreen、用户账户控制、未受信任的开发者等） | 系统界面，第 3 轮真机核对 |

### 第 2 轮：APP 截图、高亮与放大（2026-11-04）

- 分支 worktree-guide-round2-shots，基线 main f3f76cb（#108 合并后）。
- 改动：
  - 选单 frontend/guide-shots.json（33 张：Windows 10、macOS 9、Android 7、iPhone 7；键、来源场景、可选裁切、输出宽度、替代文字）。
  - 处理脚本 frontend/scripts/guide-images.mjs（npm run guide:images -- --macos/--windows/--android/--ios 目录）：Playwright 驱动本机 Chrome 解码、裁切、缩放、WebP（质量 0.82）编码；输出 public/guide/平台/键.哈希前8位.webp 与清单 src/lib/guide-images.ts（键、文件、宽高、显示宽度、设备类型、来源场景、来源 SHA-256、输出 SHA-256、字节数、替代文字）；有 texts.json 的平台（macOS、Windows）做来源文字检查（开发版、example.com 以外的域名、网站禁用词），失败则不写清单；清单外的文件删除。没有新增 npm 依赖。
  - 教程数据 guides.ts：步骤加截图键与编号高亮（正文用 ①②③ 指向同号圆圈；高亮可选圆形与编号放右上角）；新增 markNumbers。
  - 页面 guide-view.tsx 与 globals.css：手机图在宽屏时放在文字右侧（280 px），桌面图在文字下方占满正文宽度；细边框与圆角（--al-* 令牌，深色模式同样适用）；百分比定位的编号框（读屏隐藏，减少动态效果时无动画）；点击或回车打开放大层（Mantine Modal，Esc 或“关闭”退出，减少动态效果时无过渡）；每张图下注“示意图，线路名称与数据以实际为准”。next/image unoptimized、懒加载、尺寸取自清单。
  - Android：新增测试类 GuideScreenshotTest.kt（只改测试代码）：发布渠道构建信息（无“开发版”）、api.example.com、虚构线路名“香港 01”等、浅色、常规字号，文件名 guide- 前缀，每张断言所示状态并额外断言无“开发版”、无 example.com 以外的地址；scripts/test-native-android.py 的 ui 套件同时跑两个类，原有 ui.states 只统计非 guide- 文件，新增 ui.guide_shots 记录；android.yml 新增 android-ui-screenshots 产物（保留 14 天，供本脚本取图）。
  - iOS：scripts/test-native-ios.py 的 ui 套件在拍摄前把模拟器状态栏覆盖为 9:41、满信号、满电、无运营商，结束后清除（新增 ui.status_bar 记录）。未改 ScreenshotTests。
  - 测试：test:guide 新增图片断言（选单与清单一一对应、来源场景可追溯、文件字节/哈希/WebP 尺寸与清单一致、文件名带哈希、单张 ≤ 200 KB、总量 ≤ 4 MB、public/guide 下无清单外文件、高亮在 0–100 且不越界、正文编号与高亮编号一致且按 1、2、3 编号、替代文字过禁用词、桌面端替代文字无“VPN”等）；copy-guard 覆盖清单加入 lib/guide-images.ts；guide.spec 新增图片用例（只请求当前平台图片且全部解码、放大层点击/回车打开与 Esc/关闭、减少动态效果、四平台高亮证据截图）。
  - 无后端、数据库、.env、APP 产品代码改动。
- 截图来源（G-10）：
  - macOS：本机 python3 scripts/test-native-macos.py --suite ui --run-id guide-r2（4/4 PASS，28 场景，593 条文字 0 命中）。
  - Windows：main 1dc51f0 的 Windows CI 运行 41757650836 的 windows-ui-preview 产物（f3f76cb 未改 Windows 代码）。
  - Android：本分支 Android CI 运行 41789097816（workflow_dispatch）的 android-ui-screenshots 产物。本机 Robolectric 原生图形 jar 未缓存，下载经公司网络在 24 MB 处停滞，本机 ui 套件 BLOCKED（与代码无关）。
  - iOS：本机 python3 scripts/test-native-ios.py --suite ui --runtime latest（iOS 27.0 模拟器；49/49 通过，50 张截图）。注意：worktree 需要从主检出复制被忽略的 apps/ios/.env、Config/Generated.xcconfig 和 Frameworks/，否则 App 停在配置错误页、49 个用例全部失败。
  - 同一输入重新运行脚本，清单逐字节一致（四个平台一起重跑后 diff 为空）。
- 截图逐张人工过目（G-11）：无“开发版”；邮箱只有 user@ / vip@ / demo@example.com；线路名均为虚构（香港 01、东京专线 01 等）；无禁用词。处理时主动避开的内容：Windows 线路页最后一条“一个特别长的线路名称用来检查换行和裁切”（裁掉）、Windows 真实窗口截屏底部的任务栏与时钟（裁掉）、iOS“我的”页底部 Debug 专用的“调试 / 隧道调试”（只取上半部分系统权限区）、iOS 登录页页脚只有版本号（Debug 构建本次未显示“开发版”，如出现应裁掉页脚）。macOS、Windows 脚本来源检查 0 问题。
- 体积（G-12）：33 张共 760 KB，最大 53 KB（macOS login-setup）。
- 高亮（G-13）：每张图都用无头 Chrome 叠加编号框逐张核对落点（Windows、macOS、Android、iPhone 全部），偏差的已修正；页面证据截图见浏览器用例输出 screens/guide-marks-平台-主题.png。
- 验收（本地：私有 Postgres + 真实 Next.js 构建 + Chrome，scripts/test-payment-web.py --spec guide --spec ui-copy --spec theme，106 passed）：G-10 PASS；G-11 PASS（上条逐张记录；macOS、Windows 脚本检查 0 问题）；G-12 PASS（单张最大 53 KB、总量 760 KB；用例断言每个平台只请求 /guide/本平台/ 下的图片且全部解码）；G-13 PASS（逐张叠加核对 + guide-marks-*.png 页面证据，用例断言编号框都在图片内、正文编号与圆圈一致）；G-14 PASS（点击与回车打开、Esc 与“关闭”退出；减少动态效果时编号框 animation 为 none、放大层无运行中的动画）；G-15 iOS PASS（本机 ui 套件 49/49、50 张，ScreenshotTests 未改），Android 原有 35 张与新增 8 张 guide- 均在 CI 运行 41789097816 的 ui 套件中通过（本机 BLOCKED，见上）；G-16 本地 lint、typecheck、全部 test:*、webpack/next 构建通过，390 与 1280 宽无横向溢出，浅/深色截图在 browser-artifacts/screens/guide-*.png；CI 结果见 PR。
- 遗留：系统画面（VPN 授权、安装拦截等）仍为纯文字，第 3 轮真机补齐；Android 与 iPhone 的“首次授权”目前只有 iPhone 的 App 内说明页有图。APP 界面改版后需按 guide-shots.json 的来源重跑对应 --suite ui 再运行 npm run guide:images（第 3 轮写入 docs/user-guide.md）。
