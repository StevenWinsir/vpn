// Shapes of the administrator console API (handoff_payment_account.md 9, 10). Administrator-only;
// none of these ever reaches an ordinary user's pages.
import type { PlanTier } from './types';

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
  // A refreshing (yearly) allowance: the traffic figures are the current cycle's, reset to
  // quota_cycle_bytes at quota_resets_at. 0 = one-shot.
  quota_cycle_days?: number;
  quota_cycle_bytes?: number;
  quota_resets_at?: string | null;
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
// GET /admin/console/users/:id/referrals (read only; full addresses, for support questions).
export type ConsoleReferral = {
  id: string;
  referee_id: string | null;
  referee_email: string;
  status: 'registered' | 'qualified' | 'rewarded' | 'forfeited';
  first_order_cents: number;
  reward_bytes: number;
  granted_bytes: number;
  forfeit_reason: string;
  last_error_kind: string;
  created_at: string;
  qualified_at: string | null;
  granted_at: string | null;
};
export type ConsoleReferralDetail = {
  code: string | null;
  referrer: { id: string; email: string } | null;
  stats: import('./types').ReferralStats;
  items: ConsoleReferral[];
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
  billing_cycle: 'monthly' | 'yearly';
  cycle_discount_percent: number;
  original_cents: number;
  ent_max_devices: number | null;
  ent_allow_dedicated: boolean | null;
  list_price_cents: number;
  discount_cents: number;
  amount_cents: number;
  paid_cents: number | null;
  price_cents: number;
  coupon_code: string;
  created_at: string;
  paid_at: string | null;
  expires_at: string | null;
  // Mid-period upgrades (handoff_plans.md 9.1); absent on a backend from before them.
  kind?: 'purchase' | 'upgrade';
  from_plan_id?: string;
  from_plan_name?: string;
  credit_cents?: number;
  upgrade_days?: number;
  upgrade_traffic_bytes?: number;
  covers_from?: string | null;
  covers_to?: string | null;
};
// How an upgrade order was priced (GET /admin/console/orders/:id `upgrade`, the stored
// computation). A rate is an amount (cents or bytes) over a number of days.
export type ConsoleRate = { amount: number; days: number };
export type ConsoleUpgrade = {
  subscription_id: string;
  starts_at: string;
  expires_at: string;
  refreshing: boolean;
  charge_days: number;
  credit_days: number;
  value_rate: ConsoleRate;
  value_cents: number;
  credit_cents: number;
  min_charge_cents: number;
  min_charge_applied: boolean;
  to_traffic: ConsoleRate;
  from_traffic: ConsoleRate;
  new_cycle_bytes?: number;
  traffic_add_bytes: number;
  contributions: { order_id: string; cents: number }[] | null;
  lost_groups?: string[];
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

// GET /admin/console/plans (handoff_shop.md 6.2, 9). The derived yearly tier comes from the
// server; a form preview is only a hint until the saved value comes back.
export type AdminPlan = {
  id: string;
  name: string;
  description: string;
  note: string;
  price_cents: number;
  duration_days: number;
  traffic_bytes: number;
  max_devices: number;
  effective_devices: number;
  devices_over_cap: boolean;
  allow_dedicated: boolean;
  active: boolean;
  featured: boolean;
  sort: number;
  yearly_enabled: boolean;
  yearly_duration_days: number;
  yearly_discount_percent: number | null;
  yearly_discount_source: 'global' | 'plan';
  yearly: PlanTier | null;
  version: number;
  active_subscriptions: number;
  pending_orders: number;
  coupon_count: number;
  groups: string[];
  group_count: number;
  created_at: string;
  updated_at: string;
  deleted_at: string | null;
};
export type ShopSettings = {
  yearly_discount_percent: number;
  // Mid-period upgrades (handoff_plans.md 9.2); absent on a backend from before them.
  upgrade_enabled?: boolean;
  upgrade_min_charge_cents?: number;
  version: number;
  updated_by: string | null;
  updated_at: string | null;
  row_missing: boolean;
};
export type AdminPlansResponse = {
  plans: AdminPlan[];
  settings: ShopSettings;
  client_max_online_devices: number;
  following_global_plans: number;
};
// Body of POST /admin/console/plans[/:id]; version is required when editing.
export type AdminPlanInput = Omit<
  AdminPlan,
  | 'effective_devices'
  | 'devices_over_cap'
  | 'yearly_discount_source'
  | 'yearly'
  | 'active_subscriptions'
  | 'pending_orders'
  | 'coupon_count'
  | 'groups'
  | 'group_count'
  | 'created_at'
  | 'updated_at'
  | 'deleted_at'
  | 'id'
  | 'version'
> & { id?: string; version?: number };

// GET /admin/console/tickets[/:id] (handoff_ticket.md 7, 10). Staff messages name the
// administrator who sent them; none of this reaches the user side.
export type ConsoleTicket = import('./types').Ticket & {
  platform: string;
  closed_at: string | null;
  closed_by: 'user' | 'staff' | '';
  waiting_since: string | null;
  user_id: string;
  user_email: string;
  user_status: 'active' | 'deleting' | string;
};
export type ConsoleTicketMessage = import('./types').TicketMessage & {
  author_id: string;
  author_email: string;
};
export type ConsoleTicketDetail = ConsoleTicket & {
  version: number;
  closed_by_admin_email: string;
  user: {
    id: string;
    email: string;
    status: string;
    plan_name: string;
    plan_expires_at: string | null;
  };
  order: import('./types').TicketOrder | null;
  messages: ConsoleTicketMessage[];
};
export type ConsoleTicketList = {
  tickets: ConsoleTicket[];
  total: number;
  page: number;
  page_size: number;
};
export type ConsoleTicketSummary = { open_count: number; oldest_waiting_since: string | null };
