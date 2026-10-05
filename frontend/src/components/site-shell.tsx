'use client';
import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import { useState } from 'react';
import {
  AppShell,
  Avatar,
  Badge,
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
  IconArrowUpRight,
  IconLayoutDashboard,
  IconLogout,
  IconReceipt,
  IconShieldLock,
  IconStack2,
} from '@tabler/icons-react';
import { useAuth } from './providers';
import { appName } from '@/lib/types';
import { message } from '@/lib/api';

const links = [
  { href: '/dashboard', label: '总览', icon: IconLayoutDashboard },
  { href: '/plans', label: '套餐与订阅', icon: IconStack2 },
  { href: '/orders', label: '订单记录', icon: IconReceipt },
];
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
export function SiteShell({ children }: { children: React.ReactNode }) {
  const path = usePathname();
  const router = useRouter();
  const { user, logout, loading } = useAuth();
  const [opened, { toggle, close }] = useDisclosure();
  const [leaving, setLeaving] = useState(false);
  const authPage = path === '/login' || path === '/register';
  const workspace =
    ['/dashboard', '/plans', '/orders'].includes(path) || path.startsWith('/admin/');
  const navigation =
    user?.role === 'admin'
      ? [...links, { href: '/admin/nodes', label: '节点管理', icon: IconShieldLock }]
      : links;
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
              <Link className="quiet-link desktop-only" href="/plans">
                浏览套餐
              </Link>
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
                  <Menu.Divider />
                  <Menu.Item
                    color="red"
                    onClick={signout}
                    disabled={leaving}
                    leftSection={<IconLogout size={16} />}
                  >
                    退出登录
                  </Menu.Item>
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
          <Stack gap={7}>
            <Text c="dimmed" size="xs" fw={600} px="sm" mb="sm">
              个人工作台
            </Text>
            {navigation.map(({ href, label, icon: Icon }) => (
              <Link
                onClick={close}
                key={href}
                href={href}
                className={`nav-link ${path === href ? 'active' : ''}`}
              >
                <Icon size={19} stroke={1.6} />
                {label}
              </Link>
            ))}
          </Stack>
          <div className="sidebar-note">
            <Badge variant="light" color="gray" mb="sm">
              开发联调版
            </Badge>
            <Text size="xs" c="dimmed" lh={1.8}>
              节点与客户端流量正在联调。真实支付和节点侧强制配额尚未接入。
            </Text>
            <Link href="/" className="sidebar-home">
              返回首页 <IconArrowUpRight size={14} />
            </Link>
          </div>
        </AppShell.Navbar>
      ) : null}
      <AppShell.Main>{children}</AppShell.Main>
    </AppShell>
  );
}
