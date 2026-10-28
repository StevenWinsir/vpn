# 同机远程部署：浏览器同源 API + Next.js 服务端代理

## 后端可以单独上传与编译

最新修复将后端的共享协议依赖随 `backend/nodepolicy` 一并交付，`go.mod` 使用 `replace vpn/nodepolicy => ./nodepolicy`。完整复制 `backend/` 后可重命名为 `/www/wwwroot/test.hyshentou.cn_backend` 并独立构建，不需要在服务器部署 FlClash。此前 `replacement directory ../shared/nodepolicy does not exist` 是旧版跨目录依赖与单独上传不兼容；应更新完整 backend（含 nodepolicy），而不是改数据库、节点配置或删除校验。构建、旧服务替换和 CI 一致性策略见 [后端单目录部署说明](../backend/DEPLOYMENT.md)。

## 请求链路

```text
访问者浏览器
  https://app.example.com/api/v1/auth/login
    → 前端域名的 Nginx
    → 127.0.0.1:3000 上的 Next.js 动态代理
    → 127.0.0.1:8080 上的 Go /api/v1/auth/login

独立公网入口（保留）
  https://api.example.com/api/v1/...
    → 后端域名的 Nginx
    → 同一个 127.0.0.1:8080 Go 服务
```

这里的域名都是示例，需替换为实际域名。网站的浏览器请求固定使用当前前端来源下的 `/api/v1`，不会直接请求 localhost、127.0.0.1 或后端公网域名。`127.0.0.1` 只在 Next.js 服务器进程中使用，代表服务器自身。

所有页面共用 `src/lib/api.ts`。注册、登录、自动刷新会话、退出、套餐、订单等均走该链路。Next.js 只负责转发；Go 的鉴权、定价、业务事务、来源校验和数据库访问不变。

## 环境配置

### 前端：服务端内部地址

在服务器上实际运行的 `frontend/.env` 中使用：

```dotenv
API_INTERNAL_URL=http://127.0.0.1:8080/api/v1
API_TIMEOUT_MS=65000
NEXT_PUBLIC_APP_NAME=AsterLink
```

`API_INTERNAL_URL` 是 **Next.js 服务器可访问的完整 API 前缀**，必须为 HTTP(S) 绝对地址，路径为 `/api/v1`（可有结尾斜杠），不能含用户名、密码、查询参数或片段。不要指向前端自身，否则可能形成代理循环。地址未设置时默认 `http://127.0.0.1:8080/api/v1`，适用于同机直接运行的两个进程。

**无需为了本次修复把旧 `.env` 中的 `NEXT_PUBLIC_API_URL` 改为公网域名。** 本次代码完全忽略旧 `NEXT_PUBLIC_API_URL` 和 `API_BASE_URL`，即使它们仍为 localhost，也不会进入浏览器请求地址。建议升级时清理弃用字段，避免误解。本次没有改写实际 `frontend/.env` 或 `backend/.env`，只更新了前端示例配置。

`API_TIMEOUT_MS` 为代理超时，整数范围 1000–180000，默认 65000 毫秒。未设置时兼容旧 `NEXT_PUBLIC_API_TIMEOUT_MS`，但仅由服务端运行时读取。浏览器通过同源 `/api/runtime-config` 获取代理超时 + 5000 毫秒，保证代理有时间返回结构化超时错误。同一页面内共享配置加载，失败可重试。默认响应：

```json
{"apiBaseUrl":"/api/v1","apiTimeoutMs":70000,"support":{"crispWebsiteId":"83336a84-ae9f-4345-ba1f-583c0790002d","email":null}}
```

`support` 是在线客服配置（见下文“在线客服”），`email` 即 `SITE_SUPPORT_EMAIL`（未设置为 null）。客服配置错误只会关闭客服，**不会**让该接口返回错误。

该接口禁止缓存，**不公开内部 URL、数据库凭据、JWT 密钥或全部环境变量**。格式错误的配置返回 503，后端不可达返回 502，代理超时返回 504。

### 前端：首页页脚的备案号与客服邮箱（可选）

```dotenv
SITE_ICP_RECORD=京ICP备12345678号-1
SITE_SUPPORT_EMAIL=support@example.com
```

两项都只由 Next.js 服务端按请求读取，修改后重启 Next.js 即可，无需重新构建。`SITE_ICP_RECORD` 设置后首页页脚显示备案号并链接到工信部备案查询网站（https://beian.miit.gov.cn/）；`SITE_SUPPORT_EMAIL` 设置后页脚和常见问题中显示客服邮箱。未设置或格式不合法（备案号只允许汉字、字母、数字、空格和连字符，最长 40 字；邮箱须为普通地址）时不显示对应内容。首页的 Android、iOS 下载状态与 `/download` 共用 `ANDROID_*`、`IOS_*` 配置。`SITE_SUPPORT_EMAIL` 同时用作在线客服连接失败时提示里的邮箱。

### 前端：在线客服（Crisp）

```dotenv
# 不设置：使用默认网站 83336a84-ae9f-4345-ba1f-583c0790002d
# off：彻底关闭在线客服（不显示入口，不发任何请求）
CRISP_WEBSITE_ID=
```

只由 Next.js 服务端按请求读取（经 `/api/runtime-config` 下发），修改后重启 Next.js 即可，无需重新构建。取值：不设置或留空用默认网站；`off`（不区分大小写）关闭；其他 Crisp 网站 ID（UUID）使用该网站；其他任何值都视为关闭，并在 Next.js 日志中告警一次（`CRISP_WEBSITE_ID is neither "off" nor a website ID`），其余接口不受影响。

行为（handoff_ui.md 第 9 节）：

- 只在已登录的普通用户页面（总览、节点列表、套餐与订阅、订单记录、推荐有礼、下载客户端、账号设置、支付结果）显示右下角“在线客服”按钮和侧栏“在线客服”入口。首页、登录注册与找回密码页、404、管理后台、管理员账户（包括管理员访问用户页面时）和模拟登录期间一律不加载。
- Crisp 脚本（`https://client.crisp.chat/l.js`）在页面加载完成、标签页可见、进入用户页面约 4 秒后、浏览器空闲时才预加载；用户提前点击则立即加载并显示“正在连接客服…”。浏览器开启省流量模式时只在点击后加载。整个页面生命周期只注入一次。Crisp 自带的气泡始终隐藏，由站点自己的按钮打开对话，关闭对话后再次隐藏；有新消息时按钮上显示红点。
- 加载失败（脚本错误）立即、超时（约 20 秒）后在按钮旁提示“客服连接较慢，请稍后再试”，附“重试”和客服邮箱（若设置）。客服不可达不影响页面加载、数据请求和其他操作。
- 就绪后向 Crisp 设置当前账户的邮箱和昵称。**这两项未经签名校验，客服人员不能仅凭聊天窗口中显示的邮箱执行任何账户操作**（改密码、退款、改套餐等须另行核实身份）。
- 会话隔离：退出登录或会话结束时重置 Crisp 会话并整页刷新，第三方脚本不会留在登录页，也不会带到下一个账户；同一浏览器换另一个账户登录时，先重置上一个账户的对话再设置新身份（浏览器本地存储 `asterlink-support-owner` 记录对话所属账户，存储不可用时每次加载都重新开始对话）。未使用 Crisp 的跨设备会话延续（`CRISP_TOKEN_ID`）。

Crisp 控制台需要需求方自行设置（U-23）：窗口主题色设为品牌青绿（#0d7b61），语言为简体中文。Crisp 窗口的外观不跟随站点暗色主题。

安全与隐私：Crisp 脚本在用户页面拥有整页权限，这是接入第三方在线客服的固有风险，因此它不进入管理后台与认证页。登录 Cookie 为 HttpOnly，脚本无法读取；`Referrer-Policy: strict-origin-when-cross-origin` 使 Crisp 只能看到来源域名。Crisp 会在本站域名下写入自己的 Cookie 与本地存储，将来制定隐私政策时应说明。站点目前没有 CSP；以后若启用 CSP，必须放行 Crisp 的脚本、样式、图片、连接（含 WebSocket）与 frame 域名（`*.crisp.chat`，以 Crisp 官方文档为准），否则客服会静默失效。不要为 Crisp 添加 preconnect/dns-prefetch。

### 后端：上线必须调整的安全配置

在服务器的 `backend/.env` 中核对：

```dotenv
HTTP_ADDR=127.0.0.1:8080
ALLOWED_ORIGINS=https://app.example.com
COOKIE_SECURE=true
# 保留自己的 DATABASE_*、JWT_SECRET 和其他业务配置，不要用示例覆盖真实密钥。
```

`ALLOWED_ORIGINS` 填写**浏览器地址栏中的前端来源**，即 `协议://域名:端口`，不含路径或结尾斜杠。多个可信前端来源用逗号分隔，不能填 `*`。本机配置中的 localhost 来源无法自动成为你的生产域名；照搬到服务器会被 Go 返回 `403 origin_forbidden`。代理保留原始 Origin，不通过伪造 localhost 或关闭 CSRF 来绕过此检查。

`COOKIE_SECURE=true` 取决于**浏览器访问前端是否使用 HTTPS**，不是 Next.js 到 Go 的内部链路是否使用 HTTP。生产入口应使用 HTTPS；`false` 仅用于本地 HTTP 开发联调。

后端的 `APP_ENV=production` 还要求数据库 `verify-full` TLS、关闭模拟购买和自动迁移。应先完成这些条件再开启生产模式，不要为了启动成功而绕过安全检查。此次未修改后端业务代码或这些安全基线。

## Nginx：前后端分别保留域名

下面的片段放入各自**已经配置好域名与 TLS 证书的 server 块**，不是可直接粘贴运行的完整证书配置。

前端 `app.example.com`：所有路径，包括 `/api/runtime-config` 与 `/api/v1/`，都交给 Next.js。

```nginx
location / {
    proxy_pass http://127.0.0.1:3000;
    proxy_http_version 1.1;
    proxy_set_header Host $http_host;
    proxy_set_header X-Forwarded-Proto $scheme;
    # Overwrite (never append) the browser address; Next.js reads it via CLIENT_IP_HEADER.
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_cache off;
    proxy_read_timeout 190s;
}
```

**删除或调整旧的、更具体的 `/api/`、`/api/v1/` location，避免它们抢先把请求发给其他服务。** 本方案由 Next.js 动态路由完成代理，不依赖额外的 Nginx API 分流，也不是把浏览器重定向到后端。CDN 不得缓存 `/api/runtime-config` 或 `/api/v1/*`。

后端 `api.example.com`：保留你原有的入口，或将其转发到相同的 Go 进程。

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Host $http_host;
    proxy_set_header X-Forwarded-Proto $scheme;
    # Required once Go trusts 127.0.0.1 (TRUSTED_PROXIES): overwrite any client-sent value.
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_cache off;
    proxy_read_timeout 190s;
}
```

`proxy_pass` 不追加路径，保留完整 `/api/v1/...`。不要强行改写 Origin、Set-Cookie 或 Cookie 安全属性。3000/8080 可以继续只监听回环，由 Nginx 对外提供 443；无需把 Go 的 8080 暴露到公网。

网站经前端域名取得的 Cookie 属于前端域名。直接访问后端公网域名的 Cookie 属于后端域名，两个入口不会自动共享登录状态；旧版跨域登录用户升级后可能需要重新登录。其他浏览器应用直连后端域名仍需满足 Go 的来源白名单及浏览器 Cookie 规则。

## 构建、启动和后续配置更新

首次升级本次代码，在服务器的 `frontend` 目录执行：

```bash
npm ci
npm run build
npm run start
```

`npm run start` 默认监听 `127.0.0.1:3000`，供同机 Nginx 访问。使用非默认端口：

```bash
npm run start -- --port 3001
```

需要直接监听外部接口或在容器中运行时，可显式覆盖为 `npm run start -- --hostname 0.0.0.0 --port 3000`，同时配置防火墙和 TLS 入口；`0.0.0.0` 只能用作监听地址，不能作为 API 目标。端口使用 CLI 参数或进程环境 `PORT`，不依赖 `.env` 中的 PORT。

必须部署可执行的 Node.js Next 服务，不能设置 `output: export`，也不能只发布 `out/` 或 `.next/static/`。保留生产构建 `.next`、依赖和项目配置，真实 `.env` 放在实际 Next.js 工作目录，不要放入 public 目录。本项目未启用 standalone；自行启用时需单独处理 standalone 的工作目录、环境注入及静态资产。

后续只改 `API_INTERNAL_URL` 或 `API_TIMEOUT_MS` 时：编辑运行目录的 `.env`，重启全部 Next.js 实例，再完整刷新浏览器，不必重新 build。只修改磁盘文件不会改变运行中进程；只做 App Router 页面跳转也不会重置已加载的超时。使用 PM2 时还需核对其进程环境，Docker 环境变化需重新创建容器。`NEXT_PUBLIC_APP_NAME` 仍是构建时品牌配置，修改它需要重新构建。

同名变量优先级是：进程环境 → `.env.production.local` → `.env.local` → `.env.production` → `.env`。`API_TIMEOUT_MS` 优先于兼容字段 `NEXT_PUBLIC_API_TIMEOUT_MS`。配置未生效时先检查覆盖来源和旧进程，不要将内部地址改回 `NEXT_PUBLIC_*`。

Go 从工作目录加载 `.env`，因此后端进程的 WorkingDirectory 应是 `backend`；修改后端配置后需重启 Go。此次没有连接远程服务器、上传代码或重启你的线上服务。

## 网站前端：发布、回滚与紧急关闭客服

网站 UI 改版（handoff_ui.md 四轮：文案、暗色主题与图标、首页、在线客服）只改 `frontend/`，后端、数据库与客户端不变，新前端与当前后端完全兼容。

- **发布**：在 `frontend` 目录 `npm ci && npm run build && npm run start`（或按现有进程管理方式重启）。上线前确认 `CRISP_WEBSITE_ID`（默认即启用默认网站），按需设置 `SITE_ICP_RECORD`、`SITE_SUPPORT_EMAIL`。
- **紧急关闭客服**：`.env` 中设 `CRISP_WEBSITE_ID=off` 并重启 Next.js，无需回滚或重新构建；已打开的页面在下一次整页加载后生效。
- **回滚**：重新部署上一版前端构建（检出上一版本后 `npm ci && npm run build`，或使用保留的上一版 `.next` 目录）并重启 Next.js。浏览器中留下的主题偏好（`asterlink-color-scheme`）、客服归属（`asterlink-support-owner`）和 Crisp 自己的 Cookie 对旧版本没有影响。
- **缓存**：图标文件由 Next.js 按约定地址提供；此前 `/favicon.ico` 为 404，不存在旧图标缓存问题。页面与脚本由 Next.js 构建哈希区分，回滚后完整刷新即可。

## 容器、不同主机与限流

同一物理服务器的**两个独立 Docker 容器通常不共享回环网络**。这种情况下，Next.js 的 `127.0.0.1` 是前端容器自身。应使用受保护的容器网络，例如 `API_INTERNAL_URL=http://backend:8080/api/v1`，Go 在容器内监听可达接口，并通过网络隔离限制访问。不要为便利而公开数据库或后端管理端口。

前后端未来分开部署时，可以把 `API_INTERNAL_URL` 改为服务器可达的私网地址，或可信的 `https://api.example.com/api/v1`。浏览器依旧只访问前端同源路径。跨公网不得使用明文 HTTP 或关闭 TLS 证书验证。

### 真实来源地址（注册/找回邮件的 IP 额度、登录记录、支付回调）

缺省配置下 Go 不信任任何 `X-Forwarded-For`，经 Next.js 代理的全部浏览器请求在 Go 看来都来自 Next.js 主机，IP 限流（包括邮件申请每 IP 每小时 20 次）按这一个地址聚合，主要依靠邮箱额度与全局额度防滥用。同机部署（Nginx → Next.js:3000 → Go:8080，均在回环）按以下三处一起配置后，Go 才按真实浏览器地址计数：

1. **Nginx**：上面两个 server 块都用 `proxy_set_header` **覆盖**（不是 `$proxy_add_x_forwarded_for` 追加）`X-Real-IP`/`X-Forwarded-For` 为 `$remote_addr`。后端入口也必须覆盖，否则一旦 Go 信任 127.0.0.1，客户端经 `api.example.com` 自带的 `X-Forwarded-For` 会被当作真实地址。
2. **Next.js**（`frontend/.env`）：`CLIENT_IP_HEADER=X-Real-IP`。代理只从该请求头取最后一个逗号分隔项，且必须恰好是一个 IP，才以 `X-Forwarded-For` 转发给 Go；浏览器自带的 `X-Forwarded-For` 等头从不转发。未设置时不转发任何地址。
3. **Go**（`backend/.env`）：`TRUSTED_PROXIES=127.0.0.1`（容器或分机部署时填 Next.js/Nginx 的私网地址或 CIDR）。只有这些对端提供的 `X-Forwarded-For` 会被采用。

登录记录（管理后台的“注册 IP / 最近登录 IP”和账号设置页的“最近登录”）、注册 IP 与支付回调白名单读取的是同一个地址，所以上面三处不配置时，它们都会记录 Next.js 主机的地址（同机部署即 `127.0.0.1`，显示为“内网/本机”）。另外，Next.js 代理会把浏览器的 `User-Agent`（截断到 200 字符）转发给 Go，用于登录记录；除此之外不转发其他浏览器头。

支付网关的回调（`POST /api/v1/payments/notify`）应使用后端域名，由 Nginx 直接转发给 Go；来源地址白名单与 U-01 … U-09 实测见 [payment-operations.md](payment-operations.md)。

前提：3000 和 8080 只监听回环或受防火墙保护的私网，外部无法绕过 Nginx 直连；否则任何人都能自填 `X-Real-IP`。配置后用两个不同出口地址申请验证码，确认各自计数，并确认伪造请求头不改变计数（handoff_auth.md R1-08）。认证限流阈值本身不随此调整。

## 验证

服务器上先检查链路：

```bash
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:3000/api/runtime-config
curl -fsS http://127.0.0.1:3000/api/v1/meta
```

再打开前端公网登录页，浏览器 Network 中的登录地址应为 `https://你的前端域名/api/v1/auth/login`。应能看到后端设置的两条 HttpOnly Cookie，刷新、自动续期与退出都保持同源。HTTPS 页面到同源 HTTPS 的请求不是混合内容，即使服务器内部通过 HTTP 回环连接 Go。

项目回归命令：

```bash
cd frontend
npm run lint
npm run typecheck
npm run build
# 要求 8080 空闲：测试只绑定自己的假后端，不访问实际 Go 服务或数据库。
# 使用已安装的 Playwright Chromium，或指定独立测试 Chrome：
PLAYWRIGHT_CHROME_PATH="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" npm run test:api-proxy
```

代理测试复用同一份生产构建，在系统临时目录中加载隔离 `.env`，覆盖默认回环地址、旧公开 URL 被忽略、运行时切换、Cookie 多值/安全属性、Origin/CSRF、请求体大小、路径约束、幂等头、重定向保护、502/503/504，以及静态浏览器产物哈希不变。浏览器使用进程级域名映射访问 `vpn-proxy.example.test`，真实执行登录、刷新 Cookie、订单页和退出；不修改系统 hosts，不访问外部域名，不读取个人浏览器资料，不写真实 PostgreSQL，结束时清理测试服务和临时配置。

真实前后端/数据库浏览器测试改为从项目根目录执行 `python3 scripts/test-release-rehearsal.py --output artifacts/release-rehearsal-NEW`，每次使用新的输出目录。脚本创建并核对自己持有的私有 PostgreSQL 集群，用旧版本二进制生成旧数据，以当前二进制迁移（核对原有列逐值不变、新增列为缺省值），运行临时目录内的生产 Next.js 与带捕获收件箱的当前 API 测试装配，验证验证码注册、购买、Cookie 轮换、退出、订单与旧账户邮件链接重置（两类会话撤销），再演练备份恢复和回滚到已强制邮箱验证的版本（详见 backend/DEPLOYMENT.md 第 4 轮条目）。Playwright 现在要求该夹具提供 `E2E_ISOLATED=private-postgres` 与明确的 loopback `PLAYWRIGHT_BASE_URL`；不得直接指向来源不明的 localhost:3000 或部署域名执行写入测试。该真实业务测试与上面的假后端代理回归不同；生产域名、证书、Nginx、真实数据库和节点仍须在明确授权的目标环境另行验证。最新执行结果与边界见 `HANDOFF.md` 第 25 节。
