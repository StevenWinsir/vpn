export type User = { id: string; email: string; name: string; role: string; created_at: string };
export type Plan = {
  id: string;
  name: string;
  description: string;
  price_cents: number;
  duration_days: number;
  traffic_bytes: number;
  max_devices: number;
  allow_dedicated: boolean;
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
  paid_at: string;
};
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
  proxy_service_ready: boolean;
  currency: string;
  traffic_unit: string;
  // Registration and mail capabilities: effective server values, never configuration secrets.
  allow_registration?: boolean;
  registration_email_domains?: string[];
  password_reset_enabled?: boolean;
  verification_code_ttl_seconds?: number;
  verification_resend_after_seconds?: number;
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
