'use client';
import { useEffect, useRef, useSyncExternalStore } from 'react';
import { api } from './api';
import type { ConsoleTicketSummary } from './console';
import { isTicketSummary, ticketRefresh } from './tickets';
import type { TicketSummary } from './types';

// The ticket badges (handoff_ticket.md 7.1, 11.1). One poller per summary, shared by every badge
// that shows it (the console's sidebar dot and its tab use the same request). It runs only while
// a badge is mounted: every `ms` while the page is visible, at once on focus, return to the page
// and network recovery, one request at a time. Requests are quiet: a 401 neither refreshes nor
// ends the session, and any failure hides the badge instead of bothering anyone.
type Poller<T> = {
  subscribe: (listener: () => void) => () => void;
  snapshot: () => T | null;
  refresh: () => void;
};

function poller<T>(url: string, ms: number, accept: (value: unknown) => value is T): Poller<T> {
  let value: T | null = null;
  let inFlight: AbortController | null = null;
  let timer: number | null = null;
  const listeners = new Set<() => void>();
  const notify = () => listeners.forEach((listener) => listener());
  const load = () => {
    if (inFlight || listeners.size === 0) return;
    const controller = new AbortController();
    inFlight = controller;
    api<unknown>(url, { signal: controller.signal }, false)
      .then((data) => (accept(data) ? data : null))
      .catch(() => null)
      .then((next) => {
        if (inFlight !== controller) return;
        inFlight = null;
        value = next;
        notify();
      });
  };
  const visible = () => {
    if (document.visibilityState === 'visible') load();
  };
  const start = () => {
    load();
    timer = window.setInterval(visible, ms);
    window.addEventListener('focus', visible);
    window.addEventListener('online', visible);
    document.addEventListener('visibilitychange', visible);
  };
  const stop = () => {
    if (timer !== null) window.clearInterval(timer);
    timer = null;
    window.removeEventListener('focus', visible);
    window.removeEventListener('online', visible);
    document.removeEventListener('visibilitychange', visible);
    inFlight?.abort();
    inFlight = null;
    // The next account to sign in on this page never sees this one's counts.
    value = null;
  };
  return {
    subscribe(listener) {
      listeners.add(listener);
      if (listeners.size === 1) start();
      return () => {
        listeners.delete(listener);
        if (listeners.size === 0) stop();
      };
    },
    snapshot: () => value,
    refresh: () => {
      // A refresh asked for while one is running waits for the next tick: drop that answer.
      inFlight?.abort();
      inFlight = null;
      load();
    },
  };
}

const isConsoleSummary = (value: unknown): value is ConsoleTicketSummary =>
  typeof value === 'object' &&
  value !== null &&
  typeof (value as Record<string, unknown>).open_count === 'number';

const userSummary = poller<TicketSummary>(
  '/me/tickets/summary',
  ticketRefresh.userBadge,
  isTicketSummary,
);
const consoleSummary = poller<ConsoleTicketSummary>(
  '/admin/console/tickets/summary',
  ticketRefresh.consoleBadge,
  isConsoleSummary,
);

const idle = () => () => undefined;
const nothing = () => null;

function useSummary<T>(source: Poller<T>, enabled: boolean, path?: string): T | null {
  const value = useSyncExternalStore(
    enabled ? source.subscribe : idle,
    enabled ? source.snapshot : nothing,
    nothing,
  );
  // Every page change asks again (handoff_ticket.md 11.1); subscribing already asked for the
  // first page.
  const seen = useRef(path);
  useEffect(() => {
    if (seen.current === path) return;
    seen.current = path;
    if (enabled) source.refresh();
  }, [source, enabled, path]);
  return enabled ? value : null;
}

// The user's unread replies; only while tickets exist on this backend.
export function useTicketSummary(enabled: boolean, path?: string) {
  return useSummary(userSummary, enabled, path);
}
// The console's waiting queue.
export function useConsoleTicketSummary(enabled: boolean, path?: string) {
  return useSummary(consoleSummary, enabled, path);
}
// After reading, replying to or closing a ticket the badges are asked again at once.
export function refreshTicketBadges() {
  userSummary.refresh();
  consoleSummary.refresh();
}
