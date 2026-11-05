// Messages that must survive a full page load (handoff_message.md D5): signing out after the
// support chat ran, deleting the account, returning to the administrator, a session the server
// ended. Only one of these fixed keys and the time it was written are stored, in sessionStorage,
// never in the address and never any text a user typed; the next page shows its fixed wording.

export type ToastTone = 'success' | 'info' | 'warning' | 'error';
export type ToastCopy = { tone: ToastTone; title: string; message?: string };

export const nextPageToasts = {
  'signed-out': { tone: 'success', title: '已退出登录' },
  'account-deleted': { tone: 'success', title: '账号已删除', message: '感谢你的使用' },
  'returned-admin': { tone: 'success', title: '已返回管理员账号' },
  'session-ended': { tone: 'warning', title: '登录已失效', message: '请重新登录' },
} as const satisfies Record<string, ToastCopy>;

export type NextPageToast = keyof typeof nextPageToasts;

export const nextPageStorageKey = 'asterlink-next-toast';
// A message older than this belongs to a page load long gone and is dropped.
export const nextPageMaxAgeMs = 30_000;

type Store = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;

// sessionStorage can be missing or throw (private modes, blocked storage): then nothing is kept.
function session(): Store | null {
  try {
    return typeof window === 'undefined' ? null : window.sessionStorage;
  } catch {
    return null;
  }
}

export const isNextPageToast = (value: unknown): value is NextPageToast =>
  typeof value === 'string' && Object.prototype.hasOwnProperty.call(nextPageToasts, value);

export function writeNextPageToast(
  key: NextPageToast,
  store: Store | null = session(),
  now = Date.now(),
) {
  try {
    store?.setItem(nextPageStorageKey, JSON.stringify({ key, at: now }));
  } catch {
    // Full or blocked storage: the message is simply not carried over.
  }
}

// Reads the stored message once: it is removed whatever it holds, and returned only when it is a
// known key written within the last nextPageMaxAgeMs (a clock that went backwards counts as old).
export function takeNextPageToast(
  store: Store | null = session(),
  now = Date.now(),
): NextPageToast | null {
  let raw: string | null = null;
  try {
    raw = store?.getItem(nextPageStorageKey) ?? null;
    if (raw !== null) store?.removeItem(nextPageStorageKey);
  } catch {
    return null;
  }
  if (raw === null) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  if (!parsed || typeof parsed !== 'object') return null;
  const { key, at } = parsed as { key?: unknown; at?: unknown };
  if (!isNextPageToast(key) || typeof at !== 'number' || !Number.isFinite(at)) return null;
  const age = now - at;
  return age >= 0 && age <= nextPageMaxAgeMs ? key : null;
}
