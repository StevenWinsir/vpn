# AsterLink VPN：网站轻提示（Toast / Message）技术规划交接书

> 文件：handoff_message.md  
> 编写日期：2026-11-05  
> 代码基线：项目 /Users/stevenlee/Desktop/vpn，main，HEAD 6b7e48a（speed-drop fix 第 3 轮合并后）。以本次读取的 frontend/src 全部组件为准。  
> 文档性质：实施规划与验收约定。本文不代表任何功能已开发或已测试，全文不含代码示例。

## 1. 目标、范围与轮次总览

用户在网站上完成一个操作后，应当立刻得到一句“发生了什么”的反馈。这句话显示几秒后自动消失，不需要手动关闭。现状是反馈零散、缺失或重复：

- 登录成功、注册成功、退出登录后都直接跳页，**没有任何提示**（用户提出的第 1 点）。
- 有的操作用了 Mantine 通知（约 20 处，分散在 10 个文件，各自拼颜色和标题），有的操作在页面里放一个**永不消失**的绿色 Alert（账号页“密码已修改”、/admin/nodes 的“已更新”、节点代理面板），也有的操作**什么都不显示**（优惠码启用/停用、节点代理启用/停用、工单回复）。
- 账号页修改密码时，Toast 和页面内绿色 Alert 同时出现，提示重复。
- 退出登录、删除账号、返回管理员都会整页跳转（或被在线客服逻辑变成整页刷新），在跳转前弹出的提示会随页面一起消失。

本次只改网站前端（frontend）。后端、APP、计费链路都不动，也不新增 API 或 .env 参数。

采用 **2 轮**实施。每轮一个 worktree 分支和一个 PR，分支命名为 worktree-message-roundN-*，每轮记录追加到本文第 12 节。

| 轮次 | 可验收结果 | 依赖 |
| --- | --- | --- |
| 第 1 轮 | 统一提示层（toast 模块、跨整页跳转的一次性提示、全局位置/时长/数量/无障碍/深色主题）；用户侧全部提示点（登录、注册、验证码、退出、登录失效、修改密码、删除账号、返回管理员、复制推荐码/链接、工单回复）；去掉重复与常驻的成功 Alert；单元测试与 Playwright | 无 |
| 第 2 轮 | 管理侧：现有约 15 处 notifications.show 迁移到统一模块；/admin/nodes 与节点代理面板的常驻“已更新”Alert 改成 Toast；补齐缺失的成功提示（优惠码启停、节点代理启停、工单回复）；源码守卫脚本（禁止绕过统一模块直接调用）；全量回归与文档 | 第 1 轮 |

本期不做：

- 不引入新的提示库（sonner、react-hot-toast 等）。项目已经安装并挂载了 @mantine/notifications，它天然适配现有主题、深色模式和 tokens，再加一个库只会增加体积和第二套样式。
- 不把**错误**改成 Toast。表单校验错误、接口返回的业务错误继续显示在原位置（字段错误或表单顶部的红色 Alert）。原因见 D4。
- 不做“消息中心”或通知历史；不做浏览器系统通知（Notification API）或推送。
- 不为后台轮询结果弹提示（总览、节点、工单列表、推荐页的定时刷新、窗口聚焦刷新）。
- 不做“收到客服新回复”的实时弹窗；未读回复继续用侧边栏角标表达（handoff_ticket.md 11.1）。
- 不改页面文案措辞以外的视觉设计；管理侧沿用原有的运维措辞，只换呈现方式。

## 2. 已核实的代码基础与必须衔接的位置

### 2.1 提示基础设施

| 位置 | 当前事实 | 对本次的影响 |
| --- | --- | --- |
| frontend/package.json | 已依赖 @mantine/notifications（锁定 8.3.18），与 @mantine/core 8 同版本线 | 直接复用，无需新增依赖 |
| src/app/layout.tsx:4 | 已全局引入 @mantine/notifications/styles.css | 样式已就位 |
| src/components/providers.tsx:177 | 在 MantineProvider 内挂载了唯一的通知容器，位置 top-right，其余全部是默认值（默认 4 秒自动关闭，最多同时 5 条） | 全局参数在这里统一调整；容器位于根 Providers，客户端路由跳转（router.push/replace）时不会卸载，所以跳转前弹出的提示能在新页面继续显示 |
| src/components/providers.tsx:161 | onSessionEnded 监听器：任何受保护请求在刷新后仍然 401，就把用户清空，RequireAuth 随后跳到 /login | “登录已失效”提示的挂点 |
| src/lib/api.ts | message()、APIError 统一了错误文字；没有任何提示相关的封装 | 新模块与它并列 |
| Mantine Notification 组件 | 根节点默认带 role="alert"（屏幕阅读器会打断式朗读）；鼠标悬停时暂停自动关闭计时；带关闭按钮 | 成功/信息类需要改成 role="status"，见 D10；实施时先确认传入的 role 能覆盖默认值 |

### 2.2 现有调用（全部 notifications.show）

用户侧：
- site-shell.tsx:160 返回管理员失败（红）；site-shell.tsx:229 退出失败（红）。
- account-view.tsx:117 密码已修改（同时 account-view.tsx:179 还有一个常驻的绿色 Alert，重复）。
- plans-view.tsx:240 测试环境直购开通（仅测试环境出现，行内带 copy-guard 标记）。
- tickets-view.tsx:441、:458 工单已提交；tickets-view.tsx:844 工单已关闭。

管理侧：
- console-users.tsx:857 密码已修改、:940 套餐已保存、:1060 订阅已结束、:1145 账号已删除。
- console-orders.tsx:738 已确认到账、:795 订单已关闭。
- console-plans.tsx:146 套餐已删除、:399 与 :477 两处设置保存。
- console-coupons.tsx:313 优惠码已创建/已保存、:498 已删除/已停用并隐藏。
- console-announcements.tsx:293 草稿已保存、:389 发布/撤回等（标题传入，message 为空串）。
- console-tickets.tsx:673 工单已关闭。

这些调用各自写 color 和 title，没有统一的时长、图标、无障碍属性和去重。

### 2.3 页面内常驻的成功提示（需要手动关闭或永不消失）

| 位置 | 现状 | 处理 |
| --- | --- | --- |
| account-view.tsx:179 | 修改密码成功后的绿色 Alert，直到下次提交才消失；与 Toast 重复 | 第 1 轮删除，只保留 Toast |
| referral-view.tsx:222 | “推荐码已复制/邀请链接已复制/无法自动复制……”的行内文字，2.5 秒后清空 | 第 1 轮改为 Toast；按钮图标变勾的反馈保留 |
| referral-view.tsx:356–360 | 空状态里的“复制邀请链接”，失败时静默（catch 里什么都不提示） | 第 1 轮改为与上面同一套复制提示，失败给出可操作的提示 |
| admin-nodes-view.tsx:517 | 节点组/服务器保存、启停、清冷却、删除后显示“已更新”绿色 Alert，需手动关闭 | 第 2 轮改为 Toast |
| admin-agents-panel.tsx:218 | 节点代理保存/移除后的绿色 Alert，需手动关闭 | 第 2 轮改为 Toast |
| console-plans.tsx:65 | 套餐保存后的多行明细 Alert（服务端年付价、立省金额等），带关闭按钮，测试 plan-saved 依赖 | **保留**。它是需要核对的计算结果，不是一句话反馈；见 D7 |

### 2.4 会整页跳转的操作（提示必须跨页面传递）

| 位置 | 操作 | 跳转方式 |
| --- | --- | --- |
| account-view.tsx:370 | 删除账号成功 | window.location.replace('/')，整页 |
| site-shell.tsx:154 | 返回管理员成功 | window.location.replace('/admin/console/users')，整页 |
| site-shell.tsx:157 | 返回管理员时会话已失效 | window.location.replace('/login')，整页 |
| site-shell.tsx:227 | 退出登录 | router.replace('/login') 是客户端跳转；但如果本页已经加载过在线客服（Crisp），support-widget.tsx:76–86 会把下一次跳转变成整页刷新（handoff_ui.md 第 4 轮决定），提示会丢失 |
| console-users.tsx:1104 | 管理员开始只读查看用户 | 整页跳到 /dashboard；顶部横幅已经说明状态，不另加提示 |

### 2.5 测试与守卫的约束

- 现有 Playwright 用例大量使用 getByRole('alert') 和 getByText 定位页面内错误（account.spec、payment.spec、user-nodes.spec、plans-upgrade.spec 等），home.spec.ts:239 断言首页没有带文字的 alert。如果成功提示保持 Mantine 默认的 role="alert"，这些定位会多匹配到 Toast，触发严格模式冲突或误判。
- account.spec.ts:132 用 getByRole('status') 断言页面内的“密码已修改”；referral.spec.ts:251、:254、:514 用 data-testid="copy-notice" 断言复制提示。第 1 轮改动这两处时同步更新用例。
- admin-console.spec.ts:183 用 getByText('已确认到账')（来自现有 Toast），迁移后文字不变即可通过。
- copy-guard（scripts/copy-guard.test.mjs，源码扫描）和 ui-copy.spec / home.spec（运行时页面文字扫描）禁止用户页出现“开发、测试、模拟、后端、服务端、联系管理员”等词和绝对化用语。用户页的 Toast 文案同样受约束，例如返回管理员的提示不能出现“模拟”。管理侧（console、admin-*）不在扫描范围内。
- theme.test.mjs 禁止在 globals.css 的 tokens 区块之外写颜色字面量。Toast 的样式调整只能使用 --al-* tokens。
- 所有 Playwright 配置都把 Crisp 域名解析为 NOTFOUND，测试里 supportInjected() 始终为 false，所以“退出时 Crisp 触发整页刷新”的分支在 CI 中不会自然出现，需要专门构造（见第 9 节）。

## 3. 关键决策（默认值，用户可在第 1 轮开工前推翻）

| 编号 | 决策 | 理由 |
| --- | --- | --- |
| D1 | 复用 @mantine/notifications，新增一个统一封装模块 src/lib/toast.ts，全站只通过它弹提示 | 零新依赖；主题、深色模式、字体自动一致；一处改参数全站生效 |
| D2 | 四种语气：成功（teal，勾图标）、信息（blue，i 图标）、警告（orange，感叹号图标）、错误（red，叉图标）。默认时长：成功 3 秒、信息 4 秒、警告 5 秒、错误 6 秒；管理侧带后果说明的长句提示 8 秒。悬停暂停计时，保留右上角关闭按钮（可提前关，但不需要关） | 短句 3 秒足够读完；越重要、越长的提示停留越久；悬停暂停照顾读得慢的用户 |
| D3 | 位置改为 **top-center**（顶部居中，类似 Ant Design 的 Message），同屏最多 3 条，新的在上；手机宽度下左右留 12px 边距、宽度自适应 | 用户要求的是轻提示/消息提示；顶部居中在桌面和手机上都最显眼，不与右下角的在线客服按钮冲突；限 3 条避免连点刷屏 |
| D4 | **只有成功和信息走 Toast**。表单校验和接口错误继续显示在原位置（字段错误或表单顶部红色 Alert），不自动消失。只有“没有地方显示”的错误用错误 Toast：退出失败、返回管理员失败、复制失败以外的少数全局操作 | 错误需要用户读完并改正，几秒后消失反而更糟；原位显示的错误与现有测试完全兼容 |
| D5 | 跨整页跳转的提示用 sessionStorage 传递一次性“下一页提示”：只写一个**固定白名单里的键名**和写入时间，新页面挂载时读取、立即删除，超过 30 秒作废，键名不在白名单内则丢弃。不经过 URL，不存任何用户输入的文字 | 不能用 URL 参数（会被分享、留在历史记录里，还可被构造成钓鱼文案）；白名单键名对应固定文案，杜绝注入；30 秒防止一个陈旧的提示在很久以后的刷新中出现 |
| D6 | 去重：每个提示点使用固定 id（例如 copy-referral-link、session-ended）。同 id 的提示已在屏幕上时更新内容并重新计时，不再叠加 | 用户连点“复制”五次、或多个并发请求同时 401，只应看到一条 |
| D7 | 已有醒目持久状态区的结果**不加** Toast：找回密码“邮件已发送”的绿色 Alert（包含 30 分钟有效期说明）、重置密码完成页、支付结果页、升级/购买弹窗中的“价格已更新”和“优惠码已应用”、管理后台的套餐保存明细、订单“查询网关”结果 | 这些是需要阅读或核对的状态，不是一句话反馈；重复提示只会制造噪声 |
| D8 | 后台轮询、窗口聚焦和网络恢复触发的刷新一律不提示；页面上的“刷新/同步节点”按钮成功时也不提示（按钮本身有 loading，节点页已有“最近同步 时间”状态行），只有失败沿用现有的页面内错误 | 定时出现的提示会让用户困惑“我做了什么” |
| D9 | 文案规范：标题不超过 10 个汉字、不带句号；正文可选，不超过 30 个汉字，一句话说明结果或下一步；不出现技术词；可以带上下文中的安全字段（工单号、套餐名、邮箱），由 React 转义。**注册成功的提示不提推荐码**（推荐码由后端静默绑定或忽略，handoff_coupon.md 12.1） | 与 copy-guard 及商业文案规范一致；不泄露推荐码是否有效 |
| D10 | 无障碍：成功/信息 Toast 使用 role="status"（礼貌朗读），警告/错误使用 role="alert"；每条 Toast 带 data-testid="toast" 和 data-tone（success/info/warning/error），供测试定位；尊重系统“减少动态效果” | 不打断屏幕阅读器；成功提示不污染现有 getByRole('alert') 断言 |
| D11 | 登录失效提示：只在“服务器结束了会话”时出现（onSessionEnded，或 /auth/me 在已登录状态下返回 401），文案“登录已失效，请重新登录”，警告语气，固定 id。用户自己退出、删除账号、重置密码时不出现 | 用户在别处改了密码或会话到期后突然被送回登录页，需要一句解释 |
| D12 | 退出登录：本页未加载过在线客服时直接弹“已退出登录”；已加载过（supportInjected() 为真，接下来会整页刷新）时改写一次性下一页提示，由刷新后的 /login 显示 | 两种情况都只显示一次，不会刷新前后各闪一次 |
| D13 | 管理侧（第 2 轮）只改呈现方式，原有标题和正文措辞全部保留 | handoff_ui.md 约定管理侧沿用运维措辞 |

## 4. 统一提示层设计（第 1 轮）

### 4.1 模块 src/lib/toast.ts

对外只提供：
- 四个语气函数：成功、信息、警告、错误。参数为标题、可选正文、可选选项（id、时长覆盖）。
- 一个“下一页提示”函数：参数为白名单键名。只写 sessionStorage，不立即显示。
- 一个“读取下一页提示”函数：供挂载组件调用，返回白名单键名或空；内部完成读取、删除、时效检查和白名单检查；sessionStorage 不可用（隐私模式、被禁用）时静默返回空。

内部统一处理：颜色、图标、默认时长（D2）、role 与 data-testid/data-tone（D10）、同 id 去重与重新计时（D6）、正文为空时不渲染正文（现有 console-announcements.tsx:389 传了空串，迁移后不再出现空行）。

白名单键名与固定文案：

| 键名 | 标题 | 正文 | 语气 |
| --- | --- | --- | --- |
| signed-out | 已退出登录 | 无 | 成功 |
| account-deleted | 账号已删除 | 感谢你的使用 | 成功 |
| returned-admin | 已返回管理员账号 | 无 | 成功 |
| session-ended | 登录已失效 | 请重新登录 | 警告 |

### 4.2 挂载

在 providers.tsx 的通知容器旁新增一个只在浏览器端运行的小组件：首次挂载时读取一次下一页提示并显示。它位于根 Providers，所以首页、登录页、工作台、管理后台都能接收。

### 4.3 全局容器参数

providers.tsx:177 的通知容器改为：位置 top-center、同屏上限 3、默认时长 3 秒、层级高于 Modal（Mantine 默认通知层级已高于弹窗，实施时确认公告弹窗 AnnouncementGate 打开时 Toast 仍在其上方可见）。globals.css 中只用现有 tokens 微调：背景 --al-surface、边框 --al-border、阴影 --al-shadow-lg、手机宽度下的边距；在浅色和深色下各截一次图核对对比度。

### 4.4 与在线客服的关系

Crisp 按钮在右下角，层级低于通知（globals.css:317 注释已有约定）。顶部居中的 Toast 与它没有重叠。

## 5. 提示点清单

优先级：P1 本次必须做；P2 本次做，体验加分；—— 表示明确不加（理由见“说明”）。

### 5.1 认证（第 1 轮）

| 编号 | 位置 | 触发 | 提示 | 实现要点 | 优先级 |
| --- | --- | --- | --- | --- | --- |
| A1 | auth-form.tsx:108 登录成功 | 登录接口成功、跳转前 | 成功：“登录成功”，正文“欢迎回来，{姓名}” | 在 router.replace 之前调用；客户端跳转，容器不卸载，提示在 /dashboard 或 next 指向的页面继续显示 | P1 |
| A2 | auth-form.tsx:357 注册成功 | 注册接口成功、跳转前 | 成功：“注册成功”，正文“欢迎加入 {appName}” | 不提推荐码（D9） | P1 |
| A3 | auth-form.tsx:296 发送注册验证码 | 验证码接口成功 | 成功：“验证码已发送”，正文“请查收邮件” | 页面内那行倒计时和有效期说明保留（它是动态信息）；固定 id，重发时更新 | P1 |
| A4 | site-shell.tsx:227 退出登录 | logout 成功 | 成功：“已退出登录” | 按 D12 分两种路径；退出失败的现有红色 Toast 迁移到统一模块 | P1 |
| A5 | providers.tsx:161 登录失效 | onSessionEnded；/auth/me 在已登录状态下 401 | 警告：“登录已失效”，正文“请重新登录” | 固定 id 去重；若本页加载过 Crisp 则写下一页提示 session-ended；site-shell.tsx:157（返回管理员时 401 整页去 /login）同样写下一页提示 | P1 |
| A6 | password-reset.tsx:89 找回密码邮件已发送 | —— | 不加 | 页面已有醒目的绿色 Alert 并含 30 分钟有效期说明（D7） | —— |
| A7 | password-reset.tsx:277 重置密码完成 | —— | 不加 | 完成页本身就是结果（D7）；用户随后登录时会看到 A1 | —— |

### 5.2 账号与会话（第 1 轮）

| 编号 | 位置 | 触发 | 提示 | 实现要点 | 优先级 |
| --- | --- | --- | --- | --- | --- |
| B1 | account-view.tsx:116–121、:179 修改密码 | 成功 | 成功：“密码已修改”，正文“其他设备上的登录已退出” | 现有 Toast 迁移；删除 :179 的常驻绿色 Alert 及 done 状态；更新 account.spec.ts:132 | P1 |
| B2 | account-view.tsx:370 删除账号 | 成功 | 成功：“账号已删除”，正文“感谢你的使用” | 整页跳首页，写下一页提示 account-deleted；首页由根 Providers 接收 | P1 |
| B3 | site-shell.tsx:154 返回管理员 | 成功 | 成功：“已返回管理员账号” | 整页跳转，写下一页提示 returned-admin；用户页文案不得出现“模拟”；失败的现有红色 Toast 迁移 | P1 |
| B4 | announcement-gate.tsx “知道了” | —— | 不加 | 弹窗关闭本身就是反馈；若队列里还有下一条会紧接着出现 | —— |
| B5 | theme-toggle.tsx 切换外观 | —— | 不加 | 外观立即变化，所见即所得 | —— |

### 5.3 购买、订单、推荐、工单（第 1 轮）

| 编号 | 位置 | 触发 | 提示 | 实现要点 | 优先级 |
| --- | --- | --- | --- | --- | --- |
| C1 | referral-view.tsx:185–195 复制推荐码 / 邀请链接 | 复制成功 | 成功：“推荐码已复制” / “邀请链接已复制” | 固定 id copy-referral，连点只保留一条；按钮图标变勾保留；删除 :222 行内文字；更新 referral.spec.ts:251、:254 | P1 |
| C2 | 同上，复制失败 | 没有剪贴板权限或非安全上下文 | 警告：“无法自动复制”，正文“已选中邀请链接，请手动复制” | 选中输入框的现有行为保留；更新 referral.spec.ts:514 | P1 |
| C3 | referral-view.tsx:356–360 空状态“复制邀请链接” | 成功 / 失败 | 同 C1 / C2 | 复用同一个复制函数，修掉失败时静默的问题 | P1 |
| C4 | tickets-view.tsx:808–830 用户回复工单 | 发送成功 | 成功：“回复已发送” | 新消息同时出现在对话中，提示只是确认；工单提交/关闭的现有 Toast 迁移（tickets-view.tsx:441、:458、:844） | P2 |
| C5 | plans-view.tsx:240 测试环境直购 | 成功 | 原样迁移 | 保留 copy-guard 标记，仅测试环境出现 | P1（迁移） |
| C6 | purchase-modal / upgrade-modal “去支付” | —— | 不加 | 成功即整页离开去支付网关；价格变化、优惠码结果已在弹窗内显示（D7） | —— |
| C7 | pay-result-view.tsx 支付结果 | —— | 不加 | 整页就是结果展示，状态翻转会立即反映在页面主体（D7） | —— |
| C8 | 总览、节点、订单、推荐、工单页的“刷新/同步” | —— | 不加 | D8 | —— |

### 5.4 管理侧（第 2 轮）

| 编号 | 位置 | 触发 | 提示 | 实现要点 | 优先级 |
| --- | --- | --- | --- | --- | --- |
| M1 | 第 2.2 节列出的全部管理侧 notifications.show | 原有 | 措辞不变（D13） | 迁移到统一模块；console-announcements.tsx:389 的空正文不再渲染 | P1 |
| M2 | admin-nodes-view.tsx 节点组保存/创建、启用/停用、服务器保存/导入、清除冷却、删除（:334、:356、:435、:450、:471） | 成功 | 成功，标题按操作拆成短句（如“节点组已保存”“已导入服务器”“冷却已清除”“已删除”），正文沿用原句，时长 8 秒 | 删除 :517 的常驻“已更新”Alert 和 notice 状态；“操作失败”红色 Alert 保留在页面内 | P1 |
| M3 | admin-agents-panel.tsx 节点代理保存（:131）、移除（:177） | 成功 | 成功：“节点代理已保存” / “节点代理已移除”，正文沿用原句，8 秒 | 删除 :218 的常驻 Alert | P1 |
| M4 | admin-agents-panel.tsx:143 节点代理启用/停用 | 成功 | 成功：“节点代理已启用” / “已停用” | 目前成功时完全没有反馈 | P1 |
| M5 | console-coupons.tsx:117 优惠码启用/停用开关 | 成功 | 成功：“优惠码已启用” / “已停用”，正文为优惠码 | 目前成功时完全没有反馈 | P1 |
| M6 | console-tickets.tsx:398–412 管理员回复工单 | 成功 | 成功：“回复已发送”，正文为工单号 | 与 C4 一致 | P2 |
| M7 | console-plans.tsx:65 套餐保存明细 | —— | 不改 | D7；plan-saved 用例依赖 | —— |
| M8 | console-orders.tsx:432 查询网关 | —— | 不改 | 结果是需要阅读的信息，已在详情内显示 | —— |
| M9 | console-users.tsx:1104 开始只读查看 | —— | 不加 | 整页跳转后顶部横幅已说明状态 | —— |

## 6. 兼容与风险

- **role 覆盖**：若实测发现 Mantine 8.3 的 Notification 不允许覆盖 role="alert"，改为在成功/信息 Toast 内部放一个 role="status" 的正文容器，并让测试只用 data-testid="toast" 定位；不得回退到全部 alert。
- **严格模式定位**：Toast 渲染在 body 的 Portal 里，不在 main 内。用例用 main(page) 限定范围的不受影响；用 page.getByText 全页查找的，迁移后若与 Toast 同文案，需改为按 testid 或 role 定位。逐个跑完相关 spec 再提交。
- **首页**：删除账号后的 account-deleted 提示出现在首页；home.spec.ts:239 只统计 role="alert"，成功 Toast 是 status，不受影响。account.spec 的删除用例要补一条“首页出现账号已删除”的断言。
- **AnnouncementGate**：登录后跳到工作台可能立刻弹出未读公告。Toast 层级须高于公告弹窗，且公告的焦点陷阱不受 Toast 影响（Toast 不抢焦点）。
- **只读查看会话**：管理员只读查看用户期间，服务器拒绝一切修改，用户页的成功提示不会出现；返回管理员的提示由 B3 处理。
- **旧后端组合**：本次纯前端，不依赖新字段；与任何已发布后端组合行为相同。

## 7. 性能与体积

不新增依赖；统一模块只引用已在首屏包里的 @mantine/notifications 和 @tabler/icons-react 中已使用的图标，增量应在 1 KB（gzip）量级。按 handoff_ui.md 的方法在浏览器里实测 /login 和 /dashboard 的 JS 总量，与基线 6b7e48a 对比并记录。

## 8. 两轮实施与逐轮验收

### 第 1 轮：统一提示层与用户侧

范围：第 4 节全部；第 5.1、5.2、5.3 节中 P1、P2 项；用户侧现有 notifications.show 全部迁移；第 2.3 节中第 1 轮的三项。

验收（开发方执行，PASS/FAIL/BLOCKED 如实记录）：

| 编号 | 内容 |
| --- | --- |
| M1-01 | 登录成功后在目标页看到“登录成功 / 欢迎回来，{姓名}”，约 3 秒后自动消失，无需点击；带 next 参数时同样出现 |
| M1-02 | 注册成功后在 /dashboard 看到“注册成功”；带推荐码与不带推荐码文案一致 |
| M1-03 | 发送验证码成功出现提示；连续重发只保留一条 |
| M1-04 | 退出登录在 /login 看到“已退出登录”，只出现一次；构造 supportInjected 为真（测试桩）时，刷新后的 /login 上出现且只出现一次 |
| M1-05 | 在另一个上下文修改密码后，本页下一次请求被送回 /login 并看到“登录已失效”，并发多个 401 时只有一条 |
| M1-06 | 修改密码只出现 Toast，页面内不再有常驻绿色 Alert |
| M1-07 | 删除账号后首页出现“账号已删除”；30 秒后再手动刷新首页不再出现 |
| M1-08 | 返回管理员后在 /admin/console/users 出现“已返回管理员账号”；页面文字不含 copy-guard 禁词 |
| M1-09 | 复制推荐码、复制邀请链接、空状态复制都出现对应提示，连点 5 次屏幕上只有 1 条；拒绝剪贴板权限时出现“无法自动复制”且邀请链接被选中 |
| M1-10 | 成功 Toast 的 role 为 status，警告/错误为 alert；带 data-testid="toast" 与 data-tone |
| M1-11 | 悬停时不消失，移开后继续计时；关闭按钮可提前关闭 |
| M1-12 | 同屏最多 3 条；手机宽度（375px）下不溢出、不遮挡顶部导航的关键按钮超过提示时长 |
| M1-13 | 浅色、深色两种外观下截图，文字对比度不低于 4.5:1 |
| M1-14 | 下一页提示的单元测试：白名单外键名、超时、JSON 损坏、sessionStorage 不可用时都静默丢弃，读取后立即删除 |
| M1-15 | 轮询、聚焦刷新、手动刷新均不出现提示 |
| M1-16 | npm run lint、typecheck、build、test:copy、test:theme 通过；account.spec、referral.spec、register.spec、flow.spec、home.spec、tickets.spec、ui-copy.spec 及全量 payment fixture 套件通过 |

测试资产：新增 scripts/toast.test.mjs（node --test，纳入 package.json 的 test:toast 和 CI 的 web 步骤，位置与 test:tickets 相邻）；新增 tests/toast.spec.ts（使用新的专用测试账号，按 handoff_ticket 的经验，payment fixture 账号须创建为最早的行）；更新 account.spec.ts:132、referral.spec.ts:251/254/514。

### 第 2 轮：管理侧迁移、守卫与回归

范围：第 5.4 节；守卫脚本；文档。

验收：

| 编号 | 内容 |
| --- | --- |
| M2-01 | 管理侧原有提示全部经统一模块弹出，标题和正文与迁移前一致（逐条对照第 2.2 节清单） |
| M2-02 | /admin/nodes 的每种成功操作出现 8 秒 Toast，页面内不再有常驻“已更新”Alert；失败仍在页面内显示红色 Alert |
| M2-03 | 节点代理保存、移除、启用、停用都有成功提示 |
| M2-04 | 优惠码开关启用/停用有成功提示 |
| M2-05 | 管理员回复工单有成功提示 |
| M2-06 | 守卫脚本：除 src/lib/toast.tsx 与 src/components/toaster.tsx 外，src 下任何文件直接引用 @mantine/notifications 都会让测试失败（纳入 CI；第 1 轮已在 scripts/toast.test.mjs 里对用户侧文件做了同样检查，第 2 轮扩展到整个 src） |
| M2-07 | admin-console.spec、admin-console-2.spec、admin-console-plans.spec、admin-nodes.spec 通过 |
| M2-08 | 全量本地回归：payment fixture 套件、catalog 套件、test:api-proxy（本机需指定系统 Chrome）全部通过；CI 全绿，并确认 web 相关 job 实际运行 |
| M2-09 | 体积对比记录（第 7 节） |
| M2-10 | docs 中新增一页提示规范：何时用 Toast、何时用页面内 Alert、文案长度、时长、白名单键名的新增流程 |

## 9. 测试方法要点

- 自动消失的断言：显示后先断言可见，再等待“默认时长 + 1 秒”断言消失；悬停用例在悬停期间等待超过时长后仍可见。
- 退出时 Crisp 整页刷新的分支：CI 里 Crisp 不可达。用页面脚本把“本页已加载客服”的状态桩为真（或在测试构建中暴露一个只在测试环境生效的开关，带 copy-guard 标记），验证下一页提示路径；真实 Crisp 环境留给用户实测 U-61。
- 剪贴板：Playwright 授予/不授予 clipboard-write 权限分别验证 C1、C2。
- 登录失效：在第二个浏览器上下文修改密码，回到第一个上下文触发任意请求。

## 10. 用户部署后的实测（接在现有 U 系列之后）

| 编号 | 内容 |
| --- | --- |
| U-61 | 线上开启在线客服时，在 /dashboard 打开过客服后退出登录，/login 上只出现一次“已退出登录” |
| U-62 | 手机浏览器（iOS Safari、安卓 Chrome）登录、复制邀请链接，提示位置、宽度、自动消失正常 |
| U-63 | 深色外观下各类提示可读 |
| U-64 | 在 APP 内修改密码或在另一台电脑重置密码后，网站页面出现“登录已失效”并回到登录页 |

## 11. 完成定义

两轮 PR 合并到 main；第 8 节验收全部 PASS（或如实记为 BLOCKED 并说明原因）；CI 全绿；源码中不再有绕过统一模块的通知调用；用户页不再有需要手动关闭或永不消失的成功提示（第 2.3 节标注“保留”的除外）；本文第 12 节记录每轮的分支、PR、提交和证据位置。

## 12. 实施记录

（每轮完成后追加：日期、分支、PR、合并提交、验收结果表、偏差与经验。）

### 第 1 轮：统一提示层与用户侧（2026-11-05，分支 worktree-message-round1-toast，PR 见合并记录）

基线 6b7e48a。改动：

- 新增 src/lib/toast-flash.ts（纯逻辑：白名单键名与固定文案、sessionStorage 读写、30 秒时效，无 React/Mantine 依赖，供 node 单元测试直接导入）。
- 新增 src/lib/toast.tsx（四种语气、默认时长、图标、role、data-testid="toast" 与 data-tone、同 id 去重并重新计时、showFixedToast / toastAfterNavigation / toastAcrossReload / showNextPageToast）。
- 新增 src/components/toaster.tsx（取代 providers.tsx 里的 Notifications：top-center、limit 3、默认 3 秒、宽 420、系统“减少动态效果”时无过渡；挂载时显示上一页留下的提示）。
- providers.tsx：signedIn 记录当前是否有账号登录；onSessionEnded 与 reload() 的 401 只在有账号登录时显示“登录已失效”（加载过客服时改走下一页提示）；用户主动退出、删除账号、重置密码先清空用户，不会触发。
- 用户侧提示点：登录、注册、验证码（auth-form.tsx）；退出、返回管理员成功/失效/失败（site-shell.tsx）；修改密码、删除账号（account-view.tsx，删除了常驻的绿色 Alert 与 done 状态）；复制推荐码/邀请链接/空状态复制（referral-view.tsx，删除行内 copy-notice 文字；空状态按钮失败时不再静默，会选中上方邀请链接框；空状态按钮文字不再变成“邀请链接已复制”，只换勾图标，避免与 Toast 同文案）；工单提交/关闭迁移、用户回复新增“回复已发送”（tickets-view.tsx）；测试环境直购迁移（plans-view.tsx，保留 copy-guard 标记）。
- globals.css 末尾新增 .al-toasts .al-toast 规则，只用 --al-surface / --al-border / --al-shadow / --al-text / --al-text-2。
- 后端测试夹具 payment_acceptance_test.go 新增 toastroot（管理员）、toasta、toastb、toastdel，作为最早的行（控制台用户列表最新 20 条不变）。
- CI web 步骤新增 npm run test:toast；playwright.payment.config.ts 纳入 toast.spec.ts。

验收（本机 macOS，私有 PostgreSQL + 本地网关假件）：

| 编号 | 结果 | 证据 |
| --- | --- | --- |
| M1-01 | PASS | toast.spec 第 1 例：登录后 /dashboard 出现“登录成功 / 欢迎回来，Pay toasta”，约 3 秒自动消失；next 只接受 /pay/result?order=…（项目既有规则，见 next-path.ts），用该地址验证提示跟随到目标页 |
| M1-02 | PASS | referral.spec 的 register()：每次注册都断言“注册成功 / 欢迎加入”，且不含“推荐”（带推荐码与不带推荐码的注册都经过这一步） |
| M1-03 | PASS（部分） | referral.spec 的 register() 断言“验证码已发送 / 请查收邮件”；“连续重发只保留一条”未在浏览器中单独构造（重发有冷却），由同 id 去重机制与 M1-09 的去重用例间接覆盖 |
| M1-04 | PASS | 未加载客服：toast.spec 第 1 例，/login 上恰好 1 条且 sessionStorage 已清空；加载过客服（假 Crisp，整页刷新）：support-widget.spec 退出用例新增断言，刷新后的 /login 上恰好 1 条，0.5 秒后仍为 1 条 |
| M1-05 | PASS | toast.spec 第 3 例：另一浏览器修改密码后，本页点“节点列表”被送回 /login，恰好 1 条“登录已失效 / 请重新登录”（warning）；重新登录后不残留 |
| M1-06 | PASS | toast.spec 第 3 例与 account.spec：只出现 Toast，main 内没有“密码已修改”文字，Toast 消失后也没有 |
| M1-07 | PASS | toast.spec 第 4 例：首页恰好 1 条“账号已删除 / 感谢你的使用”，存储已清空，刷新后不再出现；support-widget.spec 的客服已加载删除用例也新增同样断言 |
| M1-08 | PASS | toast.spec 第 5 例：返回 /admin/console/users 后恰好 1 条“已返回管理员账号”，不含“模拟”；test:copy 通过 |
| M1-09 | PASS | referral.spec R2-01：复制推荐码、复制邀请链接、再连点 4 次、空状态按钮，屏幕上始终只有 1 条；R2-06：剪贴板拒绝时出现“无法自动复制 / 已选中邀请链接，请手动复制”（warning），邀请链接被选中 |
| M1-10 | PASS | 成功为 role="status"、data-tone="success"，getByRole('alert') 不匹配成功提示；登录失效为 warning（role="alert"） |
| M1-11 | PASS | toast.spec 第 1 例：悬停 4.5 秒仍可见，移开后 3 秒内消失；关闭按钮立即关闭 |
| M1-12 | PASS（部分） | 375px 宽下浅色与深色两次登录，Toast 左右各留 ≥8px、不越出顶部（截图 toast-375-light/dark）；“同屏最多 3 条”只由 Toaster 的 limit={3} 配置保证，未在浏览器中构造 4 条同时出现 |
| M1-13 | PASS | toast.spec 第 2 例计算标题与正文对 Toast 背景的对比度，浅色、深色均 ≥ 4.5:1 |
| M1-14 | PASS | scripts/toast.test.mjs 7/7（白名单外键名、原型链键名、超时、未来时间、各种损坏 JSON、存储缺失或抛错、文案长度与禁词、用户侧文件不直接调用 Mantine）；toast.spec 第 6 例在浏览器中复验 |
| M1-15 | PASS | toast.spec 第 1 例：点“刷新”和触发窗口 focus 后 1.5 秒内没有任何提示 |
| M1-16 | PASS | lint、typecheck、build（--webpack）、test:copy、test:theme、test:toast 及其余 8 个 node 单元套件通过；payment fixture 全量 112 passed / 0 failed（10.0 分钟）；新增断言后 referral.spec + tickets.spec 复跑 15 passed；test:api-proxy 17/17（系统 Chrome）；go vet ./internal/api/ 通过 |

偏差：

- 模块文件名为 src/lib/toast.tsx（图标需要 JSX），纯逻辑拆到 src/lib/toast-flash.ts；第 8 节 M2-06 已同步改名。
- 手机宽度未按 4.3 写自定义边距：Mantine 容器本身是 calc(100% - 32px)，宽度变量又由组件以内联样式写入，自定义规则无法覆盖也无必要。
- 工单回复提示（C4，原 P2）本轮已做；tickets.spec 用户回复步骤新增断言。
- 未跑 catalog 套件（scripts/test-node-catalog.py）：本轮未改节点页与节点管理页；第 2 轮改 /admin/nodes 时必须跑。
- 第 7 节的体积对比留到第 2 轮（M2-09）一起做。

经验：

- Mantine 8.3：notifications.show 遇到已存在的 id 会被忽略；update 不会重置计时（计时只在 autoClose 变化时重启），所以重复提示时让 autoClose 在 n 与 n+1 毫秒之间交替来重新计时。
- Notification 组件的 role 可以通过数据对象覆盖（role || "alert"），data-* 属性也会透传到根节点。
- 测位置要等入场动画结束（约 250ms），否则 y 为负。
- 夹具新账号必须加在最早的那一组（upg* 之前），否则 admin-console-2 在控制台第一页找不到 kyle。

第 1 轮合并记录：PR StevenWinsir/vpn#114，2026-11-05 squash 合并为 9f4d845，CI 16/16 全绿；本地 main 已快进，分支（本地与远端）和 worktree 已删除。

### 第 2 轮：管理侧迁移、守卫与回归（2026-11-05，分支 worktree-message-round2-admin，PR 见合并记录）

基线 9f4d845。改动：

- console-users / orders / plans / coupons / announcements / tickets 六个文件共 14 处 notifications.show 全部迁移到 toast.success，标题和正文逐字不变（含原有句号）。其中带后果说明的 4 处（用户密码已修改、账号已删除、升级设置已保存、全局年付折扣已保存）用 8 秒（toastDurations.note，本轮新增）；公告操作原来传的空正文现在不再渲染空行。
- admin-nodes-view.tsx：删除常驻的“已更新”Alert 与 notice 状态。节点组创建/保存/启用/停用/删除、服务器导入/保存/删除、清除冷却都改为 8 秒 Toast：标题是按操作拆出的短句，正文保留原句。“操作失败”红色 Alert 不变。
- admin-agents-panel.tsx：删除常驻 Alert；节点代理保存、移除改为 8 秒 Toast；启用/停用开关新增成功提示（M4，原来没有任何反馈）。
- 整个 /admin/nodes 页（含节点代理面板）共用一个提示 id admin-nodes，新结果替换上一条，不叠加（偏差，见下）。
- console-coupons.tsx：列表里的启用/停用按钮新增成功提示（M5）；console-tickets.tsx：管理员回复新增“回复已发送 / 工单号”（M6）。
- scripts/toast.test.mjs：守卫扩展到整个 src（M2-06）。除 lib/toast.tsx 与 components/toaster.tsx 外，任何文件以 from / import() / require() 引入 @mantine/notifications 都会失败；根布局的样式表引入不算。守卫同时断言这两个允许的文件确实被识别到。用一个临时探针文件验证过守卫会失败，探针已删除。
- 新增 docs/web-messages.md（M2-10）：何时用 Toast、何时用页面内 Alert、何时什么都不显示，以及调用方式、时长、id、文案规范、跨整页提示的白名单与新增流程、全站提示点清单、测试位置。README 功能列表加一行指向它。
- 用例：admin-console-2.spec 断言“优惠码已创建”并走一遍停用→启用；tickets.spec 断言管理员“回复已发送”；admin-nodes.spec 第 1 例断言节点组创建/保存、服务器导入，以及页面上只有一条结果提示、没有“已更新”；第 2 例在已停用的 Protocol-Fixtures 组里导入一台停用的服务器 Agent-Probe，依次登记、停用、启用、移除节点代理，再删除服务器和节点组，每一步断言对应提示。整个过程不进入在线目录，不影响后续 Mihomo 实流量验收。

验收（本机 macOS）：

| 编号 | 结果 | 证据 |
| --- | --- | --- |
| M2-01 | PASS | 迁移 diff 逐条对照第 2.2 节：14 处标题和正文逐字一致；admin-console.spec 的“已确认到账”、admin-console-2.spec 的“优惠码已创建”通过 |
| M2-02 | PASS | admin-nodes.spec 第 1、2 例：每种成功操作都有对应 Toast；页面上没有“已更新”文字；同页只有一条结果提示；409 删除冲突仍在弹窗内显示“节点已被修改，请刷新目录” |
| M2-03 | PASS | admin-nodes.spec 第 2 例：节点代理保存、停用、启用、移除都有提示，移除后代理从列表消失 |
| M2-04 | PASS | admin-console-2.spec：停用 / 启用 E2E-OFF10 分别出现“优惠码已停用 / 优惠码已启用”，正文为优惠码，之后该码仍为“有效” |
| M2-05 | PASS | tickets.spec：管理员发送回复后出现“回复已发送” |
| M2-06 | PASS | test:toast 7/7；临时探针 src/components/console/__guard_probe.tsx 让守卫失败（6 pass / 1 fail），删除后恢复 7/7 |
| M2-07 | PASS | admin-console、admin-console-2、admin-console-plans 随 payment fixture 全量通过；admin-nodes 两例随 catalog 套件通过 |
| M2-08 | PASS（本机） | payment fixture 全量 112 passed / 0 failed（10.2 分钟）；catalog 套件 10/10，含官方 Mihomo 实流量与加权账本（Clash.Meta 源码从主目录 rsync 进 worktree）；test:api-proxy 17/17；lint、typecheck、build（--webpack 与默认）、test:copy、test:theme 及全部 node 单元套件通过。CI 结果见 PR |
| M2-09 | PASS（有偏差） | 浏览器实测（Chrome 加载页面时取 /_next/static 下全部 JS 的 gzip 大小之和，两边都是默认 next build）：/login 257 994 → 259 855 B（+1 861，+0.7%）；/dashboard 289 043 → 290 648（+1 605）；/referral 283 363 → 285 020（+1 657）；/account 273 462 → 275 067（+1 605）；/admin/nodes 303 959 → 305 629（+1 670）；/admin/console/users 301 957 → 303 553（+1 596）。增量约 1.6–1.9 KB，比第 7 节估计的“1 KB 量级”略大，主要来自四个图标和统一模块。基线为 6b7e48a（两轮之前），本轮相对第 1 轮几乎没有新增 |
| M2-10 | PASS | docs/web-messages.md；README 一行 |

偏差：

- /admin/nodes 全页共用一个提示 id：8 秒的结果提示连续操作时会三条叠在页面顶部中央，挡住下面的按钮。改为新结果替换旧结果，页面上始终只有一条最新结果。
- 体积增量见 M2-09，比估计略大，可接受。
- 本轮没有改用户页，第 1 轮记为“部分”的 M1-03（连续重发验证码只保留一条）和 M1-12（同屏 4 条）仍未在浏览器中构造。

经验：

- Turbopack 不接受指向项目外的 node_modules 符号链接（报 Symlink … points out of the filesystem root）。在 /tmp 建体积基线时，用 cp -cR（APFS 克隆，不额外占盘）复制 node_modules。
- 从 frontend/ 目录导出基线要用 git archive <rev> -- .；6b7e48a:frontend 这种写法在子目录里解析为空树，会静默得到空包。
- 往目录页加管理侧测试步骤时，不要把节点代理登记到在线目录用的组上：线路会变成节点计费，后续 Mihomo 验收就连不上。放在已停用的组和服务器上做，做完删除。
