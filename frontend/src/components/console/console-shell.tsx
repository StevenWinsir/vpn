'use client';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { Alert, Group, Text, Title } from '@mantine/core';
import { useAuth } from '../providers';
import { RequireAuth } from '../require-auth';

// The administrator console (handoff_payment_account.md 9.1): its own area with its own
// navigation, separate from node management. This gate only decides what to show; every
// request is authorised again by the API.
const tabs = [
  { href: '/admin/console/users', label: '用户管理' },
  { href: '/admin/console/orders', label: '订单与支付' },
  { href: '/admin/console/plans', label: '套餐管理' },
  { href: '/admin/console/logins', label: '登录记录' },
  { href: '/admin/console/coupons', label: '优惠码' },
  { href: '/admin/console/announcements', label: '公告' },
];

export function ConsoleShell({ children }: { children: React.ReactNode }) {
  return (
    <RequireAuth>
      <ConsoleGate>{children}</ConsoleGate>
    </RequireAuth>
  );
}

function ConsoleGate({ children }: { children: React.ReactNode }) {
  const { user } = useAuth();
  const path = usePathname();
  if (user?.role !== 'admin') {
    return (
      <Alert m="xl" color="red" title="无权访问">
        此页面仅对管理员开放。
      </Alert>
    );
  }
  return (
    <div className="page-wrap" key={user.id}>
      <Title order={1} size={29} className="page-title">
        管理后台
      </Title>
      <Text size="sm" c="dimmed" mt={8} mb="lg">
        用户、订单与支付、套餐、登录记录、优惠码、公告。节点与线路请在“节点管理”中维护。
      </Text>
      <Group gap="xs" mb="xl" component="nav" aria-label="管理后台导航">
        {tabs.map(({ href, label }) => (
          <Link
            key={href}
            href={href}
            className={`nav-link ${path === href ? 'active' : ''}`}
            style={{ width: 'auto', padding: '8px 16px' }}
          >
            {label}
          </Link>
        ))}
      </Group>
      {children}
    </div>
  );
}
