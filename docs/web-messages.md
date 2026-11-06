# Website short messages (toasts)

Scope: the Next.js website in `frontend/` (handoff_message.md, rounds 1 and 2).

A short message tells the person what an action did. It appears at the top centre and goes away by itself; nobody has to close it. This page says when to use one, how to word it, and how to add one.

## Toast or something else

| Situation | Use |
| --- | --- |
| An action the person started succeeded (signed in, saved, copied, sent, deleted) | A success toast |
| Neutral information about an action, with nothing to fix | An info toast |
| The action could not be fully done, and the page has nowhere to say so (copy refused, the session ended) | A warning toast |
| A failure with no place on the page (signing out failed, returning to the administrator failed) | An error toast |
| A form or request error the person must read and correct | **Not a toast.** Show it where it happened: the field error, or the red `Alert` at the top of the form or dialog. It stays until the next attempt. |
| A result that must be read or checked (the reset-password done page, the payment result page, the plan-saved price details, the gateway check result) | **Not a toast.** Keep the page's own result area. |
| Polling, focus refresh, network recovery, the 刷新 / 同步节点 buttons | **Nothing.** The button's loading state and the page's "最近同步" line are enough. |

Never put an error in a toast only to make the page tidier: it disappears before it is read, and the end-to-end tests find errors with `getByRole('alert')` inside the page.

## Calling it

- Import `toast` from `@/lib/toast` and call `toast.success(title, message?, options?)` (or `info`, `warning`, `error`).
- Nothing else may import `@mantine/notifications`. `npm run test:toast` (in CI) fails when any file under `src/` other than `src/lib/toast.tsx` and `src/components/toaster.tsx` imports it. The stylesheet import in the root layout does not count.
- Durations: success 3 s, info 4 s, warning 5 s, error 6 s. Administration results that carry a consequence to read (who was signed out, what takes effect when) pass `{ duration: toastDurations.note }`, which is 8 s. Hovering pauses the countdown.
- `{ id }`: one message per id. Showing it again replaces the text and restarts the countdown instead of stacking another. Use a fixed id wherever a person can repeat the action quickly (copy buttons), or where results follow each other on one page (`/admin/nodes` uses `admin-nodes` for every result there).
- At most three messages are on screen at once; later ones wait in a queue.
- Success and info messages are `role="status"` (read politely by screen readers). Warning and error are `role="alert"`. Every message carries `data-testid="toast"` and `data-tone`, which the specs use.

## Wording

- The title is at most 10 Chinese characters with no full stop, for example 密码已修改, 邀请链接已复制.
- The body is optional: one sentence of at most 30 characters on the result or the next step.
- User pages follow the copy guard (`npm run test:copy`, `docs/brand-and-copy.md`): no development words (测试, 模拟, 后端 …) and no claims. The administration keeps its operator wording.
- Context such as a ticket number, plan name, email or coupon code may go in the body. React escapes it.
- The registration message never mentions the referral code: the server binds a good code quietly and ignores any other.

## Across a full page load

A toast lives in the root providers, so it survives client-side navigation (`router.push` / `router.replace`). A full load (`window.location.*`, or the reload the support chat forces after signing out) would lose it. For that case:

- `toastAfterNavigation(key)` stores one fixed key and the time in `sessionStorage` (`asterlink-next-toast`). The next document shows the key's fixed wording once and removes it. Stale keys (over 30 s old), unknown keys and broken values are dropped silently.
- `toastAcrossReload(key, reloading)` shows the message now, or stores it when a reload is coming. Sign-out passes `supportInjected()`.
- The keys are `signed-out`, `account-deleted`, `returned-admin` and `session-ended`, with their wording in `src/lib/toast-flash.ts`. To add one, add it to `nextPageToasts` there. The unit test checks every key's tone and copy rules. Never pass text through the address bar or store anything a person typed.

## Where the messages are

User pages:
- 登录成功 / 注册成功 / 验证码已发送
- 已退出登录, 登录已失效 (only when the server ended a signed-in session)
- 密码已修改, 账号已删除, 已返回管理员账号
- 推荐码已复制 / 邀请链接已复制 / 无法自动复制
- 工单已提交 / 工单已关闭 / 回复已发送

Administration (8 s where a consequence follows):
- Users: 密码已修改, 套餐已保存, 订阅已结束, 账号已删除
- Orders: 已确认到账, 订单已关闭
- Plans: 套餐已删除, 升级设置已保存, 全局年付折扣已保存. The plan-saved price details stay in their own card.
- Coupons: 优惠码已创建 / 已保存 / 已删除 / 已停用并隐藏, plus the list switch 优惠码已启用 / 已停用
- Announcements: 草稿已保存 and the publish / withdraw results
- Tickets: 回复已发送, 工单已关闭
- `/admin/nodes`: 节点组已创建 / 已保存 / 已启用 / 已停用 / 已删除, 服务器已导入 / 已保存 / 已删除, 冷却已清除, 节点代理已保存 / 已启用 / 已停用 / 已移除. Failures stay in the red 操作失败 card.

## Tests

- `npm run test:toast`: the next-page storage rules, the copy rules of the fixed messages, and the import guard.
- `tests/toast.spec.ts` (payment fixture): signing in and out, hover pause, phone width, contrast in both schemes, the session ended elsewhere, account deletion, returning to the administrator, and the next-page storage in the browser.
- Messages are also asserted inside the flows that cause them: `account.spec`, `referral.spec`, `support-widget.spec` (sign-out and deletion with the chat loaded), `tickets.spec`, `admin-console-2.spec` (the coupon switch), and `admin-nodes.spec` (catalog fixture).
