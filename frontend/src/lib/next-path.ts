// The only post-login destination the login page honours: a payment result page for one order.
// Anything else (other paths, other origins, extra parameters) falls back to the dashboard.
const resultPath =
  /^\/pay\/result\?order=[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
export function safeNext(value: string | null | undefined): string | null {
  return value && resultPath.test(value) ? value : null;
}
