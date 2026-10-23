// Shapes of the administrator console API (handoff_payment_account.md 9, 10). Administrator-only;
// none of these ever reaches an ordinary user's pages.

export type ConsoleSub = {
  plan_id: string;
  plan_name: string;
  starts_at: string;
  expires_at: string;
  traffic_limit_bytes: number;
  used_units: number;
  upload_bytes: number;
  download_bytes: number;
  max_devices: number;
  allow_dedicated: boolean;
  is_test: boolean;
  state: 'active' | 'expired';
  // The token a save must send back; 0 means "no subscription".
  version: number;
};
export type ConsoleUser = {
  id: string;
  email: string;
  name: string;
  role: 'user' | 'admin';
  status: 'active' | 'deleting';
  created_at: string;
  register_ip: string | null;
  last_login_at: string | null;
  last_login_ip: string | null;
  last_login_geo: string;
  deletion_requested_at?: string | null;
  subscription: ConsoleSub | null;
  online_devices: number;
};
export type ConsoleOrder = {
  id: string;
  trade_no: string | null;
  user_id: string;
  email: string;
  plan_id: string;
  plan_name: string;
  provider: string;
  pay_type: string;
  status: string;
  review_reason: string;
  price_source: string;
  list_price_cents: number;
  discount_cents: number;
  amount_cents: number;
  paid_cents: number | null;
  price_cents: number;
  coupon_code: string;
  created_at: string;
  paid_at: string | null;
  expires_at: string | null;
};
export type ConsoleNotification = {
  id: string;
  received_at: string;
  money_text: string;
  trade_status: string;
  source_ip: string;
  result: string;
};
export type ConsoleLogin = {
  id: string;
  occurred_at: string;
  user_id: string;
  email: string;
  channel: 'web' | 'native';
  platform: string;
  ip: string;
  geo_status: string;
  geo_text: string;
  user_agent: string;
  result: 'success' | 'failure';
  failure_reason: string;
};
export type ConsoleDevice = {
  platform: string;
  app_version: string;
  device: string;
  line?: string;
  connected_at?: string;
  last_heartbeat_at?: string;
  last_seen_at?: string;
};

export const reviewReasonLabel: Record<string, string> = {
  unverified_source: '回调来源 IP 未在白名单内（白名单为空）',
  amount_mismatch: '回调金额与应付金额不符',
  state_conflict: '订阅状态冲突，无法自动开通',
  callback_missing: '网关显示已支付，但未收到可信回调',
  gateway_unreachable: '长时间无法连接网关确认状态',
  closed_by_admin: '管理员已关闭',
};
export const priceSourceLabel: Record<string, string> = {
  list: '标价',
  test_price: '测试价',
  test_purchase: '模拟购买',
};
export const notifyResultLabel: Record<string, string> = {
  received: '处理中',
  accepted: '已受理',
  duplicate: '重复回调',
  unknown_order: '未知订单',
  ip_rejected: '来源 IP 被拒绝',
  invalid: '格式无效',
  review: '转人工核对',
  ignored: '已忽略',
};
export const geoLabel = (status: string, text: string) => {
  if (text) return text;
  if (status === 'pending') return '解析中';
  if (status === 'failed') return '解析失败';
  return '未解析';
};
export const platformLabel: Record<string, string> = {
  windows: 'Windows',
  macos: 'macOS',
  android: 'Android',
  ios: 'iOS',
};

// UTC rendering for hover titles.
export const utc = (value: string | null | undefined) =>
  value
    ? new Date(value)
        .toISOString()
        .replace('T', ' ')
        .replace(/\.\d+Z$/, ' UTC')
    : '';
// <input type="datetime-local"> works in the browser's zone.
export const toLocalInput = (value: string) => {
  const d = new Date(value);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
};
export const fromLocalInput = (value: string) => new Date(value).toISOString();
// A filter date (yyyy-mm-dd, the browser's day) as the API's instants.
export const dayStart = (day: string) => new Date(`${day}T00:00:00`).toISOString();
export const dayEnd = (day: string) => {
  const d = new Date(`${day}T00:00:00`);
  d.setDate(d.getDate() + 1);
  return d.toISOString();
};
export const validDay = (day: string) =>
  /^\d{4}-\d{2}-\d{2}$/.test(day) && !isNaN(new Date(`${day}T00:00:00`).getTime());
// Usage is stored in milli-bytes (the billing unit).
export const unitsToGib = (units: number) => units / 1000 / 1024 ** 3;
export const gibToUnits = (value: number) => Math.round(value * 1024 ** 3 * 1000);
export const gibToBytes = (value: number) => Math.round(value * 1024 ** 3);
