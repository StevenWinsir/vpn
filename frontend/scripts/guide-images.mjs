// Turns the native apps' screenshot-suite pictures into the usage guide's pictures
// (handoff_guide.md 7.2). Development only, like brand:apps: the results are committed and CI never
// runs this; scripts/guide.test.mjs checks them against the manifest.
//
//   npm run guide:images -- --macos <dir> --windows <dir> --android <dir> --ios <dir> --manual <dir>
//
// Each directory is one suite's output (any subset may be given; the other platforms keep their
// current entries):
//   macos    artifacts/native-macos/<run>-ui            scripts/test-native-macos.py --suite ui
//   windows  the unpacked windows-ui-preview CI artifact scripts/test-native-windows.py --suite ui
//   android  artifacts/native-android/ui                scripts/test-native-android.py --suite ui (guide-*.png)
//   ios      artifacts/native-ios/ui/screenshots        scripts/test-native-ios.py --suite ui
//   manual   a folder of real-device system pictures    hand-taken (docs/user-guide.md section 5)
//
// A manual pick carries "manual": { "device", "system" } (what it was taken on); its pictures are
// processed on their own, so a suite run never touches them and a manual run never touches a suite's.

// The pick list is guide-shots.json (key, source scene, optional crop, output width, alt text).
// Chrome (Playwright; PLAYWRIGHT_CHROME_PATH for a local one) decodes, crops, scales and encodes
// WebP, so no image dependency is added. The outputs are public/guide/<platform>/<name>.<hash8>.webp
// and src/lib/guide-images.ts (the manifest the page and the tests read). Files under public/guide
// that the manifest does not name are deleted.
import { createHash } from 'node:crypto';
import {
  existsSync,
  mkdirSync,
  readdirSync,
  readFileSync,
  rmSync,
  statSync,
  writeFileSync,
} from 'node:fs';
import { join, relative } from 'node:path';
import { bannedIn } from '../tests/copy-words.ts';

const frontend = new URL('../', import.meta.url).pathname;
const publicDir = join(frontend, 'public');
const outRoot = join(publicDir, 'guide');
const manifestPath = join(frontend, 'src/lib/guide-images.ts');
const picks = JSON.parse(readFileSync(join(frontend, 'guide-shots.json'), 'utf8'));

export const QUALITY = 0.82;
const platforms = ['windows', 'macos', 'android', 'ios'];
const argNames = [...platforms, 'manual'];

const sha256 = (data) => createHash('sha256').update(data).digest('hex');

// Every address a picture may show: the fixtures' example domain only.
export function foreignDomains(text) {
  const found = text.match(/\b[a-z0-9-]+(?:\.[a-z0-9-]+)*\.[a-z]{2,}\b/gi) ?? [];
  return found.filter((domain) => !/(^|\.)example\.com$/i.test(domain));
}

/** The source check of handoff_guide.md 7.2 over a scene's visible texts (macOS and Windows). */
export function sourceProblems(texts) {
  const problems = [];
  for (const text of texts) {
    if (text.includes('开发版')) problems.push(`development marker: ${text}`);
    for (const domain of foreignDomains(text)) problems.push(`address ${domain}: ${text}`);
    for (const word of bannedIn(text)) problems.push(`"${word}": ${text}`);
  }
  return problems;
}

/** What a manual pick (a hand-taken system picture) must say about itself (handoff_guide.md 7.4). */
export function manualProblems(pick) {
  const problems = [];
  if (!pick.manual) return [`${pick.key}: not a manual pick`];
  for (const field of ['device', 'system'])
    if (typeof pick.manual[field] !== 'string' || pick.manual[field].trim().length < 3)
      problems.push(
        `${pick.key}: manual.${field} (the device and system it was taken on) is missing`,
      );
  if (!pick.scene?.startsWith('manual:'))
    problems.push(`${pick.key}: scene must read manual:<what it shows>`);
  return problems;
}

function parseArgs(argv) {
  const dirs = {};
  for (let i = 0; i < argv.length; i++) {
    const name = argv[i].replace(/^--/, '');
    if (!argNames.includes(name) || !argv[i + 1]) throw new Error(`unknown argument ${argv[i]}`);
    dirs[name] = argv[++i];
  }
  if (!Object.keys(dirs).length)
    throw new Error('give at least one of --windows --macos --android --ios --manual <dir>');
  return dirs;
}

// The scene texts of a suite that writes them (macOS, Windows): { scene: [text, …] }.
function sceneTexts(dir) {
  const file = join(dir, 'texts.json');
  return existsSync(file) ? JSON.parse(readFileSync(file, 'utf8')) : null;
}

async function encode(browser, png, crop, width) {
  const page = await browser.newPage();
  const result = await page.evaluate(
    async ({ base64, crop, width, quality }) => {
      const img = new Image();
      img.src = `data:image/png;base64,${base64}`;
      await img.decode();
      const [sx, sy, sw, sh] = crop ?? [0, 0, img.naturalWidth, img.naturalHeight];
      if (sx < 0 || sy < 0 || sx + sw > img.naturalWidth || sy + sh > img.naturalHeight)
        throw new Error(`crop ${crop} outside ${img.naturalWidth}x${img.naturalHeight}`);
      // Never enlarge: a smaller source keeps its own size.
      const w = Math.min(width, sw);
      const h = Math.round((sh * w) / sw);
      const canvas = document.createElement('canvas');
      canvas.width = w;
      canvas.height = h;
      const context = canvas.getContext('2d');
      context.imageSmoothingEnabled = true;
      context.imageSmoothingQuality = 'high';
      context.drawImage(img, sx, sy, sw, sh, 0, 0, w, h);
      const url = canvas.toDataURL('image/webp', quality);
      if (!url.startsWith('data:image/webp')) throw new Error('this browser cannot encode WebP');
      return { data: url.slice(url.indexOf(',') + 1), w, h };
    },
    { base64: png.toString('base64'), crop: crop ?? null, width, quality: QUALITY },
  );
  await page.close();
  return { webp: Buffer.from(result.data, 'base64'), width: result.w, height: result.h };
}

function readManifest() {
  if (!existsSync(manifestPath)) return {};
  const text = readFileSync(manifestPath, 'utf8');
  const marker = 'Record<string, GuideImage> = ';
  const json = text.slice(text.indexOf(marker) + marker.length, text.lastIndexOf(';'));
  return JSON.parse(json);
}

function writeManifest(entries) {
  const sorted = Object.fromEntries(
    Object.keys(entries)
      .sort()
      .map((key) => [key, entries[key]]),
  );
  writeFileSync(
    manifestPath,
    `// Generated by npm run guide:images (scripts/guide-images.mjs) from guide-shots.json; do not edit.
// Each picture of the usage guide: its file under public/, size, where it came from (suite scene
// and source SHA-256) and what it shows (handoff_guide.md 7.2).

export type GuideImage = {
  file: string;
  width: number;
  height: number;
  /** The widest the page shows it, in CSS pixels (the zoom shows it larger). */
  display: number;
  device: 'phone' | 'desktop' | 'menubar';
  platform: 'windows' | 'macos' | 'android' | 'ios';
  scene: string;
  /** Only a hand-taken system picture: the device and system it was taken on. */
  capture?: { device: string; system: string };
  sourceSha256: string;
  sha256: string;
  bytes: number;
  alt: string;
};

export const guideImages: Record<string, GuideImage> = ${JSON.stringify(sorted, null, 2)};
`,
  );
}

function walk(dir) {
  if (!existsSync(dir)) return [];
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    return statSync(path).isDirectory() ? walk(path) : [path];
  });
}

async function main() {
  const dirs = parseArgs(process.argv.slice(2));
  const entries = readManifest();
  const { chromium } = await import('@playwright/test');
  const browser = await chromium.launch(
    process.env.PLAYWRIGHT_CHROME_PATH
      ? { executablePath: process.env.PLAYWRIGHT_CHROME_PATH }
      : {},
  );
  const problems = [];
  try {
    for (const group of Object.keys(dirs)) {
      const dir = dirs[group];
      const manual = group === 'manual';
      // A suite run owns its platform's suite pictures; a manual run owns every manual picture.
      const owns = (entry) =>
        manual ? Boolean(entry.capture) : !entry.capture && entry.platform === group;
      const mine = (shot) =>
        manual ? Boolean(shot.manual) : !shot.manual && shot.platform === group;
      const texts = manual ? null : sceneTexts(dir);
      for (const key of Object.keys(entries)) if (owns(entries[key])) delete entries[key];
      for (const pick of picks.shots.filter(mine)) {
        const platform = pick.platform;
        if (manual) problems.push(...manualProblems(pick));
        const source = join(dir, pick.source);
        if (!existsSync(source)) {
          problems.push(`${pick.key}: ${source} is missing`);
          continue;
        }
        if (texts) {
          // A real-window capture has no texts of its own; it names the scene it shows.
          const name = pick.textsScene ?? pick.scene;
          const scene = texts[name];
          if (!scene) problems.push(`${pick.key}: no texts for scene ${name}`);
          else
            for (const problem of sourceProblems(scene)) problems.push(`${pick.key}: ${problem}`);
        }
        const png = readFileSync(source);
        const { webp, width, height } = await encode(browser, png, pick.crop, pick.width);
        const hash = sha256(webp);
        const name = pick.key.split('/')[1];
        const file = `/guide/${platform}/${name}.${hash.slice(0, 8)}.webp`;
        mkdirSync(join(outRoot, platform), { recursive: true });
        writeFileSync(join(publicDir, file), webp);
        entries[pick.key] = {
          file,
          width,
          height,
          // CSS pixels on the page: phones 280 wide, the rest at most their own size.
          display: pick.display ?? (pick.device === 'phone' ? 280 : width),
          device: pick.device,
          platform,
          scene: pick.scene,
          ...(manual
            ? { capture: { device: pick.manual.device, system: pick.manual.system } }
            : {}),
          sourceSha256: sha256(png),
          sha256: hash,
          bytes: webp.length,
          alt: pick.alt,
        };
        console.log(`${pick.key}  ${width}x${height}  ${(webp.length / 1024).toFixed(0)} KB`);
      }
    }
  } finally {
    await browser.close();
  }
  if (problems.length) {
    console.error(
      `guide:images: ${problems.length} problem(s), manifest not written:\n  ${problems.join('\n  ')}`,
    );
    process.exit(1);
  }
  // Only the picks of guide-shots.json stay, and only the files the manifest names.
  for (const key of Object.keys(entries))
    if (!picks.shots.some((shot) => shot.key === key)) delete entries[key];
  const keep = new Set(Object.values(entries).map((entry) => join(publicDir, entry.file)));
  for (const path of walk(outRoot))
    if (!keep.has(path)) {
      rmSync(path);
      console.log(`removed ${relative(frontend, path)}`);
    }
  writeManifest(entries);
  const total = Object.values(entries).reduce((sum, entry) => sum + entry.bytes, 0);
  console.log(`${Object.keys(entries).length} pictures, ${(total / 1024).toFixed(0)} KB in all`);
}

if (import.meta.url === `file://${process.argv[1]}`) await main();
