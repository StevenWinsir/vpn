# AsterLink VPN：套餐有效期内升级（补差价）技术规划交接书

> 文件：handoff_plans.md  
> 编写日期：2026-11-02  
> 代码基线：项目 /Users/stevenlee/Desktop/vpn，main，HEAD dbe75ca（#99 时区修复合并后）。当前工作区所在分支 prod-api-windows 只比 main 多一个 Windows 构建参数提交（4b6b168），与本次无关；工作区里用户未提交的 iOS 改动不要动。  
> 文档性质：实施规划与验收约定。本文不代表任何功能已开发或已测试，全文不含代码示例。

## 1. 目标、范围与轮次总览

用户在 /plans 订阅某个套餐后，在套餐到期前想换到别的套餐，现在一律被拒绝。本次要做到：

1. **升级（新功能）**：套餐有效期内，可以升级到更高的套餐。到期日不变，只为剩余天数补差价。升级的价格由服务端按第 6 节的算法计算，前提是商家一定不吃亏：已经消耗的时间和流量不会折成钱，抵扣额只按用户实际付过的钱算，而且所有取整都向商家有利的方向取。
2. **降级（维持禁止，改善提示）**：有效期内不允许降级。降级本来需要退差价，用户已决定不退款，所以页面和接口都明确提示“到期后再购买”。
3. **其他更换（维持禁止）**：价格更高但某项权益更低的“平级/交叉”更换，同样提示到期后再购买。

架构不变：计费仍由客户端和节点完成，网站后端只管理权益。升级只改订阅行的权益字段，计量账本仍是同一个周期（第 7.4 节）。**本次不改 APP 代码**：升级后客户端会收到已有的 subscription_changed，提示重新登录。第 3 轮只做兼容确认（第 12 节）。

采用 **3 轮**实施。每轮一个 worktree 分支和一个 PR，分支命名为 worktree-plans-roundN-*，每轮的记录追加到本文第 18 节。

| 轮次 | 可验收结果 | 依赖 |
| --- | --- | --- |
| 第 1 轮 | 后端闭环：关系判定、升级定价引擎（纯函数）、订单时间窗快照、升级报价、升级选项和升级下单接口、履约分支、降级与平级的新提示、商店设置的两项新配置、控制台订单字段、doctor、Go 测试（含不变量随机测试） | 现有 Go/PG、假网关 |
| 第 2 轮 | 网站：/plans 卡片按关系显示“续费 / 升级 / 到期后可购买”，新增升级结算弹窗，订单页、支付结果页、FAQ 与规则文案，管理后台订单详情与商店设置；Playwright 端到端（假网关） | 第 1 轮 |
| 第 3 轮 | 全链路回归、发布演练（升级、回滚到 dbe75ca、再升级）、新旧前后端混合、APP 与 Core 兼容确认、安全审查、文档与用户实测清单 U-41 … U-46 | 前 2 轮 |

本期不做：
- 降级或退款，包括“到期后自动降级”的预约。
- 升级的同时改付费周期（例如月付的轻量直接升成年付的进阶）。这种需求分两步完成：先升级，再按现有规则把新套餐续费成年付。
- 升级使用优惠码（第 4 节 D7）。
- 管理后台手工“按差价升级”。管理员仍可用现有的订阅编辑来改套餐。
- 测试购买（/orders/test-purchase）路径下的升级。升级只走在线支付，本地用假网关验证。
- 修改四个 APP 或 Core，让它们换套餐后无需重新登录。

## 2. 问题核实（结论：问题存在，升级和降级都被拒绝）

| 位置 | 现状 | 结论 |
| --- | --- | --- |
| backend/internal/api/fulfil.go:37 planDecision | 付费订阅有效（now < expires_at）且不是测试订阅时，只要所购 plan_id 与当前不同，就返回 errPlanChange（fulfil.go:18，409 plan_change_not_supported，提示“当前套餐有效期内仅支持续费同一套餐，到期后可更换”）。函数不区分高低档，所以升级、降级和平级更换全部被拒 | 后端根因 |
| backend/internal/api/payments.go:178（报价） | 报价调用 planDecision，遇到业务错误时返回 purchasable=false 和上述 reason | 报价阶段就不可买 |
| backend/internal/api/payments.go:313（下单） | 下单再次调用 planDecision，直接 409 | 下单也不可能 |
| backend/internal/api/payment_notify.go:158 fulfilOrder | 履约前调用 applyPlan → planDecision。如果在下单后、付款前，用户的当前套餐变了，订单会被转成人工审核（state_conflict） | 现有的安全兜底，升级需要沿用 |
| frontend/src/components/plans-view.tsx:141 state() | 当前订阅有效、不是测试订阅、并且 plan_id 不同时返回 'blocked'，按钮禁用；第 389–393 行显示“当前套餐有效期内不能更换套餐，到期后可购买。”（即用户反馈的原文）；第 421–423 行的规则说明也写着“有效期内不能更换为其他套餐” | 前端只是镜像后端规则 |
| frontend/src/components/home/home-faq.tsx:35 | FAQ 同样写着“有效期内不能更换为其他套餐” | 文案要随改 |
| 现有测试 payment_test.go:97/100/623，shop_test.go:611/614/840 | 断言有效期内买别的套餐返回 plan_change_not_supported | 第 1 轮需要按新语义调整：降级和平级的断言保留，升级改成走新接口 |
| console_users.go（管理员编辑订阅） | 管理员已能在有效期内改套餐（不改周期开始时间，清掉刷新流量，并提示“会使该用户的客户端重新授权”） | 说明“同一周期内换 plan_id”在账本和客户端侧已有先例、是安全的（第 7.4 节） |

以上结论来自代码阅读，并且有现有测试的断言佐证。本规划编写期间没有启动 PG 实际运行（第 1 轮开工时先跑一次现有用例，作为基线）。

## 3. 已核实的代码基础与必须衔接的位置

### 3.1 后端

| 位置 | 当前事实 | 对本次的影响 |
| --- | --- | --- |
| model.Subscription（models.go:76） | 每个用户一行：plan_id、plan_name、starts_at、expires_at、traffic_limit_bytes、used_units（千分之一字节）、max_devices、allow_dedicated、is_test 和 version（任何权益写入都会加 1，推荐奖励写入也会加，见 referral.go:251）。还有刷新流量字段 quota_cycle_days/bytes/starts_at，以及推荐奖励 bonus_bytes/bonus_starts_at | 升级只改 plan_id、plan_name、max_devices、allow_dedicated、traffic_limit_bytes，以及刷新订阅的 quota_cycle_bytes。starts_at 和 expires_at 不动 |
| fulfil.go applyPlan / saveSubscription | 续费：同一套餐顺延天数并累加流量。月付和年付共用一个周期行。saveSubscription 同步当前周期的账本行（subscription_periods） | 升级新写一个 applyUpgrade，同样通过 saveSubscription 落库，保证账本同步 |
| billing/quota.go | QuotaCycleActive 判断订阅是否为“刷新型”（年付）；RollQuotaCycle 惰性滚动；NextQuotaReset 给出下次重置时间；推荐奖励会跨重置保留 | 刷新型订阅的升级要算“本周期剩余天数”，并把以后每个周期的额度换成新套餐的月流量（第 7.2 节） |
| shop.go termsFor / yearlyPrice | 月付价格就是 plans.price_cents；年付价格 = floor(月价 × 12 × 折扣 / 100)，天数取 yearly_duration_days | 新套餐的“日单价”直接取这两种周期之一（第 6.3 节） |
| payments.go 报价/下单 | 服务端计价；Idempotency-Key 必须是 UUID；每用户限流 20 次/10 分钟；每用户最多 5 个未完成订单；expected_amount_cents 不一致返回 409 price_changed；订单快照包括周期、设备数、专线权限 | 升级下单完全沿用这一套，另加“唯一未完成订单”规则（第 8.2 节） |
| model.Order | 已有 amount_cents、paid_cents、list_price_cents、original_cents、billing_cycle（带 check）、ent_max_devices、ent_allow_dedicated、quota_cycle_*；duration_days 和 traffic_bytes 没有 check 约束 | 升级订单把 duration_days 和 traffic_bytes 写成 0，让旧二进制无法把它当普通订单履约（第 14 节） |
| payment_notify.go judgeNotification / fulfilOrder | 锁顺序：User → Order → Coupon → Subscription → Period。fulfilOrder 返回 false 时转为人工审核（state_conflict）；迟到的回调也会履约 | 按订单类型分支，升级失败一律转人工 |
| console_orders.go 人工确认（288 行） | 管理员确认时也调 fulfilOrder，返回 false 时报 fulfil_conflict | 自动覆盖升级订单 |
| referral.go qualifyReferral | 用户第一笔已付、非 test 的订单决定推荐资格，金额取 min(应付, 实付) | 升级订单一般不会是第一笔，不需要改；如果是，就按它的金额，行为一致 |
| native.go nativeEntitlementFingerprint | 会话绑定的权益指纹 = plan_id、is_test、allow_dedicated、max_devices；指纹不一致时返回 subscription_changed | 升级必然改变指纹，客户端会断开并要求重新登录（第 12 节） |
| agent_api.go grantVerdict / node_catalog.go groupVisible | 节点授权看的是周期身份（periodIsCurrent），没有看指纹；线路组可见性由组的 plan_ids 和专线权限决定 | 周期不变，所以迟到的节点用量仍计入同一周期；升级后可见的线路组按新套餐重新计算 |
| model.ShopSettings（shop.go） | 单行设置，目前只有年付全局折扣，后台“套餐管理”里可以编辑并留审计（AuditShopSettings） | 新增升级开关和最低补差金额（第 9.2 节） |
| server.go /meta | 由配置静态拼成：payment_enabled、referral_enabled、tickets_enabled 等 | 新增 plan_upgrade_supported（第 10 节） |
| store.Migrate | 增量 AutoMigrate 加显式索引；GORM 写入值为 false 的 bool 时会被列默认值 true 顶掉（商店第 1 轮的经验） | 新的 upgrade_enabled 列有同样的坑，写入后要再显式更新一次 |
| internal/operations/readiness.go | doctor 有只读检查项 | 新增 3 项检查（第 13.3 节） |

### 3.2 前端

| 位置 | 当前事实 | 对本次的影响 |
| --- | --- | --- |
| components/plans-view.tsx | 加载 /plans、/meta 和 /me/dashboard，由 state() 在本地判定 new/renew/blocked；月付/年付切换放在地址参数里；PurchaseModal 负责结算 | 改成由服务端的“升级选项”接口决定每张卡的关系，不再在前端复刻规则 |
| components/purchase-modal.tsx | 报价→选支付方式→可填优惠码→下单→整页跳转网关；处理 price_changed；每组选择对应一个 Idempotency-Key | 新建一个升级结算组件，复用支付方式选择、跳转网关和 price_changed 的处理，但不显示优惠码 |
| components/orders-view.tsx | 每行显示“流量 / N 天”，并有“月付/年付”标签；过期订单有“再次购买”链接，会预选套餐 | 升级订单显示为“升级 A → B · 补差价”，不提供“再次购买” |
| components/pay-result-view.tsx | 轮询 /payments/orders/:id 并显示结果 | 升级订单成功时显示“已升级到 B”，并提醒客户端需要重新登录 |
| app/admin/console/orders、plans | 订单列表和详情；套餐页里有全局年付折扣的设置 | 订单增加类型和“从 A 升级”字段；商店设置增加两项 |
| tests/shop.spec.ts、payment.spec.ts | 依赖种子价格（starter 19.90 / pro 39.90 / max 79.90）和 aria-label | 新建 plans-upgrade.spec.ts，不改这些现有断言的含义 |

### 3.3 APP 与 Core

native/core/internal/apiclient/messages.go：subscription_changed 属于 TerminatesSession，Core 会停掉隧道、丢弃会话，并提示“套餐已变更，请重新登录”。旧版 Core（native/legacy-core/managed/coordinator.go:231）同样处理。四个平台都通过 Core 走这条路径，所以升级不需要改 APP。

## 4. 关键决策（默认值，用户可以在第 1 轮开工前推翻）

| 编号 | 决策 | 理由 |
| --- | --- | --- |
| D1 | 升级是“补差价、到期日不变”：订阅的开始和到期时间都不动，只把剩余时间的权益换成新套餐 | 这是云服务“升配”的通行做法。续费叠加的天数、年付和月付混合的订阅都能统一处理，不会出现抵扣额大于新套餐价、需要作废余额的情况 |
| D2 | 抵扣额只按本周期**实际付过的钱**和**剩余时间**计算，不按套餐标价，也不看剩余流量。用户保留自己剩下的流量，新增流量只按日流量差发放 | 优惠码、测试价、管理员赠送的天数都不会被折成钱。已用掉的流量没有退还的途径（第 6.8 节） |
| D3 | 取整一律对商家有利：收费天数向上取整，抵扣天数向下取整，金额向上取整，抵扣向下取整；新增流量中新套餐的部分向下取整，原套餐的部分向上取整 | 商家不吃亏。差额最多约 1 天的价差，会在弹窗里写明 |
| D4 | 剩余不足 24 小时不允许升级，提示“剩余不足 1 天，请到期后直接购买新套餐” | 避免收 1 天的钱只用几小时引起纠纷 |
| D5 | 最低补差金额由商店设置控制，默认 1.00 元（允许 0.01–100.00 元）；升级功能有总开关，默认开启 | 覆盖支付通道手续费，避免几分钱的订单；运营可以随时关掉 |
| D6 | “升级”的判定：新套餐的月付价格严格更高，并且日流量、设备数、专线权限都不低于当前订阅。价格更低算“降级”；价格相同、或价格更高但某项权益更低，算“不可更换”。后两种在有效期内都不能购买 | 规则清楚，用户不会多付钱反而少了权益，也不会出现负的新增流量 |
| D7 | 升级订单不能使用优惠码 | 抵扣已经按实付计算，再叠加优惠码会让升级部分低于标价；也避免优惠码在升级场景下的使用次数和适用范围产生歧义 |
| D8 | 测试价名单内的账户，升级应付额也换成 PAYMENT_TEST_PRICE（与现有购买一致），其他计算照常，报价同时显示正常价格 | 用户部署后可以低价实测真实网关 |
| D9 | 有一个未完成的升级订单时，不能再下任何新订单；有任何未完成订单时，不能下升级订单（409 pending_order_exists） | 升级的价格依赖订阅的当前状态，并行订单只会制造人工审核。这条只限制升级场景，现有“允许多个未完成订单”的规则不变 |
| D10 | 履约时只要订阅的“权益身份”没变（同一订阅、同一周期开始、同一到期时间、同一套餐、仍是付费且有效），就按下单时的快照开通；否则一律转人工审核，不自动改成别的东西 | 与“交付所售即所得”的原则一致。推荐奖励、流量重置会改 version，但不改权益身份，所以不能用 version 判断（见 3.1 表 referral.go:251） |
| D11 | 升级不改周期开始时间，账本仍是同一周期；客户端因为权益指纹变了，会重新登录一次 | 节点迟到的用量照常扣到当前余额，商家不会漏计费；专线和设备数需要重新授权，这正是指纹机制的用途 |
| D12 | 降级和不可更换都不退款。接口和页面提示里写明到期日，提示到期后购买 | 这是用户的决定 |

## 5. 关系判定（每张套餐卡相对于当前订阅）

先确定“当前订阅可升级”的前提：订阅存在，is_test 为假，starts_at ≤ now < expires_at。不满足时，所有套餐都按现有规则视为“新购”，测试订阅也一样（付费购买会替换测试订阅并开新周期，规则不变）。

前提满足时，用当前订阅的套餐 A（plans 表里的行，下架或软删除的仍然可读）和目标套餐 B 判定：

| 关系 | 条件 | 页面与接口行为 |
| --- | --- | --- |
| current | B 就是 A | 走现有续费流程，不变 |
| upgrade | B 可购买（上架、未删除、价格为正、条款有效），B.price_cents > A.price_cents（都按月付价格比较），B 的日流量 ≥ A 的参考日流量（第 6.5 节；刷新型订阅比较每周期流量），B.max_devices ≥ 订阅的 max_devices，订阅有专线时 B 也必须有专线 | 显示“升级”。剩余 ≥ 24 小时且开关开启才可下单；否则显示原因 |
| downgrade | B.price_cents < A.price_cents | 禁用，提示“当前套餐有效期内不支持降级（降级不退还差价），请在 {到期日} 到期后购买” |
| lateral | 其他情况（价格相同，或价格更高但流量、设备或专线某项更低） | 禁用，提示“{B} 与当前套餐权益不可直接升级，请在 {到期日} 到期后购买” |

线路组可见性不参与判定，因为它是管理员的配置。但报价会列出“升级后不再可见的线路组名称”（按 groupVisible 分别用 A 的订阅和 B 的假想订阅计算），弹窗醒目提示，doctor 也会警告管理员（第 13.3 节）。

## 6. 升级定价算法

### 6.1 术语

- E：订阅当前的到期时间；now：服务端注入时钟的当前时间；R = E − now。
- 一天固定是 24 小时，在绝对时间上计算，不依赖进程时区（与 #99 的修复一致）。
- 持有方式：订阅是刷新型（QuotaCycleActive 为真）时记为“年付持有”，否则为“月付持有”。升级订单的 billing_cycle 字段记录持有方式。

### 6.2 天数

- 收费天数 Nc = R 按 24 小时向上取整；抵扣天数 Nk = R 按 24 小时向下取整。
- 要求 Nk ≥ 1（即 R ≥ 24 小时，D4）。
- Nc 只在 R 跨过整天边界时变化（到期时间往前每 24 小时一个边界）。因此一份报价在数小时内保持不变，跨边界时由 expected_amount_cents 触发现有的 price_changed 并重新报价。

### 6.3 新套餐剩余价值 V

- 月付持有：按 B 的月付条款，V = 向上取整（B.price_cents × Nc ÷ B.duration_days），单位为分。
- 年付持有：B 提供年付时按 B 的年付价格和 yearly_duration_days 计算（termsFor 的结果）；B 不提供年付时按 B 的月付条款计算（月付日单价更高，对商家有利）。
- 中间结果用不会溢出的整数运算（价格最多 10⁷ 分，天数最多约 3653，乘积在 int64 内，但统一用安全乘除，并写溢出测试）。

### 6.4 已付残值抵扣 K（订单时间窗）

**时间窗**：从本轮起，每笔新的已付订单在履约时记录它的价值覆盖区间 [covers_from, covers_to]（新列，第 9.1 节）：
- 开新周期的订单：[now, 新到期时间]；
- 续费订单：[续费前的到期时间, 续费后的到期时间]；
- 升级订单：[now, E]。

**计入的订单**：同一用户、status = paid、provider ≠ test、paid_at ≥ 订阅的 starts_at（属于本周期）。它们包括开周期的那笔、所有续费，以及此前的升级。

**旧订单的兜底窗口**：本次上线前的订单没有时间窗，按 [paid_at, paid_at + duration_days × 24 小时] 计算。这个窗口的开始时间不会晚于真实开始时间（续费的真实开始时间是上一次的到期时间，不早于付款时间），长度相同，所以它与“剩余区间”的重叠只会小于或等于真实重叠，抵扣只会少算，不会多算。旧二进制回滚期间履约的订单也走这条兜底。

**抵扣区间**：C = [E − Nk × 24 小时, E]。

**计算**：每笔计入订单的贡献 = 向下取整（金额 × 订单窗口与 C 的重叠秒数 ÷ 订单窗口秒数），金额取 min(amount_cents, paid_cents)，不把网关容差内的多付计入。K 为所有贡献之和。管理员赠送的天数、管理员在后台延长的有效期都没有订单覆盖，贡献为 0；管理员缩短有效期时，重叠变小，K 随之变小。两种情况都只会让抵扣变少。

### 6.5 应付金额与参考流量

- 升级金额 = max(V − K, 最低补差)。如果 V − K 小于最低补差（例如订阅里混有月付天数，但按年付持有计算 B 的价值），按最低补差收取，弹窗写明“抵扣已达上限，按最低补差收取”。
- 测试价名单内的账户，应付额替换为 PAYMENT_TEST_PRICE，price_source = test_price（D8）。
- 不接受优惠码（D7）。
- A 的参考日流量（D6 判定用，也用于第 7.2 节的新增流量）取以下各项中日流量最大的一项（取最大会让新增流量最少，对商家有利）：A 当前的月付条款（traffic_bytes ÷ duration_days），以及本周期计入订单中目标套餐等于 A 的那些订单的快照（月付单：traffic_bytes ÷ duration_days；刷新型年付单：quota_cycle_bytes ÷ quota_cycle_days；升级订单：快照里的新套餐日流量）。日流量的比较用交叉相乘，不做浮点除法。年付持有时比较每周期流量：max(订阅的 quota_cycle_bytes, A.traffic_bytes)。

### 6.6 种子套餐的算例（年付折扣 90%）

种子：轻量 starter 19.90 元 / 30 天 / 100 GiB / 2 台 / 无专线；进阶 pro 39.90 / 300 GiB / 5 台 / 专线；旗舰 max 79.90 / 800 GiB / 10 台 / 专线。年付价：轻量 214.92、进阶 430.92、旗舰 862.92（365 天）。

| 场景 | 过程 | 结果 |
| --- | --- | --- |
| 例 1：月付轻量升级到进阶 | 2026-11-01 00:00 购买，实付 19.90，到期 12-01 00:00。11-11 10:00 升级：R = 19 天 14 小时，Nc = 20，Nk = 19。V = 39.90 × 20/30 = 26.60；K = 19.90 × 19/30 向下取整 = 12.60 | 补 **14.00 元**；到期日仍是 12-01；新增流量 = 300 × 20/30 − 100 × 20/30（后者向上取整到字节）≈ **133.33 GiB**；设备 5 台，开通专线。按精确天数算的公平差价是 13.06 元，多出的部分来自取整 |
| 例 2：接着从进阶升级到旗舰 | 11-21 10:00：R = 9 天 14 小时，Nc = 10，Nk = 9。V = 79.90 × 10/30 向上取整 = 26.64。K = 轻量单 19.90 × 9/30 = 5.97，加上升级单 14.00 × 216 小时 ÷ 470 小时，向下取整为 6.43，合计 12.40 | 补 **14.24 元**；新增 ≈ 166.67 GiB。三笔合计 48.14 元，按精确天数算的公平价值是 45.73 元，商家没有吃亏 |
| 例 3：年付轻量（刷新型）升级到进阶 | 2026-01-10 00:00 购买年付，实付 214.92，到期 2027-01-10。2026-11-02 12:00 升级：R = 68 天 12 小时，Nc = 69，Nk = 68。V = 430.92 × 69/365 向上取整 = 81.47；K = 214.92 × 68/365 向下取整 = 40.03 | 补 **41.44 元**。本周期到 11-06 00:00 重置，剩 3 天 12 小时，按 4 天计，本周期加 (300 − 100) × 4/30 ≈ 26.67 GiB；从 11-06 起每 30 天的额度变成 300 GiB。公平差价是 40.54 元 |
| 例 4：用了 5 折优惠码的轻量 | 时间同例 1，实付 9.95。K = 9.95 × 19/30 向下取整 = 6.30 | 补 **20.30 元**。优惠只作用于已经用掉的那部分 |
| 例 5：续费叠加后升级 | 11-01 购买轻量，11-25 续费一次（窗口为 12-01 到 12-31），到期 12-31。11-26 00:00 升级：Nc = Nk = 35。V = 39.90 × 35/30 = 46.55；K = 19.90 × 5/30 + 19.90 = 3.31 + 19.90 = 23.21 | 补 **23.34 元**；新增 ≈ 233.33 GiB；到期日仍是 12-31。如果第二笔是没有时间窗的旧订单，兜底窗口是 11-25 到 12-25，K = 22.54，用户多付 0.67 元，商家不会少收 |
| 例 6：进阶降级到轻量 | — | 拒绝：“当前套餐有效期内不支持降级（降级不退还差价），请在 2026-12-01 到期后购买” |
| 例 7：剩余 20 小时 | — | 拒绝：“剩余不足 1 天，请到期后直接购买新套餐” |

第 1 轮的 Go 表驱动测试必须逐字覆盖上表，结果精确到分和字节。

### 6.7 测试价与展示

报价同时返回：V、K、Nc、Nk、最低补差是否生效、正常应付额和测试价应付额（只对名单内账户标记 test_price）。页面只显示服务端返回的数值，不在前端计算。

### 6.8 “商家不吃亏”的论证（第 3 轮安全审查要逐条复核）

1. **抵扣不超过真实残值**：每笔订单的价值按时间均匀分布在它的覆盖窗口上；K 只取窗口落在剩余整天内的部分，并向下取整。没有窗口的旧订单用更早的兜底窗口。赠送和延长的天数没有订单覆盖。因此 K 不超过“本周期内尚未消耗的实付金额”。
2. **剩余时间按新套餐全价收**：实收 = 尚未消耗的实付 + 升级金额，而升级金额 ≥ V − K。所以剩余时间的实收不低于 V，也就是新套餐在这段时间（按整天向上取整）的标价。
3. **已消耗的不折现**：K 只看时间，不看剩余流量；用户剩下的流量原样保留，不折成钱。新增流量 = (B 日流量 − A 参考日流量) × 收费天数，与“为剩余天数补差价”一一对应。升级后整段周期的流量单价落在 A 和 B 的标价单价之间，新增部分本身按 B 的标价结算。
4. **没有负价、退款或叠加优惠**：最低补差兜底；不接受优惠码；降级和不可更换都不提供。
5. **并发与重放**：下单在用户行锁内重新计算；履约时权益身份不一致就转人工（D10）；同一 Idempotency-Key 只对应一笔订单。旧二进制拿到升级订单只会转人工（第 14 节）。
6. **连续升级**：后一次升级的抵扣包括前一次升级单的残值（例 2），用户不会被重复收费；每次都满足第 2 条，所以合计也满足。

## 7. 权益变更（履约时写入订阅的内容）

### 7.1 不变的字段

starts_at、expires_at、used_units、upload_bytes、download_bytes、bonus_bytes/bonus_starts_at、quota_cycle_days 和 quota_cycle_starts_at（刷新锚点不动），is_test（必为假）。

### 7.2 变化的字段

- plan_id、plan_name 换成 B；max_devices、allow_dedicated 取订单快照（ent_max_devices / ent_allow_dedicated，即下单时 B 的值）。
- **月付持有**：traffic_limit_bytes 增加新增流量。新增流量在履约时用快照中的 B 日流量和 A 参考日流量重新计算，天数取 min(快照 Nc, 履约时的 R 向上取整)。所以迟到的回调只会让新增流量变少或不变，不会变多。
- **年付持有**：本周期的 traffic_limit_bytes 增加 (B 每周期流量 − A 每周期流量) × 本周期剩余天数（到下次重置或到期，取较早者，向上取整）÷ 30，向下取整；quota_cycle_bytes 改为 B 的月流量，以后每次重置都按 B 发放。如果下单和履约之间正好跨过了一次重置，在新的周期上按同一规则重新计算即可，结果仍以快照中的周期流量为准。
- 上限：traffic_limit_bytes 不超过 maxPlanBytes（1 PiB），超出时返回 renewal_limit，不开通。
- version 加 1，updated_at 取注入时钟。

### 7.3 订单落账

订单置为 paid，写 paid_cents 和 paid_at；写入 covers_from = now、covers_to = E；把实际发放的流量写进 upgrade_traffic_bytes；然后照常检查推荐资格。

### 7.4 账本、授权与客户端

- 周期身份（订阅 id 加 starts_at）不变，所以 saveSubscription 会把新的 limit 同步到当前 subscription_periods 行。节点上已经发出的授权（grant）的迟到用量仍记在同一周期，不会漏计，也不会计到别的周期。
- 权益指纹变化后，在线会话的下一次 /traffic、心跳或授权请求会得到 subscription_changed，Core 停止隧道并提示重新登录。登录后按新套餐授权（专线、设备数）。会话停了之后，旧授权在空闲超时（IDLE 180 秒）后自然失效，这段时间内节点上报的用量照常扣费。
- 在线设备上限（lease.go deviceLimit）立即按新值生效。

## 8. 订单、履约与并发

### 8.1 下单（在一个事务里）

锁顺序：User → Plan（共享锁，B）→ 读 A 的行、读本周期的订单、读订阅 → 插入订单。步骤：
1. 账号必须是 active；检查 Idempotency-Key（重放时 plan_id、pay_type、订单类型都必须相同，否则 409 idempotency_conflict）。
2. 未完成订单规则（D9）：存在任何仍在有效期内的 pending 订单，返回 409 pending_order_exists，并通过 Retry-After 告知最早那笔的超时时间。
3. 重新判定关系和价格（第 5、6 节），expected_amount_cents 必填，不一致返回 409 price_changed。
4. 写订单：plan_id/plan_name 为 B；kind = upgrade；from_plan_id、from_plan_name 为 A；amount_cents = price_cents = 应付额；list_price_cents = max(V − K, 最低补差)；original_cents = V；credit_cents = K；discount_cents = 0；billing_cycle 为持有方式；cycle_discount_percent 取 B 在该持有方式下的折扣（月付为 100）；duration_days = 0、traffic_bytes = 0、quota_cycle_days = 0、quota_cycle_bytes = 0（第 14 节）；ent_max_devices/ent_allow_dedicated 为 B；upgrade_days = Nc；upgrade_traffic_bytes 先写报价时的新增流量；upgrade_snapshot 写入第 9.1 节列出的计算依据。
5. 网关商品名为“{B}·升级”，订单号和支付链接的生成方式不变。

### 8.2 普通下单的配套改动

- 存在有效期内的 pending 升级订单时，普通下单返回 409 pending_order_exists。
- planDecision 拒绝更换时，按关系给出不同的提示，code 仍为 plan_change_not_supported，旧前端照样能显示：
  - upgrade：“当前套餐有效期内可通过‘升级’补差价更换到 {B}”；
  - downgrade 和 lateral：第 5 节的提示文字。

### 8.3 履约（回调、人工确认共用 fulfilOrder）

fulfilOrder 先看订单类型，升级订单走 applyUpgrade。applyUpgrade 只有在以下条件全部满足时才开通（D10）：订阅存在；订阅 id、starts_at、expires_at、plan_id 与快照一致；is_test 为假；now < expires_at；B 的行仍存在（软删除也可以）。任一条件不满足就返回 false，由调用方转人工审核（state_conflict）。人工确认时同样提示 fulfil_conflict。管理员可以关闭订单并线下退款，或者先用订阅编辑处理用户的订阅，再关闭订单。说明写进 docs/payment-operations.md。

迟到回调：只要权益身份不变就照常开通。商家按下单时的 Nc 收了钱，履约时剩余天数只会更少，新增流量按 7.2 节重新计算也只会更少，所以商家不会吃亏。

### 8.4 并发场景（第 1 轮必须有测试）

| 场景 | 期望 |
| --- | --- |
| 同一用户两个标签页同时下升级单 | 用户行锁串行化，第二个得到 409 pending_order_exists |
| 升级单 pending 时，管理员编辑了订阅的到期时间 | 回调转人工 |
| 升级单 pending 时推荐奖励到账（version 变化） | 权益身份不变，正常开通 |
| 升级单 pending 时跨过流量重置 | 正常开通，本周期流量按新周期计算 |
| 升级单付款时订阅刚好过期 | 转人工 |
| 同一回调重复投递 | 第二次返回 duplicate，只开通一次 |
| 升级单和另一笔续费单的回调并发（续费单是 D9 生效前下的） | 先提交的开通，另一笔转人工 |

## 9. 数据模型与迁移

没有新表，只给两张表加列，AutoMigrate 在 advisory lock 内执行。

### 9.1 orders 新增列

| 列 | 类型与默认值 | 说明 |
| --- | --- | --- |
| kind | 字符串（12），非空，默认 purchase，check 只允许 purchase 或 upgrade | 订单类型。旧二进制写入的行自动是 purchase |
| from_plan_id / from_plan_name | 字符串（32/80），非空，默认空 | 升级前的套餐 |
| credit_cents | int64，非空，默认 0 | 抵扣额 K |
| upgrade_days | int，非空，默认 0 | 收费天数 Nc |
| upgrade_traffic_bytes | int64，非空，默认 0 | 新增流量（下单时为报价值，履约后为实际发放值） |
| covers_from / covers_to | 时间戳，可空 | 价值覆盖窗口，所有新的已付订单都写（第 6.4 节），不对用户序列化 |
| upgrade_snapshot | text，非空，默认空 | 计算依据（JSON 文本，不对外序列化）：订阅 id、starts_at、expires_at、持有方式、Nc、Nk、V、K、是否用了最低补差、B 的价格和天数、B 和 A 的日流量（或每周期流量）、B 的每周期流量、计入的订单 id 和各自贡献、不可见的线路组 |

用户接口 /orders 和 /payments/orders/:id 只多出 kind、from_plan_name、credit_cents、upgrade_days、upgrade_traffic_bytes 五个字段。快照和时间窗只在控制台详情里展示（经过白名单视图）。

### 9.2 shop_settings 新增列

| 列 | 类型与默认值 | 说明 |
| --- | --- | --- |
| upgrade_enabled | bool，非空，默认 true | 总开关（插入后要再显式更新一次，原因见 3.1 表 store.Migrate 一行） |
| upgrade_min_charge_cents | int，非空，默认 100，check 1–10000 | 最低补差金额 |

后台“套餐管理”里的商店设置表单增加这两项，沿用乐观锁 version 和 AuditShopSettings 审计（记录前后值）。缺行时按默认值读取。

### 9.3 发布演练

scripts/test-release-rehearsal.py 的表清单不变（没有新表），但指纹比对要覆盖新增列，并在旧库数据上验证兜底窗口的计算。

## 10. API 契约

| 接口 | 权限 | 请求 | 响应与错误 |
| --- | --- | --- | --- |
| GET /api/v1/meta | 公开 | — | 新增 plan_upgrade_supported：后端支持升级且 payment_enabled 为真。它是静态能力，不代表开关是否开启 |
| GET /api/v1/payments/upgrade-options | secured（模拟会话可读） | — | enabled（开关）、current（plan_id、plan_name、expires_at、持有方式、remaining_seconds），以及 plans 数组：每个上架套餐的 plan_id、relation（current/upgrade/downgrade/lateral/new）、purchasable、reason、message，upgrade 关系还带预估 amount_cents、upgrade_days、traffic_add_bytes。没有可升级的订阅时 current 为 null，全部为 new。payment_disabled 时返回 503 |
| POST /api/v1/payments/upgrade-quote | secured | plan_id | quote：from/to 套餐、持有方式、expires_at（不变）、upgrade_days、credited_days、value_cents、credit_cents、min_charge_applied、list_amount_cents、amount_cents、test_price、traffic_add_bytes、new_cycle_bytes（年付持有时）、device_limit（经过 deviceCap）、allow_dedicated、lost_groups（线路组名称数组）、reauth_required（固定为 true）、purchasable、reason、message；另返回 pending_orders。错误：400 invalid_plan，404 plan_not_found，409 not_upgradable（带 relation） |
| POST /api/v1/payments/upgrade-orders | secured，非模拟会话 | 请求头 Idempotency-Key（UUID）；请求体 plan_id、pay_type、expected_amount_cents（必填） | 201 或 200（重放）：order 和 pay_url，与普通下单相同。错误：409 not_upgradable、upgrade_disabled、remaining_too_short、pending_order_exists、price_changed、idempotency_conflict、renewal_limit；429 order_rate_limited；403 account_disabled |
| GET /api/v1/payments/orders/:id、GET /api/v1/orders | 不变 | — | 多出第 9.1 节的五个字段 |
| POST /api/v1/payments/orders | 不变 | — | 新增 409 pending_order_exists（只在有 pending 升级单时出现）；plan_change_not_supported 的提示按关系区分（第 8.2 节） |
| 控制台订单列表与详情 | console admin | — | 列表加 kind 和 from_plan_name；详情显示时间窗、V、K、Nc/Nk、快照中的计入订单 |
| 控制台商店设置 | console admin | 原有字段加 upgrade_enabled、upgrade_min_charge_cents | 校验失败返回 400 invalid_plan，带 field |

所有新接口都用现有的 decode（DisallowUnknownFields）。新前端只在 plan_upgrade_supported 为真时调用新接口，也只在这时发送新字段。

## 11. 前端

### 11.1 /plans 页面

- 已登录、payment_enabled 和 plan_upgrade_supported 都为真时，额外请求 upgrade-options，每张卡片按 relation 显示：
  - current：维持现在的“续费 / 续费为年付”；
  - upgrade：主按钮“升级到 {B}”，按钮下一行小字“补差价约 {金额}，到期日不变”；
  - downgrade：按钮禁用，显示“到期后可购买”，小字为第 5 节的降级提示（含到期日）；
  - lateral：同样禁用，显示第 5 节的提示；
  - 开关关闭或剩余不足 1 天：升级按钮禁用，显示服务端给的原因。
- 月付/年付切换不影响升级的价格。年付视图里，升级卡片显示同一个升级按钮，并加一句“升级按你当前的付费方式补差价；如需年付，可在升级后续费为年付”。
- 后端较旧（没有 plan_upgrade_supported）时，退回现在的本地判定，但把文案改成“当前套餐有效期内不能更换，到期后可购买”，不出现“升级”。
- 页面底部“续费与更换套餐”规则改写为：同套餐续费（原有内容）、有效期内可升级并补差价、到期日不变、不支持降级、降级不退差价、到期后可选择任意套餐。home-faq.tsx 同步修改。

### 11.2 升级结算弹窗（新组件，例如 upgrade-modal）

- 标题“升级到 {B}”。按服务端报价逐行显示：当前套餐 → 新套餐、到期日（不变）、剩余天数（收费 Nc 天，抵扣 Nk 天）、新套餐剩余价值 V、已付抵扣 −K、最低补差提示（生效时显示）、应付金额。下面列权益变化：新增流量（年付持有时显示本周期新增和以后每周期的额度）、设备数、专线。
- 醒目提示：“升级成功后，已登录的客户端会断开一次，需要重新登录以获得新权益。” lost_groups 不为空时，用警告色列出“升级后不可再使用的线路”。
- 支付方式选择、跳转网关、price_changed 时重新报价、idempotency_conflict 时换新的 key 都复用 purchase-modal 的做法。每组（B, 支付方式）一个 Idempotency-Key。没有优惠码输入框。
- pending_order_exists 时显示“你有未完成的订单，请先完成支付或等待约 2 分钟后再试”，并给出“订单记录”链接。
- 弹窗归属当前账号：账号切换后自动关闭（同 plans-view 现在的做法）。

### 11.3 其他页面

- 订单记录：kind = upgrade 的行显示“升级 {A} → {B}”和“补差价”标签，副行为“抵扣 {K} · {Nc} 天 · +{流量}”，不显示“再次购买”。
- 支付结果页：升级成功时显示“已升级到 {B}，有效期至 {到期日}”，以及重新登录的提示。
- 管理后台：订单列表增加“类型”列和“升级”标签，详情增加升级明细（第 10 节）；商店设置增加“允许有效期内升级”开关和“最低补差金额（元）”输入框，输入按分校验。
- 文案经过现有的 copy-guard 和 ui-copy.spec 检查，不出现内部术语（“快照”“时间窗”只在后台出现）。

## 12. APP 与客户端影响

- 不改 APP。升级后，客户端在下一次与服务端交互时（最长一个上报或心跳间隔）断开，提示“套餐已变更，请重新登录”。重新登录后即按新套餐取线路和授权。
- 第 3 轮的兼容确认：用现有的 Core 单元测试和桌面、Android 的 billing 世界测试，各补一个场景：会话在线时把订阅改成另一个 plan_id（周期开始时间不变），断言 Core 收到 subscription_changed 后停掉隧道、清掉会话，并且服务端账本同一周期内的迟到用量照常扣费。真机表现留给用户实测（U-42）。
- 以后如果想做到“升级后无需重新登录”，需要单独设计 Core 的静默重新授权，本期不做。

## 13. 安全、审计与运维

### 13.1 安全

- 金额只由服务端计算，请求不带金额（expected_amount_cents 只能让服务端拒绝下单，不参与计价）。
- 升级接口挂在 secured 组下，模拟会话不能下单（readOnlyWhenImpersonating）；upgrade-options 和 upgrade-quote 只读，对模拟会话开放，便于客服查看。
- plan_id 的长度和格式校验沿用 validPlanID；不接受 coupon_code 字段（DisallowUnknownFields 会直接拒绝）。
- 快照和时间窗不对用户序列化；日志不打印 pay_url。
- 限流沿用 scopePayOrder；报价接口受全局 API 限流约束，每次报价最多读本周期的订单（数量很少），不需要单独限流。

### 13.2 配置

不新增环境变量。开关和最低补差放在 shop_settings 里，由后台修改。升级接口的总前提是 PAYMENT_ENABLED。

### 13.3 doctor

- orders.upgrade_review：处于人工审核状态的升级订单数（大于 0 时警告）。
- plans.upgrade_group_gap：对每一对“A 可以升级到 B”的上架套餐，如果存在 A 能用、B 不能用的启用线路组，列出套餐对和组名（警告级）。注意：干净生产库的测试要求没有警告，种子数据下这一项必须为 0。
- shop.upgrade_settings：设置行缺失，或最低补差超出范围。

## 14. 兼容、发布与回滚

### 14.1 新旧组合

| 组合 | 行为 |
| --- | --- |
| 新后端 + 旧前端 | 旧前端仍把其他套餐显示为不可更换；如果用户直接调用普通下单，后端返回区分方向的新提示。没有新的报错 |
| 旧后端 + 新前端 | /meta 里没有 plan_upgrade_supported，前端退回本地判定，不调用新接口，不发送新字段 |
| 回滚到旧二进制（dbe75ca）后，升级订单的回调或人工确认落到旧二进制上 | 旧 fulfilOrder 把 duration_days = 0 的订单交给 usablePlan，判定失败（errPlanInvalid），转为人工审核或报 fulfil_conflict，永远不会被当成普通订单开通 |
| 回滚期间旧二进制写入的订单 | kind 取默认值 purchase，没有时间窗；再升级到新版后走兜底窗口 |
| 旧二进制读取新列 | GORM 忽略未知列；旧版控制台把升级订单显示成“B / 0 天”，只影响显示 |

### 14.2 发布顺序

先后端（迁移随启动自动执行），确认 doctor 没有新的警告后再发布网站。APP 不需要发布。

### 14.3 回滚

可以直接回滚二进制，新列留在库里无害。回滚前用 doctor 查看是否有处于人工审核的升级订单，并在 docs/payment-operations.md 里写明处理方法。

## 15. 三轮实施与逐轮验收

每轮开工先读本文和第 18 节的上一轮记录。跑 Go 数据库测试时，NATIVE_TEST_DSN 必须和 go test 在同一条命令里导出，否则测试会静默跳过。worktree 里的前端要先执行 npm ci --prefer-offline。

### 第 1 轮：后端闭环

交付：
1. 迁移：orders 的 9 个新列、shop_settings 的 2 个新列，以及迁移测试（旧行的默认值、check 约束、false 值写入）。
2. 纯函数定价引擎（建议新文件 api/upgrade.go）：关系判定、天数、V、K（含兜底窗口）、参考日流量、新增流量、最低补差。全部用整数运算，并有溢出保护。
3. 所有新的已付订单在履约时写入 covers_from/covers_to（applyPlan 返回续费前后的到期时间）。
4. 三个新接口、/meta 字段、普通下单的 pending 规则和按方向区分的提示。
5. fulfilOrder 按类型分支，applyUpgrade 落实第 7 节和第 8.3 节。
6. 控制台订单视图字段、商店设置的两项、审计。
7. doctor 的三项检查。
8. 更新现有断言（第 2 节表中的测试），并新增测试文件。

| 编号 | 验收项 | 通过标准 |
| --- | --- | --- |
| R1-01 | 算例 | 第 6.6 节的 7 个例子精确到分和字节，分别在 TZ=Asia/Shanghai、UTC、America/New_York 下运行 |
| R1-02 | 不变量随机测试 | 随机生成至少 2000 条轨迹（购买、续费、年付和月付混合、优惠码、测试价、赠送和缩短有效期、连续升级、时间推进），用一个独立的精确有理数模拟器验证：每次升级的 K 都不超过真实残值；升级金额 + 真实残值 ≥ V；新增流量 ≥ 0 且不超过 (B − A) 日流量 × Nc；订阅流量不超过 1 PiB |
| R1-03 | 关系判定 | 种子套餐和自定义套餐（价格更高但流量更少、设备更少、没有专线、价格相同）都得到正确的 relation 和提示文字；测试订阅、已过期订阅都返回 new |
| R1-04 | 端到端（假网关） | 例 1 的流程：报价 → 下单 → 回调 → 订阅的 plan、设备数、专线、流量都正确，starts_at 和 expires_at 不变；周期账本行同步；version 加 1；推荐奖励保留 |
| R1-05 | 客户端会话 | 已绑定的原生会话在升级后得到 subscription_changed；升级前发出的节点授权在同一周期内的迟到用量被扣到升级后的余额上 |
| R1-06 | 年付持有 | 例 3：本周期新增流量正确；下一次重置后额度为 300 GiB；跨过重置后才履约也正确 |
| R1-07 | 并发 | 第 8.4 节的 7 个场景 |
| R1-08 | 拒绝路径 | 剩余不足 24 小时、开关关闭、降级、平级、有 pending 订单、price_changed、幂等冲突、模拟会话下单、账号停用 |
| R1-09 | 旧二进制兼容 | 在 Go 测试里构造“旧版 fulfil”的路径：duration_days 为 0 的升级订单只会转人工；旧订单没有时间窗时的兜底窗口有专门的用例（例 5 后半） |
| R1-10 | doctor | 种子库没有警告；人为构造线路组缺口和审核中的升级订单时，报出对应警告 |
| R1-11 | 全量回归 | test-backend-standalone.py 和 test-native.py 全部通过（backend 包需要 -timeout=20m） |

### 第 2 轮：网站

交付：第 11 节的全部内容；新的 plans-upgrade.spec.ts 加入 test-payment-web.py；夹具新增账户（例如 upga 持有月付轻量，upgb 持有年付轻量，upgc 持有进阶，upgd 持有测试订阅，upge 的剩余时间不足 1 天），以及控制台的商店设置测试。

| 编号 | 验收项 | 通过标准 |
| --- | --- | --- |
| R2-01 | 卡片关系 | upga 看到“续费 / 升级到进阶 / 升级到旗舰”；upgc 看到轻量为“到期后可购买”加降级提示；upgd 全部为购买；upge 的升级按钮禁用并显示原因 |
| R2-02 | 升级弹窗 | 金额、抵扣、天数、到期日和权益变化全部来自报价；没有优惠码输入框；显示重新登录提示；lost_groups 警告可见 |
| R2-03 | 支付闭环 | 假网关付款后，结果页显示“已升级到”，订单记录显示升级行，仪表盘显示新套餐，到期日不变 |
| R2-04 | price_changed | 在测试钩子里推进时钟跨过整天边界后再点支付：先显示新价格，不会打开网关 |
| R2-05 | pending | 已有 pending 订单时，升级给出提示和订单记录链接 |
| R2-06 | 年付视图 | 切到年付后，升级按钮和价格不变，并显示说明文字 |
| R2-07 | 旧后端 | 模拟 /meta 里没有 plan_upgrade_supported：页面不请求新接口，文案为“到期后可购买” |
| R2-08 | 后台 | 订单详情显示升级明细；关掉升级开关后 /plans 的升级按钮禁用；最低补差的修改有审计记录 |
| R2-09 | 文案与无障碍 | ui-copy.spec、copy-guard 通过；新按钮有可读的 aria-label；深色主题截图正常 |
| R2-10 | 现有用例 | shop.spec.ts、payment.spec.ts 等全部仍然通过 |

### 第 3 轮：全链路回归、发布演练与文档

| 编号 | 验收项 | 通过标准 |
| --- | --- | --- |
| R3-01 | 发布演练 | 在 test-release-rehearsal.py 里执行：新版完成一次升级 → 回滚到 dbe75ca，回调和人工确认都只会转人工，旧版下单正常 → 再升级到新版，旧版期间的订单用兜底窗口计算，结果正确；全程数据指纹一致 |
| R3-02 | 混合部署 | 第 14.1 节的四种组合，各有自动化或脚本证据 |
| R3-03 | Core 与 APP 兼容 | 第 12 节的 Core 场景测试，以及至少桌面（macOS/Windows 二选一）和 Android billing 世界各一个“升级断开”场景通过；iOS 如果没有签名环境就写 BLOCKED，并说明原因 |
| R3-04 | 安全审查 | 逐条复核第 6.8 节，另查：金额篡改、对别人订单的重放、在模拟会话下单、通过 unknown field 注入优惠码、在报价与下单之间改套餐价格、旧二进制开通。结论写进 docs/payment-operations.md，并配套 security_review 测试 |
| R3-05 | 文档 | docs/payment-operations.md 增加“套餐升级”一节（规则、算法说明和算例、后台设置、人工审核的处理办法、doctor 警告、回滚、U-41 … U-46）；README 和 backend/DEPLOYMENT.md 各增加一段 |
| R3-06 | 全量 CI | 所有 CI 任务通过（Android billing 偶发失败可按惯例重跑，并记录在案） |

## 16. 测试资产与证据要求

### 16.1 开发方可执行的测试

Go 单元测试和数据库测试（第 1 轮）、Playwright 加假网关（第 2 轮）、发布演练和 Core 场景（第 3 轮）。每轮的证据放在 artifacts/plans-roundN-*：命令、输出摘要、截图。没有实际运行的项目不得写 PASS。

### 16.2 用户部署后的实测（追加到现有 U 系列之后）

| 编号 | 操作 | 期望 |
| --- | --- | --- |
| U-41 | 普通账户真实支付购买月付轻量，第二天在 /plans 升级到进阶 | 弹窗金额与订单金额一致；付款后到期日不变，总流量增加量与弹窗一致，设备 5 台，可以看到专线 |
| U-42 | 升级时，macOS、Windows、Android、iOS 客户端保持连接 | 在一个上报或心跳间隔内断开，并提示“套餐已变更，请重新登录”；重新登录后可以连接专线 |
| U-43 | 持有进阶时查看轻量卡片，并尝试直接购买 | 按钮禁用，显示降级不退差价和到期日的提示 |
| U-44 | 年付轻量升级到进阶，等到下一次 30 天重置 | 升级当周期按弹窗新增流量，重置后额度为 300 GiB |
| U-45 | 管理后台查看这笔升级订单；把最低补差改为 2 元后重新打开升级弹窗 | 显示升级明细；最低补差生效（在剩余较少的场景可见） |
| U-46 | 后台关闭“允许有效期内升级” | /plans 的升级按钮禁用；已经打开的弹窗下单得到明确提示 |

## 17. 完成定义

- 三轮的 PR 都已合并，CI 全部通过，第 18 节有每轮的记录和证据路径。
- 第 6.6 节的算例和第 6.8 节的不变量都有自动化测试保证。
- 降级和不可更换在接口和页面上都有明确的“到期后购买”提示，并且不退款。
- 文档和 U-41 … U-46 清单已交给用户；没有执行的项目如实标注。

## 18. 实施记录

（每轮完成后追加：日期、分支、PR、验收表逐项的 PASS / FAIL / BLOCKED 与证据、遗留问题。）

### 第 1 轮：后端闭环（2026-11-02，分支 plans-round1-backend，基线 main dbe75ca，PR 见合并记录）

**交付**
- 纯函数引擎 backend/internal/billing/upgrade.go：关系判定（D6）、Nc/Nk、V（向上取整）、按订单时间窗计算的 K（向下取整）、新增流量（新套餐的部分向下取整，原套餐的部分向上取整）、最低补差、线路组可见性。乘除都用 128 位整数，溢出时拒绝，不回绕。
- backend/internal/api/upgrade.go：evaluateUpgrade、GET /payments/upgrade-options、POST /payments/upgrade-quote、POST /payments/upgrade-orders（下单逻辑在 placeUpgradeOrder 里）、applyUpgrade。
- 履约：fulfilOrder 按订单类型分支（fulfilUpgrade）；普通订单履约时写入 covers_from/covers_to（applyPlan 多返回一个窗口起点）。
- 普通下单：存在 pending 升级单时返回 409 pending_order_exists；plan_change_not_supported 按方向给出不同提示（code 不变）；幂等检查区分订单类型；网关商品名为“{B}·升级”。
- /meta 增加 plan_upgrade_supported。
- 迁移：orders 新增 9 列（含 kind 的 check 约束），shop_settings 新增 2 列（含范围 check）。
- 控制台：订单列表和详情的升级字段、kind 筛选、详情里的 upgrade 快照；商店设置两项，字段可选，旧版后台只提交年付折扣时这两项保持不变，审计记录前后值。
- doctor：Shop 下的 upgrade_* 字段，警告 plans_upgrade_group_gap、upgrade_orders_need_review、shop_upgrade_settings_invalid。

**与规划的差异（均为实现细节，不改变规则）**
1. 引擎放在 internal/billing 而不是 api 包：doctor（operations 包）不能引用 api，需要共用同一套关系判定和线路组规则。api 的 groupVisible 改为委托 billing.GroupVisibleTo。
2. 第 13.1 节写的是“upgrade-quote 对模拟会话开放”，但现有的只读中间件拒绝所有 POST，与 /payments/quote 一致，所以模拟会话下 upgrade-quote 返回 403；只读的 upgrade-options（GET）可用。已加入现有的模拟登录测试。
3. 年付持有的本周期新增流量按 floor(B × N/30) − ceil(A × N/30) 计算，与普通订阅同一个函数。它比规划写的 floor((B − A) × N/30) 至多少 1 字节，方向对商家有利。
4. upgrade-orders 响应里的 other_pending_orders 恒为 0，因为 D9 保证不会有其他未完成订单。

**验收**

| 编号 | 结果 | 证据 |
| --- | --- | --- |
| R1-01 | PASS | TestUpgradeExamplesMonthly（例 1、2、4）、TestUpgradeExampleYearly（例 3）、TestUpgradeExampleRenewalsAndLegacyWindow（例 5 及其旧订单兜底，K = 22.54）、TestUpgradeRefusals（例 6、7）；billing 包的单元测试覆盖取整。都在 TZ=Asia/Shanghai、UTC、America/New_York 下通过 |
| R1-02 | PASS | TestUpgradeInvariantTrajectories：2000 条轨迹，检查了 1384 次升级（103 次触发最低补差，151 次带旧订单兜底窗口，272 次连续升级），用独立的 big.Rat 精确模型验证第 6.8 节各条，可复现。变异检查：把抵扣改成向上取整后，测试立即失败（多抵 1 分），已还原 |
| R1-03 | PASS | billing.TestUpgradeRelation（降级、同价、流量更少、设备更少、失去专线、日流量更低）；TestUpgradeRefusals（lite-plus 判为 lateral，current、new、测试订阅） |
| R1-04 | PASS | 例 1 和例 2 的端到端流程；TestUpgradeKeepsPeriodAndChangesFingerprint：周期行是同一行并已同步，推荐奖励保留，version 加 1，订单页只露出 5 个字段 |
| R1-05 | PASS（函数级） | 升级后 nativeBound 仍为真、nativeAuthorizationBound 为假（客户端会得到 subscription_changed），periodIsCurrent 为真（迟到的节点用量仍计入同一周期）。真实 Core 场景留到第 3 轮 R3-03 |
| R1-06 | PASS | TestUpgradeExampleYearly：重置后额度为 300 GiB；跨过重置后才付款，新周期获得完整的 200 GiB 差额 |
| R1-07 | PASS | TestUpgradeConcurrencyAndStaleOrders：两个标签页同时下单（201 + 409）、管理员改到期时间（转人工）、推荐奖励到账（正常开通）、付款前已到期（转人工）、重复回调（duplicate）、迟到的续费回调与升级回调并发（只开通一笔） |
| R1-08 | PASS | TestUpgradeRefusals：剩余不足 1 天、开关关闭、最低补差、降级和平级、pending 双向互斥、price_changed、幂等重放与冲突、跨接口复用 key、优惠码字段 400、缺 expected_amount、模拟会话 403（console_impersonation_test）；TestUpgradeTestPrice 覆盖 D8。账号停用沿用 lockUserRow 的检查（与普通下单同一路径） |
| R1-09 | PASS | TestUpgradeOrderRefusedByOlderFulfilment：按旧版 fulfilOrder 的读法（天数和流量取自订单），在有效期内和到期后都返回 invalid_plan；TestPlansUpgradeMigrationOverLegacyData：旧库升级后数据指纹不变，旧二进制插入的订单默认为 purchase，check 约束生效 |
| R1-10 | PASS | TestUpgradeDoctor：种子库没有新警告；加入 starter 专属线路组后报出 starter>pro 和 starter>max；审核中的升级订单报出警告 |
| R1-11 | PASS | scripts/test-native.py 退出码 0（api 包 635 s，包括 TestSecurityRouteAccessMatrix）；scripts/test-backend-standalone.py：PASS，187 个文件，含 -race 和 Linux 交叉编译。日志在 artifacts/plans-round1-backend/ |

**未做 / 留给后续轮次**：网站（第 2 轮）。发布演练脚本、混合部署、Core 和 APP 场景、安全审查和文档（第 3 轮）。本轮没有在本地运行 test-release-rehearsal.py 和 test-payment-web.py，交给 CI。
