import { APIProxyError, proxyErrorResponse, readAPIConfig } from '@/lib/server/api-config';
import { siteFacts } from '@/lib/site';
import { crispSetting } from '@/lib/support-config';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

// One warning per distinct bad value, not one per page view.
const reported = new Set<string>();

export function GET() {
  try {
    const { timeoutMs } = readAPIConfig();
    // The support settings can only switch the chat off; they never fail this response.
    const crisp = crispSetting(process.env);
    if (crisp.problem && !reported.has(process.env.CRISP_WEBSITE_ID ?? '')) {
      reported.add(process.env.CRISP_WEBSITE_ID ?? '');
      console.warn(crisp.problem);
    }
    return Response.json(
      // Never publish the internal URL. Leave time for the proxy's own timeout response to arrive.
      {
        apiBaseUrl: '/api/v1',
        apiTimeoutMs: timeoutMs + 5000,
        support: {
          crispWebsiteId: crisp.websiteId,
          email: siteFacts(process.env).supportEmail,
        },
      },
      { headers: { 'Cache-Control': 'no-store' } },
    );
  } catch (error) {
    if (error instanceof APIProxyError) return proxyErrorResponse(error);
    throw error;
  }
}
