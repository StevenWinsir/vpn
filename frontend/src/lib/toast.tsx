'use client';
// The site's one way to show a short message that goes away by itself (handoff_message.md 4.1).
// Only results go here: form and request errors stay where they happened (D4), and background
// refreshes never announce themselves (D8).
import { notifications, notificationsStore } from '@mantine/notifications';
import {
  IconAlertTriangle,
  IconCircleCheck,
  IconCircleX,
  IconInfoCircle,
} from '@tabler/icons-react';
import {
  nextPageToasts,
  takeNextPageToast,
  writeNextPageToast,
  type NextPageToast,
  type ToastTone,
} from './toast-flash';

export type { NextPageToast, ToastTone } from './toast-flash';

// Milliseconds on screen (D2); hovering pauses the countdown, moving away starts it again. note is
// for the administration's results that carry a consequence to read (who was signed out, what
// takes effect when).
export const toastDurations: Record<ToastTone | 'note', number> = {
  success: 3000,
  info: 4000,
  warning: 5000,
  error: 6000,
  note: 8000,
};

const tones: Record<ToastTone, { color: string; icon: React.ReactNode; role: string }> = {
  success: { color: 'teal', icon: <IconCircleCheck size={20} stroke={1.8} />, role: 'status' },
  info: { color: 'blue', icon: <IconInfoCircle size={20} stroke={1.8} />, role: 'status' },
  warning: { color: 'orange', icon: <IconAlertTriangle size={20} stroke={1.8} />, role: 'alert' },
  error: { color: 'red', icon: <IconCircleX size={20} stroke={1.8} />, role: 'alert' },
};

export type ToastOptions = {
  // One message per id (D6): showing it again replaces the text and restarts the countdown.
  id?: string;
  duration?: number;
};

// Mantine restarts a message's timer only when its duration changes, so a repeat alternates the
// duration by one millisecond.
const nudged = new Map<string, boolean>();

function show(tone: ToastTone, title: string, message?: string, options: ToastOptions = {}) {
  const { color, icon, role } = tones[tone];
  const base = options.duration ?? toastDurations[tone];
  const { id } = options;
  const state = notificationsStore.getState();
  const shown = !!id && [...state.notifications, ...state.queue].some((n) => n.id === id);
  let autoClose = base;
  if (id && shown) {
    const flip = !nudged.get(id);
    nudged.set(id, flip);
    autoClose = base + (flip ? 1 : 0);
  } else if (id) nudged.delete(id);
  const data = {
    id,
    color,
    icon,
    title,
    // An empty body would leave an empty line under the title.
    message: message || null,
    autoClose,
    withBorder: true,
    radius: 'md',
    role,
    'data-testid': 'toast',
    'data-tone': tone,
  };
  if (id && shown) notifications.update({ ...data, id });
  else notifications.show(data);
}

export const toast = {
  success: (title: string, message?: string, options?: ToastOptions) =>
    show('success', title, message, options),
  info: (title: string, message?: string, options?: ToastOptions) =>
    show('info', title, message, options),
  warning: (title: string, message?: string, options?: ToastOptions) =>
    show('warning', title, message, options),
  error: (title: string, message?: string, options?: ToastOptions) =>
    show('error', title, message, options),
};

// One of the fixed cross-page messages, at once.
export function showFixedToast(key: NextPageToast) {
  const copy: { tone: ToastTone; title: string; message?: string } = nextPageToasts[key];
  show(copy.tone, copy.title, copy.message, { id: key });
}

// For a page that is about to be replaced by a full load: the next page shows the message.
export function toastAfterNavigation(key: NextPageToast) {
  writeNextPageToast(key);
}

// Now, or after the full reload that is coming (the support chat turns the next navigation into
// one once it ran on the page, handoff_ui.md 9): either way the message is shown once.
export function toastAcrossReload(key: NextPageToast, reloading: boolean) {
  if (reloading) toastAfterNavigation(key);
  else showFixedToast(key);
}

// Called once when the document starts.
export function showNextPageToast() {
  const key = takeNextPageToast();
  if (key) showFixedToast(key);
}
