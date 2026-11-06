// The cross-page message (handoff_message.md D5, M1-14): only fixed keys, read once, dropped
// when old, malformed or unreadable; and the copy rules of D9 for every fixed message.
import assert from 'node:assert/strict';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { test } from 'node:test';
import {
  isNextPageToast,
  nextPageMaxAgeMs,
  nextPageStorageKey,
  nextPageToasts,
  takeNextPageToast,
  writeNextPageToast,
} from '../src/lib/toast-flash.ts';

function memory(initial = {}) {
  const data = new Map(Object.entries(initial));
  return {
    data,
    getItem: (key) => (data.has(key) ? data.get(key) : null),
    setItem: (key, value) => data.set(key, String(value)),
    removeItem: (key) => data.delete(key),
  };
}

test('a written key is shown once on the next page', () => {
  const store = memory();
  writeNextPageToast('signed-out', store, 1000);
  assert.deepEqual(JSON.parse(store.data.get(nextPageStorageKey)), { key: 'signed-out', at: 1000 });
  assert.equal(takeNextPageToast(store, 1500), 'signed-out');
  assert.equal(store.data.has(nextPageStorageKey), false);
  assert.equal(takeNextPageToast(store, 1600), null);
});

test('a later message replaces an earlier one', () => {
  const store = memory();
  writeNextPageToast('signed-out', store, 1000);
  writeNextPageToast('session-ended', store, 1001);
  assert.equal(takeNextPageToast(store, 1002), 'session-ended');
});

test('an old message, or one from the future, is dropped and removed', () => {
  for (const now of [1000 + nextPageMaxAgeMs + 1, 999]) {
    const store = memory();
    writeNextPageToast('account-deleted', store, 1000);
    assert.equal(takeNextPageToast(store, now), null);
    assert.equal(store.data.has(nextPageStorageKey), false);
  }
  const store = memory();
  writeNextPageToast('account-deleted', store, 1000);
  assert.equal(takeNextPageToast(store, 1000 + nextPageMaxAgeMs), 'account-deleted');
});

test('unknown keys, other shapes and broken JSON are dropped and removed', () => {
  const values = [
    '{"key":"<img src=x onerror=alert(1)>","at":1000}',
    '{"key":"toString","at":1000}',
    '{"key":"__proto__","at":1000}',
    '{"key":"signed-out"}',
    '{"key":"signed-out","at":"1000"}',
    '{"key":"signed-out","at":null}',
    '"signed-out"',
    '["signed-out",1000]',
    'null',
    '{',
    '',
  ];
  for (const value of values) {
    const store = memory({ [nextPageStorageKey]: value });
    assert.equal(takeNextPageToast(store, 1000), null, value);
    assert.equal(store.data.has(nextPageStorageKey), false, value);
  }
});

test('missing or throwing storage is silent', () => {
  assert.equal(takeNextPageToast(null, 1000), null);
  assert.doesNotThrow(() => writeNextPageToast('signed-out', null, 1000));
  const throwing = {
    getItem: () => {
      throw new Error('SecurityError');
    },
    setItem: () => {
      throw new Error('QuotaExceededError');
    },
    removeItem: () => {
      throw new Error('SecurityError');
    },
  };
  assert.equal(takeNextPageToast(throwing, 1000), null);
  assert.doesNotThrow(() => writeNextPageToast('signed-out', throwing, 1000));
});

test('the fixed messages follow the copy rules', () => {
  assert.deepEqual(Object.keys(nextPageToasts).sort(), [
    'account-deleted',
    'returned-admin',
    'session-ended',
    'signed-out',
  ]);
  for (const [key, copy] of Object.entries(nextPageToasts)) {
    assert.ok(isNextPageToast(key));
    assert.ok(['success', 'info', 'warning', 'error'].includes(copy.tone), key);
    assert.ok(Array.from(copy.title).length <= 10, `${key} title`);
    assert.ok(!/[。.！!]$/.test(copy.title), `${key} title ends without a full stop`);
    if (copy.message) assert.ok(Array.from(copy.message).length <= 30, `${key} message`);
    // The user pages never name the read-only view (copy-guard bans the word).
    assert.ok(!`${copy.title}${copy.message ?? ''}`.includes('模拟'), key);
  }
});

// Every message goes through src/lib/toast.tsx (handoff_message.md 4.1, M2-06): only it and the one
// container may import Mantine's notifications. The stylesheet import in the root layout is no call.
test('nothing but the toast module calls Mantine notifications', () => {
  const src = new URL('../src/', import.meta.url).pathname;
  const allowed = new Set(['lib/toast.tsx', 'components/toaster.tsx']);
  const walk = (dir) =>
    readdirSync(dir).flatMap((name) => {
      const path = join(dir, name);
      if (statSync(path).isDirectory()) return walk(path);
      return /\.(ts|tsx|js|jsx|mjs)$/.test(name) ? [path] : [];
    });
  const importer = /(?:from\s*|import\s*\(\s*|require\s*\(\s*)['"]@mantine\/notifications['"]/;
  const offenders = walk(src)
    .map((path) => relative(src, path))
    .filter((file) => !allowed.has(file))
    .filter((file) => importer.test(readFileSync(join(src, file), 'utf8')));
  assert.deepEqual(offenders, []);
  // The guard itself still sees the two allowed importers.
  for (const file of allowed) assert.ok(importer.test(readFileSync(join(src, file), 'utf8')), file);
});
