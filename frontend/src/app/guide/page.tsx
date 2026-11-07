import { Suspense } from 'react';
import { GuideView } from '@/components/guide-view';
import { guideVersion } from '@/lib/downloads';
import { loadDownloads } from '@/lib/server/downloads';
import { indexableMetadata } from '@/lib/server/seo';

// Read on every request through the same 30-second cache as the download page
// (handoff_download.md 6.4): a release published in the console shows here without a restart.
export const dynamic = 'force-dynamic';

export function generateMetadata() {
  return indexableMetadata('/guide');
}

// Public, like /download (handoff_guide.md D2): no RequireAuth. One route for every platform
// (?platform=): the sidebar matches exact paths, so a nested route would lose its highlight.
export default async function GuidePage() {
  const { entries } = await loadDownloads();
  // Only whether a package exists and its version leave the server, never hashes or addresses.
  const version = (platform: 'windows' | 'macos' | 'android' | 'ios') => {
    const v = guideVersion(entries, platform);
    return v ? { version: v } : null;
  };
  const downloads = {
    windows: version('windows'),
    macos: version('macos'),
    android: version('android'),
    ios: version('ios'),
  };
  return (
    <Suspense fallback={null}>
      <GuideView downloads={downloads} />
    </Suspense>
  );
}
