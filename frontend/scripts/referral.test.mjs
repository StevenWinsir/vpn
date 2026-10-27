import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  isReferralPage,
  isReferralSummary,
  normalizeReferralInput,
  referralBadge,
  referralField,
  referralReward,
} from '../src/lib/referral.ts';

const format = (bytes) => `${bytes / 1024 ** 3} GB`;
const item = (over) => ({
  email_masked: 'zh***@qq.com',
  registered_at: '2026-10-26T00:00:00Z',
  status: 'registered',
  reward_bytes: null,
  granted_bytes: 0,
  granted_at: null,
  forfeit_reason: '',
  referee_deleted: false,
  ...over,
});

test('input is upper-cased and loses spaces and hyphens, without ever being judged', () => {
  assert.equal(normalizeReferralInput(' ab2c-d3ef '), 'AB2CD3EF');
  assert.equal(normalizeReferralInput('ab2c d3ef'), 'AB2CD3EF');
  // Garbage stays garbage: only the backend decides, silently.
  assert.equal(normalizeReferralInput('0O1IL!!'), '0O1IL!!');
  assert.equal(normalizeReferralInput(''), '');
  assert.equal(normalizeReferralInput('A'.repeat(40)).length, 16);
});

test('referral_code is sent only to a backend that announced the programme, and only when set', () => {
  assert.deepEqual(referralField(true, 'ab2c-d3ef'), { referral_code: 'AB2CD3EF' });
  assert.deepEqual(referralField(true, '  '), {});
  assert.deepEqual(referralField(true, ''), {});
  assert.deepEqual(referralField(false, 'AB2CD3EF'), {});
  assert.deepEqual(referralField(undefined, 'AB2CD3EF'), {});
});

test('status labels follow the plan', () => {
  assert.deepEqual(referralBadge(item({}), format), { text: '已注册·未消费', color: 'gray' });
  assert.equal(
    referralBadge(item({ status: 'qualified', reward_bytes: 29 * 1024 ** 3 }), format).text,
    '待发放（约 29 GB）',
  );
  assert.equal(
    referralBadge(item({ status: 'rewarded', granted_bytes: 29 * 1024 ** 3 }), format).text,
    '已发放 +29 GB',
  );
  const forfeited = (forfeit_reason) =>
    referralBadge(item({ status: 'forfeited', forfeit_reason }), format).text;
  assert.equal(forfeited('referrer_no_plan'), '未生效（发放时无有效套餐）');
  assert.equal(forfeited('referrer_unavailable'), '未生效（账号不可用）');
  assert.equal(forfeited('zero_amount'), '未生效（消费金额为 0）');
  assert.equal(forfeited('something_new'), '未生效');
});

test('the reward column shows only amounts that exist', () => {
  assert.equal(referralReward(item({}), format), '—');
  assert.equal(referralReward(item({ status: 'forfeited' }), format), '—');
  assert.equal(
    referralReward(item({ status: 'qualified', reward_bytes: 2 * 1024 ** 3 }), format),
    '约 2 GB',
  );
  assert.equal(
    referralReward(item({ status: 'rewarded', granted_bytes: 3 * 1024 ** 3 }), format),
    '3 GB',
  );
});

test('malformed referral bodies are rejected so the card hides instead of breaking', () => {
  const stats = {
    registered_count: 0,
    purchased_count: 0,
    pending_count: 0,
    rewarded_count: 0,
    rewarded_bytes: 0,
    forfeited_count: 0,
  };
  const summary = {
    enabled: true,
    code: 'AB2CD3EF',
    share_path: '/register?ref=AB2CD3EF',
    reward_rule: { gib_per_yuan: 1, max_gib: 0 },
    has_active_plan: true,
    stats,
    current_bonus_bytes: 0,
  };
  assert.equal(isReferralSummary(summary), true);
  assert.equal(isReferralSummary({ ...summary, stats: { ...stats, rewarded_bytes: '0' } }), false);
  assert.equal(isReferralSummary({ ...summary, code: 7 }), false);
  assert.equal(isReferralSummary(null), false);
  assert.equal(isReferralSummary('<html>'), false);
  const page = { items: [item({})], total: 1, page: 1, page_size: 20 };
  assert.equal(isReferralPage(page), true);
  assert.equal(isReferralPage({ ...page, items: [{ email_masked: 1 }] }), false);
  assert.equal(isReferralPage({ items: 'x', total: 1, page_size: 20 }), false);
});
