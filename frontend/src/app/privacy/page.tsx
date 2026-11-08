import { JsonLd } from '@/components/json-ld';
import { LegalPage } from '@/components/legal-page';
import { privacyPolicy } from '@/lib/legal';
import { indexableMetadata, siteOrigin } from '@/lib/server/seo';
import { siteFacts } from '@/lib/site';
import { breadcrumbs } from '@/lib/structured-data';

// The support address comes from the server environment on every request (handoff_ui.md 8.3).
export const dynamic = 'force-dynamic';

export function generateMetadata() {
  return indexableMetadata('/privacy');
}

export default function PrivacyPage() {
  const { supportEmail } = siteFacts(process.env);
  const origin = siteOrigin();
  return (
    <>
      {origin ? (
        <JsonLd data={[breadcrumbs(origin, [{ name: '隐私政策', path: '/privacy' }])]} />
      ) : null}
      <LegalPage doc={privacyPolicy(supportEmail)} other={{ href: '/terms', label: '服务条款' }} />
    </>
  );
}
