'use client';
import { useEffect, useState } from 'react';
import {
  Alert,
  Badge,
  Button,
  Group,
  List,
  Modal,
  Paper,
  PasswordInput,
  ScrollArea,
  Skeleton,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { IconAlertTriangle } from '@tabler/icons-react';
import { useAuth } from './providers';
import { api, APIError, message } from '@/lib/api';
import {
  adminPasswordMinBytes,
  date,
  passwordMaxBytes,
  passwordMinBytes,
  utf8Bytes,
  type AccountLogin,
  type Dashboard,
  type User,
} from '@/lib/types';

export function AccountView() {
  const { user } = useAuth();
  if (!user) return null;
  return (
    <div className="page-wrap">
      <Title order={1} size={29} className="page-title">
        账号设置
      </Title>
      <Text size="sm" c="dimmed" mt={8} mb={30}>
        管理你的登录密码和账号。
      </Text>
      <Stack gap="xl" maw={820}>
        <Profile user={user} />
        <PasswordCard user={user} />
        <RecentLogins />
        <DangerZone user={user} />
      </Stack>
    </div>
  );
}

function Profile({ user }: { user: User }) {
  return (
    <Paper withBorder radius="lg" p="xl">
      <Title order={2} size={18} mb="md">
        基本信息
      </Title>
      <Stack gap="xs">
        <Row label="邮箱" value={user.email} testId="profile-email" />
        <Row label="姓名" value={user.name} />
        <Row label="注册时间" value={date(user.created_at)} />
      </Stack>
    </Paper>
  );
}

function Row({ label, value, testId }: { label: string; value: string; testId?: string }) {
  return (
    <Group gap="lg" wrap="nowrap" align="baseline">
      <Text size="sm" c="dimmed" w={72}>
        {label}
      </Text>
      <Text size="sm" fw={500} data-testid={testId} style={{ wordBreak: 'break-all' }}>
        {value}
      </Text>
    </Group>
  );
}

// Same byte rules as the server: 10–72 bytes (16–72 for administrators), measured in UTF-8.
function PasswordCard({ user }: { user: User }) {
  const minBytes = user.role === 'admin' ? adminPasswordMinBytes : passwordMinBytes;
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const nextBytes = utf8Bytes(next);
  const problem =
    next && (nextBytes < minBytes || nextBytes > passwordMaxBytes)
      ? `新密码需要 ${minBytes}–${passwordMaxBytes} 位（当前 ${nextBytes} 位，中文等字符每个计 3 位）`
      : next && next === current
        ? '新密码不能与当前密码相同'
        : confirm && confirm !== next
          ? '两次输入的新密码不一致'
          : null;
  const ready = !!current && !!next && !!confirm && !problem && !busy;
  const submit = async () => {
    if (!ready) return;
    setBusy(true);
    setError(null);
    setDone(false);
    try {
      await api('/me/password', {
        method: 'POST',
        body: JSON.stringify({ current_password: current, new_password: next }),
      });
      setCurrent('');
      setNext('');
      setConfirm('');
      setDone(true);
      notifications.show({
        color: 'teal',
        title: '密码已修改',
        message: '其他设备上的登录已退出。',
      });
    } catch (e) {
      if (e instanceof APIError && e.status === 429)
        setError(
          e.retryAfter
            ? `密码错误次数过多，请约 ${Math.ceil(e.retryAfter / 60)} 分钟后再试。`
            : '密码错误次数过多，请稍后再试。',
        );
      else setError(message(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Paper withBorder radius="lg" p="xl">
      <Title order={2} size={18} mb={6}>
        修改密码
      </Title>
      <Text size="sm" c="dimmed" mb="lg">
        修改后，你在其他浏览器和客户端上的登录会立即退出，当前浏览器保持登录。
      </Text>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <Stack maw={420}>
          <PasswordInput
            label="当前密码"
            autoComplete="current-password"
            value={current}
            onChange={(e) => setCurrent(e.currentTarget.value)}
          />
          <PasswordInput
            label="新密码"
            description={`${minBytes}–${passwordMaxBytes} 位`}
            autoComplete="new-password"
            value={next}
            onChange={(e) => setNext(e.currentTarget.value)}
          />
          <PasswordInput
            label="确认新密码"
            autoComplete="new-password"
            value={confirm}
            onChange={(e) => setConfirm(e.currentTarget.value)}
            error={confirm && confirm !== next ? '两次输入的新密码不一致' : undefined}
          />
          {problem && !(confirm && confirm !== next) ? (
            <Text size="sm" c="red" role="alert">
              {problem}
            </Text>
          ) : null}
          {error ? (
            <Alert color="red" role="alert">
              {error}
            </Alert>
          ) : null}
          {done ? (
            <Alert color="teal" role="status">
              密码已修改，其他设备上的登录已退出。
            </Alert>
          ) : null}
          <Group>
            <Button type="submit" disabled={!ready} loading={busy}>
              修改密码
            </Button>
          </Group>
        </Stack>
      </form>
    </Paper>
  );
}

const channelLabel = (login: AccountLogin) =>
  login.channel === 'web' ? '网站' : `客户端${login.platform ? `（${login.platform}）` : ''}`;
const locationLabel = (login: AccountLogin) =>
  login.geo_status === 'done' && login.geo_text ? login.geo_text : '—';

function RecentLogins() {
  const [logins, setLogins] = useState<AccountLogin[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    let active = true;
    void api<{ logins: AccountLogin[] }>('/me/logins')
      .then((result) => {
        if (active) setLogins(result.logins);
      })
      .catch((e) => {
        if (active) setError(message(e));
      });
    return () => {
      active = false;
    };
  }, []);
  return (
    <Paper withBorder radius="lg" p="xl">
      <Title order={2} size={18} mb={6}>
        最近登录
      </Title>
      <Text size="sm" c="dimmed" mb="md">
        最近 10 次登录尝试。如果看到不是你本人的登录，请立即修改密码。
      </Text>
      {error ? (
        <Alert color="red">{error}</Alert>
      ) : logins === null ? (
        <Skeleton height={120} radius="md" />
      ) : logins.length === 0 ? (
        <Text size="sm" c="dimmed">
          暂无记录。
        </Text>
      ) : (
        <ScrollArea>
          <Table verticalSpacing="xs" miw={560} data-testid="recent-logins">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>时间</Table.Th>
                <Table.Th>IP</Table.Th>
                <Table.Th>归属地</Table.Th>
                <Table.Th>渠道</Table.Th>
                <Table.Th>结果</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {logins.map((login, i) => (
                <Table.Tr key={`${login.occurred_at}-${i}`}>
                  <Table.Td>{date(login.occurred_at)}</Table.Td>
                  <Table.Td>{login.ip}</Table.Td>
                  <Table.Td>{locationLabel(login)}</Table.Td>
                  <Table.Td>{channelLabel(login)}</Table.Td>
                  <Table.Td>
                    <Badge color={login.result === 'success' ? 'teal' : 'red'} variant="light">
                      {login.result === 'success' ? '成功' : '失败'}
                    </Badge>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </ScrollArea>
      )}
    </Paper>
  );
}

const deleteDelaySeconds = 5;

function DangerZone({ user }: { user: User }) {
  const [opened, setOpened] = useState(false);
  return (
    <Paper
      withBorder
      radius="lg"
      p="xl"
      style={{ borderColor: 'var(--mantine-color-red-4)' }}
      data-testid="danger-zone"
    >
      <Title order={2} size={18} c="red.8" mb={6}>
        删除账号
      </Title>
      {user.role === 'admin' ? (
        <Text size="sm" c="dimmed">
          管理员账号不能在网站上自助删除。
        </Text>
      ) : (
        <>
          <Text size="sm" c="dimmed" mb="lg">
            删除账号后，当前套餐、订单、登录记录和设备会话会被永久清除，无法恢复。
          </Text>
          <Button color="red" variant="outline" onClick={() => setOpened(true)}>
            删除账号
          </Button>
          <DeleteDialog user={user} opened={opened} onClose={() => setOpened(false)} />
        </>
      )}
    </Paper>
  );
}

function DeleteDialog({
  user,
  opened,
  onClose,
}: {
  user: User;
  opened: boolean;
  onClose: () => void;
}) {
  // The form mounts only while the dialog is open, so every opening starts clean with a
  // fresh countdown and nothing typed survives a close.
  const [busy, setBusy] = useState(false);
  return (
    <Modal
      opened={opened}
      onClose={busy ? () => undefined : onClose}
      title="删除账号"
      centered
      closeOnClickOutside={!busy}
    >
      {opened ? <DeleteForm user={user} onClose={onClose} busy={busy} setBusy={setBusy} /> : null}
    </Modal>
  );
}

function DeleteForm({
  user,
  onClose,
  busy,
  setBusy,
}: {
  user: User;
  onClose: () => void;
  busy: boolean;
  setBusy: (busy: boolean) => void;
}) {
  const { setUser } = useAuth();
  const [password, setPassword] = useState('');
  const [email, setEmail] = useState('');
  const [seconds, setSeconds] = useState(deleteDelaySeconds);
  const [error, setError] = useState<string | null>(null);
  const [overview, setOverview] = useState<Dashboard | null>(null);
  useEffect(() => {
    const timer = setInterval(() => setSeconds((s) => Math.max(0, s - 1)), 1000);
    let active = true;
    void api<Dashboard>('/me/dashboard')
      .then((d) => {
        if (active) setOverview(d);
      })
      .catch(() => undefined);
    return () => {
      active = false;
      clearInterval(timer);
    };
  }, []);
  const sub = overview?.subscription;
  const entitled = !!sub && !!overview?.entitlement_active;
  const ready = seconds === 0 && !!password && email.trim().toLowerCase() === user.email && !busy;
  const submit = async () => {
    if (!ready) return;
    setBusy(true);
    setError(null);
    try {
      await api('/me/account/delete', {
        method: 'POST',
        body: JSON.stringify({ password, email: email.trim() }),
      });
      setUser(null);
      // A full page load: the signed-in state, dialogs and caches of the deleted account go
      // with it, and RequireAuth cannot redirect to the login page instead of the home page.
      window.location.replace('/');
    } catch (e) {
      setError(message(e));
      setBusy(false);
    }
  };
  return (
    <>
      <Stack>
        <Alert color="red" icon={<IconAlertTriangle size={18} />} title="此操作无法撤销">
          <List size="sm" spacing={4}>
            <List.Item>
              {entitled
                ? `当前套餐「${sub!.plan_name}」及剩余时长和流量（有效期至 ${date(sub!.expires_at)}），不退款、不可恢复`
                : '当前套餐和剩余额度'}
            </List.Item>
            <List.Item>全部订单记录</List.Item>
            <List.Item>所有设备上的登录会话，客户端会立即退出</List.Item>
            <List.Item>登录记录和用量记录</List.Item>
          </List>
        </Alert>
        <PasswordInput
          label="当前密码"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.currentTarget.value)}
        />
        <TextInput
          label={`请输入完整邮箱 ${user.email} 以确认`}
          autoComplete="off"
          value={email}
          onChange={(e) => setEmail(e.currentTarget.value)}
        />
        {error ? (
          <Alert color="red" role="alert">
            {error}
          </Alert>
        ) : null}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button color="red" disabled={!ready} loading={busy} onClick={() => void submit()}>
            {seconds > 0 ? `永久删除账号（${seconds}）` : '永久删除账号'}
          </Button>
        </Group>
      </Stack>
    </>
  );
}
