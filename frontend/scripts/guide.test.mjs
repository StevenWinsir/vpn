// The usage guide's content and platform choice (handoff_guide.md 8.1): structure, links, copy
// vocabulary and the User-Agent rules. The picture checks arrive with the pictures (round 2).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  beforeYouStart,
  choosePlatform,
  commonFaq,
  detectPlatform,
  downloadAvailable,
  faqFor,
  guideLinkPaths,
  guideTexts,
  parseInline,
  platformIds,
  platforms,
  requiredSections,
} from '../src/lib/guides.ts';
import { bannedIn } from '../tests/copy-words.ts';

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

test('exactly one download step per platform and only Android and iPhone have a package key', () => {
  for (const id of platformIds) {
    const steps = platforms[id].sections.flatMap((section) =>
      section.steps.filter((step) => step.download),
    );
    assert.equal(steps.length, 1, id);
  }
  assert.equal(platforms.windows.downloadKey, null);
  assert.equal(platforms.macos.downloadKey, null);
  assert.equal(platforms.android.downloadKey, 'android');
  assert.equal(platforms.ios.downloadKey, 'ios');
});

test('download availability follows the release facts, never the desktop', () => {
  const downloads = { android: { version: '1.2.3' }, ios: null };
  assert.equal(downloadAvailable('android', downloads), true);
  assert.equal(downloadAvailable('ios', downloads), false);
  assert.equal(downloadAvailable('windows', downloads), false);
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

test('the platform: a valid query wins, otherwise the device decides', () => {
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
  assert.equal(choosePlatform('android', iphone), 'android');
  assert.equal(choosePlatform('macos', windows), 'macos');
  assert.equal(choosePlatform('ios', ''), 'ios');
  assert.equal(choosePlatform('IOS', iphone), 'ios');
  assert.equal(choosePlatform('linux', android), 'android');
  assert.equal(choosePlatform('', mac), 'macos');
  assert.equal(choosePlatform(null, windows), 'windows');
  assert.equal(choosePlatform(undefined, ''), 'windows');
});
