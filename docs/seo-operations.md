# 网站搜索引擎（SEO）运维手册

> 来源：`handoff_seo.md`（2026-11-06，四轮）。本文是上线和日常运维手册：怎样打开收录、怎样核对、出了问题怎样回滚、Search Console 怎么用、哪些做法禁止。决策与取舍在该交接书第 3 节（D1–D19），各轮实施记录在第 14 节。Nginx 配置见 [deployment.md](deployment.md)“搜索引擎上线”一节。

## 1. 它由什么组成

| 部分 | 位置 | 说明 |
| --- | --- | --- |
| 收录开关 | `frontend/.env` 的 `SITE_URL`、`SITE_INDEXING` | 只有 `SITE_INDEXING=on` 且 `SITE_URL` 是合法的 https 来源时才允许收录；缺省即关闭。改完重启 Next.js，不需要重新构建 |
| 收录策略表 | `frontend/src/lib/seo.ts` | 每个页面是否可收录、是否进 sitemap；表里没有的页面一律 noindex |
| 标题与描述 | `frontend/src/lib/seo-copy.ts` | 12 个可收录页面各一组，`npm run test:seo` 检查长度、唯一性和禁用词 |
| robots.txt、sitemap.xml | 网站按请求生成 | 收录开启时 sitemap 列出 10 个地址；关闭时 robots.txt 禁止全部抓取、sitemap 为空 |
| 分享卡片 | `public/share/asterlink-share.png`（`npm run brand:icons` 生成） | 需要 `SITE_URL` |
| 结构化数据 | `frontend/src/lib/structured-data.ts` | 首页 Organization、WebSite、FAQPage；套餐页 Product；下载页 SoftwareApplication；各页面包屑。需要 `SITE_URL` |
| 服务条款、隐私政策 | `/terms`、`/privacy`（`frontend/src/lib/legal.ts`） | 修改文字时同时更新页首的“最后更新”日期（`updated`） |
| 后端 API 域名 | Go 的 `/robots.txt` 和全局 `X-Robots-Tag` | api.asterlink.vip 永不收录，与网站开关无关 |
| 性能审计 | `npm run audit:lighthouse` | 见第 6 节 |

## 2. 上线步骤（只做一次）

1. **部署后端**（含 api 域名的 robots.txt 和 noindex 头），确认：

   ```bash
   curl -s https://api.asterlink.vip/robots.txt
   curl -sI https://api.asterlink.vip/api/v1/meta | grep -i x-robots-tag
   ```

   第一条应为 `User-agent: *` 加 `Disallow: /`；第二条应为 `X-Robots-Tag: noindex, nofollow`。

2. **部署网站，暂不打开收录**：`frontend/.env` 写 `SITE_URL=https://www.asterlink.vip`，`SITE_INDEXING` 先不写（或写 `off`），重启 Next.js。按 deployment.md 配好 Nginx 的跳转和 HSTS，`nginx -t` 通过后重载。

3. **核对关闭状态**：

   ```bash
   curl -s https://www.asterlink.vip/ | grep -o '<meta name="robots"[^>]*>'
   curl -s https://www.asterlink.vip/robots.txt
   curl -sI http://asterlink.vip/plans?cycle=yearly | grep -i -E '^(HTTP|location)'
   curl -sI https://asterlink.vip/guide | grep -i -E '^(HTTP|location|strict-transport)'
   curl -sI https://www.asterlink.vip/ | grep -i strict-transport
   ```

   应看到：`noindex, nofollow`；`Disallow: /`；两个 301，`Location` 分别是 `https://www.asterlink.vip/plans?cycle=yearly` 和 `https://www.asterlink.vip/guide`；www 的响应带 `Strict-Transport-Security: max-age=31536000`。

4. **打开收录**：`frontend/.env` 加 `SITE_INDEXING=on`，重启 Next.js，再核对：

   ```bash
   curl -s https://www.asterlink.vip/ | grep -o -E '<meta name="robots"[^>]*>|<link rel="canonical"[^>]*>'
   curl -s https://www.asterlink.vip/robots.txt
   curl -s https://www.asterlink.vip/sitemap.xml
   curl -s https://www.asterlink.vip/dashboard | grep -o '<meta name="robots"[^>]*>'
   ```

   首页应为 `index, follow`，canonical 为 `https://www.asterlink.vip`；robots.txt 允许抓取，只禁止 `/api/` 和 `/download/get/`，最后一行是 Sitemap 地址；sitemap.xml 有 10 个 `<loc>`；`/dashboard` 仍为 noindex。

5. **提交搜索引擎**（见第 4 节），然后按 handoff_seo.md 第 11 节的 U-70 至 U-79 逐项记录结果。

## 3. 回滚与紧急情况

- **停止收录整个网站**：`frontend/.env` 删掉 `SITE_INDEXING` 或改为 `off`，重启 Next.js。所有页面立即变为 noindex，robots.txt 改为禁止全部抓取。已经收录的页面会在搜索引擎下次抓取后逐步移出结果，通常需要几天到几周。
- **紧急删除某个地址的搜索结果**：在 Google Search Console 的“删除”工具里提交（临时隐藏约 6 个月），同时让该页面返回 404 或 noindex，否则到期后会重新出现。
- **回滚网站版本**：按 deployment.md“发布、回滚”一节部署上一版构建。收录开关和 Nginx 配置与网站版本无关，不需要随之修改。
- **HSTS 不能随回滚撤销**：浏览器会在 `max-age` 内一直强制 HTTPS，所以 www 和 api 的证书必须持续有效。

## 4. 搜索引擎后台

- **Google Search Console**：添加“网域”资源 `asterlink.vip`，按提示在 DNS 加一条 TXT 记录完成验证（覆盖所有子域和 http/https，不需要改网站）。然后在“站点地图”提交 `https://www.asterlink.vip/sitemap.xml`。用“网址检查”对首页做“测试实际网址”，结果应为“网址可以编入 Google 索引”，“查看已抓取的网页”里能看到套餐价格。
- **Bing Webmaster Tools**：选择“从 Google Search Console 导入”，站点和 sitemap 一起导入；Bing 的索引同时供 Yahoo、DuckDuckGo 等使用。
- **百度搜索资源平台（可选）**：可以提交 sitemap，并用 `SITE_VERIFICATION_BAIDU` 或 DNS 验证。百度收录与 ICP 备案和内容审核有关，结果不在网站代码的控制范围内。
- **页面验证方式（DNS 不可用时的备选）**：把搜索引擎给的验证码写入 `SITE_VERIFICATION_GOOGLE`、`SITE_VERIFICATION_BING` 或 `SITE_VERIFICATION_BAIDU`（只允许字母、数字、下划线和连字符），重启 Next.js，首页 head 就会输出对应的 meta。
- **结构化数据检查**：用 Rich Results Test（https://search.google.com/test/rich-results）检查 `/plans`（Product）、首页（Organization、FAQPage）和 `/guide/windows`（BreadcrumbList），应没有错误。说明：FAQ 富结果目前只对政府和医疗网站展示，SoftwareApplication 富结果要求有评分，这两类主要帮助搜索引擎理解页面，搜索结果里不一定出现。

## 5. 日常运营

**关键词与页面的对应关系**：新增内容前先确认它属于哪一页，不要让多个页面争同一个词。

| 搜索意图 | 页面 |
| --- | --- |
| 品牌词：AsterLink、AsterLink 官网 | 首页 `/` |
| 价格、套餐、包月、包年 | `/plans` |
| 下载、客户端、安装包 | `/download` |
| 某平台怎么安装、怎么用（如“AsterLink Windows 安装”） | `/guide/windows`、`/guide/macos`、`/guide/android`、`/guide/ios` |
| 使用教程总览 | `/guide` |
| 登录、注册（导航型搜索） | `/login`、`/register`（可收录，不进 sitemap） |
| 条款、隐私 | `/terms`、`/privacy` |

**内容更新**：

- 客户端每次发版后，同步更新对应平台教程的截图和文字（维护方法见 [user-guide.md](user-guide.md)），保持页面“新鲜”。
- 新的常见问题优先加进首页 FAQ（`frontend/src/lib/home-faq.ts`，结构化数据会自动跟随）或对应平台教程的 FAQ，不要另开内容很少的单页。
- 改标题或描述只改 `frontend/src/lib/seo-copy.ts`，然后运行 `npm run test:seo`：标题不超过 30 字，描述 60 到 120 字，全站唯一，不出现“VPN”“翻墙”“科学上网”和广告法禁用的绝对化用语。
- 外部链接通过正当渠道获得：应用商店页面、社交媒体主页、合作方的正常推荐。

**每周看一次**：

- Search Console 的“网页”报告：私有页面应显示为“已被‘noindex’标记排除”；出现“已抓取，尚未编入索引”或“重复网页”时，对照 handoff_seo.md 第 4 节的策略表排查（常见原因：通过其他主机名访问、旧域名没有跳转）。
- “效果”报告：品牌词的排名和点击。
- “核心网页指标”报告：需要上线后积累约 28 天的真实访问数据。

## 6. 性能审计

在 `frontend` 目录：

```bash
npm run build
npm run audit:lighthouse
```

- **它做什么**：脚本在本机回环端口启动生产构建，开启收录，接一个内置的假后端（固定的三个套餐、三个已上架客户端），用 Lighthouse（通过 npx 运行，版本固定，不是网站的依赖）对首页、`/plans`、`/download`、`/guide/windows` 各做 3 次移动端审计，取每项的中位数，与 D18 的预算比较。
- **结果在哪**：报告写在 `artifacts/lighthouse-时间/`（`summary.md` 是汇总表），超出预算时退出码为 1。只看结果不判失败时加 `--report-only`；用 `--out` 指定新的输出目录。Chrome 默认用本机的 Google Chrome，其他位置用环境变量 `CHROME_PATH` 指定。
- **预算（D18）**：LCP ≤ 2.5 秒、CLS ≤ 0.1、TBT ≤ 200 毫秒、SEO 分数 100、无障碍分数 ≥ 90。
- **CI**：CI 的 “Website Lighthouse budget / Nginx examples” 任务每次都跑一遍（`--gate stable`），只以 SEO、无障碍、CLS 判定失败。LCP、TBT 随 CI 机器负载波动，只记录在构件 `lighthouse-summary` 里，不判失败。
- **基线（2026-11-07，第 4 轮，本机 Apple 芯片 Mac，Lighthouse 13.6.0 移动端模拟节流，3 次中位数）**：

| 页面 | 性能 | FCP | LCP | CLS | TBT | SEO | 无障碍 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| / | 100 | 1.21 s | 1.21 s | 0 | 1 ms | 100 | 100 |
| /plans | 100 | 1.06 s | 1.06 s | 0 | 6 ms | 100 | 100 |
| /download | 100 | 1.20 s | 1.20 s | 0 | 0 ms | 100 | 96 |
| /guide/windows | 100 | 1.20 s | 1.20 s | 0 | 0 ms | 100 | 96 |

  `/download` 和 `/guide/windows` 的无障碍扣分项是“正文中的链接只靠颜色区分”（link-in-text-block）。分数仍在预算内，按 D18“不为跑分改变视觉设计”的原则没有改动。以后如果要改，给 `.guide-text a` 和下载页说明文字里的链接加下划线即可。

## 7. 禁止事项

以下做法会被搜索引擎处罚，或违反广告法，一律不做：

- 隐藏文字或链接（与背景同色、移出屏幕、只给爬虫看）。
- 按访问者的 User-Agent 输出不同内容（伪装，cloaking）。教程总览页的设备推荐只在浏览器里判断，服务器输出的内容对所有访客相同，以后修改时也必须保持。
- 批量生成“城市 + 关键词”之类内容很少的落地页（门页）。
- 在结构化数据里写页面上没有的内容，或伪造评分、评论（aggregateRating、review）。
- 关键词堆砌：标题和描述写给人看，不罗列同义词。
- 购买链接、交换链接、在论坛群发链接。
- 无法证明的宣传用语（最快、第一、零日志、不限速等，与网站的文案守卫一致）。
- 第三方统计或广告追踪脚本：本站用 Search Console、Bing Webmaster Tools 和 Nginx 访问日志评估效果，首页“没有任何非本站请求”的测试必须继续通过。

## 8. 常见问题

- **改了 `.env` 没生效**：确认重启的是实际运行的 Next.js 进程（PM2 要核对它的进程环境），再看日志里有没有 `[seo]` 开头的告警，它会说明哪一项配置不合法（不打印配置值本身）。
- **页面显示 noindex，但开关已经打开**：多半是通过其他主机名访问的（直接用 IP、旧域名或预发域名），或 Nginx 没有原样传 Host 头。日志里会有“a page was requested under a host other than SITE_URL”的告警。
- **sitemap 里没有某个页面**：只有策略表里标为“进 sitemap”的页面才会列出；登录、注册页可收录，但按设计不进 sitemap。
- **分享到微信没有卡片**：微信只对部分来源显示卡片，规则以平台为准；先用 Telegram 或 Slack 确认 og 标签正常（U-76）。
