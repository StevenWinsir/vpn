import { Suspense } from 'react';
import { GuideView } from '@/components/guide-view';
import { androidDownload, iosDownload } from '@/lib/downloads';

// Read the release facts on every request, like the download page: a new package needs a .env
// edit and a restart, not a rebuild.
export const dynamic = 'force-dynamic';

export const metadata = { title: '使用教程' };

// Public, like /download (handoff_guide.md D2): no RequireAuth. One route for every platform
// (?platform=): the sidebar matches exact paths, so a nested route would lose its highlight.
export default function GuidePage() {
  const android = androidDownload(process.env);
  const ios = iosDownload(process.env);
  // Only whether a package exists and its version leave the server, never hashes or addresses.
  const downloads = {
    android: android ? { version: android.version } : null,
    ios: ios ? { version: ios.version } : null,
  };
  return (
    <Suspense fallback={null}>
      <GuideView downloads={downloads} />
    </Suspense>
  );
}
