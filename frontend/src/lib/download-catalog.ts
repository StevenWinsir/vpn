// Client download entries (handoff_download.md 4, 7): the names of platforms, architectures and
// formats, and the checks the console runs before it saves. No React here, so test:downloads
// covers it. The catalogue itself (which architectures and formats a platform has, the file
// suffixes, the store hosts) comes from the API; the backend's snapshot in
// backend/internal/api/testdata/client_downloads_catalog.json is what the tests compare against.

export type CatalogPlatform = { platform: string; archs: string[]; formats: string[] };
export type Catalog = {
  platforms: CatalogPlatform[];
  suffixes: Record<string, string>;
  store_hosts: Record<string, string>;
  limit: number;
};

// One row as the console API returns it.
export type ClientDownload = {
  id: string;
  platform: string;
  arch: string;
  format: string;
  app_version: string;
  url: string;
  sha256: string | null;
  size_bytes: number | null;
  signing_cert_sha256: string | null;
  bundle_id: string | null;
  published: boolean;
  released_at: string;
  version: number;
  created_at: string;
  updated_at: string;
};

// Exactly the body the API decodes (it refuses unknown keys).
export type DownloadInput = {
  platform: string;
  arch: string;
  format: string;
  app_version: string;
  url: string;
  sha256: string;
  size_bytes: number | null;
  signing_cert_sha256: string;
  bundle_id: string;
  published: boolean;
  version?: number;
};

export type DownloadField = keyof Omit<DownloadInput, 'published' | 'version'>;
export type FieldError = { field: DownloadField; message: string };

export const platformLabels: Record<string, string> = {
  windows: 'Windows',
  macos: 'macOS',
  android: 'Android',
  ios: 'iOS',
  linux: 'Linux',
};

export const archLabels: Record<string, string> = {
  x64: 'x64',
  arm64: 'arm64',
  universal: '通用',
  'arm64-v8a': 'arm64-v8a',
  'armeabi-v7a': 'armeabi-v7a',
  x86_64: 'x86_64',
};

export const formatLabels: Record<string, string> = {
  exe: '安装程序 .exe',
  msi: 'MSI 安装包 .msi',
  zip: '免安装 .zip',
  dmg: '磁盘映像 .dmg',
  pkg: '安装包 .pkg',
  apk: 'APK 安装包',
  ipa: '网页安装 .ipa',
  app_store: 'App Store',
  testflight: 'TestFlight',
  appimage: 'AppImage',
  deb: 'deb 包',
  rpm: 'rpm 包',
  'tar.gz': '压缩包 .tar.gz',
};

export const label = (map: Record<string, string>, key: string) => map[key] ?? key;

const versionPattern = /^\d+\.\d+\.\d+([.-][0-9A-Za-z.-]+)?$/;
const hex64 = /^[0-9a-f]{64}$/;
const bundlePattern = /^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$/;
export const maxDownloadBytes = 8 * 2 ** 30;

/** The forms packaging tools print (upper case, colons, spaces) become plain lower-case hex. */
export function normalizeHex(value: string): string {
  return value.trim().toLowerCase().replace(/[:\s]/g, '');
}

export function isStoreFormat(catalog: Catalog, format: string): boolean {
  return format in catalog.store_hosts;
}

export function platformOf(catalog: Catalog, platform: string): CatalogPlatform | undefined {
  return catalog.platforms.find((p) => p.platform === platform);
}

/** "arch|format" of the variants a platform already has, except the row being edited. */
export function takenVariants(rows: ClientDownload[], platform: string, except?: string) {
  return new Set(
    rows
      .filter((r) => r.platform === platform && r.id !== except)
      .map((r) => `${r.arch}|${r.format}`),
  );
}

/** The first architecture and format of a platform that are still free, for a new entry. */
export function firstFree(catalog: Catalog, rows: ClientDownload[], platform: string) {
  const p = platformOf(catalog, platform);
  const taken = takenVariants(rows, platform);
  for (const arch of p?.archs ?? [])
    for (const format of p?.formats ?? [])
      if (!taken.has(`${arch}|${format}`)) return { arch, format };
  return null;
}

/** The size field: digits only, at most 8 GiB; empty is no size. */
export function parseSize(text: string): number | null | 'invalid' {
  const trimmed = text.trim();
  if (!trimmed) return null;
  if (!/^\d{1,11}$/.test(trimmed)) return 'invalid';
  const n = Number(trimmed);
  return n >= 1 && n <= maxDownloadBytes ? n : 'invalid';
}

function checkURL(catalog: Catalog, raw: string, format: string): string | null {
  if (!raw) return '请填写下载地址';
  if (raw.length > 1024) return '下载地址最多 1024 个字符';
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return '下载地址格式不正确';
  }
  if (!u.host || !['http:', 'https:'].includes(u.protocol)) return '下载地址格式不正确';
  if (u.username || u.password) return '下载地址不能包含账号或密码';
  if (raw.includes('#')) return '下载地址不能包含 # 片段';
  const host = u.hostname.toLowerCase();
  const store = catalog.store_hosts[format];
  if (store) {
    if (u.protocol !== 'https:' || host !== store || u.port)
      return `该格式只能填写 https://${store} 的页面地址`;
    return null;
  }
  // The server allows plain http to a loopback address outside production only; it has the
  // last word on that, the console only lets it through.
  const loopback = host === 'localhost' || /^127\.\d+\.\d+\.\d+$/.test(host) || host === '[::1]';
  if (u.protocol !== 'https:' && !(u.protocol === 'http:' && loopback))
    return '下载地址必须以 https:// 开头';
  const suffix = catalog.suffixes[format] ?? '';
  let path = u.pathname;
  try {
    path = decodeURIComponent(path);
  } catch {
    // A malformed escape: compare the raw path, as the server would refuse to parse it anyway.
  }
  if (!path.toLowerCase().endsWith(suffix))
    return `下载地址的文件名须以 ${suffix} 结尾，与所选格式一致`;
  return null;
}

/**
 * Mirrors validateDownload on the server (handoff_download.md D7), in the same order, with the
 * same messages, so the form shows at once what saving would say. The server decides.
 */
export function validateDownload(catalog: Catalog, input: DownloadInput): FieldError | null {
  const bad = (field: DownloadField, message: string) => ({ field, message });
  const p = platformOf(catalog, input.platform);
  if (!p) return bad('platform', '请选择平台');
  if (!p.archs.includes(input.arch)) return bad('arch', '请选择该平台支持的架构');
  if (!p.formats.includes(input.format)) return bad('format', '请选择该平台支持的格式');
  const version = input.app_version.trim();
  if (version.length > 32 || !versionPattern.test(version))
    return bad('app_version', '版本号格式应为 1.2.3（可带 -beta.1 之类的后缀），最多 32 个字符');
  const urlError = checkURL(catalog, input.url.trim(), input.format);
  if (urlError) return bad('url', urlError);
  const sha = normalizeHex(input.sha256);
  const cert = normalizeHex(input.signing_cert_sha256);
  const bundle = input.bundle_id.trim();
  const store = isStoreFormat(catalog, input.format);
  if (store && sha) return bad('sha256', '商店链接不需要填写 SHA-256');
  if (store && input.size_bytes !== null) return bad('size_bytes', '商店链接不需要填写文件大小');
  if (!store && !hex64.test(sha)) return bad('sha256', 'SHA-256 须为 64 位十六进制');
  if (input.size_bytes !== null && (input.size_bytes < 1 || input.size_bytes > maxDownloadBytes))
    return bad('size_bytes', '文件大小须为 1 字节到 8 GiB');
  if (cert) {
    if (input.format !== 'apk')
      return bad('signing_cert_sha256', '只有 Android 安装包需要填写签名证书指纹');
    if (!hex64.test(cert)) return bad('signing_cert_sha256', '签名证书指纹须为 64 位十六进制');
  }
  if (input.format === 'ipa') {
    if (bundle.length > 155 || !bundlePattern.test(bundle))
      return bad('bundle_id', '请填写 Bundle ID，例如 com.example.app');
  } else if (bundle) return bad('bundle_id', '只有 iOS 网页安装需要填写 Bundle ID');
  return null;
}

/** The body that saves a row again unchanged except for what the caller overrides. */
export function toInput(row: ClientDownload): DownloadInput {
  return {
    platform: row.platform,
    arch: row.arch,
    format: row.format,
    app_version: row.app_version,
    url: row.url,
    sha256: row.sha256 ?? '',
    size_bytes: row.size_bytes,
    signing_cert_sha256: row.signing_cert_sha256 ?? '',
    bundle_id: row.bundle_id ?? '',
    published: row.published,
    version: row.version,
  };
}
