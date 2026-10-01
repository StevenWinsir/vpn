import { APIProxyError, proxyErrorResponse, readAPIConfig } from '@/lib/server/api-config';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

type Context = { params: Promise<{ path: string[] }> };
const maxBodyBytes = 8192; // Matches the Go API's JSON request limit.
const requestHeaders = [
  'accept',
  'authorization',
  'content-type',
  'cookie',
  'origin',
  'idempotency-key',
  'access-control-request-method',
  'access-control-request-headers',
];
const responseHeaders = [
  'content-type',
  'retry-after',
  'x-request-id',
  'vary',
  'access-control-allow-origin',
  'access-control-allow-credentials',
  'access-control-allow-methods',
  'access-control-allow-headers',
];

async function readBody(request: Request, signal: AbortSignal): Promise<ArrayBuffer | undefined> {
  if (!request.body) return undefined;
  if (Number(request.headers.get('content-length')) > maxBodyBytes)
    throw new APIProxyError(413, 'request_too_large', '请求内容超过允许大小。');
  const reader = request.body.getReader();
  const buffer = new Uint8Array(maxBodyBytes);
  let size = 0;
  const cancel = () => {
    void reader.cancel().catch(() => undefined);
  };
  signal.addEventListener('abort', cancel, { once: true });
  try {
    while (true) {
      signal.throwIfAborted();
      const { done, value } = await reader.read();
      signal.throwIfAborted();
      if (done) return buffer.buffer.slice(0, size);
      size += value.byteLength;
      if (size > maxBodyBytes) {
        await reader.cancel();
        throw new APIProxyError(413, 'request_too_large', '请求内容超过允许大小。');
      }
      buffer.set(value, size - value.byteLength);
    }
  } finally {
    signal.removeEventListener('abort', cancel);
    reader.releaseLock();
  }
}

async function proxy(request: Request, context: Context) {
  let timeout: AbortSignal | undefined;
  try {
    const { internalURL, timeoutMs } = readAPIConfig();
    const { path } = await context.params;
    // Current API routes use plain path segments. Reject traversal and encoded separators.
    if (!path.length || path.some((segment) => !/^[a-zA-Z0-9_-]+$/.test(segment)))
      throw new APIProxyError(400, 'invalid_api_path', 'API 路径无效。');
    const incomingURL = new URL(request.url);
    if (internalURL.origin === incomingURL.origin)
      throw new APIProxyError(503, 'api_config_invalid', '内部 API 地址不能指向前端自身。');
    internalURL.pathname += '/' + path.map(encodeURIComponent).join('/');
    internalURL.search = incomingURL.search;

    const headers = new Headers();
    for (const name of requestHeaders) {
      const value = request.headers.get(name);
      if (value !== null) headers.set(name, value);
    }
    // Do not forward client-controlled Host/Forwarded headers or manufacture a trusted Origin.
    headers.set('accept-encoding', 'identity');
    timeout = AbortSignal.timeout(timeoutMs);
    const signal = AbortSignal.any([request.signal, timeout]);
    const body = ['GET', 'HEAD'].includes(request.method)
      ? undefined
      : await readBody(request, signal);
    const upstream = await fetch(internalURL, {
      method: request.method,
      headers,
      body,
      signal,
      cache: 'no-store',
      redirect: 'manual',
    });
    // This JSON API does not redirect. Never forward credentials to a redirect target.
    if (upstream.status >= 300 && upstream.status < 400) {
      await upstream.body?.cancel();
      throw new APIProxyError(
        502,
        'upstream_redirect',
        '后端返回了非预期重定向，请检查内部 API 地址。',
      );
    }
    const outgoing = new Headers({ 'Cache-Control': 'no-store' });
    for (const name of responseHeaders) {
      const value = upstream.headers.get(name);
      if (value !== null) outgoing.set(name, value);
    }
    // Each Set-Cookie is separate, especially when Expires contains a comma.
    for (const cookie of upstream.headers.getSetCookie()) outgoing.append('Set-Cookie', cookie);
    const payload = await upstream.arrayBuffer();
    return new Response(
      request.method === 'HEAD' || [204, 205, 304].includes(upstream.status) ? null : payload,
      { status: upstream.status, headers: outgoing },
    );
  } catch (error) {
    if (error instanceof APIProxyError) return proxyErrorResponse(error);
    return proxyErrorResponse(
      timeout?.aborted
        ? new APIProxyError(504, 'upstream_timeout', '后端响应超时，请稍后重试。')
        : new APIProxyError(
            502,
            'upstream_unavailable',
            '无法连接后端服务，请检查服务端 API 配置和后端进程。',
          ),
    );
  }
}

export {
  proxy as GET,
  proxy as POST,
  proxy as PUT,
  proxy as PATCH,
  proxy as DELETE,
  proxy as HEAD,
  proxy as OPTIONS,
};
