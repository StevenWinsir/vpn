import 'server-only';
import { readAPIConfig } from './api-config';
import {
  createDownloadSource,
  fetchDownloadList,
  productionTTL,
  resolveDownload,
  upstreamHeaders,
} from '../download-source';
import type { DownloadState } from '../downloads';

// One cache per Next.js process (handoff_download.md D9): /download, the home page and /guide all
// read through it. The browser acceptance fixture (scripts/test-payment-web.py, which marks its
// website process with E2E_ISOLATED=private-postgres) may shorten it with DOWNLOADS_CACHE_MS so a
// change in the console shows within a second; everywhere else it is 30 seconds.
function cacheTTL(): number {
  if (process.env.E2E_ISOLATED !== 'private-postgres') return productionTTL;
  const value = Number(process.env.DOWNLOADS_CACHE_MS);
  return Number.isInteger(value) && value >= 500 && value <= productionTTL ? value : productionTTL;
}

const source = createDownloadSource({
  ttl: cacheTTL(),
  fetchList: () => fetchDownloadList(fetch, readAPIConfig().internalURL),
});

export function loadDownloads(): Promise<DownloadState> {
  return source.get();
}

/** The checked file address for a platform, architecture and format, or null. */
export async function downloadLocation(
  request: Request,
  variant: { platform: string; arch: string; format: string },
): Promise<string | null> {
  let config: ReturnType<typeof readAPIConfig>;
  try {
    config = readAPIConfig();
  } catch {
    return null;
  }
  return resolveDownload(
    fetch,
    config.internalURL,
    variant,
    upstreamHeaders(request, config.clientIPHeader),
  );
}
