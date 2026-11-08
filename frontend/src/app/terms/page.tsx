import { JsonLd } from '@/components/json-ld';
import { LegalPage } from '@/components/legal-page';
import { termsOfService } from '@/lib/legal';
import { indexableMetadata, siteOrigin } from '@/lib/server/seo';
import { siteFacts } from '@/lib/site';
import { breadcrumbs } from '@/lib/structured-data';

// The support address comes from the server environment on every request (handoff_ui.md 8.3).
export const dynamic = 'force-dynamic';

export function generateMetadata() {
  return indexableMetadata('/terms');
}

export default function TermsPage() {
  const { supportEmail } = siteFacts(process.env);
  const origin = siteOrigin();
  return (
    <>
      {origin ? (
        <JsonLd data={[breadcrumbs(origin, [{ name: '服务条款', path: '/terms' }])]} />
      ) : null}
      <LegalPage
        doc={termsOfService(supportEmail)}
        other={{ href: '/privacy', label: '隐私政策' }}
      />
    </>
  );
}
