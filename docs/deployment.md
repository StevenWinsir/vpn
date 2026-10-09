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
{"apiBaseUrl":"/api/v1","apiTimeoutMs":70000}
```

该接口禁止缓存，**不公开内部 URL、数据库凭据、JWT 密钥或全部环境变量**。格式错误的配置返回 503，后端不可达返回 502，代理超时返回 504。

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

## 容器、不同主机与限流

同一物理服务器的**两个独立 Docker 容器通常不共享回环网络**。这种情况下，Next.js 的 `127.0.0.1` 是前端容器自身。应使用受保护的容器网络，例如 `API_INTERNAL_URL=http://backend:8080/api/v1`，Go 在容器内监听可达接口，并通过网络隔离限制访问。不要为便利而公开数据库或后端管理端口。

前后端未来分开部署时，可以把 `API_INTERNAL_URL` 改为服务器可达的私网地址，或可信的 `https://api.example.com/api/v1`。浏览器依旧只访问前端同源路径。跨公网不得使用明文 HTTP 或关闭 TLS 证书验证。

Go 当前不信任任何 X-Forwarded-For，代理下 IP 限流会按 Next.js/Nginx 地址聚合。正式运营需要正确的边缘限流，或另行实现严格的可信代理网段配置。本修复不盲目信任任意转发头，也不调整认证限流阈值。

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

真实前后端/数据库浏览器测试改为从项目根目录执行 `python3 scripts/test-release-rehearsal.py --output artifacts/release-rehearsal-NEW`，每次使用新的输出目录。脚本创建并核对自己持有的私有 PostgreSQL 集群，运行普通 Gin 后端及临时目录内的生产 Next.js，验证注册、购买、Cookie 轮换、退出、订单，并演练迁移和备份恢复。Playwright 现在要求该夹具提供 `E2E_ISOLATED=private-postgres` 与明确的 loopback `PLAYWRIGHT_BASE_URL`；不得直接指向来源不明的 localhost:3000 或部署域名执行写入测试。该真实业务测试与上面的假后端代理回归不同；生产域名、证书、Nginx、真实数据库和节点仍须在明确授权的目标环境另行验证。最新执行结果与边界见 `HANDOFF.md` 第 25 节。
