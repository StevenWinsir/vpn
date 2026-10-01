import { APIProxyError, proxyErrorResponse, readAPIConfig } from '@/lib/server/api-config';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

export function GET() {
  try {
    const { timeoutMs } = readAPIConfig();
    return Response.json(
      // Never publish the internal URL. Leave time for the proxy's own timeout response to arrive.
      { apiBaseUrl: '/api/v1', apiTimeoutMs: timeoutMs + 5000 },
      { headers: { 'Cache-Control': 'no-store' } },
    );
  } catch (error) {
    if (error instanceof APIProxyError) return proxyErrorResponse(error);
    throw error;
  }
}
