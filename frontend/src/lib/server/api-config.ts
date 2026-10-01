import 'server-only';

export class APIProxyError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
    this.name = 'APIProxyError';
  }
}

export function readAPIConfig() {
  // Read inside the request, not next.config.ts: this must remain deployment-time configuration.
  const env = process.env;
  let internalURL: URL;
  try {
    internalURL = new URL(env.API_INTERNAL_URL ?? 'http://127.0.0.1:8080/api/v1');
    if (
      !['http:', 'https:'].includes(internalURL.protocol) ||
      internalURL.username ||
      internalURL.password ||
      internalURL.search ||
      internalURL.hash ||
      internalURL.pathname.replace(/\/+$/, '') !== '/api/v1'
    )
      throw new Error('invalid upstream');
  } catch {
    throw new APIProxyError(503, 'api_config_invalid', '服务端 API_INTERNAL_URL 配置无效。');
  }
  internalURL.pathname = '/api/v1';
  // Legacy timeout remains supported server-side. NEXT_PUBLIC_API_URL is deliberately ignored.
  const timeoutMs = Number(env.API_TIMEOUT_MS ?? env['NEXT_PUBLIC_API_TIMEOUT_MS'] ?? '65000');
  if (!Number.isInteger(timeoutMs) || timeoutMs < 1000 || timeoutMs > 180000)
    throw new APIProxyError(503, 'api_config_invalid', '服务端 API_TIMEOUT_MS 配置无效。');
  return { internalURL, timeoutMs };
}

export function proxyErrorResponse(error: APIProxyError) {
  return Response.json(
    { error: { code: error.code, message: error.message } },
    { status: error.status, headers: { 'Cache-Control': 'no-store' } },
  );
}
