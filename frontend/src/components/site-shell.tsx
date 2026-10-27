'use client';
import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import { useEffect, useState } from 'react';
import {
  AppShell,
  Avatar,
  Burger,
  Button,
  Group,
  Menu,
  Stack,
  Text,
  UnstyledButton,
} from '@mantine/core';
import { useDisclosure } from '@mantine/hooks';
import { notifications } from '@mantine/notifications';
import {
  IconArrowBackUp,
  IconArrowUpRight,
  IconEye,
  IconDownload,
  IconGift,
  IconLayoutDashboard,
  IconLogout,
  IconReceipt,
  IconServer2,
  IconShieldLock,
  IconStack2,
  IconUserCog,
  IconUsersGroup,
} from '@tabler/icons-react';
import { useAuth } from './providers';
import { appName } from '@/lib/types';
import { APIError, api, message } from '@/lib/api';
import { AnnouncementGate } from './announcement-gate';
import { useMeta } from '@/lib/use-meta';

const links = [
  { href: '/dashboard', label: '总览', icon: IconLayoutDashboard },
  { href: '/nodes', label: '节点列表', icon: IconServer2 },
  { href: '/plans', label: '套餐与订阅', icon: IconStack2 },
  { href: '/orders', label: '订单记录', icon: IconReceipt },
  { href: '/referral', label: '推荐有礼', icon: IconGift },
  { href: '/download', label: '下载客户端', icon: IconDownload },
  { href: '/account', label: '账号设置', icon: IconUserCog },
];
// The console has several pages under one navigation entry.
const current = (href: string, path: string) =>
  href === '/admin/console/users' ? path.startsWith('/admin/console') : path === href;
export function Logo() {
  return (
    <Link className="brand" href="/">
      <span className="brand-icon">
        <IconShieldLock size={23} stroke={1.7} />
      </span>
      <span>
        {appName}
        <small>连接，自有边界。</small>
      </span>
    </Link>
  );
}
// The referral entry shows only when /meta announces the programme (older backends do not).
function Sidebar({
  admin,
  path,
  onNavigate,
}: {
  admin: boolean;
  path: string;
  onNavigate: () => void;
}) {
  const { meta } = useMeta();
  const userLinks = links.filter((link) => link.href !== '/referral' || meta?.referral_enabled);
  const navigation = admin
    ? [
        ...userLinks,
        { href: '/admin/console/users', label: '管理后台', icon: IconUsersGroup },
        { href: '/admin/nodes', label: '节点管理', icon: IconShieldLock },
      ]
    : userLinks;
  return (
    <>
      <Stack gap={7}>
        <Text c="dimmed" size="xs" fw={600} px="sm" mb="sm">
          用户中心
        </Text>
        {navigation.map(({ href, label, icon: Icon }) => (
          <Link
            onClick={onNavigate}
            key={href}
            href={href}
            className={`nav-link ${current(href, path) ? 'active' : ''}`}
          >
            <Icon size={19} stroke={1.6} />
            {label}
          </Link>
        ))}
      </Stack>
      <div className="sidebar-note">
        <Link href="/" className="sidebar-home">
          返回首页 <IconArrowUpRight size={14} />
        </Link>
      </div>
    </>
  );
}
// While an administrator looks at the site as a user (handoff_payment_account.md 9.7): the
// banner never goes away, counts down the hard limit, and holds the way back. The server
// enforces the read-only rule; this is only the explanation.
function useReturnToAdmin() {
  const [busy, setBusy] = useState(false);
  const back = () => {
    setBusy(true);
    api('/auth/impersonation/return', { method: 'POST', body: '{}' }, false)
      .then(() => window.location.replace('/admin/console/users'))
      .catch((e) => {
        // Without a valid way back the server has already ended the session: sign in again.
        if (e instanceof APIError && e.status === 401) window.location.replace('/login');
        else {
          setBusy(false);
          notifications.show({ color: 'red', title: '返回失败', message: message(e) });
        }
      });
  };
  return { busy, back };
}
function ImpersonationBanner({ expiresAt, email }: { expiresAt: string; email: string }) {
  const { reload } = useAuth();
  const [now, setNow] = useState(() => Date.now());
  const { busy, back } = useReturnToAdmin();
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  const left = Math.max(0, Math.floor((new Date(expiresAt).getTime() - now) / 1000));
  useEffect(() => {
    // At the limit the session is gone on the server; asking again sends the page to sign-in.
    if (left === 0) void reload();
  }, [left, reload]);
  return (
    <div className="impersonation-banner" role="status" data-testid="impersonation-banner">
      <Group gap="sm" justify="center" wrap="wrap">
        <IconEye size={18} />
        <Text size="sm" fw={600}>
          你正在以 {email} 的身份只读查看，不会产生任何修改。剩余 {Math.floor(left / 60)}:
          {String(left % 60).padStart(2, '0')}
        </Text>
        <Button
          size="compact-sm"
          color="dark"
          loading={busy}
          leftSection={<IconArrowBackUp size={16} />}
          onClick={back}
        >
          返回管理员
        </Button>
      </Group>
    </div>
  );
}
export function SiteShell({ children }: { children: React.ReactNode }) {
  const path = usePathname();
  const router = useRouter();
  const { user, impersonation, logout, loading } = useAuth();
  const returning = useReturnToAdmin();
  const [opened, { toggle, close }] = useDisclosure();
  const [leaving, setLeaving] = useState(false);
  const authPage = ['/login', '/register', '/forgot-password', '/reset-password'].includes(path);
  const workspace =
    [
      '/dashboard',
      '/nodes',
      '/plans',
      '/orders',
      '/referral',
      '/download',
      '/account',
      '/pay/result',
    ].includes(path) || path.startsWith('/admin/');
  const signout = async () => {
    setLeaving(true);
    try {
      await logout();
      router.replace('/login');
    } catch (e) {
      notifications.show({ color: 'red', title: '退出失败', message: message(e) });
    } finally {
      setLeaving(false);
    }
  };
  if (authPage) return <main>{children}</main>;
  return (
    <AppShell
      header={{ height: 80 }}
      navbar={
        workspace ? { width: 236, breakpoint: 'sm', collapsed: { mobile: !opened } } : undefined
      }
      padding={0}
    >
      <AppShell.Header className="site-header">
        <Group h="100%" px={{ base: 20, sm: 32 }} justify="space-between">
          <Group gap="md">
            {workspace ? (
              <Burger
                opened={opened}
                onClick={toggle}
                hiddenFrom="sm"
                size="sm"
                aria-label="展开导航"
              />
            ) : null}
            <Logo />
          </Group>
          <Group gap="lg">
            {!workspace ? (
              <>
                <Link className="quiet-link desktop-only" href="/download">
                  下载客户端
                </Link>
                <Link className="quiet-link desktop-only" href="/plans">
                  浏览套餐
                </Link>
              </>
            ) : null}
            {user ? (
              <Menu shadow="md" width={220}>
                <Menu.Target>
                  <UnstyledButton aria-label="账户菜单">
                    <Group gap="sm">
                      <Avatar color="teal" radius="xl" size={36}>
                        {user.name.slice(0, 1)}
                      </Avatar>
                      <Text size="sm" fw={600} visibleFrom="sm">
                        {user.name}
                      </Text>
                    </Group>
                  </UnstyledButton>
                </Menu.Target>
                <Menu.Dropdown>
                  <Menu.Label>{user.email}</Menu.Label>
                  <Menu.Item
                    component={Link}
                    href="/dashboard"
                    leftSection={<IconLayoutDashboard size={16} />}
                  >
                    用户中心
                  </Menu.Item>
                  <Menu.Item
                    component={Link}
                    href="/account"
                    leftSection={<IconUserCog size={16} />}
                  >
                    账号设置
                  </Menu.Item>
                  <Menu.Divider />
                  {impersonation ? (
                    <Menu.Item
                      onClick={returning.back}
                      disabled={returning.busy}
                      leftSection={<IconArrowBackUp size={16} />}
                    >
                      返回管理员
                    </Menu.Item>
                  ) : (
                    <Menu.Item
                      color="red"
                      onClick={signout}
                      disabled={leaving}
                      leftSection={<IconLogout size={16} />}
                    >
                      退出登录
                    </Menu.Item>
                  )}
                </Menu.Dropdown>
              </Menu>
            ) : (
              <Button component={Link} href="/login" variant="light" loading={loading}>
                登录账户
              </Button>
            )}
          </Group>
        </Group>
      </AppShell.Header>
      {workspace ? (
        <AppShell.Navbar p="lg">
          <Sidebar admin={user?.role === 'admin'} path={path} onNavigate={close} />
        </AppShell.Navbar>
      ) : null}
      <AppShell.Main>
        {user && impersonation ? (
          <ImpersonationBanner expiresAt={impersonation.expires_at} email={user.email} />
        ) : null}
        {children}
      </AppShell.Main>
      {user && workspace ? <AnnouncementGate /> : null}
    </AppShell>
  );
}
