'use client';
import Link from 'next/link';
import { IconArrowRight } from '@tabler/icons-react';
import { useHomeState, type HomeAction } from './home-state';

function primary(action: HomeAction) {
  if (action.kind === 'member') return { href: '/dashboard', text: '进入用户中心' };
  if (action.kind === 'visitor' && action.register) return { href: '/register', text: '创建账户' };
  return { href: '/login', text: '登录账户' };
}

// The main call to action of the hero and the closing band. Until the account and /meta are known
// it keeps its place without a label, so no visitor sees "登录" turn into something else.
export function PrimaryAction({ tone = 'brand' }: { tone?: 'brand' | 'inverse' }) {
  const { action } = useHomeState();
  const className = `home-btn home-btn-${tone === 'brand' ? 'primary' : 'inverse'}`;
  if (action.kind === 'pending')
    return (
      <span className={`${className} is-pending`} aria-hidden="true" data-testid="action-pending">
        创建账户
        <IconArrowRight size={18} />
      </span>
    );
  const { href, text } = primary(action);
  return (
    <Link href={href} className={className}>
      {text}
      <IconArrowRight size={18} />
    </Link>
  );
}

// The header's account actions: sign-in and free registration (registration open only), or the
// user centre. A blank placeholder of the same size while loading.
export function HeaderActions({ onNavigate }: { onNavigate?: () => void }) {
  const { action } = useHomeState();
  if (action.kind === 'pending')
    return <span className="home-header-pending" aria-hidden="true" data-testid="header-pending" />;
  if (action.kind === 'member')
    return (
      <Link
        href="/dashboard"
        className="home-btn home-btn-primary home-btn-sm"
        onClick={onNavigate}
      >
        进入用户中心
      </Link>
    );
  return (
    <>
      <Link href="/login" className="home-btn home-btn-ghost home-btn-sm" onClick={onNavigate}>
        登录
      </Link>
      {action.register ? (
        <Link
          href="/register"
          className="home-btn home-btn-primary home-btn-sm"
          onClick={onNavigate}
        >
          免费注册
        </Link>
      ) : null}
    </>
  );
}
