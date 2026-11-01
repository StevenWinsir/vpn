import type {
  Ticket,
  TicketCategory,
  TicketDetail,
  TicketList,
  TicketPlatform,
  TicketStatus,
  TicketSummary,
} from './types';

// Support tickets (handoff_ticket.md 4, 11): names, text rules and the small calculations both
// sides show. The limits are the backend's constants (D5); the form checks them first so an
// obviously long text never costs a request, and the backend checks them again.
export const ticketLimits = { subject: 60, body: 3000, note: 500, maxOpen: 3 } as const;
// D7: how often each view refreshes while the page is visible.
export const ticketRefresh = {
  userDetail: 20000,
  userBadge: 60000,
  consoleList: 15000,
  consoleDetail: 10000,
  consoleBadge: 15000,
} as const;
const day = 86400000;

export const categoryOptions: { value: TicketCategory; label: string; hint: string }[] = [
  { value: 'connection', label: '连接与节点', hint: '连不上、速度慢、经常断线' },
  { value: 'account', label: '账号与登录', hint: '登录、验证码、密码与设备' },
  { value: 'billing', label: '套餐、订单与支付', hint: '付款未到账、续费、优惠码与推荐奖励' },
  { value: 'client', label: '客户端下载与安装', hint: '下载、安装与更新' },
  { value: 'other', label: '其他问题', hint: '以上都不是' },
];
export const platformOptions: { value: TicketPlatform; label: string }[] = [
  { value: 'windows', label: 'Windows' },
  { value: 'macos', label: 'macOS' },
  { value: 'android', label: 'Android' },
  { value: 'ios', label: 'iOS' },
  { value: 'web', label: '网页' },
  { value: 'other', label: '其他' },
];
export function categoryLabel(value: string): string {
  return categoryOptions.find((option) => option.value === value)?.label ?? value;
}
export function platformLabel(value: string): string {
  if (!value) return '未填写';
  return platformOptions.find((option) => option.value === value)?.label ?? value;
}

// D2: which optional fields a category opens by default.
export function categoryExtras(category: TicketCategory | null): {
  platform: boolean;
  order: boolean;
} {
  return {
    platform: category === 'connection' || category === 'client',
    order: category === 'billing',
  };
}

// The user side calls the other party "客服" (D6); closed tickets say who closed them.
export function userStatus(
  status: TicketStatus,
  closedBy: string = '',
): { text: string; color: string } {
  switch (status) {
    case 'open':
      return { text: '等待客服回复', color: 'yellow' };
    case 'answered':
      return { text: '客服已回复', color: 'teal' };
    case 'closed':
      return { text: closedBy === 'staff' ? '已关闭（由客服关闭）' : '已关闭', color: 'gray' };
    default:
      return { text: status, color: 'gray' };
  }
}
export function consoleStatus(status: TicketStatus): { text: string; color: string } {
  switch (status) {
    case 'open':
      return { text: '待处理', color: 'orange' };
    case 'answered':
      return { text: '已回复', color: 'teal' };
    case 'closed':
      return { text: '已关闭', color: 'gray' };
    default:
      return { text: status, color: 'gray' };
  }
}

// Unicode characters after trimming, the way the backend counts (handoff_ticket.md 4.3).
export function textLength(value: string): number {
  return Array.from(value.trim()).length;
}
// Control characters other than line breaks and tabs (Go's unicode.IsControl).
const controlCharacters = /[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F-\u009F]/;

// An empty string when the text is acceptable, otherwise the field's message.
export function subjectError(value: string): string {
  const length = textLength(value);
  if (length === 0) return '请填写标题';
  if (length > ticketLimits.subject) return `标题最多 ${ticketLimits.subject} 个字`;
  if (/[\r\n]/.test(value.trim())) return '标题只能是一行文字';
  if (controlCharacters.test(value)) return '标题含有无法识别的字符';
  return '';
}
export function bodyError(value: string, max: number = ticketLimits.body, name = '内容'): string {
  const length = textLength(value);
  if (length === 0) return `请填写${name}`;
  if (length > max) return `${name}最多 ${max} 个字`;
  if (controlCharacters.test(value)) return `${name}含有无法识别的字符`;
  return '';
}

// "2026-11-30 14:05" in the browser's zone.
export function localMinute(value: string): string {
  const d = new Date(value);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
export function deletionText(expiresAt: string): string {
  return `将于 ${localMinute(expiresAt)} 自动删除`;
}
// Whole days left before deletion, rounded down (0 on the last day, never negative).
export function daysLeft(expiresAt: string, now: number = Date.now()): number {
  return Math.max(0, Math.floor((new Date(expiresAt).getTime() - now) / day));
}
// Less than three days left (handoff_ticket.md 8): the user side shows a yellow notice.
export function expiringSoon(expiresAt: string, now: number = Date.now()): boolean {
  const left = new Date(expiresAt).getTime() - now;
  return left > 0 && left < 3 * day;
}

// "已等待 5 分钟", "已等待 3 小时", "已等待 2 天" (console queue).
export function waitingText(since: string | null, now: number = Date.now()): string {
  if (!since) return '—';
  const minutes = Math.max(0, Math.floor((now - new Date(since).getTime()) / 60000));
  if (minutes < 1) return '刚刚提交';
  if (minutes < 60) return `已等待 ${minutes} 分钟`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `已等待 ${hours} 小时`;
  return `已等待 ${Math.floor(hours / 24)} 天`;
}

// Badge text: nothing for zero, "9+" past nine.
export function badgeText(count: number | null | undefined): string {
  if (!count || count <= 0) return '';
  return count > 9 ? '9+' : String(count);
}

// The 429 wording (handoff_ticket.md 6.4).
export function rateLimitText(retryAfterSeconds: number | null): string {
  if (!retryAfterSeconds || retryAfterSeconds <= 0) return '操作太频繁，请稍后再试';
  return `操作太频繁，请于 ${Math.max(1, Math.ceil(retryAfterSeconds / 60))} 分钟后再试`;
}

// Whether another ticket can be submitted, and why not.
export function submitBlock(openCount: number, maxOpen: number = ticketLimits.maxOpen): string {
  return openCount >= maxOpen
    ? `处理中的工单最多 ${maxOpen} 张，请等待处理完毕或关闭已解决的工单后再提交`
    : '';
}

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null;
function isTicket(value: unknown): value is Ticket {
  return (
    isRecord(value) &&
    typeof value.id === 'string' &&
    typeof value.ticket_no === 'string' &&
    typeof value.subject === 'string' &&
    typeof value.status === 'string' &&
    typeof value.user_unread === 'boolean' &&
    typeof value.expires_at === 'string'
  );
}
// Unexpected bodies (an older backend, a proxy page) count as "not available" instead of
// breaking the page.
export function isTicketList(value: unknown): value is TicketList {
  return (
    isRecord(value) &&
    Array.isArray(value.tickets) &&
    value.tickets.every(isTicket) &&
    typeof value.total === 'number' &&
    typeof value.open_count === 'number' &&
    typeof value.max_open === 'number' &&
    Array.isArray(value.order_options)
  );
}
export function isTicketDetail(value: unknown): value is TicketDetail {
  return isTicket(value) && Array.isArray((value as Record<string, unknown>).messages);
}
export function isTicketSummary(value: unknown): value is TicketSummary {
  return (
    isRecord(value) &&
    typeof value.unread_count === 'number' &&
    typeof value.open_count === 'number' &&
    typeof value.max_open === 'number'
  );
}
