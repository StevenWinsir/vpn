import { DownloadView } from '@/components/download-view';
import { JsonLd } from '@/components/json-ld';
import { loadDownloads } from '@/lib/server/downloads';
import { indexableMetadata, siteOrigin } from '@/lib/server/seo';
import { downloadDescription } from '@/lib/seo-copy';
import { breadcrumbs, softwareApplications } from '@/lib/structured-data';

// Read on every request through the 30-second cache (handoff_download.md D9): a change in the
// console shows here within half a minute, no restart and no rebuild.
export const dynamic = 'force-dynamic';

// The description names the clients the page offers right now (handoff_seo.md 5.1), from the
// same cached list as the page itself.
export async function generateMetadata() {
  return indexableMetadata('/download', {
    description: downloadDescription(await loadDownloads()),
  });
}

// The released clients are described from the same list as the cards (handoff_seo.md 6.2), so
// the structured data never names a platform the page shows as not yet released.
export default async function DownloadPage() {
  const state = await loadDownloads();
  const origin = siteOrigin();
  return (
    <>
      {origin ? (
        <JsonLd
          data={[
            breadcrumbs(origin, [{ name: '下载客户端', path: '/download' }]),
            ...softwareApplications(origin, state),
          ]}
        />
      ) : null}
      <DownloadView state={state} />
    </>
  );
}
