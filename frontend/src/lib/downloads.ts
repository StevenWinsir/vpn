// The Android APK the /download page offers. Read from the server environment on every request
// (frontend/.env, written from artifacts/native-android/release-private/website-env.txt by
// scripts/package-native-android.py), so a new release needs a restart, not a rebuild. Any
// missing or malformed value hides the download rather than offering a link that is wrong.

export type AndroidDownload = {
  url: string;
  version: string;
  sha256: string;
  sizeBytes: number | null;
  certSha256: string | null;
  sourceUrl: string | null;
};

type Env = Record<string, string | undefined>;

const HEX64 = /^[0-9a-f]{64}$/;
const VERSION = /^\d+\.\d+\.\d+([.-][0-9A-Za-z.-]+)?$/;

function safeURL(value: string | undefined): string | null {
  if (!value) return null;
  try {
    const url = new URL(value);
    const loopback = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname);
    if (url.protocol !== 'https:' && !(url.protocol === 'http:' && loopback)) return null;
    if (url.username || url.password) return null;
    return url.toString();
  } catch {
    return null;
  }
}

export function androidDownload(env: Env): AndroidDownload | null {
  const url = safeURL(env.ANDROID_APK_URL?.trim());
  const version = env.ANDROID_APK_VERSION?.trim() ?? '';
  const sha256 = env.ANDROID_APK_SHA256?.trim().toLowerCase() ?? '';
  if (!url || !VERSION.test(version) || !HEX64.test(sha256) || !url.toLowerCase().endsWith('.apk'))
    return null;
  const size = Number(env.ANDROID_APK_SIZE_BYTES?.trim());
  const cert = env.ANDROID_CERT_SHA256?.trim().toLowerCase().replaceAll(':', '') ?? '';
  return {
    url,
    version,
    sha256,
    sizeBytes: Number.isSafeInteger(size) && size > 0 ? size : null,
    certSha256: HEX64.test(cert) ? cert : null,
    sourceUrl: safeURL(env.ANDROID_SOURCE_URL?.trim()),
  };
}

export type IosDownload = {
  /** The signed IPA (also offered as a plain download, e.g. for Apple Configurator or a MDM). */
  ipaUrl: string;
  /** manifest.plist, which iOS needs to install an IPA from a web page. */
  manifestUrl: string;
  version: string;
  sha256: string;
  sizeBytes: number | null;
  sourceUrl: string | null;
};

/**
 * The signed iOS build, from IOS_* in frontend/.env (printed by scripts/ios/resign-ipa.py). Over-the-air
 * installs only work over HTTPS, so unlike Android no loopback exception exists here.
 */
export function iosDownload(env: Env): IosDownload | null {
  const ipaUrl = safeURL(env.IOS_IPA_URL?.trim());
  const manifestUrl = safeURL(env.IOS_MANIFEST_URL?.trim());
  const version = env.IOS_IPA_VERSION?.trim() ?? '';
  const sha256 = env.IOS_IPA_SHA256?.trim().toLowerCase() ?? '';
  if (!ipaUrl || !manifestUrl || !VERSION.test(version) || !HEX64.test(sha256)) return null;
  if (!ipaUrl.startsWith('https://') || !manifestUrl.startsWith('https://')) return null;
  if (!ipaUrl.toLowerCase().endsWith('.ipa') || !manifestUrl.toLowerCase().endsWith('.plist'))
    return null;
  const size = Number(env.IOS_IPA_SIZE_BYTES?.trim());
  return {
    ipaUrl,
    manifestUrl,
    version,
    sha256,
    sizeBytes: Number.isSafeInteger(size) && size > 0 ? size : null,
    sourceUrl: safeURL(env.IOS_SOURCE_URL?.trim()),
  };
}

/** The link that makes Safari on an iPhone offer to install the app. */
export function iosInstallLink(download: IosDownload): string {
  return `itms-services://?action=download-manifest&url=${encodeURIComponent(download.manifestUrl)}`;
}

export function megabytes(bytes: number): string {
  return `${(bytes / 2 ** 20).toFixed(1)} MB`;
}

/** 64 hex digits in groups of 4 for reading aloud or comparing by eye. */
export function grouped(hex: string): string {
  return hex.match(/.{1,4}/g)?.join(' ') ?? hex;
}
