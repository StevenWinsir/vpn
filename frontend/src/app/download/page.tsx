import { DownloadView } from '@/components/download-view';
import { loadDownloads } from '@/lib/server/downloads';

// Read on every request through the 30-second cache (handoff_download.md D9): a change in the
// console shows here within half a minute, no restart and no rebuild.
export const dynamic = 'force-dynamic';

export const metadata = { title: '下载客户端' };

export default async function DownloadPage() {
  return <DownloadView state={await loadDownloads()} />;
}
