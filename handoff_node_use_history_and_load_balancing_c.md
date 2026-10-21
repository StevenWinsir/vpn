# AsterLink VPN：在线设备、流量历史与节点组负载均衡技术规划交接书

> 文件：handoff_node_use_history_and_load_balancing_c.md
> 编写日期：2026-10-20
> 代码基线：项目 /Users/stevenlee/Desktop/vpn，main，HEAD a63a882（PR #49 合并后）；以本次读取到的后端、共享 Core、网站和四个 APP 的核心源码为准。
> 文档性质：实施规划与验收约定，不代表相关功能已经开发或测试通过。全文不包含代码示例。
> 既定前提：项目采用客户端鉴权、计费、流量统计并经 API 同步后端。本规划不讨论该模式的风险，也不改变现有 production 配置保护；所有新功能在 `client_reported`（客户端上报）与 `node_reported`（节点 Agent）两种计量模式下同等工作。

## 1. 目标、范围与交付组织

三项需求共用同一个事实来源：**在线连接租约（下文简称"租约"）**。一台设备真正接入线路时获取租约，连接期间随既有的 60 秒计量上报续期，断开时释放；中途异常则在 90 秒内自动过期。这个租约同时用于以下四件事：

1. 仪表盘"连接与客户端"显示的在线设备；
2. 每用户同时在线设备上限（默认 2 台）；
3. 每台真实服务器的在线设备数、容量限制和负载均衡；
4. 设备离线后释放服务器名额。

流量历史复用同一条计量写入事务，按 5 分钟粒度汇总，严格只保留 24 小时。

| 轮次 | 本轮形成的可验收结果 | 主要依赖 |
| --- | --- | --- |
| 第 1 轮 | 数据模型、存量节点迁移为"一组一台"、配置项、后台维护任务骨架；对现有客户端零行为变化 | 现有 PostgreSQL、Go API |
| 第 2 轮 | 后端在线租约：获取、续期、释放、过期；设备上限改为按真实在线计算；流量 5 分钟汇总写入与 24 小时清理；`/me/devices`、`/me/usage-history` 接口 | 第 1 轮 |
| 第 3 轮 | 后端节点组分配器：负载率最低优先、同负载随机、用户固定分配、容量并发保护、故障排除与重新分配；客户端目录改为节点组；管理端组与服务器 API | 第 2 轮 |
| 第 4 轮 | 网站：仪表盘"连接与客户端"真实在线展示、24 小时柱状图；`/admin/nodes` 节点组管理；用户 `/nodes` 页改为节点组 | 第 2、3 轮 |
| 第 5 轮 | 共享 Go Core 适配新协议：连接时获取租约、断开释放、被重新分配时自动重连一次、旧释放重试、iOS 会话交接保护；新契约夹具与 Core 对真实后端的端到端测试 | 第 3 轮 |
| 第 6 轮 | 四平台 APP 集成新 Core，平台生命周期（睡眠、后台、系统终止、服务停止）下的在线状态与释放验收 | 第 5 轮 |
| 第 7 轮 | 全链路回归、并发与容量压测、旧客户端兼容、发布与回滚演练 | 前六轮 |

组织方式沿用之前的做法：每轮使用一个 worktree 分支（建议命名 `worktree-nodes-roundN-<主题>`），开一个 PR，并在本文第 16 节追加实施记录。PASS/FAIL/BLOCKED/NOT RUN 必须如实记录，未执行的项目不得标为通过。

本期不做：网页端踢设备下线、设备自定义名称、后端主动探测上游服务器、计费账本（`client_traffic_reports`、`node_usage_reports`）的保留期调整、Redis 或消息队列、WebSocket 推送。

## 2. 已核实的代码基础与衔接位置

### 2.1 后端

| 核心位置 | 当前实现事实 | 对本次工作的影响 |
| --- | --- | --- |
| [backend/internal/model/models.go](backend/internal/model/models.go) | `Node` 只含名称、地区、线路类型、倍率、启用、版本；`Plan`/`Subscription` 有 `max_devices`（种子：starter 2、pro 5、max 10） | `Node` 改为"真实服务器"，公开属性上移到新的节点组；设备上限规则见决策 D1 |
| [backend/internal/model/node_config.go](backend/internal/model/node_config.go) | `node_configs` 存加密 YAML 与 `plan_ids` JSON | 加密 YAML 仍按服务器保存；套餐权限迁到节点组 |
| [backend/internal/model/native.go](backend/internal/model/native.go) | `NativeSession` 按设备 UUID、平台、版本、绑定节点、倍率、计量来源、累计计数保存；`client_traffic_reports` 每次上报一行 | 租约关联原生会话与设备；流量汇总在同一事务写入 |
| [backend/internal/api/native.go](backend/internal/api/native.go) | 同设备重新登录会撤销旧会话；`nativeTransaction` 锁顺序为用户→会话→订阅，并刷新 `last_seen_at`；`checkDeviceLimit` 在 `/config` 绑定时统计"已绑定且 180 秒内出现过"的会话 | 现有上限统计的是"登录且选过线路"，不是"正在连接"，这是仪表盘与上限都不准确的根源；改为按租约统计 |
| [backend/internal/api/node_catalog.go](backend/internal/api/node_catalog.go) | `catalogProfile` 加载全部启用节点，按套餐、专线权益、Agent 管理过滤；`renderProfile` 生成"一代理 + 一 select 组 + MATCH"文档，并要求代理名等于节点名；目录标签由可见目录元数据哈希得到 | 目录列出节点组；渲染时把代理名改写为组名；分配器只加载一个组的服务器 |
| [backend/internal/api/node_watch.go](backend/internal/api/node_watch.go) | `/client/nodes` 长轮询：进程内通知加每秒轮询，按会话读取，修订号由目录内容哈希得到 | 修订号加入本会话的租约摘要，用于让被重新分配的设备在数秒内感知 |
| [backend/internal/api/admin_nodes.go](backend/internal/api/admin_nodes.go) | 批量导入 YAML、节点名全局唯一（`node_name_exists`）、乐观版本、advisory lock 708614922、审计；上限 128 节点 | 组内允许同名；增加组接口、容量与在线数；上限分别作用于组和服务器 |
| [backend/internal/api/grants.go](backend/internal/api/grants.go)、[agent_api.go](backend/internal/api/agent_api.go) | node_reported 模式下每会话一个 grant，Agent 同步、到期对账；`createGrant` 也调用 `checkDeviceLimit` | grant 创建走同一个分配器并在同一事务建立租约；grant 结束时租约同步结束 |
| [backend/internal/api/commerce.go](backend/internal/api/commerce.go) | `/me/dashboard` 返回订阅、订单数，`metering_connected`、`proxy_service_ready` 固定为 false | 增加在线设备与流量历史接口，修正固定值 |
| [backend/internal/authmail/reset.go](backend/internal/authmail/reset.go) | 密码重置撤销全部网页与原生会话 | 被撤销会话的租约必须立即失效（见 5.4） |
| [backend/internal/store/store.go](backend/internal/store/store.go)、[operations/readiness.go](backend/internal/operations/readiness.go) | AutoMigrate 加 advisory lock 的追加式迁移；doctor 按固定表清单检查 | 新表进入迁移和就绪清单 |
| [backend/cmd/api/main.go](backend/cmd/api/main.go) | 已有 grant 对账循环（15 秒）和认证邮件清理循环（1 小时） | 新增统一维护循环：租约过期、历史清理、健康事件清理 |
| [backend/internal/config/config.go](backend/internal/config/config.go) | 上报 60 秒、租约 90 秒、空闲 180 秒、长轮询 5 秒，并有启动校验 | 在线租约直接复用 `CLIENT_LEASE_SECONDS`，不再引入第二套计时 |

### 2.2 共享 Core、网站与 APP

| 核心位置 | 当前实现事实 | 对本次工作的影响 |
| --- | --- | --- |
| [native/core/internal/managed/](native/core/internal/managed/manager.go) | 四个平台共用的 Go Core（Android 经 gomobile AAR，iOS 经 xcframework，macOS/Windows 原生嵌入）。登录后 `keepAlive` 每 60 秒请求 `/session`，连接后由 Meter 每 60 秒上报 `/traffic`；`stopTunnel` 是唯一的断开路径；generation 防止迟到响应生效 | 租约获取、释放和重新分配只需在 Core 实现一次 |
| [native/core/internal/managed/actions.go](native/core/internal/managed/actions.go)、[tunnel.go](native/core/internal/managed/tunnel.go) | client_reported 模式在"选择线路"时就调用 `/config` 绑定并预取配置；node_reported 模式选择时只记录选择，连接时才创建 grant | 改为两种模式一致："选择只记录，连接时才获取租约和配置"，与"容量在实际接入时生效"一致 |
| [native/core/internal/managed/configstore.go](native/core/internal/managed/configstore.go) | `ParseProfile` 要求代理名等于目录项名 | 后端把代理名改写为组名，Core 校验规则不变 |
| [native/core/internal/apiclient/messages.go](native/core/internal/apiclient/messages.go) | 错误码中文文案在 Core 中统一维护；`device_limit` 已有文案 | 新增码只加在 Core；APP 直接显示 Core 给出的文案 |
| [native/core/internal/managed/handoff.go](native/core/internal/managed/handoff.go) | iOS 主 App 与隧道扩展之间交接会话；持有 grant 或有未结尾账时拒绝交接 | 持有租约或有未完成的释放时同样拒绝交接 |
| [shared/contracts/snapshot-v1.json](shared/contracts/snapshot-v1.json) | Core→UI 快照契约；Android `Protocol.kt` 使用 `ignoreUnknownKeys = false` 严格解码 | **本期不改变快照结构**：节点组以现有 `nodes[]` 的形式出现，新状态通过现有 `reason`/`last_error` 表达 |
| [frontend/src/components/dashboard-view.tsx](frontend/src/components/dashboard-view.tsx) | "连接与客户端"卡片是写死的空状态；流量概览只有环形图 | 改为真实在线列表，并新增柱状图卡片 |
| [frontend/src/components/admin-nodes-view.tsx](frontend/src/components/admin-nodes-view.tsx)、[admin-agents-panel.tsx](frontend/src/components/admin-agents-panel.tsx) | 节点平铺表格、导入/编辑/删除弹窗；Agent 面板每 5 秒刷新 | 重构为"套餐筛选 → 节点组 → 服务器"三层；Agent 面板继续按服务器绑定 |
| [frontend/package.json](frontend/package.json) | Mantine 8，没有图表库 | 柱状图用 Mantine 布局加原生 SVG/CSS 实现，不新增图表依赖 |

## 3. 统一设计与待确认决策

### 3.1 名词

- **节点组（Node Group）**：管理员可见、客户端可见的线路，例如"JP1"。它携带公开元数据：名称、地区、线路类型、倍率、套餐权限、启用状态、排序、版本。客户端只看到节点组。
- **服务器（Server）**：组内的一台真实上游，即现在的 `nodes` 行。它持有加密 YAML、最大在线设备数、启用状态、健康状态和管理员备注。同组服务器允许使用相同的代理名称。
- **在线设备**：持有有效租约的设备。有效的条件是：状态为 active、`expires_at` 晚于服务端当前时间、所属原生会话未撤销且未过期。只登录、未点击连接的设备不算在线。
- **已登录未连接**：原生会话在 180 秒空闲窗口内活跃，但没有有效租约。仪表盘单独列出，不占名额。
- **负载率**：服务器有效租约数除以最大在线设备数。比较时用整数交叉相乘，不使用浮点。
- **固定分配（亲和）**：每个用户在每个节点组内记录一台"首选服务器"。

### 3.2 三项需求的交集与统一取舍

- 设备上限、服务器容量、仪表盘在线状态、名额释放都只读租约表，**不另设计数器字段**，避免计数漂移。
- 租约在"实际接入"时获取：client_reported 模式通过新接口 `POST /client/connections`，node_reported 模式通过 `POST /client/grants`，两者使用同一个分配器。拉取目录、登录、选择线路都不占名额，满足"不以订阅拉取次数计算"。
- 续期复用已有请求，**连接期间不增加任何额外请求**：client_reported 由每 60 秒的 `/traffic` 续期；node_reported 由会话持有有效 grant 时的 `/session` 保活续期。
- 释放分三层：Core 主动释放 → 撤销路径同事务释放 → 90 秒自然过期。维护循环只做清理和记录原因；正确性依赖查询条件中的时间判断，不依赖维护循环是否按时执行。
- 流量历史与计费在同一事务内写入，不会出现扣费了却没有历史，或有历史却未扣费的情况。

### 3.3 待确认决策（已给出默认取舍，实施者按默认执行，用户可推翻）

| 编号 | 问题 | 默认取舍 | 理由 |
| --- | --- | --- | --- |
| D1 | "默认 2 台"与套餐已有 `max_devices`（2/5/10）的关系 | 有效上限取订阅快照 `max_devices` 与新配置 `CLIENT_MAX_ONLINE_DEVICES`（默认 2）中的较小值 | 立即对所有用户生效 2 台，不改已售订阅，也不改权益指纹；以后调高全局值即可让 pro/max 的 5/10 生效 |
| D2 | 服务器"故障"如何判定 | 硬排除：管理员停用、删除、node_reported 模式下 Agent 离线。软排除：短时间内多个不同用户上报连接失败后进入冷却。后端不主动探测上游 | 上游服务器不受控；软排除采用"不同用户数阈值 + 自动恢复"，防止单个用户恶意让节点下线 |
| D3 | 存量节点迁移后组 ID 如何生成 | 每个存量节点生成一个同 ID 的节点组（组 ID = 原节点 ID），服务器保留原 ID | 已安装客户端和测试夹具中的节点 ID 继续有效，旧客户端按原 ID 请求时会被解析为组 |
| D4 | 计费账本是否也只留 24 小时 | 不改。24 小时清理只作用于新增的流量历史汇总表 | 账本是扣费和幂等重放的依据；它的体积问题另行规划（每台在线设备约 1440 行/天） |
| D5 | 满载状态是否显示在客户端目录中 | 不显示，接入时返回明确错误 | 负载随时变化，放进目录修订号会让所有客户端的长轮询频繁唤醒 |

## 4. 数据模型与迁移

### 4.1 新表与新字段（追加式，不删除旧列）

**node_groups（节点组）**：ID（uuid）、名称、地区、线路类型（direct/dedicated）、倍率（千分比 1–10000）、套餐 ID 列表（JSON 文本，空表示所有套餐，与现有 `plan_ids` 语义一致）、启用、排序、版本（乐观锁，只在公开元数据变化时递增）、创建与更新时间。名称沿用 `nodepolicy` 对代理名称的约束（非空、长度上限、无控制字符）。约束：可见范围有交集的组（包括"所有套餐"）之间不能重名；用户在客户端永远不会看到两个同名线路。

**nodes（服务器，原表扩展）**：新增 `group_id`（外键指向 node_groups，组非空时禁止删除组）、`max_connections`（1–10000，必填）、`label`（管理员备注，最长 40 字，仅后台显示，用于区分组内同名服务器）、`unhealthy_until`（软冷却截止时间，可空）。原有 `name` 改为"YAML 中的代理名"，**取消全局唯一检查**，只要求同一次导入的 YAML 内不重名（`nodes.Parse` 已保证）。服务器 `version` 只在 YAML 或启用状态变化时递增；修改容量、备注或冷却状态不递增，避免无谓断开在线用户。

**过渡期双写**：`nodes.region/line_type/rate_permille` 和 `node_configs.plan_ids` 在保存组时同步写入组的值，保留一个发布周期，保证回滚到旧二进制时数据仍然自洽（见第 14 节）。

**connection_leases（在线租约）**：ID、用户 ID（外键，随用户删除）、原生会话 ID（外键，随会话删除）、设备 ID、平台、App 版本、节点组 ID、服务器 ID（不设外键：服务器删除后保留历史，状态改为 ended）、获取方式（`connections`/`legacy_config`/`grant`）、grant ID（仅 grant 方式）、状态（active/ended）、获取时间、最近续期时间、`expires_at`、结束时间、结束原因（`released`/`expired`/`session_revoked`/`node_unavailable`/`replaced`/`entitlement_ended`/`tunnel_unhealthy` 等白名单值）、获取时的会话累计上下行（用于计算"本次连接流量"）。部分唯一索引：同一（用户，设备）只能有一条 active，同一会话只能有一条 active。

**node_assignments（固定分配）**：（用户 ID，组 ID）为主键，记录服务器 ID、分配时间和最近使用时间；组或服务器删除时级联删除。30 天未使用的记录由维护循环清理。

**usage_buckets（流量历史汇总）**：（用户 ID，`bucket_start`）为主键，`bucket_start` 为向下取整到 5 分钟的服务端 UTC 时间；字段为上传字节、下载字节、计费单位（按倍率折算，与 `used_units` 口径一致）、更新时间。随用户删除。

**node_health_events（健康事件）**：服务器 ID、用户 ID、原因、时间。只保留 1 小时，只用于软冷却判定。

### 4.2 存量迁移

在既有迁移事务内（`pg_advisory_xact_lock(708614921)`）执行：对每个 `group_id` 为空的服务器，按 D3 创建一个同 ID 的组，复制名称、地区、线路类型、倍率、启用状态和 `node_configs.plan_ids`，然后回填 `group_id`。`max_connections` 迁移值取 1000（相当于不限，避免上线即锁死用户），后台提示管理员改为真实值；新建服务器的表单默认值为 5。迁移可重复执行：已有 `group_id` 的服务器跳过。迁移不修改订阅、会话、账本，也不修改既有 `nodes.version`。

### 4.3 锁顺序约定（所有新写路径必须遵守）

用户行 → 原生会话行 → 订阅行 → 节点组行（FOR UPDATE）→ 租约/亲和/服务器读取与写入。

- 分配器在同一组内依靠节点组行锁串行化，跨组互不阻塞；设备上限依靠用户行锁串行化。
- 管理端保存或删除组、服务器时，先获取 advisory lock 708614922，再锁节点组行，再锁服务器行。管理端不锁用户，因此与客户端路径不会形成环。
- 维护循环只用带条件的 UPDATE/DELETE 修改租约、汇总、事件和亲和行，不加用户锁。
- `/traffic` 续期在现有 `nativeTransaction` 内完成，只更新本会话的 active 租约，不锁节点组。

### 4.4 索引、体积与清理

- 租约：部分索引（服务器 ID，`expires_at`）和（用户 ID，`expires_at`），条件为 state = active；ended 行保留 24 小时后删除。
- 汇总：主键即可满足"用户 + 时间范围"查询。单用户每天最多 288 行；1 万日活约 288 万行、约数百 MB 以内，并按 24 小时滚动删除。
- 管理端在线数用一次分组聚合查询得到，避免现有 Agent 面板那种逐行 N+1 查询。
- 维护循环（API 进程内，随服务启动和停止，使用 `pg_try_advisory_lock` 避免多实例重复执行）：每 15 秒把过期租约标记为 ended（原因 expired，或会话已撤销时为 session_revoked）；每 5 分钟分批删除过期汇总行，单批有上限，避免长事务；每小时清理健康事件、ended 租约和过期亲和。所有时间都由 Go 侧时钟传入，不使用数据库 `now()`，以便测试注入时钟。

## 5. 在线租约与设备上限（需求 1）

### 5.1 生命周期

1. **获取**：在用户行锁内校验权益、会话绑定和流量批次序号（与现有 `/config` 的 `last_sequence` 规则一致，确保切换线路前旧尾账已确认）。然后结束本设备的旧 active 租约（无论是否已过期，原因 replaced），统计本用户**其他设备**的有效租约数，与有效上限比较，超出则返回 `device_limit`。最后调用分配器（第 6 节），写入租约，`expires_at` 设为当前时间加租约时长，并把会话的 `node_id` 和倍率绑定到所选服务器与组倍率。
2. **续期**：只延长"本会话 active 且尚未过期"的租约，已过期的不复活。续期前重新判断权益和服务器可用性；不满足时不续期，并在快照中返回原因。
3. **释放**：Core 断开时，先停止数据通道、确认尾账，再释放租约。释放请求幂等，失败时由 Core 在保活循环中重试（与现有孤儿 grant 的重试方式相同）。
4. **过期**：90 秒没有续期即视为离线，被硬杀、断网、休眠的设备最长 90 秒后让出名额。维护循环在之后 15 秒内补记结束原因；名额判断本身不需要等维护循环。

### 5.2 两种计量模式的续期来源

- client_reported：只有 `/traffic` 续期。断开后 Core 只会为结清尾账发送 `/traffic`，紧接着就会释放租约，不会出现"断开了还一直续期"。`/session` 在此模式下不续期，避免已登录但未连接的 App 占用名额。
- node_reported：会话持有有效 grant 即表示已连接，`/session` 续期；grant 进入 revoking/revoked/expired 时，租约在同一事务结束。Agent 离线导致 grant 失效时，租约随之结束。
- 两种模式的续期周期都由服务端返回的 `report_interval_seconds` 决定，租约长度都是 `lease_seconds`（90 秒），与现有授权租约一致。客户端的连接授权与在线租约同时到期，不会出现"能上网但后台显示离线"的长期偏差。

### 5.3 设备上限

- 有效上限按 D1 计算。上限变低时，不踢出已在线设备，只拒绝新接入。
- "同一设备重复连接不重复计数"由三点保证：（用户，设备）部分唯一索引；获取时先结束本设备旧租约；同设备重新登录时撤销旧会话。
- 设备 ID 是客户端持久化的 UUID，不是硬件证明。两台机器伪造同一 ID 时，每次登录会把另一台踢下线，因此无法借此长期共享名额。
- 原 `checkDeviceLimit` 改为基于租约实现；`/config` 与 `/grants` 不再按"绑定会话"计数。

### 5.4 撤销路径必须同步结束租约

以下位置在撤销原生会话的同一事务内把该会话的 active 租约改为 ended：同设备重新登录、`/client/logout`、密码重置的全部会话撤销（authmail 包）、管理员停用账号、订阅变化导致授权失效。即使某条路径遗漏，统计查询也会联表排除已撤销或已过期的会话，结果仍然正确，遗漏只影响结束原因的记录。

### 5.5 旧版客户端兼容

旧 Core 不会调用新接口，过渡期这样处理：

- 旧 Core 通过 `/config?node_id=` 选择线路。后端把该 ID 解析为组（D3 保证 ID 有效），在 `/config` 内完成分配并建立获取方式为 `legacy_config` 的租约，同样受设备上限和服务器容量约束。之后由 `/traffic` 续期，90 秒无上报自然过期。
- 服务器被停用或重新分配时，对 legacy 租约返回旧 Core 已认识的 `profile_changed`，旧 Core 会重新请求 `/config`，从而被分到新服务器。
- 四个平台都发布新 Core 后，运营可通过 `CLIENT_MIN_APP_VERSION` 逐步淘汰旧版本。本期把该门禁从仅作用于 `/grants` 扩展到 `/config` 和 `/connections`。

## 6. 节点组分配与负载均衡（需求 3）

### 6.1 目录与可见性

客户端目录与网站 `/nodes` 都列出节点组。一个组可见需同时满足：组已启用；套餐在组的允许列表内（空列表表示所有套餐）；专线组要求订阅允许专线；组内至少有一台已启用的服务器（production 下还要求该服务器受 Agent 管理）。条目字段保持现有 `nodeChoice` 结构：ID 为组 ID，名称、地区、线路类型、倍率来自组，版本为组版本。冷却和满载**不影响**可见性，也不进入修订号（D5），因此负载变化不会唤醒长轮询。

组的计量方式：组内有受 Agent 管理的服务器则为 node_reported，并只在受管服务器中分配；否则为 client_reported。管理端在组内混合两种服务器时显示警告。

### 6.2 分配算法（接入时，在节点组行锁内执行）

1. **候选集**：组内已启用、所属组仍对该用户可见、配置可解密且能通过校验的服务器；node_reported 组还要求 Agent 在线且支持该协议。请求中带 `avoid_node_id` 时排除该服务器（仅用于连接失败后的一次自动重试）。
2. **硬排除与软排除**：冷却中的服务器先放在一边；只有在没有任何"健康且未满载"的候选时才启用冷却中的服务器（降级）。管理员停用、删除和 Agent 离线属于硬排除，任何情况下都不分配。
3. **本设备复用**：本设备在本组已有有效租约时，沿用原服务器（计算负载时排除本设备自己）。
4. **固定分配**：用户在本组的首选服务器仍在候选集内且未满载时，直接使用，不比较负载（满足"不因负载变化频繁切换"）。
5. **负载均衡**：在其余未满载候选中选负载率最低的；负载率相同时均匀随机。随机源可注入，便于测试复现。
6. **全组满载**：返回 `group_full`；没有任何可用候选时返回 `group_unavailable`。
7. **亲和更新**：首次分配时写入首选服务器；首选服务器因停用、删除、Agent 离线或冷却而不可用时，更新为新服务器；首选服务器只是满载、新设备被分到别的服务器时，**不改**首选，用户之后的设备仍优先回到原服务器。

负载率按服务器各自的 `max_connections` 归一化，因此容量不同的服务器也能按比例平铺。"同一用户的不同设备分别计数"由每设备一条租约保证。

### 6.3 容量与并发

- 节点组行锁保证同组接入串行化，统计负载与写租约之间不会被插入。同时到达的请求都按串行顺序判断，因此不会超过上限。
- 管理员调低 `max_connections` 时，不踢出已在线设备，只拒绝新接入，后台显示"超出"。
- 拉取目录、登录、选择线路都不分配、不占位。

### 6.4 异常处理与重新分配

- **管理员停用或删除服务器**：在保存事务内，把该服务器上的 active 租约改为 ended（原因 node_unavailable），并通知目录长轮询。受影响的新 Core 会话会在 `/session`、`/traffic` 回执或目录长轮询中收到 `connection_reassigned`。Core 停止隧道、确认尾账，然后对同一组自动重新接入一次（分配器会避开不可用的服务器并更新亲和）。旧 Core 会话收到 `profile_changed`（见 5.5）。
- **停用或删除节点组**：组内全部租约结束，原因 node_unavailable；组从目录消失，Core 走现有的"所选线路已不可用"路径，不自动重连。
- **软冷却**：Core 在连接后验证失败（`tunnel_unhealthy`、`tunnel_failed`）时，释放请求带上原因，后端记录一条健康事件。5 分钟内有 `NODE_HEALTH_FAILURE_USERS`（默认 3）个**不同用户**报告同一服务器失败时，进入 `NODE_HEALTH_COOLDOWN_SECONDS`（默认 300 秒）冷却，到期自动恢复，管理员也可以手动清除。冷却只影响新分配，**不踢出**在线设备，以免误判打断正常用户。
- **连接失败重试**：Core 在验证失败后，带 `avoid_node_id` 重新接入一次，若组内还有其他候选，就会换一台服务器；只重试一次，第二次失败直接提示用户。
- **满载**：返回 `group_full`，提示"该线路当前已满，请稍后再试或选择其他线路"，Core 不自动重试。

### 6.5 配置渲染与版本

客户端拿到的文档仍是"一代理 + 一 select 组 + MATCH"，代理名改写为节点组名，Core 的 `ParseProfile` 名称校验不需要修改。目录标签仍由可见目录元数据（组）计算。以下事件会让本会话的配置失效：

- 修改某台服务器的 YAML：只影响该服务器上的会话（服务端比对配置版本，返回 `profile_changed`）；
- 修改组的公开元数据（名称、倍率、线路类型等）：影响该组全部会话；
- 修改容量、备注或冷却状态：不影响任何会话。

## 7. 流量历史（需求 2）

### 7.1 写入

在 `/traffic` 新批次入账时，于同一事务内把本次的上传增量、下载增量和计费单位累加到本用户"当前 5 分钟桶"（服务端接收时间向下取整），采用"不存在则插入，存在则累加"。node_reported 模式在 Agent 用量入账（`agentUsage`）的同一事务内做同样处理，时间取后端接收时间。重放批次（`replayed=true`）不重复累加。一次上报跨越桶边界时，整批记入接收时所在的桶，误差不超过一个上报周期。

### 7.2 读取与分段

`GET /me/usage-history` 以服务端当前时间向下取整到 5 分钟作为基线 T，窗口为（T−24 小时，T+5 分钟）。窗口从最早一端起划分为 8 个 3 小时区间，最后一个区间包含正在进行的 5 分钟。每个区间返回起止时间（UTC）、上传字节、下载字节、计费单位，没有数据的区间补 0。查询条件中始终带窗口下界，因此即使清理任务滞后，也不会把超过 24 小时的数据展示出来。

### 7.3 24 小时严格保留

维护循环每 5 分钟分批删除 `bucket_start` 早于 T−24 小时的行，保证库中不存在超出窗口的历史。删除只作用于 `usage_buckets`；计费账本按 D4 不变。用户删除时汇总行级联删除。

## 8. API 契约

### 8.1 原生客户端（`/api/v1/client`，Bearer 会话）

| 方法与路径 | 说明 |
| --- | --- |
| POST `/connections` | client_reported 组接入。需要 `Idempotency-Key`；请求体为组 ID、`last_sequence`、可选 `avoid_node_id`。成功返回租约（ID、组 ID、状态、到期时间）、服务器 ID（不透明）、YAML、版本和会话快照。同一 key 重放时返回同一结果，换组则返回 `idempotency_conflict`；对 node_reported 组返回 `grant_required` |
| POST `/connections/:id/release` | 幂等释放，请求体只含白名单原因（`user`、`stopped`、`tunnel_unhealthy`、`tunnel_failed`、`network_changed`、`lifecycle`）；只能释放本会话的租约 |
| POST `/grants` | 现有接口。`node_id` 现在表示组 ID（传入旧服务器 ID 时解析为它所属的组），新增可选 `avoid_node_id`；在同一事务内完成分配和租约建立 |
| GET `/config` | 仅为旧 Core 保留：带 `node_id` 时按 5.5 建立 legacy 租约 |
| GET `/session`、POST `/traffic` | 会话快照新增可选字段：`connection_id`、`connection_state`、`connection_expires_at`；租约被服务端结束时，对新 Core 的原因为 `connection_reassigned`，对 legacy 租约为 `profile_changed` |
| GET `/nodes` | 目录项为节点组；新增本会话 `connection` 摘要（ID、组 ID、状态），并计入修订号 |
| GET `/capabilities` | `contract_versions` 增加 3 |

### 8.2 网站用户（Cookie 会话）

- `GET /me/devices`：服务端时间、有效上限、在线数；在线列表每项包含平台、App 版本、设备短标识（设备 UUID 后 6 位）、线路组名、连接开始时间、最近心跳、本次连接上下行；"已登录未连接"列表包含平台、版本、短标识、最近活动时间。**不返回**服务器 ID、地址、会话 ID 或令牌。
- `GET /me/usage-history`：见 7.2，额外返回 `bucket_seconds=10800`、`granularity_seconds=300`、窗口起止时间和合计。
- `GET /me/dashboard`：`metering_connected` 改为"本账户 24 小时内是否有计量数据"，不再写死；新增 `online_devices` 与 `device_limit` 摘要，方便首屏一次性渲染。

### 8.3 管理端（管理员 Cookie）

- `GET /admin/node-groups`：返回组列表，每组包含公开属性、计量方式、在线合计、容量合计，以及服务器摘要（ID、备注、代理名、协议类型、启用、健康状态与冷却截止、最大在线、当前在线、版本）。在线数由一次聚合查询得到。
- `POST /admin/node-groups`、`POST /admin/node-groups/:id`：创建或更新，带乐观版本、同可见范围重名检查和审计（`node_group.saved`）。
- `DELETE /admin/node-groups/:id`：只允许删除空组，需带版本；前端引导先删服务器。
- `POST /admin/nodes`、`POST /admin/nodes/:id`：请求体改为 YAML、组 ID、`max_connections`、备注、启用、版本；地区、线路、倍率、套餐字段移到组。批量导入时，每个代理生成一台服务器，全部归入同一个组。
- `DELETE /admin/nodes/:id`：结束该服务器的租约，并删除对应的固定分配。
- `POST /admin/nodes/:id/health`：清除冷却，并记审计。

请求体上限沿用：`/admin/nodes` 路径前缀为 128 KiB，其余 8 KiB。所有管理写操作都写入 `admin_audits`。

### 8.4 新增与沿用的错误码

| 码 | HTTP | 中文文案（写入 Core 文案表与网站） |
| --- | --- | --- |
| `device_limit`（沿用） | 403 | 已达到同时在线设备上限，请先断开其他设备 |
| `group_full` | 409 | 该线路当前已满，请稍后再试或选择其他线路 |
| `group_unavailable` | 503 | 该线路暂无可用服务器，请选择其他线路 |
| `connection_reassigned` | 快照原因 | 线路服务器已调整，正在为你重新连接 |
| `connection_not_found` | 404 | 连接记录不存在，请重新连接 |
| `connection_expired` | 快照原因 | 在线状态已过期，请重新连接 |
| `grant_required` | 409 | 此线路需使用节点授权连接 |

对旧 Core 而言，新码会显示为通用文案"操作失败，请稍后重试"，不会导致崩溃；兼容路径（5.5）尽量返回旧 Core 已认识的码。

## 9. 网站前端

### 9.1 仪表盘"连接与客户端"

- 标题行显示"在线设备 x / 上限 y"徽章，满额时用醒目颜色。
- 在线设备列表：平台图标、App 版本、设备短标识、线路组名、"已连接 xx 分钟"、"最近心跳 xx 秒前"、本次连接上下行。
- 次级列表"已登录未连接"，说明这些设备不占名额。
- 保留"专线权限"一行；"设备额度"改为有效上限，并注明"超过上限的新设备连接时会被拒绝；异常离线的设备最长约 90 秒后自动让出名额"。
- 页面可见时每 15 秒刷新（前端运行时配置 `DASHBOARD_REFRESH_MS`，允许 5000–120000），页面隐藏时暂停，回到前台和网络恢复时立即刷新，并发请求合并为一个，卸载时取消。实现方式沿用 `nodes-view` 已有的轮询模式。
- 修正原先写死的文案："尚未接入代理服务""开发环境说明"等按真实计量状态显示。

### 9.2 24 小时流量柱状图

- 新增卡片"近 24 小时流量"，8 根柱，每根代表 3 小时；X 轴标签用浏览器本地时区显示起止时刻（如"09:05–12:05"），最右一根标注"至现在"。
- 每根柱为上传、下载两段堆叠；悬停或聚焦时显示上传、下载、合计和按倍率折算的计费流量。单位自动选择 MB/GB，与站内 1 GiB = 1,073,741,824 字节的口径一致。
- 使用 SVG 或 CSS 自行绘制，配色遵循现有 Mantine 主题；可访问性：每根柱有 aria 标签，并提供"以表格查看"的折叠视图。全为 0 时显示空状态。
- 与在线列表一起刷新，但历史接口独立请求，一个失败不影响另一个。

### 9.3 `/admin/nodes`

- 顶部套餐筛选：全部 / 不限套餐 / 各套餐（来自 `/plans`）。"在某套餐下新建节点组"时，默认带入当前筛选的套餐。
- 组卡片或可展开表格行：组名、地区、线路、倍率、可用套餐、启用、计量方式、"在线 x / 容量 y"负载条、服务器数。操作：编辑组、向组内导入服务器 YAML、停用、删除（仅空组可删）。
- 展开后的服务器表格：备注、代理名、协议、启用、健康（正常 / 冷却至 hh:mm / Agent 离线）、"在线 / 上限"、版本。操作：编辑（YAML、容量、备注、启用）、清除冷却、删除。
- 在线数每 5 秒刷新，与 Agent 面板节奏一致；Agent 面板的下拉框改为"组名 / 服务器备注"。
- 文案明确说明：容量在设备实际连接时生效；调低容量不踢出在线设备；停用或删除服务器会让其上设备自动改连同组其他服务器；同组服务器对用户统一显示为组名。
- 删除确认弹窗写明受影响的在线设备数。

### 9.4 用户 `/nodes`

改为显示节点组，字段与过滤规则保持不变；继续不展示任何连接参数。

## 10. 共享 Core 与四平台适配

### 10.1 Core 改动（一处实现，四平台受益）

- **apiclient**：新增接入、释放请求和对应类型；会话快照增加三个可选字段；目录增加 `connection` 摘要；补充第 8.4 节的新文案；`TerminatesSession` 不加入新码。
- **选择线路**：client_reported 也改为"只记录选择"，与 node_reported 一致，`config_ready` 沿用 node_reported 的"已选即就绪"语义。macOS 等界面依赖 `config_ready` 显示"已就绪"，因此显示保持不变。
- **连接流程**：确认旧尾账 → 刷新会话 → 接入（获取租约与配置）→ 校验配置 → Meter 开始并确认首报 → 启动引擎 → 验证。验证失败时，带失败原因释放，并带 `avoid_node_id` 自动重试一次。node_reported 流程只把 grant 的 `node_id` 换成组 ID。
- **断开流程**：在现有 `stopTunnel` 中，确认尾账之后、状态迁移之前释放租约（限时 5 秒）。失败则记录待释放 ID，由保活循环重试；登出和关闭进程沿用同一路径。
- **重新分配**：监控循环收到 `connection_reassigned` 时，走普通停止流程，然后对同一组自动重新接入一次。期间快照状态依次为 stopping → connecting → connected，`last_error` 显示"线路服务器已调整，正在为你重新连接"。自动重连失败则停在断开状态，并显示失败原因。
- **Android 恢复门禁（ResumeGate）**：睡眠或断网导致租约过期后，恢复时的重新确认路径如果收到 `connection_expired` 或 `connection_reassigned`，就重新接入；如果此时名额已被其他设备占用（`device_limit`），则停止并提示。
- **iOS 会话交接**：持有 active 租约或有待释放的租约时，交接返回 `handoff_unsettled`；交接票据里的 `selected_node_id` 改为组 ID，票据结构不变。
- **快照结构不变**：不新增快照字段，因此 Android 严格解码器、Windows/macOS/iOS 解码器和 `snapshot-v1.json` 都不需要升级版本。若实施中确实需要新字段，必须在同一轮内同时更新四个解码器和契约文件。

### 10.2 平台差异与验收重点

| 平台 | Core 运行位置 | 需要验证的点 |
| --- | --- | --- |
| macOS | App 进程内 | 正常断开立即释放；合盖睡眠后最长 90 秒在后台显示离线；唤醒后重新接入；退出 App 时释放 |
| Windows | AsterLink.Service 服务 | 服务停止、系统关机时经 `Shutdown` 释放；UI 进程退出不影响服务内连接的在线状态 |
| Android | VPN 服务进程（gomobile AAR） | 后台时目录长轮询暂停，靠 `/traffic` 回执感知重新分配；Doze 后租约过期与恢复；系统撤销 VPN（onRevoke）时释放 |
| iOS | 连接期间由隧道扩展持有会话 | 扩展内释放；系统杀死扩展后 90 秒过期；主 App 与扩展交接时的保护；需要真机签名的项目在没有付费团队时记录为 BLOCKED |

各 APP 本身不需要业务改动：只重建并嵌入新 Core，界面列表的名称自然变为组名，错误文案来自 Core。需要更新的是：各平台展示和截图测试中涉及错误码的夹具；Android `PresentationTest`、iOS `PresentationTests` 增加新码用例；平台验收脚本增加"设备上限"和"重新分配"场景。

## 11. 配置项

后端（写入 `backend/.env.example` 与部署文档，启动时校验范围）：

- `CLIENT_MAX_ONLINE_DEVICES`：默认 2，范围 1–100（D1）。
- `NODE_HEALTH_FAILURE_USERS`：默认 3，范围 2–50。
- `NODE_HEALTH_COOLDOWN_SECONDS`：默认 300，范围 60–3600。
- 租约时长复用 `CLIENT_LEASE_SECONDS`，续期周期复用 `CLIENT_REPORT_INTERVAL_SECONDS`，不新增计时配置。
- 历史粒度 5 分钟、区间 3 小时、窗口 24 小时是需求本身，写成常量，不提供配置。

前端：`DASHBOARD_REFRESH_MS` 由 Next.js 服务端运行时读取，只把间隔传给浏览器，读取方式与 `NODE_LIST_REFRESH_MS` 相同。

## 12. 分轮实施与测试验收

通用要求：后端测试使用 `python3 scripts/test-native.py`（私有临时 PostgreSQL、`-race`）；Core 在 `native/core` 下运行 `go test -race ./...`；前端运行 `tsc`、`eslint`、`next build`。涉及时间的后端逻辑必须支持注入时钟和随机源，测试不得依赖真实等待 90 秒或 24 小时。

### 第 1 轮：数据模型、迁移、配置与维护任务骨架

**工作内容**：完成 4.1 的全部表和字段；4.2 的存量迁移（含双写）；第 11 节后端配置项与启动校验；维护循环骨架（只接入汇总清理、过期租约标记、事件和亲和清理，此时还没有业务写入）；doctor/readiness 表清单；`.env.example` 与部署文档。客户端可见行为不变：目录仍按旧逻辑输出，由于组 ID 等于原节点 ID，输出内容完全一致。

**测试**：在旧库快照（由 a63a882 版本迁移并写入若干节点、会话、账本的库）上执行迁移，并重复执行一次，确认幂等；迁移后组、服务器一一对应，字段复制正确，`max_connections` 为 1000，订阅、会话、账本、版本号均未改变；旧二进制在迁移后的库上能正常启动和读取目录（回滚可行性）；配置项边界值、非法值启动失败；维护循环在注入时钟下删除边界正确（早于 T−24h 的删除，等于 T−24h 的保留）；多实例只有一个实例执行清理。

**验收**：全量后端测试和 `go vet` 通过；`TestWebNodeListPostgres`、目录契约测试不修改断言即可通过；doctor 报告新表存在。

### 第 2 轮：后端在线租约、设备上限与流量历史

**工作内容**：第 5 节全部功能。此轮分配器暂用"组内唯一服务器"（迁移后一组一台），第 3 轮替换为完整算法。具体包括：`POST /connections` 与释放接口；`/traffic` 和 `/session` 续期规则；`/config` 的 legacy 租约；`/grants` 建立租约并随 grant 结束；五条撤销路径同步结束租约；`checkDeviceLimit` 基于租约重写；快照的三个新字段与新原因；第 7 节的汇总写入（`/traffic` 与 `agentUsage`）与读取接口；`/me/devices`；`/me/dashboard` 字段修正；`CLIENT_MIN_APP_VERSION` 门禁扩展。

**测试**：

- 租约状态机：获取、续期、释放、过期、被替换、会话撤销后联表失效、已过期不续期、释放幂等、跨会话释放被拒绝。
- 设备上限：上限 2 时同一用户 10 个设备并发接入，恰好 2 个成功；同设备反复接入始终只占 1 个名额；第三台设备在一台断开后立即成功，在一台被硬杀后要等第 90 秒以后才成功（注入时钟）；D1 下全局值与套餐值取小。
- 计量一致：`/traffic` 重放不重复累加历史；扣费与汇总在同一事务（用 GORM 回调注入故障，汇总写入失败时整笔回滚、不扣费）；node_reported 的 Agent 用量同样写入历史。
- 撤销：同设备重新登录、登出、密码重置（复用 `auth_reset_test` 的夹具）、账号停用后租约立即结束，`/me/devices` 不再显示。
- 历史接口：跨 30 小时写入后，8 个区间的划分、补零、合计正确；窗口下界过滤生效；只能看到自己的数据。
- 兼容：用旧契约夹具模拟旧 Core 走 `/config` + `/traffic` 全流程，租约建立、续期、过期都正确。
- 契约：更新 native-v2 的确切快照比较，新增 `shared/contracts/native-v3.json`。

**验收**：上述测试在 `-race` 下全部通过；`/me/devices` 和 `/me/usage-history` 的响应字段白名单测试证明不泄露服务器 ID、会话 ID、令牌或地址；单次 `/traffic` 处理时间相对第 1 轮基线增加不超过 20%（记录 p50/p95）。

### 第 3 轮：后端节点组分配器、负载均衡与管理端 API

**工作内容**：第 6 节全部内容（候选集、硬/软排除、本设备复用、固定分配、最低负载率、同负载随机、满载与无可用、亲和更新、停用或删除时重新分配、软冷却与健康事件、配置渲染改名、版本规则）；目录与网站 `/nodes` 改为节点组；第 8.3 节管理端 API；取消服务器名全局唯一，增加同可见范围组名唯一。

**测试**：

- 均衡：5 台服务器各容量 5，25 个新用户依次接入，每台恰好 5 个；容量为 2/4/6 的组按比例分配；同负载时注入随机源验证每台都可能被选中，生产随机源下 1000 次分配分布大致均匀。
- 固定分配：用户首次分到 s3，之后登出、重新登录、刷新目录后仍是 s3，即使 s3 负载已是最高；s3 满载时用户的新设备分到他处，而首选仍为 s3；s3 停用后在线设备收到 `connection_reassigned`，重新接入后分到他处，首选更新。
- 容量并发：总容量 25 的组同时收到 200 个不同用户的接入请求，恰好 25 个成功，其余为 `group_full`，数据库中不存在超额租约；多组并发互不阻塞；管理员保存与客户端接入并发时不死锁（`-race` 加超时检测）。
- 异常：停用、删除服务器后在线租约立即结束、新分配排除该服务器；整组停用后组从目录消失；node_reported 下 Agent 失联时租约随 grant 结束；软冷却需达到不同用户阈值才触发（同一用户重复上报不触发），到期自动恢复，冷却中的服务器只在全组无健康候选时启用。
- 版本：改 YAML 只影响该服务器上的会话；改容量、备注、冷却不改变任何会话的配置版本，也不改变目录修订号；改组倍率触发全组 `profile_changed`。
- 管理 API：乐观版本冲突、空组才能删除、同范围重名拒绝、审计记录、在线数聚合查询正确（与逐行统计结果对比）。
- 回归：既有 grant、Agent、目录、管理节点的测试按新语义更新后全部通过。

**验收**：全部后端测试通过；容量并发测试连续运行 20 次零超额；在本机私有 PG 上，500 并发接入同一组时 p95 低于 200 ms（记录数值；若不满足，在本轮记录中写出瓶颈分析与取舍）。

### 第 4 轮：网站仪表盘、柱状图与节点组管理页

**工作内容**：第 9 节全部；`frontend/src/lib/types.ts` 新增类型（继续保持"不包含连接参数"的显式白名单）；运行时配置 `DASHBOARD_REFRESH_MS`；`/nodes` 页改为组。

**测试**：

- 单元：区间标签的时区换算、单位格式、空数据、刷新间隔边界（沿用 `scripts/node-list.test.mjs` 的风格新增测试）。
- 真实浏览器（扩展 `scripts/test-node-catalog.py` 与 Playwright）：管理员在"轻量计划"下建 JP1 组，导入 5 台同名服务器、各容量 5；后台显示 5 台、在线 0/25。用测试夹具模拟两台设备接入后，仪表盘 15 秒内显示 2/2，第三台被拒；断开一台后显示 1/2。后台服务器在线数同步变化。停用一台服务器后，其上设备转移，后台计数随之变化。
- 柱状图：写入跨 26 小时的固定历史后，页面显示 8 根柱，数值与接口一致；手机宽度下布局不溢出；键盘可以聚焦到每根柱并读出数值。
- 安全：浏览器存储与页面源码中不出现 YAML、服务器地址或服务器 ID（沿用现有目录测试的检查方式）。

**验收**：`tsc`、`eslint`、`next build`（worktree 中使用 `--webpack`）通过；节点目录浏览器验收全绿；截图存入本轮 artifacts 目录。

### 第 5 轮：共享 Go Core 适配与端到端

**工作内容**：第 10.1 节全部；Core 的契约测试切换到 native-v3 夹具；`cmd/asterlink-billtest` 增加 device_limit、group_full、reassign 场景。

**测试**：

- Core 单元（`managed` 包，使用假服务器）：选择不发请求；连接时只接入一次；断开时先结尾账后释放；释放失败后由保活重试且不重复释放；登出路径也会释放；`connection_reassigned` 触发停止 → 结账 → 重接一次，第二次失败则停住；验证失败带 `avoid_node_id` 重试一次；迟到响应因 generation 失效不能复活旧租约；持有租约时拒绝交接。
- Core 对真实后端（`backend/internal/api` 下现有 Core 端到端测试的模式）：两个 Core 实例模拟两台设备，第三个被拒；断开后后端立即可见离线；杀掉一个 Core（不释放）后约 90 秒（注入时钟）名额回收；管理员停用服务器后 Core 自动改连同组另一台，账本序号连续，历史累计正确。
- 真实流量（billtest 加 Mihomo 受控节点）：重新分配前后，账本合计等于两台受控节点各自看到的字节数之和（在现有协议计数误差说明范围内）。

**验收**：`native/core` 下 `go test -race ./...` 和 `go vet` 通过；后端全量测试仍然通过；快照契约 `snapshot-v1.json` 没有改动（git diff 为空）。

### 第 6 轮：四平台 APP 集成与生命周期验收

**工作内容**：用新 Core 重建四个平台的产物；更新各平台展示和截图夹具中的新码；平台验收脚本增加 10.2 节的场景；文档 `docs/*-billing-acceptance.md` 补充在线状态章节。

**测试与证据**：

- macOS：`scripts/test-native-macos.py` 全量；本机实测正常断开、睡眠唤醒、退出 App 后网站在线状态的变化时间（记录秒数）。公司 VPN 冲突导致无法实测的项目记为 BLOCKED，并写明原因。
- Windows：`windows.yml` CI 全绿；服务停止和关机场景使用现有 Windows 可靠性测试框架；本机没有 dotnet 时，以 CI 结果为准，并在记录中说明。
- Android：`android.yml` CI（模拟器）全绿，含 `PresentationTest` 新码、onRevoke 释放、Doze 模拟后重新接入；本机没有 JDK/SDK 时以 CI 为准。
- iOS：`ios.yml` CI 全绿（Kit 测试加模拟器宿主的计量测试框架）；真机隧道扩展场景在没有付费团队签名时记录为 BLOCKED。
- 每个平台确认自己的 CI 检查确实出现在 `gh pr checks` 中（之前出现过 workflow 名称含冒号导致整个 job 静默失效的教训）。

**验收**：四平台 CI 全绿；每个平台至少一次"设备上限拒绝"与"重新分配自动重连"的证据（日志或截图）；未能执行的项目逐条列出 NOT RUN/BLOCKED。

### 第 7 轮：全链路回归、压测、兼容与发布演练

**工作内容与测试**：

- 回滚演练：`scripts/test-release-rehearsal.py` 增加本期场景：旧版本 → 本期版本（迁移、双写）→ 回滚到 a63a882。回滚后旧二进制可以服务，客户端看到的是"每台服务器一条线路"的旧行为，这一点在记录中说明。
- 旧客户端兼容：用发布前的 Core 构建（或 legacy 夹具）连接新后端，走 `/config` 全流程，在线状态、上限、容量都生效。
- 压测：模拟 2000 台在线设备每 60 秒上报，持续 30 分钟（注入时钟加速，或在私有 PG 上真实运行一个缩短周期的版本）。记录 `/traffic` 的 p95、维护循环单批耗时、`usage_buckets` 行数与表体积、长轮询 CPU；确认 24 小时后汇总表行数稳定不再增长。
- 全量：后端、Core、前端、节点目录浏览器验收、四平台 CI 再跑一轮。
- 文档：`docs/admin-node-metering.md`、`docs/user-node-list.md`、`docs/native-client-protocol.md`、`backend/DEPLOYMENT.md` 更新。

**验收**：全部自动化测试通过；压测数据写入记录；发布清单和回滚步骤经过一次完整演练；剩余的设备实测项目逐条列明状态。

## 13. 测试资产与证据要求

- 复用现有测试框架，不为测试新增生产后门。时钟和随机源只通过测试构造函数注入，生产构建中没有切换入口。故障注入沿用 GORM 回调加请求上下文标记的方式（注意 `Statement.Table` 不带 schema 前缀，例如 "nodes"）。
- worktree 注意事项（来自前几轮经验）：`frontend/node_modules` 用符号链接指向主 checkout，并且不得提交；`next build` 在 worktree 中使用 `--webpack`，`test-node-catalog.py` 使用 `--skip-build`；私有 PG 用 `initdb --no-locale -E UTF8`，并在 `LC_ALL=C` 下启动；Mihomo 子模块从主 checkout 本地克隆，不要在 worktree 中执行 `git submodule init`。
- 每轮记录格式：分支和 PR；交付文件清单；每条测试的命令与结果（PASS/FAIL/BLOCKED/NOT RUN）；关键数值（并发成功数、延迟、行数）；遗留问题及其归属轮次。

## 14. 发布顺序与回滚

1. 备份数据库，在演练库上执行迁移并验证（第 1 轮的脚本）。
2. 发布后端：此时所有客户端仍是旧 Core，通过 legacy 路径获得在线统计和上限；网站同时发布。
3. 管理员把迁移得到的 `max_connections=1000` 改为真实容量，再按需要把同类服务器合并到同一组（把服务器移到目标组，再删除空组）。
4. 依次发布四平台新 Core 的 APP；全部可用后，按需要设置 `CLIENT_MIN_APP_VERSION`。
5. 回滚边界：后端可以回滚到 a63a882，因为双写保证旧列仍然有效；回滚后组的概念消失，客户端看到每台服务器一条线路，在线统计与上限回到旧语义。新表保留，不需要删除。管理员在回滚期间对组做的修改，再次升级时以组表为准。

## 15. 最终完成定义

- 仪表盘真实显示正在连接的设备（正常连接或断开 15 秒内可见，异常离线约 90 秒内可见），默认最多 2 台同时在线，第三台设备在客户端连接时被拒绝并有明确提示。
- 仪表盘展示以当前时间为基线、8 个 3 小时区间的柱状图；数据库中不存在超过 24 小时的历史汇总行。
- 管理员可以在套餐下创建节点组、导入同名的多台服务器、为每台设置容量，并看到每台的实时在线数与上限。
- 客户端每组只拿到一台服务器的配置，统一显示组名；分配遵循"本设备复用 → 固定分配 → 最低负载率 → 同负载随机"；满载不接入；故障、停用或删除时自动排除并改连；离线后释放名额；并发接入不超过上限。
- 两种计量模式、旧版客户端、四个平台均经过验证，或被如实记录为 BLOCKED/NOT RUN。

## 16. 实施记录

（每轮完成后在此追加：轮次、日期、分支/PR、交付、测试结果、关键数据、遗留问题。）

### 第 1 轮：数据模型、迁移、配置与维护任务骨架（2026-10-20）

- **分支/PR**：`worktree-nodes-round1-foundation`，PR 见 GitHub（StevenWinsir/vpn）。基线 a63a882。
- **交付文件**
  - `backend/internal/model/presence.go`（新）：`NodeGroup`、`ConnectionLease`、`NodeAssignment`、`UsageBucket`、`NodeHealthEvent`；租约状态/获取方式/结束原因常量；历史粒度 5 分钟、区间 3 小时、窗口 24 小时及健康事件 1 小时、ended 租约 24 小时、亲和 30 天的保留常量；`UsageBaseline`/`UsageCutoff`（T 与 T−24h）。
  - `backend/internal/model/models.go`：`Node` 新增可空 `group_id`（外键 RESTRICT）、`max_connections`（默认 1000，CHECK 1–10000）、`label`（40 字，默认空）、`unhealthy_until`；全部 `json:"-"`，管理端与网站响应形状不变。
  - `backend/internal/store/store.go`：五张新表进入 AutoMigrate；租约部分索引（每设备/每会话唯一 active，服务器/用户 + expires_at，ended_at）；`backfillNodeGroups` 按 D3 为无组服务器建同 ID 组（复制名称、地区、线路、倍率、启用、版本与 `plan_ids`，缺配置行时为 `[]`），再回填 `group_id`；可重复、可收编回滚期间旧版写入的服务器，已有组不覆盖。
  - `backend/internal/api/admin_nodes.go`：过渡双写 `mirrorLegacyGroup`——保存服务器时维护其单服务器组（只在公开元数据变化时递增组版本，仅改 YAML 不递增）；删除最后一台服务器时删除空组。
  - `backend/internal/config/config.go`：`CLIENT_MAX_ONLINE_DEVICES`（2，1–100）、`NODE_HEALTH_FAILURE_USERS`（3，2–50）、`NODE_HEALTH_COOLDOWN_SECONDS`（300，60–3600），越界启动失败；零值 Config 的访问器回落默认值。
  - `backend/internal/maintenance/`（新）：`Sweeper` 三类扫描（15 秒租约过期并记录 `expired`/`session_revoked`；5 分钟分批删除 `bucket_start < T−24h`；每小时清理健康事件、ended 租约、过期亲和）；守护事务持有 `pg_try_advisory_xact_lock(708614923)`，其他实例整次跳过；所有语句单批有上限并 `SKIP LOCKED`；时钟注入，不使用数据库 `now()`。`cmd/api/main.go` 随服务启动和停止。
  - `backend/internal/operations/readiness.go`：表清单加入五张新表；新增 `node_groups`、`ungrouped_servers` 与 `servers_without_group_run_migration` 警告。
  - `scripts/test-node-groups-migration.py`（新，第 14 节第 1 步所述演练脚本）；`scripts/test-release-rehearsal.py` 表清单加入新表（否则其“迁移后表集合”断言会失败）。
  - `backend/.env.example`、`backend/DEPLOYMENT.md`（新章节：迁移、容量初值、配置、维护循环、双写、doctor、演练与回滚）。
- **测试结果**

| 编号 | 项目 | 命令 | 结果 |
| --- | --- | --- | --- |
| R1-01 | 旧库形态上迁移、并发两次 + 再一次幂等；17 张旧表全部列逐行指纹不变；组与服务器一一对应、字段复制、`max_connections=1000`；索引存在 | `go test -race ./internal/store`（`TestNodeGroupMigrationOverLegacyData`） | PASS |
| R1-02 | 回滚可行性（库层）：旧版列集插入服务器使用默认值；旧版删除服务器不受外键阻挡；再次迁移收编新服务器且不覆盖已编辑的组；有服务器的组不可删 | 同上 | PASS |
| R1-03 | 真实二进制演练：a63a882 经其 API 产生 5 台服务器/订阅/原生会话/账本 → 当前二进制迁移两次 → 原生目录（含修订号）、所选配置 YAML 与版本、网站目录、管理列表与旧版逐字节一致 → doctor 无缺表、5 组、0 无组 → a63a882 在迁移后的库上启动、目录一致、上报流量、增改删服务器 → 再迁移收编、组表优先、账本连续 | `python3 scripts/test-node-groups-migration.py --output /tmp/vpn-r1-drill-1` | PASS（7 项检查，9.3 秒） |
| R1-04 | 租约约束：每设备/每会话一条 active、ended 历史不限、非法状态/方式被拒、随会话和用户级联删除；历史行非负且随用户删除 | `TestConnectionLeaseConstraints` | PASS |
| R1-05 | 配置默认值、边界值（1/100、2/50、60/3600）通过，越界与非数字启动失败且错误只含变量名 | `go test ./internal/config`（`TestPresenceSettingsDefaultsAndBounds`） | PASS |
| R1-06 | 维护循环注入时钟边界：早于 T−24h 删除、等于 T−24h 保留、5 分钟后边界行出窗；小批量多批次；健康事件/ended 租约/亲和保留边界 | `go test -race ./internal/maintenance` | PASS |
| R1-07 | 租约过期原因：`expires_at == now` 视为过期；会话撤销记 `session_revoked`；会话到期记 `expired`；未过期与已结束行不动；重复扫描不改行 | `TestExpireLeasesRecordsReasons` | PASS |
| R1-08 | 多实例：一个实例扫描中（GORM 回调暂停在删除语句）另一实例整次跳过；外部持锁时三类扫描均跳过；释放后正常执行；`Run` 启动即扫描并随 context 停止 | `TestSweepRunsOnOneInstanceAtATime`、`TestRunStopsWithContext` | PASS |
| R1-09 | 管理端双写：批量导入每台一个同 ID 组；仅改 YAML 组版本不变；改倍率/套餐/启用/线路地区各递增一次；管理列表仍是原 8 个字段；删除服务器删除空组 | `TestAdminNodeSaveMirrorsLegacyGroupPostgres` | PASS |
| R1-10 | doctor：直接写入的无组服务器被诊断，迁移后清零 | `TestConsoleReadinessAndAdministratorPostgres` | PASS |
| R1-11 | 全量后端测试（私有 PG、`-race`），`TestWebNodeListPostgres` 与目录契约测试未改断言 | `python3 scripts/test-native.py` | PASS（本机：298 个 PASS、0 FAIL；7 个按环境变量启用的夹具装配 SKIP，与基线相同） |
| R1-14 | 节点目录真实浏览器验收 | `scripts/test-node-catalog.py --skip-build`（CI） | 以 PR 的 CI 结果为准；本轮无前端改动，本机未运行 |
| R1-12 | `go vet ./...`、`gofmt -l`、共享快照一致、仅后端目录独立构建 | `go vet`、`scripts/sync-backend-shared.py --check`、`scripts/test-backend-standalone.py` | PASS |
| R1-13 | 发布演练脚本（`test-release-rehearsal.py`）全流程 | — | NOT RUN（本轮只更新其表清单；第 7 轮执行） |

- **关键数据**：演练库 5 台服务器 → 5 组，`ungrouped_servers=0`；回滚后旧版新增 1 台 → 再迁移 6 组；账本 2 行，`used_units = 7000 字节 × 1.2 = 8,400,000`，跨版本连续。
- **遗留与归属**：在线租约获取/续期/释放、设备上限、流量历史写入与接口（第 2 轮）；分配器与组管理 API、组名同可见范围唯一、取消服务器名全局唯一（第 3 轮）；本轮 `max_connections` 只存储不生效。管理端写组时目前在已持有 advisory lock 708614922 的前提下先锁服务器再锁组，第 3 轮改为“组 → 服务器”的约定顺序。
- **合并后补记**：PR #50 合并到 main 后，main 上 `Android lifecycle acceptance` 的 L07-cycles 第 4/10 轮失败，与本轮无关（该任务的后端是不启动维护循环的测试装配，目录与配置输出不变）。根因是测试竞态：`disconnect()` 只等 Core 停止，下一轮看到的可能是上一轮正在注销的 VPN 网络。由 PR #51 修复（每轮等待系统注销全部 VPN 网络，未注销记为失败），#51 CI 全绿，L07 10/10 且无需重试；main 失败任务重跑亦通过。

### 第 2 轮：后端在线租约、设备上限与流量历史（2026-10-21）

- **分支/PR**：`worktree-nodes-round2-leases`，PR 见 GitHub（StevenWinsir/vpn）。基线 af7df05（#51 合并后）。
- **交付文件**
  - `backend/internal/api/lease.go`（新）：租约唯一实现。`liveLeases`（active、未过期、会话未撤销未过期、账号 active，所有统计都用它）；`acquireLease`（锁顺序 用户→会话→订阅→节点组行 FOR UPDATE；先结束本设备与本会话旧租约 `replaced`，再结束授权已因订阅变化失效的其他会话租约 `entitlement_ended`，再按 D1 统计其他设备、按 `max_connections` 统计服务器负载）；`renewLease`/`renewRecording`（只延长未过期租约；有流量的批次用一条带数据修改 CTE 的语句同时累加 5 分钟桶并续期）；`renewClientLease`/`readmitLegacy`；`renewGrantLease`；`endGrantLease`/`endSessionLeases`；`leaseReason`（v3 租约近期过期 `connection_expired`、服务器不可用结束 `connection_reassigned`；legacy 租约过期不提示、服务端结束提示 `profile_changed`；超过空闲窗口的旧结果不再提示）；`groupServer`（本轮“组内唯一服务器”，未分组服务器按 D3 视为自身组）。
  - `backend/internal/api/connections.go`（新）：`POST /client/connections`（Idempotency-Key 重放/换组冲突、`last_sequence`、`avoid_node_id`、Agent 组 `grant_required`）与 `POST /client/connections/:id/release`（原因白名单、幂等、仅本会话）。
  - `backend/internal/api/presence.go`（新）：`GET /me/devices`、`GET /me/usage-history`（基线 T 向下取整 5 分钟，窗口 [T−24h+5m, T+5m)，8 个 3 小时区间补零，下界始终在查询中）。
  - `native.go`：注入时钟；登录同设备、登出结束租约；`/config` 建立 `legacy_config` 租约（替代旧 `checkDeviceLimit`）与版本门禁；`/traffic` 续期、legacy 重新接入、历史写入同事务；`/session` 为 grant 会话续期；快照三个新字段；新批次的会话更新同时写 `last_seen_at`（省去一次更新）。
  - `grants.go`：`node_id` 为组 ID、`avoid_node_id`、grant 与租约同事务、撤销结束租约、`contract_versions` 加 3。`agent_api.go`：Agent 判定撤销/确认移除/对账过期时结束租约；Agent 用量写入历史。`authmail/reset.go`：全部会话撤销同时结束租约。`commerce.go`：`/me/dashboard` 的 `metering_connected`、`online_devices`、`device_limit`。
  - `shared/contracts/native-v3.json`（新，同步到后端 testdata；`sync-backend-shared.py` 与其测试纳入 v3）；native-v2 未改。`docs/native-client-protocol.md`、`backend/DEPLOYMENT.md` 新章节。
- **测试结果**

| 编号 | 项目 | 命令 | 结果 |
| --- | --- | --- | --- |
| R2-01 | 租约状态机：登录/目录/会话读取不占位；获取、幂等重放、换组冲突；`/session` 不续期、`/traffic` 续期；释放白名单/幂等/跨会话拒绝；释放后尾账入账不复活；过期不复活且报告 `connection_expired`、该批仍入账；同设备再接入替换旧租约、只剩一条 active | `TestLeaseLifecyclePostgres`（`-race`） | PASS |
| R2-02 | 设备上限：上限 2 时 10 台设备并发接入恰好 2 台成功；同设备连续接入 5 次仍占 1 位；释放后第三台立即成功；被硬杀设备在第 89 秒仍拒绝、第 90 秒放行（注入时钟）；D1 三种取小组合 | `TestDeviceLimitPostgres` | PASS |
| R2-03 | 撤销：同设备重新登录、登出、密码重置（`authmail.RevokeAllSessions`）立即结束租约且 `/me/devices` 不再显示；停用账号与漏结束的已撤销会话均由查询排除 | `TestRevocationEndsLeasesPostgres` | PASS |
| R2-04 | 旧 Core 兼容：`/config` + `/traffic`，v1 会话字段全部保留；legacy 租约续期、空闲过期不打扰、下次上报重新接入；`/config` 受上限约束；超额的重新接入照常入账并提示 `device_limit`；服务端结束提示 `profile_changed` | `TestLegacyCoreLeasesPostgres` | PASS |
| R2-05 | 历史：按服务端接收时间入 5 分钟桶、重放不累加；历史写入故障注入（GORM Row 回调 + 请求上下文标记）时整笔回滚、不扣费、无账本行、序号不变；8 个区间划分、补零、合计、窗口下界（T−24h 整点与 30 小时前的行不显示）、只看到自己的数据 | `TestUsageHistoryPostgres` | PASS |
| R2-06 | 响应白名单：`/me/devices`、`/me/usage-history` 字段集合精确比较，不含服务器/组/租约/会话 ID、设备完整 UUID、令牌、地址或密钥；`/me/dashboard` 新字段；跨用户不可见 | `TestPresenceResponsesAreAllowListedPostgres` | PASS |
| R2-07 | 接入门禁：`CLIENT_MIN_APP_VERSION` 作用于 `/connections` 与 `/config`；`avoid_node_id`、未知组、停用服务器 → `group_unavailable`；容量 → `group_full`；Agent 组 → `grant_required`；幂等键与严格 JSON | `TestAdmissionGatesPostgres` | PASS |
| R2-08 | node_reported：grant 与租约同建、`/session` 续期、Agent 用量写入历史、撤销结束、会话登出后 Agent 同步结束租约 | `TestGrantLeaseAndHistoryPostgres` | PASS |
| R2-09 | 契约：native-v3 夹具严格解码为在线类型、释放原因与连接状态一致、v3 错误码与会话原因均在代码中出现；新连接/释放/过期快照与 grant 会话的在线响应形状与 v3 一致（原 v2 的两处会话形状比较改为对照 v3） | `TestV3FixtureFitsLiveTypes`、`TestV3CodesAreEmitted` 及上述形状断言 | PASS |
| R2-10 | 全量后端测试（私有 PG、`-race`），含真实 Core 端到端（旧 Core `/config` 路径，含 device_limit）、真实 Node Agent 与 billtest | `python3 scripts/test-native.py` | PASS（最终代码：312 PASS、0 FAIL；8 SKIP = 7 个按环境变量启用的夹具装配 + 按需运行的耗时测试） |
| R2-14 | 仅后端目录独立构建（tests/vet、Linux amd64/arm64） | `python3 scripts/test-backend-standalone.py` | PASS（103 个文件） |
| R2-15 | 新 Core 接入（`/connections`、释放、`connection_*` 原因）、平台生命周期、网站界面 | — | NOT RUN（第 4–6 轮） |
| R2-11 | `/traffic` 耗时（同一私有 PG、完整 handler、1000 次上报、预热 20 次，第 1 轮基线树 af7df05 与本轮交替各 3 次） | `TRAFFIC_TIMING_REPORTS=1000 go test -run TestTrafficTimingBaseline ./internal/api` | PASS：p50 312 → 356 µs（+14%），p95 555 → 600 µs（+8%） |
| R2-12 | 第 1 轮真实二进制迁移/回滚演练在本轮代码上 | `python3 scripts/test-node-groups-migration.py` | PASS（7 项） |
| R2-13 | `go vet`、`gofmt`、共享快照一致与其单元测试 | `go vet ./...`、`sync-backend-shared.py --check`、`test-backend-shared.py` | PASS |

- **关键数据**：并发 10 设备接入成功 2；硬杀设备名额在第 90 秒回收；`/traffic` p50 +14%、p95 +8%。耗时首版为 p50 +29%（续期、历史写入、快照读租约三条额外语句），经“续期返回行”“续期与历史合并为一条 CTE 语句”“新批次会话更新同时写 last_seen_at”降到预算内。
- **设计取舍（实施中确定）**：旧 Core 在选择线路时就调用 `/config`，因此选择会暂时占位最长 90 秒；旧 Core 的租约过期后不在空闲时提示 `profile_changed`（否则旧 APP 闲置 90 秒后显示未就绪），改由它连接时的首报按完整检查重新接入。订阅变化使其他会话授权失效时，接入时即结束它们的租约，避免新设备被不能续期的旧设备挡住 90 秒。仓库中没有管理员停用账号的接口，停用账号由统计查询排除。
- **遗留与归属**：目录长轮询中的本会话 `connection` 摘要与修订号（随重新分配在第 3 轮实现）；健康事件写入（`tunnel_unhealthy`/`tunnel_failed` 本轮只记为结束原因，软冷却在第 3 轮）；完整分配器（第 3 轮）；网站界面（第 4 轮）；Core 适配与 Core 文案（第 5 轮）。发布注意：D1 默认值使 pro/max 同时在线上限立即为 2；`CLIENT_MIN_APP_VERSION` 在新 Core 发布前须保持为空（见 DEPLOYMENT.md）。

### 第 3 轮：后端节点组分配器、负载均衡与管理端 API（2026-10-21）

- **分支/PR**：`worktree-nodes-round3-allocator`，PR 见 GitHub（StevenWinsir/vpn）。基线 c6abd5a（#52 合并后）。本轮无新表、新列、新配置项，无需迁移。
- **交付文件**
  - `backend/internal/api/allocator.go`（新）：分配器。`lockGroup`（组 ID 或旧服务器 ID 解析到组，D3，并取组行锁，锁顺序 用户→会话→订阅→组）；`allocate` 在组行锁内用一条语句读出组内已启用服务器、各自有效租约数（本设备自己的租约不计负载）、本设备所在服务器与用户首选；`pickServer`（纯函数：本设备复用 → 固定分配（健康且未满即用，不比负载）→ 负载率最低（整数交叉相乘）同负载均匀随机 → 只有无健康且未满时才用冷却服务器；全满 `group_full`，无候选 `group_unavailable`）；选中服务器若配置不可解密/不合法或 Agent 不支持该协议则像停用一样排除后重选；`touchAssignment`（首次写入；首选停用/删除/Agent 离线/冷却/刚失败时改为新服务器；仅满载时保留）；`reportFailure`（健康事件 + 5 分钟内不同用户数达 `NODE_HEALTH_FAILURE_USERS` 时冷却 `NODE_HEALTH_COOLDOWN_SECONDS`）。三种接入模式：`/connections`（Agent 管理的组返回 `grant_required`）、旧 Core `/config`、`/grants`（只在 Agent 在线的受管服务器中分配，否则 `agent_unavailable`）。随机源经 `newServerWith` 注入，生产为 math/rand/v2。
  - `backend/internal/api/node_catalog.go`：目录改为节点组。`visibleGroups` 一条联表查询（服务器 + 组，只为选中服务器取密文）+ Agent 表；组可见 = 启用、套餐、专线权益、至少一台可承载的启用服务器（production 须受管）；有受管服务器的组为 node_reported。`catalogProfile` 为指定服务器渲染，代理名改写为组名，倍率等公开属性取组；目录标签仍由可见组元数据计算（组 ID 顺序），网站/长轮询列表按（排序、名称、ID）。
  - `backend/internal/api/admin_groups.go`（新）：`GET/POST /admin/node-groups`、`POST/DELETE /admin/node-groups/:id`（乐观版本、同套餐范围重名 `node_group_name_exists`、只删空组 `node_group_not_empty`、停用/删除组结束组内租约 `group_unavailable`、保存组时双写其服务器旧列与 `plan_ids`、审计）；列表的在线数由一次聚合查询得到，含协议、健康（ok/cooldown/agent_offline/config_error）、`over_capacity`、计量方式与混合警告。
  - `backend/internal/api/admin_nodes.go`：`adminTransaction`（管理员行锁 + advisory lock 708614922，之后一律 组→服务器→租约）；分组请求体（`group_id`、`max_connections`、`label`）导入/编辑/移组，服务器版本只随 YAML（规范化比较）、启用状态、移组递增；停用/删除/移组结束其租约 `node_unavailable`；`POST /admin/nodes/:id/health` 清除冷却与健康事件；取消服务器名全局唯一。旧按服务器请求体保留（第 4 轮前端改造前），只能编辑“一组一台”服务器（否则 `node_group_managed`），重名检查改为同套餐范围组名（仍返回 `node_name_exists`）。删除旧接口产生的同 ID 组服务器时其空组随之删除，组接口建立的组保留。第 1 轮遗留的“先锁服务器再锁组”已改为约定顺序。
  - `connections.go`、`grants.go`、`native.go`：三条接入路径改用分配器；`/config` 的 `group_unavailable` 对旧 Core 改报 `client_config_unavailable`；接入响应的快照直接复用刚渲染的配置与租约（锁内少读 3 次）；会话更新同时写 `last_seen_at`。释放与 `POST /grants/:id/revoke`（新增可选 `reason`，同一白名单）的失败原因先取组锁再结束租约并记健康事件。
  - `lease.go`：快照中服务器被撤下的 `connection_reassigned`（旧 Core 为 `profile_changed`）优先于此时的 `client_config_unavailable`/`profile_changed`；结束本设备旧租约与订阅变化失效租约合并为一条语句；`latestLease` 以 active 优先、再按获取/结束时间排序（冻结时钟下两条同时刻租约原先按 UUID 随机取）。`model/presence.go` 新增结束原因 `group_unavailable`。
  - `node_watch.go`：`/client/nodes` 带本会话 v3 租约的 `connection` 摘要（ID、组 ID、状态，无到期时间）并计入修订号；旧 Core 会话目录与修订号不变。`agent_api.go`：Agent 判定改按服务器所属组的可见性。
  - `shared/contracts/native-v3.json`（及后端副本、`shared-sources.json`）：新增 `catalog_connection`、`grant_revoke` 两例与说明。`docs/native-client-protocol.md`、`backend/DEPLOYMENT.md` 新章节。测试夹具中直接写入的无组服务器改用 `adoptServer` 建同 ID 组（与迁移回填一致）。
- **测试结果**

| 编号 | 项目 | 命令 | 结果 |
| --- | --- | --- | --- |
| R3-01 | 选择规则（本设备、固定、负载率非人数、冷却仅兜底、全满）；同负载时注入随机源每台都可被选；生产随机源 1000 次分配 5 台各在 140–260 次 | `TestPickServerRules` | PASS |
| R3-02 | 均衡：5 台各容量 5，25 个新用户依次接入每台恰好 5，第 26 个 `group_full`；YAML 代理名为组名；目录同组只出现一次；容量 2/4/6 在第 6 次接入后为 1/2/3、最终 2/4/6；注入随机源 3 种取值分别落到 3 台 | `TestAllocatorBalancesPostgres` | PASS |
| R3-03 | 固定分配：首次 s3 → 释放、首选被压成最忙、登出再登录、刷新目录后仍回 s3；s3 满载时新设备去别处、首选仍为 s3；s3 停用后在线设备收到 `connection_reassigned`，重新接入分到他处、首选更新 | `TestStickyAssignmentPostgres` | PASS |
| R3-04 | 容量并发：总容量 25 的组同时 200 个不同用户接入，恰好 25 个 201、175 个 `group_full`，库中无超额服务器；同时另一组 30 个接入全部成功；同时 20 次管理员保存全部 200；另一组 40 台设备反复接入/释放时管理员反复停用/启用其中一台，全部成功、无死锁，最终停用的服务器上无租约 | `CAPACITY_RUNS=20 go test -race -run TestGroupCapacityConcurrencyPostgres` | PASS（连续 20 次零超额，-race，80 秒） |
| R3-05 | 撤下与重新分配：停用服务器 → 租约立即 `node_unavailable`、v3 会话 `connection_reassigned`、目录修订号变化且 `connection.state=ended`、重接入避开；删除旧 Core 所在服务器 → `/traffic` 快照 `profile_changed`、固定分配随外键删除、再次 `/config` 分到剩余服务器；停用整组 → 全部租约结束、目录消失、快照 `client_config_unavailable`（不提示重连）、接入 `group_unavailable` | `TestServerWithdrawalPostgres` | PASS |
| R3-06 | node_reported：Agent 离线的服务器从不分配，全部离线 `agent_unavailable`；Agent 失联后 grant 不再延长，`/session` 不再续期其租约，对账后租约以 `expired` 结束；`/grants/:id/revoke` 原因白名单（非法 400 `invalid_release`）与 `tunnel_failed` 记健康事件 | `TestGrantAllocationSkipsOfflineAgentsPostgres` | PASS |
| R3-07 | 软冷却：同一用户报告 4 次不触发；第 3 个不同用户触发（冷却至 +300 秒）；冷却中只在健康服务器满载时被使用；301 秒后自动恢复；管理员清除同时删除健康事件并记审计 | `TestSoftCooldownPostgres` | PASS |
| R3-08 | 版本：改容量/备注不改服务器版本、任何会话原因与目录修订号；冷却不改；改某台 YAML 只让该服务器的会话 `profile_changed` 且目录修订号不变；改组倍率让全组 `profile_changed`、组版本 +1、服务器旧列双写且服务器版本不变，重新接入按组倍率计费 | `TestProfileVersionRulesPostgres` | PASS |
| R3-09 | 管理 API：同范围重名（含“所有套餐”）拒绝、不相交套餐允许、保留名与未知套餐拒绝；组/空组删除的乐观版本冲突；同名服务器导入同组；分组请求体校验与未知组 404；旧请求体不能编辑受管组服务器；在线数聚合与逐台统计一致、容量合计；调低容量不踢人并显示 `over_capacity`；非空组不可删；审计条数精确 | `TestAdminGroupAPIPostgres` | PASS |
| R3-10 | 契约：native-v3 新增两例严格解码，`/client/nodes` 带连接摘要与 `grant_revoke` 响应的字段形状与夹具一致；共享快照一致 | `TestV3FixtureFitsLiveTypes`、上述形状断言、`sync-backend-shared.py --check` | PASS |
| R3-11 | 全量后端测试（私有 PG、`-race`），含真实 Core、Node Agent 与 billtest 端到端；既有 grant、Agent、目录、管理节点、第 1/2 轮测试按新语义（夹具建组）全部通过 | `python3 scripts/test-native.py` | PASS（322 PASS、0 FAIL；9 SKIP = 7 个按环境变量启用的夹具装配 + 2 个按需运行的耗时测试） |
| R3-12 | 500 并发接入同一组（5 台各容量 200，本机私有 PG，连接池 80，进程内 httptest） | `ADMISSION_TIMING=500 go test -run TestAdmissionTimingPostgres` | **未达标**：p95 2.23–2.32 s（目标 200 ms），p50 0.80–0.83 s，吞吐约 200 次/秒/组；同一测试串行时单次 p50 3.0 ms、p95 7.8 ms。分析见下 |
| R3-13 | 第 1 轮真实二进制迁移/回滚演练在本轮代码上（目录、配置、网站目录、管理列表与旧版逐字节一致） | `python3 scripts/test-node-groups-migration.py` | PASS（7 项） |
| R3-14 | `go vet`、`gofmt`、仅后端目录独立构建 | `go vet ./...`、`gofmt -l`、`scripts/test-backend-standalone.py` | PASS（107 个文件，tests/vet、Linux amd64/arm64） |
| R3-15 | `/traffic` 耗时对比第 2 轮（同一私有 PG、1000 次上报） | `TRAFFIC_TIMING_REPORTS=1000 go test -run TestTrafficTimingBaseline` | PASS：与 c6abd5a 交替各 3 次，p50 446 → 475 µs（+6%），p95 719 → 776 µs（+8%）；快照多一次组联表，属预期 |
| R3-16 | 节点目录真实浏览器验收（管理端旧页面仍走旧请求体） | `scripts/test-node-catalog.py --skip-build`（CI） | 以 PR 的 CI 结果为准；本轮无前端改动，本机未运行 |
| R3-17 | 网站节点组管理界面、Core 适配（重新分配自动重连、`avoid_node_id` 重试、释放原因） | — | NOT RUN（第 4、5 轮） |

- **关键数据**：200 并发抢 25 个名额，20 次运行均恰好 25 成功、0 超额；5×5 组依次 25 个用户每台恰好 5；1000 次同负载分配 5 台分布在 140–260 之内；500 并发同组 p95 约 2.3 s、吞吐约 200 次/秒。
- **CI 首次运行（ed0101d）**：`Backend / PostgreSQL / race` 在 `internal/api` 达到 `go test` 默认 10 分钟上限而失败（无断言失败）。比对 main 的同一任务：全部旧测试统一变慢 1.5–2 倍（合计 383 → 575 秒），本轮新测试只占 43 秒；原因是每个测试环境留下一个 schema，AutoMigrate 的 information_schema 查询随 schema 数增长，而 `allocator_test.go` 按文件序靠前且新建约 10 个环境。修复：`newIsolatedNativeDatabase` 在测试结束时删除自己的 schema（`NATIVE_TEST_KEEP_SCHEMA=1` 可保留），本机 `internal/api` 的 -race 全包由 215 秒降到 121 秒，结束后无残留 schema。
- **R3-12 瓶颈分析与取舍**：分配按规划 6.3 在节点组行锁内串行，同组接入必然排队；单次接入在锁内约 9 次数据库往返（组锁、候选+负载+首选合并查询、Agent 表、可见目录与选中服务器密文、首选写入、结束旧租约、统计其他设备、写租约、会话更新、提交），每条语句库内耗时均 <0.1 ms，时间主要是往返和高并发下的调度等待（串行 3 ms/次，并发时锁内约 5 ms/次）。500 个同时到达的请求在 200 ms 内全部完成需要每次持锁 <0.4 ms，在“组行锁串行化”的设计下不可达；本轮已把锁内语句从约 14 次减到约 9 次（Agent 表只读一次、候选与负载合并、响应快照复用、两条结束租约合并、`last_seen_at` 并入会话更新），提升约 6%。取舍：保留组锁换取“统计负载与写租约之间不可插入”的精确容量与严格的负载均衡；实际场景中设备连接分散到达，同一组瞬时 500 并发相当于全组用户同秒连接，按 200 次/秒约 2.5 秒排空，不丢请求、不超额。若以后需要更高单组吞吐，可改为“无锁读负载 → 只锁选中服务器行 → 锁内复核该服务器负载，满则换下一台”，同组不同服务器可并行（负载均衡变为近似，容量仍精确），留待第 7 轮压测结果决定。
- **设计取舍（实施中确定）**：管理端 `/admin/nodes` 同时接受分组与旧请求体，避免第 4 轮前端改造前现有管理页与发布演练失效；旧请求体只能操作“一组一台”。无组服务器（迁移后由旧二进制写入）不再按 D3 视为自身组，客户端看不到，直到下一次 `-migrate` 回填（doctor 的 `ungrouped_servers` 会提示）。整组停用/删除使用新的结束原因 `group_unavailable`，以区别于“换服务器重连”。`connection` 摘要只给 v3 租约，旧 Core 的目录修订号不受在线状态影响。发布时目录标签可能变化一次（见 DEPLOYMENT.md 本轮第 1 条）。
- **遗留与归属**：组的套餐或线路类型修改使部分在线用户不再可见时，其租约不立即结束（下一次快照为 `client_config_unavailable`，`/traffic` 仍会续期至断开），第 5 轮 Core 处理该原因后再评估是否在保存组时结束；网站节点组管理与 `/nodes` 页（第 4 轮）；Core 的重新分配自动重连、`avoid_node_id` 重试与释放原因（第 5 轮）；单组高并发吞吐（见上，第 7 轮压测决定）；旧请求体在第 4 轮前端改造后移除。

### 第 4 轮：网站仪表盘、柱状图与节点组管理页（2026-10-21）

- **分支/PR**：`worktree-nodes-round4-website`，PR 见 GitHub（StevenWinsir/vpn）。基线 6c523eb（#53 合并后）。本轮无新表、新列、新后端配置项，无需迁移；新增前端运行时配置 `DASHBOARD_REFRESH_MS`。
- **交付文件**
  - `frontend/src/lib/dashboard.ts`（新）：`dashboardRefreshMs`（5000–120000，缺省/非法回落 15000，读取方式同 `NODE_LIST_REFRESH_MS`）；`formatBytes`（1 GiB = 1,073,741,824 字节，自动 MB/GB）；`intervalLabel`（UTC 区间 → 浏览器本地时区“09:05–12:05”，最右一根“至现在”）；`chartPeak`/`historyEmpty`；以服务端时间计算的“xx 秒前/已连接 xx 分钟”。`use-polling.ts`（新）：与 `/nodes` 相同的轮询规则（可见时按间隔、聚焦/回到前台/网络恢复立即、并发合并、卸载取消）。
  - `frontend/src/lib/types.ts`：`Dashboard` 增加 `online_devices`/`device_limit`；`DeviceStatus`/`OnlineDevice`/`SignedInDevice`/`UsageHistory`/`UsageInterval` 显式白名单类型（无服务器/会话/租约 ID、地址、令牌）。
  - 仪表盘：`dashboard-view.tsx` 三个接口（`/me/dashboard`、`/me/devices`、`/me/usage-history`）独立请求、互不影响，按 `DASHBOARD_REFRESH_MS` 刷新；去掉写死的“尚未接入代理服务”“开发环境说明”和“设计值”倍率，按真实计量状态显示。`devices-card.tsx`（新）：“在线设备 x / y”徽章（满额橙色）、在线列表（平台图标、版本、设备短标识、线路组名、已连接时长、最近心跳、本次上下行）、“已登录未连接（不占名额）”次级列表、有效上限与“约 90 秒让出名额”说明、专线权限。`usage-chart.tsx`（新）：8 根 3 小时上传/下载堆叠柱（纯 CSS，无图表依赖），每根柱为可聚焦按钮且无障碍名称含上传/下载/合计/计费，悬停或聚焦显示明细，“以表格查看”折叠表，全 0 空状态；`globals.css` 新样式。
  - `/admin/nodes`：`admin-nodes-view.tsx` 重写为“套餐筛选 → 节点组卡片 → 服务器表格”。筛选为全部/不限套餐/各套餐（某套餐 = 该套餐用户可见的组，含不限套餐的组），在某套餐下新建组默认限定该套餐；组卡片显示名称、地区、线路、倍率、可用套餐、启用、计量方式、“在线 x / 容量 y”负载条、服务器数、混合计量警告；操作：编辑组、导入服务器 YAML（默认容量 5）、停用/启用、删除（仅空组）。服务器表：备注、代理名、协议、启用、健康（正常/冷却至 hh:mm/Agent 离线/配置不可读）、在线/上限与“超出”、版本；操作：编辑（YAML、容量、备注、启用、移组）、清除冷却、删除（确认框写明受影响在线设备数）。每 5 秒刷新；管理写操作期间丢弃旧轮询结果；存在迁移得到的容量 1000 时提示改为真实值；文案说明容量在实际连接时生效、调低不踢人、停用/删除服务器自动改连同组其他服务器、同组统一显示组名。`admin-agents-panel.tsx` 下拉框改为“组名 / 服务器备注”。页面只使用分组请求体。
  - 用户 `/nodes`：`nodes-view.tsx` 文案改为线路（节点组），说明连接时在组内自动分配、满载时拒绝；字段、过滤和请求不变（后端第 3 轮已输出节点组）。
  - **后端兼容修正（本轮浏览器验收发现）**：`GET /client/config` 与就绪的 `GET /client/grants/:id` 响应中的 `node_id` 原为所分配的服务器 ID。已发布的旧 Core（legacy-core `ConfigForNode`/`ValidCatalog`）与当前 Core（`actions.go`、`grant.go`）都要求它等于所选目录项（组 ID）；第 3 轮的测试只覆盖“组 ID = 服务器 ID”的迁移组，因此未暴露。网站新建的组 ID 都不等于服务器 ID，旧逻辑下现有客户端一律报 `invalid_server_response`/`config_rejected`。现改为回显组 ID（`native.go`；`grants.go` 的 `grantProfile` 同时返回组 ID），服务器仍不透明（会话行记录绑定）；`/connections` 的 `node_id` 仍为服务器 ID（v3，供第 5 轮的 `avoid_node_id` 使用）。`docs/native-client-protocol.md` 同步更正。
  - 测试：`frontend/scripts/dashboard.test.mjs`（新，`npm run test:dashboard`，已加入 CI）；`frontend/tests/dashboard-presence.spec.ts`（新）、`frontend/tests/layout.ts`（新，列出溢出视口的元素）；`admin-nodes.spec.ts` 改为节点组流程（最终仍留下 Mihomo 验收需要的 `Catalog-Half-Edited` 0.5× 与 `Catalog-Premium` 1× 两条线路）；`playwright.catalog.config.ts` 纳入新规格；`catalog_acceptance_test.go` 夹具新增“轻量计划”用户 `lite` 与 `/fixture/usage-history`（控制令牌保护，写入跨 26 小时的固定历史）；`line_echo_test.go`（新）；`allocator_test.go` 的 `TestServerWithdrawalPostgres` 改为断言 `/config` 回显组 ID、服务器取自会话绑定。`frontend/.env.example`、`docs/user-node-list.md` 补充说明。
- **测试结果**

| 编号 | 项目 | 命令 | 结果 |
| --- | --- | --- | --- |
| R4-01 | 单元：刷新间隔边界（4999/120001/小数/NaN 回落 15000）、单位（0、<0.01 MB、MB/GB 边界、计费折算）、时区换算（上海/UTC/加尔各答半小时/洛杉矶/跨午夜、“至现在”）、空数据与峰值、异常响应形状被拒绝、服务端时间差文案 | `npm --prefix frontend run test:dashboard` | PASS（6 项）；`test:nodes` 3 项、`test:downloads` 6 项、`test:api-proxy` 15 项通过 |
| R4-02 | 真实浏览器：管理员在“轻量计划”筛选下新建 JP1（默认带入该套餐），导入 5 台同名（代理名 JP1）服务器、各容量 5；后台显示 5 台、“在线 0 / 容量 25” | `scripts/test-node-catalog.py --skip-build`（`dashboard-presence.spec.ts`） | PASS |
| R4-03 | 三台模拟设备经原生 API 登录（只登录不占名额）；两台接入 201 且第二台按固定分配回到同一服务器，第三台 403 `device_limit`；仪表盘在一次刷新内显示“在线设备 2 / 2”、两条在线记录（线路 JP1）与“已登录未连接”；后台“在线 2 / 容量 25”；`/me/devices` 响应不含服务器 ID 与连接 ID | 同上 | PASS |
| R4-04 | 释放一台：仪表盘 1 / 2、后台 1 / 25；在后台编辑并停用剩余设备所在服务器：该行“已停用 0 / 5”、组“在线 0 / 容量 20”，设备 `/session` 原因为 `connection_reassigned`，重新接入分到同组另一台，后台“在线 1 / 容量 20”且新服务器行 1 / 5 | 同上 | PASS |
| R4-05 | 柱状图：写入跨 26 小时的固定历史后，接口 8 个区间、`bucket_seconds=10800`，26 小时前的行不计入合计；页面 8 根柱的无障碍名称与接口数值逐一一致（上海时区）；Tab 键依次聚焦 8 根柱并显示对应明细；“以表格查看”可展开；手机宽度（390）无横向溢出 | 同上 | PASS |
| R4-06 | 安全：仪表盘 DOM、localStorage、sessionStorage 不含 5 台服务器 ID、`proxies`、`socks5` 或节点端口 | 同上 | PASS |
| R4-07 | 管理端回归（新流程）：建组/导入/迟到的服务器详情不覆盖新草稿/改组名（版本 +1）/改容量（服务器版本不变）/“不限套餐”筛选/桌面与手机布局/普通用户 403；VLESS Reality 与 WireGuard 导入停用组、重开不丢嵌套参数、删除 409 重试、删除空组 | 同上（`admin-nodes.spec.ts`） | PASS |
| R4-08 | 用户 `/nodes` 既有规格（真实管理员变更同步、字段白名单、过滤、错误/空/失效、可见性轮询、文本渲染、匿名跳转）；注册、找回密码规格 | 同上 | PASS（浏览器 10/10） |
| R4-09 | 官方旧 Core（legacy-core）经网站新建的组（组 ID ≠ 服务器 ID）真实 Mihomo 流量与倍率账本 | 同上（`TestCatalogBackendMihomoAcceptance`） | 修正前 FAIL（`invalid_server_response`，即上述兼容问题）；修正后 PASS：半价线路 raw 3540 字节计费 1,770,000 单位，合计 raw 7080 字节计费 5,310,000 单位 |
| R4-10 | `/config` 与就绪 grant 回显组 ID、不泄露服务器 ID，不带 `node_id` 重选仍回显组 | `go test -race -run TestSelectedLineEchoed ./internal/api` | PASS |
| R4-11 | 全量后端测试（私有 PG、`-race`） | `python3 scripts/test-native.py` | PASS（324 PASS、0 FAIL、9 SKIP，与第 3 轮相同的按环境变量启用项）；首次运行 `TestServerWithdrawalPostgres` 因断言旧的“返回服务器 ID”行为而失败，已按新语义更新 |
| R4-12 | `tsc`、`eslint`、`next build --webpack` | `npm run typecheck`、`npm run lint`、`next build --webpack` | PASS |
| R4-13 | `go vet`、`gofmt`、共享快照一致 | `go vet ./...`、`gofmt -l`、`sync-backend-shared.py --check` | PASS |
| R4-14 | 第 1 轮迁移/回滚演练、仅后端目录独立构建 | `test-node-groups-migration.py`、`test-backend-standalone.py` | PASS（演练 7 项，目录、配置、网站目录、管理列表与旧版逐字节一致；独立构建 108 个文件，tests/vet、Linux amd64/arm64） |
| R4-15 | Core 接入 `/connections`、重新分配自动重连、四平台 APP | — | NOT RUN（第 5、6 轮） |

- **CI 首次运行（0f1e1ca）**：`Browser / node catalog / legacy Mihomo ledger` 在 `test:api-proxy` 失败（3 个子测试），其余 14 项全绿。原因：该测试的假上游对未知路径返回 200 回显体，新仪表盘把它当作设备列表读取 `online.length` 而整页报错（本地提交前漏跑了 `test:api-proxy`）。修复：仪表盘对 `/me/devices`、`/me/usage-history` 做响应形状校验（`isDeviceStatus`/`isUsageHistory`），异常或失败时只让对应卡片显示错误并清空旧数据，不影响概览（也覆盖回滚到旧后端时没有这两个接口的情况）；假上游补充这两个接口。本地 `test:api-proxy` 15/15、节点目录验收 10/10 + Mihomo 通过后重新推送。
- **截图**：本机 `artifacts/nodes-round4/`（`admin-groups-desktop.png`、`admin-desktop.png`、`admin-mobile.png`、`dashboard-online.png`、`dashboard-history-desktop.png`、`dashboard-mobile.png`、`nodes-*.png`、`ordinary-denied.png`）；`artifacts/` 不入库，CI 每次重新生成（CI 只上传 summary，不上传截图）。
- **设计取舍（实施中确定）**：柱状图用 CSS 网格而非 SVG，8 个两行时间标签在 390 宽度下仍可读；每根柱是按钮而不是 `role=img`，键盘可达且读屏读出数值；非零小值至少 3 像素高。“最近心跳/已连接时长”用接口的 `server_time` 计算，浏览器时钟偏差不影响。管理端“某套餐”筛选包含不限套餐的组，因为这正是该套餐用户能看到的线路。服务器表按备注排序。仪表盘不等三个接口全部成功：设备或历史失败时各自卡片显示错误，概览照常。
- **遗留与归属**：旧的按服务器请求体（`/admin/nodes` 不带 `group_id`）本轮前端已不再使用，但后端测试夹具、用户节点列表规格（`user-nodes.spec.ts`）和第 1 轮迁移/回滚演练仍依赖它（回滚目标旧二进制只认它），移除留到第 7 轮发布演练时一并处理；grant 路径若要 `avoid_node_id` 需另加服务器 ID 字段（第 5 轮）；Core 适配与四平台（第 5、6 轮）。
