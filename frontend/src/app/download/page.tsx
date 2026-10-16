import { DownloadView } from '@/components/download-view';
import { androidDownload } from '@/lib/downloads';

// Read the release facts on every request: a new APK needs a .env edit and a restart, not a rebuild.
export const dynamic = 'force-dynamic';

export default function DownloadPage() {
  return <DownloadView android={androidDownload(process.env)} />;
}
