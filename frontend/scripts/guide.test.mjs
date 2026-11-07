// The usage guide's content and platform choice (handoff_guide.md 8.1): structure, links, copy
// vocabulary, the User-Agent rules, and the pictures (round 2): manifest against files, budgets,
// highlights against the step texts.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import { guideImages } from '../src/lib/guide-images.ts';
import {
  beforeYouStart,
  commonFaq,
  detectPlatform,
  downloadAvailable,
  faqFor,
  guideLinkPaths,
  guidePath,
  guideTexts,
  legacyGuideTarget,
  markNumbers,
  parseInline,
  platformIds,
  platforms,
  requiredSections,
} from '../src/lib/guides.ts';
import { bannedIn } from '../tests/copy-words.ts';
import { manualProblems } from './guide-images.mjs';

const desktop = ['windows', 'macos'];

test('four platforms, each with every required section in order', () => {
  assert.deepEqual(platformIds, ['windows', 'macos', 'android', 'ios']);
  for (const id of platformIds) {
    const ids = platforms[id].sections.map((section) => section.id);
    assert.deepEqual(
      requiredSections.filter((name) => ids.includes(name)),
      [...requiredSections],
      id,
    );
    assert.equal(new Set(ids).size, ids.length, `${id} anchors are unique`);
    assert.ok(!ids.includes('faq') && !ids.includes('help'), `${id} leaves the page anchors alone`);
    for (const section of platforms[id].sections) {
      assert.ok(section.title && section.steps.length > 0, `${id}/${section.id}`);
      for (const step of section.steps) assert.ok(step.title.trim() && step.body.trim());
    }
    assert.ok(platforms[id].faq.length >= 1, id);
    assert.ok(faqFor(id).length > commonFaq(id).length - 1);
  }
});

test('exactly one download step per platform, each pointing at its own download card', () => {
  for (const id of platformIds) {
    const steps = platforms[id].sections.flatMap((section) =>
      section.steps.filter((step) => step.download),
    );
    assert.equal(steps.length, 1, id);
    assert.equal(platforms[id].downloadKey, id);
  }
});

test('download availability follows the published entries, the desktop included', () => {
  const downloads = {
    windows: { version: '1.2.4' },
    macos: null,
    android: { version: '1.2.3' },
    ios: null,
  };
  assert.equal(downloadAvailable('android', downloads), true);
  assert.equal(downloadAvailable('ios', downloads), false);
  assert.equal(downloadAvailable('windows', downloads), true);
  assert.equal(downloadAvailable('macos', downloads), false);
});

test('inline text: emphasis and links parse, and every link stays on the site', () => {
  assert.deepEqual(parseInline('a **b** [c](/plans) d'), [
    { type: 'text', text: 'a ' },
    { type: 'strong', text: 'b' },
    { type: 'text', text: ' ' },
    { type: 'link', text: 'c', href: '/plans' },
    { type: 'text', text: ' d' },
  ]);
  const links = [];
  for (const id of platformIds)
    for (const text of guideTexts(id))
      for (const part of parseInline(text)) if (part.type === 'link') links.push(part.href);
  for (const item of beforeYouStart)
    for (const part of parseInline(item.body)) if (part.type === 'link') links.push(part.href);
  assert.ok(links.length > 5);
  for (const href of links) assert.ok(guideLinkPaths.includes(href), href);
});

test('guide texts carry no development wording and no unprovable claim', () => {
  for (const id of platformIds)
    for (const text of guideTexts(id)) assert.deepEqual(bannedIn(text), [], `${id}: ${text}`);
  for (const item of beforeYouStart) assert.deepEqual(bannedIn(item.title + item.body), []);
});

test('the desktop guides use the clients’ own words, not the hidden technical ones', () => {
  const words = ['VPN', '内核', '协议', 'TUN', 'Helper', 'Mihomo', 'Clash', 'DNS', '端口'];
  for (const id of desktop)
    for (const text of guideTexts(id))
      for (const word of words)
        assert.ok(!text.toUpperCase().includes(word.toUpperCase()), `${id}: ${text}`);
  for (const id of platformIds)
    for (const text of guideTexts(id)) assert.ok(!text.includes('开发版'), `${id}: ${text}`);
});

test('no address or e-mail of ours is written into a guide', () => {
  for (const id of platformIds)
    for (const text of guideTexts(id)) {
      assert.ok(!/https?:\/\//i.test(text), text);
      assert.ok(!/@/.test(text), text);
    }
});

test('the device decides the recommendation; old ?platform= addresses find their page (handoff_seo.md D5)', () => {
  const iphone =
    'Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1';
  const android =
    'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Mobile Safari/537.36';
  const mac =
    'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15';
  const windows =
    'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36';
  assert.equal(detectPlatform(iphone), 'ios');
  assert.equal(detectPlatform(android), 'android');
  assert.equal(detectPlatform(mac), 'macos');
  assert.equal(detectPlatform(windows), 'windows');
  assert.equal(detectPlatform('Mozilla/5.0 (X11; Linux x86_64)'), 'windows');
  assert.equal(detectPlatform(''), 'windows');
  assert.deepEqual(platformIds.map(guidePath), [
    '/guide/windows',
    '/guide/macos',
    '/guide/android',
    '/guide/ios',
  ]);
  assert.equal(legacyGuideTarget('macos'), '/guide/macos');
  assert.equal(legacyGuideTarget(['ios', 'android']), '/guide/ios');
  // Unknown, differently written or empty values go to the overview.
  for (const value of ['IOS', 'linux', '', ['xyz']])
    assert.equal(legacyGuideTarget(value), '/guide');
  assert.equal(legacyGuideTarget(undefined), null);
});

// ---- pictures (handoff_guide.md 7.2, 8.1) ------------------------------------------------------

const frontend = new URL('../', import.meta.url).pathname;
const publicDir = join(frontend, 'public');
const picks = JSON.parse(readFileSync(join(frontend, 'guide-shots.json'), 'utf8')).shots;
const steps = platformIds.flatMap((id) =>
  platforms[id].sections.flatMap((section) =>
    section.steps.map((step) => ({ id, where: `${id}/${section.id}/${step.title}`, step })),
  ),
);

/** Width and height from a WebP header (lossy, lossless or extended). */
function webpSize(data) {
  assert.equal(data.toString('ascii', 0, 4), 'RIFF');
  assert.equal(data.toString('ascii', 8, 12), 'WEBP');
  const chunk = data.toString('ascii', 12, 16);
  if (chunk === 'VP8 ') return [data.readUInt16LE(26) & 0x3fff, data.readUInt16LE(28) & 0x3fff];
  if (chunk === 'VP8L') {
    const bits = data.readUInt32LE(21);
    return [(bits & 0x3fff) + 1, ((bits >> 14) & 0x3fff) + 1];
  }
  if (chunk === 'VP8X') return [data.readUIntLE(24, 3) + 1, data.readUIntLE(27, 3) + 1];
  throw new Error(`unknown WebP chunk ${chunk}`);
}

function files(dir) {
  if (!existsSync(dir)) return [];
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    return statSync(path).isDirectory() ? files(path) : [path];
  });
}

test('every picture of the pick list is in the manifest, and every step picture is a pick', () => {
  const keys = picks.map((pick) => pick.key);
  assert.equal(new Set(keys).size, keys.length, 'pick keys are unique');
  assert.deepEqual(Object.keys(guideImages).sort(), [...keys].sort());
  for (const pick of picks) {
    const entry = guideImages[pick.key];
    assert.equal(pick.key.split('/')[0], pick.platform, pick.key);
    assert.equal(entry.platform, pick.platform, pick.key);
    assert.equal(entry.scene, pick.scene, `${pick.key}: traceable to its suite scene`);
    assert.equal(entry.device, pick.device, pick.key);
    assert.equal(entry.alt, pick.alt, pick.key);
    assert.ok(entry.alt.trim().length >= 8, `${pick.key}: alt text describes the picture`);
    assert.deepEqual(bannedIn(entry.alt), [], `${pick.key}: ${entry.alt}`);
    if (pick.platform === 'windows' || pick.platform === 'macos')
      assert.ok(!/VPN|内核|协议/i.test(entry.alt), `${pick.key}: ${entry.alt}`);
    assert.match(entry.sourceSha256, /^[0-9a-f]{64}$/, `${pick.key}: source hash recorded`);
    assert.ok(entry.file.startsWith(`/guide/${pick.platform}/`), pick.key);
  }
  for (const { id, where, step } of steps)
    if (step.shot) {
      assert.ok(guideImages[step.shot], `${where}: ${step.shot} is in the manifest`);
      assert.equal(guideImages[step.shot].platform, id, `${where}: its own platform's picture`);
    }
  // Every pick is shown somewhere.
  const used = new Set(steps.map(({ step }) => step.shot).filter(Boolean));
  for (const key of keys) assert.ok(used.has(key), `${key} is used by a step`);
});

test('the files match the manifest: bytes, hash, size, name, budgets, nothing extra', () => {
  let total = 0;
  for (const [key, entry] of Object.entries(guideImages)) {
    const path = join(publicDir, entry.file);
    assert.ok(existsSync(path), `${key}: ${entry.file} exists`);
    const data = readFileSync(path);
    const hash = createHash('sha256').update(data).digest('hex');
    assert.equal(data.length, entry.bytes, `${key}: bytes`);
    assert.equal(hash, entry.sha256, `${key}: sha256`);
    assert.ok(
      entry.file.endsWith(`.${hash.slice(0, 8)}.webp`),
      `${key}: the name carries the hash`,
    );
    assert.deepEqual(webpSize(data), [entry.width, entry.height], `${key}: size`);
    assert.ok(entry.bytes <= 200 * 1024, `${key}: ${entry.bytes} bytes is over 200 KB`);
    assert.ok(entry.display > 0 && entry.display <= entry.width, `${key}: display width`);
    if (entry.device === 'phone') assert.ok(entry.width <= 720, `${key}: phone output width`);
    else assert.ok(entry.width <= 1440, `${key}: desktop output width`);
    total += entry.bytes;
  }
  assert.ok(total <= 4 * 1024 * 1024, `${total} bytes in all is over 4 MB`);
  const named = new Set(Object.values(guideImages).map((entry) => join(publicDir, entry.file)));
  for (const path of files(join(publicDir, 'guide')))
    assert.ok(named.has(path), `${path} is not in the manifest`);
});

test('highlights sit inside their picture and the step text names exactly their numbers', () => {
  let marks = 0;
  for (const { where, step } of steps) {
    const highlights = step.highlights ?? [];
    const named = markNumbers(step.body);
    if (!step.shot) assert.equal(highlights.length, 0, `${where}: highlights need a picture`);
    assert.deepEqual(
      named,
      highlights.map((mark) => mark.n).sort((a, b) => a - b),
      `${where}: the text names ${named.join(',') || 'none'}`,
    );
    const numbers = highlights.map((mark) => mark.n);
    assert.deepEqual(
      numbers,
      numbers.map((_, index) => index + 1),
      `${where}: numbered 1, 2, 3 … in order`,
    );
    for (const mark of highlights) {
      const [x, y, w, h] = mark.rect;
      for (const value of mark.rect)
        assert.ok(value >= 0 && value <= 100, `${where}: ${mark.rect}`);
      assert.ok(w > 0 && h > 0 && x + w <= 100 && y + h <= 100, `${where}: ${mark.rect} overflows`);
      marks += 1;
    }
  }
  for (const item of beforeYouStart) assert.deepEqual(markNumbers(item.body), []);
  for (const id of platformIds)
    for (const item of faqFor(id)) assert.deepEqual(markNumbers(item.a), [], item.q);
  assert.ok(marks >= 20, `${marks} highlights in all`);
});

test('the four platforms all have pictures, and each one leads off its app steps with one', () => {
  for (const id of platformIds) {
    const shots = steps.filter((entry) => entry.id === id && entry.step.shot);
    assert.ok(shots.length >= 6, `${id}: ${shots.length} pictures`);
    // The phones' first authorization is a system dialog: a real-device picture (pendingManual).
    const app = id === 'android' || id === 'ios' ? [] : ['authorize'];
    for (const section of [...app, 'login', 'lines', 'connect', 'usage'])
      assert.ok(
        platforms[id].sections.find((s) => s.id === section).steps.some((step) => step.shot),
        `${id}/${section} has a picture`,
      );
  }
});

// ---- hand-taken system pictures (handoff_guide.md 7.4, round 3) ---------------------------------

const pending = JSON.parse(readFileSync(join(frontend, 'guide-shots.json'), 'utf8')).pendingManual;

test('a manual picture says what it was taken on, and only manual pictures say so', () => {
  const good = {
    key: 'ios/x',
    manual: { device: 'iPhone 15', system: 'iOS 17.5' },
    scene: 'manual:x',
  };
  assert.deepEqual(manualProblems(good), []);
  assert.equal(manualProblems({ ...good, manual: { device: 'iPhone 15' } }).length, 1);
  assert.equal(manualProblems({ ...good, scene: 'x' }).length, 1);
  assert.equal(manualProblems({ key: 'ios/x', scene: 'x' }).length, 1);
  for (const pick of picks) {
    const entry = guideImages[pick.key];
    if (pick.manual) {
      assert.deepEqual(manualProblems(pick), [], pick.key);
      assert.deepEqual(entry.capture, pick.manual, `${pick.key}: capture recorded in the manifest`);
    } else assert.equal(entry.capture, undefined, `${pick.key}: a suite picture has no capture`);
  }
});

test('the pictures still to be taken name a real step, and no taken picture is still listed', () => {
  assert.ok(pending.length >= 1 || picks.some((pick) => pick.manual));
  const keys = pending.map((item) => item.key);
  assert.equal(new Set(keys).size, keys.length, 'pending keys are unique');
  for (const item of pending) {
    assert.ok(platformIds.includes(item.platform), item.key);
    assert.equal(item.key.split('/')[0], item.platform, item.key);
    assert.ok(!picks.some((pick) => pick.key === item.key), `${item.key} is already taken`);
    assert.ok(item.what.trim().length >= 10, `${item.key}: what to capture`);
    const section = platforms[item.platform].sections.find((s) => s.id === item.section);
    assert.ok(section, `${item.key}: section ${item.section}`);
    const step = section.steps.find((s) => s.title === item.step);
    assert.ok(step, `${item.key}: step ${item.step}`);
    assert.ok(!step.shot, `${item.key}: ${item.step} already has a picture`);
    // A system picture is shown as the step's picture once it exists, so the step stays text-only now.
    assert.deepEqual(bannedIn(item.what), [], item.key);
  }
});
