// The home page's pure helpers (handoff_ui.md 8): which plans the preview shows, the yearly saving
// hint, and the optional operator facts of the footer.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { previewPlans, yearlySavingPercent } from '../src/lib/home.ts';
import { icpLookupUrl, siteFacts } from '../src/lib/site.ts';

const plan = (id, extra = {}) => ({
  id,
  name: id,
  description: '',
  price_cents: 1000,
  duration_days: 30,
  traffic_bytes: 1024 ** 3,
  max_devices: 3,
  allow_dedicated: false,
  ...extra,
});
const tier = (discount, saving) => ({
  price_cents: 10000,
  original_cents: 12000,
  saving_cents: saving,
  discount_percent: discount,
  duration_days: 360,
  traffic_bytes: 12 * 1024 ** 3,
  monthly_equivalent_cents: 833,
});

test('the preview shows at most three plans, featured ones first, in the shop order', () => {
  const ids = (plans) => previewPlans(plans).map((p) => p.id);
  assert.deepEqual(ids([plan('a'), plan('b')]), ['a', 'b']);
  assert.deepEqual(ids([plan('a'), plan('b'), plan('c'), plan('d')]), ['a', 'b', 'c']);
  // A featured fourth plan pushes out the last ordinary one, but keeps its place in the order.
  assert.deepEqual(ids([plan('a'), plan('b'), plan('c'), plan('d', { featured: true })]), [
    'a',
    'b',
    'd',
  ]);
  assert.deepEqual(
    ids([
      plan('a'),
      plan('b', { featured: true }),
      plan('c', { featured: true }),
      plan('d', { featured: true }),
      plan('e', { featured: true }),
    ]),
    ['b', 'c', 'd'],
  );
  assert.deepEqual(ids([]), []);
});

test('the yearly hint uses the server figure, else the best discount, and needs a saving', () => {
  const response = (plans, max = 0) => ({
    plans,
    yearly_discount_percent: 100,
    max_yearly_saving_percent: max,
  });
  assert.equal(yearlySavingPercent(response([plan('a')])), null);
  assert.equal(yearlySavingPercent(response([plan('a', { yearly: null })])), null);
  assert.equal(yearlySavingPercent(response([plan('a', { yearly: tier(100, 0) })])), null);
  assert.equal(yearlySavingPercent(response([plan('a', { yearly: tier(85, 1800) })], 15)), 15);
  assert.equal(
    yearlySavingPercent(
      response([plan('a', { yearly: tier(90, 1200) }), plan('b', { yearly: tier(80, 2400) })]),
    ),
    20,
  );
});

test('the footer facts are shown only when well formed', () => {
  assert.deepEqual(siteFacts({}), { icpRecord: null, supportEmail: null });
  assert.deepEqual(
    siteFacts({
      SITE_ICP_RECORD: ' 京ICP备12345678号-1 ',
      SITE_SUPPORT_EMAIL: ' help@example.com ',
    }),
    { icpRecord: '京ICP备12345678号-1', supportEmail: 'help@example.com' },
  );
  for (const bad of ['<script>', 'x'.repeat(41), '京ICP备"123"', 'https://beian.miit.gov.cn/'])
    assert.equal(siteFacts({ SITE_ICP_RECORD: bad }).icpRecord, null, bad);
  for (const bad of [
    'help',
    'help@',
    '@example.com',
    'a b@example.com',
    'help@localhost',
    'x@y.com<',
  ])
    assert.equal(siteFacts({ SITE_SUPPORT_EMAIL: bad }).supportEmail, null, bad);
  assert.equal(icpLookupUrl, 'https://beian.miit.gov.cn/');
});
