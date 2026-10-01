'use client';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useState } from 'react';
import {
  Alert,
  Anchor,
  Button,
  Checkbox,
  PasswordInput,
  Stack,
  Text,
  TextInput,
  Title,
} from '@mantine/core';
import { IconArrowLeft, IconArrowRight, IconCheck } from '@tabler/icons-react';
import { api, message } from '@/lib/api';
import { appName, type User } from '@/lib/types';
import { useAuth } from './providers';
import { Logo } from './site-shell';

export function AuthForm({ register = false }: { register?: boolean }) {
  const router = useRouter();
  const { setUser } = useAuth();
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [remember, setRemember] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setError(null);
    const bytes = new TextEncoder().encode(password).length;
    if (bytes > 72 || (register && bytes < 10)) {
      setError('密码需要 10–72 字节；中文字符会占用多个字节。');
      return;
    }
    setBusy(true);
    try {
      const data = await api<{ user: User }>(
        register ? '/auth/register' : '/auth/login',
        {
          method: 'POST',
          body: JSON.stringify({ ...(register ? { name } : {}), email, password, remember }),
        },
        false,
      );
      setUser(data.user);
      setPassword('');
      router.replace('/dashboard');
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="auth-layout">
      <section className="auth-story">
        <Logo />
        <div className="auth-story-copy">
          <h2>
            你的连接。
            <br />
            你的掌控。
          </h2>
          <p>从一个账户开始，管理套餐、流量与每一次订阅。</p>
          <div className="auth-promises">
            {['一个账户，统一管理', '套餐权益，清晰可见', '订单记录，随时可查'].map((text) => (
              <div key={text}>
                <IconCheck size={19} />
                {text}
              </div>
            ))}
          </div>
        </div>
        <Text size="xs" c="gray.4">
          {appName} · 账户服务开发联调版
        </Text>
      </section>
      <section className="auth-panel">
        <Anchor component={Link} href="/" c="dimmed" size="sm" className="back-link">
          <IconArrowLeft size={16} /> 返回首页
        </Anchor>
        <div className="auth-form">
          <Title order={1} size={32}>
            {register ? '创建你的账户' : '欢迎回来'}
          </Title>
          <Text c="dimmed" mt="sm" mb={32}>
            {register ? '注册后，即可体验完整的套餐测试流程。' : '登录后继续管理你的账户与订阅。'}
          </Text>
          <form onSubmit={submit}>
            <Stack gap="lg">
              {error ? (
                <Alert color="red" title="操作未完成" role="alert">
                  {error}
                </Alert>
              ) : null}
              {register ? (
                <TextInput
                  label="姓名 / 昵称"
                  placeholder="如何称呼你"
                  value={name}
                  onChange={(e) => setName(e.currentTarget.value)}
                  required
                  minLength={2}
                  maxLength={40}
                  autoComplete="nickname"
                  size="md"
                />
              ) : null}
              <TextInput
                type="email"
                label="邮箱地址"
                placeholder="you@example.com"
                value={email}
                onChange={(e) => setEmail(e.currentTarget.value)}
                required
                maxLength={254}
                autoComplete="email"
                size="md"
              />
              <PasswordInput
                label="密码"
                placeholder={register ? '至少 10 个字符' : '输入你的密码'}
                value={password}
                onChange={(e) => setPassword(e.currentTarget.value)}
                required
                minLength={register ? 10 : undefined}
                maxLength={72}
                autoComplete={register ? 'new-password' : 'current-password'}
                size="md"
              />
              <Checkbox
                checked={remember}
                onChange={(e) => setRemember(e.currentTarget.checked)}
                label="记住登录状态（30 天）"
                description="不保存明文密码；退出登录后会话立即失效。"
              />
              <Button
                type="submit"
                size="md"
                loading={busy}
                rightSection={<IconArrowRight size={17} />}
              >
                {register ? '注册并进入工作台' : '登录'}
              </Button>
            </Stack>
          </form>
          <Text ta="center" size="sm" c="dimmed" mt="xl">
            {register ? '已经有账户？' : '还没有账户？'}{' '}
            <Anchor component={Link} href={register ? '/login' : '/register'} fw={600}>
              {register ? '直接登录' : '创建账户'}
            </Anchor>
          </Text>
          <Text size="xs" c="dimmed" lh={1.8} mt={40}>
            当前为开发测试环境。请勿使用与其他网站相同的密码；测试购买不会产生真实扣款。
          </Text>
        </div>
      </section>
    </div>
  );
}
