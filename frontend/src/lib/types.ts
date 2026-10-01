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
  metering_connected: boolean;
  proxy_service_ready: boolean;
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
export type Meta = {
  test_purchase_enabled: boolean;
  proxy_service_ready: boolean;
  currency: string;
  traffic_unit: string;
};
export const appName = process.env.NEXT_PUBLIC_APP_NAME || 'AsterLink';
export const money = (cents: number) =>
  new Intl.NumberFormat('zh-CN', { style: 'currency', currency: 'CNY' }).format(cents / 100);
export const gib = (bytes: number) =>
  new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 }).format(bytes / 1024 ** 3);
export const date = (value: string) =>
  new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(
    new Date(value),
  );
