import { sitemapXml } from '@/lib/seo';
import { currentSiteConfig } from '@/lib/server/seo';

// Rendered per request like robots.txt (handoff_seo.md D1, D13): the indexable canonical
// addresses, or an empty list while the site is not indexed.
export const dynamic = 'force-dynamic';

export function GET() {
  return new Response(sitemapXml(currentSiteConfig()), {
    headers: {
      'Content-Type': 'application/xml; charset=utf-8',
      'Cache-Control': 'public, max-age=3600',
      'X-Robots-Tag': 'noindex',
    },
  });
}
