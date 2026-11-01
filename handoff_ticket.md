# AsterLink VPN：工单系统（Ticket）技术规划交接书

> 文件：handoff_ticket.md  
> 编写日期：2026-10-31  
> 代码基线：项目 /Users/stevenlee/Desktop/vpn，main，HEAD 75f906d（Android N01 修复合并后）。以本次读取的工作区核心源码为准。  
> 文档性质：实施规划与验收约定。本文不代表任何功能已开发或已测试，全文不含代码示例。

## 1. 目标、范围与轮次总览

本次为网站加入“工单系统”：

1. **用户提交**：用户登录后，左侧栏新增“我的工单”。用户可以选择问题分类、填写标题和问题描述来提交工单，之后在同一页面查看客服回复、继续补充说明，或在问题解决后自行关闭工单。
2. **管理员实时处理**：管理后台 /admin/console 新增“工单”标签页。管理员能在 15 秒内看到新提交的工单和用户的新回复（待处理数量以角标显示），可以逐条回复，也可以不回复直接关闭（可选填一句关闭说明）。
3. **保留 1 个月**：每张工单从提交时起保留 30 天，到期后由系统自动删除工单及其全部对话，不可恢复。两端页面都显示删除日期。

架构不变：工单是纯网站功能，与客户端计费、节点、租约、计量账本没有任何交集。四个 APP 都有“复制诊断信息”按钮（macOS、iOS 的账号页，Android 的 copy_diagnostics，Windows 的 CopyDiagnosticsText），用户可以把这段文字粘贴进工单。**本次不改 APP 代码**，第 3 轮只做兼容确认。

采用 **3 轮**实施。每轮一个 worktree 分支和一个 PR，分支命名为 worktree-ticket-roundN-*，每轮的记录追加到本文第 18 节。

| 轮次 | 可验收结果 | 依赖 |
| --- | --- | --- |
| 第 1 轮 | 后端闭环：迁移（两张新表）、状态机、用户接口、管理员接口、限流与幂等、30 天到期清理（维护循环）、账号清除、模拟登录只读、/meta 开关、配置、doctor、Go 测试 | 现有 Go/PG |
| 第 2 轮 | 网站：用户侧 /tickets 页面（列表、提交、详情对话、关闭）与侧边栏未读角标；管理后台“工单”标签页（待处理队列、筛选、详情抽屉、回复、关闭、实时角标）；Playwright 端到端 | 第 1 轮 |
| 第 3 轮 | 全链路回归、发布演练（升级、回滚到 75f906d、再升级）、新旧前后端混合、APP 兼容确认、安全审查、文档与用户实测清单 | 前 2 轮 |

本期不做：
- 附件和截图上传（项目没有对象存储；文字描述加“复制诊断信息”已能覆盖大部分排查需要）。
- 邮件或短信通知（现有 SMTP 只服务于注册和找回密码，authmail 的任务模型绑定验证挑战；改造成通用通知通道是单独的工作。本期用站内未读角标代替）。
- 管理员主动给用户发起工单、工单转派或指派给特定管理员、内部备注。
- 已关闭工单重新打开（用户另开一张即可）。
- 用户长时间不回复时自动关闭。
- 满意度评价、富文本、Markdown、可点击链接。
- APP 内的工单界面（APP 可以通过网站访问）。
- 工单数据导出或在 30 天之外归档。

## 2. 已核实的代码基础与必须衔接的位置

### 2.1 后端

| 位置 | 当前事实 | 对本次的影响 |
| --- | --- | --- |
| backend/internal/api/server.go 路由（newServerWith） | 用户接口挂在 secured 组（requireAuth + readOnlyWhenImpersonating）；管理后台挂在 /admin/console 组（requireConsoleAdmin，模拟会话一律 403） | 用户工单接口挂到 secured 的 /me/tickets 下；管理员接口挂到 console 组的 /tickets 下（第 10 节） |
| server.go 全局中间件 | 请求体默认上限 8192 字节；/admin/nodes 为 128 KB；/admin/console/announcements 为 announcementBodyLimit（32 KB） | 工单正文最多 3000 字，UTF-8 下可达 12 KB，必须给 /api/v1/me/tickets 和 /api/v1/admin/console/tickets 两个前缀放宽到 32 KB，否则长正文会被 413 截断 |
| server.go decode | DisallowUnknownFields，未知字段直接 400 | 新前端只在 /meta 声明支持时才调用工单接口；请求体字段在第 10 节定死，前后端同轮对齐 |
| server.go /meta | 汇总 authMeta、paymentMeta 和 referral_enabled | 新增 tickets_enabled |
| server.go rateLimit | 只有按 IP 的每分钟限流（API_REQUESTS_PER_MINUTE，默认 180） | 不足以防单个账号刷工单，需要按用户的数据库限流（下一行） |
| api/pricing.go bumpLimit / peekLimit | 基于 auth_rate_buckets 表的固定窗口计数，跨实例有效；优惠码和管理员复核密码已在用 | 工单创建和用户回复复用它，新增两个 scope（第 6.4 节） |
| api/announcements.go | 公告标题和正文用 cleanText 校验（合法 UTF-8，不含除换行和制表符以外的控制字符），HTML 和 Markdown 只存不解释；写操作先 lockConsoleAdmin 再锁目标行；consoleAudit 只记长度不记正文 | 工单的文本校验、管理员写操作的锁顺序和审计方式完全照搬 |
| api/admin_coupons.go requireConsoleAdmin / lockConsoleAdmin / consoleAudit | 控制台权限、管理员行锁复核、审计写入 | 直接复用 |
| api/console.go consolePaging / keywordParam / enumParam / likeContains | 控制台分页（20 或 50 条）、关键词长度上限 80、枚举筛选、LIKE 转义 | 管理员工单列表直接复用 |
| api/impersonation.go | 模拟会话只能 GET；refuseImpersonated；myAnnouncements 对模拟会话返回空 | 模拟会话可以只读查看用户的工单，但不能提交、回复、关闭，也不能把回复标记为已读（第 9.2 节） |
| api/payments.go 创建订单 | 使用 Idempotency-Key 请求头（UUID），(user_id, idempotency_key) 唯一；同键不同内容返回 409 idempotency_conflict | 工单创建和每条回复都沿用同样的幂等方式，防止双击重复提交 |
| api/payments.go newTradeNo | 生成随机订单号，冲突时重试 | 工单号的生成方式参照它（第 4.4 节） |
| api/account_purge.go purgeAccount | 在一个事务里按固定顺序删除账号的所有数据，最后删除 users 行 | 增加删除该用户工单（及其消息）的步骤，外键级联兜底旧二进制（第 9.1 节） |
| internal/maintenance/maintenance.go | 维护循环在 advisory lock 708614923 下运行三类清理，每批最多 1000 行、SKIP LOCKED、时间一律来自注入的 Go 时钟；读路径自己检查过期，不依赖循环准时 | 新增第四个清理“工单到期删除”，每 10 分钟一次（第 8 节） |
| internal/store/store.go Migrate | 增量 AutoMigrate 加显式部分索引，在 advisory lock 内执行 | 新表和索引按这个方式加入 |
| internal/operations/readiness.go | doctor 有期望的表清单和各模块只读计数 | 加入两张新表和工单检查项（第 13.3 节） |
| internal/authmail/cleanup.go | auth_rate_buckets 过期行由 authmail.Cleanup 每小时清理，但只在 SMTP 配置完整时运行 | 工单限流桶同样由它清理。无 SMTP 的开发环境里桶行会留存，体量极小，可以接受，写进文档 |
| internal/config/referral.go | 功能开关以“反向布尔”保存，使未经 Load 构造的配置（测试）默认开启 | TICKETS_ENABLED 采用同样写法 |

### 2.2 前端

| 位置 | 当前事实 | 对本次的影响 |
| --- | --- | --- |
| frontend/src/components/site-shell.tsx links / Sidebar | 用户导航依次为总览、节点列表、套餐与订阅、订单记录、推荐有礼、下载客户端、账号设置；推荐入口按 meta 过滤；workspace 判断是**精确路径列表** | 在“推荐有礼”之后、“下载客户端”之前加入“我的工单”；只有 meta.tickets_enabled 为 true 且当前用户 role 为 user 时显示；/tickets 加入 workspace 列表。**工单详情不能用 /tickets/[id] 动态路由**，否则精确匹配失效、侧边栏消失，所以详情用同页的 ?id= 参数 |
| frontend/src/components/support-widget.tsx supportPages | 在线客服（Crisp）只出现在 8 个用户页面上（精确路径） | /tickets 加入列表，工单页同样提供在线客服入口；两者互补：在线客服适合即时咨询，工单适合需要排查和留存记录的问题 |
| frontend/src/components/console/console-shell.tsx tabs | 用户管理、订单与支付、套餐管理、登录记录、优惠码、公告 | 在“订单与支付”之后加入“工单”，标签上显示待处理数量角标；页面说明文字同步加入“工单” |
| frontend/src/components/console/console-common.tsx useConsoleList / useFetched | 筛选条件保存在 URL 中，分页，按 nonce 重新加载 | 管理员工单列表复用；详情抽屉用 ?ticket= 参数，可分享、刷新后保留 |
| frontend/src/lib/use-polling.ts | 页面可见时定时刷新，聚焦、回到页面、网络恢复时立即刷新；同一时间只有一个请求；隐藏时暂停 | 用户详情、管理员列表、管理员详情和角标全部用它实现“实时” |
| frontend/src/app/api/v1/[...path]/route.ts | Next 代理按路径决定请求体上限：默认 8192，公告 32 KB；**上游响应被整体读入缓冲区**（最多 512 KB）后才返回，并有总超时 | 工单两个前缀的请求体上限放宽到 32 KB，与后端一致。响应被整体缓冲，意味着 SSE 或长连接无法穿过这层代理，这是“实时”采用轮询的直接原因（D7） |
| frontend/scripts/copy-guard.test.mjs、tests/copy-words.ts | 用户页面源码和运行时文案禁止出现“开发、测试、模拟、后端、数据库、联系管理员”等词；console 组件不扫描 | 用户侧工单页面一律把回复方称为“客服”，不出现“管理员”；分类、状态、提示文案都要避开禁用词 |
| frontend/src/lib/types.ts Meta | 已有 referral_enabled 等可选字段 | 新增 tickets_enabled 和工单相关类型 |
| frontend/playwright.payment.config.ts | payment 配置跑 payment、account、admin-console 系列、shop、referral、ui-copy、theme、home、support-widget 等 spec，夹具有管理员和普通账户 | 新增 tickets.spec.ts 加入该配置 |
| .github/workflows/ci.yml Web validation | 逐条执行 lint、build（--webpack）、typecheck 和各个 node 单测 | 新增 npm run test:tickets 并加入该步骤 |

### 2.3 APP

四个平台都不需要修改。工单只在网站上使用；APP 的“复制诊断信息”内容只有应用版本、系统版本、连接状态和错误码，适合直接粘贴进工单。第 3 轮确认 APP 的现有契约测试不受新表、新路由影响。

## 3. 关键决策（默认值，用户可在第 1 轮开工前推翻）

| 编号 | 决策 | 理由 |
| --- | --- | --- |
| D1 | 分类固定为 5 个：connection（连接与节点）、account（账号与登录）、billing（套餐、订单与支付）、client（客户端下载与安装）、other（其他问题）。分类只用于管理员筛选和排序参考，用户必须选一个 | 对应本项目用户实际会遇到的问题域：节点连不上、登录或验证码问题、付款未到账、客户端装不上。类目少，用户不会犹豫；推荐奖励、优惠码归入 billing |
| D2 | 提交时另有两个**选填**项：“使用的设备”（Windows、macOS、Android、iOS、网页、其他）和“关联订单”（从自己最近 20 笔订单里选，显示订单号、套餐名和金额）。billing 分类下默认展开“关联订单”，connection 和 client 分类下默认展开“使用的设备” | 这两项是排查时管理员最常追问的信息。订单只能选自己的，服务端复核归属 |
| D3 | 状态只有三个：open（待处理，等客服）、answered（已回复，等用户）、closed（已关闭，终态）。用户回复使状态回到 open；客服回复使状态变为 answered；任一方关闭后为 closed，双方都不能再回复，也不能重新打开 | 状态越少越不容易出错。closed 作为终态，避免“关了又开”的复杂规则；用户有新问题另开一张 |
| D4 | 保留期 = 提交时间 + 30 天（固定 720 小时，不随状态或后续回复延长），写入工单的 expires_at 列。到期后读接口立即视为不存在，维护循环随后物理删除工单和全部消息。不提供配置项 | 需求原文是“递交的工单保留 1 个月”。按提交时间算规则最简单，两端可以准确显示删除日期；固定值避免改配置后已有工单的期限变得说不清 |
| D5 | 每个普通账号同时最多 3 张未关闭（open 或 answered）的工单；每 24 小时最多创建 5 张；每小时最多发送 30 条回复；每张工单最多 100 条消息（达到上限后只能关闭或另开）；标题 1–60 字，正文 1–3000 字，纯文本 | 防止滥用和刷屏，同时远高于正常用户的需求。数值写成后端常量，第 1 轮测试逐一覆盖边界 |
| D6 | 用户端把回复方统一显示为“客服”，从不显示管理员的邮箱、姓名或 id；管理员端可以看到每条回复是哪位管理员发出的 | 保护内部账号信息；同时满足复制词守卫（用户页面禁止出现“联系管理员”等字样） |
| D7 | “实时”用轮询实现：管理员工单列表和控制台角标每 15 秒刷新一次，正在查看的工单详情每 10 秒刷新一次；用户侧详情每 20 秒，侧边栏未读角标每 60 秒以及每次切换页面时刷新。页面不可见时暂停，回到页面立即刷新 | Next 代理把上游响应整体缓冲后再返回，SSE 和长连接都无法穿过；改代理的风险和成本远大于收益。15 秒以内看到新工单，对人工客服已经是“实时”。轮询走现有 usePolling，单请求、可取消 |
| D8 | 未读只做一个方向的强提醒：客服回复或关闭工单时，工单的 user_unread 置为 true；用户打开该工单详情后由前端发一次“标记已读”，置为 false。管理员侧不做已读状态，以 status=open 作为“需要处理”的唯一依据 | 用户需要知道“客服回我了”；管理员只需要一个待处理队列。多管理员共享同一队列时，“谁读过”没有意义 |
| D9 | 管理员关闭工单时可选填“关闭说明”（最多 500 字）。填写时它作为一条客服消息追加，再关闭；不填写时直接关闭，用户看到“客服已关闭此工单”。用户也可以自行关闭（“问题已解决”） | 满足“可以不回复直接关闭” |
| D10 | 管理员关闭工单时必须带上当前看到的 version；如果期间用户发了新消息（version 已变），返回 409，提示“用户有新回复，请先查看”。管理员回复则不需要 version，总是追加 | 防止管理员没看到用户的最新补充就把工单关掉；回复不设版本门槛，避免两位管理员同时处理时一方反复被打回 |
| D11 | 管理员账号不能在用户侧提交工单（入口隐藏，接口返回 403 admin_cannot_submit）；模拟登录期间可以只读查看被模拟用户的工单 | 管理员自己给自己开工单没有意义；只读查看与模拟登录现有的语义一致，便于排查 |
| D12 | 工单号为“T + 6 位 UTC+8 日期 + 6 位随机数字”，共 13 位，例如 T261031482913，唯一索引，冲突时重试。界面和在线客服沟通里都用工单号，接口另外返回 uuid 作为路由参数 | 口头和在线客服里报一个短号比报 uuid 方便；日期部分能让管理员一眼看出时间。UTC+8 与控制台的 consoleLocation 一致 |
| D13 | 总开关 TICKETS_ENABLED 默认开启。关闭时：/meta 返回 false，用户侧入口隐藏，创建、回复、用户关闭一律 403 tickets_disabled；用户仍能只读查看已有工单；管理员控制台照常可回复和关闭；到期清理照常运行 | 关闭开关用于临时停止受理，已有工单仍要能收尾，保留期规则也不能因开关而失效 |
| D14 | 工单正文从不写入日志、审计或 doctor 输出。审计只记录工单 id、动作、正文长度和关闭方式 | 工单里可能有邮箱、订单号等个人信息，只应存在于工单表里，并随 30 天到期一起删除 |

## 4. 工单模型与状态机

### 4.1 状态转换

| 当前状态 | 动作 | 结果 |
| --- | --- | --- |
| （无） | 用户提交 | open，写入首条用户消息，user_unread=false |
| open | 用户补充 | 仍为 open，追加用户消息 |
| open | 客服回复 | answered，追加客服消息，user_unread=true |
| answered | 用户回复 | open，追加用户消息 |
| answered | 客服再次回复 | 仍为 answered，追加客服消息，user_unread=true |
| open 或 answered | 客服关闭（可带说明） | closed，closed_by=staff；带说明时先追加客服消息；user_unread=true |
| open 或 answered | 用户关闭 | closed，closed_by=user；user_unread 不变 |
| closed | 任何写操作 | 409 ticket_closed |
| 任意 | 到达 expires_at | 读接口返回 404 ticket_not_found（列表中不再出现）；随后被物理删除 |

每次状态或消息变化都让 version 加 1，并更新 last_message_at；用户消息另外更新 waiting_since（工单最近一次进入 open 的时间，用于管理员队列“等待最久优先”）。

### 4.2 消息

- 消息作者分为 user 和 staff 两类。staff 消息记录发出它的管理员 id（不设外键），只在管理员接口返回。
- 消息不能编辑、不能删除（单条）。用户发错可以再补一条。
- 消息按创建时间升序展示；同一时刻按 id 排序。

### 4.3 文本规则

- 标题：去掉首尾空白后 1–60 字，单行（不能含换行），纯文本。
- 正文和回复：去掉首尾空白后 1–3000 字；关闭说明 1–500 字。允许换行和制表符。
- 校验复用 announcements.go 中的 cleanText：必须是合法 UTF-8，不能含其他控制字符。HTML、Markdown 和链接只按原文存储和显示，任何一端都不解释，也不自动生成可点击链接（防钓鱼）。
- 长度按 Unicode 字符计数（与公告一致），不是按字节。

### 4.4 工单号

按 D12 生成：日期部分取创建时刻的 UTC+8 日期，随机部分用 crypto/rand 生成 6 位数字。唯一冲突时重新生成，最多 5 次；仍失败返回 503 tickets_unavailable。同一天的容量为 100 万，远超实际需要。

## 5. 数据模型与迁移

全部是增量迁移，在现有 Migrate 的 advisory lock 和事务内完成，重复执行安全。不修改任何现有表。

### 5.1 新表 tickets

| 列 | 说明 |
| --- | --- |
| id | uuid 主键 |
| ticket_no | 13 位工单号，唯一索引 |
| user_id | 提交人，非空，外键指向 users，ON DELETE CASCADE（兜底旧二进制的账号清除）。与 created_at 组成复合索引，用于用户列表按时间倒序 |
| idempotency_key | 创建请求的幂等键（uuid），与 user_id 组成唯一索引 |
| category | 5 个分类之一，CHECK 约束 |
| platform | 空字符串或 6 个设备值之一，CHECK 约束 |
| order_id | 关联订单，可空，不设外键（订单只会随账号一起被清除，而工单此时也一同删除） |
| subject | 标题，最长 60 字（列宽按字节留足 240） |
| status | open、answered、closed，CHECK 约束 |
| user_unread | 布尔，非空，默认 false |
| message_count | 非空，默认 0，CHECK 0–100 |
| waiting_since | 最近一次进入 open 的时间，可空（closed 和 answered 时保留上一次的值，仅供参考） |
| last_message_at | 最后一条消息时间，非空 |
| closed_at、closed_by | 关闭时间和关闭方（user 或 staff），可空 |
| closed_by_admin_id | 关闭工单的管理员 id，可空，不设外键，只在管理员接口返回 |
| version | 非空，默认 1 |
| created_at、updated_at | 由注入的时钟写入（autoCreateTime 和 autoUpdateTime 都关闭） |
| expires_at | created_at + 30 天，非空 |

显式索引（谓词只用常量，与现有部分索引写法一致）：
- 管理员待处理队列：在 waiting_since 上建部分索引，条件为 status = 'open'。
- 到期清理：expires_at 普通索引。
- 管理员“全部”列表按 last_message_at 倒序：普通索引。

### 5.2 新表 ticket_messages

| 列 | 说明 |
| --- | --- |
| id | uuid 主键 |
| ticket_id | 非空，外键指向 tickets，ON DELETE CASCADE |
| position | 该消息在工单内的序号，从 1 开始，在工单行锁内取 message_count + 1，CHECK 1–100；与 ticket_id 组成唯一索引。对话按它排序，不依赖时间戳（同一时刻写入的两条消息顺序也确定）。第 1 轮实施时加入 |
| author_kind | user 或 staff，CHECK 约束 |
| author_id | 作者的用户 id，不设外键（管理员账号的生命周期与工单无关） |
| body | text，非空 |
| idempotency_key | 回复请求的幂等键，可空；与 ticket_id 组成唯一索引（首条消息使用工单的幂等键，不另存） |
| created_at | 由注入的时钟写入 |

### 5.3 体量估算

30 天保留天然限制了表的大小。以每天 200 张工单、每张平均 6 条、每条 1 KB 计，稳态约 6000 张工单、3.6 万条消息、约 40 MB，不需要分区或归档。

## 6. 用户侧流程与规则

### 6.1 提交

1. 前端带 Idempotency-Key 请求头（每次打开提交表单时生成一个 uuid，失败重试时沿用）。
2. 服务端在事务外完成格式校验（第 4.3 节）、开关检查（D13）、角色检查（D11），然后查 peekLimit 判断 24 小时创建次数。
3. 事务内：锁当前用户行（User 在最前，符合全局锁顺序）→ 按 (user_id, idempotency_key) 查找已有工单：存在且内容相同则直接返回它（200）；内容不同返回 409 idempotency_conflict → 统计未关闭且未到期的工单数，达到 3 返回 409 too_many_open_tickets → 选填订单时确认订单属于当前用户，否则 400 invalid_order → 生成工单号，写入工单和首条消息，status=open，waiting_since=last_message_at=created_at。
4. 提交成功后 bumpLimit 计数（独立语句，失败不影响已创建的工单，只记日志类别）。被拒绝的请求不计数，避免用户因为校验错误而被锁住。
5. 返回 201 和工单详情。

### 6.2 回复

- 带 Idempotency-Key。事务内锁用户行 → 锁工单行（必须属于当前用户、未到期，否则 404）→ 检查 closed（409）、消息数上限（409 ticket_message_limit）→ 同键重放直接返回已有消息 → 追加消息，status=open，waiting_since 在由 answered 转为 open 时更新为当前时间，version+1。
- 每小时 30 条的限流同样用 peekLimit / bumpLimit，scope 为 ticket_message。

### 6.3 关闭与已读

- 用户关闭：锁用户行 → 锁工单行 → 未关闭时置为 closed、closed_by=user、version+1；已关闭时直接返回当前状态（幂等，200）。
- 标记已读：只把 user_unread 置为 false，不改 version，不受开关影响。模拟会话会被中间件 403 拒绝，前端忽略这个失败。

### 6.4 限流 scope

| scope | 键 | 窗口 | 上限 |
| --- | --- | --- | --- |
| ticket_create | 用户 id | 24 小时 | 5 |
| ticket_message | 用户 id | 1 小时 | 30 |

超限返回 429 ticket_rate_limited，带 Retry-After，前端显示“操作太频繁，请于 X 分钟后再试”。

## 7. 管理员侧流程

### 7.1 待处理队列与“实时”

- 列表默认筛选“待处理”（status=open），按 waiting_since 升序（等待最久的排在最前），同时间按 id。其他筛选：已回复、已关闭、全部（全部按 last_message_at 倒序）；分类；关键词（匹配工单号前缀、用户邮箱包含、标题包含，LIKE 转义）。
- 摘要接口返回 open_count 和最早的 waiting_since。控制台“工单”标签显示 open_count 角标；角标大于 0 时，左侧栏“管理后台”入口旁显示一个小圆点。两者每 15 秒刷新（D7）。
- 列表每 15 秒刷新；有新的待处理工单进入首页时，以浅色高亮该行 5 秒，不弹窗、不播放声音。

### 7.2 详情

- 详情抽屉显示：工单号、分类、设备、关联订单（订单号、套餐、金额、状态，点击跳到订单与支付页并打开该订单）、提交人（邮箱、账号状态、当前套餐和到期时间，点击跳到用户管理页并打开该用户）、创建时间、删除日期、全部消息（staff 消息标注是哪位管理员）。
- 打开期间每 10 秒刷新一次。**刷新不能清空正在输入的回复框**；收到新的用户消息时，在对话底部提示“用户有新消息”。

### 7.3 回复与关闭

- 回复：事务内 lockConsoleAdmin（管理员行，第一把锁）→ 锁工单行 → 未到期、未关闭、未达消息上限 → 追加 staff 消息，status=answered，user_unread=true，version+1 → 写审计 admin.ticket_reply（只记工单 id 和正文长度）。回复同样带 Idempotency-Key，防止双击。
- 关闭：请求带 version 和可选的关闭说明。事务内 lockConsoleAdmin → 锁工单行 → version 不一致返回 409 ticket_changed → 有说明时追加 staff 消息 → status=closed，closed_by=staff，closed_by_admin_id，user_unread=true，version+1 → 写审计 admin.ticket_close（记录 with_note 和说明长度）。
- 提交人账号处于 deleting 状态时，详情页显示“账号注销中”，回复按钮禁用（接口返回 409 user_unavailable），仍允许关闭。

## 8. 30 天保留与自动删除

- **读路径即时生效**：所有用户接口和管理员接口都带上 expires_at > 当前时间（注入时钟）。到期那一刻起，工单从列表消失，详情和写操作返回 404。正确性不依赖清理循环是否准时，这与维护循环“读者自己检查过期”的原则一致。
- **物理删除**：在 maintenance.Sweeper 中新增“工单到期删除”清理，每 10 分钟一次（TicketInterval，Sweeper 上可注入，测试中缩短为 1 秒），在已有的 advisory lock 708614923 下执行。每批删除至多 1000 张 expires_at 早于当前时间的工单，使用 FOR UPDATE SKIP LOCKED；消息随外键级联删除。循环直到某批不足 1000 张。
- **边界**：正好等于 expires_at 视为已到期。用户正在写回复的那一刻工单到期，提交返回 404，前端提示“该工单已超过保留期限并被删除”，并刷新列表。
- **展示**：两端都显示“将于 YYYY-MM-DD HH:mm 自动删除”（浏览器本地时区，悬停显示 UTC）。剩余不足 3 天时，用户侧在工单上显示黄色提示“即将删除，如需保留请自行复制重要内容”。
- **结果记录**：清理只写一条计数日志（“删除 N 张到期工单”），不记工单号或用户。doctor 报告“已到期但尚未删除超过 1 小时”的数量，大于 0 时给出警告，说明清理循环可能没有运行。

## 9. 账号删除、模拟登录与权限

### 9.1 账号删除

- purgeAccount 在删除 orders 之前增加一步：删除 user_id 为该账号的全部工单（消息级联删除）。
- 账号进入 deleting 后、清除之前：用户已不能登录；管理员仍能在后台看到这些工单（标注“账号注销中”），可以关闭，不能回复。
- 外键 ON DELETE CASCADE 保证旧二进制的清除流程（不认识工单表）直接删除 users 行时不会失败。

### 9.2 模拟登录

- 模拟会话访问 /tickets 能看到被模拟用户的工单列表和详情（GET），页面顶部仍是现有的只读横幅；提交、回复、关闭、标记已读都会被 readOnlyWhenImpersonating 拒绝。前端在模拟期间隐藏这些按钮，并且不发送“标记已读”。
- 模拟会话不能访问管理员工单接口（requireConsoleAdmin 已拒绝）。

### 9.3 越权

- 用户接口的每一条查询都同时带 user_id = 当前用户的条件。访问别人的工单一律返回 404（不是 403），不暴露工单是否存在。
- 路由参数不是合法 uuid 时直接 404，不查库。
- 管理员接口只对 role=admin、status=active 的非模拟会话开放；写操作在事务内再用 lockConsoleAdmin 复核。

## 10. API 契约

| 接口 | 权限 | 说明 |
| --- | --- | --- |
| GET /api/v1/meta | 公开 | 新增 tickets_enabled（布尔值） |
| GET /api/v1/me/tickets?page=N&status=active/closed | 登录用户（模拟会话可读） | 每页 20 条，页码上限 10000。active 表示 open 和 answered，按 last_message_at 倒序；closed 按 closed_at 倒序；不传时为全部。顶层字段：tickets、total、page、page_size、open_count、max_open、order_options。每条工单只有：id、ticket_no、category、subject、status、user_unread、message_count、created_at、last_message_at、expires_at。order_options 是最近 20 笔订单（id、trade_no、plan_name、price_cents、status、created_at），供提交表单的“关联订单”使用 |
| GET /api/v1/me/tickets/summary | 登录用户 | 返回 unread_count（user_unread 为 true 的未到期工单数）、open_count（未关闭工单数）和 max_open（3）。侧边栏角标使用。功能关闭时仍可调用 |
| POST /api/v1/me/tickets | 登录普通用户，需 Idempotency-Key | 请求体：category、subject、body、platform（可省略或为空）、order_id（可省略或为 null）。201 返回工单详情 |
| GET /api/v1/me/tickets/:id | 登录用户（模拟会话可读） | 列表字段加 platform、closed_at、closed_by（user 或 staff）、order 摘要（与 order_options 同形，未关联时为 null）和 messages（每条只有 id、author：user 或 staff、body、created_at）。不含任何管理员身份信息，不含 closed_by_admin_id。写接口（创建、回复、关闭）成功后都返回这个形状的 ticket，另带 replayed 表示是否为幂等重放 |
| POST /api/v1/me/tickets/:id/messages | 登录普通用户，需 Idempotency-Key | 请求体：body。201 返回新消息和工单的新状态 |
| POST /api/v1/me/tickets/:id/close | 登录普通用户 | 请求体为空对象。200 返回工单 |
| POST /api/v1/me/tickets/:id/read | 登录用户（模拟会话 403） | 请求体为空对象。200 |
| GET /api/v1/admin/console/tickets | 管理员 | 参数：page、page_size（20 或 50）、status（open、answered、closed、all，默认 open）、category、keyword。每条加上 user_id、user_email、user_status、waiting_since |
| GET /api/v1/admin/console/tickets/summary | 管理员 | open_count、oldest_waiting_since |
| GET /api/v1/admin/console/tickets/:id | 管理员 | 完整详情：用户摘要（邮箱、状态、当前套餐名和到期时间）、关联订单摘要（含订单 id 和状态）、version，每条 staff 消息带管理员邮箱，closed_by_admin 邮箱 |
| POST /api/v1/admin/console/tickets/:id/messages | 管理员，需 Idempotency-Key | 请求体：body。201 |
| POST /api/v1/admin/console/tickets/:id/close | 管理员 | 请求体：version、note（可省略或为空）。200 |

错误码（新增）：tickets_disabled（403）、admin_cannot_submit（403）、invalid_ticket（400，附带 field 指出哪个字段）、invalid_order（400）、too_many_open_tickets（409）、ticket_closed（409）、ticket_message_limit（409）、ticket_changed（409）、idempotency_conflict（409）、idempotency_required（400）、ticket_rate_limited（429）、ticket_not_found（404）、user_unavailable（409）、tickets_unavailable（503，数据库不可用或工单号连续冲突）。管理员接口的数据库错误沿用 console_unavailable。

## 11. 前端

### 11.1 侧边栏与角标

- “我的工单”入口（图标用 tabler 的 IconMessageCircleQuestion 或同类），位于“推荐有礼”之后。显示条件：meta.tickets_enabled 为 true、当前用户 role 为 user（模拟登录时被模拟的是普通用户，所以也显示）。
- 有未读回复时，入口右侧显示数字角标（超过 9 显示 9+）。角标数据来自 /me/tickets/summary：每 60 秒、每次切换页面、回到页面时刷新；请求失败时隐藏角标，不打扰用户。该请求使用 useFetched 的 quiet 模式（401 不触发登出），与公告检查相同。
- 管理员的侧边栏不显示“我的工单”，而在“管理后台”入口旁按 7.1 节显示小圆点。

### 11.2 用户页面 /tickets

- 外层 RequireAuth + SiteShell；meta 未声明 tickets_enabled 时显示“工单服务暂未开放”（与推荐页对旧后端的处理一致）；为 false 时只读显示已有工单，并隐藏“提交工单”按钮。
- **列表视图**（无 ?id= 参数）：顶部说明文字“提交问题后客服会尽快回复，工单自提交起保留 30 天，到期自动删除。紧急问题可以使用在线客服。”；“提交工单”按钮（未关闭工单已有 3 张时禁用并说明原因）；“处理中 / 已关闭”两个分段；表格列为工单号、标题、分类、状态、最后更新、删除日期；有未读回复的行加粗并带圆点。375 宽视口改为卡片列表。没有工单时显示空状态和“提交工单”按钮。
- **提交表单**（模态框）：分类（必选，单选卡片或下拉）、标题、问题描述（多行，显示字数“0/3000”）、使用的设备（选填）、关联订单（选填，选项来自列表接口的 options；没有订单时不显示）。描述的占位提示：“请描述遇到的问题、出现的时间和操作步骤。连接类问题可以在客户端“账号”页点击“复制诊断信息”，粘贴到这里。”提交按钮在请求期间禁用；同一次打开表单使用同一个幂等键。
- **详情视图**（?id=工单 id）：返回列表的链接；工单号、分类、状态标签、创建时间、删除日期；对话气泡（用户消息靠右，客服消息靠左并标注“客服”），文本以 pre-wrap 纯文本渲染；底部回复框（closed 时替换为“工单已关闭”说明）；“问题已解决，关闭工单”按钮（需二次确认）。打开详情后，如果 user_unread 为 true，就发送一次“标记已读”，成功后刷新角标。详情每 20 秒刷新，刷新不清空回复框。
- 状态文案：open 显示“等待客服回复”，answered 显示“客服已回复”，closed 显示“已关闭”（关闭方为 staff 时附“由客服关闭”）。
- 所有文案经过复制词守卫，不出现“管理员”“测试”“后端”等词。

### 11.3 管理后台“工单”标签

- 路径 /admin/console/tickets，标签位于“订单与支付”之后，标签文字带 open_count 角标。
- 列表：筛选条（状态、分类、关键词，保存在 URL）；列为工单号、标题、分类、用户邮箱、状态、等待时长（open 时显示“已等待 X 分钟/小时”）、消息数、最后更新、删除日期；点击行打开详情抽屉（?ticket=id）。
- 详情抽屉按 7.2 节；回复框下方两个按钮：“发送回复”和“关闭工单”。“关闭工单”打开确认框，内含选填的关闭说明，提交时带上抽屉里的 version；收到 409 ticket_changed 时提示并自动刷新详情。
- 控制台页面说明文字更新为“用户、订单与支付、工单、套餐、登录记录、优惠码、公告。”

### 11.4 其他

- lib/types.ts 补充 Ticket、TicketDetail、TicketMessage、TicketStatus、TicketCategory 和 meta 字段；分类、设备、状态的中文名放在 lib/tickets.ts 中，并为纯函数（文案映射、剩余天数、等待时长格式化、字数统计）写 scripts/tickets.test.mjs（npm run test:tickets，加入 CI）。
- Next 代理：/api/v1/me/tickets 和 /api/v1/admin/console/tickets 前缀的请求体上限为 32 KB，test:api-proxy 增加对应用例。
- supportPages 加入 /tickets；site-shell 的 workspace 列表加入 /tickets。
- 首页 FAQ“如何联系客服”一项补一句“登录后可在‘我的工单’提交问题”。

## 12. 并发、锁顺序与性能

- 用户写操作：User(本人) → Ticket。创建时锁用户行，保证“最多 3 张未关闭工单”在并发下也成立。
- 管理员写操作：User(管理员本人，lockConsoleAdmin) → Ticket。管理员从不锁工单所属用户的行；用户从不锁管理员的行，因此不会成环。
- 账号清除：User(被清除者) → 删除其工单。与管理员同时操作同一工单时，管理员拿着工单行锁，清除等待；或清除先完成，管理员拿锁时工单已不存在，返回 404。两种顺序都不会死锁。
- 到期清理：SKIP LOCKED，跳过正被用户或管理员写入的工单，下一轮再删；不与任何写操作相互等待。
- 同一工单上用户回复与管理员关闭并发：两者串行于工单行锁；先回复则关闭因 version 变化返回 409（D10）；先关闭则回复返回 409 ticket_closed。
- 查询：用户列表走 (user_id, created_at) 复合索引；管理员待处理队列走 waiting_since 部分索引；摘要计数在 30 天保留下数据量很小。所有列表接口不返回消息正文，详情一次性返回最多 100 条消息。
- 轮询负载：每位在线管理员每 15 秒两次轻量查询（列表 + 摘要）；每位在线用户每 60 秒一次摘要计数。均远低于现有 API 限流。

## 13. 安全、隐私与运维

### 13.1 安全

- XSS：工单内容在两端都以纯文本渲染，不使用任何 HTML 注入方式；不自动生成链接。
- CSRF：沿用现有中间件（写请求必须带可信 Origin 且为 JSON）。
- 越权：第 9.3 节。
- 枚举：工单 id 是 uuid；不属于自己的工单一律 404；工单号只用于显示和管理员搜索，用户接口不支持按工单号查询。
- 滥用：第 6.4 节的限流，加上按 IP 的全局限流和请求体上限。
- 管理员身份不外泄：用户接口的字段白名单不含任何管理员 id、邮箱、姓名。

### 13.2 配置（backend/internal/config，带校验）

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| TICKETS_ENABLED | true | 总开关，见 D13。以“反向布尔”保存，测试构造的配置默认开启 |

保留期（30 天）和各项上限都是代码常量，不提供环境变量（D4、D5）。

### 13.3 doctor

期望的表清单加入 tickets 和 ticket_messages。新增只读检查项：开关状态、open 数、answered 数、最早的待处理等待时长（超过 24 小时给出警告 tickets_waiting_over_24h）、已到期但尚未删除超过 1 小时的数量（大于 0 时警告 tickets_expiry_lagging）。只输出数量和时长，不输出工单号、标题、邮箱或正文。

## 14. 兼容、发布与回滚

### 14.1 新旧组合

| 组合 | 结论与保证手段 |
| --- | --- |
| 新前端 + 旧后端 | 旧 /meta 中没有 tickets_enabled：侧边栏不显示“我的工单”，摘要请求不发出；直接访问 /tickets 显示“工单服务暂未开放”；管理后台的“工单”标签在摘要接口返回 404 时隐藏角标，页面显示“当前版本暂不支持工单” |
| 旧前端 + 新后端 | 旧前端不调用任何工单接口，行为不变 |
| 回滚到旧二进制（75f906d） | AutoMigrate 不会删除新表。旧二进制没有工单路由，旧前端也没有入口，工单只是暂时不可见。旧二进制的账号清除直接删除 users 行时，外键级联删除其工单。旧二进制不运行工单到期清理；再次升级后，读路径立即隐藏已到期工单，第一轮清理把它们删除 |
| APP | 不改代码。新表和新路由不影响 /client 系列接口和契约测试 |

### 14.2 发布顺序

先发布后端（迁移在启动时自动执行），确认 /meta 出现 tickets_enabled 后，再发布前端。

### 14.3 回滚

可以只回滚前端，也可以连同后端一起回滚，不需要手动处理数据。回滚期间用户无法提交工单；在线客服不受影响。

## 15. 三轮实施与逐轮验收

每轮都要交付实现、测试和第 18 节的记录。未执行的项必须如实标注 NOT RUN 或 BLOCKED 并写明原因，不能写成 PASS。证据存放在 artifacts/ticket-roundN-*，内容包括命令、输出摘要、截图和关键 SQL 计数，不能包含完整邮箱、工单正文或任何密钥。

### 第 1 轮：后端闭环

工作项：
- 第 5 节的模型、迁移和索引；
- 工单模块：文本校验、工单号生成器（可注入，便于制造冲突）、状态机；
- 用户接口与管理员接口（第 10 节），含幂等、限流、开关、角色检查；
- 请求体上限（Go 中间件）；
- maintenance 的工单到期清理（第 8 节）；
- purgeAccount 的工单步骤（第 9.1 节）；
- 配置项、/meta、doctor；
- 本交接书随本轮 PR 纳入版本库。

| 编号 | 验收项 |
| --- | --- |
| T1-01 | 迁移：在含旧数据的副本上重复迁移两次，现有表逐值不变；新表、外键（两处 CASCADE）、CHECK 约束和三个索引存在；EXPLAIN 显示管理员待处理队列走部分索引；doctor 报告新表和新检查项 |
| T1-02 | 创建：5 个分类、6 个设备值和空值都能创建；非法分类、非法设备、标题 0 字和 61 字、标题含换行、正文 0 字和 3001 字、含控制字符、非 UTF-8 都返回 400 invalid_ticket 并指明字段；3000 个汉字的正文（约 9 KB）能通过 Go 中间件；关联别人的订单返回 400 invalid_order；工单号格式正确；注入一个先返回已存在号码的生成器能重试成功，连续 5 次冲突返回 503 |
| T1-03 | 幂等与上限：同一 Idempotency-Key 重放返回同一张工单（200），内容不同返回 409；缺少或格式错误的 Idempotency-Key 返回 400；第 4 张未关闭工单返回 409 too_many_open_tickets，关闭一张后可以再建；两个并发请求同时创建第 3、4 张，结果恰好一成功一失败；24 小时内第 6 次创建返回 429 并带 Retry-After；被校验拒绝的请求不消耗次数 |
| T1-04 | 状态机：按 4.1 节的表逐行驱动，每个转换后的 status、version、waiting_since、last_message_at、user_unread、message_count 都正确；closed 后用户回复、管理员回复、再次关闭（管理员）都返回 409；用户再次关闭为幂等 200；第 101 条消息返回 409；每小时第 31 条用户回复返回 429 |
| T1-05 | 管理员：回复使状态变为 answered 并写审计（审计中只有长度）；带说明关闭会先追加消息再关闭，不带说明直接关闭；version 过期时关闭返回 409 ticket_changed；两位管理员同时回复都成功且顺序确定；提交人处于 deleting 时回复返回 409 user_unavailable、关闭成功；普通用户和模拟会话访问管理员接口返回 403；被撤销管理员角色的会话在事务内被拒绝 |
| T1-06 | 字段白名单：用户接口返回的字段集合与第 10 节完全一致，不含 closed_by_admin_id、author_id、管理员邮箱；访问别人的工单和不存在的工单都返回 404 且响应体相同；非 uuid 参数直接 404 |
| T1-07 | 到期：用注入时钟把时间推进到 expires_at 前 1 秒，工单可见可写；推进到正好 expires_at，列表不再出现，详情和写操作返回 404，summary 不再计数；运行一次清理后工单和消息行都被删除；构造 2500 张到期工单，清理分批删除完毕；清理时有一张工单被另一个事务锁住，本轮跳过它，下一轮删除；两个实例同时运行清理时只有一个执行 |
| T1-08 | 账号清除：用户有多张工单和消息时，purgeAccount 后全部删除；直接删除 users 行（模拟旧二进制），外键级联生效；deleting 状态下管理员可见并标注 |
| T1-09 | 模拟登录：模拟会话可以 GET 列表和详情；创建、回复、关闭、标记已读都返回 403 impersonation_read_only；模拟期间 user_unread 不变 |
| T1-10 | 开关：TICKETS_ENABLED=false 时 /meta 为 false，创建、回复、用户关闭返回 403 tickets_disabled，列表、详情、标记已读、管理员回复和关闭照常，到期清理照常；管理员账号在用户侧创建返回 403 admin_cannot_submit |
| T1-11 | 隐私：在测试中开启日志捕获，完整走一遍创建、回复、关闭、清理，日志和审计 detail 中不出现正文、标题和邮箱；doctor 输出中也没有 |
| T1-12 | 现有回归：Go 全量竞态测试（test-native.py，超时 20 分钟）、test-node-catalog.py、test-payment-web.py 都通过；native 契约测试不需要修改即可通过 |

### 第 2 轮：网站（用户页面、侧边栏角标、管理后台）

工作项：
- 第 11 节的全部界面、类型和纯函数单测；
- Next 代理的请求体上限；
- 新增 tickets.spec.ts，加入 playwright.payment.config.ts；测试夹具让维护循环的工单清理间隔为 1 秒，并在 Go 测试装配里提供一个仅测试二进制存在的“把某张工单的 expires_at 改到过去”钩子；
- CI 加入 npm run test:tickets。

| 编号 | 验收项 |
| --- | --- |
| T2-01 | 需求主流程端到端：普通用户 A 登录，侧边栏出现“我的工单” → 提交一张 billing 工单并关联自己的一笔订单 → 列表出现该工单，状态“等待客服回复” → 管理员在另一个浏览器上下文打开管理后台，15 秒内“工单”标签角标变为 1，列表首行出现该工单（不手动刷新） → 管理员打开详情，看到关联订单和用户信息，发送回复 → 60 秒内 A 的侧边栏出现未读角标 1（或切换页面后立即出现） → A 打开详情看到“客服”的回复，角标消失 → A 补充一条 → 管理员详情 10 秒内出现“用户有新消息”，状态回到待处理 |
| T2-02 | 不回复直接关闭：管理员对另一张工单直接关闭（不填说明），A 的页面显示“已关闭（由客服关闭）”，回复框消失；带说明关闭时，说明作为客服消息出现在对话末尾 |
| T2-03 | 关闭冲突：管理员打开详情后，A 发了一条新消息；管理员在旧页面点击关闭，看到 409 提示，详情自动刷新并显示新消息 |
| T2-04 | 用户自行关闭：A 点击“问题已解决，关闭工单”，确认后状态为已关闭，管理员列表中该工单不再计入待处理 |
| T2-05 | 上限与校验：第 4 张工单时“提交工单”按钮禁用并有说明；标题超长、正文超长时表单内显示字段错误，不发请求；双击提交只产生一张工单 |
| T2-06 | 保留期：列表和详情显示删除日期；通过测试钩子把一张工单改为 2 天后到期，用户侧显示黄色“即将删除”提示；改为已到期后，1 秒级清理删除它，用户列表和管理员列表都不再出现，正在打开的详情页提示“已超过保留期限并被删除” |
| T2-07 | 模拟登录：管理员模拟 A，/tickets 只读显示 A 的工单，没有提交、回复、关闭按钮，不发送“标记已读”请求（用网络监听断言） |
| T2-08 | 兼容：用路由模拟让 meta 不含 tickets_enabled，侧边栏不显示入口，不发出 summary 请求，/tickets 显示“暂未开放”；tickets_enabled=false 时只读、无提交按钮 |
| T2-09 | 界面与文案：/tickets 列表、提交表单、详情和管理后台工单页在 1440 宽和 375 宽下的截图存为证据（窄屏为卡片）；暗色主题截图各一张；ui-copy 运行时守卫和 copy-guard 源码守卫通过（用户侧没有“管理员”“测试”等词）；工单正文中的 HTML 片段以原文显示，不被解释 |
| T2-10 | 前端 lint、typecheck、build（--webpack）、test:api-proxy（含 32 KB 新用例）、test:tickets、test-payment-web.py 全部通过；现有 payment、shop、referral、admin-console 系列、support-widget spec 不受影响 |

### 第 3 轮：全链路回归、发布演练与文档

工作项：
- 扩展 test-release-rehearsal.py：新增 TICKET_ROLLBACK_REF（75f906d，或第 1 轮开工时 main 的 HEAD，以实际为准写入记录）。在当前二进制上制造各状态的工单（open、answered、closed、即将到期）→ 回滚 → 在旧版本上注册、付款、清除一个有工单的账号 → 再升级回来 → 验证第 14.1 节的各项结论；
- 新旧前后端混合部署验证；
- APP 兼容确认；
- 安全审查；
- 文档：README（工单功能段落）、backend/DEPLOYMENT.md（TICKETS_ENABLED、发布顺序、保留期说明）、docs/ 下新增或在现有运维文档中加入“工单处理与排查”一节（管理员如何处理队列、doctor 警告的含义、到期删除不可恢复的提醒）。

| 编号 | 验收项 |
| --- | --- |
| T3-01 | 演练：升级后迁移成功、工单功能可用；回滚后旧版本启动、注册、付款、账号清除都不因新表报错，被清除账号的工单被级联删除；再次升级后，回滚期间已到期的工单立即不可见，并在第一轮清理中被删除；其余工单完整保留 |
| T3-02 | 混合部署：新前端对接旧后端时入口隐藏、无报错；旧前端对接新后端一切照常 |
| T3-03 | APP：四平台契约测试和本机可运行的 APP 测试通过（本机无法执行的平台依赖 CI，或记为 NOT RUN）；确认 APP 内没有需要更新的帮助文案 |
| T3-04 | 安全审查逐项确认：越权与 404 一致性、管理员身份不外泄、XSS（含 script 标签、事件属性、javascript: 链接的正文）、CSRF、限流、请求体上限、日志与审计中无正文、doctor 无敏感输出 |
| T3-05 | doctor 在开发和生产配置下都能报告工单检查项；人为制造一张等待超过 24 小时的工单和一张到期 2 小时未删除的工单，分别出现对应警告 |
| T3-06 | 第 16.2 节的实测项写进运维文档，并在本地逐步走通一遍；真实结果由用户部署后填写 |
| T3-07 | CI 全绿，并确认新增的检查确实出现在 gh pr checks 中 |

## 16. 测试资产与证据要求

### 16.1 开发方可执行的测试

- Go 测试沿用私有 PostgreSQL（在同一次 Bash 调用中设置 NATIVE_TEST_DSN，否则数据库测试会静默跳过）、注入时钟、GORM 回调屏障（用于并发和锁顺序测试）。到期清理直接调用 Sweeper 的单次方法，不依赖真实间隔。
- 文本校验、工单号生成和状态机用表驱动单元测试覆盖。
- Playwright 沿用 E2E_ISOLATED=private-postgres、单 worker 和 test-payment-web.py 夹具；spec 使用自己新建的账户（例如 ticketa、ticketb），不复用其他 spec 的账户，结束时不需要清理（夹具每次全新建库）。管理员和用户使用两个独立的浏览器上下文，以验证“实时”刷新。
- 测试端口一律使用现有的 loopback_port、freeAddr 等辅助函数。
- 在 worktree 中按以往的经验处理：前端在 worktree 里执行 npm ci（主检出目录的 node_modules 不一定存在）；前端用 webpack 构建；Mihomo 子模块从主检出目录复制；补丁脚本写到 /tmp 下再执行；不对整个 tests 目录运行 prettier。

### 16.2 用户部署后的实测（追加到现有 U 系列之后）

前提：生产或预发布环境已部署本功能；准备一个普通测试账户和一个管理员账户，分别在两台设备或两个浏览器上登录。

| 编号 | 步骤 | 预期 |
| --- | --- | --- |
| U-36 | 普通账户在“我的工单”提交一张“连接与节点”工单，正文粘贴客户端的“复制诊断信息” | 提交成功，显示工单号和删除日期（30 天后） |
| U-37 | 管理员保持管理后台“工单”页打开，不刷新 | 15 秒内出现新工单和角标 |
| U-38 | 管理员回复；普通账户停留在任意用户页面 | 1 分钟内侧边栏出现未读角标；打开后看到“客服”回复，角标消失 |
| U-39 | 管理员对另一张工单不回复直接关闭 | 用户侧显示“已关闭（由客服关闭）”，不能再回复 |
| U-40 | 提交 30 天后再次查看 | 工单已从两端消失 |

任何一步与预期不符时，先运行 doctor 查看工单检查项，再对照第 7、8 节判断原因，并记入第 18 节。

## 17. 完成定义

- 普通用户登录后，左侧栏有“我的工单”，可以按分类提交工单、查看客服回复、继续补充、自行关闭；有新回复时侧边栏显示未读角标。
- 管理后台有“工单”标签，新工单和用户新回复在 15 秒内出现，带待处理角标；管理员可以逐条回复，也可以不回复直接关闭（可选说明），关闭前看不到的新消息会被拦截提示。
- 每张工单自提交起保留 30 天，到期立即对两端不可见，并由系统自动物理删除；账号清除时工单一并删除。
- 工单内容不出现在日志、审计和 doctor 输出中；用户侧不暴露任何管理员身份。
- 回滚和新旧前后端混合部署经过演练；APP 不需要修改代码并已确认兼容。
- 第 15 节的全部验收项都有 PASS 证据，或有写明原因的 BLOCKED / NOT RUN 记录，CI 全绿。生产环境的结论以用户按第 16.2 节实测的结果为准，与开发方的结果分开记录。

## 18. 实施记录

（每轮完成后在此追加：日期、分支、PR、验收结果表、偏离本文的决定及理由。）

### 第 1 轮：后端闭环（2026-11-01，分支 worktree-ticket-round1-backend，PR 见合并记录）

基线：main 75f906d。证据：artifacts/ticket-round1-backend（go-race-suite.txt、ticket-tests-race.txt、catalog/、payment/）。

**实现概要**
- 模型与迁移：backend/internal/model/ticket.go（Ticket、TicketMessage），store.Migrate 加入两张表和部分索引 idx_ticket_open_queue（waiting_since, id，条件 status = 'open'）。两处外键都是 ON DELETE CASCADE。
- 用户接口：backend/internal/api/tickets.go，即 /me/tickets（列表）、POST /me/tickets（提交）、/me/tickets/summary、/me/tickets/:id、/:id/messages、/:id/close、/:id/read。
- 管理员接口：backend/internal/api/console_tickets.go，即 /admin/console/tickets、/summary、/:id、/:id/messages、/:id/close，审计动作为 admin.ticket_reply 和 admin.ticket_close（只记长度）。
- 请求体上限：Go 中间件对 /api/v1/me/tickets 和 /api/v1/admin/console/tickets 前缀放宽到 32 KB。Next 代理的同样修改按计划留到第 2 轮。
- 到期删除：maintenance.Sweeper.PruneTickets，每 10 分钟一次（Sweeper.Tickets 可注入），在既有 advisory lock 下运行，每批 1000 行，SKIP LOCKED。
- 账号清除：purgeAccount 在删除 orders 前删除该用户的工单。
- 配置：TICKETS_ENABLED（反向布尔，默认开启），写入 backend/.env.example；/meta 新增 tickets_enabled。
- doctor：期望表加入 tickets、ticket_messages；新增 tickets 段（enabled、open、answered、oldest_wait_seconds、expired_not_deleted），警告 tickets_waiting_over_24h、tickets_expiry_lagging。

**验收结果**

| 编号 | 结果 | 证据 / 说明 |
| --- | --- | --- |
| T1-01 | PASS | TestTicketMigrationOverExistingData：先完整迁移，再删除两张工单表以模拟 75f906d 的库，然后迁移两次。所有既有表按迁移前的列做行指纹比对，逐值不变。两处 CASCADE 外键、8 个索引（含部分索引）和各 CHECK 约束都存在；关闭 seqscan 后，EXPLAIN 显示队列查询走 idx_ticket_open_queue。doctor 的表清单和检查项见 T1-11 |
| T1-02 | PASS | TestTicketCreateValidation：5 个分类、6 个设备值和空值都能创建；12 种非法输入返回 400 invalid_ticket 并指明 field；3000 个汉字（9000 字节）的正文能通过；别人的订单、非 UUID、不存在的订单都返回 invalid_order；工单号符合 ^T\d{12}$，日期部分为 UTC+8；冲突两次后重试成功，连续 5 次冲突返回 errTicketUnavailable（接口为 503 tickets_unavailable）。偏离见下文第 2 条 |
| T1-03 | PASS | TestTicketIdempotencyAndCaps：同键重放返回 200 replayed，内容不同返回 409，缺键或键格式错误返回 400；第 4 张未关闭工单返回 409，关闭一张后可以再提交；当天第 6 次提交返回 429 并带 Retry-After；10 次校验失败不消耗次数；并发争抢最后一个名额时恰好一成一败 |
| T1-04 | PASS | TestTicketStateMachine：按 4.1 节逐步驱动，每一步的 status、waiting_since、user_unread、message_count（与消息行数一致）、version 都正确；关闭后三种写操作返回 409，用户再次关闭为幂等 200；第 101 条消息双方都返回 409，带说明的关闭也返回 409，不带说明可以关闭；回复重放只存一次；每小时第 31 条回复返回 429，下一小时恢复 |
| T1-05 | PASS | TestTicketConsoleQueueAndActions：队列按等待时长排序，用户回复后排到队尾；分类、工单号、邮箱、标题四种筛选正确，% 按字面匹配；审计只有 {"length":6} 或 {"note_length":0,"with_note":false}；version 过期时返回 409 ticket_changed；两位管理员并发回复都成功，对话顺序确定；提交人注销中时回复返回 409 user_unavailable，此前的回复仍可重放（200），关闭成功；普通用户访问 5 条管理员路由均返回 403，模拟会话返回 403；用 GORM 回调在事务加锁前撤销管理员角色，返回 403 且没有写入 |
| T1-06 | PASS | TestTicketUserFieldWhitelist：列表、列表项、详情、消息、摘要的字段集合与第 10 节逐一相等；两种用户接口的响应中都不含管理员 id、邮箱、author_id、author_email、closed_by_admin；别人的工单与不存在的工单 404 响应体逐字节相同；非 UUID 参数返回 404 |
| T1-07 | PASS | API 侧（TestTicketExpiry）：到期前 1 秒可见、可写；正好到期时列表、摘要、详情、回复、关闭、已读、管理员详情、回复、关闭全部不可见或返回 404，到期工单不再占未关闭名额。清理侧（internal/maintenance/tickets_test.go 的 4 个测试）：恰好到期的工单被删除，晚 1 秒到期的保留，消息随外键删除；2500 张分三批删完；被锁住的工单本轮跳过、下一轮删除；其他实例持有锁时整轮跳过；Run 按缩短后的间隔执行工单清理 |
| T1-08 | PASS | TestTicketAccountPurge：PurgeDeletedAccounts 删除该用户的全部工单和消息，其他用户不受影响；直接删除 users 行（模拟旧二进制）时外键级联生效。“注销中在后台可见并标注”在 T1-05 中验证 |
| T1-09 | PASS | TestTicketImpersonationReadOnly：模拟会话可以 GET 列表、详情和摘要；提交、回复、关闭、已读均返回 403 impersonation_read_only；user_unread、状态、消息数都不变 |
| T1-10 | PASS | TestTicketsSwitchedOff 与 config.TestTicketsConfiguration：开关关闭时 /meta 为 false，提交、回复、用户关闭返回 403 tickets_disabled；列表、详情、摘要、已读、管理员回复和关闭照常；管理员在用户侧提交返回 403 admin_cannot_submit；默认值和零值配置都是开启，非布尔值被拒绝 |
| T1-11 | PASS | TestTicketTextStaysPrivate：完整走一遍创建、回复、关闭（带说明），期间捕获日志；日志、审计 detail 和 doctor JSON 中都没有标题、正文、回复、说明、邮箱、工单号。doctor 统计正确，两个警告按条件出现 |
| T1-12 | PASS | Go 全量竞态：scripts/test-native.py，exit 0，10 个包全部 ok，顶层 269 PASS、0 FAIL、11 SKIP（均为由其他脚本驱动的夹具和 world 测试，见 go-race-suite.txt），16 个工单测试另在 -race 下单独运行通过（ticket-tests-race.txt）。native 契约测试没有修改，照常通过。test-node-catalog.py：PASS（真实浏览器 → PostgreSQL 目录 → 官方 Mihomo 真实流量与加权账本，Playwright 10 passed）。test-payment-web.py：PASS（浏览器 → Next.js → Gin/PostgreSQL → 本地模拟网关，Playwright 73 passed，含 admin-console 系列、referral、ui-copy、support-widget） |

**偏离本文的决定**
1. ticket_messages 增加 position 列（工单内序号，CHECK 1–100，与 ticket_id 组成唯一索引），对话按它排序，取代原计划的 (ticket_id, created_at) 索引。原因：测试时钟下，同一时刻写入的多条消息按随机 uuid 排序，顺序不确定。第 5.2 节已同步更新。
2. 非 UTF-8 字节不返回 400：Go 的 JSON 解码器在校验前就把它们替换成 U+FFFD，所以库里不可能存入非法 UTF-8。测试断言的是存储结果合法。
3. 第 10 节补齐了实际字段：列表另返回 open_count、max_open；摘要另返回 max_open；详情另有 platform、closed_at、closed_by；写接口返回完整的 ticket 和 replayed。
4. 限流使用现有 bumpLimit 的固定窗口：“24 小时 5 张”按 UTC 自然日计，“每小时 30 条”按整点小时计，与优惠码、下单的限流方式一致。
5. 管理员的回复同样带 Idempotency-Key；关闭先检查是否已关闭（409 ticket_closed），再检查 version（409 ticket_changed）。

**未完成 / 留给后续轮次**：Next 代理的 32 KB 上限、全部界面和 Playwright 属于第 2 轮；回滚演练、混合部署和文档属于第 3 轮。
