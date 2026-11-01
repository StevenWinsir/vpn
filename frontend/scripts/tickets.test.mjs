import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import {
  badgeText,
  bodyError,
  categoryExtras,
  categoryLabel,
  categoryOptions,
  consoleStatus,
  daysLeft,
  deletionText,
  expiringSoon,
  isTicketDetail,
  isTicketList,
  isTicketSummary,
  localMinute,
  platformLabel,
  platformOptions,
  rateLimitText,
  subjectError,
  submitBlock,
  textLength,
  ticketLimits,
  ticketRefresh,
  userStatus,
  waitingText,
} from '../src/lib/tickets.ts';

// Support tickets, the pure parts (handoff_ticket.md 4.3, 7, 8, 11).
const day = 86400000;
const minute = 60000;
const now = Date.parse('2026-11-01T08:00:00Z');
const at = (ms) => new Date(now + ms).toISOString();

test('the five categories and six devices are the backend values, in display order', () => {
  assert.deepEqual(
    categoryOptions.map((c) => c.value),
    ['connection', 'account', 'billing', 'client', 'other'],
  );
  assert.deepEqual(
    platformOptions.map((p) => p.value),
    ['windows', 'macos', 'android', 'ios', 'web', 'other'],
  );
  assert.equal(categoryLabel('billing'), '套餐、订单与支付');
  assert.equal(categoryLabel('unknown'), 'unknown');
  assert.equal(platformLabel('macos'), 'macOS');
  assert.equal(platformLabel(''), '未填写');
});

test('a category opens the optional field support asks for most (D2)', () => {
  assert.deepEqual(categoryExtras('billing'), { platform: false, order: true });
  assert.deepEqual(categoryExtras('connection'), { platform: true, order: false });
  assert.deepEqual(categoryExtras('client'), { platform: true, order: false });
  assert.deepEqual(categoryExtras('account'), { platform: false, order: false });
  assert.deepEqual(categoryExtras(null), { platform: false, order: false });
});

test('user statuses name support "客服" and say who closed; the console has its own words', () => {
  assert.equal(userStatus('open').text, '等待客服回复');
  assert.equal(userStatus('answered').text, '客服已回复');
  assert.equal(userStatus('closed', 'user').text, '已关闭');
  assert.equal(userStatus('closed', 'staff').text, '已关闭（由客服关闭）');
  assert.equal(consoleStatus('open').text, '待处理');
  assert.equal(consoleStatus('answered').text, '已回复');
  assert.equal(consoleStatus('closed').text, '已关闭');
});

test('length counts Unicode characters after trimming, like the backend', () => {
  assert.equal(textLength('  abc \n'), 3);
  assert.equal(textLength('连接失败'), 4);
  // One astral character is one character (Go counts runes).
  assert.equal(textLength('😀😀'), 2);
  assert.equal(textLength('   '), 0);
});

test('subject: 1–60 characters on one line, no control characters', () => {
  assert.equal(subjectError('无法连接'), '');
  assert.equal(subjectError('  '), '请填写标题');
  assert.equal(subjectError('字'.repeat(60)), '');
  assert.equal(subjectError('字'.repeat(61)), '标题最多 60 个字');
  assert.equal(subjectError('第一行\n第二行'), '标题只能是一行文字');
  // Surrounding line breaks are trimmed away by both sides.
  assert.equal(subjectError('\n标题\n'), '');
  assert.equal(subjectError('a\u0007b'), '标题含有无法识别的字符');
});

test('body and notes: 1..max characters, line breaks and tabs allowed', () => {
  assert.equal(bodyError('第一行\n\t第二行'), '');
  assert.equal(bodyError('字'.repeat(3000)), '');
  assert.equal(bodyError('字'.repeat(3001)), '内容最多 3000 个字');
  assert.equal(bodyError('', ticketLimits.body, '问题描述'), '请填写问题描述');
  assert.equal(bodyError('x\u0000', ticketLimits.body, '回复'), '回复含有无法识别的字符');
  assert.equal(bodyError('\u0085', ticketLimits.body, '回复'), '回复含有无法识别的字符');
  assert.equal(bodyError('字'.repeat(501), ticketLimits.note, '关闭说明'), '关闭说明最多 500 个字');
  // HTML is just text: nothing to reject, nothing to interpret.
  assert.equal(bodyError('<script>alert(1)</script>'), '');
});

test('deletion date, days left and the three-day notice (handoff_ticket.md 8)', () => {
  const expires = at(30 * day);
  assert.match(deletionText(expires), /^将于 \d{4}-\d{2}-\d{2} \d{2}:\d{2} 自动删除$/);
  assert.equal(localMinute('2026-11-01T08:05:59Z').length, 16);
  assert.equal(daysLeft(expires, now), 30);
  assert.equal(daysLeft(at(day - 1), now), 0);
  assert.equal(daysLeft(at(-day), now), 0);
  assert.equal(expiringSoon(at(2 * day), now), true);
  assert.equal(expiringSoon(at(3 * day - 1), now), true);
  assert.equal(expiringSoon(at(3 * day), now), false);
  assert.equal(expiringSoon(at(0), now), false);
  assert.equal(expiringSoon(at(-minute), now), false);
});

test('waiting time in the console queue', () => {
  assert.equal(waitingText(null, now), '—');
  assert.equal(waitingText(at(-30000), now), '刚刚提交');
  assert.equal(waitingText(at(-5 * minute), now), '已等待 5 分钟');
  assert.equal(waitingText(at(-59 * minute), now), '已等待 59 分钟');
  assert.equal(waitingText(at(-60 * minute), now), '已等待 1 小时');
  assert.equal(waitingText(at(-47 * 60 * minute), now), '已等待 47 小时');
  assert.equal(waitingText(at(-3 * day), now), '已等待 3 天');
  // A clock slightly ahead of the browser never shows a negative wait.
  assert.equal(waitingText(at(5 * minute), now), '刚刚提交');
});

test('badges, rate limits and the open-ticket cap', () => {
  assert.equal(badgeText(0), '');
  assert.equal(badgeText(undefined), '');
  assert.equal(badgeText(1), '1');
  assert.equal(badgeText(9), '9');
  assert.equal(badgeText(10), '9+');
  assert.equal(rateLimitText(null), '操作太频繁，请稍后再试');
  assert.equal(rateLimitText(30), '操作太频繁，请于 1 分钟后再试');
  assert.equal(rateLimitText(3600), '操作太频繁，请于 60 分钟后再试');
  assert.equal(submitBlock(2), '');
  assert.match(submitBlock(3), /最多 3 张/);
  assert.match(submitBlock(5, 5), /最多 5 张/);
});

test('the refresh intervals of D7', () => {
  assert.deepEqual(ticketRefresh, {
    userDetail: 20000,
    userBadge: 60000,
    consoleList: 15000,
    consoleDetail: 10000,
    consoleBadge: 15000,
  });
});

test('unexpected bodies are recognised and refused', () => {
  const ticket = {
    id: '6f9619ff-8b86-4011-b42d-00c04fc964ff',
    ticket_no: 'T261101123456',
    category: 'billing',
    subject: '付款未到账',
    status: 'open',
    user_unread: false,
    message_count: 1,
    created_at: at(0),
    last_message_at: at(0),
    expires_at: at(30 * day),
  };
  const list = {
    tickets: [ticket],
    total: 1,
    page: 1,
    page_size: 20,
    open_count: 1,
    max_open: 3,
    order_options: [],
  };
  assert.ok(isTicketList(list));
  assert.ok(!isTicketList({ ...list, tickets: [{ id: 1 }] }));
  assert.ok(!isTicketList({ ...list, order_options: undefined }));
  assert.ok(!isTicketList('<html>'));
  assert.ok(isTicketDetail({ ...ticket, messages: [] }));
  assert.ok(!isTicketDetail(ticket));
  assert.ok(isTicketSummary({ unread_count: 0, open_count: 0, max_open: 3 }));
  assert.ok(!isTicketSummary({ open_count: 0 }));
  assert.ok(!isTicketSummary(null));
});

// The user side never names an administrator (D6, handoff_ticket.md 11.2): replies come from
// "客服". Comments are for developers and are skipped, as in copy-guard.
test('the user ticket pages never say "管理员"', () => {
  for (const file of ['../src/components/tickets-view.tsx', '../src/lib/tickets.ts']) {
    const text = readFileSync(new URL(file, import.meta.url), 'utf8')
      .split('\n')
      .filter((line) => !/^\s*(\/\/|\/\*|\*|\{\/\*)/.test(line))
      .map((line) => line.replace(/\s\/\/\s.*$/, ''))
      .join('\n');
    assert.ok(!text.includes('管理员'), file);
    assert.ok(text.includes('客服'), file);
    // Plain text only: no HTML injection anywhere on the user side.
    assert.ok(!text.includes('dangerouslySetInnerHTML'), file);
  }
});
