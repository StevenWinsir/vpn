// Static checks for the appearance work (handoff_ui.md 6.4, 7.2–7.4): colours live only in the
// token block of globals.css, both schemes define the same tokens, the icon set matches its one
// source (brand/mark.svg) and no route brings an icon of its own.
import assert from 'node:assert/strict';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { test } from 'node:test';
import { artModule, icoSizes, outputs, sourcePath } from './brand-icons.mjs';

const src = new URL('../src/', import.meta.url).pathname;
const css = readFileSync(join(src, 'app/globals.css'), 'utf8');

function files(dir) {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    return statSync(path).isDirectory() ? files(path) : [path];
  });
}

function tokenBlock() {
  const start = css.indexOf('tokens:start');
  const end = css.indexOf('tokens:end');
  assert.ok(start >= 0 && end > start, 'globals.css has one tokens:start … tokens:end block');
  assert.equal(css.indexOf('tokens:start', start + 1), -1);
  return { tokens: css.slice(start, end), rest: css.slice(0, start) + css.slice(end) };
}

function definitions(block, selector) {
  const at = block.indexOf(selector);
  assert.ok(at >= 0, selector);
  const body = block.slice(block.indexOf('{', at) + 1, block.indexOf('\n}', at));
  return new Set([...body.matchAll(/^\s*(--al-[\w-]+)\s*:/gm)].map((m) => m[1]));
}

test('outside the token block globals.css has no colour literals', () => {
  const { rest } = tokenBlock();
  const literal =
    /#[0-9a-fA-F]{3,8}\b|\b(rgba?|hsla?|hwb|lab|lch|oklab|oklch)\(|:\s*(white|black)\b|\s(white|black)\s*[;,)]/;
  const found = rest
    .replace(/\/\*[\s\S]*?\*\//g, (comment) => comment.replace(/[^\n]/g, ' '))
    .split('\n')
    .map((line, index) => [index + 1, line])
    .filter(([, line]) => literal.test(line))
    .map(([number, line]) => `globals.css:${number} ${line.trim()}`);
  assert.deepEqual(found, []);
  const code = css.replace(/\/\*[\s\S]*?\*\//g, '');
  assert.ok(!code.includes('light-dark('), 'no light-dark(): older browsers lack it (6.4)');
});

test('both schemes define the tokens the rules use', () => {
  const { tokens, rest } = tokenBlock();
  const light = definitions(tokens, ":root[data-mantine-color-scheme='light']");
  const dark = definitions(tokens, ":root[data-mantine-color-scheme='dark']");
  for (const name of dark) assert.ok(light.has(name), `${name} has a light value`);
  const used = new Set([...rest.matchAll(/var\((--al-[\w-]+)\)/g)].map((m) => m[1]));
  for (const name of used) assert.ok(light.has(name), `${name} is defined`);
  // Everything that differs between the schemes has a dark value; the rest is shared on purpose.
  for (const name of [
    '--al-bg',
    '--al-surface',
    '--al-border',
    '--al-text',
    '--al-text-3',
    '--al-brand',
    '--al-banner-bg',
  ])
    assert.ok(dark.has(name), `${name} has a dark value`);
});

test('the head script and the provider share one storage key and default to the system', () => {
  const layout = readFileSync(join(src, 'app/layout.tsx'), 'utf8');
  const providers = readFileSync(join(src, 'components/providers.tsx'), 'utf8');
  assert.match(
    layout,
    /<ColorSchemeScript defaultColorScheme="auto" localStorageKey=\{colorSchemeKey\}/,
  );
  assert.match(providers, /localStorageColorSchemeManager\(\{ key: colorSchemeKey \}\)/);
  assert.match(providers, /defaultColorScheme="auto"/);
  assert.ok(!/defaultColorScheme="light"/.test(layout + providers));
});

function png(buffer) {
  assert.equal(buffer.subarray(0, 8).toString('hex'), '89504e470d0a1a0a', 'PNG signature');
  return { width: buffer.readUInt32BE(16), height: buffer.readUInt32BE(20) };
}

test('the icon set is generated from brand/mark.svg', () => {
  const svg = readFileSync(sourcePath, 'utf8');
  assert.equal(readFileSync(outputs.svg, 'utf8'), svg, 'src/app/icon.svg is the source as is');
  assert.equal(readFileSync(outputs.art, 'utf8'), artModule(svg), 'brand-mark-art.ts is current');
  const ico = readFileSync(outputs.ico);
  assert.deepEqual(
    [ico.readUInt16LE(0), ico.readUInt16LE(2), ico.readUInt16LE(4)],
    [0, 1, icoSizes.length],
  );
  icoSizes.forEach((size, index) => {
    const entry = 6 + 16 * index;
    assert.equal(ico[entry], size);
    const data = ico.subarray(
      ico.readUInt32LE(entry + 12),
      ico.readUInt32LE(entry + 12) + ico.readUInt32LE(entry + 8),
    );
    assert.deepEqual(png(data), { width: size, height: size });
  });
  assert.deepEqual(png(readFileSync(outputs.apple)), { width: 180, height: 180 });
  assert.deepEqual(png(readFileSync(outputs.manifest192)), { width: 192, height: 192 });
  assert.deepEqual(png(readFileSync(outputs.manifest512)), { width: 512, height: 512 });
  const manifest = readFileSync(join(src, 'app/manifest.ts'), 'utf8');
  assert.ok(
    manifest.includes("'/icons/icon-192.png'") && manifest.includes("'/icons/icon-512.png'"),
  );
});

test('every route uses the root icons: none of its own, no icons metadata', () => {
  const app = join(src, 'app');
  const own = files(app)
    .map((path) => relative(app, path))
    .filter((path) => path.includes('/') && /(^|\/)(icon|apple-icon|favicon)\d*\.\w+$/.test(path));
  assert.deepEqual(own, []);
  const overrides = files(src)
    .filter((path) => /\.(ts|tsx)$/.test(path) && !path.endsWith('app/manifest.ts'))
    .filter((path) => /\bicons\s*:/.test(readFileSync(path, 'utf8')))
    .map((path) => relative(src, path));
  assert.deepEqual(overrides, []);
});
