# 认证邮件基础（注册验证码 / 找回链接）

> 对应 [handoff_auth.md](../handoff_auth.md) 第 1 轮。实现位于 `backend/internal/authmail`，配置位于 `backend/internal/config/mail.go`。注册与找回的 HTTP 接口在第 2、3 轮接入。

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
