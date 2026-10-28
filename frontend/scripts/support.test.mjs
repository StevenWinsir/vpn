// The online support chat (handoff_ui.md 9): the server-side switch (D10) and the loader's state
// machine on a minimal fake browser, with fake timers. The real-browser behaviour is
// tests/support-widget.spec.ts.
import assert from 'node:assert/strict';
import { mock, test } from 'node:test';
import { crispSetting, defaultCrispWebsiteId } from '../src/lib/support-config.ts';

const id = '0f2b6c1e-3a4d-4e5f-8a9b-0c1d2e3f4a5b';

test('CRISP_WEBSITE_ID: unset uses the default, "off" disables, anything else invalid disables', () => {
  for (const value of [undefined, '', '   '])
    assert.deepEqual(crispSetting({ CRISP_WEBSITE_ID: value }), {
      websiteId: defaultCrispWebsiteId,
      problem: null,
    });
  assert.equal(defaultCrispWebsiteId, '83336a84-ae9f-4345-ba1f-583c0790002d');
  assert.deepEqual(crispSetting({ CRISP_WEBSITE_ID: ` ${id.toUpperCase()} ` }), {
    websiteId: id,
    problem: null,
  });
  for (const value of ['off', 'OFF', ' Off '])
    assert.deepEqual(crispSetting({ CRISP_WEBSITE_ID: value }), { websiteId: null, problem: null });
  for (const value of [
    'none',
    'false',
    id + 'x',
    id.slice(1),
    '{' + id + '}',
    'javascript:alert(1)',
  ]) {
    const setting = crispSetting({ CRISP_WEBSITE_ID: value });
    assert.equal(setting.websiteId, null, value);
    assert.match(setting.problem ?? '', /CRISP_WEBSITE_ID/);
    // The log line never repeats the value itself.
    assert.ok(!setting.problem.includes(value), value);
  }
});

// A fake page: enough of window/document for the loader, recording every $crisp call.
function fakePage({
  readyState = 'complete',
  visibility = 'visible',
  saveData = false,
  storage,
} = {}) {
  const listeners = { window: {}, document: {} };
  const on = (target) => (type, callback) => {
    (listeners[target][type] ??= []).push(callback);
  };
  const fire = (target, type) => {
    const list = listeners[target][type] ?? [];
    listeners[target][type] = [];
    for (const callback of list) callback();
  };
  const head = [];
  const store = storage ?? new Map();
  const window = {
    addEventListener: on('window'),
    requestIdleCallback: (callback) => setTimeout(callback, 50),
    localStorage: {
      getItem: (key) => {
        if (store === 'blocked') throw new Error('blocked');
        return store.get(key) ?? null;
      },
      setItem: (key, value) => {
        if (store === 'blocked') throw new Error('blocked');
        store.set(key, value);
      },
    },
  };
  const document = {
    readyState,
    visibilityState: visibility,
    addEventListener: on('document'),
    head: { appendChild: (element) => head.push(element) },
    createElement: () => {
      const element = {
        dataset: {},
        events: {},
        addEventListener(type, callback) {
          this.events[type] = callback;
        },
        remove() {
          head.splice(head.indexOf(element), 1);
        },
      };
      return element;
    },
  };
  // The loader measures with performance.now(); follow the fake clock.
  performance.now = () => Date.now();
  globalThis.window = window;
  globalThis.document = document;
  Object.defineProperty(globalThis, 'navigator', {
    value: { connection: { saveData } },
    configurable: true,
  });
  // The queue the loader fills before Crisp exists, read as plain commands.
  const calls = () =>
    (window.$crisp ?? []).map((command) =>
      command
        .filter((part) => typeof part !== 'function')
        .flat()
        .join(' '),
    );
  return { window, document, head, store, calls, fire };
}

let instance = 0;
async function loader() {
  return import(`../src/lib/support.ts?instance=${++instance}`);
}
const user = { id: 'u1', email: 'a@example.invalid', name: 'A' };

test('preload waits for load, four seconds, visibility and idle time, then injects once', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout', 'Date'] });
  const page = fakePage({ readyState: 'loading', visibility: 'hidden' });
  const support = await loader();
  support.activateSupport(id, user);
  support.activateSupport(id, user);
  t.mock.timers.tick(10000);
  assert.equal(page.head.length, 0, 'nothing before the load event');
  page.document.readyState = 'complete';
  page.fire('window', 'load');
  t.mock.timers.tick(10000);
  assert.equal(page.head.length, 0, 'nothing while the tab is hidden');
  page.document.visibilityState = 'visible';
  page.fire('document', 'visibilitychange');
  t.mock.timers.tick(100);
  assert.equal(page.head.length, 1);
  assert.equal(page.head[0].src, 'https://client.crisp.chat/l.js');
  assert.equal(page.head[0].async, true);
  assert.equal(page.window.CRISP_WEBSITE_ID, id);
  assert.deepEqual(page.window.CRISP_RUNTIME_CONFIG, { locale: 'zh' });
  assert.deepEqual(page.calls(), [
    'safe true',
    'do chat:hide',
    'on chat:opened',
    'on chat:closed',
    'on message:received',
    'set user:email a@example.invalid',
    'set user:nickname A',
  ]);
  assert.equal(page.store.get(support.supportOwnerKey), 'u1');
  support.suspendSupport();
  support.activateSupport(id, user);
  t.mock.timers.tick(30000);
  assert.equal(page.head.length, 1, 'never a second script');
});

test('the four seconds count from the first user page; save-data waits for a click', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout', 'Date'] });
  let page = fakePage();
  let support = await loader();
  support.activateSupport(id, user);
  t.mock.timers.tick(3000);
  assert.equal(page.head.length, 0);
  // Leaving and coming back does not restart the count.
  support.suspendSupport();
  support.activateSupport(id, user);
  t.mock.timers.tick(1000);
  t.mock.timers.tick(100);
  assert.equal(page.head.length, 1);

  page = fakePage({ saveData: true });
  support = await loader();
  support.activateSupport(id, user);
  t.mock.timers.tick(60000);
  assert.equal(page.head.length, 0, 'no preload with save-data');
  support.openSupport();
  assert.equal(page.head.length, 1, 'a click loads at once');
  assert.equal(support.supportSnapshot().requested, true);
});

test('ready, open, close and unread', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout', 'Date'] });
  const page = fakePage({ saveData: true });
  const support = await loader();
  support.activateSupport(id, user);
  support.openSupport();
  const handlers = Object.fromEntries(
    page.window.$crisp.filter((c) => c[0] === 'on').map((c) => [c[1], c[2]]),
  );
  // Crisp replaces the queue with its own object once it has loaded.
  const after = [];
  page.window.$crisp = { push: (command) => after.push(command.flat().join(' ')) };
  page.window.CRISP_READY_TRIGGER();
  assert.deepEqual(after, ['do chat:show', 'do chat:open']);
  assert.deepEqual(support.supportSnapshot(), {
    status: 'ready',
    requested: false,
    open: true,
    unread: false,
  });
  handlers['message:received']();
  assert.equal(support.supportSnapshot().unread, false, 'no dot while the chat is open');
  handlers['chat:closed']();
  assert.equal(after.at(-1), 'do chat:hide');
  handlers['message:received']();
  assert.equal(support.supportSnapshot().unread, true);
  support.openSupport();
  assert.equal(support.supportSnapshot().unread, false);
  assert.deepEqual(after.slice(-2), ['do chat:show', 'do chat:open']);
  // Leaving the user pages closes and hides it; signing out resets the conversation.
  support.suspendSupport();
  assert.deepEqual(after.slice(-2), ['do chat:close', 'do chat:hide']);
  assert.equal(support.supportSnapshot().open, false);
  support.endSupportSession();
  assert.ok(after.includes('do session:reset'));
  support.openSupport();
  assert.equal(after.at(-1), 'do chat:hide', 'nothing opens once signed out');
});

test('a timeout fails after 20 s; a late ready is accepted; an error retries with a new script', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout', 'Date'] });
  const page = fakePage({ saveData: true });
  const warn = mock.method(console, 'warn', () => {});
  const support = await loader();
  support.activateSupport(id, user);
  support.openSupport();
  t.mock.timers.tick(19900);
  assert.equal(support.supportSnapshot().status, 'loading');
  t.mock.timers.tick(200);
  assert.equal(support.supportSnapshot().status, 'failed');
  assert.equal(support.supportSnapshot().requested, true);
  // Retrying a script that is still pending waits again without a second copy.
  support.openSupport();
  assert.equal(support.supportSnapshot().status, 'loading');
  assert.equal(page.head.length, 1);
  page.window.CRISP_READY_TRIGGER();
  assert.equal(support.supportSnapshot().status, 'ready');
  assert.equal(support.supportSnapshot().open, true);

  const failing = fakePage({ saveData: true });
  const other = await loader();
  other.activateSupport(id, user);
  other.openSupport();
  failing.head[0].events.error();
  assert.equal(other.supportSnapshot().status, 'failed');
  other.dismissSupport();
  assert.equal(other.supportSnapshot().requested, false);
  other.openSupport();
  assert.equal(failing.head.length, 1, 'the failed script element is replaced, not added to');
  assert.equal(other.supportSnapshot().status, 'loading');
  assert.equal(failing.calls().filter((c) => c === 'safe true').length, 1, 'a fresh queue');
  assert.equal(warn.mock.callCount(), 0);
  warn.mock.restore();
});

test('a conversation of another (or an unknown) account is reset before identifying', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout', 'Date'] });
  const storage = new Map([['asterlink-support-owner', 'u0']]);
  let page = fakePage({ saveData: true, storage });
  let support = await loader();
  support.activateSupport(id, user);
  support.openSupport();
  assert.deepEqual(page.calls().slice(5), [
    'do session:reset',
    'set user:email a@example.invalid',
    'set user:nickname A',
  ]);
  // The same account again: its conversation continues.
  page = fakePage({ saveData: true, storage });
  support = await loader();
  support.activateSupport(id, user);
  support.openSupport();
  assert.ok(!page.calls().includes('do session:reset'));
  // Another account in the same page (already loaded): reset, then the new identity.
  support.activateSupport(id, { id: 'u2', email: 'b@example.invalid', name: '' });
  assert.deepEqual(page.calls().slice(-2), [
    'do session:reset',
    'set user:email b@example.invalid',
  ]);
  assert.equal(storage.get('asterlink-support-owner'), 'u2');
  // Unreadable storage: the owner is unknown, so the conversation always starts afresh.
  page = fakePage({ saveData: true, storage: 'blocked' });
  support = await loader();
  support.activateSupport(id, user);
  support.openSupport();
  assert.ok(page.calls().includes('do session:reset'));
});
