# 后端单目录部署与编译

## 本次修复

旧版 `go.mod` 把 `vpn/nodepolicy` 替换为 `../shared/nodepolicy`。把 `backend/` 独立上传为 `/www/wwwroot/test.hyshentou.cn_backend` 后，Go 就会寻找 `/www/wwwroot/shared/nodepolicy`，缺少它即出现 `replacement directory ... does not exist`。这不是 PostgreSQL、`.env`、代理节点参数或文件权限错误，也不是传入 `main.go` 绝对路径引起的。

现在后端使用 `replace vpn/nodepolicy => ./nodepolicy`，所需源文件和测试夹具已随 `backend/` 提交。服务器不需要 FlClash、Flutter、Mihomo 子模块或 Python 来构建 API。完整复制 `backend/`，无论目录叫什么名字都可以构建；不要只复制 `cmd/` 或只修改 go.mod 而漏掉 `nodepolicy/`。

```text
test.hyshentou.cn_backend/
  go.mod
  go.sum
  nodepolicy/
    go.mod
    policy.go
    policy_test.go
    testdata/proxies.yaml
  cmd/api/main.go
  cmd/doctor/main.go
  internal/...
  shared-sources.json
  .env                  # 仅放服务器自己的配置，不在源码包中
```

## 已部署服务器的操作

先在新发布目录解压这次完整的 backend 源码，核对包校验值。保留服务器原有 `.env`、节点加密密钥、数据库及旧二进制备份；源码包只含 `.env.example`，不要把它直接改名覆盖有效配置。不修改数据库、不需要为本次构建修复运行迁移。

在包含新版 `go.mod` 的实际后端目录执行：

```bash
cd /www/wwwroot/test.hyshentou.cn_backend
go version
test -f nodepolicy/go.mod && test -f nodepolicy/policy.go
GOWORK=off go list -m -f '{{.Replace.Dir}}' vpn/nodepolicy
GOWORK=off go mod download
mkdir -p bin
GOWORK=off go build -mod=readonly -trimpath -o bin/api.next ./cmd/api
GOWORK=off go build -mod=readonly -trimpath -o bin/doctor ./cmd/doctor
```

`go list` 应指向此后端目录中的 `nodepolicy`，不再指向外面的 FlClash。推荐按整个包 `./cmd/api` 编译，确保将来新增同包源文件不会漏编；当前 `go build /绝对路径/cmd/api/main.go` 也经过隔离回归验证。

CI 使用 Go 1.26.6，可优先保持一致；不要盲目升级依赖或删除 go.sum。网络首次构建仍须能下载 go.mod/go.sum 中的普通 Go 依赖，但 `vpn/nodepolicy` 已在本地，不需要 `go get vpn/nodepolicy`。无需修改 GOPROXY、关闭节点校验或绕过生产保护来修复本错误。

编译成功不代表现有服务自动更新。确认进程管理器原来的启动文件路径和 WorkingDirectory，再在维护窗口停止旧进程、替换对应二进制并重启。这里的 `bin/api.next` 不会覆盖运行中的文件；服务应以非 root 专用用户运行。保持 WorkingDirectory 为后端目录，因为 API 在工作目录加载 `.env`。检查 `/healthz` 和普通账号登录；不要启动第二个进程争抢相同端口。

本次只是打包/依赖路径修复，没有改变 API 格式、15 种协议规则、流量倍率、套餐、TLS、鉴权或数据库结构，也不需要为此重编译已提供的多协议 macOS DMG。更详细的真实部署与商业边界见仓库 docs/deployment.md 和 docs/managed-protocols.md。

## 注册与邮件基础（handoff_auth.md 第 1 轮）

本轮新增 `Registration and mail` 配置分组、三张认证表、SMTP 发送 worker 与数据库共享限流。网站注册/找回接口在第 2、3 轮接入；**本轮不改变现有 `/api/v1/auth/register` 行为**，因此不要把本轮当作已具备验证码注册的版本开放给生产用户。

1. **依赖**：新增 `github.com/wneessen/go-mail v0.7.2`（MIT，唯一依赖 golang.org/x/text，`go 1.24` 不变）。首次构建需要能下载该模块；`go.sum` 已锁定校验值。
2. **迁移**：新增 `email_auth_challenges`、`auth_mail_jobs`、`auth_rate_buckets` 三张表、`users` 的 `email_verified_at`/`password_changed_at`/`auth_version`（默认 1）三列，以及部分唯一索引 `idx_auth_challenge_active`。均为附加迁移，沿用 `-migrate` 的 advisory lock；旧账户的 `email_verified_at` 保持空值，不批量伪造“已验证”。生产 `AUTO_MIGRATE=false`，部署时先备份，再在维护窗口执行一次 `bin/api.next -migrate`。
3. **配置**：在服务器 `.env` 中按 `.env.example` 的 `Registration and mail` 分组填写。`SMTP_HOST`、`SMTP_USER`、`SMTP_PASS` 三项全部留空表示不启用邮件；只填一部分会拒绝启动。`ALLOW_REGISTRATION=true` 必须同时具备完整 SMTP、`WEBSITE_BASE_URL`（生产只允许 HTTPS 根地址）和 `AUTH_MAIL_SECRET`（`openssl rand -base64 32` 生成，不得复用 JWT 或节点密钥）。QQ 邮箱使用 465 隐式 TLS 和 **SMTP 授权码**，不是 QQ 登录密码。启动错误只报告变量名和错误类别，不回显取值。
4. **多实例**：所有 API 实例必须使用同一 `AUTH_MAIL_SECRET`、域名集合和 `WEBSITE_BASE_URL`。轮换邮件密钥会使未完成的验证码/找回链接全部失效：先关闭注册并等待队列清空，再统一更新。邮件密钥不要与数据库备份放在一起。
5. **来源地址**：`TRUSTED_PROXIES` 缺省为空，后端只使用 TCP 对端地址，任何 `X-Forwarded-For` 都被忽略；Next.js 代理当前不转发浏览器地址（第 2 轮补充），因此在配置可信入口之前，邮件申请的来源 IP 额度实际上是“Next.js 服务器一份”，主要依靠邮箱额度和全局额度防滥用。配置时只列出会覆盖客户端转发头的反向代理地址，并用防火墙阻止外部直连 API 端口。
6. **运行与检查**：邮件配置完整时 API 进程内启动发送 worker（每实例 2 并发、单次 20 秒硬超时、45 秒租约、最多 3 次尝试）和每小时一次的认证数据清理；正常退出不会等待卡住的 SMTP 连接，未完成任务在租约到期后由任一实例接手。`bin/doctor` 的 `auth_mail` 字段报告配置状态、待投递/24 小时失败数和最老待投递时长，不发送邮件；未配置邮件时 `production_ready` 为 false 并给出 `auth_mail_not_configured` 警告。
7. **回滚**：本轮只新增表和列，旧二进制可以忽略它们继续运行；不要删除新表或回退 `auth_version`。

## 开发者维护：校验逻辑不能分叉

唯一手工维护源仍为 `shared/nodepolicy`。`backend/nodepolicy` 是提交到仓库的精确快照，不是独立实现。修改源规则或共享原生 API 测试契约后，在完整仓库中运行：

```bash
python3 scripts/sync-backend-shared.py --write
python3 scripts/sync-backend-shared.py --check
python3 scripts/test-backend-shared.py
# 将新增源文件纳入 Git 索引后：
python3 scripts/test-backend-standalone.py
```

同步检查只读且不自动修复 CI 内容，逐字节校验 Go 源码、测试及脱敏夹具，维护 SHA-256 清单，拒绝未审查文件和符号链接。与真实 pinned Core Option 结构体的对照测试仍在 canonical 模块运行，不能为适配独立服务器而移除。客户端原生构建依赖位置不变，避免改变已验证的构建缓存输入追踪。

新增独立部署回归只把 Git 已跟踪的 backend 文件复制到临时 `test.hyshentou.cn_backend` 目录，刻意不带 `.env`、FlClash、Git 元数据或 go.work。执行后端/协议模块测试与 vet、包路径及 main.go 绝对路径构建，并交叉编译 Linux amd64/arm64。它不运行 API、不访问真实数据库；数据库集成回归仍由 `scripts/test-native.py` 的私有 PostgreSQL 执行。
