# AsterLink · VPN 账户与订阅服务（第一阶段）

这是 Next.js + Mantine 与 Go + Gin + GORM 的前后端分离项目。**当前交付的是连接真实 PostgreSQL 的账户、订阅和模拟购买基础，不是可直接收费运营的完整 VPN 产品。** AsterLink 是可在前端 `.env` 修改的临时品牌名。

## 已实现

- 首页、注册、登录、用户中心、套餐选择、模拟购买确认、订单分页与移动端布局。
- 邮箱规范化、bcrypt 密码哈希、HS256 JWT，校验算法、签发方、受众、到期时间和数据库会话状态。
- HttpOnly / SameSite Cookie；选择记住登录时持久化最长 30 天；否则会话最长 12 小时。访问 JWT 默认 15 分钟，刷新令牌轮换，数据库仅保存刷新令牌哈希。退出会撤销当前会话及其访问令牌。没有把密码或 JWT 存入 localStorage。
- 实际 PostgreSQL 连接、独立 `vpn_app` schema、连接池、健康检查、非破坏性初始迁移和套餐种子数据。
- 服务端定价、金额以人民币“分”保存；订单与订阅同一事务提交；用户行锁及 `(user_id, idempotency_key)` 唯一约束处理并发和重复提交。
- 套餐有效期、流量额度、专线权限的服务端校验；普通线路 500‰ 与专线 1000‰ 的整数计费算法和节点元数据模型。
- 严格 Origin 白名单、Cookie 写请求的来源检查、JSON 请求限制、请求体上限、基础 IP 限流及安全响应头。
- `/client/bootstrap` 权益检查与节点元数据接口，不返回 YAML、代理地址或密码。当前 `proxy_service_ready` 始终为 false。

## 当前不包含

真实支付和回调验签、管理员后台、邮箱验证/找回密码、可发布的原生客户端、代理节点部署、节点侧实时计量/限额执行、监控/备份/高可用/合规和许可证审查。原生后端现已支持安装实例会话、配置绑定设备上限及私有 YAML 下发；FlClash 已完成 P2–P3 登录、账户 RPC 与冷启动门禁，实际配置应用和计量/连接仍待 P4–P5。

测试套餐不提供代理服务。关闭 `TEST_PURCHASE_ENABLED` 后，测试购买入口及测试套餐的 bootstrap 授权都会失效；生产模式禁止打开模拟支付。不要将测试订单改名冒充真实支付成功。

## 本机启动

需要 Go 1.24+、Node.js 22+。本机已有通过 nvm 安装的 Node；`scripts/node.sh` 可在 PATH 没有 Node 时加载 nvm 的默认版本。

实际配置已经分别写入 `backend/.env` 和 `frontend/.env`，权限为 0600，均被 `.gitignore` 排除。不要复制真实 `.env` 到 Git、聊天、截图或前端构建产物。

```bash
# 终端一：后端，须在 backend 目录运行以加载该目录的 .env
cd /Users/stevenlee/Desktop/vpn/backend
go run ./cmd/api

# 终端二：前端
cd /Users/stevenlee/Desktop/vpn/frontend
bash ../scripts/node.sh npm run dev
```

浏览器统一使用 **http://localhost:3000**，所有业务请求发往当前前端来源的 **`/api/v1`**，再由 Next.js 服务端转发至 **`http://127.0.0.1:8080/api/v1`**。浏览器不直接连接 8080，也不读取内部后端地址。不要在一次会话中混用 `localhost` 与 `127.0.0.1`，它们的 Cookie 所属主机不同。开发 API 只监听本机。

也可在项目根目录运行 `bash scripts/dev.sh`，在同一个终端启动两项服务；Ctrl+C 停止本次脚本启动的进程，日志位于 `.runtime/`。先关闭已运行的 3000/8080 服务以免端口冲突。

首次在另一台机器安装依赖：

```bash
cd backend && go mod download
cd ../frontend && bash ../scripts/node.sh npm ci
```

新数据库首次启动前，在 `backend` 目录运行 `go run ./cmd/api -migrate`。当前用户提供的数据库已经初始化，实际 `.env` 的 `AUTO_MIGRATE` 已设为 false，避免每次启动重复做数据库结构检查。

网络访问 Go 模块代理困难时，可仅为当次命令设置可达的 `GOPROXY`；不要关闭 `GOSUMDB` 校验，也不要随意关闭 TLS 验证。

## 环境配置

后端所有部署参数在 `backend/.env`，完整字段见 `.env.example`。当前开发连接使用用户指定的 `150.158.85.254:4321`、`clash` 数据库与 `clash` 用户。密码仅存在本机真实 `.env`。

**首次连接探测确认该 PostgreSQL 服务端未启用 SSL。当前 `DATABASE_SSLMODE=disable` 仅用于本次开发联调，意味着公网数据库链路未加密。上线前必须启用并验证 TLS，限制数据库端口来源 IP，轮换已在聊天中分享的数据库密码；也可以改为受保护的内网连接。**

| 配置 | 作用 |
| --- | --- |
| `DATABASE_*` | 数据库地址、端口、账号、密码、schema、TLS、连接池 |
| `JWT_SECRET` / `JWT_ISSUER` / `JWT_AUDIENCE` | 签名密钥与令牌身份；密钥已随机生成 |
| `ACCESS_TOKEN_MINUTES` | 访问 JWT 有效期，默认 15 分钟 |
| `SESSION_HOURS` / `REMEMBER_DAYS` | 普通会话及记住登录的最长寿命，刷新不会无限延长绝对到期时间 |
| `COOKIE_SECURE` | 生产必须为 true，开发 localhost HTTP 为 false |
| `ALLOWED_ORIGINS` | 精确前端来源白名单，不能使用 `*` |
| `TEST_PURCHASE_ENABLED` | 仅开发/测试环境启用模拟购买 |
| `AUTO_MIGRATE` | 开发初始化；生产必须 false，通过显式迁移操作变更结构 |
| `REQUEST_TIMEOUT_SECONDS` / `MIGRATION_TIMEOUT_SECONDS` | 请求和显式迁移超时，当前分别为 60 秒与 180 秒；这是开发公网数据库容错，不是生产性能目标 |
| `BCRYPT_COST` / `AUTH_REQUESTS_PER_MINUTE` / `API_REQUESTS_PER_MINUTE` | 密码哈希成本与单进程基础限流 |

前端服务器的 `frontend/.env` 使用 **`API_INTERNAL_URL=http://127.0.0.1:8080/api/v1`** 和 **`API_TIMEOUT_MS=65000`**。均由 Next.js 服务端运行时读取；未设置内部地址时默认同机回环地址。浏览器固定请求当前站点 `/api/v1`，Next.js 动态路由负责转发，保留原始 Origin、Cookie、鉴权、幂等键、响应状态及多条 Set-Cookie。`/api/runtime-config` 只公开固定相对路径和浏览器超时（代理超时 + 5000 毫秒，默认 70000），绝不返回内部地址。所有 API 响应禁止缓存。

**旧 `NEXT_PUBLIC_API_URL` 和 `API_BASE_URL` 不再参与请求地址选择**，因此保留旧 `.env` 的 localhost 值不会让浏览器访问用户电脑。旧 `NEXT_PUBLIC_API_TIMEOUT_MS` 仅作为服务端超时后备，新 `API_TIMEOUT_MS` 优先。首次升级本次代码需要 build；此后修改内部地址/超时，只需重启 Next.js 并刷新浏览器。非法配置返回 503，后端不可达返回 502，代理超时返回 504；不会把内部地址或原始连接错误公开给浏览器。

`NEXT_PUBLIC_APP_NAME` 仍是构建时品牌配置，修改它仍需重新 build。**公开变量绝不能包含数据库密码、JWT 密钥或代理密码。** 后端 `.env` 修改后需重启 API；数据库中的业务配置由请求读取。服务器进程环境变量及 `.env.production.local` / `.env.local` / `.env.production` 可能覆盖 `.env`，部署时应检查这些来源。

同机远程部署、独立后端公网域名、Nginx、Cookie 和环境变量优先级详见 **[docs/deployment.md](docs/deployment.md)**。`npm run start` 默认监听 `127.0.0.1:3000`，公网入口通过 Nginx 转发到 Next.js，包含 `/api/` 路径。前端必须运行 Next.js Node 服务，不能静态导出。**上线仍必须把后端 `ALLOWED_ORIGINS` 改为真实前端来源，HTTPS 入口使用 `COOKIE_SECURE=true`**；不能通过伪造 Origin 或放开 `*` 绕过校验。

生产模式启动会拒绝：模拟购买、自动迁移、不安全 Cookie、非 HTTPS Origin，以及不是 `verify-full` 的数据库 TLS 模式。这是安全基线，不代表完成生产认证。

## 数据与购买规则

schema 内有 `users`、`sessions`、`plans`、`subscriptions`、`orders`、`nodes`。初始化不会删除/清空已有表；套餐种子使用冲突不覆盖，避免每次启动覆盖管理员后续修改。生产应演进为经审查的版本化 SQL 迁移，普通应用账号不应有 schema 变更权限。

演示套餐：轻量 19.90 元 / 100 GiB / 30 天；进阶 39.90 元 / 300 GiB / 30 天；旗舰 79.90 元 / 800 GiB / 30 天。它们是测试定价，不是实际线路报价。原生配置接口已限制当前有效绑定会话的设备数，但安装 UUID 不是硬件身份，也不是节点侧并发执行证明。

有效期内只支持续购同一测试套餐：在原到期时间上增加天数，额度累加，已用流量保留。到期后重购从当前时间起算，重置额度与计数。有效期内跨套餐切换返回 409，避免未定义的升级/抵扣规则。每次购买意图生成独立 UUID 请求标识，同一失败请求重试时必须保留该标识；关闭弹窗或刷新后发起的新购买意图会获得新标识。

1 GiB = 1024³ 字节。`used_units` 的一个单位为 1/1000 字节，避免 0.5 倍计费因逐条取整而失真。实际流量 10 GiB，在倍率 500‰ 下计费 5 GiB，在 1000‰ 下计费 10 GiB。原生客户端上报账本接口已实现，当前明确只采用上下行合计 1000‰；实际 FlClash 计数器/Meter 接线和节点侧采集仍未完成，不伪造流量或逐节点倍率。

## API

统一前缀 `/api/v1`。错误结构为 `{"error":{"code":"...","message":"..."}}`。所有写请求使用 `Content-Type: application/json`；浏览器必须 `credentials: 'include'`。Cookie 写请求必须有可信 `Origin`。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/healthz`、`/readyz`（无前缀） | 存活、实时数据库连通性 |
| GET | `/meta`、`/plans` | 功能开关、上架套餐 |
| POST | `/auth/register` | `{name,email,password,remember}`，自动登录 |
| POST | `/auth/login` | `{email,password,remember}` |
| POST | `/auth/refresh` | 通过 HttpOnly 刷新 Cookie 轮换会话令牌 |
| POST | `/auth/logout` | 撤销当前会话，清除 Cookie |
| GET | `/auth/me` | 当前用户（需登录） |
| GET | `/me/dashboard` | 套餐、额度、订单数量（需登录） |
| GET | `/orders?page=1` | 当前用户订单，每页 20 条（需登录） |
| POST | `/orders/test-purchase` | `{plan_id}` + `Idempotency-Key: <UUID>`（需登录） |
| GET | `/client/bootstrap` | 有效期、配额、线路权限校验及节点元数据（需登录和有效权益） |

浏览器登录只设置 Cookie，不把令牌返回给 JavaScript。原生客户端使用独立会话、VIP 配置读取和客户端上报账本接口。**P2–P4 已通过相应阶段验收：VIP 服务端配置经过 Mihomo 语义验证、会话归属和私有原子保存后实际应用，用户只能选择当前服务端配置的节点，配置管理和导入/导出绕过已收口。** 本机 can_connect 仍为 false，P5 真实 Meter/首次授权未接通，不能作为可连接或可发布的成品。配置兼容范围、隔离测试及计费可信度限制见 [docs/native-client-integration.md](docs/native-client-integration.md)。原有网页 Cookie/JWT 机制保持不变。

## 检查与测试

最新验证见 [HANDOFF.md](HANDOFF.md) 第 9.11、19 节；协议见 [原生协议契约](docs/native-client-protocol.md)。第七轮 Flutter **1,927 项**（零失败/跳过）、Core **133** / managed **38** 项、managed 三轮竞态、vet、后端临时 PostgreSQL **13 项顶层**回归通过；Android arm64 Core 的 NDK vet/c-shared 构建通过。完整 macOS App 的 bundled/first/reopen 三阶段及 24 条命名检查通过，覆盖实际配置应用/节点选择、错误/缺失/替换/换账户清理、Core 重启、App 重开和正常退出。测试后已重建正常 lib/main.dart App 并核验包内 Core 无测试标签/主机开关。证据位于 `artifacts/p4-20260930-r7/`。Android **120 项 JVM** 仍是第六轮历史，本轮未重跑。系统代理命令隔离，真实节点/TUN/系统代理/后台、线上 TLS 和发布仍未验收。

```bash
# 在项目根目录运行：临时 PostgreSQL，同时覆盖原生和原网页业务，不读取真实 .env。
python3 scripts/test-native.py

cd backend
go test ./...
go vet ./...
# 显式授权测试写入当前 .env 指定的真实数据库；只清理该次测试创建的准确 UUID
RUN_DB_TESTS=1 go test -race -count=1 -v ./...
# 显式初始迁移
go run ./cmd/api -migrate

cd ../frontend
bash ../scripts/node.sh npm run lint
bash ../scripts/node.sh npm run build
# 隔离的同源代理回归：真实 Next.js 生产构建 + 本机假后端，不访问数据库。
# 要求 8080 空闲；可设置 PLAYWRIGHT_CHROME_PATH 使用本机 Chrome。
bash ../scripts/node.sh npm run test:api-proxy
bash ../scripts/node.sh npm audit
# 以下完整业务 E2E 会写账户/订单；只能在明确配置的隔离前后端运行。
bash ../scripts/node.sh npx playwright install chromium
bash ../scripts/node.sh npm run test:e2e
```

本机已安装 Chrome，也可跳过浏览器下载，直接执行：

```bash
PLAYWRIGHT_CHROME_PATH="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
  bash ../scripts/node.sh npm run test:e2e
```

浏览器测试在独立临时用户资料中运行，不读取你现有的 Chrome 登录数据。当前公网数据库往返存在明显延迟；生产部署应将 API 与数据库放在低延迟、受保护的同区域网络，不能把扩大超时当成性能优化。

Go 集成测试覆盖注册、重复邮箱、伪造 JWT、CSRF、价格篡改、订单幂等、同键/不同键并发续购、用户隔离、套餐过期/耗尽、专线权限、关闭测试购买、刷新轮换/退出撤销以及伪造 X-Forwarded-For 不能绕过限流。测试数据清理严格限定于当次创建的 UUID，不做全表删除。

浏览器测试覆盖注册→模拟购买→刷新持久化→订单→退出→错误密码→重新登录，以及 390px 移动布局。浏览器测试保留 `browser-qa-...@example.invalid` 测试账户和 `paid_test` 订单以便核查，不设置公开固定管理员密码。截图/失败诊断放在被 Git 忽略的测试目录，不能作为生产资产。

## 下一阶段

见 `HANDOFF.md` 第 19 节。P0–P4 的相应阶段验收已满足，下一轮进入 P5：将唯一 Meter 接到真实内核计数，完成首次确认、每分钟上报、拒绝时停止、重载/退出尾账交接，再根据用户连接意图开放监听。不要恢复旧 setupConfig/quickSetup 或直接信任 session.can_connect。P4 当前只接受自包含的内联节点、显式 select 组和受限内联规则，远程 providers/自动测速/脚本等需后续单独设计。生产构建不得携带测试标签或任意主机注入。系统代理所有权、TUN/后台和其他平台仍按 P6/P7 验收；正式运营还需可信支付、节点侧计量及可撤销凭据。
