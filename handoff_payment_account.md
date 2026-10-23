# AsterLink VPN：支付网关、账号设置与管理后台技术规划交接书

> 文件：handoff_payment_account.md  
> 编写日期：2026-10-22  
> 代码基线：项目 /Users/stevenlee/Desktop/vpn，main，HEAD 9ee9066b67c8eb2ccc14f3ee9e930656a1562f7b（node groups 第 7 轮合并后）。以本次读取的工作区核心源码为准。  
> 外部依据：根目录《支付接入技术文档.md》（网关 v2.0.0，2025-12-05）。  
> 文档性质：实施规划与验收约定。本文不代表任何功能已开发或已测试，全文不含代码示例。

## 1. 目标、范围与轮次总览

本次在既有架构上新增三块能力：

1. 网站接入第三方支付网关，用户可选微信支付或支付宝支付，并可输入优惠码（可留空）。管理员可以在 .env 中开启测试价。
2. 用户控制台新增“账号设置”，用户可修改密码、删除账号。
3. 新增独立的管理员后台，与现有“节点管理”页面分开。后台包含用户管理、订单与支付、登录记录、优惠码、公告和模拟登录。

架构不变：客户端鉴权、计费、流量统计并通过 API 同步，节点、目录、租约和计量账本沿用现有实现，本规划不再讨论这一点。支付与账号功能只在后端和网站实现。四个 APP 继续通过网站购买（Android AppModel 的 WebPage.Plans、iOS、macOS、Windows 均已打开网站 /plans），本次不修改 APP 代码。APP 侧只验收兼容行为，例如付款到账后的续期、改密或删号后的会话失效（见第 14 节第 6 轮）。

采用 **6 轮**实施，每轮一个 worktree 分支和一个 PR。轮次记录追加到本文第 18 节，延续 handoff_auth.md 和 node groups 交接书的做法。

| 轮次 | 可验收结果 | 依赖 |
| --- | --- | --- |
| 第 1 轮 | 支付后端闭环：配置、订单扩展、网关适配、下单、回调、对账、履约、测试价、优惠码数据与计算，以及优惠码管理 API；全部用假网关验证 | 现有 Go/PG |
| 第 2 轮 | 网站支付体验：支付方式选择、优惠码输入、跳转网关、超时与返回网站、结果页、订单页改造；用本地模拟网关做浏览器全流程测试 | 第 1 轮 |
| 第 3 轮 | 账号设置：修改密码、删除账号（两阶段清除管线）；登录记录采集与 IP 归属地异步解析；注册 IP 记录 | 第 1 轮迁移框架 |
| 第 4 轮 | 管理后台 I：独立后台框架与权限、用户列表/筛选/详情、改密、改套餐、删除用户、订单与支付面板、登录记录面板 | 第 1、3 轮 |
| 第 5 轮 | 管理后台 II：优惠码管理界面、公告（编辑、发布、强制确认弹窗、确认人数）、模拟登录与返回管理员 | 第 4 轮 |
| 第 6 轮 | 全链路回归、安全审查、部署文档与用户实测清单、四平台兼容、发布与回滚演练 | 前 5 轮 |

网关没有测试环境。开发方按文档实现，并测试所有能在本地验证的部分（本地模拟网关、签名、回调、对账、并发与篡改）。**真实付款测试由用户部署后执行**，使用第 15.2 节的清单，结果记入第 18 节。

本期不做：网关退款 API（文档中没有）、发票、余额/钱包、自动续费、跨套餐升级补差、APP 内支付、APP 内公告、管理员 MFA、Redis 或外部队列。

## 2. 已核实的代码基础与必须衔接的位置

### 2.1 后端

| 位置 | 当前事实 | 对本次的影响 |
| --- | --- | --- |
| [backend/internal/api/server.go](backend/internal/api/server.go) | 路由集中注册；/api/v1 整组套用进程内 IP 限流（API_REQUESTS_PER_MINUTE）；JSON 严格解析、拒绝未知字段；请求体默认 8 KB，仅 /admin/nodes 放宽到 128 KB；非 GET 必须是 application/json，带 Cookie 的写请求必须带可信 Origin；Gin 只信任 TRUSTED_PROXIES 提供的 X-Forwarded-For | 网关回调是无 Origin、无 Cookie 的 JSON POST，可通过现有 CSRF 中间件，但不能套用通用 IP 限流，要单独挂载。公告正文需要单独放宽请求体上限（Go 与 Next.js 代理两处） |
| [backend/internal/api/admin_nodes.go](backend/internal/api/admin_nodes.go) | requireAdmin 除了检查 role=admin，还要求开启 CLIENT_NODE_CATALOG_ENABLED | **新后台不能复用 requireAdmin**，否则未开节点目录时整个用户管理不可用。要新建只检查角色和会话类型的管理员中间件 |
| [backend/internal/api/auth.go](backend/internal/api/auth.go) | 网站登录：bcrypt 在锁外校验，lockVerifiedUser 在 User 行锁下确认 AuthVersion 和哈希；Session 表存刷新令牌哈希；requireAuth 每次联表确认会话有效 | 修改密码复用这套“锁外 bcrypt、锁内复核”。模拟登录会话也存在 Session 表，requireAuth 需要识别会话类型。登录记录在这里接入 |
| [backend/internal/api/native.go](backend/internal/api/native.go) | APP 登录创建 NativeSession，同样走 lockVerifiedUser；会话撤销后客户端得到 native_session_expired | 登录记录也要覆盖 APP 登录。改密、删号、管理员改密都必须撤销 NativeSession |
| [backend/internal/authmail/reset.go](backend/internal/authmail/reset.go) | RevokeAllSessions 按主键顺序撤销该用户的全部 Session 与 NativeSession；CompleteReset 写 AdminAudit（auth.password_reset）并调用 EnqueuePasswordChanged | 改密、管理员改密、删号直接复用 RevokeAllSessions。邮件已配置时复用改密通知 |
| [backend/internal/api/commerce.go](backend/internal/api/commerce.go) | testPurchase：Idempotency-Key 必须是 UUID；在 User 行锁下写 Subscription 和 Order；有效期内只能续购同一测试套餐；到期重开会重置额度；Order 状态 paid_test、provider test；allowTest 在生产恒为 false | 续期与新周期的写法要抽成公共履约函数，供真实支付、管理员改套餐、测试购买共用。现有测试购买保留为开发工具 |
| [backend/internal/model/models.go](backend/internal/model/models.go) | Order 对 User 的外键为 RESTRICT；PaidAt 是非指针时间；Subscription 每用户唯一；Plan 价格以分为单位（starter 1990、pro 3990、max 7990） | 删号不能依赖级联，要显式按依赖顺序删除。待支付订单的 PaidAt 要改成可空 |
| [backend/internal/model/native.go](backend/internal/model/native.go)、[grants.go](backend/internal/model/grants.go)、[presence.go](backend/internal/model/presence.go) | ClientTrafficReport 对 NativeSession、NodeUsageReport 对 ConnectionGrant 都是 RESTRICT；ConnectionGrant 与 SubscriptionPeriod 只有 user_id 索引、没有外键；ConnectionLease、NodeAssignment、UsageBucket、NodeHealthEvent 对 User 是 CASCADE | 删号清除必须先等未结清的 grant 结清，再按“报告 → grant → 周期 → 会话”的顺序删除（第 7.3 节） |
| [backend/internal/api/native.go](backend/internal/api/native.go) 的 nativeBound / nativeEntitlementFingerprint | 会话绑定 subscription_id、starts_at，指纹由 PlanID、IsTest、AllowDedicated、MaxDevices 组成；续期只改到期时间和总额度时不触发 subscription_changed | 同套餐续费不打断在线客户端；换套餐、开新周期、管理员改权益会使客户端重新授权。这是期望行为，要在验收中确认 |
| [backend/internal/api/grants.go](backend/internal/api/grants.go) | ensurePeriod / syncPeriod 维护 grant 模式的周期账本 | 管理员修改额度或已用量时，必须在同一事务内同步 SubscriptionPeriod，否则节点侧计量预算失真 |
| [backend/internal/model/node_config.go](backend/internal/model/node_config.go) | AdminAudit 只有 ActorID、Action、TargetID、Version | 后台的改套餐、模拟登录等操作需要非敏感细节，计划给 AdminAudit 增加一个可空的 JSON 文本列 Detail |
| [backend/internal/store/store.go](backend/internal/store/store.go) | 事务内 advisory lock 加 AutoMigrate，外加常量条件的部分索引；只增不删 | 新表和新列都接入这里。部分唯一索引不能使用当前时间作为条件 |
| [backend/internal/maintenance/maintenance.go](backend/internal/maintenance/maintenance.go)、[cmd/api/main.go](backend/cmd/api/main.go) | 维护循环用 advisory lock 保证单实例执行；authmail worker 与 grant 对账循环都随 API 进程启停 | 支付对账、删号清除、IP 归属地解析都采用同样的进程内循环和独立 advisory lock 键，不引入外部队列 |
| [backend/internal/operations/readiness.go](backend/internal/operations/readiness.go)、[cmd/doctor](backend/cmd/doctor/main.go) | doctor 校验固定的表清单 | 新表加入清单；增加支付配置检查，只报告类别，不输出密钥 |
| [backend/internal/config/config.go](backend/internal/config/config.go) | 生产模式禁止 TEST_PURCHASE_ENABLED、AUTO_MIGRATE、非安全 Cookie 和非 verify-full；WEBSITE_BASE_URL 已存在（邮件用） | 支付配置在这里加载并做生产约束。return_url 复用 WEBSITE_BASE_URL |

### 2.2 前端

| 位置 | 当前事实 | 本次落点 |
| --- | --- | --- |
| [frontend/src/components/site-shell.tsx](frontend/src/components/site-shell.tsx) | 导航为固定数组；管理员额外显示“节点管理”；workspace 路由白名单写死；侧栏写着“真实支付尚未接入” | 增加“账号设置”导航；管理员增加单独的“管理后台”入口（不并入节点管理）；模拟登录时显示常驻横幅，并把“退出登录”替换为“返回管理员”；公告弹窗挂在工作区外壳上；更新侧栏文案 |
| [frontend/src/components/plans-view.tsx](frontend/src/components/plans-view.tsx) | 只有测试购买弹窗；每次打开生成 Idempotency-Key；确认框随账户切换自动消失 | 改成正式购买弹窗：选择支付方式、输入优惠码、展示服务端报价、确认后跳转网关。PAYMENT_ENABLED=false 时保留原测试购买流程，避免既有 E2E 失效 |
| [frontend/src/components/orders-view.tsx](frontend/src/components/orders-view.tsx) | 状态只识别 paid_test；列名写“演示金额” | 支持待付款、已支付、已超时、待核对等状态，以及支付方式、实付金额、优惠和支付时间 |
| [frontend/src/app/api/v1/[...path]/route.ts](frontend/src/app/api/v1/[...path]/route.ts) | 路径段只允许字母、数字、下划线和连字符；拒绝上游重定向；请求体默认 8 KB；转发头白名单不含 user-agent；CLIENT_IP_HEADER 只转发一个可信 IP | 登录记录需要 UA，白名单增加 user-agent（截断后转发）。公告接口单独放宽请求体。不要为了支付把上游重定向放开，网关 URL 由 JSON 返回，浏览器自行跳转 |
| [frontend/src/components/providers.tsx](frontend/src/components/providers.tsx)、[frontend/src/lib/api.ts](frontend/src/lib/api.ts) | 用户态在内存；最终 401 触发 onSessionEnded；匿名接口列表禁用自动刷新 | /auth/me 增加模拟登录信息；返回管理员接口加入禁止自动刷新列表；删号、改密成功后主动清理用户态 |
| [frontend/src/components/admin-nodes-view.tsx](frontend/src/components/admin-nodes-view.tsx) | 通过 RequireAuth 和 AdminGate 做客户端门禁（只用于展示，真正的权限由 API 决定） | 新后台复用这种门禁模式，但做成独立路由和独立组件，并额外拒绝模拟会话 |
| [frontend/tests](frontend/tests)、[frontend/scripts](frontend/scripts) | Playwright（flow、register、reset、admin-nodes、dashboard-presence、user-nodes）与代理单测 | 新增支付、账号、后台、公告、模拟登录的 E2E；代理单测补充 UA 转发和请求体上限 |

## 3. 关键决策（默认值，用户可在第 1 轮开工前推翻）

| 编号 | 决策 | 理由 |
| --- | --- | --- |
| D1 | 支付只在网站完成，APP 继续打开网站 /plans | APP 已有入口，不需要四平台改代码 |
| D2 | 一个本地订单对应一个网关订单号 trade_no。网关订单 2 分钟有效、不可复用，超时后必须重新下单 | 网关规定 trade_no 唯一，重复提交会被拒绝 |
| D3 | 同一用户**允许同时有多笔待支付订单**。例如用户关掉 20 元的支付窗口后重新下单，两笔订单各有自己的 trade_no，网关会把后一笔调整为 20.01 元以示区分。每笔订单独立计时、独立回调、独立履约。为防止有人刷单占满金额空间，每人同时最多 5 笔活动订单（可配置），并且下单频率受限；同一 Idempotency-Key 的重放返回原订单 | 用户确认的行为；网关靠金额区分订单 |
| D4 | 回调来源 IP 在白名单内、且回调金额在应付金额和应付金额加容差之间（容差默认 0.50 元）时，才自动开通。低于应付、超过上限、找不到订单或状态不允许的，进入人工核对。**白名单为空时，所有回调都只进入人工核对，不自动开通**，网关 IP 可以从第一笔真实回调的流水中获得（第 6.8 节） | 网关签名不覆盖金额，回调也没有签名（第 6.1 节）；网关方可能无法事先提供 IP |
| D5 | 回调丢失、但网关状态查询显示 paid 的订单，进入待核对，由管理员在网关商户后台核实后人工确认。**仅凭状态查询不自动开通** | 状态接口不返回金额，无法排除被篡改金额的订单 |
| D6 | 有效期内只能续费同一套餐，续费会延长天数、累加流量；换套餐只能在到期后进行；有效的测试订阅（is_test）被真实付款覆盖时，开启新的正式周期。多笔待付订单先后到账时按到账顺序逐笔履约：同一套餐付两次就续两次；不同套餐的第二笔进入人工核对（由管理员线下退款，或在到期后手动开通） | 与现有规则一致，也与客户端会话指纹的行为一致 |
| D7 | 测试价只对 .env 列出的邮箱生效，生产环境强制要求名单非空；测试价订单生成正式订阅，但订单标记为测试价，统计时排除 | 测试价在生产环境走真实资金，避免全站误开 0.01 元 |
| D8 | 删号分两阶段：先立即停用并撤销会话，再由后台在 grant 结清后物理删除全部关联数据。只保留不含用户标识的匿名回调流水（订单号、金额、时间） | 满足“全部删除”；匿名流水不再关联账号，仅用于对账和处理纠纷 |
| D9 | 管理员不能对其他管理员执行模拟、改密或删除，也不能删除自己；网站上的自助删号对管理员账号关闭 | 防止权限横向滥用和误删最后一个管理员 |
| D10 | 模拟登录会话只读：除“返回管理员”和“退出”外，所有写操作都拒绝；不显示公告、不记录为用户登录、不更新最近登录时间 | 安全策略，同时满足“模拟时公告不显示” |
| D11 | 公告发布后内容锁定，只能下线或复制为新公告；只在网站显示 | 让确认人数统计有意义；APP 公告不在本期范围 |
| D12 | IP 归属地使用可配置的 HTTPS 公共接口并做缓存；内网和本机地址不外查；未配置时显示“未解析” | 满足异步、不影响登录性能的要求，接口供应商可替换 |
| D13 | 实付金额最低 0.01 元，不支持 0 元订单；优惠码不能把价格降到 0 | 网关要求金额为正数，同时少一条绕过网关直接开通的路径 |

## 4. 配置（backend/.env）

在 backend/.env.example 新增 Payment 分组，并同步 backend/DEPLOYMENT.md、docs/deployment.md 和 doctor。真实商户号、密钥只写在部署机的 backend/.env，不进 Git、前端环境变量、客户端包、日志或本文。

| 变量 | 默认 | 校验与用途 |
| --- | --- | --- |
| PAYMENT_ENABLED | false | 严格布尔。true 时下面的必填项缺一不可，否则拒绝启动；false 时下单接口返回 503 payment_disabled，网站保留旧的测试购买流程 |
| PAYMENT_GATEWAY_URL | https://pay.hyshentou.cn | 只接受根 origin，不带路径、查询或用户信息；生产必须 HTTPS |
| PAYMENT_CHANNEL | 空 | 商户号，只允许数字和字母、长度有限 |
| PAYMENT_SECRET | 空 | 网关密钥，拒绝 replace- 占位符和过短值；不打印，doctor 只报告“已配置/缺失” |
| PAYMENT_NOTIFY_URL | 空 | 回调的完整绝对地址，指向后端公网域名的 /api/v1/payments/notify；生产必须 HTTPS；不能带查询或片段 |
| PAYMENT_NOTIFY_ALLOWED_IPS | 空 | 网关出口 IP 或 CIDR，逗号分隔。为空时可以正常启动，但所有回调只进入人工核对、不自动开通，启动日志和 doctor 给出告警；填入后才开启自动开通 |
| PAYMENT_AMOUNT_TOLERANCE_CENTS | 50 | 0–500。网关每遇到一笔同金额的未超时订单就加 0.01，回调金额允许在应付到应付加容差之间。默认 0.50 元，可容纳同一金额同时约 50 笔待付订单 |
| PAYMENT_MAX_PENDING_PER_USER | 5 | 1–20。每人同时最多几笔活动中的待付订单；另外每人每 10 分钟最多下单 20 次 |
| PAYMENT_TEST_PRICE_ENABLED | false | 测试价开关。开启时启动日志输出醒目的 WARNING |
| PAYMENT_TEST_PRICE | 0.01 | 以元为单位、最多两位小数的正数，严格解析成分，范围 0.01 到 1000.00 |
| PAYMENT_TEST_PRICE_EMAILS | 空 | 测试价生效的邮箱名单，按既有邮箱规则规范化。生产开启测试价时必须非空；开发为空表示对所有人生效，并告警 |
| PAYMENT_PENDING_LIMIT_SECONDS | 180 | 本地“活动中待支付”窗口，120–600，必须不小于网关的 2 分钟 |
| GEOIP_LOOKUP_URL | 空 | IP 归属地接口模板（HTTPS），包含 IP 占位标记；为空则不解析 |
| GEOIP_LOOKUP_TOKEN | 空 | 可选的接口令牌，只放在请求头或接口要求的位置，不写日志 |
| GEOIP_REQUESTS_PER_MINUTE | 30 | 1–600，全局外呼速率上限 |
| IMPERSONATION_MINUTES | 30 | 5–120，模拟会话的硬性有效期，不可续期延长 |

启动规则：

1. 开启支付时，WEBSITE_BASE_URL 必须已配置（用于拼 return_url）。现有规则要求它在生产为 HTTPS。
2. 生产模式下 TEST_PURCHASE_ENABLED 仍被禁止，旧的模拟购买与本次的测试价是两件事：测试价走真实网关、真实扣款。
3. 修改配置要重启后端。多实例部署时，所有实例的支付配置必须一致。
4. /api/v1/meta 只增加公开字段：是否开启支付、可用支付方式（wxpay、alipay）、待支付窗口秒数。不暴露商户号、回调地址或测试价是否开启。测试价只在报价接口中、对名单内用户显示。

## 5. 数据模型与迁移

全部是增量迁移，接入 store.Migrate。不删列、不改旧数据含义。上线前在含旧数据的副本上验证锁等待和重复迁移是否安全。

### 5.1 现有表的附加字段

| 表 | 新增字段 | 说明 |
| --- | --- | --- |
| users | RegisterIP（可空）、LastLoginAt（可空）、LastLoginIP（可空）、DeletionRequestedAt（可空）；Status 新增取值 deleting | 旧用户的注册 IP 为空，界面显示“—”，不补造。为后台筛选增加 created_at、last_login_at 索引 |
| orders | TradeNo（可空、唯一）、PayType、ListPriceCents、DiscountCents、AmountCents（应付）、PaidCents（回调实付，可空）、CouponID（可空、无外键）、CouponCode（快照）、PriceSource（list / test_price / test_purchase）、ExpiresAt（可空）、ReviewReason、UpdatedAt；PaidAt 改为可空 | 旧订单的 PriceCents 保持原义。新订单把 PriceCents 写成应付金额，兼容旧前端读取。Postgres 唯一索引允许多个 NULL，旧订单不受影响 |
| sessions | ImpersonatorID（可空）、ImpersonatorSessionID（可空）、HardExpiresAt（可空） | 第 5 轮加入。非空表示模拟会话 |
| admin_audits | Detail（文本，默认空） | 只存非敏感的 JSON 摘要，例如“套餐 pro→max，到期时间 A→B”；禁止写入密码、令牌、完整 IP 列表或回调原文 |

订单状态集合：pending（待付款）、paid（已支付并已开通，二者在同一事务内完成）、expired（已超时）、failed（网关回报失败）、review（待人工核对，原因见 ReviewReason）、paid_test（旧的模拟订单）。前端只按这个集合映射文案，未知值原样显示。

### 5.2 新表

| 表 | 关键内容 | 约束 |
| --- | --- | --- |
| PaymentNotification | 随机 ID、trade_no、order_id（可空）、money 原始文本、trade_status、name、来源 IP、接收时间、处理结果（accepted / duplicate / unknown_order / ip_rejected / amount_mismatch / invalid / state_conflict） | 回调一进来就先写入。删号时把 order_id 置空，形成匿名流水（D8）。保留 180 天 |
| Coupon | 随机 ID、Code（规范化为大写，唯一）、备注、DiscountType（percent / amount）、PercentOff（1–99）或 AmountOffCents、PlanIDs（JSON，空数组表示全部套餐）、StartsAt、EndsAt、MaxUses（0 表示不限）、PerUserLimit（默认 1）、UsedCount、Enabled、DeletedAt（软删）、CreatedBy、Version、时间戳 | UsedCount 是只增的计数列，不由订单行推导，删号不会减少它。已被使用过的码只能软删 |
| LoginEvent | 随机 ID、user_id、Channel（web / native）、Platform、IP、UserAgent（截断到 200）、Result（success / failure）、FailureReason、OccurredAt、GeoStatus（pending / done / failed / skipped）、GeoText（≤120）、GeoResolvedAt | 索引：occurred_at、(user_id, occurred_at)、(geo_status, occurred_at)。保留 180 天，删号时一并删除 |
| IPGeoCache | IP（主键）、GeoText、ResolvedAt、ExpiresAt | 缓存 30 天，失败结果缓存 1 小时，避免反复外呼 |
| Announcement | 随机 ID、Title（≤100）、Body（≤5000 字符，纯文本）、Status（draft / published / archived）、PublishedAt、CreatedBy、Version、时间戳 | 第 5 轮加入 |
| AnnouncementRead | (announcement_id, user_id) 联合主键、AcknowledgedAt | 重复确认用 ON CONFLICT DO NOTHING，天然幂等。删号时删除 |

doctor 的表清单与 migrate_test 同步加入以上表。新表与 users 之间要么不建外键，要么在清除管线中显式删除，**不能依赖级联隐式删除财务相关数据**。

## 6. 支付网关接入与防绕过

### 6.1 网关事实与由此产生的风险（严格按文档）

| 文档事实 | 风险 | 本项目的对策 |
| --- | --- | --- |
| 下单方式是浏览器 GET /submit，参数为 channel、type、trade_no、notify_url、return_url、name、money、sign | 支付 URL 对用户完全可见 | 网关 URL 只由后端生成；前端拿到后立即整页跳转，不允许前端拼接任何参数 |
| sign 等于 MD5(trade_no + secret) 的小写十六进制，**不覆盖 money 等其他参数** | 用户可以保留 sign、改小 money 后自行提交（前提是抢在原 URL 之前用同一个 trade_no 提交） | 回调金额必须不低于应付金额（D4）。trade_no 用密码学随机数生成，事先不可预测。后端接口返回后前端立即跳转，缩短抢先提交的窗口 |
| 回调为 JSON POST（trade_no、name、money、trade_status），**没有任何签名** | 任何知道 trade_no 的人（包括用户本人）都能伪造“支付成功”回调 | 来源 IP 白名单（未配置时只进入人工核对，不自动开通）。即使通过白名单，仍要校验金额、订单状态和套餐可履约性。回调先落库再处理 |
| 状态查询 GET /api/order/status/{trade_no} 返回 pending / paid / timeout / not_found 和 expired，不返回金额 | 无法单凭状态查询确认实付金额 | 状态查询只用于对账、超时判定和界面提示。paid 但缺少可信回调时进入人工核对（D5） |
| 同一支付方式下金额相同的未超时订单会被自动 +0.01；微信和支付宝分别判重；超时的订单不再占用金额 | 实付金额可能高于应付；同一用户的多笔待付订单也会互相错开金额；有人大量下单可以把别人的金额推高 | 容差区间（默认 0.50 元），超出部分只记录、不退回；每人活动订单数和下单频率都有上限（D3） |
| 订单有效期固定 2 分钟，超时后网关显示超时页 | 用户错过时间窗口 | 本地窗口为 3 分钟（比网关多 1 分钟，用来接收迟到的回调）；超时后重新下单即可，无需等待旧订单 |
| return_url：付款成功后网关页面 5 秒内自动跳转；超时页上的“返回商户页面”按钮也跳转到这里 | 用户直接关闭窗口时不会发生跳转；跳转本身不代表付款成功 | 网站结果页只根据后端订单状态显示结果，见第 6.8 节 |
| 网关对同一笔收款的回调已做幂等，只回调一次 | 我方处理失败就没有第二次回调 | 回调接收处理必须快速且持久：先落库再处理；失败的订单由对账循环发现并转入核对 |
| 回调返回 HTTP 200 即可 | — | 返回 200 和简短 JSON，不向网关回显内部错误细节 |

网关没有测试环境，只有正式接口。开发阶段完全按文档实现，并用按文档行为编写的本地模拟网关测试；真实付款由用户部署后执行（第 15.2 节）。以下事项不再是开工前提，如果日后能从网关方了解到，再记入第 18 节用于加强防护：回调出口 IP、同一 trade_no 第二次提交的行为、是否会提供带签名的回调、回调失败是否重试。这些信息都不能用来放宽 D4 或 D5。

### 6.2 报价与下单

1. **报价**（只读、不预占）：用户选套餐，可选填优惠码。后端按顺序计算：标价 → 测试价替换（仅名单内用户）→ 优惠码折扣 → 不低于 0.01 元。返回各项金额、优惠码是否可用及原因、本次是续费还是新开、套餐能否购买（有效期内换套餐返回不可购买及原因）。
2. **下单**：请求带 UUID 格式的 Idempotency-Key、套餐、支付方式（只接受 wxpay、alipay）、可选优惠码。在一个事务内：锁 User，确认账户为 active 且当前不是模拟会话；按 Idempotency-Key 检查重放（同一个 key 但参数不同返回 409）；检查 D3 的活动订单数上限和下单频率（超出返回 429 和 Retry-After）；已有其他活动订单时照常下单，响应中附带提示“你还有 N 笔未完成的订单，请勿重复付款”；重新计算价格（**不信任报价接口的结果或前端传来的金额**）；如有优惠码，锁定该码所在行，校验有效期、适用套餐、启用状态、总次数（UsedCount 加上活动中待付订单数）和每人次数；生成 trade_no 并写入订单。
3. **trade_no**：20 位数字，由 8 位 UTC 日期加 12 位密码学随机数字组成；唯一索引冲突时重新生成，最多重试 3 次。
4. **生成网关 URL**：参数按文档逐个 URL 编码；name 使用服务端的套餐名快照，不使用任何用户输入；money 由整数分格式化为两位小数；notify_url 取配置；return_url 为 WEBSITE_BASE_URL 下的固定结果页，只带本地订单 ID。sign 按文档算法计算。URL 只在下单响应中返回，不写日志。
5. 下单接口返回订单摘要和网关 URL。前端用整页跳转打开（不用 iframe，也不开新窗口），因为网关页面的倒计时和成功后的跳转都依赖整页环境。

### 6.3 回调处理

1. 路由 POST /api/v1/payments/notify，单独挂载：不套用户鉴权和通用 IP 限流，改用宽松的独立限流；请求体上限 4 KB；严格 JSON；money 以数字原文读取，不经过浮点运算转换成分，最多两位小数，超出、负数、非数字或过大都判为 invalid。
2. 先写 PaymentNotification（含来源 IP），再判断。来源 IP 用 Gin 的 ClientIP，前提是 TRUSTED_PROXIES 与 Nginx 覆盖转发头的配置正确（docs/deployment.md“真实来源地址”一节）。白名单已配置但来源不在其中：记为 ip_rejected，不改订单，返回 200。白名单为空：继续做第 3 步的全部校验，但原本会自动开通的结果一律改为 review（unverified_source），并保留来源 IP 供管理员核实。
3. 按 trade_no 读订单（不加锁）→ 开启事务：锁 User → 锁 Order → 判断：
   - 订单已是 paid：记为 duplicate，返回成功（幂等）。
   - TRADE_FAILED：只把 pending 改为 failed，释放优惠码预占；其他状态不动。
   - TRADE_SUCCESS 且金额在容差区间内：订单状态为 pending 或 expired 时都履约（迟到但真实到账的钱照样开通），同一事务内写 PaidCents、PaidAt、优惠码 UsedCount 加 1、写订阅。如果履约规则不允许（例如期间已被管理员换成其他套餐），改为 review（state_conflict），不开通。
   - 金额低于应付或超过上限：改为 review（amount_mismatch），不开通。
4. 处理中出现数据库错误时返回 5xx，但不依赖网关重试，订单留给对账循环。
5. 任何情况都不能根据回调里的 name 或 money 去改订单的套餐或应付金额。

### 6.4 对账循环

进程内循环，每 30 秒一次，使用独立的 advisory lock。处理对象：超过本地窗口仍为 pending 的订单，最多每批 50 笔，同一订单查询间隔不小于 30 秒。

| 网关状态 | 本地处理 |
| --- | --- |
| timeout / not_found / expired=true | pending 改为 expired，释放优惠码预占 |
| pending（还在网关的 2 分钟内） | 下轮再查 |
| paid，但没有被接受的回调 | 改为 review（callback_missing），**不开通** |
| 查询失败 | 重试；超过 30 分钟仍无法确认时改为 review（gateway_unreachable） |

查询网关使用独立的 HTTP 客户端：超时 5 秒、校验证书、不跟随重定向、响应上限 4 KB、严格 JSON。结果页的订单查询接口在订单 pending 时也可以触发一次带限频的网关查询（每单至少间隔 10 秒），只用于界面显示“支付已完成，正在确认到账”，不会因此开通。

### 6.5 履约规则（公共函数）

把 commerce.go 里的续期和新周期逻辑抽成一个公共函数，统一由三方调用：真实付款、管理员改套餐、旧测试购买。

- 当前订阅有效、套餐相同、且不是测试订阅：到期时间加套餐天数，总流量加套餐流量，已用量不变，StartsAt 不变（客户端不会掉线）。
- 当前订阅已到期或不存在，或当前为测试订阅：开新周期，StartsAt 设为现在，额度重置，权益从套餐复制，IsTest 设为 false（客户端会收到 subscription_changed 并重新授权，这是预期行为）。
- 当前订阅有效、套餐不同：拒绝。下单阶段返回 409；履约阶段转入 review。
- 沿用现有上限：10 年有效期、2^50 字节流量。超出时拒绝或转入 review。
- 同一事务内调用 ensurePeriod/syncPeriod 同步 grant 周期账本。

### 6.6 测试价

测试价只影响价格计算，其他流程（网关、回调、对账、履约）与正式价格完全一致。订单记录 PriceSource=test_price，并同时保存标价，便于对照。管理员订单面板默认把测试价订单单独标出，统计收入时排除。报价接口对名单内用户明确返回“测试价”标记，前端显示黄色提示。关闭开关或把邮箱移出名单后，新订单立即恢复标价；已创建的订单保持原价。

### 6.7 优惠码计算

- 输入先去掉首尾空白再转大写；只允许字母、数字和连字符，长度 4–32。
- 百分比折扣按分计算并向下取整到分（对用户有利，不产生小数分）；固定金额折扣不超过“应付金额减 0.01 元”。
- 校验失败的原因只给用户四种：无效或已失效、不适用于此套餐、已达使用上限、你已使用过。不区分“码不存在”和“码已停用”，降低被枚举的价值。
- 防枚举：在 AuthRateBucket 中新建作用域，同一用户每小时最多 20 次无效尝试，超过返回 429 和 Retry-After。
- 预占即“活动中待付订单”：订单超时或失败自动释放；付款成功时 UsedCount 加 1。每人次数同时计入本人已付和活动中的订单，因此同一用户不能用多笔待付订单绕过“每人限用 1 次”：第二笔要么不带码，要么等第一笔超时。迟到的真实付款即使已超出上限也照常开通，后台显示“超额使用”提示。

### 6.8 支付页超时、跳转回网站与结果页（定义）

网关文档只规定了“成功后 5 秒自动跳转 return_url”和“超时页按钮跳转 return_url”，网站侧行为在此定义：

1. **return_url 的固定格式**：WEBSITE_BASE_URL 加 /pay/result，查询参数只有本地订单 ID（UUID）。不放金额、状态或 trade_no。下单时写入，不能由用户指定。
2. **用户可能遇到的四种路径**：
   - 扫码付款成功：网关显示成功，5 秒后跳到结果页。结果页通常已经看到“已到账”，否则显示“正在确认到账”并继续轮询。
   - 2 分钟内未付款：网关显示超时页，用户点击按钮回到结果页，结果页显示“订单已超时，未扣款”和“重新购买”按钮（返回套餐页，并预选原套餐和支付方式）。
   - 用户直接关闭网关窗口：不会发生跳转。订单在网关侧 2 分钟后失效，本地由对账循环在 3 分钟窗口结束后标为已超时，同时释放金额占用和优惠码预占。用户回到网站随时可以重新下单（D3），订单页里能看到这笔“已超时”的记录。
   - 超时之后才付款（例如扫的是旧二维码）：网关侧订单已失效，通常不会产生回调。如果仍收到可信回调，按 6.3 节对已超时订单照常开通。
3. **结果页的判断依据**：只看后端订单状态。地址栏参数、网关页面文字、“已经跳转回来”这件事本身，都不作为成功依据。
4. **轮询节奏**：每 2 秒一次，最长 5 分钟；页面不可见时暂停。5 分钟后仍未确定，显示“结果确认中，可稍后在订单记录中查看”。
5. **未登录或登录已过期时回到结果页**：先跳登录，登录后回到同一结果页；访问不属于自己的订单 ID 返回 404。

## 7. 账号设置

前端路由 /account（导航“账号设置”），包含三块内容：基本信息（只读：邮箱、注册时间）、修改密码、危险区（删除账号）。可选显示“我的最近 10 次登录”（时间、IP、归属地、渠道），复用第 8 节数据。

### 7.1 修改密码

- 请求包含当前密码和新密码。新密码长度沿用现有规则（普通用户 10–72 个 UTF-8 字节，管理员 16–72），必须不同于当前密码。
- 流程：锁外用 bcrypt 校验当前密码，并为新密码计算哈希 → 事务内锁 User，用 lockVerifiedUser 复核 → 写入新哈希、PasswordChangedAt，AuthVersion 加 1 → 调用 RevokeAllSessions 撤销全部网站会话和 APP 会话 → 为当前浏览器签发新会话 → 写审计 account.password_change → 邮件已配置时发送改密通知。
- 当前密码输错的次数用数据库计数：同一账户每 15 分钟最多 5 次，超过返回 429，防止被盗的会话被用来暴力猜原密码。
- 模拟会话中禁止；APP 会话全部失效，APP 的既有行为是提示重新登录。

### 7.2 删除账号（危险操作）

- 界面：红色危险区；二次确认对话框中列出将被删除的内容（套餐、剩余时长和流量、订单、设备会话、登录记录），要求输入当前密码，并手动输入完整邮箱；按钮倒计时 5 秒后才能点击。
- 前置条件：不是模拟会话；角色为普通用户（D9）；当前没有活动中的待付订单（否则提示“请等待约 N 秒，待订单失效后再删除”）。有效套餐不阻止删除，但必须明确提示“剩余权益不退款、不可恢复”。
- 第一阶段（同步、在一个事务内）：锁 User → 复核密码版本 → Status 改为 deleting，记录 DeletionRequestedAt → RevokeAllSessions → 结束全部租约（原因 session_revoked）→ 把未结清的 grant 置为撤销，复用 revokeGrant 的状态迁移，让节点侧停止转发 → 写审计 account.delete_requested → 邮件已配置时发送“账号已删除”通知。之后清除 Cookie 并跳转到首页。从这一刻起，登录统一返回 invalid_credentials，找回密码也不发信。
- 第二阶段见 7.3。

### 7.3 清除管线（用户自删与管理员删除共用）

维护循环每分钟用独立的 advisory lock 处理一次：对 Status=deleting 的用户，在其 grant 全部结清（ReleasedAt 非空），或距删除请求已超过结清宽限加租约时长之后，在一个事务内按以下顺序物理删除：

node_usage_reports（属于该用户的 grant）→ connection_grants → subscription_periods → client_traffic_reports → connection_leases → native_sessions → sessions → usage_buckets、node_assignments、node_health_events → announcement_reads → login_events → 该用户的 email_auth_challenges（按用户 ID 和邮箱）以及对应的未发送邮件任务 → orders（把 payment_notifications.order_id 置空，保留匿名流水）→ subscriptions → users。

最后写一条审计 account.purged（TargetID 为原用户 UUID，不含邮箱）。清除完成后，同一邮箱可以重新注册，新账号没有任何历史。任何一步失败都整体回滚，下一轮重试；连续失败要输出告警日志。迟到的支付回调找不到订单时记为 unknown_order，只保留匿名流水，由管理员线下处理。

## 8. 登录记录与 IP 归属地

- **采集点**：网站登录、APP 登录、注册成功（记为一次登录）。成功和失败都记录；失败只在邮箱对应的账户存在时记录，不保存用户随意输入的邮箱。刷新令牌和模拟登录都不算登录。
- **不影响登录性能**：LastLoginAt 和 LastLoginIP 在登录事务中顺带更新（事务里本来就持有 User 锁，只多一条 UPDATE）。LoginEvent 在会话提交之后尽力插入，设置短超时，失败只写日志，不影响登录结果。归属地完全异步。
- **注册 IP**：在注册事务中写入 RegisterIP。真实 IP 依赖 CLIENT_IP_HEADER 与 TRUSTED_PROXIES 的既有配置；APP 直连后端域名时，取 Nginx 覆盖后的地址。
- **归属地 worker**：进程内循环，用 SKIP LOCKED 方式每批领取 20 条 pending 记录，优先读缓存；私有、回环、链路本地地址直接标为“内网/本机”；外呼受 GEOIP_REQUESTS_PER_MINUTE 限制，超时 3 秒，响应上限 4 KB，只取国家、省份、城市、运营商拼成短文本；最多重试 3 次，之后标为 failed；未配置接口时标为 skipped。
- **隐私**：外呼只发送 IP。若选用境外接口，需在隐私政策中说明，必要时可以换成离线 IP 库。日志不打印完整 UA。

## 9. 管理后台

### 9.1 入口与权限

- 前端独立路由 /admin/console，下设 users、orders、logins、coupons、announcements 五个子页，有自己的二级导航。侧栏中“管理后台”与“节点管理”是两个独立入口。
- 后端新建管理员中间件：要求 role=admin，并且当前会话不是模拟会话；**不依赖**节点目录开关。路由前缀 /api/v1/admin/console/。
- 二次验证：模拟登录、修改用户密码、删除用户、人工确认到账这四个操作，请求中必须带管理员本人的当前密码；按管理员计数，每 15 分钟最多 5 次错误。
- 所有写操作都写 AdminAudit（Detail 中记录非敏感摘要）。修改套餐使用 Version 乐观锁，防止两位管理员互相覆盖。

### 9.2 用户管理

- **列表**：服务端分页（每页 20 或 50）。筛选条件：邮箱或姓名关键词（转义 LIKE 通配符）、角色、状态、套餐、订阅状态（有效 / 已过期 / 无 / 测试）、注册时间区间、最近登录区间。排序：注册时间、最近登录时间、到期时间。列：邮箱、姓名、角色、状态、注册时间、注册 IP、套餐与到期时间、流量已用/总量、最近登录时间、IP 与归属地、在线设备数。
- **详情抽屉**：账户信息、订阅详情、最近订单、最近登录记录、已登录设备（复用 /me/devices 的视图结构，不暴露 token 或节点地址）。
- **修改密码**：管理员设置新密码（遵守长度规则），可勾选“通知用户”；撤销该用户全部会话；管理员看不到旧密码。
- **修改套餐**：可选套餐、开始时间、到期时间、总流量、已用量（保持 / 清零 / 指定值），也可以“立即结束订阅”。全部走 6.5 节的公共函数和周期同步。界面提示“更换套餐或开新周期会使该用户的客户端重新授权”。
- **删除用户**：二次验证，并输入目标邮箱确认，走第 7 节的两阶段管线。对管理员账号和管理员本人，这些按钮在界面上禁用，后端同样拒绝。

### 9.3 订单与支付

- 列表筛选：状态、支付方式、价格来源（标价 / 测试价 / 模拟）、用户邮箱、订单号、创建或支付时间区间。列：订单号、用户、套餐、标价、优惠码与优惠、应付、实付、支付方式、状态（已支付 / 待付款 / 已超时 / 失败 / 待核对）、创建时间、支付时间。顶部汇总当天和当月的已支付笔数与金额（排除测试价）。
- 详情：显示该订单的全部回调流水（来源 IP、处理结果）和对账查询记录。
- 操作：“查询网关状态”（只读）；对 review 订单“人工确认到账”（二次验证、填写备注、按 6.5 节履约，金额以网关商户后台核实的为准）；“标记关闭”（不开通，只记备注）。退款需线下处理，本期不提供退款按钮。

### 9.4 登录记录面板

独立页面，按时间倒序。筛选：用户邮箱、IP、渠道、结果、时间区间。列：时间、用户、渠道和平台、IP、归属地（解析中 / 已解析 / 失败）、UA 摘要、结果。提供“同一 IP 多账号”“短时间多次失败”的快捷筛选，便于发现撞库。只读。

### 9.5 优惠码管理（第 5 轮界面，第 1 轮先做 API）

列表显示码、折扣、适用套餐、有效期、已用 / 上限、每人限次、状态。支持新建、编辑、启用/停用、删除。码可手动输入，也可一键生成 10 位随机码（服务端生成）。从未被使用过的码可以物理删除；已使用过的只能软删，订单中的快照不受影响。已有订单使用过的码，其折扣类型和数值不允许修改，只能停用后新建。

### 9.6 公告

- 管理员：新建草稿（标题、正文纯文本，保留换行）→ 预览 → 发布（写入 PublishedAt）→ 下线（archived）。草稿可以编辑和删除；已发布的内容锁定，只能下线或“复制为新草稿”。列表显示状态、发布时间、已确认人数和当前活跃用户数，可以分页查看确认人列表。
- 用户：真实登录会话进入工作区时，前端请求“待确认公告”。后端返回已发布、本人未确认的公告，按 PublishedAt 升序（同一时间按 ID），最多 20 条。弹窗没有关闭按钮，Esc 和点击遮罩都无效，正文以纯文本渲染（不解析 HTML 或 Markdown，防 XSS）。只有“知道了”按钮，点击后调用确认接口，成功后显示下一条；接口失败时弹窗保留并显示错误。每次切换路由和每 5 分钟重新检查一次，这样已登录的用户也能看到新发布的公告。
- 模拟会话：待确认接口返回空列表，确认接口返回 403。这一点由后端按会话类型判断，不依赖前端。

### 9.7 模拟登录与返回管理员

1. 管理员在用户详情中点击“模拟登录”，输入本人密码。后端校验：管理员会话正常且不是模拟会话；目标用户为 active 的普通用户；二次验证通过。
2. 创建模拟 Session：UserID 为目标用户，ImpersonatorID 为管理员，ImpersonatorSessionID 为管理员当前会话；不记住登录；HardExpiresAt 为现在加 IMPERSONATION_MINUTES，刷新不能超过这个时间。
3. 轮换管理员会话的刷新令牌，并把新令牌写入专用的返回 Cookie：HttpOnly、Secure（跟随 COOKIE_SECURE）、SameSite=Strict、Path 限定为返回接口、过期时间与模拟会话一致。然后把 vpn_access 和 vpn_refresh 换成模拟会话的令牌。写审计 admin.impersonate_start。
4. 模拟期间：/auth/me 额外返回模拟信息（管理员邮箱、到期时间），前端显示醒目的常驻横幅和倒计时；全部写接口返回 403 impersonation_read_only；管理员接口一律拒绝；公告不显示；不写登录记录；不更新最近登录时间。
5. 返回管理员：校验当前会话是模拟会话，返回 Cookie 对应 ImpersonatorSessionID 且该会话未撤销、未过期，管理员仍是 active 的管理员 → 撤销模拟会话 → 轮换管理员刷新令牌并写回正常 Cookie → 清除返回 Cookie → 写审计 admin.impersonate_end → 前端跳回 /admin/console/users。任一校验失败：撤销模拟会话，清除全部 Cookie，跳转到登录页。
6. 边界情况：管理员在模拟期间被改密或停用，返回失败，必须重新登录；目标用户被删除或改密，RevokeAllSessions 会顺带撤销模拟会话；同一浏览器的其他管理员标签页会一起变成用户身份，横幅和 403 处理要能正确应对；模拟会话期间退出登录，会同时清除返回 Cookie。

## 10. API 契约总表

全部沿用 /api/v1 前缀、严格 JSON、no-store、稳定的错误码和中文提示。

| 方法与路径 | 鉴权 | 说明 |
| --- | --- | --- |
| POST /payments/quote | 用户（非模拟） | 报价，不预占 |
| POST /payments/orders | 用户（非模拟），需 Idempotency-Key | 下单，返回订单与网关 URL |
| GET /payments/orders/:id | 用户，只能看本人订单 | 结果页轮询；pending 时可触发限频查询 |
| POST /payments/notify | 无鉴权，IP 白名单 | 网关回调 |
| POST /me/password | 用户（非模拟） | 修改密码 |
| POST /me/account/delete | 用户（非模拟、非管理员） | 删除账号第一阶段 |
| GET /me/logins | 用户 | 可选：本人最近登录 |
| GET /me/announcements | 用户（模拟会话返回空） | 待确认公告 |
| POST /me/announcements/:id/ack | 用户（非模拟） | 确认公告，幂等 |
| POST /auth/impersonation/return | 模拟会话加返回 Cookie | 返回管理员 |
| GET /admin/console/users，GET /admin/console/users/:id | 管理员 | 列表与详情 |
| POST /admin/console/users/:id/password、/subscription、/subscription/end、/delete、/impersonate | 管理员；其中 password、delete、impersonate 需二次验证 | 用户操作 |
| GET /admin/console/orders，GET /admin/console/orders/:id，POST /admin/console/orders/:id/check、/confirm、/close | 管理员；confirm 需二次验证 | 订单与支付 |
| GET /admin/console/logins | 管理员 | 登录记录 |
| GET、POST /admin/console/coupons，POST、DELETE /admin/console/coupons/:id | 管理员 | 优惠码 |
| GET、POST /admin/console/announcements，POST /admin/console/announcements/:id、/publish、/archive，DELETE /admin/console/announcements/:id，GET /admin/console/announcements/:id/reads | 管理员 | 公告 |

/auth/me 和 /me/dashboard 的 User 响应保持现有字段，只增加可选的模拟信息块。订单 JSON 增加的字段都是可选的，旧前端仍可解析。

## 11. 前端页面与交互要点

- **套餐页**：PAYMENT_ENABLED 为 true 时按钮显示“立即购买”或“续费”，会被拒绝的套餐按钮禁用并说明原因。购买弹窗包含支付方式（微信绿、支付宝蓝两张选择卡，没有默认选中，必须主动选择）、优惠码输入框和“应用”按钮（可留空）、服务端报价明细（标价、测试价、优惠、应付）、“去支付”按钮。点击后进入下单接口，成功后整页跳转。防重复提交、账户切换后弹窗消失的现有机制保留。
- **结果页 /pay/result**：行为按第 6.8 节，显示“待支付 / 支付已完成正在确认 / 已到账 / 已超时 / 待人工核对（请联系客服并提供订单号）”。
- **订单页**：新增状态、支付方式、实付、优惠、支付时间。待付款订单显示剩余时间，但不提供“继续支付”（网关不允许复用 trade_no），需要时直接重新下单。有多笔待付订单时，顶部提示“请勿重复付款”。
- **账号设置页**：表单校验与后端规则一致，按 UTF-8 字节计算长度；危险区样式醒目。
- **管理后台**：表格使用服务端分页与筛选，筛选条件同步到 URL 查询参数便于分享；危险操作统一使用二次验证弹窗；所有日期按浏览器本地时区显示，同时在悬停提示中显示 UTC。
- **外壳**：workspace 白名单加入新路由；侧栏文案更新为“已接入微信/支付宝支付”；模拟横幅在所有工作区页面顶部常驻。

## 12. 并发、锁顺序与幂等

统一锁顺序：**User → Order → Coupon → Subscription → SubscriptionPeriod → Session/NativeSession → MailJob**，同类多行按主键顺序加锁。禁止先锁订单或优惠码，再回头请求 User 锁。bcrypt 计算和网关 HTTP 调用一律放在事务外。

| 场景 | 保证 |
| --- | --- |
| 同一回调重复到达、回调与对账并发、回调与人工确认并发 | 都在 User 加 Order 行锁下判断状态，只会履约一次 |
| 同一个 Idempotency-Key 并发下单 | 现有的（user_id, idempotency_key）唯一索引兜底，重放返回原订单 |
| 两个请求同时用完最后一次优惠码 | 锁定优惠码行后再计数 |
| 管理员改套餐与回调履约并发 | 二者都先锁 User，后提交的一方基于最新状态判断，冲突时转入 review |
| 同一用户多笔待付订单几乎同时到账 | 各自锁 User 后串行履约，第二笔基于第一笔履约后的订阅判断（同套餐续期，不同套餐转入 review） |
| 删号与付款并发 | 删号检查活动订单要在 User 锁下进行；迟到的回调找不到 active 用户时转入 review 或 unknown_order |
| 模拟会话与目标用户改密或删号并发 | RevokeAllSessions 覆盖模拟会话；requireAuth 每次请求都查库 |

## 13. 日志、审计、隐私与数据保留

- 严禁写入日志：PAYMENT_SECRET、sign、完整网关 URL、Cookie、令牌、密码、回调原文中的个人信息、完整 UA。可以记录：请求 ID、订单 ID、trade_no 后 6 位、状态迁移、处理结果类别、脱敏邮箱。
- 审计动作命名：payment.confirm_manual、payment.close、account.password_change、account.delete_requested、account.purged、admin.user_password、admin.user_subscription、admin.user_delete、admin.impersonate_start、admin.impersonate_end、admin.coupon_*、admin.announcement_*。
- 数据保留：LoginEvent 180 天；PaymentNotification 180 天；IPGeoCache 30 天；AnnouncementRead 随公告或用户删除；审计长期保留，只含 UUID 和非敏感摘要。
- 指标：下单量、按状态分类的回调数、review 数、对账查询失败数、归属地队列积压、模拟登录次数。告警条件：任何 ip_rejected 或 amount_mismatch，以及 review 积压超过阈值。

## 14. 六轮实施与逐轮验收

每轮都要交付实现、测试和第 18 节的记录。未执行的项必须如实标注 NOT RUN 或 BLOCKED 并写明原因，不能写成 PASS。分支命名为 worktree-payment-roundN-*。

### 第 1 轮：支付后端闭环（假网关）

工作项：第 4 节配置与生产约束；第 5 节中订单、优惠码、回调流水和 AdminAudit.Detail 的迁移；网关适配包（签名、URL 构造、状态查询客户端、回调解析）；报价、下单、回调、订单查询接口；对账循环；6.5 节公共履约函数（旧的测试购买改为调用它）；测试价；优惠码 API；doctor 与表清单；.env.example 和部署文档。本交接书随本轮 PR 一并纳入版本库。

| 编号 | 验收项 |
| --- | --- |
| R1-01 | 用文档示例（trade_no 12345678901234567890 加示例密钥）算出的签名与文档给出的值完全一致；URL 各参数编码正确 |
| R1-02 | 配置矩阵：缺商户号或密钥、非法布尔值、非 HTTPS 的生产回调地址、生产开测试价但名单为空，都拒绝启动，并且报错不泄露密钥；IP 白名单为空时能启动，但有告警，且 doctor 报告“自动开通未启用” |
| R1-03 | 用本地模拟网关（按文档实现 submit 的签名校验、分支付方式的 +0.01 去重与 2 分钟失效、状态查询、回调发送）跑通：下单 → 回调 → paid → 订阅开通或续期正确，客户端会话指纹的行为符合 2.1 节预期 |
| R1-04 | 篡改攻击：改小 money 后付款，回调金额偏低，订单转入 review、不开通；伪造回调来自白名单外 IP，记为 ip_rejected；伪造回调来自白名单内但金额偏低，转入 review；白名单为空时，即使回调完全正确也只转入 review（unverified_source），流水中有来源 IP |
| R1-05 | 回调格式边界：12.001、负数、字符串、科学计数、超大值、缺字段、多余字段、TRADE_FAILED、未知 trade_no 都不改动订单，而且都留下流水 |
| R1-06 | 并发：同一回调 20 路并发、回调与对账并发、两次相同 Idempotency-Key 并发、两人争抢最后一次优惠码，都只发生一次履约或计数 |
| R1-07 | 对账：timeout 转为 expired 并释放优惠码；paid 但无回调转入 review 且不开通；网关不可达不误判；迟到但真实的回调能把 expired 订单正确开通 |
| R1-08 | D3 与 D6：同一用户连续下 3 笔 20 元订单，模拟网关依次给出 20.00、20.01、20.02，三笔回调分别对上各自订单；同套餐先后付两笔则续期两次；不同套餐的第二笔转入 review；活动订单数和下单频率超限返回 429；有效期内换套餐，报价和下单都拒绝；测试订阅被正式付款覆盖时开新周期 |
| R1-09 | 测试价只对名单内邮箱生效；订单记录 test_price 和标价；关闭后恢复标价 |
| R1-10 | 优惠码：百分比和固定金额的计算与取整、最低 0.01 元、适用套餐、有效期、总次数、每人次数、软删；无效尝试达到上限返回 429 |
| R1-11 | 迁移：旧数据副本上重复迁移两次，旧订单逐值不变，PaidAt 可空化之后旧订单的值不变；doctor 能报告新表 |
| R1-12 | 现有回归：Go 竞态测试、test-native.py、test-node-catalog.py 中的旧测试购买链路都通过 |

### 第 2 轮：网站支付体验与真实网关联调

工作项：套餐页购买弹窗、结果页、订单页、meta 字段、外壳文案；Playwright 用假网关主机跑通整页跳转和返回；有公网 HTTPS 测试环境和商户凭据时，用测试价 0.01 元做真实联调。

| 编号 | 验收项 |
| --- | --- |
| R2-01 | 浏览器 E2E：选择微信或支付宝 → 可选填优惠码 → 报价 → 跳转假网关 → 回调 → 返回结果页，显示“已到账”，订单页和总览同步更新 |
| R2-02 | 不选支付方式无法提交；优惠码无效时显示四类原因；留空正常；切换账户或会话失效时弹窗消失 |
| R2-03 | 结果页地址栏手动加“成功”参数不会改变显示；访问他人的订单 ID 返回 404 |
| R2-07 | 第 6.8 节四条路径都在模拟网关上走通：成功后自动跳回；超时页按钮跳回并显示已超时；直接关窗后订单在 3 分钟窗口结束时变为已超时，并可立即重新下单；超时后的可信回调仍能开通 |
| R2-04 | PAYMENT_ENABLED=false 时恢复旧测试购买 UI，flow.spec 等旧用例通过 |
| R2-05 | 生成的网关 URL 与文档示例逐个参数比对一致（参数名、编码、两位小数金额、小写签名）。真实网关付款不在本轮执行，记为“用户部署后实测”，对应第 15.2 节 |
| R2-06 | 前端 lint、typecheck、`build --webpack`、代理单测全部通过 |

### 第 3 轮：账号设置、清除管线与登录记录

工作项：/account 页面；改密、删号接口；7.3 节清除管线；登录记录采集、LastLogin 和 RegisterIP；归属地 worker 与缓存；代理转发 UA。

| 编号 | 验收项 |
| --- | --- |
| R3-01 | 改密：当前密码错误被拒；错误次数达到上限返回 429；成功后其他浏览器和 APP 会话收到 401，当前浏览器保持登录；旧密码不能再登录；邮件已配置时收到通知 |
| R3-02 | 改密与另一端登录并发：旧密码不会留下新会话（沿用 lockVerifiedUser 测试模式） |
| R3-03 | 删号第一阶段：立即无法登录，APP 下一次请求得到 native_session_expired，租约结束，grant 被撤销 |
| R3-04 | 清除：带有订单、流量报告、grant、周期、租约、公告确认、登录记录的账户被完整删除；逐表计数为 0；匿名回调流水仍在但 order_id 为空；同一邮箱可以重新注册且没有历史 |
| R3-05 | 存在活动待付订单时不能删号；管理员账号不能自助删号；模拟会话不能改密或删号 |
| R3-06 | 登录记录：网站与 APP 的成功、失败都有记录；刷新令牌不算登录；归属地接口不可用或很慢时，登录耗时不受影响（对比基线）；内网 IP 不外呼；缓存命中时不外呼 |
| R3-07 | 伪造的 X-Forwarded-For 不改变记录的 IP（沿用 handoff_auth.md R1-08 的方法） |

### 第 4 轮：管理后台 I

工作项：新管理员中间件与二次验证；后台框架与导航；用户列表、筛选、详情；改密、改套餐、结束订阅、删除；订单与支付面板及其操作；登录记录面板。

| 编号 | 验收项 |
| --- | --- |
| R4-01 | 普通用户、未登录用户、模拟会话访问所有 /admin/console 接口分别得到 403 或 401；CLIENT_NODE_CATALOG_ENABLED=false 时后台仍然可用；节点管理页不受影响 |
| R4-02 | 列表筛选与排序组合正确；关键词中的 % 和 _ 被转义；1 万用户规模下分页查询在可接受时间内（记录实测值） |
| R4-03 | 改套餐：续期客户端不掉线；换套餐时客户端收到 subscription_changed；已用量清零后 grant 周期账本同步；两位管理员同时编辑时，后提交的一方得到冲突提示 |
| R4-04 | 对管理员账号或自己执行改密、删除、模拟，后端拒绝；二次验证错误次数达到上限返回 429 |
| R4-05 | 人工确认 review 订单：只开通一次，与迟到回调并发时也只开通一次，审计中有记录 |
| R4-06 | 全部写操作都产生审计且 Detail 中没有敏感字段 |
| R4-07 | 浏览器 E2E 覆盖：管理员筛选用户 → 打开详情 → 修改套餐 → 用户总览页同步变化 |

### 第 5 轮：管理后台 II（优惠码界面、公告、模拟登录）

工作项：优惠码管理界面；公告表、接口、管理界面、强制确认弹窗；Session 模拟字段、模拟与返回接口、只读中间件、横幅；请求体上限调整。

| 编号 | 验收项 |
| --- | --- |
| R5-01 | 公告：草稿不可见；发布两条后，真实登录的用户按发布时间依次看到；Esc、点击遮罩、刷新页面都无法跳过；点击“知道了”后该条不再出现，在其他浏览器也不出现；确认人数准确；下线后不再推送 |
| R5-02 | 公告正文中的 HTML 或脚本按纯文本显示；5000 个汉字可以保存（Go 与代理两处的请求体上限都验证过） |
| R5-03 | 模拟登录：总览、订单、节点等页面显示目标用户数据；任何写操作返回 403；公告不弹出、确认接口拒绝；目标用户的最近登录时间和登录记录不变；审计有开始和结束两条 |
| R5-04 | 返回管理员：成功回到后台，并且模拟会话已撤销（旧 Cookie 重放返回 401）；模拟会话超过硬性有效期后自动失效、无法续期；管理员在此期间被改密时返回失败并要求重新登录 |
| R5-05 | 返回 Cookie 的属性（HttpOnly、SameSite=Strict、Path 限定）可在浏览器中观察到；通过 API 盗用返回 Cookie 访问其他接口无效 |
| R5-06 | 优惠码界面：新建、编辑、停用、删除（已使用过的变成软删）、随机生成；已被使用过的码不能修改折扣 |

### 第 6 轮：全链路回归、安全审查与发布

工作项：release rehearsal 扩展（旧版本数据 → 当前版本迁移 → 支付、账号、后台全流程 → 回滚）；四平台兼容验收；安全审查清单；部署文档（Nginx 中回调路径、IP 白名单、真实来源地址）；README 更新。

| 编号 | 验收项 |
| --- | --- |
| R6-01 | 演练：备份 → 迁移 → 全流程 → 回滚到上一版本后，旧版本仍能读取新增列并正常运行（新列都可空或有默认值） |
| R6-02 | 四平台兼容：网站付款续期时 APP 不断线；新周期时 APP 提示重新授权并恢复；网站改密或删号后四端回到登录页（不能在本机执行的平台依赖 CI，或记为 NOT RUN） |
| R6-03 | 安全审查逐项打勾：回调只认白名单 IP、金额下限校验、trade_no 不可预测、无前端金额输入、模拟会话只读、二次验证、日志脱敏、XSS、越权访问（IDOR，换用他人 ID 访问订单、公告、用户详情）、优惠码枚举限流 |
| R6-04 | 生产配置下 doctor 全绿；以生产配置启动时，TEST_PURCHASE 仍被拒绝 |
| R6-05 | 第 15.2 节的用户实测清单、对应的部署说明和排查指引已交付，并在本地模拟网关上把清单中的每一步都走通过一遍；真实结果由用户部署后填写 |
| R6-06 | CI 全绿，并确认新增的检查确实出现在 gh pr checks 中 |

## 15. 测试资产与证据要求

### 15.1 开发方可执行的测试

- 本地模拟网关严格按《支付接入技术文档.md》实现：submit 校验参数和签名，按支付方式分别做 +0.01 去重，订单 2 分钟失效，提供成功页（5 秒后跳转 return_url）和超时页（按钮跳转 return_url），提供状态查询，并按文档格式发送回调。它只存在于测试代码中，模拟的是文档描述的行为，而不是网关的真实实现；两者不一致的地方只能靠 15.2 节发现。
- Go 测试沿用私有 PostgreSQL（NATIVE_TEST_DSN 或 test-native.py）、注入时钟（newServerAt）和 GORM 回调屏障的做法。假网关用 httptest 实现，放在测试包内，**不在生产代码中留任何测试后门或固定密钥**。
- Playwright 沿用 E2E_ISOLATED=private-postgres 和明确的回环 PLAYWRIGHT_BASE_URL；假网关作为夹具进程。worktree 中按既有经验，用 webpack 构建并跳过重复构建。
- 证据存放在 artifacts/payment-roundN-*，包括命令、输出摘要、截图和数据库逐表计数，但不能包含密钥、令牌或完整网关 URL。
### 15.2 用户部署后的真实付款实测清单

前提：公网 HTTPS 部署完成；backend/.env 已填商户号、密钥、回调地址；PAYMENT_TEST_PRICE_ENABLED=true，测试价 0.01 元，PAYMENT_TEST_PRICE_EMAILS 只填自己的测试账户；PAYMENT_NOTIFY_ALLOWED_IPS 先留空。每一步都在第 18 节记录日期、trade_no 后 6 位和结果。

| 编号 | 步骤 | 预期 |
| --- | --- | --- |
| U-01 | 测试账户用微信支付购买一个套餐（0.01 元） | 网关显示绿色页面和二维码；付款后 5 秒跳回结果页。因为白名单为空，订单为“待人工核对”，后台订单详情的回调流水中能看到来源 IP |
| U-02 | 把 U-01 中看到的来源 IP 填入 PAYMENT_NOTIFY_ALLOWED_IPS 并重启后端；在后台对 U-01 订单“人工确认到账” | 订阅开通；之后的回调可以自动开通 |
| U-03 | 用支付宝重复一次 | 蓝色页面；付款后自动到账，结果页显示已到账（若支付宝回调来自不同 IP，按 U-01、U-02 再补一次） |
| U-04 | 打开支付页后不付款，等待 2 分钟，点击超时页上的按钮 | 跳回结果页，显示已超时、未扣款 |
| U-05 | 打开支付页后直接关闭窗口，再立即重新购买同一套餐 | 第二笔可以正常下单；网关页面显示的金额为 0.02 元（0.01 加去重 0.01），付款后到账；第一笔在 3 分钟后变为已超时 |
| U-06 | 带一个有效优惠码购买 | 报价显示折扣，最低 0.01 元；付款后优惠码的已用次数加 1 |
| U-07 | 同一套餐再付一次 | 订阅延长天数并累加流量，APP 保持在线 |
| U-08 | 关闭测试价，用一个不在名单中的账户查看报价 | 显示正式价格（不必真付） |
| U-09 | 检查后端日志 | 日志中没有密钥、签名或完整网关 URL |

任何一步与预期不符：先查看后台订单详情里的回调流水（来源 IP、处理结果）和后端日志，再对照第 6 节判断是网关行为与文档不一致，还是本地实现有问题；网关行为与文档不一致的，记入第 18 节，再决定是否单开修复轮次。

## 16. 发布顺序与回滚

1. 先部署第 1 轮的迁移和代码，保持 PAYMENT_ENABLED=false：上线后无可见变化。
2. 配置 Nginx（回调路径直达后端、覆盖转发头）和 TRUSTED_PROXIES，开启支付和测试价，由用户按第 15.2 节完成真实付款实测，并在过程中得到网关 IP 白名单。
3. 关闭测试价，开启正式价，观察 review 和 ip_rejected 告警。
4. 回滚：把 PAYMENT_ENABLED 改为 false 即可停止新下单；已付款但还没处理的回调由新版本的对账循环和人工核对兜底。回退代码前先确认没有活动中的待付订单。迁移只做增量，旧版本可以忽略新列。模拟会话在回滚后会被旧版本当作普通会话，所以回滚前要先撤销全部模拟会话（第 6 轮提供一条运维命令或 doctor 检查）。

## 17. 完成定义

- 用户可以选择微信或支付宝、可选填优惠码完成真实付款，并自动开通或续期；任何篡改金额或伪造回调都不能免费开通。
- 测试价由 .env 控制，只对名单内账户生效，有清晰标记。
- 用户可以自助改密和删号，删号后关联数据被物理清除，只留不含用户标识的匿名回调流水。
- 管理员后台独立于节点管理，覆盖用户、订单支付、登录记录、优惠码、公告和模拟登录，危险操作有二次验证和审计。
- 公告逐条强制确认、确认人数准确，模拟登录时不显示。
- 第 14 节全部验收项都有 PASS 证据，或有写明原因的 BLOCKED / NOT RUN 记录，CI 全绿。
- 开发方的完成以本地模拟网关的验证为准；真实付款的结论以用户按第 15.2 节实测的结果为准，二者分开记录。

## 18. 实施记录

（每轮完成后在此追加：日期、分支、PR、执行过的验收编号与结果、未执行项及原因、遗留问题、对后续轮次的影响。）


### 第 1 轮记录（2026-10-22）：支付后端闭环（假网关）

- 分支 `worktree-payment-round1-backend`，基线 9ee9066。PR 与 CI 结果见 PR 页面，本记录只写本机已执行的内容。
- 新增 `backend/internal/payment`：金额解析与格式化、`MD5(trade_no+secret)` 签名、20 位订单号、按文档参数顺序生成 submit URL、状态查询客户端（5 秒超时、不跟随重定向、响应上限 4 KB）、回调解析。`internal/config/payment.go` 实现第 4 节配置；第 4 节表中的 GEOIP_*、IMPERSONATION_MINUTES 留到第 3、5 轮。新增 `PAYMENT_MAX_PENDING_PER_USER`（D3）。
- API 新增：`POST /payments/quote`、`POST /payments/orders`、`GET /payments/orders/:id`、`POST /payments/notify`；对账函数 `ReconcilePayments` 由 `cmd/api` 每 30 秒运行一次，用 SKIP LOCKED 认领订单，不使用 advisory lock，可多实例运行；管理接口 `/admin/console/coupons`，使用新中间件 `requireConsoleAdmin`，不依赖节点目录开关。`fulfil.go` 的 `planDecision`/`applyPlan` 成为唯一的履约实现，旧测试购买也改为调用它（行为不变，已有测试通过）。
- 数据：`orders` 增加网关字段（`paid_at` 改为可空），新表 `payment_notifications`、`coupons`，`admin_audits.detail`，两个待付款部分索引；doctor 增加 `payment` 段和两张表。
- 偏离与决定：
  1. **文档签名示例与算法不一致**：文档写 `MD5("1234567890123456789056Hhtcc1yfq39WhvsqMe") = c237aabf7d689f6bbdded44662c8459d`，但 md5(1)、Python、Go 三者算出的都是 `becd5f2a12fcf4746840db6870131f3d`。拼接顺序、分隔符、加入 channel 或金额等变体也都不等于该值，因此判断示例值是占位数据。实现遵循文档文字和全部示例代码（trade_no 在前，secret 在后，小写十六进制）。部署后 U-01 若被拒为“签名验证失败”，先核对此处。
  2. **回调金额按浮点处理**：依文档 FAQ Q7（回调金额为浮点数），回调金额精确解析为有理数，只有距整分不超过百万分之一时才取整（例如 20.009999999999998 记为 20.01），否则视为无效。下单 URL 的金额始终是两位小数。
  3. **回调允许未知字段**：只要求文档的四个字段，其余字段忽略，避免网关以后新增字段时拒收真实回调。请求体超过 4 KB 也会记录（结果 invalid）。
  4. 迟到的真实到账照常开通：订单处于 expired、failed，或因 callback_missing、gateway_unreachable 进入 review 时都适用。因金额不符、来源未验证或状态冲突进入 review 的订单保持原判，由管理员处理（第 4 轮界面）。
  5. 优惠码代码长度 4–32；百分比折扣向下取整；履约使用订单下单时的套餐快照（天数、流量），不受管理员之后修改套餐的影响。
- 本机验收（私有 PostgreSQL 16，Unix socket；`go test -race -count=1 ./...` 全部通过；需要真实 Core 或 Agent 二进制的端到端测试由 CI 的 `test-native.py` 构建后运行，本机未构建这些二进制）：

| 编号 | 结果 | 证据（测试名） |
| --- | --- | --- |
| R1-01 | PASS（算法）；文档示例值不符，见偏离 1 | TestSignFollowsGatewayAlgorithm、TestSubmitURL |
| R1-02 | PASS | TestPaymentConfiguration、TestPaymentProductionRules |
| R1-03 | PASS（微信与支付宝） | TestPaymentHappyPath |
| R1-04 | PASS | TestPaymentTamperingAndForgery（改小金额、URL 被重用、白名单外 IP、伪造 X-Forwarded-For、低于应付或超出容差、白名单为空） |
| R1-05 | PASS | TestPaymentCallbackEdges、TestParseNotification |
| R1-06 | PASS | TestPaymentConcurrency（20 路相同回调只开通 1 次；8 路同一 Idempotency-Key 只建 1 单；两人争抢最后一次优惠码，只成功 1 人） |
| R1-07 | PASS | TestPaymentReconciliation |
| R1-08 | PASS | TestPaymentMultiplePendingOrders、TestPaymentRenewalRules、TestPaymentLimits |
| R1-09 | PASS | TestPaymentTestPrice |
| R1-10 | PASS | TestPaymentCoupons、TestAdminCoupons |
| R1-11 | 部分 PASS：新库连续迁移两次通过，旧测试订单读写不变（authmail 旧数据用例） | 用旧二进制生成旧数据的跨版本演练（test-release-rehearsal.py）本机未运行，留到第 6 轮 R6-01 |
| R1-12 | 本机 `go test -race ./...` 全部通过；`test-node-catalog.py`、`test-native.py` 由 CI 执行 | — |

- 遗留与对后续轮次的影响：网站 `types.ts` 中的 `paid_at` 在第 2 轮改为可空；第 4 轮订单详情显示回调来源 IP，供 U-02 使用。

### 第 2 轮记录（2026-10-22）：网站支付体验与本地模拟网关联调

- 分支 `worktree-payment-round2-web`，基线 09a572f（第 1 轮合并后）。PR 与 CI 结果见 PR 页面，本记录只写本机已执行的内容。
- 前端：`PurchaseModal`（支付方式两张选择卡，无默认选中；优惠码输入与“应用”；展示服务端报价的标价、测试价、优惠、应付；“去支付”整页跳转网关）；`/pay/result` 结果页（只依据后端订单状态，每 2 秒轮询、最长 5 分钟、页面不可见时暂停；待支付、正在确认到账、已到账、已超时未扣款、失败、待人工核对；超时与失败显示“重新购买”，回到 /plans 并预选套餐和支付方式）；订单页（金额、优惠、实付、支付方式、状态、剩余时间、支付时间，多笔待付显示“请勿重复付款”）；`/meta` 的 `payment_enabled`、`payment_methods` 控制套餐页，`PAYMENT_ENABLED=false` 时旧的测试购买界面原样保留；侧栏文案随开关更新；未登录访问结果页先跳 `/login?next=`，登录后回到同一订单（next 只接受 `/pay/result?order=<uuid>`，其他一律回总览）。
- 前端不发送任何金额：下单请求只含套餐、支付方式和优惠码；每个（套餐、支付方式、优惠码）组合使用一个 Idempotency-Key，组合变化即新订单；输入框中未“应用”的优惠码或不可用的优惠码会禁用“去支付”，保证屏幕价格就是实扣价格。
- **后端改动（偏离第 1 轮，为满足 6.8 节第 2 条）**：结果页订单查询（`GET /payments/orders/:id`）在网关查询返回 `timeout` 或 `expired=true`，且订单已创建满网关的 2 分钟时，立即把仍为 pending 的订单标为 expired（用户锁加订单锁内，抽成 `settlePending`，对账循环也改用它）。这样从网关超时页点“返回商户页面”时能直接看到“已超时，未扣款”，不必等本地 3 分钟窗口结束。迟到的可信回调仍然照常开通已超时订单；网关返回 `paid` 仍然只是显示提示，不改状态、不开通。`not_found` 不触发提前过期（订单可能还没被浏览器打开）。
- 测试资产：`payment_harness_test.go` 的本地模拟网关增加浏览器页面（金额页、成功后 5 秒跳回 return_url、超时页“返回商户页面”按钮）；`payment_acceptance_test.go` 提供可由浏览器访问的假网关夹具（真实 API、可前移的时钟、控制端点：推进时钟、模拟支付与回调、查询订单和订阅），`scripts/test-payment-web.py` 启动私有 PostgreSQL、API、Next.js 并运行 `frontend/tests/payment.spec.ts`。该步骤已加入 CI（web-core 作业）。顺带修复 `scripts/test-release-rehearsal.py` 的表清单（第 1 轮新增的 payment_notifications、coupons 未登记，导致演练在迁移断言处失败；该脚本不在 CI 中）。
- 本机验收（私有 PostgreSQL 16；Chrome 使用本机 Google Chrome）：

| 编号 | 结果 | 证据 |
| --- | --- | --- |
| R2-01 | PASS（本地模拟网关） | payment.spec.ts 第 1 条：选微信 → 优惠码 SAVE20 → 报价 ¥31.92 → 跳转网关 → 回调 → 5 秒后跳回结果页“已到账” → 订单页（已支付、微信支付、¥31.92、优惠 ¥7.98）与总览同步 |
| R2-02 | PASS | 第 1、2 条：未选支付方式“去支付”禁用；无效、不适用、已达上限、已使用过、过期各自的原因文案；留空正常；未应用的优惠码禁用支付；会话失效（清 Cookie 后触发 401）弹窗消失。账户切换使弹窗消失沿用原有 `choice.userId` 机制，未单独新增用例 |
| R2-03 | PASS | 第 4 条：地址栏加 status=success 等参数，待付订单仍显示“待支付”；他人订单和非法 ID 都显示“订单不存在”（后端 404）；登录跳转的 next 只认结果页 |
| R2-07 | PASS（本地模拟网关） | 第 1、5、6 条：成功后自动跳回；超时页按钮跳回并显示“已超时，未扣款”，“重新购买”预选套餐与方式；直接关窗后订单页有倒计时、可立即重新下单（网关金额 0.02，即 0.01 加 0.01 去重），时钟前移 200 秒后两笔都变为“已超时”；超时后的可信回调仍然开通。另有 Go 测试 `TestPaymentResultPageExpiresAfterGatewayTimeout` |
| R2-04 | PASS | `test-release-rehearsal.py` 的浏览器阶段：`PAYMENT_ENABLED` 默认关闭，flow.spec 两个用例通过（测试购买 UI 原样）；api-proxy 单测 15 项通过 |
| R2-05 | PASS（对照假网关）；真实网关 NOT RUN | 第 1 条逐字符比较浏览器地址栏中的 submit 查询串：参数名与顺序、各参数 URL 编码、两位小数金额、小写 MD5 签名、return_url 只含本地订单 ID。真实网关付款不在本轮执行，属“用户部署后实测”，对应第 15.2 节 U-01..U-09，未标记为 PASS |
| R2-06 | PASS | `npm run lint`、`npm run typecheck`、`npm run build -- --webpack`、`npm run test:api-proxy`（需 PLAYWRIGHT_CHROME_PATH 指向本机 Chrome，本机未安装 Playwright 自带的 Chromium）通过；`go test -race -count=1 ./...` 全部通过 |

- 其他浏览器用例（admin-nodes、dashboard-presence、user-nodes、register、reset）依赖 `test-node-catalog.py` 的 Mihomo 夹具，本机未运行，由 CI 执行。
- NOT RUN：有公网 HTTPS 测试环境和商户凭据时的真实联调（本机没有；网关也没有测试环境）。移动端视口下的购买弹窗没有单独的截图用例（两张支付卡在窄屏下单列排列，由 CSS 保证，未做自动化验证）。
- 证据：本机 `artifacts/payment-round2-web/`（summary.json、日志、`browser-artifacts/screens/` 下的结算弹窗、已到账、待核对截图），已被 .gitignore 忽略；CI 上传 `payment-web-evidence`。
- 对后续轮次的影响：第 4 轮订单详情仍需展示回调来源 IP（U-02）；`settlePending` 可供第 4 轮“人工确认到账 / 标记关闭”复用同样的锁顺序。

### 第 3 轮记录（2026-10-23）：账号设置、清除管线与登录记录

- 分支 `worktree-payment-round3-account`，基线 fd0f24f（第 2 轮及两个 CI 稳定性修复合并后）。PR 与 CI 结果见 PR 页面，本记录只写本机已执行的内容。
- 后端：`POST /me/password`（当前密码在锁外校验，同一账户每 15 分钟最多 5 次错误，数据库计数，删号接口共用这份额度；新哈希在锁外计算，事务内 `lockVerifiedUser` 复核；`RevokeAllSessions` 撤销全部网站与 APP 会话后为当前浏览器签发新会话并保留“记住我”选择；审计 `account.password_change`；已配置邮件时发送改密通知）。`POST /me/account/delete`（需要当前密码与完整邮箱；管理员 403 `admin_account_protected`；用户锁下检查活动待付订单，有则 409 `pending_orders` 并给出等待秒数；第一阶段：状态 `deleting`、撤销全部会话与租约、未结清 grant 置 `revoking`、审计 `account.delete_requested`、发送“账号已删除”通知）。`GET /me/logins`（本人最近 10 次，含失败）。
- 清除管线 `api.PurgeDeletedAccounts`：`cmd/api` 每分钟一次，advisory lock `708614924`；某用户全部 grant 已释放，或已过期超过 `NODE_AGENT_SETTLE_GRACE_SECONDS`（节点已自行停止转发）时，单事务按依赖顺序删除全部数据，只保留 `payment_notifications`（`order_id` 置空）；审计 `account.purged` 只含原 UUID。失败整体回滚，连续 3 次失败输出 `WARNING`（只含 UUID）。`announcement_reads` 在第 5 轮建表时要加入 `account_purge.go` 的步骤列表（文件中已留注释）。
- 登录记录：网站登录、APP 登录、注册成功写 `login_events`（成功同步插入，带 2 秒超时、失败只写日志；失败记录在后台协程里写，避免“账号存在与否”产生时间差；账号不存在或处于 `deleting` 时不记录；刷新令牌不记录）。`users.last_login_at/last_login_ip` 在登录事务内更新，`register_ip` 在注册事务内写入。`internal/loginlog`：归属地 worker 每 5 秒领取 20 条（`SKIP LOCKED` 加 1 分钟租约），先查 `ip_geo_caches`（成功 30 天、失败 1 小时），再按 `GEOIP_REQUESTS_PER_MINUTE` 限速外呼（超时 3 秒、不跟随重定向、响应上限 4 KB、仅向接口发送 IP、令牌只放 Authorization 头），最多 3 次；内网/本机/链路本地/CGNAT 地址与未配置接口不外呼；每小时清理 180 天前的 `login_events`、`payment_notifications` 和过期缓存。新配置 `GEOIP_LOOKUP_URL`（必须含一个 `{ip}`，不得在主机名中，生产要求 HTTPS）、`GEOIP_LOOKUP_TOKEN`、`GEOIP_REQUESTS_PER_MINUTE`。
- 前端：`/account`（基本信息、修改密码、最近登录、危险区）；删号对话框列出将清除的内容，密码加完整邮箱，倒计时 5 秒，取消即清空；成功后整页跳转首页；管理员账号只显示说明；侧栏与账户菜单增加“账号设置”；API 代理转发 `User-Agent`（截断到 200 字符）。`doctor` 增加 `account` 段（删除中账号数、最久等待、待解析记录数、`account_purge_stuck` 警告）。
- 偏离与决定：
  1. **模拟会话部分推迟到第 5 轮**：R3-05 中“模拟会话不能改密或删号”依赖 `sessions.impersonator_id`，该列第 5 轮才加入。第 5 轮加入模拟会话时，必须让 `changePassword` 与 `requestAccountDeletion` 在入口处拒绝模拟会话，并补测试；这里不放无法测试的空判断。
  2. 归属地初始状态在插入时就确定（内网直接 `done`、未配置接口 `skipped`），worker 只处理需要外呼的记录；之后补配接口不会回头补解析已标记 `skipped` 的旧记录。
  3. 错误的当前密码返回 403 `incorrect_password`，不用 401，避免被前端当成会话过期；达到上限返回 429 并带 `Retry-After`。
  4. 清除条件用 grant 的 `expires_at` 而不是“删除请求时间 + 宽限”，因为 grant 的到期时间已经是它的最晚转发时间；效果等价但更精确。
  5. `PaymentNotification` 的 180 天保留（第 13 节）在第 1 轮没有实现，一并由本轮的每小时清理任务补上。
- 本机验收（私有 PostgreSQL 16，Unix socket）：

| 编号 | 结果 | 证据 |
| --- | --- | --- |
| R3-01 | PASS | `TestAccountChangePassword`（错误密码被拒；其他浏览器与 APP 会话 401、当前浏览器保持登录；旧密码不能登录；通知邮件一封；审计）、`TestAccountPasswordAttemptsAreLimited`（第 6 次 429，窗口过后恢复，删号接口共用额度）、`TestAccountChangePasswordKeepsRememberChoice`、`TestAccountPasswordRuleForAdministrators`；浏览器 `account.spec.ts` 第 2 条 |
| R3-02 | PASS | `TestAccountPasswordChangeRacesLogin`：改密先提交，在途的旧密码登录 401 且无新会话；登录先提交，被改密撤销；两次并发改密，慢的一次 409 |
| R3-03 | PASS | `TestAccountDeletionFirstPhase`：登录（网站与 APP）立即 `invalid_credentials`，网站会话 401，APP 会话 `native_session_expired`，租约以 `session_revoked` 结束，grant 为 `revoking`，找回密码不发信，只有一封删号通知 |
| R3-04 | PASS | `TestAccountPurge`（grant 未结清时等待；结清后逐表计数为 0；匿名流水保留且 `order_id` 为空；其他用户数据不变；同一邮箱重新注册没有历史）、`TestAccountPurgeAfterGraceWithoutConfirmation`、`TestAccountPurgeIsAtomic`（外键阻塞时整体回滚并报告，移除后重试成功）、`TestAccountPurgeSingleRunner`；浏览器第 3、4 条（删除后 `purge` 返回 1，用户行消失） |
| R3-05 | 部分 PASS | 活动待付订单阻止删号（`TestAccountDeletionFirstPhase`、`TestAccountDeletingBlocksPaymentFulfilment`、浏览器第 4 条）；管理员不能自助删号（Go 与浏览器第 5 条）；**模拟会话部分 NOT RUN**，见偏离 1 |
| R3-06 | PASS | `TestLoginRecords`（网站与 APP 的成功、失败；平台；不存在的邮箱与刷新不记录）、`TestLoginDoesNotWaitForGeoLookup`（接口挂起时三次登录耗时不受影响且外呼 0 次）、`TestLoginSucceedsWhenRecordCannotBeStored`、`TestRegistrationRecordsAddress`、`TestMyLogins`；`internal/loginlog` 的 `TestWorkerResolvesCachesAndSkipsLocal`（缓存命中不外呼、内网不外呼、令牌头）、`TestWorkerRetriesThenFails`、`TestWorkerRejectsBadAnswers`（重定向、超大、HTML、429、超时）、`TestWorkersDoNotShareARecord`、`TestPrune`、`TestParseLocation` |
| R3-07 | PASS | `TestLoginRecordAddressIgnoresForgedHeaders`：伪造的 `X-Forwarded-For`、`X-Real-IP`、`Forwarded` 不改变记录与最近登录 IP；配置了可信代理后才采用转发地址 |
| 其他 | PASS | `go vet ./...`、`go test -race -count=1 ./...` 全部通过；`config` 的 `TestGeoConfiguration`；前端 `lint`、`typecheck`、`build --webpack`、api-proxy 单测 16 项（含 UA 转发与截断）；`scripts/test-payment-web.py` 12 项浏览器用例（账号 5 项 + 支付 7 项）通过 |

- NOT RUN：真实邮件投递（用捕获 Mailer 验证内容）；真实 IP 归属地服务（用本地假服务验证解析与限速，解析器按 ip-api、ipapi.co、ipwho.is 的常见字段名编写，上线前用所选服务实测一次）；APP 端界面（本轮不改 APP，兼容行为由 `native_session_expired` 的 Go 测试覆盖，四端实机回到登录页留到第 6 轮 R6-02）；`test-node-catalog.py` 的 Mihomo 浏览器用例（本机未运行，由 CI 执行）；旧版本数据副本上的迁移演练（`test-release-rehearsal.py`，留到第 6 轮 R6-01；本轮新列均可空、新表为增量）。
- 证据：本机 `artifacts/payment-round3-account/`（浏览器截图：账号页、改密成功、删号对话框、订单阻止删号），已被 .gitignore 忽略；CI 上传 `payment-web-evidence`。
- 对后续轮次的影响：第 4 轮的用户列表可直接读取 `register_ip`、`last_login_at`、`last_login_ip` 和 `login_events`（含 `geo_status`）；管理员删除用户走 `requestAccountDeletion` 的同一套第一阶段（抽成不依赖当前会话的函数时，保留“活动订单检查”与审计动作名的区别）；第 5 轮见偏离 1 和 `announcement_reads`。
