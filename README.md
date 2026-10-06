# AsterLink · VPN 账户、节点管理与 macOS 开发闭环

Next.js + Mantine 前端、Go + Gin + GORM API、PostgreSQL，以及基于 FlClash/Mihomo 的托管客户端。前后端分别使用自己的 `.env`。

**当前是可验证的开发闭环，不是可直接收费运营的成品。** 已接通管理员节点 YAML、套餐过滤、真实内核累计流量与逐节点倍率结算；账本仍属于 `client_reported`，没有节点侧独立计量/可撤销的每用户代理凭据。新版托管 macOS 连接要求特权 TUN 成功启动，覆盖 TCP/UDP 与 IPv4/IPv6；这不等于系统级断网保护，也不能仅凭 CI 认定所有浏览器均无 WebRTC 泄漏。生产模式拒绝启用这种代理下发方式，避免把开发账本误当成商业强制计费。

## 已实现的主链路

管理员通过 `/login` 登录，进入 `/admin/nodes`：导入、读取、修改节点 YAML，以及带确认和版本校验的删除，配置启停、地区、普通/专线、倍率和允许套餐。普通用户不能调用管理员 API。节点密码使用独立 32 字节密钥进行 AES-GCM 加密，配置写入与无密码审计记录同事务完成；更新使用版本号避免管理员并发覆盖。

macOS 客户端先登录自己的网站账户，再由后端检查有效期、测试/付费权益、设备上限和额度。普通界面只显示可选节点与账户信息；节点密钥由 Core 使用，默认不提供 YAML 编辑/导出。**不展示 YAML 不等于无法从用户自己控制的设备中提取密钥。**

连接前必须获得已确认的授权，之后按累计上下行计数上报。重复批次幂等；切换线路/倍率时先结清旧段，再绑定新版本。断网、会话拒绝、额度耗尽、过期、配置变化等情况停止官方客户端连接。默认每 60 秒上报、授权租约 90 秒；这不是服务器端瞬时断开保证。

macOS 可选择在钥匙串记住密码，按 API 地址隔离、不使用明文偏好文件或 iCloud 同步。重开只预填密码，仍需重新登录；关闭应用撤销当前会话，显式退出登录清除保存项。钥匙串不可用时不降级为明文存储。其他平台尚未实现这一记忆功能。

## 本机开发

需要项目所固定的 Go、Flutter/Dart、Rust/Xcode，以及 Node.js 22+ 和 PostgreSQL 工具。CI 版本见 [`.github/workflows/ci.yml`](.github/workflows/ci.yml)。本机 SDK 入口使用 `scripts/flclash-env.sh`，Node/nvm 入口使用 `scripts/node.sh`。

先从 `backend/.env.example` 与 `frontend/.env.example` 配置各自真实 `.env`，权限设为 `0600`；真实凭据、日志、备份和测试证据均不提交 Git。首次启用开发节点目录，需要：

```dotenv
# backend/.env —— 仅开发环境
APP_ENV=development
AUTO_MIGRATE=false
CLIENT_NODE_CATALOG_ENABLED=true
CLIENT_ALLOW_TEST_ENTITLEMENTS=true
TEST_PURCHASE_ENABLED=true
NODE_ENCRYPTION_KEY=<openssl rand -base64 32 生成，保管并备份，不能随意替换>
```

`CLIENT_ALLOW_TEST_ENTITLEMENTS` 与 `TEST_PURCHASE_ENABLED` 必须同时开启才允许测试套餐客户端联调。轻量套餐没有专线权益；应使用普通线路测试，或用另一个具有专线权益的测试账户，不应绕过套餐检查。

```bash
# 项目根目录：先仅检查准备操作，再显式执行。
# pg_dump 主版本不能低于服务器；macOS 已安装的 libpq 18 可只对本终端启用。
export PATH="/opt/homebrew/opt/libpq/bin:/opt/homebrew/bin:$PATH"
python3 scripts/prepare-dev.py --admin-email admin@vpn.example.invalid
python3 scripts/prepare-dev.py --admin-email admin@vpn.example.invalid --apply
```

准备脚本仅支持 `APP_ENV=development`：先备份当前 schema 和 `.env`，再开启开发开关、显式迁移并创建独立管理员。不会覆盖现有账户、套餐或节点；重复邮箱拒绝创建。管理员随机密码仅写入 `.runtime/development-bootstrap-*/administrator.json`（0600）。脚本失败后检查同目录和数据库状态，不要盲目重跑。**不自动导入示例公网节点。**

也可单独执行控制台操作：

```bash
cd backend
# 只读诊断：不迁移、不写用户、不消耗额度。
go run ./cmd/doctor -email test@test.com
# 先备份，再显式迁移。
go run ./cmd/api -migrate
# 显式授权已有独立管理员账户，保留密码；不要误用普通客户邮箱。
go run ./cmd/api -grant-admin admin@vpn.example.invalid
```

启动本机前后端：

```bash
# 项目根目录，确保 Go 在 PATH 中且 3000/8080 没有其他服务占用。
bash scripts/dev.sh
```

浏览器统一使用 `http://localhost:3000`。所有浏览器 API 请求发往同源 `/api/v1`，Next.js 服务端再按 `frontend/.env` 的 `API_INTERNAL_URL` 转发到 Go。不要混用 localhost 与 127.0.0.1 的 Cookie。浏览器不读取内部后端地址或任何数据库/代理密钥。

构建连接本机 API 的普通 macOS 开发应用：

从集成测试切换回普通应用时，先执行 `bash scripts/flclash-env.sh flutter clean`，避免复用测试期间变动的 App.framework；最后检查整个 bundle 的嵌套签名。不要同时运行同一 Flutter 工程的打包和测试任务。

```bash
test -f FlClash/.env || cp FlClash/.env.example FlClash/.env
chmod 600 FlClash/.env
# 确认 FlClash/.env 的 CLIENT_API_BASE 为本机或实际 HTTPS 入口。
bash scripts/flclash-env.sh flutter pub get
bash scripts/flclash-env.sh flutter build macos --debug --no-pub --target=lib/main.dart
codesign --verify --deep --strict FlClash/build/macos/Build/Products/Debug/FlClash.app
open FlClash/build/macos/Build/Products/Debug/FlClash.app
```

`FlClash/.env` 的 `CLIENT_API_BASE` 是 Core 的构建参数，文件变化纳入构建缓存依赖；不要仅依赖 Flutter 钩子可能过滤掉的终端环境变量。正式远程入口必须使用 HTTPS。替换地址需要重新构建 App；普通构建不得包含 `managed_acceptance` 标签或测试主机注入。以上是本机开发包，不代表 Apple Developer 签名、公证或 App Store 发布完成。

## 管理节点和计费

节点 YAML 只接受 `proxies:` 下的自包含节点，支持受限 Shadowsocks AEAD、HTTP、SOCKS5；不接受 providers、脚本或任意完整 Clash 控制配置。密码在管理员的授权读取中可见，不出现在列表、普通账户 API 或审计记录里。

倍率保存为整数千分比：`500` 表示 0.5×，`1000` 表示 1×。`used_units` 为 1/1000 字节单位：

```text
本段扣量 units = (累计上传增量 + 累计下载增量) × 会话绑定的 rate_permille
可显示的剩余额度 bytes = floor((套餐总量 bytes × 1000 - used_units) / 1000)
```

管理员保存或删除在事务提交后通知在线新版客户端；带鉴权的目录长轮询通常在秒级同步，跨 API 实例每秒检查数据库。**不是 WebSocket，也不是节点侧即时撤销共享凭据。** 目录变更会停止旧连接、结清旧倍率流量，再刷新配置；用户需明确重新连接，最后一个可用节点被删除则清空配置。计费上报仍为 60 秒、授权租约仍为 90 秒。部署顺序、TUN 授权和 WebRTC 验收边界见 [删除同步与 macOS TUN](docs/node-removal-and-macos-tun.md)。

## 验证

根目录 CI 执行 Go/PostgreSQL 竞态回归、Next.js 构建与同源代理安全测试、真实浏览器节点管理到 Mihomo 计费验收、Flutter 回归/覆盖率，以及 macOS 普通应用构建和真实 utun 的双栈 UDP 回归。嵌套的 `FlClash/.github/workflows` 不会替代根仓库 CI。

```bash
# 私有临时 PostgreSQL，自动清理，不读取开发 .env。
python3 scripts/test-native.py

# 隔离浏览器 → 管理员 → 数据库目录 → 实际 Mihomo TCP → 两种倍率。
bash scripts/node.sh python3 scripts/test-node-catalog.py --output artifacts/catalog-new-run

# 前端检查；本机可设置 PLAYWRIGHT_CHROME_PATH 指向已有 Chrome。
bash scripts/node.sh npm --prefix frontend run lint
bash scripts/node.sh npm --prefix frontend run build -- --webpack
bash scripts/node.sh npm --prefix frontend run typecheck
bash scripts/node.sh npm --prefix frontend run test:api-proxy

# 只读部署诊断不意味着真实公网节点已经可用。
(cd backend && go run ./cmd/doctor -email test@test.com)
```

测试使用私有数据库/临时浏览器用户目录和本地可控代理，不读取日常 Chrome 资料。macOS App 三阶段验收脚本为 `scripts/test-macos-managed.py`，它隔离文件、钥匙串测试项与系统代理命令。验收日志和截图保留在 `artifacts/`，不得将其当作真实公网节点、全设备 TUN、正式支付或线上 TLS 的验收证据。历史阶段记录见 `docs/verification.md`；本次完整结果以 PR 和 CI 的实际状态为准。

本轮本机验收记录见 [开发闭环验收](docs/development-acceptance.md)。GitHub CI 状态应以该 PR 的实时 Checks 为准，不能用本机结果替代。

## 正式商业上线前仍必须完成

节点侧的独立计量、每用户认证与可撤销凭据、到期/超额即时限额执行；macOS 特权分发与系统级断网保护、真实浏览器 WebRTC/DNS/IPv6/UDP 泄漏、休眠/网络切换/崩溃验收；真实支付与验签对账、退款与纠纷流程；邮箱验证、找回密码和管理员 MFA；数据库验证 TLS、密钥轮换、监控告警、备份恢复和高可用；签名、公证和其他平台逐一验收。

FlClash 及其依赖的许可证必须分别审查。项目保留了上游 [GPL-3.0 许可证](FlClash/LICENSE)，收费不等于可以忽略修改版客户端分发时的源码和许可证义务。不要承诺向付费用户提供无法兑现的带宽、专线质量或隐私保障。

**已在聊天中披露的数据库和代理凭据应轮换。** 当前开发数据库如仍使用 `sslmode=disable`，其公网链路未加密；提高超时不能替代 TLS、网络隔离和访问控制。
