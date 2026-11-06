// The server side of the public download list and the redirect (handoff_download.md 6.1, 6.3,
// D9), written against an injected fetch and clock so test:downloads drives it without a server.
// Only server code imports it (src/lib/server/downloads.ts and the /download/get routes).
import { isIP } from 'node:net';
import { checkURL, downloadCatalog, knownVariant } from './download-catalog.ts';
import { sanitizeDownloads, type DownloadState, type PublicDownload } from './downloads.ts';

export const listTimeoutMs = 3000;
export const staleForMs = 24 * 60 * 60 * 1000;
export const productionTTL = 30_000;

type Fetch = (input: string, init?: RequestInit) => Promise<Response>;

/**
 * The public list, cached for ttl. A failed read keeps serving the last good copy for up to a day;
 * without one the page says the list cannot be read right now. A failure is remembered for a few
 * seconds so a dead backend is not asked on every page view.
 */
export function createDownloadSource({
  fetchList,
  now = Date.now,
  ttl = productionTTL,
}: {
  fetchList: () => Promise<unknown>;
  now?: () => number;
  ttl?: number;
}) {
  let good: { at: number; entries: PublicDownload[] } | null = null;
  let failedAt: number | null = null;
  let pending: Promise<DownloadState> | null = null;
  const retryAfter = Math.min(ttl, 5000);

  const fallback = (): DownloadState =>
    good && now() - good.at < staleForMs
      ? { status: 'ok', entries: good.entries }
      : { status: 'unavailable', entries: [] };

  const refresh = async (): Promise<DownloadState> => {
    try {
      const entries = sanitizeDownloads(await fetchList());
      if (!entries) throw new Error('unexpected download list');
      good = { at: now(), entries };
      failedAt = null;
      return { status: 'ok', entries };
    } catch {
      failedAt = now();
      return fallback();
    }
  };

  return {
    async get(): Promise<DownloadState> {
      if (good && now() - good.at < ttl) return { status: 'ok', entries: good.entries };
      if (failedAt !== null && now() - failedAt < retryAfter) return fallback();
      pending ??= refresh().finally(() => {
        pending = null;
      });
      return pending;
    },
  };
}

/** GET {api}/downloads with a short timeout; the parsed JSON, or a throw. */
export async function fetchDownloadList(fetchImpl: Fetch, api: URL): Promise<unknown> {
  const response = await fetchImpl(`${api.toString().replace(/\/+$/, '')}/downloads`, {
    headers: { accept: 'application/json' },
    cache: 'no-store',
    redirect: 'error',
    signal: AbortSignal.timeout(listTimeoutMs),
  });
  if (!response.ok) {
    await response.body?.cancel();
    throw new Error(`download list ${response.status}`);
  }
  return response.json();
}

/**
 * The browser address as the trusted ingress reports it (the same rule as the /api/v1 proxy): with
 * "append" style headers only the last entry is the ingress's own; anything that is not exactly one
 * IP address is ignored.
 */
export function clientAddress(value: string | null): string | null {
  if (!value || value.length > 1024) return null;
  const last = value.split(',').at(-1)!.trim();
  return isIP(last) ? last : null;
}

/** The headers the redirect lookup sends upstream: the browser address and nothing else of the request. */
export function upstreamHeaders(request: Request, clientIPHeader: string): Headers {
  const headers = new Headers({ accept: 'application/json' });
  const address = clientIPHeader ? clientAddress(request.headers.get(clientIPHeader)) : null;
  if (address) headers.set('x-forwarded-for', address);
  return headers;
}

/**
 * Where /download/get/{platform}/{arch}/{format} sends the browser: the backend's 302 target,
 * checked again by the address rules (D7), or null (unknown combination, not published, a timeout,
 * anything unexpected). Nothing upstream says is echoed.
 */
export async function resolveDownload(
  fetchImpl: Fetch,
  api: URL,
  variant: { platform: string; arch: string; format: string },
  headers: Headers,
): Promise<string | null> {
  const { platform, arch, format } = variant;
  if (!knownVariant(downloadCatalog, platform, arch, format)) return null;
  try {
    const response = await fetchImpl(
      `${api.toString().replace(/\/+$/, '')}/downloads/${platform}/${arch}/${encodeURIComponent(format)}`,
      {
        headers,
        cache: 'no-store',
        redirect: 'manual',
        signal: AbortSignal.timeout(listTimeoutMs),
      },
    );
    await response.body?.cancel();
    const location = response.headers.get('location');
    if (response.status !== 302 || !location) return null;
    return checkURL(downloadCatalog, location, format) === null ? location : null;
  } catch {
    return null;
  }
}

/** A redirect the browser can follow, never cached, never indexed, never sending this page as referrer. */
export function redirectResponse(location: string): Response {
  return new Response(null, {
    status: 302,
    headers: {
      Location: location,
      'Cache-Control': 'no-store',
      'Referrer-Policy': 'no-referrer',
      'X-Robots-Tag': 'noindex',
    },
  });
}
