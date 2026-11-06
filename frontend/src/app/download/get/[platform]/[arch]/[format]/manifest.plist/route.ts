import { downloadLocation, loadDownloads } from '@/lib/server/downloads';
import { iosManifest } from '@/lib/downloads';

// /download/get/ios/arm64/ipa/manifest.plist (handoff_download.md D6): the over-the-air manifest
// for an iPhone web install, generated from the published IPA entry, so no manifest file has to be
// uploaded or kept in step. Any other path, or no installable entry: 404, which iOS reports as
// "cannot install".
export const dynamic = 'force-dynamic';

type Context = { params: Promise<{ platform: string; arch: string; format: string }> };

const notFound = () =>
  new Response('Not found\n', {
    status: 404,
    headers: { 'Content-Type': 'text/plain; charset=utf-8', 'Cache-Control': 'no-store' },
  });

export async function GET(request: Request, context: Context) {
  const { platform, arch, format } = await context.params;
  if (platform !== 'ios' || arch !== 'arm64' || format !== 'ipa') return notFound();
  const [state, ipaUrl] = await Promise.all([
    loadDownloads(),
    downloadLocation(request, { platform, arch, format }),
  ]);
  const entry = state.entries.find(
    (e) => e.platform === 'ios' && e.arch === 'arm64' && e.format === 'ipa',
  );
  if (!ipaUrl || !entry?.bundleId) return notFound();
  return new Response(iosManifest({ ipaUrl, bundleId: entry.bundleId, version: entry.version }), {
    headers: {
      'Content-Type': 'application/xml; charset=utf-8',
      'Cache-Control': 'no-store',
      'X-Robots-Tag': 'noindex',
    },
  });
}
