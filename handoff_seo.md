# AsterLink VPN：网站搜索引擎优化（SEO）技术规划交接书

> 文件：handoff_seo.md  
> 编写日期：2026-11-06  
> 代码基线：项目 /Users/stevenlee/Desktop/vpn，main，HEAD 94f253b（download 第 3 轮合并后）。  
> 生产域名：网站 https://www.asterlink.vip；后端 API https://api.asterlink.vip（四个客户端直连它，所以它在公网可以解析，但不对搜索引擎开放，也不出现在网站页面里）。  
> 文档性质：实施规划与验收约定。本文不代表任何功能已开发或已测试，全文不含代码示例。

## 1. 目标、范围与轮次总览

现状（已逐项核实，见第 2 节）：

- 全站禁止收录：根布局 frontend/src/app/layout.tsx 的 metadata 写死了 robots: index false、follow false。这是 handoff_ui.md D11 有意保留的，当时说“上线推广时再单独决定”。本规划就是这次决定。
- 没有 robots.txt、sitemap.xml、canonical、Open Graph 分享卡片、结构化数据（JSON-LD）和搜索引擎验证入口，也没有“站点公开地址”的配置项（metadataBase 无从设置）。
- 首页“套餐价格”预览和 /plans 的价格都是浏览器端请求 /api/v1/plans 后才渲染，爬虫拿到的首屏 HTML 里没有任何价格。
- 使用教程四个平台共用一个地址 /guide?platform=，没有参数时服务端按空 UA 渲染 Windows，浏览器端再按设备切换。四份教程在搜索引擎眼里是同一页。
- 除 /reset-password 以外，各页只有一个短标题（如“登录”“使用教程”），没有各自的描述（description）。首页标题是口号“连接，自有边界”，不含任何可搜索的业务词。
- 没有服务条款和隐私政策页。这两页是商业网站的基本信任信号，也是很多支付和广告渠道审核的必备项。

目标：

1. 让 Google、Bing（覆盖 Yahoo、DuckDuckGo 等使用 Bing 索引的引擎）顺利发现、抓取、渲染并收录网站的公开页面，按品牌词（AsterLink）能稳定搜到官网首页，业务词（住宅原生 IP、专线等）有可竞争的页面承接。
2. 达到商业网站的通行标准：每页标题和描述唯一且准确；规范地址唯一；私有页面不进索引；分享到社交平台有卡片；结构化数据真实、可校验；核心网页指标（Core Web Vitals）达标；信任页面齐全。
3. 不改变现有的业务逻辑、计费、鉴权和文案守卫规则。不引入第三方统计脚本。生产以外的环境（本地、CI、预发）永远不会被收录。
4. api.asterlink.vip 永不被收录，网站产物和页面里也不出现这个主机名。

采用 **4 轮**实施。每轮一个 worktree 分支、一个 PR，分支命名为 worktree-seo-roundN-*。每轮记录追加到本文第 14 节。

| 轮次 | 可验收结果 | 依赖 |
| --- | --- | --- |
| 第 1 轮 | 收录基础设施：站点地址与收录开关配置（运行时读取）、各路由的收录策略表、根布局运行时元数据（metadataBase、robots、canonical）、动态 robots.txt 与 sitemap.xml、/api 与下载跳转的 X-Robots-Tag、非规范主机自动 noindex、后端 API 域名的 robots.txt 与全局 noindex 头、守卫测试 | 无 |
| 第 2 轮 | 页面元数据与可抓取内容：每页唯一的标题和描述、Open Graph / Twitter 卡片与 1200×630 分享图、套餐价格服务端渲染（首页预览和 /plans）、标题层级审查、页脚与页面间的内部链接 | 第 1 轮 |
| 第 3 轮 | 教程分平台地址（/guide 总览 + /guide/{平台}，旧参数地址 308 跳转）、结构化数据（Organization、WebSite、BreadcrumbList、SoftwareApplication、Product/Offer、FAQPage）、服务条款与隐私政策页 | 第 2 轮 |
| 第 4 轮 | 性能与上线：Lighthouse 基线与预算、按测量结果做的必要优化、Nginx 规范跳转（http、裸域到 www）与 HSTS 文档、搜索引擎验证配置、运维文档 docs/seo-operations.md、全量回归 | 第 3 轮 |

本期不做：

- 不做多语言版本和 hreflang。全站只有简体中文，html lang 保持 zh-CN。
- 不做博客或文章系统，不批量生成“城市 + 关键词”之类的落地页（门页，doorway pages）。这类页面违反 Google 的垃圾内容政策，会拖累整站。
- 不接入 Google Analytics、百度统计等第三方统计或广告像素。home.spec 现有断言“整页滚动过程中没有任何非本站请求”继续有效。效果评估改用 Search Console、Bing Webmaster Tools 和 Nginx 访问日志，见第 12 节。
- 不做站内搜索，所以 WebSite 结构化数据里不声明 SearchAction。
- 不承诺搜索排名。排名取决于内容、外部链接、竞争和时间，代码只能保证“技术上可以被收录、理解，而且体验达标”。验收只看技术就绪程度，不看名次。
- 不做百度的专门适配（熊掌号、MIP 等）。百度站长平台的提交作为可选的运营动作写在第 12 节，不作为验收项。

## 2. 已核实的代码基础与必须衔接的位置

### 2.1 网站（frontend）

| 位置 | 当前事实 | 对本次的影响 |
| --- | --- | --- |
| src/app/layout.tsx | 静态 metadata：标题模板“%s · AsterLink”，默认标题“AsterLink · 连接，自有边界”，一句描述，robots noindex/nofollow；html lang="zh-CN"；viewport 的 themeColor 跟随系统 | 改为运行时生成的元数据（第 1 轮）；默认标题和描述在第 2 轮重写 |
| 各 page.tsx | 只有 title，没有 description。dashboard、download、guide、nodes、reset-password 和首页是 force-dynamic，其余（login、register、plans、orders、account、referral、tickets、pay/result 等）在构建时预渲染 | 预渲染页面的 head 会在构建时定型，见 D3；每页补 description，私有页补 noindex |
| next.config.ts headers() | 全站安全头；/reset-password 加 noindex 和 no-referrer；/download/get/* 加 noindex 和 no-referrer | 在这里追加 /api/* 的 noindex 头 |
| src/components/site-shell.tsx | 客户端组件。认证页只包 main；首页自带头尾；workspace 路径用精确匹配（含 /plans、/download、/guide），带“用户中心”侧栏 | /guide/{平台} 要改为前缀匹配，侧栏高亮也一样 |
| src/app/page.tsx | 首页服务端组件，force-dynamic；功能、步骤、平台卡都在服务端渲染；PlanPreview（客户端请求 /plans）和 HomeFaq（客户端组件，按 /meta 拼装答案）在客户端渲染 | 价格改为服务端取数（第 2 轮）；FAQ 文本要能在服务端以相同规则生成，供结构化数据使用（第 3 轮） |
| src/components/home/home-faq.tsx | 答案依赖 meta（付款方式、是否支持升级、工单）以及 supportEmail、chat；折叠面板用 hidden 属性，文本在 DOM 中 | FAQ 文案抽成纯函数，服务端和客户端共用同一份 |
| src/components/plans-view.tsx | 客户端组件，挂载后请求 /plans 和 /meta；h1 为页面标题 | 接受服务端传入的初始套餐数据，挂载后照常刷新 |
| src/components/home/plan-preview.tsx | 客户端请求 /plans，失败或为空时整块隐藏 | 同上，初始数据由服务端给出；失败时的隐藏行为不变 |
| src/app/guide/page.tsx、src/components/guide-view.tsx、src/lib/guides.ts | 单一路由 + ?platform=；choosePlatform 优先用参数，否则 detectPlatform(UA)，服务端 UA 为空时得到 windows；切换平台用 router.replace 写参数；教程数据是 1047 行的纯数据，内容质量高 | 第 3 轮改为 /guide 总览 + /guide/{平台} |
| src/components/download-view.tsx:213、241 | 链接到 /guide?platform=… | 第 3 轮改为新地址 |
| src/lib/server/downloads.ts、src/lib/download-source.ts | 服务端读取下载列表，30 秒缓存，失败时回退到上次的成功结果 | 第 2 轮的套餐读取照此实现；/download 的动态描述和 SoftwareApplication 用它的数据，绝不使用真实地址 |
| src/app/manifest.ts、icon.svg、favicon.ico、apple-icon.png、public/icons | 图标齐全，由 scripts/brand-icons.mjs 从 brand/mark.svg 生成 | 分享图由同一个脚本生成（第 2 轮） |
| src/lib/site.ts | SITE_ICP_RECORD、SITE_SUPPORT_EMAIL：服务端环境变量，按请求读取，格式非法时隐藏 | 新的站点地址和收录开关沿用这个模式（D1） |
| scripts/copy-guard.test.mjs | 用户页禁用开发词汇和广告法极限用语（最快、第一、零日志、不限速、翻墙、科学上网……），排除 app/api、lib/server 等 | 新增的标题、描述和结构化数据文案都在扫描范围内；结构化数据构造函数不能放进被排除的目录来躲避扫描 |
| src/app/api/v1/[...path]/route.ts | 浏览器访问 /api/v1 的反向代理，只转发白名单响应头 | 后端加的 X-Robots-Tag 不会穿过代理，网站要自己加（D10） |
| src/app/not-found.tsx | 自定义 404 页，标题“页面不存在” | 确认返回 404 状态码，并带 noindex |
| 首页 FAQ、功能卡文案 | 遵守 handoff_ui.md D5（中性表述，“VPN”只在技术上不可避免时出现）和 D12（无法证明的宣传不写） | 关键词策略受这两条约束（D6） |
| CI ci.yml “Web validation” | lint、build、typecheck，再加 test:* 一组 node --test；浏览器端到端由 scripts/test-payment-web.py 运行，那个任务有 25 分钟上限，余量约 5 分钟 | 新增的 node 测试放进 Web validation；浏览器端用例只加轻量的 HTTP 级检查，见第 10 节 |

### 2.2 后端（backend）

| 位置 | 当前事实 | 对本次的影响 |
| --- | --- | --- |
| internal/api/server.go | 根路由上有 /healthz、/readyz、支付回调；/api/v1 下的公开接口有 /meta、/plans、/downloads；NoRoute 返回 JSON 404 | 新增根路由 GET /robots.txt（全站禁止抓取），再加一个给所有响应补 X-Robots-Tag: noindex, nofollow 的中间件 |
| internal/api/shop.go plans() | 公开套餐列表：名称、描述、原价、月付和年付档位、流量、设备数、是否专线、是否推荐 | 网站服务端渲染价格和 Product/Offer 都用它，不新增接口 |
| security_review_test.go TestSecurityRouteAccessMatrix | 每条路由要么在 public 白名单里，要么匿名访问返回 401 | 实施时核实 /robots.txt 是否需要加入白名单 |
| config 中的 WEBSITE_BASE_URL | 后端用它生成邮件链接和支付返回地址，生产上应为 https://www.asterlink.vip | 部署文档要求它与网站的 SITE_URL 一致（第 4 轮） |

### 2.3 部署（docs/deployment.md）

- Nginx：前端 server 块把所有路径交给 Next.js（127.0.0.1:3000）；API 域名交给 Go（127.0.0.1:8080）。
- 文档里目前没有 http 到 https、裸域到 www 的跳转说明，也没有 HSTS、gzip/brotli 的说明。
- 使用教程截图有一个可选的长缓存 location。
- 历史域名：memory 里记有旧演示域名 demo.hyshentou.cn 和 test.hyshentou.cn。只要它们仍然解析到同一个网站，就会形成重复内容，第 4 轮的文档要处理。

## 3. 关键决策（默认值，用户可在第 1 轮开工前推翻）

| 编号 | 决策 | 理由 |
| --- | --- | --- |
| D1 | 新增两个**服务端**环境变量，按请求读取，修改后重启 Next.js 即可，不需要重新构建：SITE_URL（站点规范来源，只允许“https://主机名”，不能带路径、查询参数或结尾斜杠；本地允许 http://localhost 一类的回环地址）和 SITE_INDEXING（只有值为 on 时才允许收录）。只有 SITE_INDEXING=on 且 SITE_URL 合法时，可收录页面才输出 index；其他任何情况，全站都按“禁止收录”输出：页面 noindex、robots.txt 禁止全部抓取、sitemap.xml 返回空列表。非法值在服务端日志告警一次，不会让页面出错 | 默认关闭最安全：本地、CI、预发和以后克隆出来的环境，忘了配置也不会被收录；生产只需在 .env 里加两行。不用 NEXT_PUBLIC_*，因为那是构建时变量，同一份构建换环境就会出错 |
| D2 | 规范来源固定为 https://www.asterlink.vip。所有 canonical、og:url、sitemap 里的地址和结构化数据里的 URL 都用 SITE_URL 拼成绝对地址，**绝不**从请求的 Host 头推导。http 和裸域 asterlink.vip 由 Nginx 301 到 https://www（第 4 轮） | 一个站只能有一个规范来源；Host 头可以伪造 |
| D3 | 所有带收录标记或 canonical 的页面，元数据必须在运行时生成。如果某个页面因此不能再构建时预渲染，就让它变成动态渲染（这些页面本来就以客户端渲染为主，代价很小）。验收时用同一份构建分别以“开”和“关”两种环境启动，比较页面 head（S1-03） | 预渲染页面的 head 在构建时定型，会让 D1 的“重启生效”失效。这是本规划最容易踩的坑 |
| D4 | 收录策略按路由逐个声明，集中在一张策略表里，由纯函数模块提供，并配单元测试（第 4 节）。没有在表里声明的路由一律 noindex，这样以后新增页面默认不会被收录 | 白名单比黑名单安全；一张表就能看清全站策略 |
| D5 | 使用教程拆成五个地址：/guide 为总览页，可收录，列出四个平台的入口和“开始之前”；/guide/windows、/guide/macos、/guide/android、/guide/ios 为四个独立页面，各自可收录，各有标题和描述。旧的 /guide?platform=x 用 308 永久跳转到 /guide/x；参数值无效时跳到 /guide。总览页按设备高亮推荐的平台（只在浏览器端判断，服务端输出的内容对所有访客相同），并给出“继续阅读 X 教程”主按钮。/guide/未知值返回 404 | 四份教程各有自己的搜索需求（如“AsterLink Windows 安装”），合用一个地址只会被当成一页；服务端不能按 UA 输出不同内容（那属于伪装，cloaking）；旧链接继续可用 |
| D6 | 关键词与文案：主题词为品牌词 AsterLink，加上“住宅原生 IP”“原生 IP”“专线”“网络加速”“多设备”“Windows / macOS / Android / iPhone 客户端”，与首页现有的卖点一致。遵守 handoff_ui.md D5 和 D12 以及 copy-guard：标题和描述不出现“VPN”“翻墙”“科学上网”和任何极限用语；移动端教程正文里因系统弹窗而不可避免的“VPN”保持原样。不使用 meta keywords（Google 和 Bing 都忽略它，只会暴露关键词策略） | 与已上线的品牌语气一致；违规宣传在国内有法律风险，在搜索引擎那里也会被降权 |
| D7 | 标题和描述的规则：每个可收录页面的标题和描述全站唯一；标题不超过 30 个汉字（含品牌后缀），描述 60 到 120 个汉字；首页标题不用模板，用完整的“AsterLink · 住宅原生 IP 与专线稳定连接”一类写法；内页用“页面主题 · AsterLink”。所有文案由单元测试检查长度、唯一性和禁用词 | Google 在中文搜索结果里大约显示 30 个汉字的标题、80 到 120 个汉字的描述；重复的标题和描述会被 Search Console 报警 |
| D8 | 关键内容在服务端渲染：首页套餐预览和 /plans 的套餐卡由服务端读取公开的 /plans（进程内缓存 60 秒，超时 3 秒，失败时用上一次成功的结果，最长 24 小时；都没有时保持现在的行为，首页隐藏预览，/plans 显示加载态），作为初始数据交给客户端组件。登录后与账户相关的部分（续费、升级、当前套餐）仍在浏览器端请求 | 价格是商业页面最重要的内容，必须出现在首屏 HTML 里；沿用 download-source 已验证过的缓存和回退模式 |
| D9 | 结构化数据只写页面上真实可见、系统能兑现的内容：首页放 Organization 和 WebSite；内页放 BreadcrumbList；/download 每个已上架平台放一个 SoftwareApplication（名称、操作系统、类别、版本，**不含下载地址、价格和评分**）；/plans 每个在售套餐放一个 Product，内含月付和年付的 Offer（人民币价格、在售状态），不写评分和评论；首页放 FAQPage，文本与可见答案逐字一致。不使用 HowTo（Google 已于 2023 年停用这类富结果），不伪造 aggregateRating 或 review | 虚假的结构化数据会被人工处罚。说明：FAQ 富结果目前只对政府和医疗类网站展示，SoftwareApplication 富结果要求有评分，所以这两类主要帮助搜索引擎理解页面，不一定出现富结果；Product 和 BreadcrumbList 有机会出现富结果 |
| D10 | 不应被收录的地址：/api/*（Next 代理和 runtime-config）统一加 X-Robots-Tag: noindex, nofollow；robots.txt 只 Disallow /api/ 和 /download/get/，下载按钮加 rel="nofollow"。用户中心页（/dashboard、/nodes、/orders、/referral、/tickets、/account、/pay/result）、/forgot-password、/reset-password、/admin/** 和 404 页在页面元数据里写 noindex，**不**写进 robots.txt：被 Disallow 的页面爬虫看不到 noindex，反而可能以“无摘要的地址”出现在结果里，而且 robots.txt 会公开 /admin 路径 | 这是 Google 文档推荐的“用 noindex 而不是 Disallow 排除页面”的做法；/download/get 会跳到大文件，不希望爬虫去下载 |
| D11 | /login、/register 可以收录（品牌词“AsterLink 登录”是常见的导航型搜索），但不放进 sitemap。/register?ref=… 的 canonical 指向 /register；/plans?cycle=、?plan=、?pay= 的 canonical 指向 /plans。所有 canonical 都不带查询参数和锚点 | 推荐码和购买参数会制造无数个重复地址 |
| D12 | 非规范主机：请求的 Host（经可信代理传来）与 SITE_URL 的主机名不一致时（比如直接用 IP 访问、旧域名、预发域名），页面一律 noindex，canonical 仍然指向 SITE_URL。这里读取 Host 只用来**收紧**策略，伪造 Host 只会让伪造者自己拿到 noindex | Nginx 跳转之外的第二道防线，防止重复内容 |
| D13 | sitemap.xml 只列可收录的规范地址：/、/plans、/download、/guide、/guide/{四个平台}、/terms、/privacy。lastmod 只在能给出真实时间时才写：/download 取已上架条目中最新的 released_at，其余页面不写。不写 changefreq 和 priority（Google 忽略这两项）。不做图片 sitemap（教程截图已有 alt 文字，同源可抓取） | 不真实的 lastmod 会让搜索引擎不再信任它 |
| D14 | 分享卡片：og:title、og:description、og:url、og:type（首页 website，其他页也是 website）、og:site_name、og:locale zh_CN、og:image（1200×630 PNG，由 scripts/brand-icons.mjs 从 brand/mark.svg 加固定的中文标语合成，带内容哈希或固定文件名加版本参数），twitter:card 为 summary_large_image。分享图是提交到仓库的静态文件，不用运行时生成（运行时生成中文需要嵌入大字体文件） | 微信、Telegram、X、Slack 等的预览都依赖这些标签；静态文件稳定、快、可审查 |
| D15 | 搜索引擎验证优先用 DNS TXT 记录（Google Search Console 的“网域”资源、Bing 可从 Google 导入），不需要改代码。同时提供三个可选的服务端环境变量 SITE_VERIFICATION_GOOGLE、SITE_VERIFICATION_BING、SITE_VERIFICATION_BAIDU，设置后在首页 head 输出对应的验证 meta；值只允许字母、数字、下划线和连字符，长度不超过 100，非法时忽略并告警 | DNS 验证覆盖整个域名，也不依赖页面；meta 方式作为备用 |
| D16 | 后端 API 域名：Go 的根路由提供 /robots.txt（禁止全部抓取），所有响应带 X-Robots-Tag: noindex, nofollow。网站的构建产物、页面 HTML 和 sitemap 中不允许出现 api.asterlink.vip 或 API_INTERNAL_URL 的值，由守卫测试检查 | 客户端要直连这个域名，它没法藏起来，但可以明确告诉搜索引擎不要收录；网站本来就从不暴露内部地址 |
| D17 | 服务条款（/terms）和隐私政策（/privacy）两个新页面：正文由实施者根据代码里已经实现的事实起草，内容包括收集哪些数据（邮箱、登录记录与 IP、设备标识、流量统计、订单）、保存多久（例如工单 30 天自动删除）、账户注销、付款与不退款规则（与 FAQ 一致）、客户端计费的说明、联系方式。**用户在 PR 中审阅并确认文字后才能合并**。页脚加这两个链接；注册按钮下方加一行“注册即表示你同意《服务条款》和《隐私政策》”（只是链接，不加勾选框） | 商业网站的基本信任信号，也是支付和推广渠道审核时常见的要求；法律文字必须由运营者本人确认，实施者不能替用户承诺 |
| D18 | 性能预算（Lighthouse 移动端实验室数据，在本机生产构建上测）：/、/plans、/download、/guide/windows 四页的 LCP ≤ 2.5 秒、CLS ≤ 0.1、TBT ≤ 200 毫秒；Lighthouse SEO 分数 100、无障碍分数 ≥ 90。先测基线再优化，只改超出预算的项；不为跑分改变视觉设计 | 这是 Google 的“良好”阈值；没有测量依据的优化容易做无用功 |
| D19 | 不加第三方脚本。Crisp 仍然只在已登录的用户页加载（handoff_ui.md D9），对公开页面的抓取和性能没有影响 | 保持隐私承诺和现有测试断言 |

## 4. 收录策略表（第 1 轮实现，第 3 轮补齐新页面）

| 路由 | 收录 | 进 sitemap | canonical | 备注 |
| --- | --- | --- | --- | --- |
| / | index, follow | 是 | / | 首页 |
| /plans | index, follow | 是 | /plans（去掉全部参数） | 价格在服务端渲染（D8） |
| /download | index, follow | 是 | /download | 下载按钮 rel=nofollow |
| /guide | index, follow | 是 | /guide | 第 3 轮改为总览页 |
| /guide/{windows,macos,android,ios} | index, follow | 是 | 各自地址 | 第 3 轮新增 |
| /terms、/privacy | index, follow | 是 | 各自地址 | 第 3 轮新增，用户确认文字后上线 |
| /login、/register | index, follow | 否 | 各自地址（去掉 ref、next 等参数） | 导航型品牌搜索 |
| /forgot-password、/reset-password | noindex, nofollow | 否 | 无 | reset 页现有的 noindex 头保留 |
| /dashboard、/nodes、/orders、/referral、/tickets、/account、/pay/result | noindex, follow | 否 | 无 | 需要登录的页面 |
| /admin/** | noindex, nofollow | 否 | 无 | 不写进 robots.txt |
| /download/get/** | X-Robots-Tag noindex（现有） | 否 | 无 | robots.txt Disallow |
| /api/** | X-Robots-Tag noindex, nofollow | 否 | 无 | robots.txt Disallow |
| 404 页 | noindex | 否 | 无 | 状态码必须是 404 |

robots.txt 内容（收录开启时）：允许全部；Disallow /api/ 和 /download/get/；最后一行声明 Sitemap 为 SITE_URL 加 /sitemap.xml。收录关闭时：Disallow 全部，不声明 Sitemap。两种情况都必须是动态响应（D3），Content-Type 为 text/plain，并带短缓存（例如 1 小时），以便开关切换后很快生效。

## 5. 页面元数据与内容（第 2 轮）

### 5.1 标题与描述（定稿以实施时 copy-guard 和长度测试通过为准）

| 页面 | 标题方向 | 描述方向 |
| --- | --- | --- |
| / | AsterLink · 住宅原生 IP 与专线稳定连接 | 住宅宽带原生 IP 出口与专线线路，一个账户在手机和电脑上同时使用；线路倍率公开，月付年付可选，微信、支付宝付款即买即用 |
| /plans | 套餐价格与订阅 · AsterLink | 月付与年付套餐的价格、流量、设备数和专线权益一览；年付每 30 天刷新流量；在线付款后立即开通 |
| /download | 下载客户端 · AsterLink | **动态**：只列出当前已上架的平台（来自下载列表）；一个平台都没有上架时，使用不提具体平台的通用句 |
| /guide | 使用教程 · AsterLink | 从注册、购买到在各设备上安装、登录、连接的完整图文步骤 |
| /guide/{平台} | Windows 使用教程：安装、登录与连接 · AsterLink（其余平台同理，iOS 写作 iPhone） | 该平台的适用设备、安装、首次授权、登录、选线路和常见问题，各平台分别写 |
| /login、/register | 登录 · AsterLink、注册账户 · AsterLink | 各一句 |
| /terms、/privacy | 服务条款 · AsterLink、隐私政策 · AsterLink | 各一句 |

- 文案集中放在一个纯数据模块里（不放在 lib/server 下，copy-guard 才能扫描到），由单元测试检查 D7 的长度、唯一性和禁用词。
- 私有页面保留现有的短标题，不写描述。

### 5.2 分享卡片

- 根布局提供站点级的默认 og 标签和分享图，各页覆盖 og:title、og:description、og:url。
- 分享图：1200×630 PNG，体积目标在 150 KB 以内；浅色背景、品牌标志、产品名和一句卖点（与首页 hero 一致）。生成步骤并入 brand-icons 脚本，CI 现有的品牌资产检查扩展为同时检查分享图的尺寸和格式。

### 5.3 服务端渲染的价格

- 新增服务端套餐读取模块，结构与下载读取模块一致（可注入、带缓存、能回退），只拷贝已知字段。
- 首页 PlanPreview 和 PlansView 接收初始数据：有初始数据时首屏直接渲染套餐卡，不出现骨架屏；挂载后照常请求，结果不同时以新结果为准，不闪烁。
- 现有行为保持：/plans 返回 503 或空列表时，首页预览整块隐藏（home.spec 已有断言）。

### 5.4 标题层级与内部链接

- 每个可收录页面恰好一个 h1，h2/h3 不跳级（首页、/plans、/download、/guide 已基本符合，实施时逐页核对并修正）。
- 首页页脚“产品”栏加“使用教程”，第 3 轮再加“服务条款”“隐私政策”；首页“三步开始使用”的第三步链接到 /guide。
- /download 的每张平台卡已有“查看使用教程”，第 3 轮指向新的分平台地址；各教程页的“下载安装”步骤已链接到 /download#{平台}。
- 公开页面之间的链接都用普通的 a 链接（Next 的 Link 也会渲染成 a），不能用按钮加 JavaScript 跳转，否则爬虫发现不了。

## 6. 教程分平台与结构化数据（第 3 轮）

### 6.1 教程路由

- /guide/{平台}：服务端根据路径确定平台，整页内容在首屏 HTML 里，与现在 ?platform= 的效果相同。平台切换控件改成四个普通链接，导航到新地址（保留现有的滚动行为）。
- /guide：总览页，内容为页首标题、“开始之前”（guides.ts 的 beforeYouStart）、四张平台卡（适用设备一行、版本徽章（如有）、进入教程的链接）。按设备推荐只在浏览器端加一个高亮和主按钮，服务端渲染的内容对所有访客相同。
- 旧地址的兼容：/guide?platform=有效值 → 308 到 /guide/{值}；无效值 → 308 到 /guide。跳转放在路由层，不依赖客户端脚本。
- 站内引用全部改为新地址：download-view、guides.ts 的链接白名单、tests、docs/user-guide.md。客户端（apps 和 native）里没有引用教程地址（已用 grep 核实），不受影响。
- site-shell：workspace 判断和侧栏“使用教程”的高亮都改为“/guide 或以 /guide/ 开头”。

### 6.2 结构化数据

- 所有 JSON-LD 由纯函数生成，在服务端组件里输出成 application/ld+json 脚本。**输出前必须转义 “<” 等字符**：套餐名称和描述是管理员可以编辑的自由文本，不转义就有通过“</script>”注入页面的风险。
- Organization：name、url（SITE_URL）、logo（512 像素图标的绝对地址）；配置了 SITE_SUPPORT_EMAIL 时加 contactPoint（客服、邮箱）。
- WebSite：name、url、inLanguage zh-CN。
- BreadcrumbList：/plans、/download、/guide、/guide/{平台}、/terms、/privacy，层级为“首页 › 页面”或“首页 › 使用教程 › Windows”。页面上没有可见的面包屑，结构化数据只表达站点层级；如果 Rich Results Test 对此报警，再在页首加一个轻量的可见面包屑（实施时决定，记入第 14 节）。
- SoftwareApplication（/download）：每个已上架的平台一个，包括 name（AsterLink for Windows 等）、operatingSystem、applicationCategory（BusinessApplication 或 UtilitiesApplication，实施时二选一并说明理由）、softwareVersion（推荐架构的版本）。不写 downloadUrl（遵守 handoff_download.md D4，页面数据中不出现真实地址），不写 offers 和评分。
- Product + Offer（/plans）：每个在售套餐一个 Product，包括 name、description（为空时用流量、设备数拼一句）、brand；offers 列出月付和年付两档，含 price（元，保留两位小数，与页面显示一致）、priceCurrency CNY、availability InStock、url（/plans）。套餐读取失败、页面没有渲染价格时，不输出 Product。
- FAQPage（首页）：问答文本由 home-faq 抽出的纯函数在服务端用同一套参数（meta、supportEmail、chat）生成，与可见文本逐字一致；所以服务端也要读 /meta（同样带缓存和回退，失败时用与客户端失败时一致的保守文案）。

### 6.3 服务条款与隐私政策

- 两页都是静态服务端页面，使用公开页面的外观（不带用户中心侧栏，参照 404 页或认证页的布局，实施时决定），文本通过 copy-guard。
- 起草依据必须是代码和已合并的交接书里的事实，逐条在 PR 描述中列出出处（例如“工单 30 天自动删除：handoff_ticket.md”）。
- 页首标注“生效日期”和“最后更新日期”，以后修改时更新。

## 7. 性能与部署（第 4 轮）

### 7.1 性能

- 新增本地审计脚本（放在 frontend/scripts 下）：对生产构建依次运行 Lighthouse 移动端审计，输出 JSON 和摘要，并与 D18 的预算比较。Lighthouse 作为开发依赖或用 npx 临时运行，实施时选不污染运行时依赖的方式。
- 先把基线写入第 14 节，再根据超标项处理。可能的方向（只在测量证明有必要时做）：
  - 全量引入的 @mantine/core/styles.css 是否拖慢首屏；
  - 首页首屏的客户端组件是否带来过多 JavaScript；
  - 教程截图是否都带宽高、首屏以外懒加载（现在 guide-view 用 unoptimized 的 Image，已带宽高）；
  - 字体：现在使用系统字体，不引入网络字体。
- 是否进 CI：不放进现有浏览器任务（25 分钟上限，余量约 5 分钟）。如果要做 CI 门禁，就另开一个独立任务，并且只用“SEO 分数 100、CLS ≤ 0.1”这类稳定的指标做门禁，耗时类指标只作记录（CI 机器的性能波动太大）。

### 7.2 部署（写入 docs/deployment.md 和新文档 docs/seo-operations.md）

- Nginx：80 端口的全部主机名 301 到 https://www.asterlink.vip，保留路径和查询参数；443 上的 asterlink.vip 同样 301 到 www；旧演示域名如果不再使用就 301 到 www，否则在那边设 SITE_INDEXING=off（D12 已兜底）。
- HSTS：www 与 api 都加 Strict-Transport-Security，max-age 一年；默认不加 preload（preload 很难撤销，由用户决定）。
- 压缩：确认 Nginx 对 HTML、CSS、JS、JSON、XML、SVG 开启 gzip（有 brotli 模块的话也开）；_next/static 已由 Next.js 带上不可变缓存头。
- 环境变量：生产的 frontend/.env 加 SITE_URL=https://www.asterlink.vip 和 SITE_INDEXING=on；backend 的 WEBSITE_BASE_URL 和 ALLOWED_ORIGINS 与之一致；frontend/.env.example 加入这两项和三个可选的验证变量，并写好说明。
- 上线顺序：先部署后端（API 域名的 robots.txt 和 noindex 头），再部署网站并保持 SITE_INDEXING=off，用 curl 核对页面 head、robots.txt 和 sitemap；确认无误后改为 on 并重启，再到 Search Console 提交 sitemap。
- 回滚：把 SITE_INDEXING 改回 off 并重启，即可整站停止收录（已收录的页面会在下次抓取后逐步移出索引）；如需紧急删除某个地址，用 Search Console 的“删除”工具。

## 8. 兼容、安全与风险

- 现有测试的影响：home.spec、guide.spec、download.spec、ui-copy.spec、theme.spec 中凡是引用 /guide?platform= 或断言 head 的用例都要在对应轮次同步修改；home.spec 的“无第三方请求”断言必须继续通过。
- 动态化（D3）的影响：/login、/register、/plans 等页面从预渲染改为动态渲染后，每次请求多一次服务端渲染。用户量很小时可以忽略，第 4 轮的性能测量会覆盖这一点。
- 结构化数据注入：见 6.2，转义是硬性要求，单元测试要覆盖“套餐名称含 </script> 和引号”的情况。
- 内容一致性：结构化数据、description 和可见内容不能互相矛盾（例如 /download 的描述说有 Windows，页面上却显示“暂未发布”）。守卫测试在“部分平台上架”和“全部未上架”两种夹具下分别比对。
- 下载地址：继续遵守 handoff_download.md D4，sitemap、结构化数据和描述中都不出现真实地址。
- 收录开关误开：只有生产的 .env 写 on；CI 和 test-payment-web.py 的默认环境不设这个变量，即处于关闭状态；只有专门的用例会临时开启。
- 搜索引擎处罚风险：不做隐藏文字、不做按 UA 区别输出、不做门页、不伪造评分、不买链接。这些写进 docs/seo-operations.md 的“禁止事项”。
- 法律与合规：服务条款和隐私政策的文字必须由用户确认（D17）；ICP 备案号的展示逻辑不变。

## 9. 四轮实施与逐轮验收

验收编号：S{轮次}-{序号}。结果按 PASS / FAIL / BLOCKED 如实记录，未实际执行的一律不写 PASS。

### 第 1 轮：收录基础设施

实施内容：

- 新增站点配置纯函数模块：解析 SITE_URL、SITE_INDEXING 和三个验证变量，包括格式校验和一次性告警；提供“当前请求是否允许收录”的判断（开关、地址合法、Host 与规范主机一致三项都满足才允许）。
- 新增收录策略表模块（第 4 节）：输入路由，输出 robots 指令、是否进 sitemap、canonical 路径。
- 根布局改为运行时生成元数据：metadataBase 取 SITE_URL；默认 robots 为 noindex（D4）；可收录页面按策略表输出 index 和 canonical。
- 私有页面补 noindex（D10、D11）；404 页确认带 noindex 并返回 404 状态码。
- 新增动态的 robots.txt 和 sitemap.xml 路由（第 4 节、D13）。
- next.config 给 /api/:path* 加 X-Robots-Tag。
- 后端：根路由 /robots.txt 加全局 X-Robots-Tag 中间件，并补 Go 测试；核实安全矩阵测试。
- 测试：新增 test:seo（node --test），放进 CI 的 Web validation；新增 tests/seo.spec.ts（进入 test-payment-web.py 的用例集），只用 HTTP 请求检查响应头和 head，不打开页面截图。

| 编号 | 验收项 | 方法 |
| --- | --- | --- |
| S1-01 | SITE_URL 的解析：合法的 https 来源通过；带路径、查询参数、结尾斜杠、http（非回环）、账号密码、空值都判为非法，并只告警一次 | test:seo |
| S1-02 | 策略表覆盖 src/app 下的所有页面路由：用测试列出全部 page.tsx，每一个都必须在表里，否则失败（防止以后新增页面漏配） | test:seo |
| S1-03 | 同一份 next build，分别用 SITE_INDEXING=on 和未设置启动：开启时 / 输出 index 和绝对地址的 canonical；未设置时全站 noindex、没有 canonical；/plans、/login、/register 两种情况下都能正确切换（证明 D3 生效） | seo.spec，两个 Next 进程 |
| S1-04 | 开启状态下：robots.txt 内容与第 4 节一致，Sitemap 行使用 SITE_URL；sitemap.xml 是合法 XML，只含第 4 节标“是”的地址（本轮为 /、/plans、/download、/guide），全部是 https 绝对地址；关闭状态下 robots.txt 为 Disallow 全部，sitemap 为空 | seo.spec |
| S1-05 | 带参数的地址：/plans?cycle=yearly、/register?ref=ABC 的 canonical 不带参数 | seo.spec |
| S1-06 | 私有页面（第 4 节标 noindex 的每一项，各取一个代表）在开启状态下仍输出 noindex | seo.spec |
| S1-07 | /api/runtime-config 和 /api/v1/meta 的响应带 X-Robots-Tag: noindex, nofollow；/download/get/* 的现有头不变 | seo.spec |
| S1-08 | Host 与 SITE_URL 不一致时（用 127.0.0.1 访问开启的进程），页面输出 noindex，canonical 仍指向 SITE_URL | seo.spec |
| S1-09 | 不存在的地址返回 404 状态码和 noindex | seo.spec |
| S1-10 | 后端：GET /robots.txt 返回 Disallow 全部；任意 /api/v1 响应（包括 404 和 401）都带 X-Robots-Tag；安全矩阵测试通过 | go test -race ./... |
| S1-11 | 构建产物（.next 下的服务端和静态文件）与 S1-03 抓到的 HTML 中都不出现 api.asterlink.vip 和测试用的 API_INTERNAL_URL 值 | test:seo 或 seo.spec 的产物扫描 |
| S1-12 | 原有全部测试通过：lint、typecheck、build、test:* 全部、test-payment-web.py 全量、copy-guard；浏览器任务耗时与基线相比增加不超过 1 分钟 | CI |

### 第 2 轮：页面元数据与可抓取内容

实施内容：第 5 节全部内容；分享图生成并提交；服务端套餐读取模块；首页预览和 /plans 接收初始数据；页脚和“三步开始使用”加内部链接。

| 编号 | 验收项 | 方法 |
| --- | --- | --- |
| S2-01 | 每个可收录页面的标题和描述唯一，长度符合 D7，不含禁用词和“VPN” | test:seo + test:copy |
| S2-02 | 不执行 JavaScript 时（直接请求 HTML），首页和 /plans 的 HTML 里有每个在售套餐的名称、月付价格、流量和设备数，与夹具套餐一致 | seo.spec |
| S2-03 | /plans 返回 503 时：首页仍为 200，预览整块隐藏；/plans 为 200，显示加载态或错误提示（与现在相同）；缓存中有上次结果时用上次结果 | seo.spec + 单元测试（可注入的读取模块） |
| S2-04 | 首页和 /plans 在浏览器中加载时没有“骨架屏 → 内容”的闪烁，首屏绘制时套餐卡已在（记录从首次绘制开始的套餐卡出现时间） | 浏览器用例 |
| S2-05 | 每个可收录页面都有 og:title、og:description、og:url（绝对地址）、og:image（绝对地址，可以访问，1200×630，PNG）、og:locale zh_CN、twitter:card | seo.spec + 品牌资产检查 |
| S2-06 | /download 的描述只列已上架平台：夹具分别为“只上架 Android”和“全部未上架”时，描述文字相应变化，并与页面上的平台卡状态一致 | seo.spec（使用 /fixture/downloads 的 set 参数） |
| S2-07 | 每个可收录页面恰好一个 h1，h2/h3 不跳级 | seo.spec 解析 HTML |
| S2-08 | 页脚有“使用教程”链接；首页到 /plans、/download、/guide、/register 的链接都是普通 a 链接 | home.spec 扩展 |
| S2-09 | 第 1 轮全部用例和原有全部测试继续通过 | CI |

### 第 3 轮：教程分平台、结构化数据、信任页面

实施内容：第 6 节全部内容；策略表和 sitemap 加入新页面；站内链接迁移；用户审阅条款和隐私政策的文字。

| 编号 | 验收项 | 方法 |
| --- | --- | --- |
| S3-01 | /guide/{四个平台} 返回 200，首屏 HTML 含该平台全部章节标题和步骤文字；标题、描述、canonical 各不相同 | seo.spec |
| S3-02 | /guide?platform=macos 308 到 /guide/macos；/guide?platform=xyz 308 到 /guide；/guide/xyz 返回 404 | seo.spec |
| S3-03 | /guide 总览：服务端 HTML 对不同 UA 完全相同（用 iPhone、Android、Windows、Googlebot 四种 UA 请求后比对正文）；浏览器中按设备高亮推荐平台 | seo.spec + guide.spec |
| S3-04 | 侧栏“使用教程”在 /guide 和 /guide/* 上都高亮；平台切换走链接，浏览器的后退和前进正确 | guide.spec 修改 |
| S3-05 | 站内不再出现 /guide?platform= 链接（扫描 src 和渲染后的 HTML） | test:seo |
| S3-06 | 每段 JSON-LD 都能解析为合法 JSON，@context 和 @type 正确，所需字段齐全；Product 的价格与页面显示的价格逐个相等 | seo.spec |
| S3-07 | 套餐名称或描述含“</script>”、引号、尖括号时，JSON-LD 被正确转义，页面结构不被破坏 | test:seo |
| S3-08 | FAQPage 的问答与首页可见的 FAQ 文本逐字一致，覆盖“有/无客服邮箱”“有/无在线客服”“支持/不支持升级”的组合 | test:seo（纯函数）+ seo.spec |
| S3-09 | SoftwareApplication 只出现在已上架的平台，不含任何 http 下载地址；在“全部未上架”夹具下不输出 | seo.spec |
| S3-10 | 不出现 aggregateRating、review、HowTo、SearchAction | test:seo |
| S3-11 | /terms、/privacy 返回 200，可收录，在 sitemap 中，页脚和注册页有链接；文字通过 copy-guard；**用户已在 PR 中确认文字**（未确认则本项 BLOCKED，且不合并） | seo.spec + 用户确认 |
| S3-12 | sitemap 包含第 4 节的全部 9 个地址，不多也不少 | seo.spec |
| S3-13 | 原有测试和第 1、2 轮的用例全部通过 | CI |

### 第 4 轮：性能、上线与收尾

实施内容：第 7 节全部内容；docs/seo-operations.md（日常运营、禁止事项、上线与回滚、Search Console 使用说明、关键词与页面的对应关系）；更新 docs/deployment.md 和 frontend/.env.example；全量回归。

| 编号 | 验收项 | 方法 |
| --- | --- | --- |
| S4-01 | 记录四个页面的 Lighthouse 基线（移动端，每页 3 次取中位数），并写入第 14 节 | 审计脚本 |
| S4-02 | 优化后（如有优化）四个页面达到 D18 的预算：LCP、CLS、TBT、SEO 100、无障碍 ≥ 90；未达标的项写明原因，不虚报 | 审计脚本 |
| S4-03 | 浅色、暗色 × 1440、390 宽度下，首页、/plans、/download、/guide/windows 的整页截图与第 3 轮相比没有非预期的变化 | 现有截图用例 |
| S4-04 | 部署文档中的 Nginx 片段经 nginx -t 语法检查（在本机或 CI 的临时容器中进行；无法执行时标 BLOCKED 并说明原因） | 手工或脚本 |
| S4-05 | .env.example 中新增的变量说明完整；SITE_INDEXING 缺省即关闭的行为在文档中写明 | 审阅 |
| S4-06 | 全量回归：后端 go test -race、网站全部 node 测试、test-payment-web.py 全量、CI 全部任务绿色 | CI |

## 10. 测试方法要点

- 纯函数优先：站点配置、策略表、标题和描述数据、JSON-LD 构造、FAQ 文本生成都写成纯模块，用 node --experimental-strip-types --test 测试，与现有的 test:guide、test:downloads 方式相同。
- HTTP 级端到端：seo.spec 在 test-payment-web.py 的用例集里运行，大部分断言用 Playwright 的 request 直接取 HTML 和响应头，再做简单的 HTML 解析，不开浏览器页面，以控制浏览器任务的耗时。
- 两个 Next 进程：download 第 3 轮已经有“第二个 offline Next 进程”的先例。本规划需要一个“收录开启”的进程（SITE_URL 设为夹具地址，SITE_INDEXING=on），默认进程保持关闭，S1-03 用这两个进程比对。新进程只在 seo.spec 运行期间存在。
- 夹具：套餐数据用现有的套餐夹具；下载状态用 /fixture/downloads?set=empty|base|multi；含特殊字符的套餐名称只在单元测试中构造，不污染共享夹具。
- worktree 注意事项（来自以往轮次的记录）：test-payment-web.py 需要真实的 frontend/node_modules（用 cp -cR 从仓库根目录复制，Turbopack 不接受符号链接）；在 worktree 里 Bash 不接受 heredoc，补丁脚本要先写到 /tmp 再运行。
- 外部校验工具（Rich Results Test、Schema Markup Validator、PageSpeed Insights、Search Console 的网址检查）只能针对公网地址使用，所以放在第 11 节的用户实测里，不作为轮次验收的前提。

## 11. 用户部署后的实测（接在现有 U 系列之后）

| 编号 | 实测内容 |
| --- | --- |
| U-70 | 用 curl 核对：http://asterlink.vip、http://www.asterlink.vip、https://asterlink.vip 都 301 到 https://www.asterlink.vip，并保留路径；响应带 HSTS |
| U-71 | 先以 SITE_INDEXING=off 部署：首页 head 为 noindex，robots.txt 为 Disallow 全部；改为 on 并重启后，首页变为 index，robots.txt 和 sitemap.xml 内容正确 |
| U-72 | https://api.asterlink.vip/robots.txt 为 Disallow 全部，任意接口响应带 X-Robots-Tag；四个客户端登录和连接都不受影响 |
| U-73 | Google Search Console 添加“网域”资源（DNS TXT 验证），提交 sitemap.xml，状态为“成功”；用网址检查对首页做“测试实际网址”，结果为“可以编入索引”，渲染后的 HTML 中有套餐价格 |
| U-74 | Rich Results Test 检查 /plans（Product）、首页（Organization、FAQPage）、/guide/windows（BreadcrumbList），没有错误 |
| U-75 | Bing Webmaster Tools 从 Google Search Console 导入站点并提交 sitemap |
| U-76 | 把首页链接分别发到微信、Telegram，预览卡片显示标题、描述和分享图（微信的显示规则以平台为准，没有卡片时记录现象即可） |
| U-77 | PageSpeed Insights（移动端）检查首页和 /plans；上线 28 天后查看 Search Console 的“核心网页指标”报告 |
| U-78 | 上线 2 到 4 周后，用 site:www.asterlink.vip 搜索和品牌词“AsterLink”搜索，记录已收录的页面数和首页的位置；在 Search Console 的“网页”报告中确认私有页面显示为“已被 noindex 标记排除” |
| U-79 | 审阅并确认 /terms、/privacy 的最终文字（第 3 轮合并前） |

## 12. 商业运营建议（不属于代码验收，写入 docs/seo-operations.md）

- 关键词与页面对应：品牌词 → 首页；价格和套餐类 → /plans；客户端下载类 → /download；“某平台怎么安装或使用” → /guide/{平台}。新增内容先确认它对应哪个页面，避免多个页面争同一个词。
- 内容更新节奏：每次客户端发版后更新对应教程的截图和文字，这能让页面保持“新鲜”；新增的常见问题优先放进首页 FAQ 或教程 FAQ，不另开很薄的单页。
- 外部链接：通过正当渠道获得（应用商店页面、社交媒体主页、合作方的正常推荐），不买链接、不做链接农场，也不在论坛群发。
- 监控：每周看一次 Search Console 的“网页”“效果”“核心网页指标”报告；出现“已抓取，尚未编入索引”或“重复网页”时，对照本文第 4 节排查。
- 百度（可选）：百度站长平台可以提交 sitemap，并用 SITE_VERIFICATION_BAIDU 验证；百度收录与 ICP 备案状态和内容审核相关，结果不在本规划控制范围内。
- 禁止事项：隐藏文字、按 UA 区别输出、门页、虚假评分、关键词堆砌、未兑现的宣传用语（与 copy-guard 一致）。

## 13. 完成定义

- 四轮 PR 全部合并，CI 全绿，第 14 节有每轮的如实记录。
- 生产环境开启收录后，U-70 到 U-74 通过；其余 U 项按时间推进并记录结果。
- 本地、CI、预发环境在默认配置下都不可收录；api.asterlink.vip 不可收录。
- 不新增第三方脚本；首页“无非本站请求”的断言继续成立；文案守卫没有豁免项。

## 14. 实施记录

（每轮完成后在此追加：日期、分支、基线、PR、合并提交、各验收项的结果和证据、偏离规划之处及原因、遗留问题。）

### 第 1 轮：收录基础设施（2026-11-06，分支 worktree-seo-round1-indexing，基线 94f253b）

实现内容：

- frontend/src/lib/seo.ts（纯函数，test:seo 覆盖）：
  - SITE_URL 解析（parseSiteURL），siteConfig 汇总开关、规范来源和验证值；
  - 规范主机判断 canonicalHost；
  - 收录策略表 policies，共 17 条路由，与第 4 节一致，/admin/console 下的全部页面由 console 的 layout 统一声明；
  - pageSEO、closedRobots、robotsTxt、sitemapXml。
- frontend/src/lib/server/seo.ts：
  - 按请求读取环境变量，每个问题只告警一次，日志不含变量的值。页面和路由处理器各自加载一份模块，所以“已告警”的记录放在进程的 globalThis 上。
  - indexableMetadata 读取请求的 Host，输出 robots、canonical 和 metadataBase，并因此让页面变为按请求渲染（D3）。
  - verificationMetadata 只用在首页（D15）。
- 页面：
  - 可收录的 6 个页面（/、/plans、/download、/guide、/login、/register）改为 generateMetadata 调用 indexableMetadata；
  - 不可收录的 10 个页面和 console 的 layout 改为 closedRobots(自己的路由)；
  - 根布局保持静态，默认 noindex, nofollow（D4）。
- next build 路由表：/login、/plans、/register 由静态（○）变为动态（ƒ），/robots.txt、/sitemap.xml 为动态；私有页面仍是静态预渲染，因为它们的值与环境无关。
- next.config：
  - htmlLimitedBots 设为匹配所有 UA。Next 16 默认只对它名单里的“受限爬虫”（含 Bingbot、百度，但**不含 Googlebot**和普通浏览器）把元数据放进 head，对其他访客会把元数据流式追加到 body。这个选项在 Next 16.3.6 里只控制元数据是否流式输出（server/lib/streaming-metadata.js），不影响其他渲染行为。
  - /api/:path* 加 X-Robots-Tag: noindex, nofollow。
- 新增路由处理器 src/app/robots.txt/route.ts 和 src/app/sitemap.xml/route.ts：force-dynamic，Cache-Control 为 public, max-age=3600，并带 X-Robots-Tag: noindex。
- download-view：下载按钮加 rel="nofollow"（D10）。
- 后端：
  - 第一个中间件给所有响应加 X-Robots-Tag: noindex, nofollow，包括错误、404 和被拒绝的来源；
  - 根路由 GET /robots.txt 返回 Disallow 全部；
  - 安全矩阵的 public 白名单加入 GET /robots.txt。
- 测试与 CI：
  - scripts/seo.test.mjs（test:seo）加入 CI 的 Web validation；
  - tests/seo.spec.ts 加入付款用例集；
  - scripts/test-payment-web.py 新增第三个 Next 进程 frontend-indexed：同一份构建，SITE_URL 为它自己的地址，SITE_INDEXING=on，并带两个验证值，通过 SEO_INDEXED_URL 传给用例。默认进程不设 SITE_*，而且 harness 本来就会过滤 SITE_ 开头的外部变量。
- frontend/.env.example 加入 SITE_URL、SITE_INDEXING 和三个验证变量的说明，包括“Host 必须原样透传”。

偏离规划之处：

1. SITE_URL 允许并去掉一个结尾斜杠，与后端 WEBSITE_BASE_URL 的规则保持一致（config/mail.go websiteOrigin），第 3 节 D1 原写“不能带结尾斜杠”。这样两个变量可以填同一个值，运维更不容易填错；带路径、两个斜杠、查询参数或账号密码的值仍然非法。
2. 根布局没有改为运行时生成，metadataBase 只由可收录页面在自己的 generateMetadata 中给出（Next 允许按段设置）。这样私有页面可以继续静态预渲染，D3 的要求不受影响。
3. sitemap 中 /download 的 lastmod（D13）本轮未写：公开下载列表的解析结果（PublicDownload）目前不保留 released_at，补上要改下载模块和它的测试。第 3 轮会重新处理 sitemap，届时一并补上；在此之前不写 lastmod，符合 D13“不真实就不写”的要求。
4. 首页 canonical 由 Next 输出为不带结尾斜杠的来源地址（如 https://www.asterlink.vip），sitemap 写的是带斜杠的形式。两者对搜索引擎是同一个地址，用例按 URL 规范化后比较。
5. 404 页有两个 robots 标签：Next 自带的 noindex，加上根布局的 noindex, nofollow。两者一致，没有冲突，S1-09 只要求每个值都含 noindex。

验收结果：

| 编号 | 结果 | 证据 |
| --- | --- | --- |
| S1-01 | PASS | test:seo：合法来源 7 种、非法值 16 种（含 http 非回环、路径、双斜杠、查询参数、锚点、账号密码、空格）；开关组合 7 种；问题说明不含变量的值 |
| S1-02 | PASS | test:seo：枚举 src/app 下的 26 个 page.tsx，每个都必须调用与自身路由一致的 indexableMetadata 或 closedRobots；策略表里的每一项都有对应页面；页面文件中不得自写 robots 对象 |
| S1-03 | PASS | seo.spec：同一份构建，关闭进程中 6 个可收录页面都是 noindex, nofollow 且没有 canonical，开启进程中都是 index, follow 且 canonical 为绝对地址；标签都在 head 里，body 中没有 |
| S1-04 | PASS | seo.spec + test:seo：两种状态下的 robots.txt 和 sitemap.xml 内容、Content-Type、X-Robots-Tag |
| S1-05 | PASS | seo.spec：带 cycle、plan+pay、ref、next、platform 参数的地址，canonical 都不带参数 |
| S1-06 | PASS | seo.spec：开启状态下 12 个私有页面的 robots 值与第 4 节一致，且没有 canonical |
| S1-07 | PASS | seo.spec：两个进程的 /api/runtime-config、/api/v1/meta、/api/v1 不存在的接口都带 noindex, nofollow；/download/get 的 302 保留 noindex；/download 上所有指向 /download/get/ 的按钮都带 rel="nofollow" |
| S1-08 | PASS | seo.spec：Host 为 old.example.test 或 localhost:端口时 noindex，canonical 仍指向 SITE_URL；Host 正确时恢复 index |
| S1-09 | PASS | seo.spec：两个进程中不存在的地址都返回 404，robots 值都含 noindex，没有 canonical |
| S1-10 | PASS | TestAPIHostIsNeverIndexed：robots.txt 的内容、类型和缓存头，以及 7 种响应（200、401、404、根路径 404、403 来源拒绝）都带 X-Robots-Tag。在私有 PostgreSQL 上跑 backend go test -race -count=1 ./...，全部包通过（internal/api 用时 490 秒），其中包括 TestSecurityRouteAccessMatrix。说明：scripts/test-native.py 在 worktree 里会停在 Mihomo 准备这一步，因为 worktree 里没有 Mihomo 子模块（以往轮次也是如此），所以本地改用同样的数据库设置单独跑后端测试；依赖 Core 和节点 agent 二进制的端到端用例因此跳过，它们由 CI 完整执行 |
| S1-11 | PASS | test:seo 扫描 .next 构建产物，没有 api.asterlink.vip；seo.spec 检查了 20 多张页面的 HTML，都不含 api.asterlink.vip 和夹具的内部 API 地址 |
| S1-12 | 本地 PASS，CI 见 PR | scripts/test-payment-web.py 全量 129/129 通过，用时 10.6 分钟；其中 seo.spec 的 9 个用例单独跑时用时 1.8 秒，第三个 Next 进程的启动不到 1 秒，浏览器任务的耗时增加远小于 1 分钟。网站全部 node 测试本地通过：test:api-proxy 17/17（本机需要 PLAYWRIGHT_CHROME_PATH）、nodes 3、downloads 21、dashboard 6、referral 5、copy 4、theme 5、home 3、support 6、tickets 12、guide 14、toast 7、seo 9；tsc、eslint 通过，新文件已按 prettier 格式化；付款用例集全量和 CI 见下 |

需要用户留意：

- 上线时 Nginx 必须原样透传 Host（docs/deployment.md 现有片段 proxy_set_header Host $http_host 符合）。否则即使 SITE_INDEXING=on，所有页面也会是 noindex，日志里会有一条“host other than SITE_URL”的告警。U-71 会检查这一点。
- 本轮没有动标题和描述（第 2 轮），也没有动 /guide 的结构（第 3 轮）。
