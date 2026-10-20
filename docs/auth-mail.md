# 认证邮件基础（注册验证码 / 找回链接）

> 对应 [handoff_auth.md](../handoff_auth.md) 第 1 轮。实现位于 `backend/internal/authmail`，配置位于 `backend/internal/config/mail.go`。第 2 轮接入了注册接口（见下方“注册接口”），第 3 轮接入找回与重置接口（见下方“找回与重置接口”）。

## 配置清单

| 变量 | 缺省 | 规则 |
| --- | --- | --- |
| `ALLOW_REGISTRATION` | `false` | 严格布尔；`true` 时必须具备完整 SMTP、`WEBSITE_BASE_URL`、`AUTH_MAIL_SECRET`，否则拒绝启动 |
| `SMTP_HOST` / `SMTP_USER` / `SMTP_PASS` | 空 | 三项全空 = 不启用邮件；部分填写 = 拒绝启动。主机为纯主机名；`SMTP_USER` 为完整发件邮箱（同时作为 From）；`SMTP_PASS` 为授权码，原样使用、从不打印 |
| `SMTP_PORT` | `465` | 1–65535，始终校验格式 |
| `SMTP_SECURE` | `true` | `true` 隐式 TLS；`false` 强制 STARTTLS（无 STARTTLS 即失败），从不明文 |
| `SMTP_FROM_NAME` | `AsterLink VPN` | 1–64 字符，拒绝控制字符；由邮件库按 RFC 2047 编码 |
| `WEBSITE_BASE_URL` | 空 | 根 origin；拒绝用户信息、查询、fragment、路径；生产仅 HTTPS，HTTP 仅限 loopback |
| `AUTH_MAIL_SECRET` | 空 | 标准 Base64，解码后至少 32 字节；不得等于 JWT/节点密钥 |
| `REGISTRATION_ALLOWED_EMAIL_DOMAINS` | 10 个默认域名 | 精确域名、去重、转小写；显式设为空、通配符、尾点、非 ASCII 均拒绝启动 |
| `TRUSTED_PROXIES` | 空 | IP/CIDR 列表；只有这些对端的 `X-Forwarded-For` 被采用，其余一律使用 TCP 对端地址 |

启动错误只包含变量名与错误类别。`GET /api/v1/meta` 新增 `allow_registration`、`registration_email_domains`、`password_reset_enabled`、`verification_code_ttl_seconds`、`verification_resend_after_seconds`，不包含任何 SMTP、网址或密钥信息。

## 注册接口（第 2 轮）

均经网站同源 `/api/v1` 代理访问，严格 JSON（拒绝未知字段，请求体 ≤ 8 KiB），响应 `Cache-Control: no-store`，沿用现有 Origin/Cookie 规则和认证入口 IP 限流。

| 接口 | 请求 | 成功 |
| --- | --- | --- |
| `POST /auth/register-code` | `{"email"}` | 202：`message`（固定通用提示）、`verification_id`（随机 UUID）、`expires_at`、`next_send_at`（UTC）、`expires_in_seconds`、`resend_after_seconds`（服务端计算的相对秒数，网站倒计时只用这两项）。已注册邮箱返回完全相同形态，只是不发邮件 |
| `POST /auth/register` | `{"name","email","password","remember","verification_id","code"}` | 201：`{"user"}` 与原有 `vpn_access`/`vpn_refresh` Cookie；账户 `email_verified_at` 写入、角色固定 `user`、状态 `active`、不赠送套餐 |

检查顺序：发码为“格式 → 域名白名单 → 注册开关 → 邮件能力 → 共享限流”；注册为“开关 → 姓名（2–40 个 Unicode 字符）与密码（10–72 UTF-8 字节）→ 白名单（按当前配置重查）→ 验证码格式 → bcrypt（事务外）→ 邮箱协调行锁内校验并在同一事务中建号、建会话、消费挑战”。

| 状态 / 错误码 | 含义 |
| --- | --- |
| 400 `invalid_json` | 未知字段（如 `role`、`status`、`verified`、`user_id`）、`code` 不是字符串等 |
| 400 `invalid_email`（发码）/ `invalid_registration`（注册） | 邮箱、姓名或密码格式不合法 |
| 400 `email_domain_not_allowed` | 不在 `/meta` 的 `registration_email_domains` 中（发码与注册两处都查） |
| 400 `verification_required` / `invalid_verification_code` | 未带 `verification_id`；`code` 不是恰好 6 个 ASCII 数字（不计入试错） |
| 400 `verification_invalid` / `verification_expired` / `verification_used` / `verification_locked` | 不匹配（含 ID 与邮箱不对应）/ 过期或已被新验证码取代 / 已使用 / 单挑战 5 次错误已锁定 |
| 403 `registration_disabled` | 注册关闭或邮件未配置 |
| 409 `email_exists` | 验证码已正确核对，但该邮箱已有账户 |
| 429 `rate_limited` + `Retry-After` | 发送冷却、邮箱/IP/全局额度，或同邮箱 30 分钟验证码错误预算用尽 |
| 503 `mail_unavailable` / `auth_unavailable` | 邮件队列满或不可用 / 数据库等暂不可用 |

同一验证码并发提交只会成功一次，其余得到 `verification_used`；成功后响应丢失再重试同样是 `verification_used`，网站提示直接登录。

## 找回与重置接口（第 3 轮）

约束同上（严格 JSON、no-store、Origin/Cookie 规则、认证入口 IP 限流）。三个接口都不触发网站的自动刷新。

| 接口 | 请求 | 成功 |
| --- | --- | --- |
| `POST /auth/forgot-password` | `{"email"}` | 202：`message`（固定通用提示）、`next_send_at`、`resend_after_seconds`。已注册、未注册、已禁用账户的响应完全相同；只有存在且 active 的账户收到链接。按历史身份规则规范化，不套用注册白名单；与注册共用邮箱冷却和额度 |
| `POST /auth/reset-password/validate` | `{"token"}` | 200：`valid`、`expires_at`、`expires_in_seconds`、`password_min_bytes`（普通用户 10、管理员 16）、`password_max_bytes`（72）。只读，可重复调用，不消费 token |
| `POST /auth/reset-password` | `{"token","password","password_confirmation"}` | 200：`password_reset`、`message`；清除发起浏览器的 `vpn_access`/`vpn_refresh`；**不创建新登录会话** |

链接为 `WEBSITE_BASE_URL + /reset-password#token=<43 位 URL 安全字符>`。token 在 fragment 中，浏览器打开、预取、邮件扫描器的 GET/HEAD 都不会把它发到服务器；网站页面读到内存后立即用 `history.replaceState` 从地址栏移除，之后只出现在上述两个 POST 的请求体中。

`reset-password` 的顺序：解析 token（只读）→ 两次密码一致 → 字节规则（按账户角色）→ 与当前密码不同（bcrypt，事务外）→ 计算新哈希（事务外）→ 单一事务 `CompleteReset`：

1. 锁 User，确认 active、邮箱未变、`auth_version` 仍是签发链接时的版本；锁挑战，确认 active、已投递、未过期、属于该用户与版本；
2. 写新哈希、`password_changed_at`、`auth_version + 1`，`email_verified_at` 为空时补齐（持有邮件链接即证明邮箱）；
3. 消费本 token，作废该账户其他未完成的重置挑战并取消其未发邮件；
4. 撤销该用户全部网站 `sessions` 与客户端 `native_sessions`（按主键顺序加锁）；
5. 写 `admin_audits`（动作 `auth.password_reset`，Actor/Target 为该用户，Version 为新 `auth_version`），排入“密码已修改”通知（无 challenge，通知投递失败不影响已提交的改密）。

任一步失败整体回滚（503 `auth_unavailable`），token 仍可用。字段错误不消费 token。

| 状态 / 错误码 | 含义 |
| --- | --- |
| 400 `invalid_json` | 未知字段、类型不符 |
| 400 `invalid_email` | forgot 的邮箱格式不合法 |
| 400 `reset_token_invalid` | 缺失、格式错误、不存在、过期、已使用、被新链接取代、账户已禁用或密码已被改过——统一一个答案 |
| 400 `password_mismatch` / `invalid_password` / `password_unchanged` | 两次不一致 / 字节数不合规则 / 与当前密码相同 |
| 429 `rate_limited` + `Retry-After` | 邮箱冷却与额度、IP/全局额度（forgot） |
| 503 `mail_unavailable` / `auth_unavailable` | 邮件未配置或队列满 / 数据库等暂不可用 |

### 会话撤销与登录竞态

- 已撤销的网站会话：下一次 `auth/me` 等请求 401，刷新令牌 401 `session_expired`（沿用原契约）。网站在受保护请求经一次刷新仍 401 时统一清除用户态，受保护页面（总览、订单、节点、管理员编辑器）随之卸载，套餐页的购买确认框关闭。
- 已撤销的客户端会话：`/client/*` 全部 401 `native_session_expired`，四平台 Core 已把它作为会话终止处理；旧 token 的流量报告不会入账，撤销前已提交的报告保持不变。
- 网站登录与客户端登录在 bcrypt 校验后，于创建会话的事务中锁 User 并复核 `auth_version` 与哈希；期间若已重置则返回 401 `invalid_credentials`，不会创建旧密码会话。网站刷新改为“事务外定位候选 → 锁 User → 锁 Session 复核”，与重置的锁顺序一致。

### 四平台 APP（第 4 轮）

- 登录页“注册账号”“忘记密码”只打开 `WEBSITE_BASE_URL` 下的 `/register`、`/forgot-password`（macOS/Windows 系统浏览器、Android Custom Tabs、iOS SFSafariViewController）；APP 内没有邮箱、验证码或重置逻辑，也不做重置后自动登录。
- 网站重置后，在线 APP 在下一次 Core 会话检查、目录轮询或流量上报得到 `native_session_expired` 时结束会话；至多一次保存密码静默重登，遇到 `invalid_credentials` 即停止并要求手动输入。
- 保存密码只在“仍等于刚被拒绝的那个密码”时删除：手动输错不会删掉正确的保存项，另一处登录刚保存的新密码也不会被旧结果删除；邮箱与设备 UUID 保留。启动时的自动登录与过期后的静默重登在读取凭据期间若用户已手动登录、切换账号或退出，则放弃，不会覆盖用户的新操作。

## 数据库变更（附加迁移）

| 对象 | 内容 |
| --- | --- |
| `users` | `email_verified_at`（可空，旧账户保持空）、`password_changed_at`（可空）、`auth_version`（非空，默认 1）；公开 JSON 不变 |
| `email_auth_challenges` | 用途、规范化邮箱、可空用户 ID、HMAC 摘要（`(purpose, digest)` 唯一）、代次、绑定的 AuthVersion、是否投递、状态、失败次数、签发/过期/消费时间；部分唯一索引 `idx_auth_challenge_active (purpose, email) WHERE status='active'` |
| `auth_mail_jobs` | 可空挑战 ID、模板、脱敏收件提示、AES-256-GCM 密文载荷（绑定任务 ID + 模板，终结即擦除）、状态、尝试次数、下次尝试、租约（到期/持有者/版本）、固定 Message-ID、脱敏失败类别 |
| `auth_rate_buckets` | 主键 `(scope, key, window_start)`；key 为 HMAC 摘要，不存原始邮箱或 IP；`email_coord` 为跨时间窗的邮箱协调行（承载 60 秒共享冷却） |

三张表之间及与业务表之间没有外键，清理不可能级联到账户、订单、订阅或流量记录。

## 防滥用默认值

邮箱冷却 60 秒、每小时 5 次、每天 10 次（注册与找回共享，存在与否都记账）；单 IP 每小时 20、每天 100；全局每分钟 60；待投递 ≥ 1000 返回服务不可用；单挑战 5 次错误锁定；同邮箱 30 分钟最多 5 次验证码错误。全部在 PostgreSQL 中原子计数，多实例与重启共享。

## 发送 worker

每实例 2 并发；单次投递 20 秒硬超时（连接 I/O 截止时间）；租约 45 秒（`FOR UPDATE SKIP LOCKED` 领取，写回同时匹配持有者+版本+`sending` 状态）；最多 3 次，退避 15 秒、60 秒并加最多 20% 抖动；SMTP 5xx 与 TLS 校验失败直接终结；凭证临近过期（1 分钟内）、已被新申请作废或账户 AuthVersion 已变化的任务取消不投递。认证使用 `AUTH PLAIN`，只在 TLS 连接上执行。

## 模板预览（合成数据）

HTML 版本可本地生成：`AUTHMAIL_PREVIEW_DIR=/tmp/authmail-preview go test -run Preview ./internal/authmail`（在 `backend/` 目录执行）。纯文本版本如下。

```text
Subject: AsterLink VPN 注册验证码

您好：

您正在注册 AsterLink VPN 账户，本次邮箱验证码为：

    012345

验证码 10 分钟内有效，仅可使用一次。如果您多次申请，请只使用最新一封邮件中的验证码。

如果这不是您本人的操作，请忽略本邮件，无需任何处理。请勿将验证码告诉任何人。

AsterLink VPN（app.example.com）
```

```text
Subject: AsterLink VPN 重置密码

您好：

我们收到了重置 AsterLink VPN 账户密码的申请。请在 30 分钟内打开以下链接设置新密码（链接仅可使用一次）：

https://app.example.com/reset-password#token=SYNTHETIC-PREVIEW-TOKEN-NOT-USABLE

请确认链接属于 app.example.com。如果您多次申请，请只使用最新一封邮件中的链接。

如果这不是您本人的操作，请忽略本邮件，您的密码不会改变。

AsterLink VPN
```

```text
Subject: AsterLink VPN 密码已修改

您好：

您的 AsterLink VPN 账户密码刚刚已被修改，所有设备上的原有登录均已退出，请使用新密码重新登录。

如果这不是您本人的操作，请立即通过 app.example.com 的“忘记密码”重新设置密码。

AsterLink VPN
```

HTML 版本使用 `html/template` 转义全部变量，不含脚本、跟踪像素、外部图片或第三方统计。
