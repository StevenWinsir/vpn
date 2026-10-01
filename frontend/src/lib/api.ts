// Browser requests must stay on the frontend origin. Only the Next.js server knows the upstream.
const base = '/api/v1';
export class APIError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
    this.name = 'APIError';
  }
}
let timeoutInFlight: Promise<number> | null = null;
function requestTimeout(): Promise<number> {
  if (!timeoutInFlight) {
    timeoutInFlight = fetch('/api/runtime-config', {
      credentials: 'same-origin',
      cache: 'no-store',
      signal: AbortSignal.timeout(10000),
    })
      .then(async (response) => {
        const config = await response.json();
        if (!response.ok)
          throw new APIError(
            response.status,
            config?.error?.code || 'api_config_invalid',
            config?.error?.message || '服务端 API 配置不可用。',
          );
        if (
          config.apiBaseUrl !== base ||
          !Number.isInteger(config.apiTimeoutMs) ||
          config.apiTimeoutMs < 1000 ||
          config.apiTimeoutMs > 185000
        )
          throw new APIError(503, 'api_config_invalid', '服务端 API 配置不可用。');
        return config.apiTimeoutMs as number;
      })
      .catch((error) => {
        timeoutInFlight = null;
        throw error;
      });
  }
  return timeoutInFlight;
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
    throw new APIError(0, 'network_error', '无法连接服务，请检查后端是否运行后重试。');
  }
  const data = await response.json().catch(() => null);
  if (!response.ok)
    throw new APIError(
      response.status,
      data?.error?.code || 'request_failed',
      data?.error?.message || '请求失败，请稍后重试。',
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
      ['/auth/login', '/auth/register', '/auth/logout', '/auth/refresh'].includes(path)
    )
      throw error;
    if (!refreshInFlight)
      refreshInFlight = request('/auth/refresh', { method: 'POST', body: '{}' })
        .then(() => undefined)
        .finally(() => {
          refreshInFlight = null;
        });
    await refreshInFlight;
    return request<T>(path, init);
  }
}
export function message(error: unknown) {
  return error instanceof Error ? error.message : '发生未知错误，请重试。';
}
