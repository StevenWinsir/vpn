'use client';
import { useEffect } from 'react';
import { usePathname, useRouter } from 'next/navigation';
import { Alert, Button, Center, Loader, Stack } from '@mantine/core';
import { useAuth } from './providers';
import { safeNext } from '@/lib/next-path';
export function RequireAuth({ children }: { children: React.ReactNode }) {
  const { user, loading, error, reload } = useAuth();
  const router = useRouter();
  const path = usePathname();
  useEffect(() => {
    if (loading || user || error) return;
    // A payment result keeps its place across sign-in; everything else returns to the default page.
    const target =
      path === '/pay/result' ? safeNext(window.location.pathname + window.location.search) : null;
    router.replace(target ? `/login?next=${encodeURIComponent(target)}` : '/login');
  }, [loading, user, error, router, path]);
  if (error)
    return (
      <Center mih={360}>
        <Stack>
          <Alert color="red" title="连接暂时中断">
            {error}
          </Alert>
          <Button onClick={() => void reload()}>重新连接</Button>
        </Stack>
      </Center>
    );
  if (loading || !user)
    return (
      <Center mih={400}>
        <Loader aria-label="正在验证登录" />
      </Center>
    );
  return children;
}
