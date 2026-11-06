import { downloadLocation } from '@/lib/server/downloads';
import { redirectResponse } from '@/lib/download-source';
import { platformOrder, type PlatformId } from '@/lib/downloads';

// /download/get/{platform}/{arch}/{format} (handoff_download.md 6.3, D4, D5): the download
// buttons' only target. The backend's redirect is looked up here, its address checked again, and
// the browser sent on; a combination that is unknown, withdrawn or unreachable leads back to its
// card on the download page instead of an error.
export const dynamic = 'force-dynamic';

type Context = { params: Promise<{ platform: string; arch: string; format: string }> };

export async function GET(request: Request, context: Context) {
  const { platform, arch, format } = await context.params;
  const known = platformOrder.includes(platform as PlatformId);
  const location = known ? await downloadLocation(request, { platform, arch, format }) : null;
  return redirectResponse(location ?? (known ? `/download#${platform}` : '/download'));
}
