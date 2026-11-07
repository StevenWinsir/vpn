import { robotsTxt } from '@/lib/seo';
import { currentSiteConfig } from '@/lib/server/seo';

// Rendered per request from SITE_URL and SITE_INDEXING (handoff_seo.md 4, D1): a restart switches
// it, no rebuild. An hour of caching lets crawlers see a change soon after it is made.
export const dynamic = 'force-dynamic';

export function GET() {
  return new Response(robotsTxt(currentSiteConfig()), {
    headers: {
      'Content-Type': 'text/plain; charset=utf-8',
      'Cache-Control': 'public, max-age=3600',
      'X-Robots-Tag': 'noindex',
    },
  });
}
