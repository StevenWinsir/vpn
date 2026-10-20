'use client';
import Link from 'next/link';
import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Alert,
  Anchor,
  Button,
  Group,
  Loader,
  PasswordInput,
  Stack,
  Text,
  TextInput,
  Title,
} from '@mantine/core';
import { IconArrowRight, IconCircleCheck, IconMailCheck } from '@tabler/icons-react';
import { api, APIError, message } from '@/lib/api';
import { utf8Bytes, type ForgotPasswordReceipt, type ResetRules } from '@/lib/types';
import { useMeta } from '@/lib/use-meta';
import { AuthLayout, Footnote } from './auth-form';
import { useAuth } from './providers';

const remaining = (until: number, now: number) => Math.max(0, Math.ceil((until - now) / 1000));
const clock = (seconds: number) =>
  `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;

const backToLogin = (
  <Text ta="center" size="sm" c="dimmed" mt="xl">
    想起密码了？{' '}
    <Anchor component={Link} href="/login" fw={600}>
      返回登录
    </Anchor>
  </Text>
);

// Forgot password (handoff_auth.md 8.1): only an email. The answer is the same for every
// address, so the page never says whether an account exists.
export function ForgotPasswordForm() {
  const { meta } = useMeta();
  const [email, setEmail] = useState('');
  const [field, setField] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [sending, setSending] = useState(false);
  const [resendAt, setResendAt] = useState(0);
  const [now, setNow] = useState(() => Date.now());
  // Bumped when the address changes, so a late answer for an older address is dropped.
  const sequence = useRef(0);

  const cooldown = remaining(resendAt, now);
  useEffect(() => {
    if (cooldown <= 0) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [cooldown]);

  function changeEmail(value: string) {
    setEmail(value);
    setField(null);
    if (value.trim().toLowerCase() !== email.trim().toLowerCase()) {
      sequence.current++;
      setNotice(null);
      setResendAt(0);
      setSending(false);
    }
  }

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setError(null);
    setField(null);
    const address = email.trim().toLowerCase();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(address)) {
      setField('请输入有效的邮箱地址');
      return;
    }
    const current = ++sequence.current;
    setSending(true);
    try {
      const receipt = await api<ForgotPasswordReceipt>(
        '/auth/forgot-password',
        { method: 'POST', body: JSON.stringify({ email: address }) },
        false,
      );
      if (current !== sequence.current) return;
      const at = Date.now();
      setNow(at);
      setResendAt(at + receipt.resend_after_seconds * 1000);
      setNotice(receipt.message);
    } catch (err) {
      if (current !== sequence.current) return;
      if (err instanceof APIError && err.code === 'rate_limited') {
        const at = Date.now();
        setNow(at);
        if (err.retryAfter) setResendAt(at + err.retryAfter * 1000);
        setError(
          err.retryAfter
            ? `请求过于频繁，请在 ${clock(err.retryAfter)} 后重试。`
            : '请求过于频繁，请稍后重试。',
        );
      } else if (err instanceof APIError && err.code === 'invalid_email') setField(err.message);
      else setError(message(err));
    } finally {
      if (current === sequence.current) setSending(false);
    }
  }

  const unavailable = meta?.password_reset_enabled === false;
  const label = sending
    ? '发送中'
    : cooldown > 0
      ? `${cooldown} 秒后可重发`
      : notice
        ? '重新发送'
        : '发送重置链接';
  return (
    <AuthLayout>
      <Title order={1} size={32}>
        找回密码
      </Title>
      <Text c="dimmed" mt="sm" mb={32}>
        输入账户邮箱，我们会发送一封包含重置链接的邮件。
      </Text>
      {unavailable ? (
        <Alert color="gray" title="邮件找回暂不可用" role="status">
          当前服务未开启邮件找回，请稍后再试或联系管理员。
        </Alert>
      ) : (
        <form onSubmit={submit} noValidate>
          <Stack gap="lg">
            {error ? (
              <Alert color="red" title="操作未完成" role="alert">
                {error}
              </Alert>
            ) : null}
            <TextInput
              type="email"
              label="邮箱地址"
              placeholder="you@example.com"
              value={email}
              onChange={(e) => changeEmail(e.currentTarget.value)}
              error={field}
              required
              maxLength={254}
              autoComplete="email"
              size="md"
            />
            {notice ? (
              <Alert color="teal" icon={<IconMailCheck size={18} />} role="status">
                {notice} 链接 30 分钟内有效且只能使用一次；多次申请时请使用最新一封邮件。
              </Alert>
            ) : null}
            <Button
              type="submit"
              size="md"
              loading={sending}
              disabled={cooldown > 0}
              rightSection={<IconArrowRight size={17} />}
            >
              {label}
            </Button>
          </Stack>
        </form>
      )}
      {backToLogin}
      <Footnote />
    </AuthLayout>
  );
}

type Phase = 'checking' | 'missing' | 'invalid' | 'unavailable' | 'ready' | 'done';
type Fields = Partial<Record<'password' | 'confirmation', string>>;

// Reads the token from the fragment exactly once and removes it from the address bar. The
// fragment never reaches the server, and the token lives only in memory from here on.
function takeFragmentToken(): string | null {
  const hash = window.location.hash;
  if (!hash) return null;
  const token = new URLSearchParams(hash.slice(1)).get('token');
  window.history.replaceState(
    window.history.state,
    '',
    window.location.pathname + window.location.search,
  );
  return token || null;
}

// Reset password (handoff_auth.md 8.2–8.3). The form depends only on the mailed token, never
// on the signed-in state, and success does not sign anybody in.
export function ResetPasswordForm() {
  const { setUser } = useAuth();
  // Kept in a ref: Strict Mode runs the mount effect twice, and the second run must not lose
  // the token the first run already moved out of the address bar.
  const token = useRef<string | null>(null);
  const checked = useRef(false);
  const [phase, setPhase] = useState<Phase>('checking');
  const [rules, setRules] = useState<ResetRules | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const [password, setPassword] = useState('');
  const [confirmation, setConfirmation] = useState('');
  const [fields, setFields] = useState<Fields>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const validate = useCallback(async () => {
    if (!token.current) {
      setPhase('missing');
      return;
    }
    setPhase('checking');
    setProblem(null);
    try {
      const result = await api<ResetRules>(
        '/auth/reset-password/validate',
        { method: 'POST', body: JSON.stringify({ token: token.current }) },
        false,
      );
      setRules(result);
      setPhase('ready');
    } catch (err) {
      if (err instanceof APIError && err.code === 'reset_token_invalid') {
        token.current = null;
        setPhase('invalid');
      } else {
        setProblem(message(err));
        setPhase('unavailable');
      }
    }
  }, []);

  useEffect(() => {
    if (!token.current) token.current = takeFragmentToken();
    if (!checked.current) {
      checked.current = true;
      void validate();
    }
    // A link opened while this page is already showing only changes the fragment.
    const follow = () => {
      const next = takeFragmentToken();
      if (!next) return;
      token.current = next;
      void validate();
    };
    window.addEventListener('hashchange', follow);
    return () => window.removeEventListener('hashchange', follow);
  }, [validate]);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (!rules || !token.current) return;
    setError(null);
    const problems: Fields = {};
    const bytes = utf8Bytes(password);
    if (bytes < rules.password_min_bytes || bytes > rules.password_max_bytes)
      problems.password = `密码需要 ${rules.password_min_bytes}–${rules.password_max_bytes} 字节（当前 ${bytes} 字节）`;
    if (confirmation !== password) problems.confirmation = '两次输入的新密码不一致';
    setFields(problems);
    if (Object.keys(problems).length) return;
    setBusy(true);
    try {
      await api(
        '/auth/reset-password',
        {
          method: 'POST',
          body: JSON.stringify({
            token: token.current,
            password,
            password_confirmation: confirmation,
          }),
        },
        false,
      );
      token.current = null;
      setPassword('');
      setConfirmation('');
      // Every session of the account was revoked; this tab forgets its user as well.
      setUser(null);
      setPhase('done');
    } catch (err) {
      if (!(err instanceof APIError)) setError(message(err));
      else if (err.code === 'reset_token_invalid') {
        token.current = null;
        setPhase('invalid');
      } else if (err.code === 'password_mismatch') setFields({ confirmation: err.message });
      else if (err.code === 'invalid_password' || err.code === 'password_unchanged')
        setFields({ password: err.message });
      else if (err.code === 'rate_limited')
        setError(
          err.retryAfter
            ? `请求过于频繁，请在 ${clock(err.retryAfter)} 后重试。`
            : '请求过于频繁，请稍后重试。',
        );
      else setError(err.message);
    } finally {
      setBusy(false);
    }
  }

  const reapply = (
    <Button component={Link} href="/forgot-password" variant="light" mt="md">
      重新申请重置链接
    </Button>
  );
  let body: React.ReactNode;
  if (phase === 'checking')
    body = (
      <Group gap="sm" c="dimmed">
        <Loader size="sm" /> 正在检查重置链接…
      </Group>
    );
  else if (phase === 'missing')
    body = (
      <Alert color="gray" title="请从邮件中打开重置链接" role="status">
        出于安全考虑，链接打开后会立即从地址栏移除，刷新页面后需要重新点击邮件中的链接。
        <div>{reapply}</div>
      </Alert>
    );
  else if (phase === 'invalid')
    body = (
      <Alert color="red" title="重置链接无效或已过期" role="alert">
        链接可能已过期、已被使用，或已被更新的邮件取代。请重新申请找回密码。
        <div>{reapply}</div>
      </Alert>
    );
  else if (phase === 'unavailable')
    body = (
      <Alert color="red" title="暂时无法检查重置链接" role="alert">
        {problem}
        <div>
          <Button mt="md" variant="light" size="xs" onClick={() => void validate()}>
            重试
          </Button>
        </div>
      </Alert>
    );
  else if (phase === 'done')
    body = (
      <Stack gap="lg">
        <Alert color="teal" icon={<IconCircleCheck size={18} />} title="密码已重置" role="status">
          所有设备上的原有登录均已退出。请使用新密码重新登录网站和客户端。
        </Alert>
        <Button
          component={Link}
          href="/login"
          size="md"
          rightSection={<IconArrowRight size={17} />}
        >
          使用新密码登录
        </Button>
      </Stack>
    );
  else
    body = (
      <form onSubmit={submit} noValidate>
        <Stack gap="lg">
          {error ? (
            <Alert color="red" title="操作未完成" role="alert">
              {error}
            </Alert>
          ) : null}
          <PasswordInput
            label="新密码"
            placeholder={`${rules?.password_min_bytes}–${rules?.password_max_bytes} 字节`}
            description="中文等字符每个占 3 字节；密码不会被裁剪或去除空格，且不能与当前密码相同。"
            value={password}
            onChange={(e) => {
              setPassword(e.currentTarget.value);
              setFields((f) => ({ ...f, password: undefined }));
            }}
            error={fields.password}
            required
            autoComplete="new-password"
            size="md"
          />
          <PasswordInput
            label="确认新密码"
            value={confirmation}
            onChange={(e) => {
              setConfirmation(e.currentTarget.value);
              setFields((f) => ({ ...f, confirmation: undefined }));
            }}
            error={fields.confirmation}
            required
            autoComplete="new-password"
            size="md"
          />
          <Text size="xs" c="dimmed">
            重置后，网站和所有客户端上的原有登录都会退出。刷新本页面后需要重新打开邮件中的链接。
          </Text>
          <Button
            type="submit"
            size="md"
            loading={busy}
            rightSection={<IconArrowRight size={17} />}
          >
            设置新密码
          </Button>
        </Stack>
      </form>
    );
  return (
    <AuthLayout>
      <Title order={1} size={32}>
        设置新密码
      </Title>
      <Text c="dimmed" mt="sm" mb={32}>
        为你的账户设置一个新的登录密码。
      </Text>
      {body}
      {phase === 'done' ? null : backToLogin}
      <Footnote />
    </AuthLayout>
  );
}
