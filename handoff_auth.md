# AsterLink VPN：注册与密码找回技术规划交接书

> 文件：handoff_auth.md  
> 编写日期：2026-10-20  
> 代码基线：项目 /Users/stevenlee/Desktop/vpn，main，HEAD 67e7b37f935e654e4378c14a32bc888ccff2065f；以本次读取到的工作区核心源码为准。  
> 文档性质：实施规划与验收约定，不代表相关功能已经开发或测试通过。全文不包含代码示例。

## 1. 目标、范围与交付组织

本次完善邮箱注册、忘记密码及密码重置后的会话处理，采用 **4 轮实施**。网站提供完整注册和找回界面；四个平台 APP 复用网站入口，继续通过既有客户端登录接口登录。后端统一负责邮箱凭证的生成、校验、发送与账户密码更新。

项目继续采用既定的客户端鉴权、计费、流量统计及 API 同步方式；节点定义、客户端配置同步、计量账本和订单套餐沿用现有实现。本规划只处理认证变化与这些链路的必要衔接。

| 轮次 | 本轮形成的可验收结果 | 主要依赖 |
| --- | --- | --- |
| 第 1 轮 | 配置、邮箱策略、数据库迁移、SMTP 发送任务及共享限流可独立验证 | 现有 PostgreSQL、Go API |
| 第 2 轮 | 网站六位邮箱验证码注册完整闭环；原来的无验证注册入口不能绕过 | 第 1 轮 |
| 第 3 轮 | 邮件链接重置密码、全部会话撤销及并发竞态修复形成闭环 | 第 1 轮；复用第 2 轮的页面和错误处理 |
| 第 4 轮 | 四平台入口和会话失效行为验收，真实邮件投递、全链路回归与上线演练 | 前三轮全部通过 |

每轮必须同时交付实现、必要测试和验收记录，发现阻断问题先在本轮修复，再进入下一轮。验证码、找回链接、SMTP 和会话撤销只做一套后端实现，不在四个 APP 内复制邮箱业务。暂不引入短信、社交登录、邮箱更换、双因素认证、管理后台邮件编辑器、Redis 或独立消息队列。

## 2. 已核实的代码基础与必须衔接的位置

### 2.1 后端与公共能力

| 核心位置 | 当前实现事实 | 对本次工作的影响 |
| --- | --- | --- |
| [backend/internal/api/auth.go](backend/internal/api/auth.go) | 注册直接接收姓名、邮箱、密码并创建用户；登录使用 bcrypt；网站使用访问 JWT、刷新令牌及数据库 Session | 在原注册入口强制增加邮箱证明；成功后保留原来的用户响应及 Cookie 行为 |
| [backend/internal/api/server.go](backend/internal/api/server.go) | 公开 auth 路由使用进程内 IP 限流；JSON 拒绝未知字段；Cookie 请求有 Origin 校验；Gin 未信任代理地址 | 新接口必须进入既有约束；邮箱限流需要数据库共享状态；Next.js 代理后的真实 IP 必须另行核实 |
| [backend/internal/api/native.go](backend/internal/api/native.go) | APP 使用独立 NativeSession 与不透明 Bearer token；设备 UUID、平台、节点绑定、租约与计量均关联原生会话 | 只撤销网站 Session 不足以结束 APP 会话；重置必须处理两张会话表 |
| [backend/internal/model/models.go](backend/internal/model/models.go)、[native.go](backend/internal/model/native.go) | User 无邮箱验证时间和密码版本；Session 与 NativeSession 分开；客户端流量报告有会话加序号唯一约束 | 增加最少必要字段与邮件数据；保留用户 ID、账本关系及流量幂等约束 |
| [backend/internal/config/config.go](backend/internal/config/config.go)、[backend/.env.example](backend/.env.example) | 后端从工作目录读取 .env；现有配置中没有 SMTP、注册开关或邮箱白名单 | 必须同时补配置结构、启动校验、示例配置、部署说明和测试环境隔离 |
| [backend/internal/store/store.go](backend/internal/store/store.go) | PostgreSQL、GORM；迁移为事务内 advisory lock 加 AutoMigrate，重复执行应安全 | 沿用现有迁移入口，补索引及幂等验证，不另建一套迁移框架 |
| [backend/internal/operations/readiness.go](backend/internal/operations/readiness.go)、[backend/cmd/doctor/main.go](backend/cmd/doctor/main.go) | doctor 检查固定表清单与业务就绪状态 | 增加新表和配置检查；普通就绪探针不发送邮件 |
| [backend/internal/api/commerce.go](backend/internal/api/commerce.go)、[backend/internal/billing/client.go](backend/internal/billing/client.go) | 套餐和客户端计量使用用户锁及事务；累计字节、序号和历史报告共同保证账务一致性 | 重置复用用户锁顺序，禁止删除或重建用户、订阅、订单和流量记录 |
| [backend/internal/api/node_catalog.go](backend/internal/api/node_catalog.go)、[node_watch.go](backend/internal/api/node_watch.go)、[backend/internal/nodes/config.go](backend/internal/nodes/config.go) | 管理端维护节点，私有配置加密存储；客户端获取目录和配置受会话及套餐检查约束 | 撤销后的旧令牌不能继续拉节点、配置或续租；邮箱功能不改变节点协议 |
| [native/core/internal/managed/manager.go](native/core/internal/managed/manager.go)、[tunnel.go](native/core/internal/managed/tunnel.go) | 共享 Core 持有账户、令牌和计量状态；generation 防止过期异步响应复活；终止会话会停止引擎、清理连接和账户态 | 沿用既有终止路径，避免 UI 独立维护一套“仍已登录”的状态 |
| [native/core/internal/apiclient/messages.go](native/core/internal/apiclient/messages.go) | native_session_expired、invalid_credentials 等稳定错误码会终止会话 | 后端撤销继续返回兼容错误码，不能只新增一个旧客户端不识别的错误码 |

两处并发问题必须随密码重置修复：网站登录在 bcrypt 校验后直接创建会话；原生登录虽然随后锁定用户，但没有重新确认锁定后的密码仍是刚才验证的版本。若旧密码校验与重置交错，单纯“改密码再撤销现有会话”仍可能留下新建的旧密码会话。网站刷新令牌当前先锁 Session，与拟定的用户优先锁顺序也需要统一。

仓库还保留 native/legacy-core、节点 agent、grant 及相关历史测试分支；本次将其作为已有兼容面检查，不把调整这些模式或部署它们作为邮箱认证的前置条件。README 中部分平台成熟度、协议数量描述滞后，本规划以实际源码为依据。

### 2.2 前端与各平台 APP

前端目前为 Next.js 16、React 19 与 Mantine 组件，浏览器固定请求网站同源 /api/v1，只有 Next.js 服务端知道 API_INTERNAL_URL。完整读取页面、组件和测试后，确认没有既存的忘记密码、重置密码或用户资料改密页面。

| 源码位置 | 当前实现与本次落点 |
| --- | --- |
| [frontend/src/components/auth-form.tsx](frontend/src/components/auth-form.tsx)、[login/page.tsx](frontend/src/app/login/page.tsx)、[register/page.tsx](frontend/src/app/register/page.tsx) | 登录和注册复用 AuthForm；注册没有验证码，成功后更新 Provider 并跳转 dashboard。在此扩展验证码；另增 forgot-password、reset-password 页面 |
| [frontend/src/components/providers.tsx](frontend/src/components/providers.tsx)、[require-auth.tsx](frontend/src/components/require-auth.tsx) | 用户态仅在内存；Provider 首次或主动 reload 遇最终 401 才清用户，已有 generation 防迟到覆盖。需要补统一失效通知，不只删除 Cookie |
| [frontend/src/lib/api.ts](frontend/src/lib/api.ts) | 401 会单飞刷新并重试一次；APIError 只留状态码、错误码、文字，丢弃 Retry-After。新匿名 Auth 调用明确禁用自动刷新，并受控保留限流元信息 |
| [frontend/src/app/api/v1/[...path]/route.ts](frontend/src/app/api/v1/[...path]/route.ts)、[frontend/src/lib/server/api-config.ts](frontend/src/lib/server/api-config.ts) | 通用路径校验，无逐接口白名单；已转发多枚 Set-Cookie、Retry-After、Origin，拒绝客户端转发头和上游跳转。保留这些能力，可信来源传递单独设计 |
| [frontend/src/components/site-shell.tsx](frontend/src/components/site-shell.tsx)、[frontend/src/app/page.tsx](frontend/src/app/page.tsx)、[frontend/src/lib/types.ts](frontend/src/lib/types.ts) | 认证布局只识别 login/register，首页和登录页有固定注册链接，Meta 尚无 Auth 字段；需扩展两条认证路由并接入开关 |
| [frontend/src/components/nodes-view.tsx](frontend/src/components/nodes-view.tsx)、[admin-nodes-view.tsx](frontend/src/components/admin-nodes-view.tsx) | 节点列表已有最终 401 清用户的局部处理；总览、订单、管理员编辑等尚未统一。管理员 YAML 编辑器、购买确认框必须随会话失效清理 |

四平台都已实现客户端业务与系统服务集成，并已有独立 WEBSITE_BASE_URL 公共构建参数。复用各平台 ClientEnvironment 与 [shared/contracts/env-v1.json](shared/contracts/env-v1.json)，不新增另一套网站地址推导逻辑。APP 的 .env 参数在构建时写入安装包，修改地址需要重新构建发布；后端新增同名 WEBSITE_BASE_URL 仅用于邮件地址生成，部署时保持环境配对。

| 平台与核心文件 | 当前实现 | 本次必要改动或验收 |
| --- | --- | --- |
| macOS：[SessionController.swift](apps/macos/Sources/AsterLinkShared/SessionController.swift)、[CredentialStore.swift](apps/macos/Sources/AsterLinkShared/CredentialStore.swift)、[Views.swift](apps/macos/Sources/AsterLinkApp/Views.swift) | Keychain 保存自愿记住的密码；XPC Helper 管理 Go Core；已有官网注册入口，失效最多一次静默重登 | 增加找回 URL/入口；补旧快照、异步静默重登与手动登录的操作版本保护；检查真实 worker 的旧会话结束 |
| Android：[SessionController.kt](apps/android/app/src/main/java/com/asterlink/vpn/session/SessionController.kt)、[CredentialStore.kt](apps/android/app/src/main/java/com/asterlink/vpn/security/CredentialStore.kt)、[AsterVpnService.kt](apps/android/app/src/main/java/com/asterlink/vpn/vpn/AsterVpnService.kt) | Keystore 加密密码，DataStore 保存密文；应用级观察 Core；前台 VPN 服务管理系统授权；网站使用 Custom Tabs 或系统浏览器 | 在 AppModel 的页面枚举与 LoginScreen 加找回入口；检查后台停隧道、一次旧密码失败清理和迟到凭据读取竞态 |
| Windows：[SessionController.cs](apps/windows/AsterLink.Shared/Client/SessionController.cs)、[CredentialManagerStore.cs](apps/windows/AsterLink.Platform/CredentialManagerStore.cs)、[CoreHost.cs](apps/windows/AsterLink.Service/CoreHost.cs) | 系统凭据管理器保存密码；WPF 通过常驻服务控制 Core；已有注册 URL，无找回 URL | 增加 URL、AppViewModel 命令与 LoginView 链接；结束旧账户/隧道会话，常驻服务继续可用于新登录 |
| iOS：[SessionController.swift](apps/ios/Packages/AsterLinkKit/Sources/AsterLinkKit/SessionController.swift)、[Platform.swift](apps/ios/AsterLink/Platform.swift)、[PacketTunnelProvider.swift](apps/ios/AsterLinkTunnel/PacketTunnelProvider.swift) | Keychain 保存密码；APP/Extension 通过内存 ticket 交接唯一会话所有者；网站已用 SFSafariViewController | 复用 AppModel.openWebsite 增加找回入口；验证独立 Extension、旧 ticket 的 adopt/takeBack/resume 及重新登录 |

密码项均与服务环境和邮箱关联，设备 UUID 有独立存储。四端的保存密码不等于永久保存 native token；删除失效密码不能一并删除设备身份。平台详细验收见第 9、10 节。

### 2.3 当前用户与密码规则

现有邮箱身份使用前后空白去除、整串转小写后匹配；新功能延续这一规则，避免同一邮箱在登录、注册、找回中形成不同身份。邮箱本地部分大小写和服务商别名的一般标准不能直接覆盖已有数据库身份规则。[邮箱规范参考](https://cheatsheetseries.owasp.org/cheatsheets/Email_Validation_and_Verification_Cheat_Sheet.html)

普通用户现有密码限制为 **10–72 个 UTF-8 字节**，姓名为 2–40 个 Unicode 字符，bcrypt 成本使用既有配置。密码不得被静默裁切、去空白或大小写转换；前端展示字节限制并使用与后端一致的计算方式，不能把字符数当字节数。管理员由 CLI 创建时的 16–72 字节要求保留，管理员经找回设置新密码时同样应用该下限。bcrypt 的 72 字节上限来自库本身。[bcrypt 文档](https://pkg.go.dev/golang.org/x/crypto/bcrypt)

当前 auth-form 的提交逻辑已按 UTF-8 字节检查，但 HTML minLength 和“至少 10 个字符”的文案又施加字符限制，中文密码会出现不一致；第 2 轮修正这一冲突，昵称也按后端 Unicode 字符规则校验。重置的新密码必须不同于当前密码，只在持有有效重置凭证后检查，防止已保存的旧密码仍能成功重新登录；不扩展为复杂密码历史库。

旧账户新增 EmailVerifiedAt 后保持空值，不能批量伪造“已验证”。旧账户照常登录和找回；首次成功通过邮件链接重置时，可以补齐其验证时间。仅发出邮件、收到 SMTP 成功响应或检查到邮箱域名存在，都不构成邮箱验证成功。

## 3. 配置与邮箱政策

### 3.1 .env 约定

在 backend/.env.example 新增 Registration and mail 配置分组，实际部署对应填写 backend/.env。邮件参数只存在于后端运行环境。下表保留需求指定的键名和配置含义，以配置清单代替代码示例。真实凭据不得写入仓库、前端环境变量、客户端安装包、测试日志或此文档。

| 变量 | 目标配置或默认策略 | 校验与用途 |
| --- | --- | --- |
| ALLOW_REGISTRATION | 需求部署值 true；缺省 false | 严格解析布尔值。控制验证码申请和最终注册两步；关闭后仍允许已有用户登录、找回 |
| SMTP_HOST | smtp.qq.com | 纯主机名，不接受带协议或路径的 URL |
| SMTP_PORT | 465 | 整数且在有效端口范围 |
| SMTP_SECURE | true | true 表示建立连接即使用隐式 TLS；false 表示必须协商 STARTTLS，不表示允许明文 |
| SMTP_USER | xxxx@qq.com 为占位符，实际为完整发件邮箱 | 认证用户名，同时作为初版信封发件地址及 From 地址 |
| SMTP_PASS | xxxx 为占位符，实际为邮件服务商授权凭据 | QQ 邮箱使用 SMTP 授权码，不是 QQ 登录密码；不得打印 |
| SMTP_FROM_NAME | AsterLink VPN | 邮件展示名称；通过邮件库编码，拒绝控制字符和头部注入 |
| WEBSITE_BASE_URL | 必填的正式网站绝对地址 | 用于生成找回链接。只接受根 origin，拒绝用户信息、查询参数、fragment 和非根路径；生产只允许 HTTPS，开发 HTTP 仅限 loopback；使用受信任的固定地址，不读取请求 Host 生成链接 |
| AUTH_MAIL_SECRET | 必填的独立高熵密钥，至少 32 字节随机材料，以统一的 Base64 编码保存 | 派生不同用途的验证码摘要、令牌摘要和邮件载荷加密密钥；不得复用 JWT 或节点密钥 |
| REGISTRATION_ALLOWED_EMAIL_DOMAINS | 可覆盖的精确域名列表；缺省采用 3.2 节集合 | 逗号分隔、去空白、转小写、去重；显式配置为空、含通配符或非法域名时拒绝启动 |

SMTP 465 使用隐式 TLS；587 等配置可采用强制 STARTTLS，均校验证书与主机名，最低 TLS 1.2，不允许自动降级。QQ 官方说明也要求使用完整邮箱和授权码。[RFC 8314](https://datatracker.ietf.org/doc/html/rfc8314) · [QQ 官方配置说明](https://help.mail.qq.com/detail/108/1089/)

配置启动规则：

1. 开启注册时，完整 SMTP、网站地址、邮件密钥缺一不可；存在部分 SMTP 参数、非法布尔值或非法 URL 时启动失败，并只报告缺失项或错误类别。
2. 全部 SMTP 参数缺省且关闭注册时，可以运行原有服务，元信息明确表示邮件找回不可用。正式验收本功能时不允许以该状态作为“完成”。
3. SMTP 配置完整但服务商临时不可达，由发送任务重试和告警处理；普通登录、套餐、节点与计量 API 不因此停止。已确认邮件发送能力不可用或队列满时，邮件申请接口统一返回可重试的服务不可用结果，不能因邮箱存在与否返回不同结果。
4. 配置变更随进程重启生效；全部 API 实例使用同一邮件密钥、域名集合和网站地址。轮换邮件密钥会使尚未完成的凭证失效，先停止新申请、清理或作废未完成任务，再统一更新；不影响 JWT 或节点加密密钥。
5. 更新 backend/.env.example、backend/DEPLOYMENT.md 和 doctor；真实 backend/.env 由部署人员填写，不由迁移或测试自动覆盖。Next.js 只接收公开元信息，不接收 SMTP 或邮件密钥。

### 3.2 新注册邮箱的严格允许列表

初版允许以下 **10 个精确域名**，管理员可通过上述环境变量明确替换：qq.com、foxmail.com、163.com、126.com、yeah.net、gmail.com、outlook.com、hotmail.com、icloud.com、yahoo.com。列表采用封闭集合；未列出的企业域名、别名域名及子域名均不自动接受。

后端先规范化输入，再解析单一邮箱地址，最后对域名执行完全相等比较。拒绝显示名称、多地址、换行、控制字符、域名尾点、空域名和混淆性 Unicode 域名；新注册按 ASCII 邮箱处理。不得以字符串后缀包含、通配符、MX 查询、邮箱服务商网页跳转结果或前端判断放行域名。不能把包含 qq.com 的任意主机都视作 QQ 邮箱。

不删除 Gmail 地址中的点，不删除加号扩展，不合并不同拼写的邮箱；是否实际能收信由验证码证明。允许列表限制可注册的邮箱服务商，不作为对邮箱所有者真实身份的承诺。

此规则只应用于 **注册申请验证码和最终创建新账户**，两处都必须执行。已有的非列表邮箱、历史测试账户及管理员继续按原有身份规则登录和找回，不能因白名单收紧而失去账户。创建管理员仍走既有受控 CLI，不开放角色选择或公开管理员注册。

GET /api/v1/meta 返回实际生效的域名集合与开关，前端据此提示用户；前端文案不是授权依据。申请后白名单或注册开关变化，最终注册按当时的后端配置重新判断。

## 4. 数据与模块设计

采用 **三张轻量新表、三个 User 附加字段、一个进程内发送 worker**。持久化、共享限流和任务协调都使用现有 PostgreSQL，不增加外部基础设施。建议将邮箱领域逻辑集中在 backend/internal/authmail，将 SMTP 适配器单独封装；API handler 负责输入、调用服务和映射稳定响应，不能各自复制状态机。

### 4.1 User 与新表

| 数据对象 | 必要内容 | 约束与用途 |
| --- | --- | --- |
| User 附加字段 | EmailVerifiedAt 可空、PasswordChangedAt 可空、AuthVersion 非空且默认 1 | 版本只在密码实际改变时递增；三个字段先用于后端内部，公开 User 响应保持既有字段 |
| EmailAuthChallenge | 随机 ID、用途、规范化邮箱、可空用户 ID、凭证摘要、生成版本、签发/过期/消费时间、失败次数、状态；重置用途另绑定签发时的 AuthVersion | 不保存明文验证码或 token。用途限定注册或密码重置；用户 ID、邮箱、用途与摘要共同参与校验 |
| AuthMailJob | 随机 ID、可空挑战 ID、模板种类、收件邮箱或加密载荷、密文载荷、任务状态、尝试次数、下次尝试时间、领取租约、领取者标识与版本、固定 Message-ID、脱敏失败类别、创建/终结时间 | 挑战和发送任务同事务创建；worker 只能投递仍然有效的任务；不复制保存用户密码 |
| AuthRateBucket | 邮箱或可信来源 IP 的摘要键、用途/动作、时间窗、计数、下次允许发送时间、更新时间 | 通过数据库原子更新实施所有实例共享的发送、试错与冷却限制，不用进程内 map 作为最终限制 |

EmailAuthChallenge 的凭证业务状态与 AuthMailJob 的投递状态分开。挑战从创建起开始计时，可用性由用途、版本、期限、次数、消费/作废状态共同决定；不引入“SMTP 成功后才激活凭证”的第二套提交协议。邮件有可能晚到，用户只使用最新一次申请的凭证。

验证码空间只有六位数字，不能只存可离线枚举的普通 SHA-256；使用独立密钥参与的 HMAC，绑定用途、邮箱、挑战 ID 和随机验证码。重置 token 同样做用途绑定的摘要校验，摘要有唯一查询索引；可先按用途加 token 的 HMAC 定位挑战，再检查行内绑定的用户、邮箱和 AuthVersion，避免依赖遍历用户才能验证。摘要采用恒定时间比较。AUTH_MAIL_SECRET 派生摘要键与加密键时使用不同用途标识。邮件任务因投递必须暂存秘密，以认证加密保护载荷，并绑定任务 ID 和模板用途；库中不保留明文正文或完整找回 URL。

关键索引包括挑战查询及清理索引、任务待发及租约索引、限流键与时间窗唯一索引。确保同一邮箱同一用途只有一个当前有效版本：在邮箱用途协调锁下作废前代、建立新代，再由唯一约束兜底；不要把随时间变化的“当前时间”写进部分唯一索引条件。到期状态由读取时检查和后台清理共同处理，不能依赖定时任务及时执行才保证安全。

### 4.2 并发与锁的统一约定

没有 User 的注册申请，以数据库中的邮箱协调行串行化；首次创建该行也必须能抗并发。协调行使用跨时间窗稳定的邮箱键，可作为 AuthRateBucket 的固定协调类型存储；不能拿每小时/每天换键的滚动桶代替串行锁，也不随过期计数桶一起删除。注册和找回共用这条邮箱协调记录，才能共同约束冷却与总量。已有账户的密码重置、登录提交和刷新采用 **User → 稳定邮箱协调行（需要时）→ Challenge → Session/NativeSession → MailJob** 的固定顺序。同类多行按稳定主键顺序处理。所有路径不得先锁挑战或会话再反向请求 User 锁。

旧刷新令牌可先在事务外定位候选用户，但进入事务后必须按上述顺序重新检查令牌、会话、撤销状态、到期时间及用户状态。密码 bcrypt 计算和 SMTP 网络连接都不放在持有业务行锁的长事务中。

注册或重置挑战的正确消费与账户写入必须原子提交。错误验证码的次数递增也必须提交：不能通过返回事务错误将刚增加的失败次数一起回滚。并发验收必须验证这一点。

### 4.3 迁移、清理与可观测性

迁移增加字段、默认值、表和必要索引，接入现有 advisory lock 与 AutoMigrate 流程；需要手工建立的约束或索引也纳入同一可重复执行的迁移入口。上线前在含旧数据的副本验证新增索引和字段的锁等待。重复迁移不重置 AuthVersion、不补造历史邮箱验证时间、不触碰计费数据。

建议每天分批清理：过期或终结挑战保留最多 24 小时；终结邮件任务立即擦除密文秘密，脱敏元数据保留 7 天；过期限流桶按窗口清理；安全审计保留 30 天并遵循项目已有日志策略。数据库备份同样可能包含尚未清理的密文，邮件密钥不能随数据库备份混放。表间外键不允许认证清理级联删除账户或财务记录。

成功改密的事务审计复用现有 AdminAudit 表，使用独立 Auth 动作名称，ActorID 和 TargetID 记录已验证的用户 ID，Version 记录新的 AuthVersion；无需增加第四张表，也不赋予普通用户管理员权限。任务故障与发送尝试使用脱敏运行日志。

日志仅包含请求 ID、任务 ID、用途、状态、次数及脱敏邮箱/来源；禁止输出密码、验证码、token、完整重置链接、邮件正文、Cookie、Authorization 及 SMTP 认证交换。指标至少覆盖发送接受量、失败类别、队列等待时间、重试次数、验证码错误/锁定数和重置成功数，不以原始邮箱作为高基数标签。

## 5. API 契约

所有网站接口继续通过 Next.js 的既有 API 代理访问 Go API；Go API 为最终权限与输入校验边界。使用现有 JSON 严格解析、请求体限制、超时、Origin/Cookie 规则以及 no-store 响应。只允许预定字段，不能为添加验证码而放松未知字段检查。

| 方法与路径 | 输入或用途 | 成功响应约定 |
| --- | --- | --- |
| GET /api/v1/meta | 公开能力与配置 | 保留现有字段，增加 allow_registration、registration_email_domains、password_reset_enabled、验证码有效期与重发间隔；不泄露内部配置 |
| POST /api/v1/auth/register-code | email | 202；通用提示、随机 verification_id、expires_at、next_send_at；不创建 User，不返回验证码 |
| POST /api/v1/auth/register | 保留 name、email、password、remember，增加 verification_id、code | 正确消费验证码后沿用 201、现有 user 和 vpn_access/vpn_refresh Cookie |
| POST /api/v1/auth/forgot-password | email | 202；通用提示与 next_send_at；不返回 token、用户 ID、账户存在标记或任务投递详情 |
| POST /api/v1/auth/reset-password/validate | token | 200；表示当前可使用，并返回最小/最大密码字节数等填写规则；只读，不消费 |
| POST /api/v1/auth/reset-password | token、password、password_confirmation | 200；完成改密与全部会话撤销，清理发起浏览器的原有认证 Cookie；不创建新登录会话 |
| 原有登录、刷新、退出及 /api/v1/client/* | 保留现有外部契约 | 内部补密码版本竞态与撤销处理，APP 不需要邮箱注册专用 Bearer 令牌 |

code 必须为字符串形式的 **六个 ASCII 数字**，包括前导零；不得按整数接收或隐式转换。verification_id 是高熵不透明随机标识，绑定当前邮箱及用途，不能替代验证码。所有时间使用服务端 UTC；前端倒计时仅用于体验。

邮箱申请接口对已注册/未注册邮箱返回同形态结果。注册申请中已经存在或不可注册的账户可以创建无投递的等形态短期请求记录，仍返回随机标识与相同时间字段，避免响应格式或 ID 特征泄露账户存在性；新账户路径才排入验证码邮件。忘记密码对不存在或不可用账户不发送重置邮件，但响应语义相同。两者都先执行相同输入检查和共享限流，避免 SMTP 是否执行影响同步响应时间。

“202”表示请求已接受，不等于已经到达收件箱。格式不合法、注册域名不在列表、注册关闭、限流和全局邮件服务不可用可以明确提示，它们不得成为区分特定账户存在性的分支。重置页面只在持有有效 token 后获得必要的填写规则。

| 类别 | HTTP 与稳定错误约定 | 前端处理 |
| --- | --- | --- |
| 输入或密码格式不合法 | 400，注册沿用 invalid_registration，新接口增加明确字段错误码 | 定位字段，不清空不相关内容 |
| 注册关闭 | 403，registration_disabled | 隐藏或禁用注册入口，保留登录和找回 |
| 注册域名不允许 | 400，email_domain_not_allowed | 显示后端实际允许列表 |
| 验证码不匹配、过期、已使用、次数耗尽 | 400，verification_invalid / verification_expired / verification_used / verification_locked | 提示重新输入或重新获取；不显示正确码，不把剩余尝试作为授权依据 |
| 邮箱并发注册冲突 | 409，email_exists | 仅在有效邮箱证明已核对之后反馈，引导登录或找回 |
| 重置凭证不可用 | 400，reset_token_invalid | 将过期、已使用、作废、用途不符等归为统一提示，提供重新申请入口 |
| 限流 | 429，rate_limited，Retry-After | 使用服务端时间恢复按钮，不循环自动重试 |
| 邮件能力不可用或数据库暂不可用 | 503，稳定服务不可用码 | 显示稍后重试；内部原因进入脱敏日志 |
| 已撤销网站会话 | 沿用当前 401 契约 | 保留原有刷新与退出协调机制 |
| 已撤销 APP 会话 | 401，native_session_expired | 进入现有 Core 终止会话路径 |

错误类别最终与仓库已有响应结构统一，不能同时引入第二种异常包裹结构。API 文档明确哪些字段会新增，Next.js 测试覆盖状态码、多个 Set-Cookie、Retry-After 与新增响应字段的转发。浏览器 APIError 同时扩展受控的 retryAfter/重试时间信息，否则代理虽已转发，表单仍无法读取。发码、forgot、validate、reset 明确关闭 API helper 的自动 Cookie refresh；只有受保护业务请求走既有单次刷新协调。

## 6. 邮件发送与防滥用

### 6.1 SMTP 实现边界

使用 Go SMTP 邮件库封装可替换的 Mailer 接口，建议选用 github.com/wneessen/go-mail，并在第 1 轮确认与项目 Go 工具链兼容的固定版本、许可证及依赖范围。其隐式 TLS、强制 STARTTLS、MIME 和带上下文的发送能力可以减少自建邮件协议代码。[官方库文档](https://pkg.go.dev/github.com/wneessen/go-mail)

首期提供三种固定服务端模板：注册验证码、密码重置链接、密码已变更通知。邮件同时含纯文本与 HTML，使用 UTF-8，用户输入必须转义。注册邮件展示六位数字、用途、有效时间以及只使用最新邮件的说明；找回邮件展示固定网站域名、按钮、可复制链接和有效时间；改密通知不含密码或令牌。模板包含品牌名称，不添加跟踪像素、外部脚本或第三方统计。

Go 请求处理仅完成数据库提交后返回。进程内 worker 使用短事务领取任务并设置租约，释放数据库锁后执行 SMTP，完成后再记录结果。同一有效租约只由一个 worker 持有；进程退出或崩溃后，租约到期允许其他实例恢复。建议领取租约为 45 秒，覆盖 SMTP 的 20 秒硬超时和数据库收尾；网络调用必须真正受上下文/连接截止时间约束。每次领取递增版本或生成 owner token，写回时同时匹配领取身份和任务可写状态，旧 worker 不得将已取消或重领任务恢复为 sent/retry。worker 持有 MailJob 锁时不再反向锁 Challenge/User，有效性预检与业务取消通过既定锁顺序和条件更新协调。

初版每实例最多 2 个并发发送，单次完整投递最多 20 秒，最多 3 次尝试，短退避建议为 15 秒、60 秒并附少量抖动；连接数和任务领取量均有上限。重试必须使用同一挑战、同一秘密及 Message-ID，不生成新验证码、延长有效期或重复扣申请额度。SMTP 明确的永久拒绝直接终结，临时失败重试；临近挑战过期或已作废的任务取消，不投递已过期凭证。

接受新一次申请时，同事务作废前次同邮箱同用途挑战及尚未发送的任务；这项规则不等待 SMTP 成功。已经在 SMTP 网络调用中的旧邮件可能无法撤回，但其中凭证仍因版本失效而不能使用。数据库提交失败不发信。SMTP 已接受而确认写库前进程崩溃，可能导致同一封邮件重投，允许这种有限重复；凭证消费必须仍然一次成功。

SMTP “接受”不能保证收件箱到达。队列正常但投递失败时，面向用户的响应不暴露账户存在性；运维通过脱敏任务状态定位失败。注册或找回页面提示检查垃圾邮件、等待后重试，不能以无限自动重发补偿失败。实际发件身份涉及服务商对发件人、每日额度及认证的要求，按真实账户配置验收，不凭配置项存在判定邮件可用。

### 6.2 共享限流与验证码试错

既有 IP 限流可以保留为入口保护，邮件业务另使用 PostgreSQL 原子计数。第一版统一采用以下默认值，并集中在后端配置/常量与测试中；前端从 meta 或响应读取展示值，不能硬编码第二套安全规则。

| 维度 | 初版限制 | 说明 |
| --- | --- | --- |
| 同一规范化邮箱发送冷却 | 60 秒 | 注册与找回共享冷却，避免交替接口绕过 |
| 同一规范化邮箱申请量 | 每小时 5 次、每天 10 次 | 注册与找回共享总量；存在与否都记账 |
| 同一可信来源 IP 申请量 | 每小时 20 次、每天 100 次 | 仅针对邮件申请，不与正常客户端续租、流量报告混用 |
| 全局邮件申请量 | 每分钟 60 次 | 可按实际供应商额度收紧；先限流后生成任务 |
| 全局待投递任务数 | 1,000 条暂停接收阈值 | 准入检查的并发余量有界；超阈值统一返回 503，避免数据库队列无界增长 |
| 注册单挑战错误次数 | 5 次 | 错误递增后提交；第 5 次后锁定该挑战 |
| 同邮箱注册校验错误总量 | 每 30 分钟最多 5 次 | 重发、换 verification_id、重启或切换 API 实例都不能清零 |
| 验证/重置接口来源保护 | 沿用并独立配置现有 auth IP 限流 | 对短码额外应用邮箱错误预算；长随机 token 不依赖六位码规则 |

全局准入计数先在独立短事务完成并释放，再进入用户/邮箱业务事务，避免与 User 优先的锁顺序交叉。失败请求可保守计入申请额度，不需要跨事务返还额度的复杂机制。单挑战锁定返回 verification_locked；邮箱总试错预算用尽返回带恢复时间的 429。

所有限流状态以服务端时钟计算，使用原子 SQL 更新或数据库锁，不能先读计数再在事务外加一。对不存在邮箱也执行相同限制，避免限流响应成为账户枚举信号。单邮箱额度不可被 IP 轮换绕开，单 IP 与全局额度防止轮换邮箱耗尽发送资源。

验证码尝试预算耗尽仅暂停相关注册证明，不能禁用 User、修改密码或阻断已有账户正常登录。邮箱格式校验失败和全局拒绝也需要廉价入口限流，避免依靠随机无效地址制造无限数据库桶。

### 6.3 Next.js 代理后的来源 IP

当前 Gin 未信任代理，Next.js 服务端转发可能使全部用户共用一个后端可见地址；不能简单将所有 X-Forwarded-For 设为可信。上线时固定入口链：外层反向代理覆盖客户端自行提供的转发头，Next.js 仅从受信任入口取来源地址，Go 只信任明确配置的代理地址或 CIDR，并限制 Next.js/Go 内网端口被直接访问。

先检查仓库代理工具如何构造请求，再决定新增最小的来源头传递与可信代理配置。若生产入口尚不具备可信来源，默认仍使用连接对端地址加邮箱额度，不能信任任意浏览器报头；真实 IP 链路验收通过后才使用客户端 IP 额度。必须同时测试“不同真实用户不会全部共用一份额度”和“伪造转发头不能绕过额度”。

## 7. 六位验证码注册流程

### 7.1 申请与重发

用户在现有注册界面填写邮箱并申请验证码。后端依次执行输入规范化、严格域名白名单、注册开关、统一邮件能力检查、数据库限流；通过后生成密码学安全的六位随机十进制验证码，范围包括 000000–999999，保持六位字符串。生成过程必须均匀且使用安全随机源，不使用时间、普通伪随机数、固定测试码或递增序列。

验证码有效期为 **10 分钟**，从挑战创建提交时起算。只为确实可注册的新邮箱排入邮件任务。接受重发后生成新 verification_id 和新验证码，作废同邮箱旧挑战及未发送任务，重新开始新挑战有效期，但保留邮箱级试错和发送预算。每次重发的界面均使用最新返回的标识。

不在申请验证码阶段预先创建用户、不保存密码或占用付费资源。邮箱白名单、账户存在性、服务可用性与限流检查顺序保持稳定，公开结果不透露是否已有账号。

### 7.2 完成注册

最终提交包含当前邮箱、姓名、密码、remember、verification_id 和 code。后端先做全部廉价字段校验，再计算 bcrypt，最后进入事务重新确认邮箱、开关、当前挑战、用途、摘要、有效期、版本、错误预算与消费状态。

事务中创建唯一邮箱对应的 User，写入 EmailVerifiedAt 和 AuthVersion 初始值，创建原有网站 Session，并标记挑战已消费、取消未发送的相关任务。角色和账户状态由服务器固定为普通活跃用户；请求不能指定 verified、role、status、user_id 或套餐。

同一验证码并发提交只能创建一个账户和一份成功结果。唯一键冲突使用现有 email_exists 语义映射，但只能在证明有效后返回。数据库错误回滚用户、会话与消费状态；邮件发送状态不能决定账户创建成功。

成功后保持现有 201 与 Cookie 登录体验。若提交已经成功但响应丢失，重试不得再次创建账户；界面引导使用原密码登录，必要时找回，不能自动放宽为无验证码注册。新注册不自动获得付费套餐，套餐、试用与订单按现有入口处理。

### 7.3 网站状态与文案

注册页面沿用当前视觉和表单组件，增加验证码字段、发送/重发按钮、倒计时、允许域名提示和注册关闭状态。用户修改邮箱后立即清空验证码及 verification_id，旧响应不得覆盖新邮箱的状态；请求加本地序号或取消控制，避免慢响应串线。刷新页面需要重新申请，不能将验证码、密码或待提交表单写入 localStorage。

按钮状态覆盖未申请、提交中、已接受待收信、冷却、可重发、验证码错误、过期、锁定、成功。发送中防重复点击；冷却后能手动重发。网络错误与验证码错误分开，不丢失姓名和已填写邮箱。对六位验证码支持粘贴、自动填充提示和数字键盘，但后端仍严格验证六个 ASCII 数字。

同步处理 frontend/src/app/page.tsx 的固定注册链接、auth-form 的注册入口和 site-shell 的认证页面集合；新增 forgot-password、reset-password 不进入 RequireAuth，登录页补忘记密码入口。

注册关闭时，首页/登录页入口、注册页直达和 API 都有一致表现；已收到码的用户也不能绕过关闭开关完成注册。元信息失败时注册操作保持禁用或显示重试，不默认开放。

## 8. 邮件链接找回与密码重置

### 8.1 申请找回邮件

“忘记密码”页面仅要求邮箱。后端按既有邮箱身份规范化进行查找，**不套用新注册域名白名单**，不要求登录，也不要求再输入注册用六位验证码。邮箱中的高熵专属链接本身完成持有者验证。

存在且允许找回的 active 账户生成 32 字节安全随机 token，经 URL 安全编码后使用；库内只保存用途绑定摘要、用户 ID、邮箱、当前 AuthVersion 与期限。有效期 **30 分钟**，一次性消费。不存在或禁用账户不发送可用重置链接，但返回相同的 202 提示；请求本身不改变密码、账户状态、会话和套餐。

重发采用同样的最新申请有效规则。不得通过一封新邮件恢复禁用账户，不设置临时密码，不发送现有密码，也不在用户真正提交新密码前自动登出所有设备。统一提示、随机一次性凭证、有效期及成功后撤销会话遵循 OWASP 找回流程原则。[OWASP 找回密码指引](https://cheatsheetseries.owasp.org/cheatsheets/Forgot_Password_Cheat_Sheet.html)

### 8.2 链接与页面保护

链接固定指向 WEBSITE_BASE_URL 对应网站的 /reset-password 页面。token 放在 URL fragment，由客户端页面读取到内存后立即通过历史替换移除地址栏中的秘密，再用 POST 请求验证和提交；不把 token 放入查询参数、服务端页面属性、Cookie 或持久存储。

页面实现必须考虑 React 重渲染和开发环境 Strict Mode，不因重复初始化把已经保存到内存的 token 清空。刷新页面后需重新打开原邮件链接，页面明确说明。重置页不加载统计脚本、跟踪像素或第三方资源，设置 no-store、no-referrer 与禁止索引；根布局和代理访问日志也要确认没有捕获 token。

邮件按钮和复制链接仅包含预配置网站地址，不接受任意 redirect_url，不以 Host、Origin、Referer 或用户提交地址构造域名。生产全程 HTTPS，页面资源同源。浏览器打开链接、邮件扫描器访问、GET/HEAD、页面预取，以及只读 validate 请求均不消耗 token；只有提交合法新密码的 POST 可以消费。

### 8.3 密码提交与事务边界

后端先解析 token、定位并初步确认有效挑战和用户，校验新密码与确认密码一致、字节规则以及与当前密码不同；相同密码返回明确字段错误但不消费凭证。bcrypt 比较与新哈希计算在长事务外完成，并记录对应 AuthVersion。正式提交时按统一锁顺序锁定 User 和对应挑战，再检查账户状态、邮箱匹配、用途、摘要、期限、最新版本、AuthVersion 以及未消费状态。

在同一数据库事务内完成：

1. 更新原用户的 PasswordHash、PasswordChangedAt，AuthVersion 递增；若 EmailVerifiedAt 为空则补齐当前验证时间。
2. 将当前凭证标记已消费，作废该用户其他未完成重置挑战，取消它们尚未投递的任务。
3. 撤销该用户所有未撤销的网站 Session 和 NativeSession，不只撤销发起请求的浏览器或单一 device_uuid。
4. 写入脱敏安全审计，排入“密码已变更”通知任务。通知任务不携带新密码，SMTP 投递失败不回滚已经提交的密码修改。

这些数据库动作任一步失败，事务整体回滚并返回可重试错误；不得出现密码已改、会话仍有效，或 token 已消费、密码未改的中间结果。密码变更通知可使用不关联 challenge 的任务类型，避免被重置挑战清理误删。

提交成功后不自动登录，也不把新密码回填到客户端。清理当前浏览器旧 Cookie，并显式清空 Provider 用户态，显示成功结果并提供正常登录入口。重置表单的可用性只依赖邮件凭证；全站 Provider 自行调用 auth/me 得到的未登录或网络错误，不能阻断这张公开表单。第二次使用同一 token、并发两个重置请求或使用旧邮件，均不能再次修改密码。

### 8.4 登录、刷新与密码重置的竞争

网站和原生登录都在读取用户时记住 AuthVersion，完成 bcrypt 校验后，再在创建会话的事务中锁 User，重新检查版本与状态。若期间发生改密，拒绝使用这次旧验证结果创建会话，让用户重新登录；不要在锁外重试旧密码创建新会话。

网站刷新改为先定位候选用户，再锁 User、锁 Session，重新检查 refresh hash、撤销和期限后旋转刷新令牌。重置先完成时，刷新必须失败；刷新先完成时，新会话仍会被随后的重置撤销。所有后续修改密码的入口都必须共用同一密码变更服务和锁顺序。

原生已有 nativeTransaction 的 User 优先顺序继续保留。流量报告与改密按数据库提交顺序决定：在重置之前获准提交的报告正常入账；重置之后的旧令牌报告返回现有会话失效结果，不能绕过撤销检查写入。

## 9. APP 与网站会话失效后的行为

### 9.1 共用契约

四平台“注册”“忘记密码”打开固定网站地址，通过现有浏览器入口完成流程：桌面系统浏览器、Android Custom Tabs/浏览器、iOS 既有 SFSafariViewController 均可复用。初版不做自建 WebView 邮箱表单、客户端 SMTP、自动凭证注入、自定义回调协议或重置后自动登录。网站完成后用户回到 APP，用新密码走现有 /api/v1/client/login，仍属于同一 User。

网站地址使用各平台已存在的可信公共构建配置，不能把 /api/v1/client 地址直接拼成网页链接，也不能盲目信任用户可编辑 API 地址作为邮件域名。发布包配置的 API 与网站必须属于同一服务环境；开发环境按现有本机允许规则处理。

服务端重置提交后，旧网站 JWT 即使尚未到 exp，也因 Session 撤销而在下一次鉴权失败；旧刷新令牌不能再旋转。NativeSession 撤销后，session、nodes、config、traffic 等后续请求均不得授权成功。

这不是服务端主动推送断线：在线客户端在下次会话检查、目录检查或计量上报得到终止结果后停止；网络不可达时，按现有租约截止和生命周期检查收束。验收依据 REPORT_INTERVAL、LEASE_TTL 和实际状态机测量，不承诺邮件页面点击后所有离线设备瞬时得到通知。

网站另增加统一的最终 401 通知：受保护请求经必要的单次刷新后确认失效，通知 Provider 清用户，取消该用户未完成的读写，销毁旧总览/订单数据、管理员 YAML 编辑器及购买确认窗口，再由 RequireAuth 引导登录；网络错误和 5xx 保留重试机会。复用 Provider 的 generation，避免迟到的 auth/me 或业务结果恢复旧用户；节点页现有局部处理作为回归基线。

共享 Core 终止会话时应清理令牌、账户快照、配置、计量句柄和异步任务，关闭引擎和出口状态；generation 保证迟到响应不能恢复旧登录。沿用 native_session_expired 错误码，以兼容现有 TerminatesSession 判断。任何 APP 的显示态、后台服务或扩展都不能覆盖 Core 已退出旧认证会话的结论。桌面 Helper/常驻系统服务与账户会话不是同一生命周期，不要求停掉 Windows 服务或杀死全部 Core 进程；signed_out 且旧隧道关闭即可继续等待新密码登录。

### 9.2 保存密码与旧凭据

保存密码是平台凭据存储的独立状态，不能假定 Core 清除 token 就自动清除了它。普通租约过期、网络错误或服务暂不可用不直接证明保存密码错误，不能无条件删除用户记住的密码；但也不能在会话被撤销后无限自动重试保存密码。

四平台已经有最多一次保存密码静默重登的策略，可以保留；本次不新增循环重试或新的未知终止错误码。一次旧密码失败后必须停止这条恢复链，回到要求手动输入密码的状态。

当使用保存的旧密码登录得到 invalid_credentials，平台应清除或禁用该项保存密码，保留邮箱和非秘密偏好，提示输入新密码；新的手动登录成功且用户仍选择记住时再写入新密码。用户取消记住或明确退出账户时遵循现有清除行为。验证失败不能覆盖另一个并发手动登录刚保存的新密码，凭据删除应绑定本次尝试使用的账号和凭据版本。

macOS SessionController 目前直接采用部分返回快照，其轮询尚缺 iOS 同类的 operationVersion 保护；第 4 轮补齐必要的旧 generation/操作版本判断，并测试静默重登任务与手动新密码、切换账号、退出交错。Android 异步读取保存项后再发起静默登录的路径同样要检查新操作是否已取代旧意图。

后台 VPN、桌面守护服务和 iOS 扩展都要测试：不能依靠前台页面刷新才停止旧会话；APP 重新打开后不能因缓存显示为已连接；失败登录不能触发自动建隧道。

### 9.3 与客户端计量的交界

继续沿用现有“先关闭数据通路，再处理末次确认”的常规停止流程。密码重置后旧令牌已撤销，末次流量确认可能得到 401，必须进入现有终止会话分支，不能为了等待成功确认一直保持连接、无限重试或重新登录旧密码。

已经由后端确认的报告、序号、累计流量与订阅余额原样保留；尚未确认的旧会话尾量不得转移到新会话重放，不能重置历史用量，也不新增旧令牌的宽限授权协议。验收记录应区分已确认报告和会话终止时未确认的本地尾量，不宣称当前仅在内存中的待确认记录具备持久补传能力。

若测试覆盖仓库保留的 grant 分支，撤销继续走已有 revoking/确认收束语义，不直接归还尚未核实的预算、不删除历史账本；这只属于已有分支回归，不增加节点侧组件要求。

### 9.4 iOS 的实际会话所有者交接

iOS 的 APP 和 PacketTunnel Extension 都能运行 Core，但同一时刻只有一个持有 token 并上报。内存 ticket 交接会沿用同一 NativeSession；[native/core/internal/managed/handoff.go](native/core/internal/managed/handoff.go) 的 AdoptHandoff 先在线调用 session 核验，再恢复该会话，因此密码重置后旧 ticket 必须被服务器拒绝。

验收覆盖 AsterLink/TunnelController.swift 的 export/adopt/takeBack、NetworkTunnelControl.swift 的 release/resume，以及 PacketTunnelProvider 在 APP 挂起或被杀时独立处理会话终止。ticket 不能写入 VPN 系统配置、日志或持久存储；Extension 的本地终止检查不能因主 APP 不在前台而失效。保留现有单所有者、无 On Demand 自动拉起及有界停止流程。


## 10. 四轮实施与逐轮测试验收

以下为后续开发必须执行的验收计划，**本次文档工作没有执行这些功能测试，也没有实际连接 SMTP 发信**。自动测试使用隔离 PostgreSQL、可注入时钟和 Fake Mailer；真实邮件与系统 VPN 行为在第 4 轮用明确的验收环境执行。

### 第 1 轮：配置、数据库、SMTP 和共享防滥用

**实施范围：** 增加第 3–4 节的配置、模型、迁移和邮箱模块；实现发信任务、模板、可替换 Mailer、共享限流与清理；补 doctor 和公开 meta 能力；核实可信代理配置。此轮先在开发/验收环境验证基础能力，不向生产开放未完成的注册路径。

主要改动落点为 backend/internal/config、backend/internal/model、backend/internal/store、建议新增的 backend/internal/authmail、backend/internal/api/server.go、backend/cmd/api/main.go、backend/internal/operations/readiness.go、backend/.env.example、backend/DEPLOYMENT.md。worker 随 API 生命周期启动、停止，不使常规退出等待无限 SMTP 连接。

| 编号 | 测试场景与方法 | 必须得到的结果 |
| --- | --- | --- |
| R1-01 | 分别测试完整配置、注册开关缺省、非法布尔值、缺 SMTP 项、空白名单、错误网址/密钥；检查启动日志与 meta | 配置按既定规则失败或启用，缺省不开放注册；没有秘密进入日志或公开配置 |
| R1-02 | 空库、含旧用户及订单的库各执行两次迁移；模拟两个实例同时迁移 | 新表/索引齐全且只建立一次；旧列和业务数据保持原值；新字段默认正确；无重复用户、无伪造验证时间 |
| R1-03 | 单测规范化和域名策略：大小写、空白、允许域名、未允许子域名、相似后缀、显示名称、多地址、控制字符、Unicode 域名 | 注册只接受明确允许的完整域名；登录/找回的历史邮箱兼容不受白名单影响 |
| R1-04 | 可控 SMTP 测试服务分别提供隐式 TLS、STARTTLS、错误证书、主机名不符、无 STARTTLS 和认证失败 | 正确加密模式发送成功；其余按策略失败，绝不明文降级；中文名称和 HTML/纯文本邮件可正确解析 |
| R1-05 | 同时运行两个 worker，注入发送中退出、数据库确认失败、SMTP 超时、临时和永久拒绝 | 单次任务不会被并发领取；崩溃后可恢复；重试有次数和时间上限；有限重复邮件仍使用同一凭证 |
| R1-06 | 申请后马上重发、旧任务已领取、任务排队至过期、改密后旧任务仍在队列 | 只有当前有效凭证可用；过期/作废任务不再投递；已经发出的旧邮件不能恢复凭证有效性 |
| R1-07 | 相同邮箱不同大小写、不同实例、不同 IP 交替注册/找回；并发跨过冷却和额度边界 | 共享冷却、邮箱总额与 IP/全局额度均生效，最终接受数量不超过上限；重启不能清零 |
| R1-08 | 通过真实反向代理与 Next.js 入口模拟两个来源，再伪造 X-Forwarded-For/Forwarded 及绕过入口访问 | 可信来源可区分；伪造头不能改变授权来源；内部端口不能成为绕过入口 |
| R1-09 | 读取测试数据库、日志、公开 meta、前端构建产物；执行过期清理并停止 worker | 无明文验证码/令牌/密码/SMTP 秘密；密文载荷及时擦除；旧财务记录不受清理影响；正常服务退出有界 |

**本轮通过标准：** 上述测试全部通过；SMTP 适配器与邮件领域逻辑可被测试替换；队列与限流在多实例下可验证；迁移可重复。交付配置清单、数据库变更说明、模板预览及脱敏测试记录。Fake Mailer 的注入仅存在于测试装配，不增加生产可调用的“取验证码”接口或固定万能验证码。

### 第 2 轮：网站邮箱验证码注册

**实施范围：** 注册申请与消费 API、元信息展示、现有注册表单扩展、注册入口开关、完整错误状态；同步修改所有直接调用原注册接口的测试。成功注册继续进入原 dashboard，并使用原有 Cookie 机制。

后端主要修改 auth.go、新注册请求结构和邮箱服务调用；前端沿用 auth-form、认证 Provider、类型与 API helper，增加验证码交互。前端代理只做必要的可信来源转发补充，保留现有通用路径校验、请求体上限和多 Cookie 转发，不能误认为所有新路径都需要新增代理白名单。

| 编号 | 测试场景与方法 | 必须得到的结果 |
| --- | --- | --- |
| R2-01 | 用 Fake Mailer 捕获允许域名邮件，从真实浏览器申请、收取六位码并完成注册 | 201；仅一条新 User；EmailVerifiedAt 非空；两枚 Cookie 正确；dashboard 与后续登录可用 |
| R2-02 | 覆盖带前导零验证码、全零验证码、整段粘贴、少/多一位、全角数字、整数型输入与控制字符 | 合法六位字符串可验证；其他输入不能隐式转换后通过 |
| R2-03 | 同一挑战连续错误输入并通过不同实例/重发继续尝试，随后提交正确码 | 第 5 次错误锁定；失败次数真实写库；重发不清邮箱错误预算；到期前不能绕过 |
| R2-04 | 可控时钟推进到有效期边界、60 秒重发边界；旧码和新码分别提交 | 过期拒绝、冷却不能绕过；新申请使旧凭证失效；重试不延长旧凭证期限 |
| R2-05 | 发码后更改邮箱、白名单或关闭注册；用旧 verification_id 与其他邮箱组合 | 邮箱与用途绑定有效；最终注册重查配置；旧页面不能绕过服务端开关 |
| R2-06 | 同一邮箱并发申请、并发正确注册；注册成功后模拟网络响应丢失并重试 | 最多创建一个账户；挑战只消费一次；返回受控冲突/已使用结果，没有多份套餐或异常会话 |
| R2-07 | 直接请求原 auth/register 不带验证码；伪造 role、status、verified、user_id；混用重置 token | 全部拒绝，原入口不存在兼容绕过；不会创建用户 |
| R2-08 | 对已注册和新邮箱申请，比较状态码、响应结构、时间字段形式与多次响应耗时分布 | 公开结果不形成可靠账户存在性信号；只有合资格新邮箱收到注册邮件 |
| R2-09 | 模拟 SMTP 延迟、服务不可用、429、代理 502/504；修改邮箱时旧请求迟到；移动端键盘操作 | 状态与文案正确，按钮不重复提交，旧响应不覆盖新邮箱；计时依据服务端；没有密码持久化 |
| R2-10 | 用注册后的账户购买测试套餐、查看订单/节点、刷新 Cookie、退出与重新登录；同时验证旧账户登录 | 原有订单、权限、角色、CSRF、Cookie 旋转和节点目录行为继续通过 |

**本轮通过标准：** 注册必须经真实的验证码消费链路；前后端开关一致；无无验证注册通道；浏览器与后端集成验收通过。修复测试夹具时不能在生产逻辑中允许 example.invalid 或其他非白名单域名；普通原生登录夹具可继续以历史用户方式直接建库。

### 第 3 轮：找回链接、密码重置与全部会话撤销

**实施范围：** /forgot-password 与 /reset-password 页面，邮件 token 签发和只读验证，原子改密服务，网站/原生登录的 AuthVersion 校验，刷新锁顺序调整及稳定会话失效响应。使用现有 Cookie 和原生 token，不更换认证框架。

| 编号 | 测试场景与方法 | 必须得到的结果 |
| --- | --- | --- |
| R3-01 | 已注册、未注册、禁用、历史非白名单邮箱分别申请找回；比较公开响应并查询数据库 | 同形态 202；仅允许找回的账户收到链接；申请不修改密码、状态、会话或权益 |
| R3-02 | 连续打开邮件、执行 GET/HEAD、浏览器预取和多次 validate；检查访问日志与地址栏 | 均不消费 token；fragment 不进入服务端请求 URL；页面读取后地址栏去除秘密；不产生第三方请求 |
| R3-03 | token 缺失、格式错误、用途混用、到期、前代链接、已消费；合法 token 提交不一致、与当前相同或超长的密码 | 无越权修改；错误密码不消耗可用 token；密码字节规则与页面一致 |
| R3-04 | 为同一用户创建多个网站会话及四个平台的 NativeSession，再完成一次重置 | 原 User 被更新；所有旧会话撤销；旧 JWT、刷新令牌、native token 后续鉴权均失败；新密码登录成功，旧密码失败 |
| R3-05 | 两个正确重置请求同时提交同一 token，以及两个先后申请 token 的交错提交 | 最多一次修改成功；旧 token 不复活；AuthVersion 按成功改密次数递增 |
| R3-06 | 在网站与原生登录的 bcrypt 校验后设置同步屏障，再提交重置并释放登录；反向顺序再测 | 重置先完成时不能创建旧密码新会话；登录先完成时会话被重置撤销 |
| R3-07 | 刷新与重置并发；多个刷新、报告和重置按不同顺序竞争 User 锁 | 无死锁、无撤销后刷新成功；账务按有效事务顺序提交，旧会话不得继续计量写入 |
| R3-08 | 在更新密码、消费挑战、撤销会话、创建通知任务之间注入数据库失败 | 整体回滚；不存在改密成功但旧会话可用、或 token 被消费但密码未改 |
| R3-09 | 重置时保持其他浏览器标签页、已登录重置页和后台 APP；模拟迟到 API 响应 | 成功页面不自动登录；Provider 的 auth/me 不挡住重置表单；最终 401 清除旧总览/订单/管理员 YAML 表单和购买确认；迟到数据不复活旧状态 |
| R3-10 | 核对重置前后 User ID、角色、账户状态、套餐、订单、已确认流量、设备配置与节点归属 | 只改变密码相关字段和认证状态；没有重建账号、清零用量、赠送套餐、恢复禁用状态或跨账户影响 |

**本轮通过标准：** 密码原子更新、两类会话撤销和两处登录竞态测试全部通过；并发测试使用可控制的屏障/时钟，不能依靠随机等待碰运气。审查认证日志、邮件和 URL 后，确认未输出秘密。密码变更通知投递失败可以重试，但不能改变已经成功的重置结果。

### 第 4 轮：四平台联调、回归与发布验收

**实施范围：** 根据第 2 节与第 9 节的平台落点补注册/找回浏览器入口、网站地址配置和必要的旧保存密码处理；重点验证 Core、平台服务和 UI 对会话终止的一致认识。同步更新测试夹具、文档与 CI 中受影响的约束。

| 编号 | 测试场景与方法 | 必须得到的结果 |
| --- | --- | --- |
| R4-01 | macOS、Windows、Android、iOS 逐个平台从登录页打开注册/找回，完成网站流程后手动新密码登录 | 打开正确环境的网站；没有复制 SMTP/验证码业务；回到同一账户且不要求重装 APP |
| R4-02 | 四平台分别在前台连接、后台连接、应用重启、睡眠恢复/网络切换时由网站重置密码 | 下一次在线鉴权终止或既有租约到期后关闭旧数据通路；实际所有者结束旧认证会话；Android 前台服务/iOS 隧道按生命周期停止，桌面常驻服务可继续新登录 |
| R4-03 | 每个平台保存旧密码后执行重置，再使用旧保存项登录；并发提交新密码或切换账号 | 最多一次旧保存项在线尝试；确认 invalid_credentials 后清理该旧项；旧快照、迟到读凭据、旧失败结果不覆盖新操作，不删除设备 UUID |
| R4-04 | iOS 分别验证 APP 独占 Core、扩展持有 Core、APP 离开前台和扩展重启等生命周期；Android 验证前台 VPN 服务，Windows/macOS 验证守护进程所有权 | 密码重置能到达实际连接所有者；没有第二份 Core/旧交接凭证重新启动已撤销会话 |
| R4-05 | 有已确认流量和未确认尾量时重置；重复旧报告；新密码新会话重新连接 | 已确认账本和余额不变；旧 token 401 不造成等待成功确认的死循环；旧尾量不计入新会话 |
| R4-06 | 桌面与手机浏览器完整走注册、忘记密码、重置、跨标签页、返回/刷新、Cookie 过期与 429 恢复 | 无布局溢出、键盘阻断或状态串线；重置页可独立打开；Cookie 清理和错误反馈正确 |
| R4-07 | 用正式配置形式的 QQ SMTP 向验收人员拥有的 QQ、Gmail、163 邮箱发送注册、找回和变更通知 | 实际到信且主题、中文名称、正文、验证码/链接、有效期正确；记录到达/垃圾箱情况，不用 SMTP 接受日志代替收件验收 |
| R4-08 | 在 HTTPS 验收域名和真实代理链上验证 Cookie、Origin、链接域名、证书、限流、关闭注册和 SMTP 故障 | 邮件链接固定可信网站；Secure Cookie 和来源限制正确；关注册不影响旧用户登录/找回；邮件故障不阻断原有业务 |
| R4-09 | 执行既有 Go/前端/Core/平台测试，并运行隔离的迁移、备份恢复和回滚演练 | 原有成功基线继续通过；新增 Auth 测试通过；旧数据完整；回滚不会重新开放无验证注册 |
| R4-10 | 发布前审核实际差异、安装包公开配置、日志/截图/报告和配置说明 | 不包含真实凭据、验证码、重置 token 或节点私有参数；验收证据能对应版本与平台 |

**本轮通过标准：** 四平台分别记录结果，不能用共享 Go 单测替代 iOS 扩展或 Android VPN 服务实测，也不能把 Windows 常驻服务仍运行误判为未注销；真实邮件验收使用明确归属的测试邮箱与非生产测试账户。没有真实设备/签名条件的项目写为“未执行”，不得填为通过。需要发布的支持平台，其阻断项解决后再发布对应版本。

## 11. 现有测试资产的适配与证据要求

### 11.1 复用现有测试，不制造生产后门

| 现有资产 | 已观察到的适配点 |
| --- | --- |
| [backend/internal/api/integration_test.go](backend/internal/api/integration_test.go) | 注册 helper 直接提交 name/email/password，邮箱使用 example.invalid；需要改走 Fake Mailer 验证码链路，保留角色注入、Cookie、订单幂等和权限隔离断言 |
| [backend/internal/api/native_test.go](backend/internal/api/native_test.go) | 私有 PostgreSQL、数据库直建历史用户、设备/报告并发测试可继续复用；增加重置撤销与并发登录用例 |
| [backend/internal/api/auth_test.go](backend/internal/api/auth_test.go)、[backend/internal/config/config_test.go](backend/internal/config/config_test.go) | 扩充新注册域名/密码/开关/密钥测试；不要把仅用于注册的域名规则并入通用历史邮箱校验 |
| [scripts/test-native.py](scripts/test-native.py) | 已创建独立 PostgreSQL，但继承环境变量；显式剥离真实邮件配置，注入测试密钥和 Fake Mailer，避免运行者 .env 影响测试 |
| [frontend/scripts/api-proxy.test.mjs](frontend/scripts/api-proxy.test.mjs) | 已覆盖真实 Next.js 同源代理、多 Cookie、Origin、Retry-After、超时、重定向及头部过滤；更新 mock meta 和注册流程，增测邮件接口与来源信任配置 |
| [frontend/tests/flow.spec.ts](frontend/tests/flow.spec.ts) 与其他浏览器测试 | 注册流程需要取测试收件箱中的验证码；保留购买、刷新、登录/退出和响应式验收；新增找回/重置覆盖 |
| [scripts/test-node-catalog.py](scripts/test-node-catalog.py) | 继续验收客户端节点/配置一致性；其环境清理名单要补邮件相关前缀，勿继承真实 SMTP |
| [scripts/test-release-rehearsal.py](scripts/test-release-rehearsal.py) | 注册夹具仍走旧接口，表清单与摘要说明写死旧数量；同时有旧二进制迁移和回滚路径，需要按本次模型及真实基线适配 |
| [frontend/playwright.config.ts](frontend/playwright.config.ts) | 只允许由隔离演练提供的本机 PostgreSQL 场景；维持这一限制，不能把真实网站地址作为自动写入目标 |
| [apps/macos/Tests/AsterLinkSharedTests/Stage2Tests.swift](apps/macos/Tests/AsterLinkSharedTests/Stage2Tests.swift) | 已有 Keychain、启动在线认证、一次静默重登、退出/普通关闭差异；补重置及旧操作覆盖新密码的竞态 |
| [apps/ios/Packages/AsterLinkKit/Tests/AsterLinkKitTests/SessionControllerTests.swift](apps/ios/Packages/AsterLinkKit/Tests/AsterLinkKitTests/SessionControllerTests.swift)、同目录 LifecycleTests.swift、TunnelFlowTests.swift | 复用凭据、generation、Extension rejoin 与 owner 交接测试；加旧 ticket 被重置撤销的场景 |
| [apps/android/app/src/test/java/com/asterlink/vpn/session/SessionControllerTest.kt](apps/android/app/src/test/java/com/asterlink/vpn/session/SessionControllerTest.kt) 与 androidTest 中 Account/SecurityStore/Billing/Lifecycle 验收 | 复用保存密码、一次重登、服务终止、Keystore 与实际 Core 测试；加网站重置及后台恢复 |
| [apps/windows/AsterLink.Tests/ClientTests.cs](apps/windows/AsterLink.Tests/ClientTests.cs) 与 AsterLink.Tests.Windows 的 CredentialStore/Worker/Pipe/Recovery 测试 | 复用系统凭据、迟到 generation、worker 丢失、常驻服务与所有者策略；加改密后新登录 |
| [.github/workflows/ci.yml](.github/workflows/ci.yml) | 复用现有后端、网站代理、节点、Core 和平台检查，增加 Auth 专项；按实际工具链锁定 SMTP 依赖并验证独立 backend 构建 |

各测试启动器的环境隔离至少覆盖 SMTP_、AUTH_MAIL_、REGISTRATION_、ALLOW_REGISTRATION 和 WEBSITE_BASE_URL，并避免 Config.Load 从真实 backend/.env 补载这些项。应显式注入测试配置或使用隔离工作目录。测试邮件只能进入测试 Mailer/本机 SMTP；不为了方便读取而新增公开调试端点。

迁移演练应比较**原有列和业务记录**是否保持原值，再单独检查新增列的默认值，不能直接要求新增字段后的整行 JSON 指纹等于迁移前结果。scripts/test-release-rehearsal.py 的固定表数量、旧注册假设和回滚版本语义必须修正，不能仅增加一张表后修改预期数字就宣称演练充分。

包含注册的浏览器 trace、网络记录和失败截图可能记录验证码、密码或重置 token。测试应使用一次性合成数据，报告导出前移除敏感请求体和 URL；真实 SMTP 验收不保存含可用 token 的截图，证据只保留脱敏状态和时间。

### 11.2 每轮验收记录格式

每条验收记录包含：轮次与编号、被测提交/构建版本、操作系统或浏览器、隔离环境标识、前置数据、实际操作、预期结果、实际结果、脱敏证据路径、通过/失败/未执行、问题及修复后的复验结果。

数据库类验收附受影响行数、版本和状态变化以及数据不变断言；接口类附状态码、稳定错误码和 Cookie 属性；邮件类附任务 ID 与脱敏投递状态；平台类附实际 Core/服务/扩展持有者及停止时刻。不能用“页面看起来正常”替代事务撤销检查，也不能用单一平台结果覆盖全部客户端。

## 12. 发布、故障处理与回滚

### 12.1 发布顺序

1. 在隔离环境完成四轮必要测试；准备数据库备份，验证可恢复；准备真实 SMTP 授权码、邮件密钥、网站 HTTPS 地址、允许域名和代理信任配置。
2. 以短暂关闭注册的方式协调后端和网站发布，先执行兼容的附加迁移，再部署具备强制验证码验证的后端与 worker，然后部署包含新注册/找回页面的网站。
3. 旧原生登录契约保持兼容。具备新入口/保存密码处理的客户端分别完成平台验收后发布；后端撤销返回旧客户端已识别的错误码。
4. 核对 doctor、新表、队列与模板，执行受控邮件冒烟及注册/重置一次完整闭环；确认 cookie、网站域名与代理来源后再开启注册。
5. 观察首日的邮件队列等待、临时失败率、429、验证码锁定、重置失败、原生会话终止与正常登录成功情况，使用脱敏任务 ID 排查。

关闭注册只停止申请注册验证码和最终新用户创建，不停止已有用户登录、找回、订单或客户端 API。若邮件服务中断，统一暂停邮件申请或维持有界队列重试，清楚展示暂不可用；不能退回无验证注册、固定验证码或人工公开 token。

### 12.2 回滚边界

数据库采取向前兼容的附加迁移，回滚应用时不删除新表、不恢复旧密码哈希、不把已撤销会话改回有效、不回退 AuthVersion，也不还原已经消费的 token。需要恢复数据库时按独立灾难恢复程序处理，不能将一般应用回滚等同于全库恢复。

旧后端二进制当前不认识 ALLOW_REGISTRATION，也没有强制验证码校验。**仅设置 ALLOW_REGISTRATION=false 并回滚旧二进制不能保证注册关闭。** 若必须回到旧后端，先在实际入口可靠阻断注册创建和发码路径，禁止直连绕过；同时关闭新找回页面/入口，待兼容版本恢复后再开放。优先回滚到已经包含强制邮箱验证与版本检查的已验收版本。

旧版本也不具备完整密码版本并发处理，因此不得在旧后端混跑期间继续开放新重置写接口。多实例滚动发布时，只有全部承接登录/刷新/改密请求的实例具备同一并发契约后，才开放密码重置；新旧版本之间不能仅靠负载均衡碰运气。

回滚演练必须验证：原密码不会复活、被撤销令牌仍无效、未开放无验证注册、订单与已确认流量保留、新密码在兼容版本仍能登录。任何条件不满足都属于发布阻断项。

## 13. 最终完成定义

交付达到以下状态才认定本项目本轮 Auth 完善完成：

- 新用户只能通过允许域名邮箱收到的六位数字验证码注册，直接调用原注册 API 无法绕过。
- 已有用户可通过邮件专属链接设置新密码，链接一次性、限时、用途明确，浏览器访问不提前消费。
- SMTP 配置按需求保存在后端 .env，消息可在真实目标邮箱收到；错误配置、发送故障与滥用有明确处理。
- 改密原子撤销网站与 APP 会话，并修复旧密码登录/刷新与重置的并发问题。
- 四个平台继续使用原有客户端鉴权和计量链路，入口、保存密码、后台会话终止与新密码登录分别验收。
- 用户身份、角色、套餐、订单、已确认流量与节点关系完整保留；文档和测试夹具同步更新。
- 四轮验收记录真实可核对；失败或未执行项目明确列出，不将规划中的测试写成已通过。

本文件供实施人员直接按轮次接手。具体命名可遵循仓库风格微调，但邮箱证明、严格域名策略、数据库原子性、两类会话撤销、兼容错误码、秘密保护与逐轮验收结果不得弱化。

## 14. 实施记录

### 第 1 轮记录（2026-10-20）：配置、数据库、SMTP 和共享防滥用

- **分支/基线：** `worktree-auth-round1-foundation`，基于 main `67e7b37`；PR 与 CI 结果见 PR 页面。
- **环境：** macOS 26（darwin/arm64），Go 1.26.6；`scripts/test-native.py` 启动的私有 PostgreSQL 16.15（仅 Unix socket，测试结束销毁）；进程内受控 SMTP 测试服务（自签 CA）与 Fake Mailer；可注入时钟。未连接任何真实 SMTP、未读取任何真实 `.env`。
- **交付：** `backend/internal/authmail`（摘要/加密密钥派生、邮箱策略、挑战签发与校验、共享限流、模板、SMTP 适配器、发送 worker、清理）；`config/mail.go`；三张表与 `users` 三列及部分唯一索引；`/meta` 能力字段；`TRUSTED_PROXIES`；doctor `auth_mail`；`.env.example`、`backend/DEPLOYMENT.md`、[docs/auth-mail.md](docs/auth-mail.md)（配置清单、数据库变更、模板预览）；测试启动器剥离 `SMTP_`/`AUTH_MAIL_`/`REGISTRATION_`/`ALLOW_REGISTRATION`/`WEBSITE_BASE_URL`/`TRUSTED_PROXIES`。
- **依赖：** `github.com/wneessen/go-mail v0.7.2`（MIT）。未选 v0.8.x：它要求 `go 1.25` 并连带升级全部 golang.org/x 依赖；v0.7.2 仅依赖 golang.org/x/text，`go.mod` 的 `go 1.24.0` 和其余依赖版本保持不变。
- **整体回归：** `python3 scripts/test-native.py`（后端全部包，`-race`）exit 0；`go vet ./...` 通过；`scripts/sync-backend-shared.py --check` 通过；authmail 包在 `-race -count=3` 下连续通过。

| 编号 | 证据（测试） | 实际结果 | 结论 |
| --- | --- | --- | --- |
| R1-01 | `config.TestMail*`、`TestPartialSMTP…`、`TestEnvExampleMailGroupStartsWithMailOff`、`TestProductionMailRequiresHTTPS`、`api.TestMetaAuthCapabilities`、`TestConsoleReadiness…` | 缺省关闭注册；38 种非法配置全部拒绝启动且错误不回显取值/秘密；`.env.example` 原样加载可启动且邮件关闭；meta 仅含开关、域名和时长，不含 SMTP/网址/密钥；未配置邮件时 doctor 报 `auth_mail_not_configured` 且 `production_ready=false` | PASS |
| R1-02 | `authmail.TestMigrationRepeatableOverLegacyData`；既有测试夹具对空库迁移两次 | 回退为旧结构并写入旧用户（含 admin、disabled）与订单后，两实例并发迁移再迁移一次：旧列逐值不变、订单不变、`auth_version=1`、验证时间为空；后续迁移不重置 AuthVersion；索引齐全、部分索引条件为常量状态；认证表无外键 | PASS |
| R1-03 | `authmail.TestRegistrationEmailPolicy`、`TestHistoricalIdentityRuleUnaffectedByAllowList`、`config.TestDomainList…` | 仅完全相等的允许域名通过；子域、相似后缀、显示名、多地址、换行/控制字符、尾点、Unicode/西里尔混淆域名拒绝；不改写点号与加号；历史邮箱规则不受白名单影响 | PASS |
| R1-04 | `authmail.TestSMTPAdapterTransportSecurity`（11 个场景）、`TestSMTPFromNameHeaderInjectionRefused` | 隐式 TLS 与强制 STARTTLS 发送成功并正确解析中文发件名/主题、纯文本与 HTML、固定 Message-ID；不受信证书、主机名不符、无 STARTTLS、仅 TLS 1.1、认证失败均失败且从未在明文连接发送凭据；5xx 永久、4xx 临时；不应答服务器在 2 秒硬超时内结束 | PASS（真实 QQ SMTP 属 R4-07，未执行） |
| R1-05 | `TestWorkerConcurrentClaimsDeliverOnce`、`TestWorkerCrashRecoveryAndStaleWriteBack`、`TestWorkerRetriesAreBounded…`、`TestWorkerPermanentTimeoutAndWriteBackFailure`、`TestWorkerShutdownIsBounded` | 两 worker 并发 12 封各投递一次；租约内不可重领，到期由他人恢复，崩溃方迟到写回被丢弃；临时失败按 15s/60s(+≤20%) 退避、第 3 次终结、Message-ID 与验证码不变、不延长有效期；永久拒绝立即终结；卡死调用按超时结束；注入写回失败后租约到期重投，两封相同 Message-ID 与相同验证码（有限重复）；停止 worker 不等待卡住的 SMTP，任务交回，用尽次数后终结 | PASS |
| R1-06 | `TestSupersededAndStaleMail`、`TestResetRequestsHaveEqualShape` | 重发后旧任务取消并擦除密文、旧码返回过期、新码可用且只能用一次；已领取的旧任务在预检时取消，即使送达旧码也无效；排队至过期前 1 分钟的任务取消不投递；AuthVersion 变化后排队的找回邮件取消、旧 token 失效；validate 不消费 token | PASS（“改密”以直接递增 AuthVersion 模拟，真正的改密服务属第 3 轮） |
| R1-07 | `TestSharedMailboxCooldownAndQuotas`、`TestConcurrentLimitsNeverOvershoot`、`TestVerificationFailuresAreCommittedAndBudgeted` | 大小写不同、注册/找回交替、两个服务实例之间共享 60 秒冷却与 5/小时、10/天额度；新实例（模拟重启）看到相同计数；20 个并发同邮箱只接受 1 个；同 IP 轮换邮箱恰好 20 个；全局每分钟恰好 5 个（测试上限）；队列满返回不可用；错误验证码次数在返回错误前已提交，重发不清零邮箱错误预算，跨实例生效 | PASS（“多实例”为同进程内多个独立 Service 共享同一数据库，未启动多个 OS 进程） |
| R1-08 | `api.TestClientAddressTrust` | 未配置可信代理时伪造 `X-Forwarded-For`/`X-Real-IP`/`Forwarded` 均无效；可信入口后两个真实地址可区分；客户端伪造的前缀被忽略；绕过入口直连无法伪造地址 | 部分：后端 PASS；**真实反向代理 + Next.js 入口链路未执行**——Next.js 代理目前不转发浏览器地址，第 2 轮补充转发并联测 |
| R1-09 | `TestNoSecretsAtRestAndCleanup`、`TestWorkerShutdownIsBounded`、`TestMetaAuthCapabilities`、config 错误检查 | 三张认证表行数据中无明文验证码、找回链接/token、收件地址（任务表）、IP、SMTP 授权码或邮件密钥；发送后密文立即擦除；worker 日志仅含任务 ID 与脱敏收件提示；8 天后清理删除挑战/任务/计数桶而用户与订单不变，协调行闲置 30 天后才删除；meta 不含秘密 | PASS（前端构建产物扫描未执行：本轮未改前端且未新增任何前端环境变量） |

**已知限制与交接给后续轮次：**

1. 原 `/api/v1/auth/register` 本轮未改动，仍可无验证码注册；第 2 轮必须接入 `VerifyRegistration` 并受 `ALLOW_REGISTRATION` 约束（R2-07）。在此之前不应以本轮版本对生产开放注册。
2. `/meta` 的 `password_reset_enabled` 目前反映“邮件配置完整”，找回接口在第 3 轮提供；前端第 2、3 轮再消费这些字段。
3. 可用接口：`Service.IssueRegistration / IssueReset / VerifyRegistration(onSuccess 在同一事务内建号) / ResolveReset（只读）/ EnqueuePasswordChanged(tx)`。第 3 轮需新增消费重置、撤销两类会话并作废其他重置挑战的改密服务，锁顺序 User → 邮箱协调行 → Challenge → Session/NativeSession → MailJob。
4. SMTP 固定使用 `AUTH PLAIN`（仅在 TLS 上执行）；不支持 PLAIN 的服务商会以 `smtp_auth` 失败。QQ/163/Gmail/Outlook 支持 PLAIN，真实验收在 R4-07。
5. 小时/天额度为固定时间窗，窗口边界附近单邮箱最多可连续 2×5 次，仍受每天 10 次与 60 秒冷却约束。
6. 指标：仓库无指标系统；本轮提供 worker 进程内计数、脱敏日志和 doctor 队列统计（待投递、24 小时失败、最老等待），验证码错误/锁定计数尚未单独导出。
7. `scripts/test-release-rehearsal.py` 在本轮之前已失效（写死 10 张表，main 已有 14 张；注册夹具走旧接口），按第 11.1 节在 R4-09 适配；本轮未运行它，CI 也不运行它。
8. 清理每小时执行一次（规划建议每天），保留期按 4.3 节；关机中断的投递会消耗一次尝试。

### 第 2 轮记录（2026-10-20）：网站邮箱验证码注册

- **分支/基线：** `worktree-auth-round2-register`，基于 main `08e3f40`（含第 1 轮 PR #46）；PR 与 CI 结果见 PR 页面。
- **环境：** macOS 26.6.2（darwin/arm64），Go 1.26.6，Node 26.10.0，Next.js 16.3.6，Playwright 1.63.0 + 本机 Chrome；私有 PostgreSQL 16.15（仅 Unix socket，测试结束销毁）；Fake Mailer（进程内捕获，经第 1 轮真实 worker 投递）；可注入时钟。未连接真实 SMTP、未读取真实 `.env`。
- **交付：**
  - 后端：新增 `POST /api/v1/auth/register-code`；`POST /api/v1/auth/register` 改为必须携带 `verification_id` + `code`，经 `VerifyRegistration` 在同一事务中消费挑战、建号（`email_verified_at` 写入、角色/状态由服务器固定）、建会话；开关与白名单在最终注册时按当前配置重查；错误码按第 5 节映射（`failAuthMail` 供第 3 轮复用）。原无验证码注册路径已删除。
  - 网站：注册表单增加验证码字段/发送与重发按钮/服务端相对秒数倒计时/允许域名提示/注册关闭与 meta 失败状态；改邮箱即清空验证码与 verification_id，并以请求序号丢弃迟到响应；密码按 UTF-8 字节、昵称按 Unicode 字符校验（去掉 HTML 字符数限制）；验证码输入只保留 ASCII 数字（支持带空格粘贴、`inputmode=numeric`、`autocomplete=one-time-code`）；`APIError.retryAfter` 读取 Retry-After；`/auth/register-code` 不触发自动刷新；首页主按钮与登录页“创建账户”按 `/meta` 显示。
  - 代理来源地址：Next.js 新增可选 `CLIENT_IP_HEADER`，仅转发该头最后一项且必须是单个 IP，作为 `X-Forwarded-For` 交给只信任 `TRUSTED_PROXIES` 的 Go；文档补充 Nginx 两个入口都必须覆盖转发头（否则 Go 信任 127.0.0.1 后可经后端入口伪造）。
  - 测试与文档：`auth_register_test.go`（R2-02～R2-08 及字段规则）；集成测试注册夹具改走验证码链路（保留的 `.test` 保留域名经配置覆盖白名单，生产逻辑不放行任何非白名单域名）；目录验收夹具接入 Fake Mailer 并新增 `tests/register.spec.ts`（CI 的 `test-node-catalog.py` 运行）；`api-proxy.test.mjs` 增加注册界面状态与来源地址用例；更新 `docs/auth-mail.md`、`docs/deployment.md`、`backend/DEPLOYMENT.md`、两份 `.env.example`。
- **整体回归：** `python3 scripts/test-native.py`（后端全部包，`-race`）exit 0；`go vet ./...` 通过；`sync-backend-shared.py --check` 通过；前端 `lint`、`typecheck`、`build --webpack`、`test:api-proxy`（14/14）、`test:nodes`、`test:downloads` 通过；`test-node-catalog.py` PASS（8 个浏览器用例含注册，及 Mihomo 实流量账本）。

| 编号 | 证据（测试） | 实际结果 | 结论 |
| --- | --- | --- | --- |
| R2-01 | `api.TestRegistrationCodeFlow`；`tests/register.spec.ts`（真实 Next.js → Go → PostgreSQL，Chrome） | 202 后不建 User；邮件经 worker 投递到 Fake Mailer；先错码提示且不建号，正确码 201、恰好一条 User、`email_verified_at` 非空、角色 user/active；两枚 HttpOnly/Lax/`/api/v1` Cookie；dashboard、刷新页面、退出后用密码重新登录均正常；浏览器存储为空 | PASS |
| R2-02 | `TestRegistrationCodeFormat`；浏览器用例 | `000000`、`012345` 可注册；5/7 位、全角数字、首尾空白、换行、字母、符号、NUL、空串均为 `invalid_verification_code` 且不计入试错；JSON 数字 `12345` 被严格解码拒绝；网页输入“９９ 99-99x99”只保留 `999999` | PASS |
| R2-03 | `TestRegistrationGuessingLimits` | 跨两个 API 实例交替 4 次错误后库内 failures=4，第 5 次 `verification_locked`，随后正确码仍锁定；默认预算下 4 次错误 + 重发 + “重启”实例再错 1 次后正确码返回 429 + Retry-After，9 分钟后（验证码到期前）仍 429，未建号 | PASS |
| R2-04 | `TestRegistrationExpiryAndResend` | 59 秒重发 429、`Retry-After: 1`；60 秒可重发；旧码随即 `verification_expired`；新码在其创建后满 10 分钟过期；另一邮箱在 9:59 成功 | PASS |
| R2-05 | `TestRegistrationBindingAndConfiguration` | 他人邮箱、随机/非法 verification_id 均 `verification_invalid`；关闭注册、缩小白名单、邮件配置缺失的实例上同一有效验证码分别 403/400/403；配置恢复后原挑战仍可完成；子域与显示名格式拒绝 | PASS |
| R2-06 | `TestRegistrationConcurrency` | 10 个不同 IP 并发申请同一邮箱：1×202、9×429，仅 1 个 active 挑战；8 个并发正确提交（两实例）：1×201、7×`verification_used`；重放得 `verification_used`；1 个用户、1 个会话、0 个订阅；已有账户先错码 `verification_invalid`，正确码后才 409 | PASS |
| R2-07 | `TestRegistrationHasNoBypass` | 旧格式请求 `verification_required`；附加 role/status/verified/email_verified/user_id/plan_id/id 均 `invalid_json`；重置 token 当验证码/ID 用均被拒；非白名单域名在最终注册被拒；全程 0 个用户 | PASS |
| R2-08 | `TestRegistrationCodeDoesNotRevealAccounts` | 已注册与新邮箱各 15 次：状态码、字段集合与类型、verification_id 均为 UUID、提示文案完全一致；中位耗时 1.85ms 对 1.93ms（本机，`-race`）；只有新邮箱收到邮件 | PASS（耗时仅为单机样本，非生产网络测量） |
| R2-09 | `api-proxy.test.mjs` “registration: …”（模拟后端 + 真实 Next.js 生产构建 + Chrome） | 非白名单域名即时提示且不发请求；慢响应期间改邮箱，旧响应被丢弃；发码后按钮“N 秒后可重发”禁用并显示有效期；429 显示“1:15 后重试”并按 Retry-After 禁用；503 与代理 504 显示对应文案且表单保留；错误码不清空姓名/邮箱；390px 无横向溢出；关闭注册时注册页、首页、登录页一致 | PASS（真实 SMTP 延迟属 R4-07；未在真实手机键盘上测试，仅断言 inputmode/autocomplete） |
| R2-10 | `TestPostgresIntegration`（注册夹具改走验证码）；目录验收的历史账户用例 | 新注册账户的测试购买幂等、并发、订单、刷新旋转、CSRF/CORS、角色隔离、退出全部通过；数据库直建的历史账户（含 admin）登录与节点用例通过 | PASS |
| R1-08（第 1 轮遗留） | `api-proxy.test.mjs` “client address …”；`api.TestClientAddressTrust` | 未配置时任何来源头都不转发；配置 `CLIENT_IP_HEADER` 后只转发单个合法 IP（含 IPv6、追加链取最后一项），伪造的 `X-Forwarded-For` 不替代、非法值不转发；非法配置 503 | 部分：Next.js 与 Go 分段 PASS；**真实 Nginx → Next.js → Go 整链未执行**（R4-08） |

**已知限制与交接给后续轮次：**

1. `frontend/tests/flow.spec.ts` 与 `scripts/test-release-rehearsal.py` 仍按旧的无验证码注册编写；二者在本轮之前已失效且不在 CI 中运行（第 1 轮记录第 7 条），随 R4-09 一并按真实发码链路适配（演练需为真实 `cmd/api` 提供测试收件箱）。
2. 忘记密码入口、`/forgot-password`、`/reset-password` 页面与 `site-shell` 认证路由扩展属第 3 轮；本轮登录页未放置尚不存在页面的链接。
3. `/meta` 读取失败时注册页显示错误与重试，首页/登录页按“未开放”处理（不默认开放）。
4. 回滚到第 2 轮之前的二进制会重新开放无验证码注册，见 `backend/DEPLOYMENT.md` 第 2 轮第 4 条。
5. 浏览器倒计时以服务端返回的相对秒数为准，页面刷新后需重新获取验证码（服务端冷却仍生效并以 429 + Retry-After 提示剩余时间）。

### 第 3 轮记录（2026-10-20）：找回链接、密码重置与全部会话撤销

- **分支/基线：** `worktree-auth-round3-reset`，基于 main `6bc0742`（含第 2 轮 PR #47）；PR 与 CI 结果见 PR 页面。
- **环境：** macOS 26（darwin/arm64），Go 1.26.6，Node 26.10.0，Next.js 16.3.6（生产构建 `--webpack`），Playwright + 本机 Chrome；私有 PostgreSQL 16（仅 Unix socket，测试结束销毁）；Fake Mailer（经真实 worker 投递）；可注入时钟。未连接真实 SMTP、未读取真实 `.env`。
- **交付：**
  - 后端：`authmail.CompleteReset`（唯一改密服务，单事务按 User → Challenge → Session/NativeSession → MailJob 加锁：复核账户与挑战 → 写新哈希、`password_changed_at`、`auth_version+1`、补齐空的 `email_verified_at` → 消费本 token 并作废同账户其他重置挑战、取消其未发邮件 → `RevokeAllSessions` 撤销全部 `sessions` 与 `native_sessions` → 写 `admin_audits`（`auth.password_reset`）→ 排入“密码已修改”通知）；新增 `POST /auth/forgot-password`、`/auth/reset-password/validate`（只读）、`/auth/reset-password`（成功后清除发起浏览器 Cookie，不登录）；管理员新密码沿用 16 字节下限（锁内按当前角色复核）。
  - 竞态修复：网站与客户端登录在 bcrypt 后于建会话事务内锁 User 并复核 `auth_version`/哈希/状态（`lockVerifiedUser`），过期验证返回 401 `invalid_credentials`；网站刷新改为“事务外定位候选 → 锁 User → 锁 Session 复核 → 旋转”，与重置锁顺序一致。原生已有 `nativeTransaction` 顺序不变；撤销沿用 `native_session_expired`。
  - 网站：`/forgot-password`（通用提示、服务端倒计时、429/503/格式错误）与 `/reset-password`（从 fragment 读 token 到内存后立即 `history.replaceState` 去除；Strict Mode 下用 ref 防止二次初始化丢 token；监听 `hashchange` 处理页面已打开时再点链接；只用 POST 验证/提交；按请求渲染 no-store，`Referrer-Policy: no-referrer`、`X-Robots-Tag: noindex`、`<meta name="referrer" content="no-referrer">`）；登录页“忘记密码？”；`site-shell` 认证路由加入两页；三个新接口不触发自动刷新。
  - 统一最终 401：受保护请求经一次刷新仍 401 时 `api.ts` 发出会话结束通知，Provider `setUser(null)`（递增 generation，迟到的 `auth/me` 或业务结果不能恢复旧用户）；`RequireAuth` 卸载总览/订单/节点/管理员 YAML 编辑器并跳转登录；套餐页购买确认框绑定打开它的账户 ID，会话结束即关闭。网络错误与 5xx 不触发。
  - 测试与文档：`auth_reset_test.go`（R3-01～R3-08、R3-10；屏障为测试内 GORM 回调 + 请求 context 标记，故障注入同理，生产代码无测试钩子）；`api-proxy.test.mjs` 新增找回/重置与跨标签最终 401 用例；目录验收夹具新增 `/fixture/reset-link`、`/fixture/historical-user`（控制令牌保护，仅测试装配）与 `tests/reset.spec.ts`（CI 的 `test-node-catalog.py` 运行）；更新 `docs/auth-mail.md`“找回与重置接口”、`backend/DEPLOYMENT.md` 第 3 轮。
- **整体回归：** `python3 scripts/test-native.py`（后端全部包，`-race`）exit 0；`go vet ./...`、`sync-backend-shared.py --check` 通过；第 3 轮与注册、集成测试 `-race -count=3` 连续通过；前端 `lint`、`typecheck`、`build --webpack`、`test:api-proxy`（15/15）、`test:nodes`、`test:downloads` 通过；`test-node-catalog.py --skip-build` PASS（9 个浏览器用例含注册与重置，及 Mihomo 实流量账本）。

| 编号 | 证据（测试） | 实际结果 | 结论 |
| --- | --- | --- | --- |
| R3-01 | `TestResetRequestsRevealNothingAndChangeNothing`；`reset.spec.ts` | 已注册、历史非白名单（`corp.example`）、禁用、未注册四类：同为 202、字段集合/提示/`resend_after_seconds` 完全一致；仅 active 的两类收到链接；申请后密码哈希、状态、`auth_version`、验证时间不变，原会话仍有效，未建账户；格式错误 `invalid_email`、附加字段 `invalid_json`；同邮箱 60 秒内再申请 429 + Retry-After | PASS |
| R3-02 | `TestResetValidationNeverConsumes`；`api-proxy` 重置用例；`reset.spec.ts` | validate 连续 5 次均 200 且挑战仍 active；GET/HEAD 访问重置接口（含带 query 的 token）一律 404 且不消费；同一链接在浏览器打开两次、来回跳转后仍可用；打开后地址栏无 fragment；浏览器全部请求 URL 与 mock 后端收到的 URL 均不含 token，token 只出现在两个 POST 请求体；页面无第三方请求，`localStorage`/`sessionStorage` 为空；响应头 no-referrer/noindex/no-store | PASS（Nginx 访问日志属 R4-08，未执行；fragment 不发往服务器由浏览器保证） |
| R3-03 | `TestResetTokenAndPasswordRules` | 缺失、过短、过长、非法字符、UUID、随机合法格式、注册 `verification_id`、注册验证码、被新链接取代、已消费、到期（30 分钟整点失效、前 1 秒可用）、账户禁用后，validate 与 reset 均 `reset_token_invalid`；不一致 `password_mismatch`、同当前密码 `password_unchanged`、9 字节与 73 字节 `invalid_password`，这些都不消耗 token（随后 validate 200 并成功）；4 个汉字（12 字节）被接受；管理员 15 字节拒绝、16 字节成功；网页按 UTF-8 字节提示“当前 9 字节” | PASS |
| R3-04 | `TestResetRevokesAllSessionsAndKeepsAccount`；`reset.spec.ts` | 两个网站会话 + macOS/Windows/Android/iOS 四个 NativeSession：重置后旧 access 401 `unauthorized`、旧 refresh 401 `session_expired`，四个 token 的 session/nodes/config/traffic 全部 401 `native_session_expired`，库内无未撤销会话；另一账户不受影响；旧密码网站/客户端登录 401，新密码两端均登录到同一 User；发起浏览器两枚 Cookie 被清除；真实浏览器中另一标签页下一次受保护请求后回到登录页 | PASS（四平台以 API 层模拟会话；真实 APP 行为属 R4-02） |
| R3-05 | `TestResetConcurrentSubmissions` | 同一 token 8 个并发提交（两个 API 实例）：1×200、7×`reset_token_invalid`，`auth_version`=2，审计 1 行；已 validate 的旧链接 A 在新链接 B 使用后仍无效；之后再申请 C 成功；三次成功后 `auth_version`=4，四个历史 token 全部无效 | PASS |
| R3-06 | `TestResetLoginRaces`（web/native × reset_first/login_first） | 屏障停在 bcrypt 已通过、锁 User 之前：期间完成重置，释放后登录 401 `invalid_credentials`、无 Cookie、0 会话；反向（重置停在锁 User 之前、登录先完成）后重置撤销该会话，随后鉴权 401。变异验证：去掉锁内版本复核后 reset_first 两例即失败（返回 200 并建会话） | PASS |
| R3-07 | `TestResetRefreshAndTrafficOrdering` | 报告持有 User 锁时重置等待，报告 200 入账后重置完成，下一次报告 401；重置持有 User 锁时刷新与报告都等待，释放后刷新 401、报告 401 且不入账；6 个会话×3 次刷新 + 20 次连续报告 + 重置自由竞争：全部在时限内完成、只有 200/401（无死锁 503）、所有刷新令牌事后 401、报告行数 = 接受数、订阅 `used_units` = 报告扣费之和 | PASS |
| R3-08 | `TestResetIsAtomic` | 分别在更新密码、消费/作废挑战、撤销网站会话、撤销客户端会话、取消邮件任务、写审计、排入通知 7 处注入数据库错误：均 503，密码/版本/时间字段不变，两类会话仍有效，token 仍可 validate，无审计与通知残留；去掉注入后同一 token 成功 | PASS |
| R3-09 | `api-proxy` 重置用例（真实 Next.js 生产构建 + Chrome + mock 后端）；`reset.spec.ts`（真实后端） | 另一浏览器打开购买确认框与总览：重置后点击确认 → 最终 401 → 对话框关闭、页头变为“登录账户”；总览标签切到订单 → 跳转登录、旧总览内容消失；在仍登录的浏览器第二个标签中打开找回与重置页面可正常完成，成功页不自动登录并清除 Cookie；页面错误为 0 | 部分：上述 PASS；**开发模式 Strict Mode 双重初始化未在浏览器实测**（生产构建不双调用，实现以 ref 保护）；“后台 APP”属 R4-02 |
| R3-10 | `TestResetRevokesAllSessionsAndKeepsAccount` | ID、邮箱、姓名、角色、状态、创建时间不变；只变哈希、`password_changed_at`、`auth_version`（1→2）、补齐的 `email_verified_at`；订阅（ID、额度、已用 4242、到期）与订单不变；NativeSession 未删除，设备、平台、节点、序号保持，仅写 `revoked_at`；审计 Actor/Target=该用户、Version=2；通知邮件不含 token/新密码 | PASS |

**已知限制与交接给后续轮次：**

1. 多实例滚动发布时须全部 API 实例为本版本后再开放找回（`backend/DEPLOYMENT.md` 第 3 轮第 1 条）；回滚边界同第 2 条。
2. 套餐页“确认模拟开通”使用 `crypto.randomUUID()`，在非 HTTPS、非 loopback 的 HTTP 来源下不可用（既有行为，非本轮引入）；代理测试中登录页签改用 loopback 地址。生产为 HTTPS，不受影响。
3. 第 2 轮 `register.spec.ts` 末尾的冷却断言假定流程超过 1 秒，本机流程约 1 秒时会显示 `1:00`；已放宽为接受 `0:ss` 或 `1:00`（仅测试）。
4. `validate`/`reset` 沿用认证入口的进程内 IP 限流，未另设独立参数；长随机 token 不适用六位码的邮箱试错预算。
5. 四平台“忘记密码”入口、旧保存密码处理与真实 APP 会话终止属第 4 轮；`flow.spec.ts` 与发布演练脚本仍待 R4-09 适配（同第 2 轮记录第 1 条）。

### 第 4 轮记录（2026-10-20）：四平台联调、回归与发布验收

- **分支/基线：** `worktree-auth-round4-platforms`，基于 main `c84de59`（含第 3 轮 PR #48）；PR 与 CI 结果见 PR 页面。
- **环境：** macOS 26（darwin/arm64），Go 1.26.6，Swift（Xcode 工具链）、Node 26.10.0、Next.js 16.3.6（生产构建 `--webpack`）、Playwright + 本机 Chrome；私有 PostgreSQL 16（Unix socket / 演练为仅 loopback 的 SCRAM 集群，结束销毁）；Fake/捕获 Mailer 经真实 worker 投递。本机**无 JDK/Android SDK、无 .NET、无 Windows、无已签名 iOS 设备或付费 Apple 团队、无 HTTPS 验收域名、未持有真实 SMTP 授权码**；Android 与 Windows 只能由 PR 的平台 CI 编译与运行单测。未连接真实 SMTP、未读取真实 `.env`。
- **交付：**
  - 四平台“忘记密码”入口：macOS `SessionController.forgotPasswordURL` + 登录页 `Link`；Windows `ForgotPasswordUrl` + `OpenForgotPasswordCommand` + `LoginView` 按钮；Android `WebPage.ForgotPassword("/forgot-password")` + 登录页按钮（中/英文案）；iOS 登录页按钮复用 `AppModel.openWebsite("forgot-password")`。地址均由既有 `WEBSITE_BASE_URL` 拼接，APP 内无邮箱/验证码/重置逻辑。
  - 旧保存密码（9.2）：四平台 `invalid_credentials` 改为**比较后删除**——只有保存项仍等于刚被拒绝的密码才删除；邮箱偏好与设备 UUID 保留。**行为变更**：此前手动输错任意密码也会删除已保存的正确密码（Windows/Android/macOS/iOS 单测原本断言该行为），现按 9.2“凭据删除应绑定本次尝试使用的账号和凭据版本”只删被拒绝的那一项；相应旧断言改为“输入的正是保存的旧密码”。
  - 操作版本保护：macOS 新增 `operationVersion`（登录、退出、动作递增；轮询/动作结果与版本不符即丢弃）；macOS/iOS/Android 的静默重登在调度时固定账号与版本，读取凭据后若用户已登录、切换账号或退出则放弃；iOS/Android 启动时读取保存密码期间若用户已手动登录，启动的自动登录放弃。Windows 的读取为同步调用，无此窗口，未改。
  - 回归资产（R4-09）：`frontend/tests/flow.spec.ts` 改走验证码注册（从演练收件箱取码）并把 `/forgot-password` 加入移动端页面检查；`playwright.config.ts` 增加 `testMatch: ['flow.spec.ts']`（此前会误跑需要目录夹具的 4 个用例）；`scripts/test-release-rehearsal.py` 重写为三二进制演练（旧版本 `67e7b37` 产生旧数据 → 当前代码迁移 → 回滚到已强制验证的 `c84de59`），按旧表**原有列**比较指纹并单独核对新增列缺省值、按 17 张表集合（非计数）核对，环境清理名单补 `SMTP_`/`AUTH_MAIL_`/`REGISTRATION_`/`ALLOW_REGISTRATION`/`WEBSITE_BASE_URL`/`TRUSTED_PROXIES` 等；浏览器阶段改用新测试装配 `TestReleaseRehearsalFixture`（与 `cmd/api` 同样从环境加载配置，worker 投递到捕获收件箱，取码/取链接只经控制令牌端点）；新增旧账户邮件重置与回滚后核验。
  - 文档：`backend/DEPLOYMENT.md` 第 4 轮条目、`docs/auth-mail.md`“四平台 APP”、`docs/deployment.md` 演练说明。
- **整体回归（本机）：** `python3 scripts/test-native.py`（后端全部包，`-race`）exit 0、0 个 FAIL；`go vet ./...` 通过；macOS `swift build` 与 `swift test` 52 个用例 0 失败；iOS `AsterLinkKit` `swift test` 82 个用例 0 失败；`test-release-rehearsal.py` 缺省目标两次 PASS（`/tmp/r4-rehearsal-1`、`-2`）、`--rollback-ref 6bc0742` 一次 PASS（三个不同二进制，`/tmp/r4-rehearsal-3`），演练内含前端 `build --webpack`、`typecheck`、`lint` 与 2 个浏览器用例。Android 单测/UI 截图、Windows 单测、iOS APP 目标编译由 PR CI 执行，结果见 PR。

| 编号 | 证据（测试） | 实际结果 | 结论 |
| --- | --- | --- | --- |
| R4-01 | 四平台入口单测：macOS `testWebsiteLinksPointAtConfiguredSite`、Windows `Website_links_come_from_the_build_configuration`（地址断言）；Android/iOS 入口经既有 `openWebsite` 路径 | 四平台“注册账号”“忘记密码”均指向构建配置的 `WEBSITE_BASE_URL` 下 `/register`、`/forgot-password`；APP 内未复制任何邮箱业务；新密码手动登录走既有 `/client/login`（单测：旧保存项失败后新密码登录成功并保存，同一账户，设备 UUID 不变） | 部分：地址与登录状态机 PASS；**四台真实设备/虚拟机上点击入口、完成网站流程再回 APP 登录未执行**（本机无 Android/Windows 运行环境，iOS/macOS 实机需签名） |
| R4-02 | 共享 Core 与后端：第 3 轮 `TestResetRevokesAllSessionsAndKeepsAccount`（四平台 NativeSession 撤销后 session/nodes/config/traffic 均 401 `native_session_expired`）；本轮演练中旧账户重置后两个真实 HTTP 客户端会话的 session 与 traffic 均 401 | 后端撤销与错误码在 API 层成立，四平台 Core 既有 `TerminatesSession` 路径处理该错误码 | **未执行**（真实平台）：前台/后台连接、应用重启、睡眠恢复/网络切换下的 Android 前台服务、iOS 隧道、桌面守护服务停止时刻需真实设备与签名，本轮无条件 |
| R4-03 | macOS `PasswordResetTests`（6 例）、iOS `PasswordResetTests`（4 例）、Android `SessionControllerTest` 新增 5 例、Windows `PasswordResetTests`（3 例） | 启动时旧保存密码只在线尝试 1 次 → `invalid_credentials` → 删除该项、保留邮箱，之后无重试 → 新密码手动登录成功并保存；手动输错不删正确保存项；登录途中另一处保存的新密码不被删除；静默重登在用户先退出/先登录另一账号时放弃，且只会把保存密码发给它自己的账号；启动读凭据期间用户手动登录，旧读取不再登录；退出前开始的轮询结果在退出后被丢弃。**变异验证**：把 macOS/iOS 控制器换回本轮前版本，macOS 6 例中 5 例失败（含“把 A 账号的保存密码发给表单里新输入的邮箱”）、iOS 4 例中 3 例失败（含“用户已登录 B 账号后被迟到的静默重登切回已过期的 A 账号”） | 部分：macOS/iOS 本机 PASS；Android/Windows 单测已编写，**以 CI 结果为准**；真实 Keychain/Keystore/凭据管理器上的交错未在设备上执行 |
| R4-04 | 共享 Core：`native/core/internal/managed/handoff.go` 的 `AdoptHandoff` 先在线核验会话（既有测试）；后端撤销使旧会话 401 | iOS 旧 ticket 在重置后被服务器拒绝的链路由既有 adopt 在线核验保证 | **未执行**：iOS APP/扩展独占 Core、扩展重启、APP 离开前台，Android 前台 VPN 服务，Windows/macOS 守护进程所有权均需真实设备/签名/系统服务 |
| R4-05 | 演练 `summary.json` 第 4、6 条；第 3 轮 `TestResetRefreshAndTrafficOrdering` | 两个会话已确认报告合计扣费 1,000,000 单位，重置后余额与报告行不变；重置后旧 token 的报告 401 不入账（当前与回滚二进制均如此），无重放 | 部分：服务端账本 PASS；**Core 末次确认得到 401 后不死循环、未确认尾量不计入新会话**依赖既有 Core 终止路径，本轮未在真实隧道上测量 |
| R4-06 | `flow.spec.ts`（演练，生产构建 + Chrome，1440px 与 390px）；第 2、3 轮 `register.spec.ts`、`reset.spec.ts`、`api-proxy.test.mjs`（429/503、跨标签、Cookie 清理） | 验证码注册、购买、Cookie 轮换、退出/重新登录通过；`/`、`/login`、`/register`、`/forgot-password`、`/plans` 在 390px 无横向溢出；受保护页跳登录；页面错误为 0 | 部分：桌面 Chrome 与移动视口 PASS；**真实手机浏览器与软键盘未执行** |
| R4-07 | — | 需要部署人员的 QQ SMTP 授权码与验收人员拥有的 QQ/Gmail/163 收件箱 | **未执行**（真实凭据只能由部署人员填写到 `backend/.env`，不由本轮代填） |
| R4-08 | 第 1/2 轮 `TestClientAddressTrust`、`api-proxy` 来源地址用例（分段） | 分段 PASS（见第 1、2 轮） | **未执行**：HTTPS 验收域名、证书、真实 Nginx → Next.js → Go 整链、Secure Cookie 与 SMTP 故障演练需目标环境 |
| R4-09 | `test-native.py`；`go vet`；macOS/iOS `swift test`；`test-release-rehearsal.py` ×3（见上） | 后端全部包通过；演练：旧版本数据迁移两次后原有列逐值不变、新增列为缺省值（`auth_version=1`、未验证）、17 张表集合正确；当前二进制关闭注册时旧接口请求 403 `registration_disabled` 且不建号；验证码注册浏览器闭环；旧账户（非白名单域名、未验证）邮件链接重置：token 一次性、两类会话撤销、旧密码 401、新密码可用、`auth_version=2` 且由重置补齐验证时间；全库备份恢复 17 表指纹迁移前后一致；回滚到 `c84de59`（及另测 `6bc0742`）并**开启注册**时无验证码注册 400 `verification_required`、旧密码仍 401、撤销的 token 仍 401、订单与已确认流量保留、新密码可登录 | 部分：本机部分 PASS；Android/Windows/iOS APP 目标以 CI 为准。缺省回滚目标与本轮后端生产二进制相同（`identical_binaries: current=rollback`，本轮未改后端生产代码），跨版本证据来自 `--rollback-ref 6bc0742` 一次 |
| R4-10 | 审查本轮差异与演练输出 | 差异中无真实凭据；演练的 SMTP 口令、邮件密钥、JWT、数据库口令均为每次随机生成、只存在于临时目录与进程环境；验证码/重置 token 不写入日志或 `summary.json`；演练产物仅含脱敏检查项、二进制与备份的 SHA-256 | 部分：源码差异与演练产物 PASS；**安装包公开配置与发布截图**随各平台发布另行审核，本轮未出包 |

**已知限制与交接：**

1. 本轮无法在本机执行的项目（R4-01 实机、R4-02、R4-04、R4-07、R4-08 及 R4-03/R4-05 的设备部分）均标为“未执行/部分”，按第 10 节通过标准，**Auth 完善尚未达到第 13 节最终完成定义**；需要在具备 Android 模拟器/真机、Windows、已签名 iOS/macOS、HTTPS 验收域名与真实 QQ SMTP 的环境补测后再发布对应平台。
2. 保存密码删除语义变更（见“交付”）：手动输错不再删除已保存的正确密码。若产品希望“任何一次被拒绝都清除保存项”，需要另行决定，不能与 9.2 的绑定要求同时满足。
3. 演练浏览器阶段运行的是当前 API 的 Go 测试装配而非 `cmd/api` 二进制（普通二进制只信任系统证书库，无法向本机测试 SMTP 投递；不新增生产 CA 配置项）；迁移、旧账户登录、客户端计量、回滚与恢复仍使用普通二进制。
4. `docs/deployment.md` 中“最新执行结果见 HANDOFF.md 第 25 节”的引用沿用旧文，未在本轮调整。
