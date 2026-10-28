// Regenerates the icons of the four native apps from the one brand source, brand/mark.svg
// (handoff_app_standardization.md 4). Development only: the results are committed and CI never
// runs this; scripts/test-brand-assets.py checks them against apps/brand-manifest.json. After
// replacing the logo run
//   npm run brand:icons && npm run brand:apps
// (with PLAYWRIGHT_CHROME_PATH pointing at a local Chrome when Playwright has no browser of its
// own), then commit the changed files.
//
//   macOS    apps/macos/Branding         AppIcon.icns, menu-bar template images (3 states, 1x/2x)
//   Windows  apps/windows/AsterLink.App/Assets   AsterLink.ico, tray-{idle,live,error}.ico
//            apps/windows/Packaging      installer wizard small images (55 and 110 px)
//   iOS      apps/ios/AsterLink/Assets.xcassets  AppIcon (light, dark, tinted), LaunchMark
//   Android  apps/android/app/src/main/res/drawable  launcher foreground, notification icon
//
// Two shapes are derived from the source: the full mark (tile + shield) and the glyph (the shield
// only: the first element, the tile, is stripped). Nothing else is drawn by hand.
import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import { pathToFileURL } from 'node:url';
import { ico, parseSource, sourcePath } from './brand-icons.mjs';

const repo = new URL('../../', import.meta.url);
const at = (path) => new URL(path, repo);

// Colours that are not in the source come from the website tokens (globals.css).
const colors = {
  white: '#ffffff',
  brandDark: '#3dc89a', // --al-brand in the dark scheme
  neutral: '#66756f', // --al-text-3: the idle tray tile
  alert: '#e03131', // --al-unread: the error dot
};

// ---- the two shapes ---------------------------------------------------------------------------

export function shapes(svg) {
  const { viewBox, body, tile } = parseSource(svg);
  const rect = /^<rect\b[^>]*\/>/.exec(body)?.[0];
  if (!rect) throw new Error('brand/mark.svg: the tile must be a self-closing <rect>');
  return { viewBox, tile, rect, glyph: body.slice(rect.length) };
}

const svgOf = (viewBox, inner) =>
  `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${viewBox}">${inner}</svg>`;
const recolor = (glyph, from, to) => glyph.replaceAll(from, to);

// Glyph pieces for the menu bar, drawn from the same geometry as the source: the shield outline
// (first path), the stem (second path) and the dot (circle).
function glyphParts(glyph) {
  const paths = [...glyph.matchAll(/<path d="([^"]+)"/g)].map((m) => m[1]);
  const dot = /<circle cx="([\d.]+)" cy="([\d.]+)" r="([\d.]+)"/.exec(glyph);
  const width = /stroke-width="([\d.]+)"/.exec(glyph)?.[1];
  if (paths.length !== 2 || !dot || !width) throw new Error('brand/mark.svg: unexpected glyph');
  return { shield: paths[0], stem: paths[1], dot: dot.slice(1, 4), width };
}

// ---- rendering --------------------------------------------------------------------------------

const dataUrl = (svg) => 'data:image/svg+xml;base64,' + Buffer.from(svg).toString('base64');

async function shot(browser, size, html, background) {
  const page = await browser.newPage({ viewport: { width: size, height: size }, deviceScaleFactor: 1 });
  await page.setContent(
    `<html><body style="margin:0;background:${background ?? 'transparent'};width:${size}px;height:${size}px;overflow:hidden">${html}</body></html>`,
  );
  await page.evaluate(() => Promise.all([...document.images].map((img) => img.decode())));
  const png = await page.screenshot({ omitBackground: !background });
  await page.close();
  return png;
}

// Raw RGBA pixels of an SVG flattened onto an opaque background (for BMP files).
async function pixels(browser, svg, size, background) {
  const page = await browser.newPage({ viewport: { width: size, height: size }, deviceScaleFactor: 1 });
  await page.setContent('<html><body style="margin:0"></body></html>');
  const base64 = await page.evaluate(
    async ({ url, size, background }) => {
      const img = new Image();
      img.src = url;
      await img.decode();
      const canvas = document.createElement('canvas');
      canvas.width = canvas.height = size;
      const context = canvas.getContext('2d');
      context.fillStyle = background;
      context.fillRect(0, 0, size, size);
      context.drawImage(img, 0, 0, size, size);
      const data = context.getImageData(0, 0, size, size).data;
      let text = '';
      for (let i = 0; i < data.length; i += 0x8000) text += String.fromCharCode(...data.subarray(i, i + 0x8000));
      return btoa(text);
    },
    { url: dataUrl(svg), size, background },
  );
  await page.close();
  return Buffer.from(base64, 'base64');
}

const image = (svg, size, style = '') =>
  `<img src="${dataUrl(svg)}" width="${size}" height="${size}" style="display:block;${style}">`;

// ---- containers -------------------------------------------------------------------------------

// ICNS: 'icns', total length, then (type, length, PNG) entries. PNG payloads are valid for every
// type below on macOS 10.7+; the build needs neither iconutil nor a Mac to assemble it.
const icnsTypes = [
  ['icp4', 16], ['icp5', 32], ['ic11', 32], ['ic12', 64], ['ic07', 128],
  ['ic13', 256], ['ic08', 256], ['ic14', 512], ['ic09', 512], ['ic10', 1024],
];
export function icns(pngBySize) {
  const entries = icnsTypes.map(([type, size]) => {
    const data = pngBySize.get(size);
    const head = Buffer.alloc(8);
    head.write(type, 0, 'ascii');
    head.writeUInt32BE(data.length + 8, 4);
    return Buffer.concat([head, data]);
  });
  const total = 8 + entries.reduce((sum, entry) => sum + entry.length, 0);
  const head = Buffer.alloc(8);
  head.write('icns', 0, 'ascii');
  head.writeUInt32BE(total, 4);
  return Buffer.concat([head, ...entries]);
}

// 24-bit bottom-up BMP (the installer wizard takes BMP only).
export function bmp(rgba, size) {
  const row = Math.ceil((size * 3) / 4) * 4;
  const out = Buffer.alloc(54 + row * size);
  out.write('BM', 0, 'ascii');
  out.writeUInt32LE(out.length, 2);
  out.writeUInt32LE(54, 10);
  out.writeUInt32LE(40, 14);
  out.writeInt32LE(size, 18);
  out.writeInt32LE(size, 22);
  out.writeUInt16LE(1, 26);
  out.writeUInt16LE(24, 28);
  out.writeUInt32LE(row * size, 34);
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const from = ((size - 1 - y) * size + x) * 4;
      const to = 54 + y * row + x * 3;
      out[to] = rgba[from + 2];
      out[to + 1] = rgba[from + 1];
      out[to + 2] = rgba[from];
    }
  }
  return out;
}

// ---- Android vectors --------------------------------------------------------------------------

// The glyph as a VectorDrawable. Strokes, round caps and joins are kept as in the source; the
// circle becomes the equivalent path. `scale` enlarges the glyph inside the 108 dp adaptive
// canvas by shrinking the viewport (strokes then scale with it); a plain translate centres it.
export function androidGlyph(glyph, { size, scale, comment }) {
  const { shield, stem, dot, width } = glyphParts(glyph);
  const [cx, cy, r] = dot.map(Number);
  const viewport = +(size / scale).toFixed(6);
  const shift = +((viewport - 64) / 2).toFixed(6);
  const open = shift ? `    <group android:translateX="${shift}" android:translateY="${shift}">\n` : '';
  const close = shift ? '    </group>\n' : '';
  const pad = shift ? '    ' : '';
  const stroke = (d) =>
    `${pad}    <path\n${pad}        android:pathData="${d}"\n${pad}        android:strokeColor="#FFFFFFFF"\n${pad}        android:strokeLineCap="round"\n${pad}        android:strokeLineJoin="round"\n${pad}        android:strokeWidth="${width}" />\n`;
  return (
    `<?xml version="1.0" encoding="utf-8"?>\n<!-- ${comment} Generated by frontend/scripts/brand-apps.mjs from frontend/brand/mark.svg. Do not edit. -->\n` +
    `<vector xmlns:android="http://schemas.android.com/apk/res/android"\n    android:width="${size}dp"\n    android:height="${size}dp"\n    android:viewportWidth="${viewport}"\n    android:viewportHeight="${viewport}">\n` +
    open +
    stroke(shield) +
    stroke(stem) +
    `${pad}    <path\n${pad}        android:fillColor="#FFFFFFFF"\n${pad}        android:pathData="M${cx - r},${cy}a${r},${r} 0 1,0 ${2 * r},0a${r},${r} 0 1,0 ${-2 * r},0z" />\n` +
    close +
    '</vector>\n'
  );
}

// ---- main -------------------------------------------------------------------------------------

const sha256 = (data) => createHash('sha256').update(data).digest('hex');

async function main() {
  const { chromium } = await import('@playwright/test');
  const svg = readFileSync(sourcePath, 'utf8');
  const { viewBox, tile, rect, glyph } = shapes(svg);
  const full = svgOf(viewBox, rect + glyph);
  const manifest = [];
  const emit = (path, data, kind, extra = {}) => {
    const url = at(path);
    mkdirSync(dirname(url.pathname), { recursive: true });
    writeFileSync(url, data);
    manifest.push({ path, kind, ...extra, sha256: sha256(data) });
  };

  const browser = await chromium.launch(
    process.env.PLAYWRIGHT_CHROME_PATH ? { executablePath: process.env.PLAYWRIGHT_CHROME_PATH } : {},
  );
  try {
    // macOS: the 1024 canvas holds an 824 rounded tile with a soft shadow (the macOS icon grid).
    const macIcon = async (size) => {
      const tileSize = (size * 824) / 1024;
      const offset = (size * 100) / 1024;
      const shadow = `filter:drop-shadow(0 ${size * 0.0098}px ${size * 0.0195}px rgba(0,0,0,0.28))`;
      return shot(
        browser,
        size,
        `<div style="position:absolute;left:${offset}px;top:${offset}px">${image(full, tileSize, shadow)}</div>`,
      );
    };
    const macSizes = [...new Set(icnsTypes.map(([, size]) => size))];
    const macPngs = new Map();
    for (const size of macSizes) macPngs.set(size, await macIcon(size));
    emit('apps/macos/Branding/AppIcon.icns', icns(macPngs), 'icns', {
      sizes: macSizes,
      source: 'full mark on the macOS icon grid',
    });

    // macOS menu bar: black-on-transparent template images, 18 pt, 1x and 2x.
    const parts = glyphParts(glyph);
    const [dx, dy, dr] = parts.dot;
    const ink = '#000000';
    const outline = (inner) =>
      `<g fill="none" stroke="${ink}" stroke-linecap="round" stroke-linejoin="round" stroke-width="${parts.width}">${inner}</g>`;
    const menuStates = {
      Idle: outline(`<path d="${parts.shield}"/><path d="${parts.stem}"/>`) +
        `<circle cx="${dx}" cy="${dy}" r="${dr}" fill="${ink}"/>`,
      Active:
        `<mask id="m" maskUnits="userSpaceOnUse" x="0" y="0" width="64" height="64"><rect width="64" height="64" fill="#fff"/>` +
        `<path d="${parts.stem}" stroke="#000" stroke-width="${parts.width}" stroke-linecap="round" fill="none"/>` +
        `<circle cx="${dx}" cy="${dy}" r="${dr}" fill="#000"/></mask>` +
        `<path d="${parts.shield}" fill="${ink}" stroke="${ink}" stroke-width="${parts.width}" stroke-linecap="round" stroke-linejoin="round" mask="url(#m)"/>`,
      Error: outline(`<path d="${parts.shield}"/><path d="M32 22v9"/>`) +
        `<circle cx="32" cy="39" r="2.6" fill="${ink}"/>`,
    };
    for (const [name, inner] of Object.entries(menuStates)) {
      const art = svgOf('10 10 44 44', inner);
      for (const [scale, suffix] of [[1, ''], [2, '@2x']]) {
        const size = 18 * scale;
        emit(`apps/macos/Branding/MenuBar${name}Template${suffix}.png`, await shot(browser, size, image(art, size)), 'png', {
          width: size,
          height: size,
          source: 'glyph, template image',
        });
      }
    }

    // Windows: the app icon is the full mark (Windows applies no mask).
    const render = async (art, size) => ({ size, data: await shot(browser, size, image(art, size)) });
    const appSizes = [16, 20, 24, 32, 40, 48, 64, 96, 256];
    const appIcon = [];
    for (const size of appSizes) appIcon.push(await render(full, size));
    emit('apps/windows/AsterLink.App/Assets/AsterLink.ico', ico(appIcon), 'ico', {
      sizes: appSizes,
      source: 'full mark',
    });
    const traySizes = [16, 20, 24, 32];
    const trayArt = {
      idle: svgOf(viewBox, rect.replace(tile, colors.neutral) + glyph),
      live: full,
      error: svgOf(
        viewBox,
        rect + glyph + `<circle cx="50" cy="50" r="11" fill="${colors.alert}" stroke="#ffffff" stroke-width="2.5"/>`,
      ),
    };
    for (const [state, art] of Object.entries(trayArt)) {
      const frames = [];
      for (const size of traySizes) frames.push(await render(art, size));
      emit(`apps/windows/AsterLink.App/Assets/tray-${state}.ico`, ico(frames), 'ico', {
        sizes: traySizes,
        source: `full mark, ${state} state`,
      });
    }
    for (const size of [55, 110]) {
      emit(`apps/windows/Packaging/wizard-small-${size}.bmp`, bmp(await pixels(browser, full, size, '#ffffff'), size), 'bmp', {
        width: size,
        height: size,
        source: 'full mark on white',
      });
    }

    // iOS: the system rounds the light icon, so it is a full-bleed square; dark and tinted are
    // transparent with the glyph alone (the system supplies the backdrop).
    const squareTile = rect.replace(/\srx="[^"]*"/, '');
    const ios = 'apps/ios/AsterLink/Assets.xcassets';
    emit(`${ios}/AppIcon.appiconset/light.png`, await shot(browser, 1024, image(svgOf(viewBox, squareTile + glyph), 1024), tile), 'png', {
      width: 1024, height: 1024, opaque: true, source: 'full mark, square',
    });
    emit(`${ios}/AppIcon.appiconset/dark.png`, await shot(browser, 1024, image(svgOf(viewBox, recolor(glyph, colors.white, colors.brandDark)), 1024)), 'png', {
      width: 1024, height: 1024, source: 'glyph in the dark-scheme brand colour',
    });
    emit(`${ios}/AppIcon.appiconset/tinted.png`, await shot(browser, 1024, image(svgOf(viewBox, glyph), 1024)), 'png', {
      width: 1024, height: 1024, source: 'glyph, white (the system tints it)',
    });
    emit(`${ios}/LaunchMark.imageset/launch-mark.png`, await shot(browser, 240, image(full, 240)), 'png', {
      width: 240, height: 240, source: 'full mark',
    });

    // Android.
    const res = 'apps/android/app/src/main/res/drawable';
    emit(
      `${res}/ic_launcher_foreground.xml`,
      androidGlyph(glyph, { size: 108, scale: 1.4, comment: 'Launcher foreground: the brand glyph inside the 66 dp safe zone of the 108 dp adaptive canvas.' }),
      'android-vector',
      { source: 'glyph' },
    );
    emit(
      `${res}/ic_notification.xml`,
      androidGlyph(glyph, { size: 24, scale: 0.55, comment: 'Notification and Quick Settings icon: a white silhouette (the system only uses its alpha).' }),
      'android-vector',
      { source: 'glyph' },
    );
  } finally {
    await browser.close();
  }

  const body = {
    generator: 'frontend/scripts/brand-apps.mjs',
    source: { path: 'frontend/brand/mark.svg', sha256: sha256(readFileSync(sourcePath)) },
    files: manifest.sort((a, b) => a.path.localeCompare(b.path)),
  };
  writeFileSync(at('apps/brand-manifest.json'), JSON.stringify(body, null, 2) + '\n');
  console.log(`app icons regenerated from brand/mark.svg (${manifest.length} files)`);
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) await main();
