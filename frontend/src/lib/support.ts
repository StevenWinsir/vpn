// The online support chat loader (handoff_ui.md 9). One module-level instance for the whole life
// of the page, so React strict mode, client navigation and remounts never inject Crisp twice.
//
// Crisp is far away for most users, so it must never compete with the page: nothing is requested
// until the page has loaded, the tab is visible, about four seconds have passed since the first
// user page and the browser is idle (or until the user asks for the chat). Its own bubble stays
// hidden; the site's launcher opens the chat and the chat hides again when it is closed.
//
// Crisp JavaScript SDK calls used (docs.crisp.chat, Web SDK, $crisp methods):
//   ["safe", true]                         no exceptions from $crisp
//   ["do", "chat:hide" | "chat:show" | "chat:open" | "chat:close"]
//   ["do", "session:reset"]                unbinds this browser from the previous conversation
//   ["set", "user:email", [email]], ["set", "user:nickname", [name]]
//   ["on", "chat:opened" | "chat:closed" | "message:received", callback]
//   window.CRISP_WEBSITE_ID, window.CRISP_RUNTIME_CONFIG = { locale }, window.CRISP_READY_TRIGGER
// Every call goes through the $crisp queue, which Crisp replays once it has loaded.

export const crispScriptUrl = 'https://client.crisp.chat/l.js';
const readyTimeoutMs = 20000;
const preloadDelayMs = 4000;
const idleTimeoutMs = 2000;
// The account the browser's Crisp conversation belongs to, so the next account never sees it.
export const supportOwnerKey = 'asterlink-support-owner';
// Storage that cannot be read: the owner is unknown, so every load starts a fresh conversation.
const unknownOwner = '\u0000unknown';

export type SupportStatus = 'idle' | 'loading' | 'ready' | 'failed';
export type SupportState = {
  status: SupportStatus;
  // The user asked for the chat and is waiting for it (connecting, or shown the fallback card).
  requested: boolean;
  // The Crisp chat window is open; the site's launcher steps aside meanwhile.
  open: boolean;
  unread: boolean;
};
export type SupportUser = { id: string; email: string; name: string };

type CrispQueue = { push: (command: unknown[]) => unknown };
type CrispWindow = Window & {
  $crisp?: CrispQueue;
  CRISP_WEBSITE_ID?: string;
  CRISP_RUNTIME_CONFIG?: { locale: string };
  CRISP_READY_TRIGGER?: () => void;
};

const initial: SupportState = { status: 'idle', requested: false, open: false, unread: false };
let state = initial;
const listeners = new Set<() => void>();
function update(patch: Partial<SupportState>) {
  state = { ...state, ...patch };
  for (const listener of [...listeners]) listener();
}
export function subscribeSupport(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
export const supportSnapshot = () => state;
export const supportServerSnapshot = () => initial;

let websiteId: string | null = null;
// A signed-in ordinary user is on a user page; outside that the chat stays hidden.
let active = false;
let identity: SupportUser | null = null;
let identified: string | null = null;
let owner: string | null | undefined;
let script: HTMLScriptElement | null = null;
let scriptFailed = false;
let readyTimer: ReturnType<typeof setTimeout> | undefined;
let firstActiveAt: number | null = null;
let preloadPlanned = false;
let warned = false;

function warnOnce(text: string, error?: unknown) {
  if (warned) return;
  warned = true;
  console.warn(text, error ?? '');
}

function crisp(...command: unknown[]) {
  try {
    (window as CrispWindow).$crisp?.push(command);
  } catch (error) {
    warnOnce('Online support call failed', error);
  }
}

function currentOwner() {
  if (owner === undefined) {
    try {
      owner = window.localStorage.getItem(supportOwnerKey);
    } catch {
      owner = unknownOwner;
    }
  }
  return owner;
}
function rememberOwner(id: string) {
  owner = id;
  try {
    window.localStorage.setItem(supportOwnerKey, id);
  } catch {
    // Unreadable storage: the next page load resets the conversation instead.
  }
}

// Ties the Crisp conversation to the signed-in account: a conversation of another account (or of
// an unknown one) is reset first. Email and nickname help the agents; they are not verified.
function pushIdentity() {
  if (!identity || identified === identity.id) return;
  const previous = identified ?? currentOwner();
  if (previous !== null && previous !== identity.id) crisp('do', 'session:reset');
  rememberOwner(identity.id);
  identified = identity.id;
  crisp('set', 'user:email', [identity.email]);
  if (identity.name) crisp('set', 'user:nickname', [identity.name]);
}

function hideChat() {
  crisp('do', 'chat:close');
  crisp('do', 'chat:hide');
}

function onReady() {
  clearTimeout(readyTimer);
  if (state.status === 'ready' && state.open) return;
  // A late arrival after the timeout is still accepted.
  update({ status: 'ready' });
  if (active && state.requested) showChat();
  else hideChat();
}
function onOpened() {
  update({ open: true, unread: false });
}
function onClosed() {
  crisp('do', 'chat:hide');
  update({ open: false });
}
function onMessage() {
  if (!state.open) update({ unread: true });
}
function showChat() {
  crisp('do', 'chat:show');
  crisp('do', 'chat:open');
  update({ open: true, unread: false, requested: false });
}

function waitForReady() {
  clearTimeout(readyTimer);
  readyTimer = setTimeout(() => {
    if (state.status === 'loading') update({ status: 'failed' });
  }, readyTimeoutMs);
}

function inject() {
  if (script || !websiteId) return;
  try {
    const w = window as CrispWindow;
    // Before Crisp has loaded, $crisp is a plain queue; start a new one for a retry.
    w.$crisp = [] as unknown as CrispQueue;
    w.CRISP_WEBSITE_ID = websiteId;
    w.CRISP_RUNTIME_CONFIG = { locale: 'zh' };
    w.CRISP_READY_TRIGGER = onReady;
    crisp('safe', true);
    crisp('do', 'chat:hide');
    crisp('on', 'chat:opened', onOpened);
    crisp('on', 'chat:closed', onClosed);
    crisp('on', 'message:received', onMessage);
    identified = null;
    pushIdentity();
    const element = document.createElement('script');
    element.src = crispScriptUrl;
    element.async = true;
    element.dataset.support = 'crisp';
    element.addEventListener('error', () => {
      scriptFailed = true;
      clearTimeout(readyTimer);
      if (state.status === 'loading') update({ status: 'failed' });
    });
    watchLeaving();
    script = element;
    scriptFailed = false;
    update({ status: 'loading' });
    waitForReady();
    document.head.appendChild(element);
  } catch (error) {
    warnOnce('Online support could not start', error);
    scriptFailed = true;
    update({ status: 'failed' });
  }
}

// After a failure: a script that errored is replaced (the only way to request it again); one
// that is still pending is given another full wait, since a second copy could run twice.
function retry() {
  if (script && scriptFailed) {
    script.remove();
    script = null;
    inject();
  } else if (script) {
    update({ status: 'loading' });
    waitForReady();
  } else inject();
}

function saveData() {
  const connection = (navigator as Navigator & { connection?: { saveData?: boolean } }).connection;
  return connection?.saveData === true;
}
function whenIdle(callback: () => void) {
  if ('requestIdleCallback' in window)
    window.requestIdleCallback(callback, { timeout: idleTimeoutMs });
  else setTimeout(callback, 300);
}

function schedulePreload() {
  if (preloadPlanned || script || saveData()) return;
  preloadPlanned = true;
  const attempt = () => {
    if (script || !active) {
      preloadPlanned = false;
      return;
    }
    if (document.readyState !== 'complete') {
      window.addEventListener('load', attempt, { once: true });
      return;
    }
    const wait = preloadDelayMs - (performance.now() - (firstActiveAt ?? 0));
    if (wait > 0) {
      setTimeout(attempt, wait);
      return;
    }
    if (document.visibilityState !== 'visible') {
      document.addEventListener('visibilitychange', attempt, { once: true });
      return;
    }
    whenIdle(() => {
      if (!script && active && document.visibilityState === 'visible') {
        preloadPlanned = false;
        inject();
      } else attempt();
    });
  };
  attempt();
}

// A signed-in ordinary user is on a user page.
export function activateSupport(id: string, user: SupportUser) {
  try {
    websiteId ??= id;
    active = true;
    identity = user;
    if (script) pushIdentity();
    firstActiveAt ??= performance.now();
    schedulePreload();
  } catch (error) {
    warnOnce('Online support could not start', error);
  }
}

// Another page (the administration, an error page) or an impersonation: hide everything.
export function suspendSupport() {
  active = false;
  if (script) hideChat();
  if (state.open || state.requested) update({ open: false, requested: false });
}

export const supportInjected = () => script !== null;

// Whether the page has already started a navigation to another document (for example the account
// deletion's location.replace('/')): a reload now would cancel it. The Navigation API reports it
// as it starts; beforeunload is the fallback where that API is missing.
let leavingDocument = false;
let watching = false;
function watchLeaving() {
  if (watching) return;
  watching = true;
  const navigation = (
    window as Window & {
      navigation?: {
        addEventListener: (
          type: 'navigate',
          listener: (event: { destination: { sameDocument: boolean } }) => void,
        ) => void;
      };
    }
  ).navigation;
  navigation?.addEventListener('navigate', (event) => {
    if (!event.destination.sameDocument) leavingDocument = true;
  });
  window.addEventListener('beforeunload', () => {
    leavingDocument = true;
  });
}

// Replaces the document with a fresh load of the current address, so Crisp's script is gone,
// unless the page is already on its way to another document.
export function reloadWithoutSupport() {
  setTimeout(() => {
    if (!leavingDocument) window.location.replace(window.location.href);
  }, 0);
}

// Signed out or the session ended: the conversation must not reach the next account. The caller
// then reloads the page, so the third-party script does not stay on the sign-in pages either.
export function endSupportSession() {
  suspendSupport();
  identity = null;
  if (!script) return;
  crisp('do', 'session:reset');
  crisp('do', 'chat:hide');
  identified = null;
}

export function openSupport() {
  if (!active || !websiteId) return;
  if (state.status === 'ready') {
    showChat();
    return;
  }
  update({ requested: true, unread: false });
  if (state.status === 'failed') retry();
  else if (!script) inject();
}

export function dismissSupport() {
  update({ requested: false });
}
