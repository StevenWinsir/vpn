import { permanentRedirect } from 'next/navigation';
import { GuideHub } from '@/components/guide-hub';
import { JsonLd } from '@/components/json-ld';
import { legacyGuideTarget } from '@/lib/guides';
import { guideDownloads } from '@/lib/server/guide';
import { indexableMetadata, siteOrigin } from '@/lib/server/seo';
import { breadcrumbs } from '@/lib/structured-data';

// Read on every request through the same 30-second cache as the download page
// (handoff_download.md 6.4): a release published in the console shows here without a restart.
export const dynamic = 'force-dynamic';

export function generateMetadata() {
  return indexableMetadata('/guide');
}

// The overview of the guide (handoff_seo.md D5); each platform has its own page under
// /guide/{platform}. Public, like /download (handoff_guide.md D2): no RequireAuth. The former
// single page took the platform from ?platform=; those addresses move here for good (308).
export default async function GuidePage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const target = legacyGuideTarget((await searchParams).platform);
  if (target) permanentRedirect(target);
  const origin = siteOrigin();
  return (
    <>
      {origin ? (
        <JsonLd data={[breadcrumbs(origin, [{ name: '使用教程', path: '/guide' }])]} />
      ) : null}
      <GuideHub downloads={await guideDownloads()} />
    </>
  );
}
