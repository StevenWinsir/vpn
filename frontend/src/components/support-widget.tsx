'use client';
import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import { Button, Loader } from '@mantine/core';
import { IconHeadset, IconMessageCircle, IconX } from '@tabler/icons-react';
import { useAuth } from './providers';
import { runtimeConfig, type RuntimeConfig } from '@/lib/api';
import {
  activateSupport,
  dismissSupport,
  endSupportSession,
  openSupport,
  reloadWithoutSupport,
  subscribeSupport,
  supportInjected,
  supportServerSnapshot,
  supportSnapshot,
  suspendSupport,
} from '@/lib/support';

// The user pages that offer the chat (handoff_ui.md D9). Not the home page, the sign-in pages,
// error pages or the administration.
export const supportPages = [
  '/dashboard',
  '/nodes',
  '/plans',
  '/orders',
  '/referral',
  '/tickets',
  '/download',
  '/guide',
  '/account',
  '/pay/result',
];
// The platform guides (/guide/windows …) belong to the guide.
export const isSupportPage = (path: string) =>
  supportPages.includes(path) || path.startsWith('/guide/');

const useSupportState = () =>
  useSyncExternalStore(subscribeSupport, supportSnapshot, supportServerSnapshot);

// Decides whether the chat is offered here and keeps the loader in step: only signed-in ordinary
// users (never an administrator, never during an impersonation) on a user page, and only while
// the chat is switched on. Returns the configuration while it is offered, else null.
export function useSupportAccess(path: string): RuntimeConfig['support'] | null {
  const { user, impersonation } = useAuth();
  const [config, setConfig] = useState<RuntimeConfig['support'] | null>(null);
  const signedIn = user !== null;
  useEffect(() => {
    // Already fetched before the first API call; this reuses that result.
    if (!signedIn) return;
    let current = true;
    runtimeConfig()
      .then((value) => {
        if (current) setConfig(value.support);
      })
      .catch(() => undefined);
    return () => {
      current = false;
    };
  }, [signedIn]);
  const websiteId = config?.crispWebsiteId ?? null;
  const offered =
    !!user && user.role !== 'admin' && !impersonation && isSupportPage(path) && !!websiteId;
  useEffect(() => {
    if (offered && user && websiteId)
      activateSupport(websiteId, { id: user.id, email: user.email, name: user.name });
    else suspendSupport();
  }, [offered, websiteId, user]);
  // Signing out or the end of the session in a page that already ran Crisp: the conversation is
  // reset at once, and the page's own next navigation (to sign-in, home, …) becomes a full page
  // load, so the script never stays for the sign-in pages or for the next account. Already off
  // the user pages, or an administrator signed in here: reload now.
  const previous = useRef<string | null>(null);
  const leaving = useRef<string | null>(null);
  useEffect(() => {
    const was = previous.current;
    previous.current = user?.id ?? null;
    if (leaving.current !== null) {
      if (path !== leaving.current) reloadWithoutSupport();
      return;
    }
    if (!supportInjected()) return;
    if (user?.role === 'admin') {
      endSupportSession();
      reloadWithoutSupport();
    } else if (was && !user) {
      endSupportSession();
      if (isSupportPage(path)) leaving.current = path;
      else reloadWithoutSupport();
    }
  }, [user, path]);
  return offered ? config : null;
}

export function SupportLauncher({ email }: { email: string | null }) {
  const { status, requested, open, unread } = useSupportState();
  const connecting = requested && status !== 'failed';
  const failed = requested && status === 'failed';
  return (
    <div className="support-launcher" data-testid="support-launcher" data-status={status}>
      {failed ? (
        <div className="support-card" role="alert" data-testid="support-fallback">
          <button
            type="button"
            className="support-card-close"
            aria-label="关闭提示"
            onClick={dismissSupport}
          >
            <IconX size={16} aria-hidden />
          </button>
          <p className="support-card-title">客服连接较慢，请稍后再试</p>
          {email ? (
            <p className="support-card-text">
              也可以发送邮件至 <a href={`mailto:${email}`}>{email}</a>
            </p>
          ) : null}
          <Button size="xs" onClick={openSupport}>
            重试
          </Button>
        </div>
      ) : null}
      {connecting ? (
        <span className="support-hint" role="status" data-testid="support-connecting">
          正在连接客服…
        </span>
      ) : null}
      {open ? null : (
        <button
          type="button"
          className="support-button"
          aria-label={unread ? '在线客服，有新消息' : '在线客服'}
          aria-busy={connecting || undefined}
          data-testid="support-button"
          onClick={openSupport}
        >
          {connecting ? (
            <Loader size={22} color="var(--al-on-brand)" aria-hidden />
          ) : (
            <IconMessageCircle size={26} stroke={1.8} aria-hidden />
          )}
          {unread ? <span className="support-unread" data-testid="support-unread" /> : null}
        </button>
      )}
    </div>
  );
}

// The sidebar entry (handoff_ui.md 5.4): the same as the launcher.
export function SupportNavItem({ onNavigate }: { onNavigate: () => void }) {
  return (
    <button
      type="button"
      className="nav-link sidebar-support"
      data-testid="support-nav"
      onClick={() => {
        onNavigate();
        openSupport();
      }}
    >
      <IconHeadset size={19} stroke={1.6} aria-hidden />
      在线客服
    </button>
  );
}

// Whether the chat is offered to this visitor (the same rule as useSupportAccess, minus its side
// effects), for a page's own "ask the chat" button.
export function useSupportOffered(): boolean {
  const { user, impersonation } = useAuth();
  const [websiteId, setWebsiteId] = useState<string | null>(null);
  const eligible = !!user && user.role !== 'admin' && !impersonation;
  useEffect(() => {
    if (!eligible) return;
    let current = true;
    runtimeConfig()
      .then((value) => {
        if (current) setWebsiteId(value.support?.crispWebsiteId ?? null);
      })
      .catch(() => undefined);
    return () => {
      current = false;
    };
  }, [eligible]);
  return eligible && !!websiteId;
}
