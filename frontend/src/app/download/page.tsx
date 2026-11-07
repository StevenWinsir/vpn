import { DownloadView } from '@/components/download-view';
import { loadDownloads } from '@/lib/server/downloads';
import { indexableMetadata } from '@/lib/server/seo';
import { downloadDescription } from '@/lib/seo-copy';

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

export default async function DownloadPage() {
  return <DownloadView state={await loadDownloads()} />;
}
