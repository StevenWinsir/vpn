# AsterLink VPN：客户端下载管理（管理后台 + /download）技术规划交接书

> 文件：handoff_download.md  
> 编写日期：2026-11-05  
> 代码基线：项目 /Users/stevenlee/Desktop/vpn，main，HEAD 9f4d845（message 第 1 轮合并后）。message 第 2 轮 PR #115 尚未合并，本规划第 2 轮依赖它（见第 9 节）。  
> 文档性质：实施规划与验收约定。本文不代表任何功能已开发或已测试，全文不含代码示例。

## 1. 目标、范围与轮次总览

现状：/download 只展示 Android 和 iPhone 两个平台。发布信息写在 frontend/.env 的 ANDROID_APK_* 和 IOS_* 变量里，换版本要改 .env 并重启网站。下载按钮的 href 就是真实文件地址，页面上还有一张“开源许可与源代码”卡片。

目标：

1. 管理后台 /admin/console 新增“客户端下载”板块。管理员可以添加、修改、删除 Windows、macOS、Android、iOS、Linux 的下载条目，包括公网下载地址、版本号、校验值和大小，并控制上架或下架。保存后自动同步到 /download，不需要改 .env，也不需要重启。
2. 同一平台可以发布多个架构（x64、arm64 等）和多种安装包格式（exe、msi、dmg、AppImage、deb 等）。数据模型从一开始就按“平台 × 架构 × 格式”设计，以后新增架构时只需在后台添加条目。
3. /download 的整体设计、logo、“安装、升级与卸载”卡片都保留。平台顺序改为 Windows、macOS、Android、iOS、Linux：补全 Windows、macOS 两块，Linux 先占位，显示“即将推出”。
4. /download 不显示真实下载地址。页面的可见文字、HTML 属性和传给浏览器的页面数据里都不出现真实地址，按钮统一指向本站的跳转路径。
5. 删除“开源许可与源代码”卡片，以及它背后的 ANDROID_SOURCE_URL 和 IOS_SOURCE_URL。

采用 **3 轮**实施。每轮一个 worktree 分支和一个 PR，分支命名为 worktree-download-roundN-*，每轮记录追加到本文第 13 节。

| 轮次 | 可验收结果 | 依赖 |
| --- | --- | --- |
| 第 1 轮 | 后端：数据表与迁移、字段校验、管理接口（列表、新增、修改、删除，带乐观锁和审计）、公开列表接口（不含地址）、公开跳转接口（302）、Go 测试与安全矩阵 | 无 |
| 第 2 轮 | 管理后台“客户端下载”标签页：按平台分组的列表、新增/编辑弹窗、上架/下架、删除确认、冲突提示，Playwright 用例 | 第 1 轮；message 第 2 轮（#115）已合并 |
| 第 3 轮 | 网站侧：/download 改为读后端，五平台顺序，多架构展示，站内跳转路由与 iOS 安装清单路由，删除许可卡片，补 Windows/macOS 安装说明；首页与使用教程改用同一数据源；去掉 .env 下载变量；打包脚本输出后台录入用的字段；文档；全量回归 | 第 1 轮（第 2 轮可并行开发，但合并顺序在第 2 轮之后，便于端到端验收） |

本期不做：

- 不做文件上传和托管。安装包仍由管理员放到自己的 CDN、对象存储或网站静态目录，后台只登记公网地址。
- 不做下载次数统计。公开接口不写数据库，避免被刷。
- 不做按浏览器自动识别架构并只展示一个按钮。各架构按钮都列出，并清楚标注适用设备，推荐架构用实心按钮。
- 不做客户端内的自动更新或检查更新。公开列表接口为以后的客户端检查更新留了余地，但本期客户端不接。
- 不做更新说明（Release Notes）等自由文本。公开页面上的所有文字都来自前端固定文案，受 copy-guard 约束。
- 不改 CLIENT_MIN_APP_VERSION（最低可连接版本）的配置方式。
- 不做防盗链或签名 URL。真实地址无法对下载者保密，见 D4。

## 2. 已核实的代码基础与必须衔接的位置

### 2.1 网站

| 位置 | 当前事实 | 对本次的影响 |
| --- | --- | --- |
| frontend/src/app/download/page.tsx | force-dynamic，每次请求从 process.env 读取 androidDownload、iosDownload，交给 DownloadView | 改为从后端读取（经服务端缓存模块） |
| frontend/src/lib/downloads.ts | 解析 ANDROID_APK_*、IOS_* 环境变量，包括 URL 校验（https，回环地址允许 http，禁止带账号密码）、版本正则、64 位十六进制 SHA-256、后缀检查，以及 iosInstallLink、megabytes、grouped | 环境变量解析整体删除；格式化函数保留；新增平台、架构、格式的固定表和文案 |
| frontend/src/components/download-view.tsx | 两张平台卡（id="android"、id="ios"）+ 两张“安装、升级与卸载”卡 + “开源许可与源代码”卡；按钮 href 是真实地址；iOS 用 itms-services 链接，其中直接带清单地址 | 主要改动点 |
| frontend/src/app/page.tsx:83–91 | 首页“多平台客户端”四张卡，顺序为 macOS、Windows、Android、iOS；macOS 和 Windows 写死为未发布，另外两张从环境变量判断 | 改用同一数据源，顺序改为 Windows 在前 |
| frontend/src/app/guide/page.tsx、src/lib/guides.ts、guide-view.tsx | 教程的“下载安装”步骤：downloadKey 为 android/ios 时显示“前往下载”和版本，Windows/macOS 为 null，显示“安装包请联系客服获取” | Windows、macOS 的 downloadKey 改为 windows/macos；数据源换成后端 |
| frontend/src/app/api/v1/[...path]/route.ts | 浏览器访问 /api/v1 的反向代理。拒绝上游重定向（redirect: manual，遇 3xx 报 upstream_redirect），只转发白名单响应头，不转发 Location | 不能借这个代理做下载跳转，需要单独的站内路由（第 6.3 节） |
| frontend/src/lib/server/api-config.ts | readAPIConfig() 提供 API_INTERNAL_URL、超时和 CLIENT_IP_HEADER | 服务端读取下载列表、跳转路由都复用它 |
| frontend/src/components/console/console-shell.tsx | 管理后台标签数组（用户、订单与支付、工单、套餐、登录记录、优惠码、公告）和页首说明文字 | 新增“客户端下载”标签 |
| frontend/.env.example:19–42 | ANDROID_APK_URL 等 6 项、IOS_IPA_URL 等 6 项 | 第 3 轮删除 |
| scripts/test-payment-web.py:90–93 | Playwright 夹具通过环境变量“发布” Android 1.2.3（地址 downloads.example.test），iOS 不发布 | 改为夹具通过后端数据发布 |
| frontend/tests：home.spec.ts:312、guide.spec.ts:175/197、ui-copy.spec.ts:81/97、theme.spec.ts:445 | 都断言了下载页或下载状态，home.spec.ts:330 断言按钮 href 以 https://downloads.example.test/ 开头 | 第 3 轮同步修改；href 断言改为“指向站内跳转路径” |
| scripts/downloads.test.mjs（test:downloads，CI 已运行） | 测试环境变量解析 | 第 3 轮改写为测试新的纯函数 |

### 2.2 后端

| 位置 | 当前事实 | 对本次的影响 |
| --- | --- | --- |
| backend/internal/api/server.go:139–253 | v1 公开路由、secured 路由、console 组（/admin/console/*，已含 admin 权限中间件）；全局 APIRate 限流；请求体默认 8 KB | 新路由挂在这里 |
| console_plans.go:621–700（商店设置） | 标准写法：lockConsoleAdmin、行级 FOR UPDATE、乐观锁 version 不符时返回 409 version_conflict、没有改动时返回 400 no_change、在同一事务内调用 consoleAudit 记录 from/to 明细 | 管理接口照此实现 |
| admin_coupons.go | “POST 新增 / POST :id 修改 / DELETE :id”的路由风格 | 沿用 |
| model.AdminAudit | Action 最长 40 字符，TargetID 为 uuid，Detail 是简短的非机密 JSON | 下载条目主键用 uuid；审计 Action 命名为 admin.download_create/update/delete |
| store.Migrate | AutoMigrate 列表 + 补充索引的 SQL | 新增模型和唯一索引 |
| security_review_test.go:197 TestSecurityRouteAccessMatrix | 每条路由要么在 public 白名单里，要么匿名访问必须返回 401 | 新的公开 GET 接口要加入白名单 |
| config：c.Env 只能是 development、test 或 production | 用于“非生产环境允许回环 http 地址” | 夹具测试需要 |
| native.go:487 requireAppVersion（CLIENT_MIN_APP_VERSION） | 客户端版本过低时返回“请升级”，用户会被引导回 /download | 不改，但验收时确认 /download 能提供新版本 |
| scripts/test-release-rehearsal.py | 升级、回滚和备份恢复演练，会检查新表与新列 | 第 1 轮确认新表在演练中通过（旧后端回滚时忽略新表） |

### 2.3 打包产物的真实命名（决定架构和格式的取值）

- Windows：AsterLink-Setup-x64-<版本>.exe（scripts/package-native-windows.py，win-x64）。
- macOS：AsterLink-<版本>-arm64.dmg（package-native-macos.py，min_macos 15.0，architecture arm64）。
- Android：AsterLink-<版本>-android-<abi>.apk，abi 可以是 arm64-v8a、armeabi-v7a 或 x86_64（package-native-android.py --abi，可以多选）。
- iOS：resign-ipa.py 输出已签名的 IPA，以及 manifest.plist、bundle id（app_id）和版本。
- 教程现有文案写的是：Windows 仅支持 x64；macOS 仅支持 Apple 芯片，“暂不支持 Intel 芯片的 Mac”。

## 3. 关键决策（默认值，用户可在第 1 轮开工前推翻）

| 编号 | 决策 | 理由 |
| --- | --- | --- |
| D1 | 唯一数据源是后端数据库，表名 client_downloads。网站不再读 .env 的下载变量，也不保留回退：旧后端没有该接口时，网站各平台显示“暂未发布” | 两个数据源并存会出现“改了后台不生效”的困惑；部署顺序固定为先后端再网站，代价可接受 |
| D2 | 一条记录对应“平台 + 架构 + 格式”，三者组合唯一。发布新版本就是修改这条记录（换版本号、地址、校验值），旧版本的信息保留在审计记录里。不做多版本并存和草稿 | 管理员要的是增、删、改；“每个组合只有一个当前版本”最不容易出错，页面也只展示最新版 |
| D3 | 平台、架构、格式都来自前后端各自维护的一份固定白名单（第 4.2 节），由管理员从下拉框选择，不能自由输入。新增一种架构或格式需要改代码，两端同时改 | 公开页面上的文字全部来自固定文案，便于审校；后端能严格校验地址后缀 |
| D4 | “不显示 URL”的含义：/download 的可见文字、HTML 属性和服务端组件传给浏览器的数据里都不含真实地址；公开列表接口也不返回地址。按钮指向本站的跳转路径 /download/get/{平台}/{架构}/{格式}，由服务器 302 跳转到真实地址。真实地址不视为机密，下载者在浏览器的下载记录里仍能看到最终地址；需要防盗链时应在 CDN 侧做签名 URL，不在本期范围 | 满足“页面不显示地址”，又不假装能隐藏一个必须让浏览器下载的地址。跳转路径固定，还可以直接发给用户或在客户端的“请升级”提示里使用，地址更换后旧链接仍然有效 |
| D5 | 跳转路径是语义路径，不用条目 id。组合不存在或已下架时，跳回 /download#{平台}，不报 404 | 删除后重建的条目路径不变；已失效的旧链接也能把用户带到下载页 |
| D6 | iOS 分三种格式：ipa（网页安装，Ad Hoc 或企业签名）、app_store、testflight。网页安装所需的 manifest.plist 由网站根据条目里的 IPA 地址、bundle id 和版本号**动态生成**，路径为 /download/get/ios/arm64/ipa/manifest.plist；管理员不需要另外上传清单文件 | iOS 的分发方式还没定（memory 记录为待定），三种格式都留好入口；动态生成后少一个需要同步的文件，也不依赖 iOS 是否跟随清单地址的重定向 |
| D7 | 商店类格式（app_store、testflight）的地址只允许 apps.apple.com 和 testflight.apple.com，不需要 SHA-256 和大小。文件类格式要求 https 地址（非生产环境允许回环地址 http），不能带账号密码，路径后缀必须与格式一致（大小写不敏感，允许带查询参数），并且必须填写 SHA-256 | 沿用 downloads.ts 现有规则，再加上商店白名单；防止把 .dmg 地址登记成 Windows 条目 |
| D8 | 系统要求（例如“Windows 10 2004 及以上”“macOS 15 及以上”）和各架构的中文说明都是前端固定文案，不作为后台字段 | 最低系统版本只会随客户端代码变化，变化时本来就要发版；不给公开页面开自由文本的口子 |
| D9 | 网站服务端读取公开列表时带 30 秒的进程内缓存，请求超时 3 秒。读取失败时，如果有上一次成功的结果就继续使用（最长 24 小时）；没有的话页面照常返回 200，每张平台卡显示“下载信息暂时无法获取，请稍后刷新”，与“暂未发布”区分开 | 首页和下载页都要读，缓存能保证首字节速度和后端故障时的可用性；后台保存后最多 30 秒生效，管理端页面会说明 |
| D10 | 卡片内按固定顺序列出各架构的按钮，推荐架构用实心按钮（Windows x64、macOS arm64、Android arm64-v8a、Linux x64），其余用浅色按钮。各架构版本号相同时，卡片右上角只显示一个版本徽章；不同时，每个按钮旁边显示各自的版本 | 只有一个架构时与现在的外观完全一致；多架构时不需要用户懂技术也能选对 |
| D11 | 每个文件都显示 SHA-256，沿用现有的 4 位一组 Code 块。有多个文件时，每个文件下方各自显示一个，并标明对应的架构和格式。Android 的签名证书指纹显示在 Android 卡片内，取第一个上架的 apk 条目（各架构使用同一把签名钥匙） | 保持现有设计，信息不减少 |
| D12 | 页面顺序：平台卡依次为 Windows、macOS、Android、iOS、Linux；“安装、升级与卸载”卡依次为 Windows、macOS、Android、iPhone，Linux 暂不放（还没有客户端，文案无从核实）。锚点 #windows、#macos、#android、#ios、#linux，现有 #android 和 #ios 保持不变 | 按用户指定的顺序；教程和首页的锚点链接继续有效 |
| D13 | Linux 卡片：有上架条目时与其他平台一样展示；没有时显示“Linux 版本即将推出，敬请期待”。文案不能用“开发”，这是 copy-guard 禁词。后台现在就允许登记 Linux 条目 | 预留到位，以后上线不需要改网站 |
| D14 | 首页“多平台客户端”保持 4 张卡（不加 Linux，避免破坏现有 4 列网格），顺序改为 Windows、macOS、Android、iOS。某平台有上架条目时显示“立即下载”，链接到 /download#{平台}；没有时显示“即将上线”；后端读取失败且没有缓存时显示指向 /download 的“前往下载页” | 不在后端出故障时把已发布的平台说成“即将上线” |
| D15 | 删除“开源许可与源代码”卡片、ANDROID_SOURCE_URL、IOS_SOURCE_URL 及相关字段；第 3 轮的守卫规定 /download 页面文字不得出现“开源、许可、源代码、GPL、Mihomo、内核”等字样 | 与 handoff_app_standardization.md D9/D10“不向用户展示底层信息”一致；GPL 声明由用户放在纸质说明书里，不再讨论 |
| D16 | 管理端文案沿用运维措辞（不在 copy-guard 扫描范围内），管理端的成功提示一律通过统一的 toast 模块（src/lib/toast.tsx）弹出；字段错误显示在表单内 | 符合 handoff_message.md D4、D13 |
| D17 | 打包脚本不再输出 .env 行，改为在 release-private 目录输出“后台录入单”（download-entry.txt），列出平台、架构、格式、版本、文件名、SHA-256、字节数，Android 再加证书指纹、iOS 再加 bundle id；管理员上传文件后照着填入后台 | 打包和录入之间没有猜测；文件放在 private 目录，不进公开包 |

## 4. 数据模型（第 1 轮）

### 4.1 表 client_downloads

| 字段 | 说明 |
| --- | --- |
| id | uuid 主键（审计的 TargetID 要求 uuid） |
| platform | windows / macos / android / ios / linux |
| arch | 见 4.2 |
| format | 见 4.2 |
| app_version | 客户端版本号，规则与现有 VERSION 正则一致（数字.数字.数字，可带后缀），最长 32 字符 |
| url | 真实地址，最长 1024 字符，校验规则见 D7 |
| sha256 | 64 位小写十六进制，入库前规范化（去空格和冒号、转小写）；商店类为空 |
| size_bytes | 可选，正整数，上限 8 GiB |
| signing_cert_sha256 | 可选，只允许 Android apk 条目填写，同样规范化 |
| bundle_id | iOS ipa 条目必填，反向域名格式，最长 155 字符；其他条目必须为空 |
| published | 是否上架，默认 false。新增条目默认下架，确认无误后再上架 |
| released_at | 创建时写入，以后 app_version 或 url 变化时更新；页面显示为“更新于 YYYY-MM-DD” |
| version | 乐观锁，从 1 开始，每次修改加 1 |
| updated_by、created_at、updated_at | 与其他管理表一致 |

- 唯一索引：(platform, arch, format)。
- 总条数上限 60。超过时新增返回 400，提示“条目过多”。
- 删除是硬删除。审计明细里保留被删条目的平台、架构、格式、版本、地址的主机名加文件名，以及 SHA-256。
- 迁移：加入 AutoMigrate 列表，补建唯一索引；重复执行没有副作用。旧后端回滚时忽略这张表，在发布演练中验证。

### 4.2 白名单（前后端各一份，第 3 轮用单元测试比对两份是否一致）

| 平台 | 架构（顺序即页面顺序，带 ★ 的为推荐） | 格式 |
| --- | --- | --- |
| windows | x64★（64 位 Intel / AMD 电脑）、arm64（ARM 版 Windows 电脑） | exe（安装程序）、msi、zip（免安装版） |
| macos | arm64★（Apple 芯片，M 系列）、x64（Intel 芯片）、universal（通用版） | dmg、pkg |
| android | arm64-v8a★（64 位手机，绝大多数）、armeabi-v7a（较旧的 32 位手机）、x86_64（x86 设备）、universal（通用版） | apk |
| ios | arm64（iPhone） | ipa（网页安装）、app_store、testflight |
| linux | x64★、arm64 | appimage、deb、rpm、tar.gz |

后缀对应：exe→.exe，msi→.msi，zip→.zip，dmg→.dmg，pkg→.pkg，apk→.apk，ipa→.ipa，appimage→.AppImage，deb→.deb，rpm→.rpm，tar.gz→.tar.gz。比较时不区分大小写。

## 5. 接口（第 1 轮）

### 5.1 管理接口（console 组，需要管理员权限）

- GET /api/v1/admin/console/downloads：返回全部条目（含地址、上架状态、乐观锁版本），按平台、架构和格式的固定顺序排列；同时返回白名单，供管理端渲染下拉框，避免前端硬编码与后端不一致。
- POST /api/v1/admin/console/downloads：新增。重复的组合返回 409 duplicate，提示“该平台的此架构与格式已存在，请直接编辑”。
- POST /api/v1/admin/console/downloads/:id：修改，必须带 version。版本不符返回 409 version_conflict；没有改动返回 400 no_change。平台不可修改，需要换平台时删除后重建；架构和格式可以修改，但仍受唯一约束。
- DELETE /api/v1/admin/console/downloads/:id：删除。
- 写操作都走 lockConsoleAdmin，并在同一事务内写审计（admin.download_create / admin.download_update / admin.download_delete）。审计明细记录变化字段的 from/to；地址只记录主机名和文件名，以控制长度。
- 字段错误沿用 fieldError 的结构（字段名加中文说明），管理端把错误显示在对应输入框下方。

### 5.2 公开接口（不需要登录，受全局 APIRate 限流）

- GET /api/v1/downloads：只返回已上架条目，字段为 platform、arch、format、app_version、size_bytes、sha256、signing_cert_sha256、bundle_id、released_at。**不含 url 和 id**。响应带 Cache-Control: public, max-age=30。
- GET /api/v1/downloads/:platform/:arch/:format：已上架时返回 302，Location 为真实地址，并带 Cache-Control: no-store 和 Referrer-Policy: no-referrer；不存在、已下架或参数不在白名单内时返回 404 not_found。
- 两条路由都加入安全矩阵的 public 白名单。
- 跳转是从网站服务器发起的，必须像 /api/v1 代理那样，按 CLIENT_IP_HEADER 转发浏览器的地址；否则所有下载请求都会计入网站服务器这一个 IP 的限流桶。

## 6. 网站下载页（第 3 轮）

### 6.1 数据读取

- 新增服务端模块（只在服务端运行），负责请求后端 GET /downloads，带缓存、超时和失败回退（D9），并把结果按白名单重新组装成页面需要的结构：只拷贝已知字段，后端以后即使多返回字段（包括地址）也不会传到浏览器。未知的平台、架构或格式直接丢弃。
- /download、首页、/guide 三处都使用这个模块，保持 force-dynamic。

### 6.2 页面结构（不大改，只扩展）

- 页首标题和说明不变。
- 五张平台卡按 D12 排序，保持现有结构：大图标、标题、系统要求一行、版本徽章、按钮区、SHA-256、“安装后不会用？查看使用教程”。图标：Windows 用 IconBrandWindows，macOS 和 iOS 用 IconBrandApple（与现有 iPhone 卡一致），Android 用 IconBrandAndroid，Linux 用 Tabler 的 Linux 品牌图标（实施时确认图标名存在，没有的话改用 IconTerminal2 这类中性图标）。
- 按钮文案：
  - Windows：“下载安装程序（x64）”。
  - macOS：“下载 DMG（Apple 芯片）”。
  - Android：“下载 APK”，有多个架构时在括号里写架构说明。
  - iOS ipa：“在 iPhone 上安装”；app_store：“前往 App Store”；testflight：“通过 TestFlight 安装”。
  - Linux：“下载 AppImage / DEB / RPM / 压缩包（x64）”。
  - 有大小时都在括号里附上，例如“（45.9 MB）”。
- 系统要求（固定文案）：Windows 为“Windows 10（2004）及以上或 Windows 11”；macOS 为“macOS 15 及以上”；Android 为“Android 8.0 及以上”；iOS 为“iOS 17 及以上 · iPhone”；Linux 先不写。架构说明并入按钮文案或按钮下方的小字，不再写死在标题行。
- iOS ipa 的说明文字（用 Safari 打开、信任开发者、仅限已登记设备）保留，只在 ipa 条目上架时显示；商店类格式显示一句对应的简短说明。
- 平台没有上架条目时，沿用现有灰色 Alert“{平台}版本暂未发布，请稍后再来”；Linux 用 D13 的文案；读取失败时显示 D9 的文案。
- “安装、升级与卸载”：保留现有的 Android、iPhone 两张卡，内容不变；新增 Windows、macOS 两张，放在它们前面，内容取自 guides.ts 中已与客户端核对过的“升级与卸载”和“下载安装”章节：
  - Windows：运行安装程序；遇到“Windows 已保护你的电脑”时点“更多信息 → 仍要运行”；升级前先从任务栏通知区域图标选择“退出”，再覆盖安装；卸载在“设置 → 应用”中进行，询问是否删除保存的数据时默认选“否”。
  - macOS：打开 DMG 后拖入“应用程序”，并从“应用程序”里打开；首次打开被系统拦截时的放行步骤；升级时先断开连接，再替换应用，提示“后台服务需要更新”时到“账户”页处理；卸载前先在“账户”页移除后台服务，再使用安装包里附带的卸载程序。
  - 两张卡都带“详细图文步骤见使用教程”链接（/guide?platform=windows、/guide?platform=macos）。文案受教程 D10 约束：桌面端说明不出现 VPN、内核、协议等客户端隐藏的词。
- 删除“开源许可与源代码”卡片（D15）。

### 6.3 站内跳转路由与 iOS 清单路由

- /download/get/{平台}/{架构}/{格式}：Next.js 路由处理器。参数先过白名单，再在服务端以 redirect: manual 请求后端的公开跳转接口，取得 Location 后按 D7 的规则重新校验，然后对浏览器返回 302，带 no-store、no-referrer 和 X-Robots-Tag: noindex。后端返回 404、校验不通过或请求超时时，302 跳回 /download#{平台}，并且不在任何响应里回显上游返回的内容。
- /download/get/ios/arm64/ipa/manifest.plist：同样先取得 IPA 地址，再结合缓存列表里的 bundle_id 和 app_version 生成标准的 OTA 清单。标题固定为 AsterLink，所有值经 XML 转义，Content-Type 为 application/xml，no-store。条目不可用时返回 404，iOS 会提示“无法安装”。
- iOS 安装按钮的 itms-services 链接只引用本站清单路由的绝对地址。生成绝对地址时使用站点公开地址的配置（实施时核实项目里现有的配置项，例如 WEBSITE_BASE_URL 或请求的 Host 经可信代理后的值），不能信任任意 Host 头。

### 6.4 首页与使用教程

- 首页：按 D14 处理。
- 使用教程：Windows 和 macOS 的 downloadKey 改为 windows/macos，GuideDownloads 扩展为四个平台。显示的版本取推荐架构的版本，没有推荐架构时取第一个上架条目的版本。“前往下载”跳到对应的锚点。教程中“仅支持 x64”“暂不支持 Intel 芯片的 Mac”这类设备说明暂时不动；以后后台上架 arm64 版 Windows 或 Intel 版 Mac 时，必须同步修改教程文案。这一点写进第 3 轮的运维文档。

## 7. 管理后台“客户端下载”板块（第 2 轮）

- 路由 /admin/console/downloads，标签名“客户端下载”，放在“公告”之后；同步更新页首说明文字。
- 布局：五个平台分组，按 D12 的顺序排列。每组一个表格，列为：架构、格式、版本、大小（MB）、SHA-256（前 8 位，悬停显示全部）、状态（已上架 / 未上架，可直接切换）、更新时间、操作（编辑、在新标签打开地址、删除）。组标题右侧有“添加”按钮。没有条目的平台显示“尚未添加”。
- 新增/编辑弹窗：平台（从分组带入，编辑时不可改）、架构、格式（都是下拉框，选项随平台变化，已存在的组合标为不可选）、版本号、下载地址、SHA-256、文件大小（字节，旁边实时显示换算后的 MB）、签名证书 SHA-256（只在 Android 显示）、Bundle ID（只在 iOS ipa 显示）、是否上架。选择商店类格式时，隐藏 SHA-256 和大小输入框，并提示允许的域名。前端先做与后端一致的即时校验，以后端结果为准。
- 页面顶部有固定说明：保存后约 30 秒内在下载页生效；地址需要先确认可以下载；新增条目默认不上架。
- 删除：确认弹窗中写明平台、架构、格式和版本。
- 成功提示（toast）：“已添加”“已保存”“已上架”“已下架”“已删除”。409 冲突时在表单内显示“已被其他管理员修改，请刷新后重试”，并提供刷新按钮。
- 在新标签打开地址：管理员可以在这里直接核对真实地址，这里不受 D4 限制。

## 8. 兼容、安全与风险

- **部署顺序**：先部署后端第 1 轮，在后台录入现有的 Android/iOS 条目（取值见旧 .env 或打包输出）并上架，再部署网站第 3 轮。如果网站先上线，各平台会显示“暂未发布”，这是 D1 的已知代价，部署文档里写明。
- **开放重定向**：跳转目标只来自数据库里经过校验的地址，路径参数先过白名单，不存在把任意参数拼进 Location 的路径。网站侧对 Location 再做一次 D7 校验，防止后端被替换或配置错误时跳到 javascript: 等非法协议。
- **SSRF**：后端和网站都不会主动访问管理员填写的地址。不做“检测链接”功能，这也是原因之一。
- **限流与真实 IP**：见 5.2；第 3 轮的验收里专门检查一次。
- **缓存一致性**：多个 Next.js 进程各自缓存 30 秒，切换版本时最多有 30 秒新旧版本混用，对下载没有实际影响。后端跳转接口不缓存，所以按钮总是指向最新地址。
- **微信内置浏览器**：它无法直接下载 APK，这是已知限制，本期不处理，留给用户实测时观察（U-68）。
- **CSP**：如果项目已经配置了 CSP，itms-services 链接和跳转都是导航而不是资源加载，不需要放开；实施时核实一次。
- **体积**：页面主要在服务端渲染；新增的客户端代码只有按钮和展开逻辑，不新增依赖。

## 9. 三轮实施与逐轮验收

开发方执行验收，结果如实记录为 PASS、FAIL 或 BLOCKED；没有实际测过的项目不得记为 PASS。

### 第 1 轮：后端数据、接口与审计

范围：第 4、5 节。

| 编号 | 内容 |
| --- | --- |
| DL1-01 | 迁移：新库直接建表，旧库升级后建表并带唯一索引；重复执行迁移没有副作用；发布演练（test-release-rehearsal.py）的升级和回滚都通过 |
| DL1-02 | 校验表格测试：五个平台的每个合法架构和格式组合都能保存；不在白名单的平台、架构或格式，以及跨平台组合（如 windows 加 dmg）都返回字段错误 |
| DL1-03 | 地址规则：http（生产环境）、带账号密码、javascript:、后缀不符、超长地址都拒绝；后缀大小写不敏感、带查询参数可以通过；非生产环境的回环 http 可以通过；商店类只接受 apps.apple.com 和 testflight.apple.com |
| DL1-04 | SHA-256、证书指纹规范化（大写、冒号、空格）后入库；文件类缺少 SHA-256 时拒绝；非 Android 填证书指纹、非 iOS ipa 填 bundle id 时拒绝；ipa 缺少 bundle id 时拒绝 |
| DL1-05 | 新增重复组合返回 409 duplicate；修改时版本不符返回 409；没有改动返回 400 no_change；超过 60 条时拒绝 |
| DL1-06 | released_at 只在 app_version 或 url 变化时更新；上架和下架不改变它 |
| DL1-07 | 每次写操作恰好写一条审计记录，Action 与明细正确，明细中不含完整地址 |
| DL1-08 | 权限：匿名用户访问管理接口返回 401，普通用户 403，管理员在只读查看用户期间不能写入；安全矩阵测试通过，新的公开路由已加入白名单 |
| DL1-09 | 公开列表只含已上架条目，JSON 中断言不存在 url 和 id 键，顺序固定，带 Cache-Control |
| DL1-10 | 公开跳转：已上架时返回 302 和正确的 Location，并带 no-store、no-referrer；下架、不存在、参数非法时返回 404；跳转响应体中没有其他条目的信息 |
| DL1-11 | 后端 go vet 和全量 go test 通过；CI 全绿，并确认 backend 相关 job 实际运行 |

### 第 2 轮：管理后台板块

范围：第 7 节。开工前确认 message 第 2 轮（#115）已经合并，统一 toast 模块和全源码守卫已在 main 上。

| 编号 | 内容 |
| --- | --- |
| DL2-01 | 管理员能看到“客户端下载”标签，五个平台分组按顺序显示，没有条目时显示空状态；普通用户访问时显示“无权访问” |
| DL2-02 | 为每个平台各新增一条（Windows x64 exe、macOS arm64 dmg、Android arm64-v8a apk、iOS ipa、Linux x64 appimage），列表即时出现，默认未上架 |
| DL2-03 | 下拉框随平台联动，已存在的组合不可选；切换到商店类格式时隐藏 SHA-256 和大小 |
| DL2-04 | 前端即时校验与后端一致；后端的字段错误显示在对应输入框下方 |
| DL2-05 | 编辑版本号后保存成功，弹出 toast；用第二个浏览器上下文先改同一条，再在第一个上下文保存，表单内出现冲突提示，刷新后能继续编辑 |
| DL2-06 | 上架/下架开关可用，并出现 toast；删除时弹出确认框，确认后条目消失 |
| DL2-07 | 文件大小输入时实时显示换算后的 MB；SHA-256 悬停显示完整值；“在新标签打开”的链接正确 |
| DL2-08 | 浅色、深色外观和 1280px、768px 宽度下截图，表格不溢出（窄屏可横向滚动） |
| DL2-09 | lint、typecheck、build、test:toast（守卫）、test:copy、test:theme 通过；新的 admin-downloads.spec.ts（放进 payment fixture 套件）与已有的 admin-console*.spec 通过 |

### 第 3 轮：下载页、首页、教程与收尾

范围：第 6 节；去掉环境变量；打包脚本录入单（D17）；文档。

| 编号 | 内容 |
| --- | --- |
| DL3-01 | /download 的平台卡顺序为 Windows、macOS、Android、iOS、Linux，锚点齐全；“安装、升级与卸载”的顺序为 Windows、macOS、Android、iPhone |
| DL3-02 | 页面 HTML 全文（包括服务端注入的页面数据）搜索夹具里的真实下载域名，结果为 0；所有下载按钮的 href 都是 /download/get/… 或 itms-services（其中只引用本站的清单路由） |
| DL3-03 | 点击按钮（拦截请求，不实际下载）：站内路由返回 302，Location 等于后台登记的地址；下架后同一路径 302 回 /download#{平台} |
| DL3-04 | 多架构：同一平台上架两个架构时出现两个按钮，推荐架构为实心按钮；版本相同时只显示一个徽章，版本不同时每个按钮各自显示版本；每个文件都显示对应的 SHA-256 |
| DL3-05 | iOS 清单路由：生成的 plist 能被标准解析器解析，bundle id、版本和 IPA 地址正确，特殊字符已转义；app_store 和 testflight 条目的按钮文案与跳转正确 |
| DL3-06 | 未上架的平台显示“暂未发布”；Linux 显示“即将推出”；后端停掉时，页面仍返回 200 并显示“暂时无法获取”；后端恢复并在缓存过期后自动恢复正常 |
| DL3-07 | 页面上不再有“开源许可与源代码”卡片，页面文字不含 D15 列出的词；守卫纳入 test:copy 或 ui-copy.spec |
| DL3-08 | 首页四张卡的顺序与状态和后台数据一致，“立即下载”跳到对应锚点；教程中 Windows/macOS 上架后显示“前往下载”和版本 |
| DL3-09 | 管理端到下载页的端到端测试：在后台修改版本号，等缓存过期后（测试环境可以把缓存时间配置得更短，这个配置只在非生产环境生效）下载页显示新版本 |
| DL3-10 | 跳转请求带上了浏览器的真实地址：后端日志或限流桶按浏览器 IP 计数，而不是按网站服务器 IP |
| DL3-11 | 手机宽度（375px）和浅色/深色下截图，按钮不溢出，Code 块可以换行；对比度不低于 4.5:1 |
| DL3-12 | 代码库中不再有 ANDROID_APK_*、IOS_IPA_*、*_SOURCE_URL 的读取；.env.example 已删除对应段落；test-payment-web.py 改为由夹具录入条目 |
| DL3-13 | 四个打包脚本都在 release-private 中输出 download-entry.txt，字段与后台表单一一对应；release_scrub 检查通过（该文件不进入公开目录） |
| DL3-14 | 文档：新增 docs/client-downloads.md，内容包括操作流程、白名单、跳转路径、部署顺序、教程文案的同步提醒；android-release.md 第 4 节和 iOS、macOS、Windows 的发布文档改为“录入后台”；关于 .env 的说明同步更新 |
| DL3-15 | test:downloads（改写后）、test:home、test:guide、test:copy、test:theme、lint、typecheck、build 通过；home.spec、guide.spec、ui-copy.spec、theme.spec 与新的 download.spec 通过；本地全量 payment fixture 套件和 catalog 套件通过；CI 全绿 |

## 10. 测试方法要点

- 后端使用现有的 newConsoleEnv 和 e.call 测试工具；跳转测试要关闭自动跟随重定向，并断言 Location 和响应头。
- 网站的纯逻辑部分（白名单、按钮文案、排序、徽章合并规则、plist 生成与转义、Location 再校验）放进 src/lib 中不依赖 React 的模块，由 test:downloads 覆盖。前后端白名单是否一致，通过读取后端公开接口返回的白名单或一份共享的 JSON 来比对。按项目惯例，首选放进 shared 目录，再由 sync-backend-shared.py 同步给后端，实施时核实这个机制是否适用。
- Playwright 只拦截发往夹具下载域名的请求，不访问外网。夹具中的下载域名继续使用 downloads.example.test，通过管理接口录入条目。
- “后端停掉”的场景：单独写一个 spec，在一个独立的网站进程上把 API_INTERNAL_URL 指向不可达的端口，避免影响其他用例。
- 缓存时间：通过一个只在非生产环境生效的环境变量缩短（例如 1 秒），生产环境固定为 30 秒，并带 copy-guard 标记注释。

## 11. 用户部署后的实测（接在现有 U 系列之后）

| 编号 | 内容 |
| --- | --- |
| U-65 | 在后台录入真实的 Windows Setup.exe 和 macOS DMG 地址并上架，在 Windows 电脑和 Mac 上从 /download 下载，确认文件能打开，SHA-256 与页面一致 |
| U-66 | Android 手机用系统浏览器从 /download 下载 APK 并覆盖安装；后台换一个新版本后，30 秒内页面版本更新 |
| U-67 | iPhone 用 Safari 点击“在 iPhone 上安装”，系统出现安装提示（需要已签名的 IPA 和已登记的设备；条件不具备时记为 BLOCKED） |
| U-68 | 在微信内置浏览器打开 /download，观察各按钮的行为并记录，作为后续是否加入“请在浏览器中打开”提示的依据 |
| U-69 | 后台下架某个条目后，旧的跳转链接回到下载页而不是报错；浏览器“查看源代码”中看不到真实地址 |

## 12. 完成定义

三轮 PR 都合并到 main；第 9 节的验收全部 PASS，或者如实记为 BLOCKED 并说明原因；CI 全绿；网站不再读取下载相关的 .env；/download 页面和页面数据中不含真实地址；“开源许可与源代码”卡片已删除；本文第 13 节记录每一轮的分支、PR、提交和证据位置。

## 13. 实施记录

（每轮完成后追加：日期、分支、PR、合并提交、验收结果表、证据目录 artifacts/download-roundN、遗留问题。）

### 第 1 轮：后端数据、接口与审计（2026-11-05，分支 worktree-download-round1-backend，基线 c6a48e0，PR #116，squash 合并为 78c56c0）

实现位置：
- backend/internal/model/download.go：ClientDownload 模型。
- backend/internal/api/downloads.go：白名单、校验、管理接口和两个公开接口。
- server.go：注册路由。
- store.go：在 AutoMigrate 中加入新表。
- operations/readiness.go：doctor 的必需表清单加入 client_downloads。
- backend/DEPLOYMENT.md：新增“客户端下载”一节。
- scripts/test-release-rehearsal.py：ALL_TABLES 加入新表；在“套餐升级”阶段加入下载回滚演练。

测试：
- internal/api/downloads_test.go（5 个测试）。
- internal/store/download_migrate_test.go。
- security_review_test.go：公开白名单加入两条路由。

本地环境：私有 Unix socket PostgreSQL 16，与 test-native.py 的启动方式相同。worktree 中没有 Mihomo 子模块，没有构建 Core、节点代理二进制，依赖它们的端到端测试在本地跳过，由 CI 执行。

| 编号 | 结果 | 证据 |
| --- | --- | --- |
| DL1-01 | PASS | TestClientDownloadMigrationOverExistingData：在已迁移的库上删表后重新迁移两次，其他表指纹不变；唯一索引 idx_client_download_variant (platform, arch, format) 存在；重复组合被数据库拒绝；默认 published=false、version=1；再次迁移后行数不变。发布演练脚本已加入新表和回滚检查（dbe75ca 上 404、表指纹不变、再升级后 302 恢复），本地只做了 py_compile，完整演练由 CI 的 “Upgrade, payments, … rollbacks” 步骤运行，PR #116 的 “Release rehearsal / migration / rollback” job 通过 |
| DL1-02 | PASS | TestValidateClientDownload：白名单内全部 27 个组合都能通过；未知平台、跨平台的架构或格式都返回对应字段错误 |
| DL1-03 | PASS | 同上，覆盖以下情况：生产环境的 http、回环 http（生产环境拒绝，非生产允许，IPv6 回环也允许）、账号密码、javascript:、# 片段、后缀不符、后缀只出现在查询参数里、超长、商店外域名、商店 http。.AppImage 大写后缀和带查询参数的地址可以通过 |
| DL1-04 | PASS | 大写或带冒号空格的 SHA-256、证书指纹都会被规范化；文件类缺少 SHA-256 时拒绝；商店类填写 SHA-256 或大小时拒绝；非 apk 填证书指纹、非 ipa 填 bundle id、ipa 缺少 bundle id 时都拒绝 |
| DL1-05 | PASS | 重复组合返回 409 duplicate（新增，以及把一条改成已存在的组合时）；版本不符返回 409 version_conflict；没有改动返回 400 no_change；改平台返回 400 invalid_download（field=platform）；不存在的 id 或非 uuid 返回 404；第 60 条之后返回 400 too_many（TestClientDownloadsCapAndRetiredVariants） |
| DL1-06 | PASS | 上架时 released_at 不变；改版本号和地址后 released_at 等于当时时间 |
| DL1-07 | PASS | Windows 条目的审计为 create 1、update 2、delete 1；明细只有主机名和文件名，不含 https:// 和查询参数中的 token；app_version 的 from/to 正确 |
| DL1-08 | PASS | 匿名访问返回 401，普通用户 403 admin_required；TestSecurityRouteAccessMatrix 通过（新的管理路由自动纳入检查：只读查看用户期间所有写接口都返回 403） |
| DL1-09 | PASS | 公开列表只含已上架条目，顺序为 windows/exe、android/apk、ios/ipa；JSON 中没有 url、id 键，响应正文中也没有地址域名和 token；Cache-Control 为 public, max-age=30；白名单中已不存在的组合（模拟“退役”的平台）不会出现 |
| DL1-10 | PASS | 已上架时返回 302，Location 为登记地址，并带 no-store 和 no-referrer；未上架、跨平台组合、未知平台、不存在的条目返回 404 not_found，且没有 Location；手工把数据库中的地址改成 javascript: 后返回 404；删除后返回 404 |
| DL1-11 | PASS | go vet ./... 与 gofmt 通过；`go test -race -count=1 ./...` 全部 ok（api 包 479 秒），EXIT 0；日志在本机 /tmp/dl-round1-gotest.log。改动 doctor 的表清单后，又单独跑了 TestConsoleReadinessAndAdministratorPostgres 和 TestTicket*，都通过。PR #116 的 CI 16 个 job 全部通过，其中 “Backend / PostgreSQL / race” 实际运行 |

与规划的差异：
1. 前后端白名单一致性改为“后端 Go 常量 + backend/internal/api/testdata/client_downloads_catalog.json 快照”，由 TestClientDownloadCatalogSnapshot 保证两者相同；第 3 轮的前端测试直接比对这份快照，不走 shared 和 sync-backend-shared.py 的同步机制（那套机制面向 nodepolicy 和原生契约，接入成本高于收益）。
2. 管理列表同时返回 catalog（平台、架构、格式、后缀、商店域名、上限 60），供第 2 轮的下拉框使用。
3. 地址额外拒绝 # 片段；跳转前对库中地址按当前环境重新校验，不合法时返回 404。
4. 白名单最多只有 27 个组合，60 条上限现在不会触发，只作为白名单扩展后的防线，测试中用直接写库的方式验证。

遗留问题：无。第 2 轮可以开工（message 第 2 轮 #115 已合并）。

### 第 2 轮：管理后台板块（2026-11-05，分支 worktree-download-round2-console，基线 78c56c0，PR #117，squash 合并为 f31eecf）

实现位置：
- frontend/src/lib/download-catalog.ts：平台、架构、格式的中文名称；与后端 validateDownload 同顺序、同文案的即时校验；已占用组合、首个可用组合、字节数解析、把一行转回提交体（只含后端接受的键）。不依赖 React，第 3 轮的下载页可以直接复用这些名称。
- frontend/src/components/console/console-downloads.tsx：五个平台分组的表格、新增/编辑弹窗、上架开关、删除确认。
- frontend/src/app/admin/console/downloads/page.tsx：路由。
- console-shell.tsx：在“公告”之后加入“客户端下载”标签，并更新页首说明。

测试：
- frontend/scripts/download-catalog.test.mjs（并入 test:downloads，8 项）：读取后端快照 testdata/client_downloads_catalog.json，检查每个平台、架构、格式都有名称且平台顺序为 Windows、macOS、Android、iOS、Linux；27 个组合全部通过；与后端相同的拒绝和放行表格；十六进制规范化；字节数解析；已占用组合；提交体的键。
- frontend/tests/admin-downloads.spec.ts（加入 playwright.payment.config.ts）：3 个用例，并在结束时通过界面删除本 spec 添加的全部条目。
- 夹具 payment_acceptance_test.go 新增账号 dlroot（管理员）和 dla（普通用户），排在最早创建的一组，不影响用户列表前 20 条。

| 编号 | 结果 | 证据 |
| --- | --- | --- |
| DL2-01 | PASS | dla 打开 /admin/console/downloads 显示“无权访问”；dlroot 看到“客户端下载”标签处于选中状态，五组顺序为 windows、macos、android、ios、linux，每组显示“尚未添加”（截图 downloads-empty.png） |
| DL2-02 | PASS | 依次添加 Windows x64 exe、macOS arm64 dmg、Android arm64-v8a apk（带签名证书指纹）、iOS arm64 ipa（带 Bundle ID）、Linux x64 AppImage，每次出现“已添加”提示，行立即出现且显示“未上架”，计数为 5 |
| DL2-03 | PASS | 再次添加 Windows 时默认选中 x64 + MSI，格式下拉中的“安装程序 .exe（已添加）”不可选；添加 iOS 时默认 App Store，SHA-256、文件大小、Bundle ID 输入框都不出现，提示 apps.apple.com，切换到 TestFlight 后提示 testflight.apple.com |
| DL2-04 | PASS | 前端即时校验依次给出版本号、https、后缀、SHA-256 的错误，文案与后端一致；带冒号的大写 SHA-256 规范化后保存。后端字段错误：地址中的 %ZZ 是浏览器能接受、后端解析会拒绝的转义，保存后“下载地址格式不正确”显示在地址输入框下方，修改地址后消失。商店域名不符时的错误也显示在地址框下 |
| DL2-05 | PASS | 把 Windows 版本改为 1.2.4，出现“已保存”提示；第一个页面打开编辑后，第二个浏览器上下文先保存为 1.2.5，第一个页面保存时表单内显示“已被其他管理员修改，请刷新后重试”；点“刷新”后表单换成 1.2.5，再改为 1.2.6 保存成功 |
| DL2-06 | PASS | 打开开关出现“已上架”提示，经网站代理请求公开列表，可以看到 windows 条目且响应中没有地址域名；关闭开关出现“已下架”提示；删除 Linux 时确认框写明“Linux · x64 · AppImage”和版本 1.2.3，确认后出现“已删除”提示，该组回到“尚未添加” |
| DL2-07 | PASS | 文件大小输入 48128121 时旁边实时显示“约 45.9 MB”，列表中显示 45.9 MB；SHA-256 列显示前 8 位，title 为完整值；“打开地址”的 href 为登记的地址，target=_blank，rel 含 noopener |
| DL2-08 | PASS | 浅色、深色 × 1280、768 共 4 张截图（downloads-light-1280.png 等），layout.ts 的 overflowing 检查都为空；768 宽时表格在卡片内横向滚动 |
| DL2-09 | PASS | lint、typecheck（tsc --noEmit）、test:toast（7）、test:copy（3）、test:theme（5）、test:downloads（14）本地通过；next build 成功。admin-downloads.spec.ts 单独运行 3 passed（/tmp/dl-round2-web-2）；完整支付夹具浏览器套件 115 passed（10.3 分钟，含 admin-console、admin-console-2、admin-console-plans，/tmp/dl-round2-web-full）。本 spec 约 18 秒，CI 浏览器 job 的 25 分钟上限仍有余量。PR #117 的 CI 16 个 job 全部通过。合并后 main 上的浏览器 job 两次因 Ubuntu 软件源下载过慢（约 40 kB/s，安装浏览器依赖用了 8–9 分钟）触到 25 分钟上限被取消，日志中没有失败用例，与代码无关 |

本地证据：截图在 /tmp/dl-round2-web-2/browser-artifacts/screens/（downloads-empty、downloads-list、downloads-{light,dark}-{1280,768}）。

与规划的差异：
1. 后端字段错误的浏览器验证没有拦截请求，用的是前后端解析差异的真实情况（%ZZ）。
2. 上架开关失败时用 toast.error 提示并刷新列表，没有单独的页内错误框；表单内的错误仍按 D16 显示在表单里。
3. 冲突后点“刷新”不关闭弹窗，而是用最新数据重新填充表单，管理员可以直接继续编辑。

### 第 3 轮：下载页、首页、教程与收尾（2026-11-06，分支 worktree-download-round3-site，基线 f31eecf）

实现位置：
- 网站数据：src/lib/downloads.ts 改写为不读环境变量的纯函数（按白名单逐字段重建公开列表、按钮文案、推荐架构、版本徽章合并、首页状态、教程版本、iOS 清单生成与 XML 转义）；src/lib/download-source.ts（30 秒缓存、3 秒超时、失败时沿用上次结果最长 24 小时、请求合并、跳转查询与地址再校验、只转发可信头里的一个 IP）；src/lib/server/downloads.ts（每个网站进程一个缓存）；src/lib/download-catalog.ts 增加网站自带的白名单常量。
- 路由：src/app/download/get/[platform]/[arch]/[format]/route.ts（302 跳转）与 …/manifest.plist/route.ts（iOS 清单）；next.config.ts 为 /download/get/ 覆盖 Referrer-Policy 为 no-referrer。
- 页面：download-view.tsx 按 Windows、macOS、Android、iPhone、Linux 排列，多架构按钮、每个文件的 SHA-256、iOS 三种格式说明、Linux“即将推出”、读取失败提示；“安装、升级与卸载”新增 Windows、macOS 两张；删除“开源许可与源代码”卡片。首页四张卡改为 Windows、macOS、Android、iOS，链接到各自锚点，读取失败时显示“前往下载页”。教程的 Windows、macOS 改用 windows、macos 下载键。
- 去掉环境变量：.env.example 删除 ANDROID_*、IOS_* 段落；网站代码不再读取这些变量。
- 打包：scripts/download_entry.py；package-native-android/windows/macos/ios.py 和 ios/resign-ipa.py 写出 download-entry.txt，不再输出 .env 行；release_scrub 把它列为只能在 private 目录的文件。
- 夹具：payment_acceptance_test.go 启动时录入 Android arm64-v8a（取代原来的 .env），/fixture/downloads?set=empty|base|multi；test-payment-web.py 去掉 ANDROID_* 变量，给网站进程加 E2E_ISOLATED 与 DOWNLOADS_CACHE_MS=1000，并另起一个连不上后端的网站进程。
- 文档：新增 docs/client-downloads.md；android-release.md 第 4 节、ios-release.md、macos-release.md、windows-release.md、deployment.md、web-appearance.md 改为“录入后台”。

测试：
- scripts/downloads.test.mjs 改写（12 项）与 download-catalog.test.mjs（9 项，含网站白名单与后端快照逐项相等），合计 test:downloads 21 项。
- copy-guard.test.mjs 新增 D15 守卫：下载页的路由、组件和数据模块不得出现“开源、许可、源代码、GPL、Mihomo、内核”等字样。
- guide.test.mjs 更新为四个平台都有下载键。
- tests/download.spec.ts（新，5 个用例）；home.spec、guide.spec、admin-downloads.spec 随数据来源调整。
- scripts/test-download-entry.py（新，已加入 CI 的 Native Core job）。

| 编号 | 结果 | 证据 |
| --- | --- | --- |
| DL3-01 | PASS | download.spec：平台卡 id 与顺序为 windows、macos、android、ios、linux；“安装、升级与卸载”依次为 Windows、macOS、Android、iPhone |
| DL3-02 | PASS | 服务端返回的 HTML 和浏览器中的页面内容都不含 downloads.example.test 和 testflight.apple.com；所有下载按钮的 href 都以 /download/get/ 开头，或是只引用本站清单路由的 itms-services 链接 |
| DL3-03 | PASS | 站内路由 302 的 Location 等于登记地址，带 no-store、no-referrer、noindex；点击 Windows ARM64 按钮后浏览器请求了登记的文件地址（已拦截）；未上架的 macOS x64、不存在的 Linux、跨平台组合都跳回 /download#平台，未知平台跳回 /download；夹具改为 base 后，同一 TestFlight 和 Windows ARM64 路径跳回卡片 |
| DL3-04 | PASS | Windows x64 1.2.3 与 ARM64 1.2.4：x64 为实心按钮，没有卡片徽章，每个按钮旁各自显示版本；Android 两个架构同为 1.2.3：一个徽章，两段 SHA-256 分别标明架构和格式，签名证书指纹显示一次；未上架的 macOS x64 不显示 |
| DL3-05 | PASS | 清单路由返回 application/xml、no-store；浏览器 DOMParser 解析无错误，url（含 & 的地址已转义）、bundle-identifier、bundle-version、kind、title 都正确；其他路径的 manifest.plist 返回 404，下架后返回 404。TestFlight 按钮文案“通过 TestFlight 安装”，跳转到 testflight.apple.com。plist 转义另有单元测试 |
| DL3-06 | PASS | Linux 显示“Linux 版本即将推出，敬请期待。”；未发布的平台显示“暂未发布”；连不上后端的网站进程：/download 返回 200，五张卡都显示“下载信息暂时无法获取，请稍后刷新。”，跳转路径回到 /download#android，首页四张卡为 unknown 并显示“前往下载页”。后端恢复、缓存过期后自动恢复由单元测试覆盖（注入时钟） |
| DL3-07 | PASS | 页面上没有“开源许可与源代码”；test:copy 新增的守卫通过 |
| DL3-08 | PASS | home.spec：四张卡顺序为 Windows、macOS、Android、iOS，Android 的“立即下载”指向 /download#android；download.spec：multi 数据下四张都可下载，macOS 指向 /download#macos；Windows 上架前教程显示“联系客服获取”，上架后显示“前往下载”（/download#windows）和“当前版本 1.2.3” |
| DL3-09 | PASS | 在后台把 Android 版本改为 1.2.5，网站缓存（夹具为 1 秒）过期后下载页徽章、教程“当前版本”都变为 1.2.5 |
| DL3-10 | PASS（单元）/ 部署后实测 | 跳转查询与 /api/v1 代理用同一规则：只从 CLIENT_IP_HEADER 取最后一个 IP，以 X-Forwarded-For 转发，其他请求头（Cookie、Authorization）都不转发；单元测试覆盖。夹具没有可信反向代理，后端限流桶按浏览器 IP 计数需要在真实部署中确认（并入 U-69） |
| DL3-11 | PASS | 375px 浅色、深色：overflowing 检查为空；按钮文字不被截断（检查 label 的 scrollWidth）；SHA-256 代码块不超出屏幕宽度；按钮、提示和小标题的对比度都不低于 4.5:1。第一次截图发现长按钮文字在手机上被截断，已改为可换行并加入断言 |
| DL3-12 | PASS | 仓库中除交接文档、.env.example 的迁移说明、docs/client-downloads.md 和守卫测试外，没有 ANDROID_APK_*、IOS_IPA_*、IOS_MANIFEST_URL、*_SOURCE_URL、website-env.txt 的引用；test-payment-web.py 改为由夹具录入 |
| DL3-13 | PASS（脚本检查）/ 真实打包未运行 | test-download-entry.py：录入单字段与后台表单标签逐一对应，缺字段、大写或带冒号的哈希都被拒绝；五个脚本都写录入单、都不再输出 .env 行；release_scrub 把它视为只能在 private 目录的文件，放进公开目录会被报告；release_scrub --self-test 通过；五个脚本 py_compile 通过。本机没有运行真实打包（需要签名材料、Xcode 归档或 Windows 工具链），各平台的 release 套件由 CI 运行 |
| DL3-14 | PASS | docs/client-downloads.md 新增；android-release.md 第 4 节、ios-release.md 第 5–6 步、macos-release.md 第 4 节第 8 步、windows-release.md 第 5 节、deployment.md、web-appearance.md 已更新 |
| DL3-15 | PASS（本地）/ CI 见 PR | lint、tsc --noEmit、test:downloads（21）、test:home（3）、test:guide（14）、test:copy（4）、test:theme（5）、test:toast（7）通过；test:api-proxy 用本机 Chrome 运行 17 项全部通过（本机没有 Playwright 自带的 headless shell，不指定时 4 项因无法启动浏览器而失败，与改动无关）；next build 成功。完整支付夹具浏览器套件 120 passed（10.5 分钟，/tmp/dl3-web-full，含新的 download.spec 和更新后的 home、guide、admin-downloads、theme、ui-copy）。节点目录套件（test-node-catalog.py）的浏览器部分 10 passed；其后的 TestCatalogBackendMihomoAcceptance 在本地 worktree 因没有 Mihomo 子模块（native/third_party/Clash.Meta）无法编译，记为本地 BLOCKED，由 CI 运行 |

与规划的差异：
1. iPhone 安装链接中的站点地址由浏览器根据当前页面生成，服务端不需要新增站点地址配置，也不信任 Host 头；服务端渲染时按钮先指向 #ios，页面加载完成后换成 itms-services 链接。
2. 测试用的短缓存只在网站进程带有夹具标记 E2E_ISOLATED=private-postgres 时读取 DOWNLOADS_CACHE_MS（500–30000 毫秒）；网站没有“生产环境”的概念，所以用夹具标记代替。
3. tsconfig 打开 allowImportingTsExtensions，让 node --test 能直接加载互相引用的 .ts 模块。
4. resign-ipa.py 不再打印 IOS_* 行，改为在签名文件旁写录入单；--ipa-url 仍可写 manifest.plist，供网站以外的安装方式使用。
5. iOS 未签名包也写录入单，并注明“未签名，不能上架”，以签名后的录入单为准。
