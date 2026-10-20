'use client';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useEffect, useRef, useState } from 'react';
import {
  Alert,
  Anchor,
  Button,
  Checkbox,
  Group,
  Loader,
  PasswordInput,
  Stack,
  Text,
  TextInput,
  Title,
} from '@mantine/core';
import { IconArrowLeft, IconArrowRight, IconCheck, IconMailCheck } from '@tabler/icons-react';
import { api, APIError, message } from '@/lib/api';
import {
  appName,
  nameLength,
  passwordMaxBytes,
  passwordMinBytes,
  utf8Bytes,
  type RegisterCodeReceipt,
  type User,
} from '@/lib/types';
import { useMeta } from '@/lib/use-meta';
import { useAuth } from './providers';
import { Logo } from './site-shell';

export function AuthForm({ register = false }: { register?: boolean }) {
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
        <div className="auth-form">{register ? <RegisterForm /> : <LoginForm />}</div>
      </section>
    </div>
  );
}

function Footnote() {
  return (
    <Text size="xs" c="dimmed" lh={1.8} mt={40}>
      当前为开发测试环境。请勿使用与其他网站相同的密码；测试购买不会产生真实扣款。
    </Text>
  );
}

function LoginForm() {
  const router = useRouter();
  const { setUser } = useAuth();
  const { meta } = useMeta();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [remember, setRemember] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setError(null);
    if (utf8Bytes(password) > passwordMaxBytes) {
      setError(`密码最多 ${passwordMaxBytes} 字节；中文字符会占用多个字节。`);
      return;
    }
    setBusy(true);
    try {
      const data = await api<{ user: User }>(
        '/auth/login',
        { method: 'POST', body: JSON.stringify({ email, password, remember }) },
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
    <>
      <Title order={1} size={32}>
        欢迎回来
      </Title>
      <Text c="dimmed" mt="sm" mb={32}>
        登录后继续管理你的账户与订阅。
      </Text>
      <form onSubmit={submit}>
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
            onChange={(e) => setEmail(e.currentTarget.value)}
            required
            maxLength={254}
            autoComplete="email"
            size="md"
          />
          <PasswordInput
            label="密码"
            placeholder="输入你的密码"
            value={password}
            onChange={(e) => setPassword(e.currentTarget.value)}
            required
            autoComplete="current-password"
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
            登录
          </Button>
        </Stack>
      </form>
      {meta?.allow_registration ? (
        <Text ta="center" size="sm" c="dimmed" mt="xl">
          还没有账户？{' '}
          <Anchor component={Link} href="/register" fw={600}>
            创建账户
          </Anchor>
        </Text>
      ) : null}
      <Footnote />
    </>
  );
}

// Codes are six ASCII digits. Typing, pasting or autofill keep only those digits; the
// backend still validates the exact format.
const codeDigits = (value: string) => value.replace(/[^0-9]/g, '').slice(0, 6);
const emailDomain = (value: string) => {
  const email = value.trim().toLowerCase();
  const at = email.lastIndexOf('@');
  return at > 0 ? email.slice(at + 1) : '';
};
const remaining = (until: number, now: number) => Math.max(0, Math.ceil((until - now) / 1000));
const clock = (seconds: number) =>
  `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;

type Fields = Partial<Record<'name' | 'email' | 'password' | 'code', string>>;
// Proof of the most recent code request. Deadlines are local times derived from the
// server's relative seconds, so a skewed device clock does not move them.
type Proof = { email: string; id: string; expiresAt: number };

function RegisterForm() {
  const router = useRouter();
  const { setUser } = useAuth();
  const { meta, error: metaError, loading: metaLoading, reload: reloadMeta } = useMeta();
  const [closed, setClosed] = useState(false);
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [remember, setRemember] = useState(false);
  const [code, setCode] = useState('');
  const [proof, setProof] = useState<Proof | null>(null);
  const [resendAt, setResendAt] = useState(0);
  const [notice, setNotice] = useState<string | null>(null);
  const [sending, setSending] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<React.ReactNode>(null);
  const [fields, setFields] = useState<Fields>({});
  const [now, setNow] = useState(() => Date.now());
  // Bumped whenever the address changes or a newer request starts: late responses for an
  // older address or request are dropped instead of overwriting the current state.
  const sequence = useRef(0);

  const ticking = resendAt > now || (proof !== null && proof.expiresAt > now);
  useEffect(() => {
    if (!ticking) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [ticking]);

  const domains = meta?.registration_email_domains ?? [];
  const open = !closed && meta?.allow_registration === true;
  const cooldown = remaining(resendAt, now);
  const validFor = proof ? remaining(proof.expiresAt, now) : 0;

  function changeEmail(value: string) {
    setEmail(value);
    if (value.trim().toLowerCase() !== proof?.email) {
      sequence.current++;
      setProof(null);
      setCode('');
      setNotice(null);
      setResendAt(0);
      setSending(false);
    }
    setFields((f) => ({ ...f, email: undefined, code: undefined }));
  }

  function closeRegistration() {
    setClosed(true);
    setProof(null);
    setCode('');
  }

  async function sendCode() {
    setError(null);
    setFields((f) => ({ ...f, email: undefined, code: undefined }));
    const address = email.trim().toLowerCase();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(address)) {
      setFields((f) => ({ ...f, email: '请输入有效的邮箱地址' }));
      return;
    }
    if (domains.length && !domains.includes(emailDomain(address))) {
      setFields((f) => ({ ...f, email: '该邮箱服务商暂不支持注册，请使用下方列出的邮箱' }));
      return;
    }
    const current = ++sequence.current;
    setSending(true);
    try {
      const receipt = await api<RegisterCodeReceipt>(
        '/auth/register-code',
        { method: 'POST', body: JSON.stringify({ email: address }) },
        false,
      );
      if (current !== sequence.current) return;
      const at = Date.now();
      setNow(at);
      setProof({
        email: address,
        id: receipt.verification_id,
        expiresAt: at + receipt.expires_in_seconds * 1000,
      });
      setResendAt(at + receipt.resend_after_seconds * 1000);
      setCode('');
      setNotice(receipt.message);
    } catch (err) {
      if (current !== sequence.current) return;
      if (err instanceof APIError && err.code === 'rate_limited') {
        const at = Date.now();
        setNow(at);
        if (err.retryAfter) setResendAt(at + err.retryAfter * 1000);
        setError(
          err.retryAfter
            ? `发送过于频繁，请在 ${clock(err.retryAfter)} 后重试。`
            : '发送过于频繁，请稍后重试。',
        );
      } else if (err instanceof APIError && err.code === 'registration_disabled') {
        closeRegistration();
      } else if (
        err instanceof APIError &&
        ['email_domain_not_allowed', 'invalid_email'].includes(err.code)
      ) {
        setFields((f) => ({ ...f, email: err.message }));
      } else {
        setError(message(err));
      }
    } finally {
      if (current === sequence.current) setSending(false);
    }
  }

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setError(null);
    const problems: Fields = {};
    const length = nameLength(name);
    if (length < 2 || length > 40) problems.name = '姓名 / 昵称需要 2–40 个字符';
    const bytes = utf8Bytes(password);
    if (bytes < passwordMinBytes || bytes > passwordMaxBytes)
      problems.password = `密码需要 ${passwordMinBytes}–${passwordMaxBytes} 字节（当前 ${bytes} 字节）`;
    if (!proof || proof.email !== email.trim().toLowerCase()) problems.code = '请先获取邮箱验证码';
    else if (code.length !== 6) problems.code = '请输入邮件中的 6 位数字验证码';
    setFields(problems);
    if (Object.keys(problems).length || !proof) return;
    setBusy(true);
    try {
      const data = await api<{ user: User }>(
        '/auth/register',
        {
          method: 'POST',
          body: JSON.stringify({
            name,
            email: proof.email,
            password,
            remember,
            verification_id: proof.id,
            code,
          }),
        },
        false,
      );
      setPassword('');
      setCode('');
      setUser(data.user);
      router.replace('/dashboard');
    } catch (err) {
      if (!(err instanceof APIError)) setError(message(err));
      else if (err.code === 'verification_invalid' || err.code === 'invalid_verification_code')
        setFields({ code: err.message });
      else if (err.code === 'verification_expired' || err.code === 'verification_locked') {
        setProof(null);
        setCode('');
        setFields({ code: err.message });
      } else if (err.code === 'verification_used' || err.code === 'email_exists') {
        setError(
          <>
            {err.message}{' '}
            <Anchor component={Link} href="/login" fw={600}>
              前往登录
            </Anchor>
          </>,
        );
      } else if (err.code === 'registration_disabled') closeRegistration();
      else if (err.code === 'email_domain_not_allowed') setFields({ email: err.message });
      else if (err.code === 'rate_limited')
        setError(
          err.retryAfter
            ? `验证码尝试过于频繁，请在 ${clock(err.retryAfter)} 后重试。`
            : '验证码尝试过于频繁，请稍后重试。',
        );
      else setError(err.message);
    } finally {
      setBusy(false);
    }
  }

  const header = (
    <>
      <Title order={1} size={32}>
        创建你的账户
      </Title>
      <Text c="dimmed" mt="sm" mb={32}>
        使用常用邮箱注册，验证邮箱后即可进入工作台。
      </Text>
    </>
  );
  const loginLink = (
    <Text ta="center" size="sm" c="dimmed" mt="xl">
      已经有账户？{' '}
      <Anchor component={Link} href="/login" fw={600}>
        直接登录
      </Anchor>
    </Text>
  );

  if (metaLoading && !meta)
    return (
      <>
        {header}
        <Group gap="sm" c="dimmed">
          <Loader size="sm" /> 正在读取注册设置…
        </Group>
        {loginLink}
      </>
    );
  if (!meta)
    return (
      <>
        {header}
        <Alert color="red" title="暂时无法读取注册设置" role="alert">
          {metaError}
          <Button mt="md" variant="light" size="xs" onClick={() => void reloadMeta()}>
            重试
          </Button>
        </Alert>
        {loginLink}
      </>
    );
  if (!open)
    return (
      <>
        {header}
        <Alert color="gray" title="当前暂未开放注册" role="status">
          新用户注册暂时关闭。已有账户可以正常登录。
        </Alert>
        {loginLink}
      </>
    );

  const sendLabel = sending
    ? '发送中'
    : cooldown > 0
      ? `${cooldown} 秒后可重发`
      : proof
        ? '重新发送'
        : '获取验证码';
  return (
    <>
      {header}
      <form onSubmit={submit} noValidate>
        <Stack gap="lg">
          {error ? (
            <Alert color="red" title="操作未完成" role="alert">
              {error}
            </Alert>
          ) : null}
          <TextInput
            label="姓名 / 昵称"
            placeholder="如何称呼你（2–40 个字符）"
            value={name}
            onChange={(e) => {
              setName(e.currentTarget.value);
              setFields((f) => ({ ...f, name: undefined }));
            }}
            error={fields.name}
            required
            autoComplete="nickname"
            size="md"
          />
          <TextInput
            type="email"
            label="邮箱地址"
            placeholder="you@qq.com"
            description={domains.length ? `支持：${domains.join('、')}` : undefined}
            value={email}
            onChange={(e) => changeEmail(e.currentTarget.value)}
            error={fields.email}
            required
            maxLength={254}
            autoComplete="email"
            disabled={busy}
            size="md"
          />
          <div>
            <Group align="flex-start" gap="sm" wrap="nowrap">
              <TextInput
                label="邮箱验证码"
                placeholder="6 位数字"
                value={code}
                onChange={(e) => {
                  setCode(codeDigits(e.currentTarget.value));
                  setFields((f) => ({ ...f, code: undefined }));
                }}
                error={fields.code}
                inputMode="numeric"
                autoComplete="one-time-code"
                required
                size="md"
                style={{ flex: 1 }}
              />
              <Button
                variant="light"
                size="md"
                mt={25}
                onClick={() => void sendCode()}
                loading={sending}
                disabled={busy || cooldown > 0}
                style={{ flexShrink: 0 }}
              >
                {sendLabel}
              </Button>
            </Group>
            {notice && proof ? (
              <Text size="xs" c="dimmed" mt={6} role="status">
                <IconMailCheck size={14} style={{ verticalAlign: '-2px' }} /> {notice}
                {validFor > 0
                  ? ` 验证码 ${clock(validFor)} 内有效，请使用最新一封邮件中的验证码。`
                  : ' 验证码已过期，请重新获取。'}
              </Text>
            ) : null}
          </div>
          <PasswordInput
            label="密码"
            placeholder={`${passwordMinBytes}–${passwordMaxBytes} 字节`}
            description="中文等字符每个占 3 字节；密码不会被裁剪或去除空格。"
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
            注册并进入工作台
          </Button>
        </Stack>
      </form>
      {loginLink}
      <Footnote />
    </>
  );
}
