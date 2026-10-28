// Browser requests must stay on the frontend origin. Only the Next.js server knows the upstream.
const base = '/api/v1';
export class APIError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    // Seconds from the server's Retry-After header (429), when present.
    public retryAfter: number | null = null,
    // The rest of the error object (for example `field`, or the reasons a plan is in use).
    public details: Record<string, unknown> = {},
  ) {
    super(message);
    this.name = 'APIError';
  }
}
function retryAfterSeconds(response: Response): number | null {
  const value = response.headers.get('Retry-After');
  return value && /^\d{1,6}$/.test(value) ? Number(value) : null;
}
// Anonymous authentication calls never trigger the cookie refresh-and-retry path.
const noRefreshPaths = [
  '/auth/login',
  '/auth/register',
  '/auth/register-code',
  '/auth/forgot-password',
  '/auth/reset-password/validate',
  '/auth/reset-password',
  '/auth/logout',
  '/auth/refresh',
  // A 401 here means the way back is gone; refreshing the user's session would only hide it.
  '/auth/impersonation/return',
];
// Final 401 notification (handoff_auth.md 9.1): a protected request that still fails after the
// single refresh means the server ended this session, for example after a password reset on
// another device. Listeners clear the signed-in state; network errors and 5xx never fire it.
const sessionEndedListeners = new Set<() => void>();
export function onSessionEnded(listener: () => void) {
  sessionEndedListeners.add(listener);
  return () => {
    sessionEndedListeners.delete(listener);
  };
}
function sessionEnded() {
  for (const listener of [...sessionEndedListeners]) listener();
}
// Infrastructure failures (the Next.js proxy, its configuration, an unreachable or misconfigured
// backend) carry operator wording. Ordinary pages show a plain sentence instead; the code and the
// original message stay on the error (details) and in one console warning. Administrators keep
// the original text, which tells them what to fix.
const infrastructureCodes = new Set([
  'api_config_invalid',
  'invalid_api_path',
  'upstream_redirect',
  'upstream_response_too_large',
  'upstream_unavailable',
  'upstream_timeout',
  'node_config_unavailable',
  'node_catalog_disabled',
]);
const unavailable = '服务暂时不可用，请稍后重试。';
function forUsers(error: APIError): APIError {
  if (typeof window === 'undefined' || window.location.pathname.startsWith('/admin')) return error;
  const infrastructure =
    infrastructureCodes.has(error.code) ||
    // copy-guard-next-line: matches the backend's wording, never displays it
    (error.code === 'database_unavailable' && error.message.includes('数据库'));
  if (!infrastructure) return error;
  console.warn(`API ${error.status} ${error.code}: ${error.message}`);
  return new APIError(
    error.status,
    error.code,
    error.code === 'upstream_timeout' ? '服务响应超时，请稍后重试。' : unavailable,
    error.retryAfter,
    { ...error.details, message: error.message },
  );
}
export type RuntimeConfig = {
  apiTimeoutMs: number;
  // The online support chat (handoff_ui.md 9.6): null websiteId means it is switched off.
  support: { crispWebsiteId: string | null; email: string | null };
};
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
// Anything unexpected in the support part only switches the chat off; it never fails the API.
function supportPart(value: unknown): RuntimeConfig['support'] {
  const support = (value && typeof value === 'object' ? value : {}) as Record<string, unknown>;
  const id = support.crispWebsiteId;
  const email = support.email;
  return {
    crispWebsiteId: typeof id === 'string' && UUID.test(id) ? id : null,
    email: typeof email === 'string' && email.includes('@') ? email : null,
  };
}
// Fetched once, before the first API request, and shared (the support chat reads the same result).
let configInFlight: Promise<RuntimeConfig> | null = null;
export function runtimeConfig(): Promise<RuntimeConfig> {
  if (!configInFlight) {
    configInFlight = fetch('/api/runtime-config', {
      credentials: 'same-origin',
      cache: 'no-store',
      signal: AbortSignal.timeout(10000),
    })
      .then(async (response) => {
        const config = await response.json();
        if (!response.ok)
          throw forUsers(
            new APIError(
              response.status,
              config?.error?.code || 'api_config_invalid',
              // copy-guard-next-line: original wording; forUsers() replaces it outside /admin
              config?.error?.message || '服务端 API 配置不可用。',
            ),
          );
        if (
          config.apiBaseUrl !== base ||
          !Number.isInteger(config.apiTimeoutMs) ||
          config.apiTimeoutMs < 1000 ||
          config.apiTimeoutMs > 185000
        )
          // copy-guard-next-line: original wording; forUsers() replaces it outside /admin
          throw forUsers(new APIError(503, 'api_config_invalid', '服务端 API 配置不可用。'));
        return {
          apiTimeoutMs: config.apiTimeoutMs as number,
          support: supportPart(config.support),
        };
      })
      .catch((error) => {
        configInFlight = null;
        throw error;
      });
  }
  return configInFlight;
}
async function requestTimeout(): Promise<number> {
  return (await runtimeConfig()).apiTimeoutMs;
}
async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  let response: Response;
  try {
    const timeout = AbortSignal.timeout(await requestTimeout());
    const headers = new Headers(init.headers);
    if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json');
    response = await fetch(`${base}${path}`, {
      ...init,
      credentials: 'include',
      cache: 'no-store',
      signal: init.signal ? AbortSignal.any([init.signal, timeout]) : timeout,
      headers,
    });
  } catch (error) {
    if (error instanceof APIError) throw error;
    throw new APIError(
      0,
      'network_error',
      error instanceof DOMException && error.name === 'TimeoutError'
        ? '服务响应超时，请检查网络后重试。'
        : '网络连接异常，请检查网络后重试。',
    );
  }
  const data = await response.json().catch(() => null);
  if (!response.ok)
    throw forUsers(
      new APIError(
        response.status,
        data?.error?.code || 'request_failed',
        data?.error?.message || '请求失败，请稍后重试。',
        retryAfterSeconds(response),
        data?.error && typeof data.error === 'object' ? data.error : {},
      ),
    );
  return data as T;
}
let refreshInFlight: Promise<void> | null = null;
export async function api<T>(path: string, init: RequestInit = {}, retry = true): Promise<T> {
  try {
    return await request<T>(path, init);
  } catch (error) {
    if (
      !(error instanceof APIError) ||
      error.status !== 401 ||
      !retry ||
      noRefreshPaths.includes(path)
    )
      throw error;
    if (!refreshInFlight)
      refreshInFlight = request('/auth/refresh', { method: 'POST', body: '{}' })
        .then(() => undefined)
        .finally(() => {
          refreshInFlight = null;
        });
    try {
      await refreshInFlight;
      return await request<T>(path, init);
    } catch (final) {
      if (final instanceof APIError && final.status === 401) sessionEnded();
      throw final;
    }
  }
}
export function message(error: unknown) {
  return error instanceof Error ? error.message : '发生未知错误，请重试。';
}
