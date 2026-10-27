export type User = { id: string; email: string; name: string; role: string; created_at: string };
// GET /auth/me while an administrator is looking at the site as this user.
export type Impersonation = { admin_email: string; expires_at: string };
export type BillingCycle = 'monthly' | 'yearly';
// One billing cycle of a plan (GET /plans, handoff_shop.md 7.5). Every figure is computed
// by the server in whole cents; pages only display them.
export type PlanTier = {
  price_cents: number;
  original_cents: number;
  saving_cents: number;
  discount_percent: number;
  duration_days: number;
  traffic_bytes: number;
  monthly_equivalent_cents: number;
  // Yearly (handoff_shop.md D2 revised): quota_cycle_bytes is granted again every
  // quota_cycle_days, used up or not; traffic_bytes is then the nominal twelve months.
  quota_cycle_days?: number;
  quota_cycle_bytes?: number;
};
export type Plan = {
  id: string;
  name: string;
  description: string;
  // The monthly terms stay at the top level for readers that predate the yearly tier.
  price_cents: number;
  duration_days: number;
  traffic_bytes: number;
  // The device count users can really have online (the plan's value capped by the server).
  max_devices: number;
  allow_dedicated: boolean;
  featured?: boolean;
  monthly?: PlanTier;
  // Null when the plan has no yearly tier.
  yearly?: PlanTier | null;
};
export type PlansResponse = {
  plans: Plan[];
  yearly_discount_percent: number;
  max_yearly_saving_percent: number;
};
export type Subscription = {
  id: string;
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
  // A refreshing (yearly) allowance: the traffic figures above are the current 30-day
  // cycle's, reset to quota_cycle_bytes when it ends. 0 = one-shot allowance.
  quota_cycle_days?: number;
  quota_cycle_bytes?: number;
  quota_cycle_starts_at?: string | null;
};
export type Dashboard = {
  user: User;
  subscription: Subscription | null;
  entitlement_active: boolean;
  order_count: number;
  // True when this account has metered traffic in the last 24 hours.
  metering_connected: boolean;
  proxy_service_ready: boolean;
  online_devices: number;
  device_limit: number;
  // When the current traffic cycle is refreshed; null for a one-shot allowance.
  quota_resets_at?: string | null;
  // Referral reward left in the current subscription period (already part of the limit).
  bonus_bytes?: number;
};

// Referral programme (GET /me/referral, /me/referrals; handoff_coupon.md 11). Never an id,
// a full address or an order of the referred user.
export type ReferralStatus = 'registered' | 'qualified' | 'rewarded' | 'forfeited';
export type ReferralStats = {
  registered_count: number;
  purchased_count: number;
  pending_count: number;
  rewarded_count: number;
  rewarded_bytes: number;
  forfeited_count: number;
};
export type ReferralSummary = {
  enabled: boolean;
  code: string;
  share_path: string;
  reward_rule: { gib_per_yuan: number; max_gib: number };
  has_active_plan: boolean;
  stats: ReferralStats;
  current_bonus_bytes: number;
};
export type ReferralItem = {
  email_masked: string;
  registered_at: string;
  status: ReferralStatus;
  reward_bytes: number | null;
  granted_bytes: number;
  granted_at: string | null;
  forfeit_reason: string;
  referee_deleted: boolean;
};
export type ReferralPage = {
  items: ReferralItem[];
  total: number;
  page: number;
  page_size: number;
};
// Explicit presence contract (GET /me/devices): never a server, session, lease or grant id,
// address or token. `device` is the last 6 characters of the client's device UUID.
export type OnlineDevice = {
  platform: string;
  app_version: string;
  device: string;
  line: string;
  connected_at: string;
  last_heartbeat_at: string;
  upload_bytes: number;
  download_bytes: number;
};
export type SignedInDevice = {
  platform: string;
  app_version: string;
  device: string;
  last_seen_at: string;
};
export type DeviceStatus = {
  server_time: string;
  device_limit: number;
  online_count: number;
  online: OnlineDevice[];
  signed_in: SignedInDevice[];
  lease_seconds: number;
};
// GET /me/usage-history: 8 three-hour intervals ending with the bucket in progress.
export type UsageInterval = {
  start: string;
  end: string;
  upload_bytes: number;
  download_bytes: number;
  charged_units: number;
};
export type UsageHistory = {
  server_time: string;
  window_start: string;
  window_end: string;
  bucket_seconds: number;
  granularity_seconds: number;
  buckets: UsageInterval[];
  total_upload_bytes: number;
  total_download_bytes: number;
  total_charged_units: number;
};
export type Order = {
  id: string;
  plan_id: string;
  plan_name: string;
  price_cents: number;
  duration_days: number;
  traffic_bytes: number;
  status: string;
  provider: string;
  created_at: string;
  // Null while a gateway order is unpaid.
  paid_at: string | null;
  // Gateway orders only (handoff_payment_account.md 5.1); legacy test orders omit them.
  trade_no?: string;
  pay_type?: string;
  list_price_cents?: number;
  discount_cents?: number;
  amount_cents?: number;
  paid_cents?: number | null;
  coupon_code?: string;
  price_source?: string;
  expires_at?: string;
  // Shop snapshots; orders from before them read monthly with original_cents 0.
  billing_cycle?: BillingCycle;
  cycle_discount_percent?: number;
  original_cents?: number;
  // Yearly (handoff_shop.md D2 revised): quota_cycle_bytes is granted again every
  // quota_cycle_days, used up or not; traffic_bytes is then the nominal twelve months.
  quota_cycle_days?: number;
  quota_cycle_bytes?: number;
};
export type PayType = 'wxpay' | 'alipay';
export type CouponResult = { code: string; applied: boolean; reason?: string; message?: string };
export type Quote = {
  plan_id: string;
  plan_name: string;
  duration_days: number;
  traffic_bytes: number;
  list_price_cents: number;
  test_price: boolean;
  base_cents: number;
  discount_cents: number;
  amount_cents: number;
  coupon: CouponResult | null;
  // list_price_cents is the price of the chosen cycle; original_cents is twelve months at
  // the monthly price for yearly.
  billing_cycle?: BillingCycle;
  original_cents?: number;
  cycle_discount_percent?: number;
  cycle_saving_cents?: number;
  monthly_equivalent_cents?: number;
  // Yearly (handoff_shop.md D2 revised): quota_cycle_bytes is granted again every
  // quota_cycle_days, used up or not; traffic_bytes is then the nominal twelve months.
  quota_cycle_days?: number;
  quota_cycle_bytes?: number;
  device_limit?: number;
  allow_dedicated?: boolean;
  purchasable: boolean;
  kind?: 'new' | 'renew';
  reason?: string;
  message?: string;
};
export type PlacedOrder = {
  order: Order;
  pay_url: string;
  replayed: boolean;
  other_pending_orders: number;
};
// GET /payments/orders/:id; gateway_paid is only a display hint, never an entitlement.
export type PaymentOrder = { order: Order & { gateway_paid?: boolean } };
// Explicit metadata contract. Never add connection parameters or YAML here. Each entry is a
// node group (a line); the client is assigned one of its servers when it connects.
export type PublicNode = {
  id: string;
  name: string;
  region: string;
  line_type: 'direct' | 'dedicated';
  rate_permille: number;
  version: number;
};
export type NodeCatalog = {
  nodes: PublicNode[];
  entitlement_active: boolean;
  expires_at: string;
  is_test: boolean;
};
export type Meta = {
  test_purchase_enabled: boolean;
  // Public payment switches (never the merchant, callback address or test-price state).
  payment_enabled?: boolean;
  payment_methods?: string[];
  payment_pending_seconds?: number;
  proxy_service_ready: boolean;
  currency: string;
  traffic_unit: string;
  // Registration and mail capabilities: effective server values, never configuration secrets.
  allow_registration?: boolean;
  registration_email_domains?: string[];
  password_reset_enabled?: boolean;
  verification_code_ttl_seconds?: number;
  verification_resend_after_seconds?: number;
  // Absent on a backend from before the referral programme: the website then neither shows
  // the code field nor sends referral_code (the backend rejects unknown request fields).
  referral_enabled?: boolean;
};
export type RegisterCodeReceipt = {
  message: string;
  verification_id: string;
  expires_at: string;
  next_send_at: string;
  expires_in_seconds: number;
  resend_after_seconds: number;
};
export type ForgotPasswordReceipt = {
  message: string;
  next_send_at: string;
  resend_after_seconds: number;
};
// Returned only for a usable reset token; validation never consumes it.
export type ResetRules = {
  valid: true;
  expires_at: string;
  expires_in_seconds: number;
  password_min_bytes: number;
  password_max_bytes: number;
};
// Account password rules, measured in UTF-8 bytes exactly like the backend.
export const passwordMinBytes = 10;
export const passwordMaxBytes = 72;
export const utf8Bytes = (value: string) => new TextEncoder().encode(value).length;
// Display names are 2–40 Unicode characters after trimming, as the backend counts them.
export const nameLength = (value: string) => Array.from(value.trim()).length;
export const appName = process.env.NEXT_PUBLIC_APP_NAME || 'AsterLink';
export const money = (cents: number) =>
  new Intl.NumberFormat('zh-CN', { style: 'currency', currency: 'CNY' }).format(cents / 100);
export const gib = (bytes: number) =>
  new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 }).format(bytes / 1024 ** 3);
export const date = (value: string) =>
  new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(
    new Date(value),
  );

// A gateway order is live while pending and before its local expiry.
export const livePending = (order: Order, now = Date.now()) =>
  order.status === 'pending' && !!order.expires_at && new Date(order.expires_at).getTime() > now;
export const orderStatusLabel: Record<string, { text: string; color: string }> = {
  pending: { text: '待付款', color: 'yellow' },
  paid: { text: '已支付', color: 'teal' },
  expired: { text: '已超时', color: 'gray' },
  failed: { text: '支付失败', color: 'red' },
  review: { text: '待人工核对', color: 'orange' },
  paid_test: { text: '测试成功', color: 'teal' },
};
export const payTypeLabel: Record<string, string> = { wxpay: '微信支付', alipay: '支付宝' };
export const orderIdPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export const adminPasswordMinBytes = 16;
// One row of GET /me/logins. geo_status is pending, done, failed or skipped.
export type AccountLogin = {
  occurred_at: string;
  channel: 'web' | 'native';
  platform: string;
  ip: string;
  result: 'success' | 'failure';
  geo_status: string;
  geo_text: string;
};
