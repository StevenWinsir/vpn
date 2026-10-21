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

## 网站邮箱验证码注册（handoff_auth.md 第 2 轮）

本轮起 **`/api/v1/auth/register` 只接受已消费的邮箱验证码**，原来的无验证码注册不再存在；同时新增 `POST /api/v1/auth/register-code`。网站注册页需要与后端同一批发布。

1. **先后顺序**：需第 1 轮的附加迁移已执行（无新迁移）。发布时先保持 `ALLOW_REGISTRATION=false`，部署后端与网站，确认 `/api/v1/meta` 的 `allow_registration` 为 false 且注册页显示“当前暂未开放注册”；完成一次受控邮件冒烟后再改为 `true` 并重启 API。
2. **注册开关**：`ALLOW_REGISTRATION=false`、或邮件配置不完整时，发码与最终注册都返回 403 `registration_disabled`，已收到验证码的用户也无法完成注册；已有用户登录不受影响。网站入口（首页、登录页“创建账户”、注册页）都按 `/meta` 实际值显示，读取失败时按关闭处理。
3. **来源地址**：Next.js 新增可选 `CLIENT_IP_HEADER`，与 Go 的 `TRUSTED_PROXIES` 和 Nginx 覆盖转发头三处一起配置，见 docs/deployment.md“真实来源地址”。不配置时行为与以前相同（所有网站请求按 Next.js 主机地址计 IP 额度）。
4. **回滚**：回滚到第 1 轮或更早的二进制会**重新开放无验证码注册**（旧接口不认识验证码）。如必须回滚，先在入口层阻断 `/api/v1/auth/register` 与 `/api/v1/auth/register-code`，再替换二进制（handoff_auth.md 12.2）。
5. **接口细节**：请求/响应与错误码见 docs/auth-mail.md“注册接口”。

## 邮件链接找回与密码重置（handoff_auth.md 第 3 轮）

本轮新增 `POST /api/v1/auth/forgot-password`、`/auth/reset-password/validate`、`/auth/reset-password`，以及网站 `/forgot-password`、`/reset-password` 页面；网站与客户端登录、网站刷新的内部加锁顺序随之调整（外部契约不变）。无新迁移。

1. **所有实例同一版本后才开放重置**：重置依赖“登录/刷新在锁内复核 `auth_version`”。滚动发布期间若仍有旧实例承接登录或刷新，可能在重置后创建旧密码会话。多实例部署时，先让全部 API 实例升级到本版本，再发布包含找回入口的网站；或在全部实例升级前于入口层阻断 `/api/v1/auth/forgot-password` 与 `/api/v1/auth/reset-password`。
2. **找回可用性**：`/meta` 的 `password_reset_enabled` 等于“邮件配置完整”。邮件未配置时找回页显示“暂不可用”，接口返回 503 `mail_unavailable`。找回与 `ALLOW_REGISTRATION` 无关：关闭注册不影响已有用户找回。
3. **代理与日志**：重置链接的 token 在 URL fragment 中，不会出现在 Nginx/Next.js 访问日志；它只出现在 `/api/v1/auth/reset-password*` 的 POST 请求体中。不要开启记录请求体的访问日志或 APM 采样。`/reset-password` 页面响应 `Referrer-Policy: no-referrer`、`X-Robots-Tag: noindex` 并按请求渲染（no-store）。
4. **回滚**：回滚到第 2 轮或更早的二进制时，旧二进制没有找回接口，网站找回页会显示请求失败；更早版本还缺少登录竞态复核。先在入口层关闭网站找回入口与上述接口，再替换二进制。已完成的重置不会被回滚：新密码哈希、递增的 `auth_version`、已撤销的会话都保持（handoff_auth.md 12.2）。
5. **接口细节**：见 docs/auth-mail.md“找回与重置接口”。

## 四平台入口与发布演练（handoff_auth.md 第 4 轮）

本轮后端生产代码与迁移不变；变化在四个 APP、浏览器测试夹具与发布演练脚本。

1. **APP 发布顺序**：后端与网站先按第 2、3 轮条目发布。APP 新版只增加“忘记密码”网站入口并修正旧保存密码的处理，旧版 APP 仍可与新后端配合（撤销沿用 `native_session_expired`，旧密码得到 `invalid_credentials`）。各平台按自己的验收结果分别发布，入口地址来自安装包内的 `WEBSITE_BASE_URL`，与后端 `WEBSITE_BASE_URL` 必须属于同一服务环境。
2. **发布演练**：`python3 scripts/test-release-rehearsal.py --output <新目录>` 现在使用三个普通 API 二进制——产生旧数据的旧版本（缺省 `67e7b37`，无邮箱验证）、当前代码、回滚目标（缺省 `c84de59`，即第 3 轮，已强制验证与密码版本）——并在浏览器阶段以 Go 测试装配 `TestReleaseRehearsalFixture` 提供捕获收件箱（普通二进制只信任系统证书库，无法向本机测试 SMTP 投递）。`summary.json` 的 `identical_binaries` 会如实列出相同的二进制：后端生产代码未变的版本与回滚目标相同时，回滚步骤只是同一二进制在新数据上的重启；需要跨版本证据时加 `--rollback-ref 6bc0742`（第 2 轮）。
3. **回滚目标的选择**：演练与正式回滚都只回到已包含强制邮箱验证的版本；回到 `67e7b37` 或更早会重新开放无验证码注册（见第 2 轮第 4 条）。

## 节点组与在线状态的数据基础（handoff_node_use_history_and_load_balancing_c.md 第 1 轮）

本轮只建立数据模型、迁移、配置与维护任务骨架；**客户端、网站与管理端可见行为不变**（目录、配置、管理列表与升级前逐字节一致）。在线设备、设备上限、流量历史和节点组分配在后续轮次接入。

1. **迁移**：新增 `node_groups`、`connection_leases`、`node_assignments`、`usage_buckets`、`node_health_events` 五张表；`nodes` 新增可空 `group_id`（外键，组内有服务器时不能删除组）、`max_connections`（默认 1000，范围 1–10000）、`label`（默认空）、`unhealthy_until`（可空）。迁移在原有 advisory lock 708614921 事务内，为每台尚无组的服务器建立**同 ID** 的单服务器组（决策 D3），复制名称、地区、线路、倍率、启用、版本和 `node_configs.plan_ids`，然后回填 `group_id`。已有组的服务器跳过，可重复执行；不修改订阅、会话、账本，也不修改既有 `nodes.version`。生产 `AUTO_MIGRATE=false`：先备份，再在维护窗口执行一次 `bin/api.next -migrate`。
2. **容量初值**：迁移后所有服务器 `max_connections=1000`（相当于不限，避免上线即锁死用户）。本轮还不按容量拒绝接入；节点组管理界面上线后（第 4 轮）再由管理员改为真实容量。
3. **配置**：新增 `CLIENT_MAX_ONLINE_DEVICES`（默认 2，1–100；有效上限 = min(订阅 `max_devices`, 此值)）、`NODE_HEALTH_FAILURE_USERS`（默认 3，2–50）、`NODE_HEALTH_COOLDOWN_SECONDS`（默认 300，60–3600）。越界或非数字拒绝启动并只报告变量名。在线租约时长复用 `CLIENT_LEASE_SECONDS`，续期周期复用 `CLIENT_REPORT_INTERVAL_SECONDS`。本轮这三项只被加载和校验，尚未影响任何请求。
4. **维护循环**：API 进程内新增维护循环：每 15 秒结束过期在线租约、每 5 分钟分批删除 24 小时窗口之外的流量历史、每小时清理 1 小时前的健康事件、24 小时前结束的租约和 30 天未用的固定分配。每次扫描以 `pg_try_advisory_xact_lock(708614923)` 保证多实例同一时刻只有一个实例执行，其余实例跳过；所有删除和更新都有单批上限。失败只记录“will retry”，不输出数据库细节。
5. **管理端过渡双写**：本轮管理端仍按服务器保存；保存时同步维护该服务器的单服务器组（新服务器建同 ID 组，修改公开属性时组版本递增，仅改 YAML 不递增），删除最后一台服务器时删除其空组。
6. **检查**：`bin/doctor` 的 `missing_tables` 覆盖五张新表；新增 `node_groups`（组数量）与 `ungrouped_servers`（无组服务器数）。`ungrouped_servers > 0` 时给出 `servers_without_group_run_migration` 警告，表示有服务器在上次迁移后由旧二进制写入，需要再执行一次 `-migrate`。
7. **演练与回滚**：`python3 scripts/test-node-groups-migration.py --output <新目录>` 用 `a63a882` 的普通二进制经其自身 API 产生数据，当前二进制迁移两次并逐行比对，比较新旧二进制的客户端目录、配置、网站目录与管理列表，再用旧二进制在迁移后的库上启动、读写节点，最后再次迁移确认收编。回滚到 `a63a882`：旧二进制忽略新表和新列（新列有默认值或可空，旧版插入服务器仍然有效）；不要删除新表。回滚期间旧版新增的服务器会在下次 `-migrate` 时自动建组；旧版对已有服务器的属性修改不会写回组，再次升级时以组表为准（handoff 第 14 节）。

## 在线租约、设备上限与流量历史（handoff_node_use_history_and_load_balancing_c.md 第 2 轮）

本轮起后端按真实连接统计在线设备，并在接入时执行设备上限；网站仪表盘界面在第 4 轮接入，四平台新 Core 在第 5、6 轮接入。

1. **迁移**：`connection_leases` 新增 `idempotency_key` 列（默认空）与三个索引（`idx_lease_idempotency`、`idx_lease_grant`、`idx_lease_session_latest`），附加迁移，照常先备份再执行一次 `bin/api.next -migrate`。
2. **行为变化（发布前须知）**：设备上限从“已绑定线路且 180 秒内活跃的会话”改为“持有有效在线租约的设备”，有效上限 = min(订阅 `max_devices`, `CLIENT_MAX_ONLINE_DEVICES`，默认 2)。因此发布后 pro/max 订阅的同时在线上限立即变为 2；需要放开时调高 `CLIENT_MAX_ONLINE_DEVICES` 并重启 API。上限调低不踢出已在线设备，只拒绝新接入。
3. **旧版 APP**：无需升级即生效。旧 Core 在“选择线路”（`GET /config`）时建立租约，连接期间由每 60 秒的流量上报续期；断开或被杀后最长 `CLIENT_LEASE_SECONDS`（90 秒）让出名额；暂停后再次连接时由首次上报重新接入。选择线路后若 90 秒内未连接，该选择暂时占用名额直至过期。
4. **容量**：本轮已按服务器 `max_connections` 拒绝超额接入（`group_full`）；迁移值为 1000，相当于不限，节点组管理界面上线（第 4 轮）前不要手工调低。
5. **新接口**：原生 `POST /api/v1/client/connections` 与 `POST /api/v1/client/connections/:id/release`（新 Core 使用）；网站 `GET /api/v1/me/devices`、`GET /api/v1/me/usage-history`；`GET /api/v1/me/dashboard` 的 `metering_connected` 改为“24 小时内有计量数据”，并新增 `online_devices`、`device_limit`。网站接口只返回平台、版本、设备 UUID 后 6 位、线路组名和时间/字节数，不返回服务器、会话、租约 ID 或地址。
6. **流量历史**：每次入账在同一事务内写入 5 分钟汇总桶，维护循环按第 1 轮规则删除 24 小时窗口之外的行；计费账本不受影响。
7. **`CLIENT_MIN_APP_VERSION`**：从仅作用于 `/grants` 扩展到 `/config` 与 `/connections`。在四平台新 Core 发布之前设置它会让旧 APP 无法选择线路，请保持为空。
8. **回滚**：回滚到第 1 轮二进制时新列与表被忽略，设备上限回到旧语义；租约行保留，维护循环（第 1 轮已有）照常过期与清理。不要删除新列或表。

## 节点组分配器、负载均衡与管理端组接口（handoff_node_use_history_and_load_balancing_c.md 第 3 轮）

本轮没有新表、新列或新配置项，**无需迁移**；`-migrate` 照常可重复执行。网站管理页的节点组界面在第 4 轮接入，本轮现有 `/admin/nodes` 页面继续可用。

1. **目录改为节点组**：客户端目录、网站 `/nodes` 与 `/client/bootstrap` 列出节点组（组名、地区、线路、倍率、组版本）。组可见需：组启用、套餐在允许列表内（空表示所有套餐）、专线组要求订阅允许专线、组内至少一台启用的服务器（production 下须受 Agent 管理）。下发的配置把代理名改写为组名，同组多台服务器对用户完全一样。迁移得到的“一组一台”与升级前同名同 ID；**发布后首次请求时，若第 1、2 轮期间有“只改 YAML”的服务器，目录标签会变化一次**（组版本与服务器版本不再相同），在线客户端收到一次 `profile_changed` 并重新获取配置。目录展示顺序由 Go 侧按（排序、名称、ID）字节序排列，库的排序规则若不是 C，含大小写混排或非 ASCII 名称的目录修订号也会在发布后变化一次。
2. **分配规则**：接入（`/connections`、`/grants`、旧 Core 的 `/config`）在节点组行锁内选服务器：本设备已在线的服务器 → 用户在该组的固定服务器（健康且未满即用，不比较负载）→ 负载率（在线数/`max_connections`）最低者，相同则均匀随机 → 只有没有健康且未满的服务器时才用冷却中的服务器。全组满载返回 409 `group_full`；没有可用服务器返回 503 `group_unavailable`（旧 Core 的 `/config` 收到它已认识的 `client_config_unavailable`）。Agent 管理的组只在 Agent 在线、支持该协议的服务器中分配。
3. **故障与重新分配**：停用、删除或移组的服务器上的在线租约在同一事务结束（`node_unavailable`），新 Core 收到 `connection_reassigned`，旧 Core 收到 `profile_changed` 后重新 `/config`，都被分到同组其他服务器；固定分配随之更新。停用或删除整组则结束组内全部租约（`group_unavailable`），线路从目录消失，不提示重连。容量调低不踢出在线设备，管理列表显示 `over_capacity`。
4. **软冷却**：释放请求（`/connections/:id/release`，以及 `/grants/:id/revoke` 新增的可选 `reason`）带 `tunnel_unhealthy` 或 `tunnel_failed` 时记录健康事件。5 分钟内 `NODE_HEALTH_FAILURE_USERS`（默认 3）个**不同用户**报告同一服务器时，该服务器进入 `NODE_HEALTH_COOLDOWN_SECONDS`（默认 300 秒）冷却，到期自动恢复；冷却只影响新分配，不踢出在线设备。管理员可调用 `POST /api/v1/admin/nodes/:id/health` 立即清除（同时删除该服务器的健康事件，记审计 `node.health_cleared`）。
5. **管理端接口**：`GET /api/v1/admin/node-groups`（组与服务器摘要、计量方式、混合计量警告、在线/容量合计，在线数由一次聚合查询得到）；`POST /api/v1/admin/node-groups`、`POST /api/v1/admin/node-groups/:id`（乐观版本、同套餐范围重名拒绝 `node_group_name_exists`、审计 `node_group.saved`）；`DELETE /api/v1/admin/node-groups/:id`（只删空组，`node_group_not_empty`）。`POST /api/v1/admin/nodes` 新增“分组”请求体：`yaml`、`group_id`、`max_connections`（1–10000）、`label`（≤40 字）、`enabled`、`version`，批量导入的每个代理成为该组的一台服务器；服务器名不再全局唯一。旧的按服务器请求体（地区/线路/倍率/套餐）保留到第 4 轮，只能编辑“一组一台”的服务器（否则 409 `node_group_managed`），重名检查改为同套餐范围内的组名（仍返回 `node_name_exists`）。服务器版本只在 YAML、启用状态或所属组变化时递增；改容量、备注不影响任何会话。保存组时把公开属性同步写入其服务器的旧列与 `node_configs.plan_ids`（过渡双写，服务器版本不变）。
6. **合并同类服务器**：先在目标组导入或用分组请求体把服务器的 `group_id` 改为目标组（其上的设备会改连同组其他服务器），再删除已空的旧组。删除由旧接口产生的“一组一台”服务器时，其同 ID 组随之删除；用组接口建立的组保留到单独删除。
7. **回滚**：回滚到第 2 轮二进制时组的多服务器语义消失：旧二进制按服务器列出目录，同组的多台服务器会以同名的多条线路出现，并且旧二进制仍按服务器名全局唯一检查新保存。回滚前若已合并服务器，用户会看到重名线路，这一点属于已知取舍（handoff 第 14 节）。

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
