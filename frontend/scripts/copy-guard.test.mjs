// Commercial copy guard for the user pages (handoff_ui.md 4.6). The administration (/admin, the
// console components, admin-* components and their helpers) keeps its operator wording and is not
// scanned. A line may be exempted by a "copy-guard" marker on the same or the preceding line,
// with the reason; only code that never renders in production should need one.
import assert from 'node:assert/strict';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { test } from 'node:test';

const root = new URL('../src/', import.meta.url).pathname;

const excluded = [
  'app/admin/',
  'app/api/',
  'components/console/',
  'components/admin-',
  'lib/console.ts',
  'lib/plan-math.ts',
  'lib/server/',
];

// Development and implementation vocabulary that must not reach a user page.
const banned = [
  '开发',
  '联调',
  '测试',
  '模拟',
  '演示',
  '尚未接入',
  '待接入',
  '后端',
  '前端',
  '数据库',
  '快照',
  '服务端',
  '计量',
  '心跳',
  '联系管理员',
  '管理员配置',
];
// Wording that is not ours: Apple's own dialog and settings label for an enterprise-signed app.
const allowed = ['未受信任的开发者', '信任该开发者'];

function files(dir) {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) return files(path);
    return /\.(ts|tsx)$/.test(name) ? [path] : [];
  });
}

const userFiles = files(root)
  .map((path) => relative(root, path))
  .filter((path) => !excluded.some((prefix) => path.startsWith(prefix)));

// Comments are for developers. Whole-line comments are skipped; a trailing " // ..." is cut
// (a URL's "//" has no space before it).
function code(line) {
  const trimmed = line.trim();
  if (/^(\/\/|\/\*|\*|\{\/\*)/.test(trimmed)) return '';
  return line.replace(/\s\/\/\s.*$/, '').replace(/\/\*.*?\*\//g, '');
}

function findings() {
  const found = [];
  let exemptions = 0;
  for (const path of userFiles) {
    const lines = readFileSync(join(root, path), 'utf8').split('\n');
    lines.forEach((line, index) => {
      let text = code(line);
      for (const phrase of allowed) text = text.replaceAll(phrase, '');
      const hits = banned.filter((word) => text.includes(word));
      if (!hits.length) return;
      if (line.includes('copy-guard') || (index > 0 && lines[index - 1].includes('copy-guard'))) {
        exemptions++;
        return;
      }
      found.push(`${path}:${index + 1} [${hits.join(', ')}] ${line.trim()}`);
    });
  }
  return { found, exemptions };
}

test('the scan covers the user pages and leaves the administration out', () => {
  for (const path of [
    'app/page.tsx',
    'app/layout.tsx',
    'components/dashboard-view.tsx',
    'components/plans-view.tsx',
    'components/site-shell.tsx',
    'lib/api.ts',
  ])
    assert.ok(userFiles.includes(path), path);
  assert.ok(!userFiles.some((path) => path.startsWith('components/console/')));
  assert.ok(!userFiles.includes('components/admin-nodes-view.tsx'));
});

test('user pages carry no development wording', () => {
  const { found, exemptions } = findings();
  console.log(`copy-guard: ${userFiles.length} files, ${exemptions} exempted lines`);
  assert.deepEqual(found, []);
});

test('the four removed items stay removed (handoff_ui.md R2–R5)', () => {
  const all = files(root).map((path) => readFileSync(path, 'utf8'));
  for (const phrase of [
    '推荐有礼 · 我的推荐码',
    '线路信息，与管理员配置同步',
    '支持微信支付与支付宝支付',
    '开发联调版',
  ])
    assert.ok(!all.some((text) => text.includes(phrase)), phrase);
  assert.ok(!userFiles.includes('components/referral-card.tsx'));
});
