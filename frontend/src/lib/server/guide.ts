import 'server-only';
import { guideVersion } from '../downloads';
import type { GuideDownloads } from '../guides';
import { loadDownloads } from './downloads';

/**
 * What the guide pages may know about each released client: whether a package exists and its
 * version, never hashes or addresses (handoff_guide.md 6.4).
 */
export async function guideDownloads(): Promise<GuideDownloads> {
  const { entries } = await loadDownloads();
  const version = (platform: keyof GuideDownloads) => {
    const v = guideVersion(entries, platform);
    return v ? { version: v } : null;
  };
  return {
    windows: version('windows'),
    macos: version('macos'),
    android: version('android'),
    ios: version('ios'),
  };
}
