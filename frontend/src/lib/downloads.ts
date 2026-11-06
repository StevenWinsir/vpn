// What the public pages show about client downloads (handoff_download.md 6). The release facts
// come from the backend's public list (src/lib/server/downloads.ts), never from .env; this module
// only shapes them, so test:downloads covers it without a server. No address ever reaches it:
// the public list has none, and every button points at the site's own redirect path (D4).
import {
  archLabels,
  downloadCatalog,
  formatLabels,
  knownVariant,
  label,
  platformOf,
} from './download-catalog.ts';

export type PlatformId = 'windows' | 'macos' | 'android' | 'ios' | 'linux';
export const platformOrder: PlatformId[] = ['windows', 'macos', 'android', 'ios', 'linux'];

/** One published file as the page may know it: no address, no id. */
export type PublicDownload = {
  platform: PlatformId;
  arch: string;
  format: string;
  version: string;
  sizeBytes: number | null;
  sha256: string | null;
  certSha256: string | null;
  bundleId: string | null;
};

/** ok: the list was read (possibly from the last good copy); unavailable: never read (D9). */
export type DownloadState = { status: 'ok' | 'unavailable'; entries: PublicDownload[] };

const HEX64 = /^[0-9a-f]{64}$/;
const VERSION = /^\d+\.\d+\.\d+([.-][0-9A-Za-z.-]+)?$/;
const BUNDLE = /^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$/;

const text = (value: unknown) => (typeof value === 'string' ? value : null);
const hex = (value: unknown) => {
  const v = text(value);
  return v && HEX64.test(v) ? v : null;
};

function rank(e: PublicDownload): number[] {
  const p = platformOf(downloadCatalog, e.platform)!;
  return [platformOrder.indexOf(e.platform), p.archs.indexOf(e.arch), p.formats.indexOf(e.format)];
}

/**
 * The backend's answer rebuilt field by field from the catalogue: anything unknown (a platform,
 * architecture or format, or any field, an address included) is dropped, so a future backend
 * that says more cannot make the page say more. null: not the expected shape at all.
 */
export function sanitizeDownloads(raw: unknown): PublicDownload[] | null {
  const list = (raw as { downloads?: unknown } | null)?.downloads;
  if (!Array.isArray(list)) return null;
  const out: PublicDownload[] = [];
  const seen = new Set<string>();
  for (const item of list.slice(0, 200)) {
    if (!item || typeof item !== 'object') continue;
    const r = item as Record<string, unknown>;
    const platform = text(r.platform) ?? '';
    const arch = text(r.arch) ?? '';
    const format = text(r.format) ?? '';
    const version = text(r.app_version) ?? '';
    const key = `${platform}/${arch}/${format}`;
    if (!knownVariant(downloadCatalog, platform, arch, format) || seen.has(key)) continue;
    if (!VERSION.test(version) || version.length > 32) continue;
    const store = format in downloadCatalog.store_hosts;
    const sha256 = hex(r.sha256);
    if (!store && !sha256) continue;
    const size = r.size_bytes;
    const bundle = text(r.bundle_id);
    if (format === 'ipa' && !(bundle && BUNDLE.test(bundle) && bundle.length <= 155)) continue;
    seen.add(key);
    out.push({
      platform: platform as PlatformId,
      arch,
      format,
      version,
      sizeBytes:
        !store && typeof size === 'number' && Number.isSafeInteger(size) && size > 0 ? size : null,
      sha256: store ? null : sha256,
      certSha256: format === 'apk' ? hex(r.signing_cert_sha256) : null,
      bundleId: format === 'ipa' ? bundle : null,
    });
  }
  return out.sort((a, b) => {
    const ra = rank(a);
    const rb = rank(b);
    return ra[0] - rb[0] || ra[1] - rb[1] || ra[2] - rb[2];
  });
}

/** The site's own path that answers with a redirect to the file (D5). */
export function downloadPath(e: { platform: string; arch: string; format: string }): string {
  return `/download/get/${e.platform}/${e.arch}/${e.format}`;
}

export const iosManifestPath = '/download/get/ios/arm64/ipa/manifest.plist';

/**
 * The link that makes Safari on an iPhone offer to install the app. It names only this site's own
 * manifest route; origin is the page's own address as the browser sees it.
 */
export function iosInstallLink(origin: string): string {
  return `itms-services://?action=download-manifest&url=${encodeURIComponent(origin + iosManifestPath)}`;
}

/** The architecture a platform's filled button goes to (D10). */
export const recommendedArch: Record<PlatformId, string> = {
  windows: 'x64',
  macos: 'arm64',
  android: 'arm64-v8a',
  ios: 'arm64',
  linux: 'x64',
};

// What a button says about the device a file is for, in the platform's own terms.
const deviceNotes: Record<string, Record<string, string>> = {
  windows: { x64: 'x64', arm64: 'ARM64' },
  macos: { arm64: 'Apple 芯片', x64: 'Intel 芯片', universal: '通用版' },
  android: {
    'arm64-v8a': '64 位手机',
    'armeabi-v7a': '32 位旧款手机',
    x86_64: 'x86_64',
    universal: '通用版',
  },
  linux: { x64: 'x64', arm64: 'ARM64' },
};

const actions: Record<string, string> = {
  exe: '下载安装程序',
  msi: '下载 MSI 安装包',
  zip: '下载免安装版',
  dmg: '下载 DMG',
  pkg: '下载 PKG 安装包',
  apk: '下载 APK',
  ipa: '在 iPhone 上安装',
  app_store: '前往 App Store',
  testflight: '通过 TestFlight 安装',
  appimage: '下载 AppImage',
  deb: '下载 DEB',
  rpm: '下载 RPM',
  'tar.gz': '下载压缩包',
};

export function megabytes(bytes: number): string {
  return `${(bytes / 2 ** 20).toFixed(1)} MB`;
}

/** 64 hex digits in groups of 4 for reading aloud or comparing by eye. */
export function grouped(hex: string): string {
  return hex.match(/.{1,4}/g)?.join(' ') ?? hex;
}

/**
 * A button's words. Desktop buttons always name the device (handoff 6.2); Android names it only
 * when there is more than one architecture to choose from; iOS never does.
 */
export function buttonLabel(e: PublicDownload, many: boolean): string {
  const parts: string[] = [];
  const note = deviceNotes[e.platform]?.[e.arch];
  if (note && (e.platform !== 'android' || many)) parts.push(note);
  if (e.sizeBytes) parts.push(megabytes(e.sizeBytes));
  const action = actions[e.format] ?? '下载';
  return parts.length ? `${action}（${parts.join(' · ')}）` : action;
}

export type DownloadButton = {
  key: string;
  /** The redirect path, or for an iPhone web install the manifest route (built in the browser). */
  href: string;
  ios: boolean;
  label: string;
  solid: boolean;
  /** Shown beside the button only when the files of a platform differ in version (D10). */
  version: string | null;
};

export type PlatformView = {
  platform: PlatformId;
  buttons: DownloadButton[];
  /** One version for the whole card when every file has the same one. */
  badge: string | null;
  files: { key: string; title: string; sha256: string }[];
  certSha256: string | null;
  hasIpa: boolean;
  store: 'app_store' | 'testflight' | null;
};

export function platformView(entries: PublicDownload[], platform: PlatformId): PlatformView {
  const own = entries.filter((e) => e.platform === platform);
  const versions = new Set(own.map((e) => e.version));
  const same = versions.size <= 1;
  const archs = new Set(own.map((e) => e.arch));
  const recommended = own.find((e) => e.arch === recommendedArch[platform]) ?? null;
  const filed = own.filter((e) => e.sha256);
  return {
    platform,
    badge: same && own.length > 0 ? own[0].version : null,
    buttons: own.map((e) => ({
      key: `${e.arch}/${e.format}`,
      href: e.format === 'ipa' ? iosManifestPath : downloadPath(e),
      ios: e.format === 'ipa',
      label: buttonLabel(e, archs.size > 1),
      solid: e === recommended,
      version: same ? null : e.version,
    })),
    files: filed.map((e) => ({
      key: `${e.arch}/${e.format}`,
      title:
        filed.length > 1
          ? `SHA-256（${label(archLabels, e.arch)} · ${label(formatLabels, e.format)}）`
          : e.format === 'ipa'
            ? 'SHA-256（IPA 文件，下载后可核对文件完整性）'
            : 'SHA-256（下载后可核对文件完整性）',
      sha256: e.sha256!,
    })),
    certSha256: own.find((e) => e.format === 'apk' && e.certSha256)?.certSha256 ?? null,
    hasIpa: own.some((e) => e.format === 'ipa'),
    store:
      (own.find((e) => e.format === 'app_store' || e.format === 'testflight')?.format as
        'app_store' | 'testflight' | undefined) ?? null,
  };
}

/** The home page's card for a platform (D14). */
export function homeStatus(
  state: DownloadState,
  platform: PlatformId,
): 'ready' | 'soon' | 'unknown' {
  if (state.status === 'unavailable') return 'unknown';
  return state.entries.some((e) => e.platform === platform) ? 'ready' : 'soon';
}

/** The version a guide shows: the recommended architecture's, else the first published file's. */
export function guideVersion(entries: PublicDownload[], platform: PlatformId): string | null {
  const own = entries.filter((e) => e.platform === platform);
  return (own.find((e) => e.arch === recommendedArch[platform]) ?? own[0])?.version ?? null;
}

function xml(value: string): string {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&apos;');
}

/** The over-the-air manifest iOS reads before it installs an IPA from a web page (D6). */
export function iosManifest({
  ipaUrl,
  bundleId,
  version,
}: {
  ipaUrl: string;
  bundleId: string;
  version: string;
}): string {
  return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>items</key>
  <array>
    <dict>
      <key>assets</key>
      <array>
        <dict>
          <key>kind</key>
          <string>software-package</string>
          <key>url</key>
          <string>${xml(ipaUrl)}</string>
        </dict>
      </array>
      <key>metadata</key>
      <dict>
        <key>bundle-identifier</key>
        <string>${xml(bundleId)}</string>
        <key>bundle-version</key>
        <string>${xml(version)}</string>
        <key>kind</key>
        <string>software</string>
        <key>title</key>
        <string>AsterLink</string>
      </dict>
    </dict>
  </array>
</dict>
</plist>
`;
}
