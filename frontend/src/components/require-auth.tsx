'use client';
import { useEffect } from 'react';
import { useRouter } from 'next/navigation';
import { Alert, Button, Center, Loader, Stack } from '@mantine/core';
import { useAuth } from './providers';
export function RequireAuth({ children }: { children: React.ReactNode }) {
  const { user, loading, error, reload } = useAuth();
  const router = useRouter();
  useEffect(() => {
    if (!loading && !user && !error) router.replace('/login');
  }, [loading, user, error, router]);
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
