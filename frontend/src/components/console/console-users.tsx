'use client';
import { useEffect, useState } from 'react';
import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Drawer,
  Group,
  Modal,
  NumberInput,
  Paper,
  PasswordInput,
  ScrollArea,
  SegmentedControl,
  Select,
  Skeleton,
  Stack,
  Table,
  Text,
  TextInput,
} from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { api, APIError } from '@/lib/api';
import {
  gib,
  money,
  orderStatusLabel,
  passwordMaxBytes,
  passwordMinBytes,
  payTypeLabel,
  utf8Bytes,
  type Plan,
} from '@/lib/types';
import {
  dayEnd,
  dayStart,
  fromLocalInput,
  geoLabel,
  gibToBytes,
  gibToUnits,
  platformLabel,
  toLocalInput,
  unitsToGib,
  validDay,
  type ConsoleDevice,
  type ConsoleLogin,
  type ConsoleOrder,
  type ConsoleSub,
  type ConsoleUser,
} from '@/lib/console';
import {
  describeError,
  ErrorBox,
  PageSizeSelect,
  Pager,
  ReauthField,
  useConsoleList,
  useFetched,
  When,
  type Values,
} from './console-common';
import { useAuth } from '../providers';

const keys = [
  'q',
  'role',
  'status',
  'sub',
  'plan',
  'created_from',
  'created_to',
  'login_from',
  'login_to',
  'sort',
  'order',
  'page_size',
  'page',
  'user',
] as const;

function toApi(v: Values): Values {
  return {
    q: v.q,
    role: v.role,
    status: v.status,
    sub: v.sub,
    plan: v.plan,
    sort: v.sort,
    order: v.order,
    page_size: v.page_size,
    created_from: validDay(v.created_from) ? dayStart(v.created_from) : '',
    created_to: validDay(v.created_to) ? dayEnd(v.created_to) : '',
    login_from: validDay(v.login_from) ? dayStart(v.login_from) : '',
    login_to: validDay(v.login_to) ? dayEnd(v.login_to) : '',
  };
}

type UserList = { users: ConsoleUser[]; total: number; page: number; page_size: number };

const fmtGib = (n: number) =>
  new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 }).format(n);

export function subscriptionText(sub: ConsoleSub | null) {
  if (!sub) return { text: '无套餐', color: 'gray' };
  if (sub.state === 'expired') return { text: '已过期', color: 'gray' };
  return sub.is_test ? { text: '测试套餐', color: 'yellow' } : { text: '有效', color: 'teal' };
}

export function ConsoleUsers() {
  const list = useConsoleList<UserList>('/admin/console/users', keys, toApi);
  const { values, apply } = list;
  // Edits typed into the form sit over the URL's values until they are applied.
  const [edits, setEdits] = useState<Values>({});
  const draft = { ...values, ...edits };
  const set = (key: string, value: string | null) =>
    setEdits((d) => ({ ...d, [key]: value ?? '' }));
  const go = (patch: Values) => {
    setEdits({});
    apply(patch);
  };
  const [plans, setPlans] = useState<Plan[]>([]);
  useEffect(() => {
    void api<{ plans: Plan[] }>('/plans')
      .then((r) => setPlans(r.plans))
      .catch(() => undefined);
  }, []);
  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    go({ ...draft, user: '', page: '' });
  };
  const size = values.page_size === '50' ? 50 : 20;
  return (
    <Stack gap="lg">
      <Paper withBorder radius="lg" p="lg">
        <form onSubmit={submit} aria-label="用户筛选">
          <Group align="flex-end" gap="md" wrap="wrap">
            <TextInput
              label="邮箱或姓名"
              placeholder="关键词"
              value={draft.q}
              onChange={(e) => set('q', e.currentTarget.value)}
              w={220}
            />
            <Select
              label="角色"
              clearable
              w={120}
              data={[
                { value: 'user', label: '普通用户' },
                { value: 'admin', label: '管理员' },
              ]}
              value={draft.role || null}
              onChange={(v) => set('role', v)}
            />
            <Select
              label="账号状态"
              clearable
              w={130}
              data={[
                { value: 'active', label: '正常' },
                { value: 'deleting', label: '删除中' },
              ]}
              value={draft.status || null}
              onChange={(v) => set('status', v)}
            />
            <Select
              label="套餐"
              clearable
              w={150}
              data={plans.map((p) => ({ value: p.id, label: p.name }))}
              value={draft.plan || null}
              onChange={(v) => set('plan', v)}
            />
            <Select
              label="订阅状态"
              clearable
              w={130}
              data={[
                { value: 'active', label: '有效' },
                { value: 'expired', label: '已过期' },
                { value: 'none', label: '无套餐' },
                { value: 'test', label: '测试套餐' },
              ]}
              value={draft.sub || null}
              onChange={(v) => set('sub', v)}
            />
            <TextInput
              label="注册自"
              type="date"
              value={draft.created_from}
              onChange={(e) => set('created_from', e.currentTarget.value)}
            />
            <TextInput
              label="注册至"
              type="date"
              value={draft.created_to}
              onChange={(e) => set('created_to', e.currentTarget.value)}
            />
            <TextInput
              label="最近登录自"
              type="date"
              value={draft.login_from}
              onChange={(e) => set('login_from', e.currentTarget.value)}
            />
            <TextInput
              label="最近登录至"
              type="date"
              value={draft.login_to}
              onChange={(e) => set('login_to', e.currentTarget.value)}
            />
            <Select
              label="排序"
              allowDeselect={false}
              w={150}
              data={[
                { value: 'created_at', label: '注册时间' },
                { value: 'last_login_at', label: '最近登录' },
                { value: 'expires_at', label: '到期时间' },
              ]}
              value={draft.sort || 'created_at'}
              onChange={(v) => set('sort', v === 'created_at' ? '' : v)}
            />
            <Select
              label="顺序"
              allowDeselect={false}
              w={100}
              data={[
                { value: 'desc', label: '降序' },
                { value: 'asc', label: '升序' },
              ]}
              value={draft.order || 'desc'}
              onChange={(v) => set('order', v === 'desc' ? '' : v)}
            />
            <PageSizeSelect value={draft.page_size} onChange={(v) => set('page_size', v)} />
            <Button type="submit">筛选</Button>
            <Button
              variant="subtle"
              onClick={() => go(Object.fromEntries(keys.map((k) => [k, ''])))}
            >
              重置
            </Button>
          </Group>
        </form>
      </Paper>
      <ErrorBox text={list.error} />
      <Paper withBorder radius="lg" p="md">
        {list.loading && !list.data ? (
          <Skeleton height={260} />
        ) : (
          <>
            <ScrollArea>
              <Table verticalSpacing="sm" horizontalSpacing="md" miw={1200} aria-label="用户列表">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>用户</Table.Th>
                    <Table.Th>状态</Table.Th>
                    <Table.Th>注册时间 / IP</Table.Th>
                    <Table.Th>套餐</Table.Th>
                    <Table.Th>流量（已用 / 总量）</Table.Th>
                    <Table.Th>最近登录</Table.Th>
                    <Table.Th>在线</Table.Th>
                    <Table.Th />
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {(list.data?.users ?? []).map((u) => (
                    <UserRow
                      key={u.id}
                      user={u}
                      onOpen={() => apply({ user: u.id, page: values.page })}
                    />
                  ))}
                </Table.Tbody>
              </Table>
              {list.data && list.data.users.length === 0 ? (
                <Text ta="center" c="dimmed" py="xl">
                  没有符合条件的用户
                </Text>
              ) : null}
            </ScrollArea>
            <Pager
              page={list.page}
              total={list.data?.total ?? 0}
              size={size}
              onChange={(p) => apply({ page: p === 1 ? '' : String(p), user: '' })}
            />
          </>
        )}
      </Paper>
      <UserDrawer
        id={values.user}
        plans={plans}
        onClose={() => apply({ user: '', page: values.page })}
        onChanged={list.reload}
      />
    </Stack>
  );
}

function UserRow({ user: u, onOpen }: { user: ConsoleUser; onOpen: () => void }) {
  const sub = subscriptionText(u.subscription);
  return (
    <Table.Tr data-testid="user-row">
      <Table.Td>
        <Text size="sm" fw={600}>
          {u.email}
        </Text>
        <Text size="xs" c="dimmed">
          {u.name}
        </Text>
      </Table.Td>
      <Table.Td>
        <Group gap={6}>
          {u.role === 'admin' ? <Badge color="violet">管理员</Badge> : null}
          {u.status === 'deleting' ? (
            <Badge color="red">删除中</Badge>
          ) : (
            <Badge variant="light">正常</Badge>
          )}
        </Group>
      </Table.Td>
      <Table.Td>
        <Text size="sm">
          <When value={u.created_at} />
        </Text>
        <Text size="xs" c="dimmed">
          {u.register_ip ?? '—'}
        </Text>
      </Table.Td>
      <Table.Td>
        <Group gap={6}>
          <Badge color={sub.color} variant="light">
            {sub.text}
          </Badge>
          {u.subscription ? <Text size="sm">{u.subscription.plan_name}</Text> : null}
        </Group>
        {u.subscription ? (
          <Text size="xs" c="dimmed">
            到期 <When value={u.subscription.expires_at} />
          </Text>
        ) : null}
      </Table.Td>
      <Table.Td>
        {u.subscription ? (
          <Text size="sm">
            {fmtGib(unitsToGib(u.subscription.used_units))} /{' '}
            {gib(u.subscription.traffic_limit_bytes)} GiB
          </Text>
        ) : (
          '—'
        )}
      </Table.Td>
      <Table.Td>
        <Text size="sm">
          <When value={u.last_login_at} />
        </Text>
        <Text size="xs" c="dimmed">
          {u.last_login_ip ? `${u.last_login_ip} · ${u.last_login_geo || '未解析'}` : '—'}
        </Text>
      </Table.Td>
      <Table.Td>{u.online_devices}</Table.Td>
      <Table.Td>
        <Button size="xs" variant="light" onClick={onOpen} aria-label={`查看 ${u.email}`}>
          详情
        </Button>
      </Table.Td>
    </Table.Tr>
  );
}

type Detail = {
  user: ConsoleUser;
  orders: ConsoleOrder[];
  logins: ConsoleLogin[];
  devices: { online: ConsoleDevice[]; signed_in: ConsoleDevice[] };
};
type Action = 'password' | 'subscription' | 'end' | 'delete' | 'impersonate' | null;

function UserDrawer({
  id,
  plans,
  onClose,
  onChanged,
}: {
  id: string;
  plans: Plan[];
  onClose: () => void;
  onChanged: () => void;
}) {
  const { user: me } = useAuth();
  const [action, setAction] = useState<Action>(null);
  const [nonce, setNonce] = useState(0);
  const { data: detail, error } = useFetched<Detail>(
    id ? `/admin/console/users/${encodeURIComponent(id)}` : null,
    nonce,
  );
  const refresh = () => {
    setNonce((n) => n + 1);
    onChanged();
  };
  const u = detail?.user.id === id ? detail.user : null;
  const protectedReason =
    u && me
      ? u.id === me.id
        ? '这是你自己的账号'
        : u.role === 'admin'
          ? '不能对管理员账号执行此操作'
          : ''
      : '';
  return (
    <Drawer
      opened={!!id}
      onClose={onClose}
      position="right"
      size="xl"
      title={u ? `用户详情 · ${u.email}` : '用户详情'}
    >
      <Stack gap="lg" data-testid="user-detail">
        <ErrorBox text={error} />
        {!u && !error ? <Skeleton height={200} /> : null}
        {u && detail ? (
          <>
            <Paper withBorder radius="md" p="md">
              <Stack gap={4}>
                <Info label="姓名" value={u.name} />
                <Info label="角色" value={u.role === 'admin' ? '管理员' : '普通用户'} />
                <Info label="状态" value={u.status === 'deleting' ? '删除中' : '正常'} />
                <Info
                  label="注册"
                  value={
                    <>
                      <When value={u.created_at} /> · {u.register_ip ?? '—'}
                    </>
                  }
                />
                <Info
                  label="最近登录"
                  value={
                    u.last_login_at ? (
                      <>
                        <When value={u.last_login_at} /> · {u.last_login_ip} ·{' '}
                        {u.last_login_geo || '未解析'}
                      </>
                    ) : (
                      '从未登录（或在该功能上线前注册）'
                    )
                  }
                />
                <Info label="在线设备" value={String(u.online_devices)} />
              </Stack>
            </Paper>
            <SubscriptionBlock sub={u.subscription} />
            <Group gap="sm">
              <Button
                variant="light"
                onClick={() => setAction('subscription')}
                disabled={u.status !== 'active'}
              >
                修改套餐
              </Button>
              <Button
                variant="light"
                color="orange"
                onClick={() => setAction('end')}
                disabled={u.status !== 'active' || u.subscription?.state !== 'active'}
              >
                结束订阅
              </Button>
              <Button
                variant="light"
                onClick={() => setAction('password')}
                disabled={!!protectedReason || u.status !== 'active'}
                title={protectedReason}
              >
                修改密码
              </Button>
              <Button
                variant="light"
                color="violet"
                onClick={() => setAction('impersonate')}
                disabled={!!protectedReason || u.status !== 'active'}
                title={protectedReason}
              >
                模拟登录
              </Button>
              <Button
                color="red"
                variant="light"
                onClick={() => setAction('delete')}
                disabled={!!protectedReason || u.status !== 'active'}
                title={protectedReason}
              >
                删除用户
              </Button>
            </Group>
            {protectedReason ? (
              <Text size="xs" c="dimmed">
                {protectedReason}，改密和删除已禁用。
              </Text>
            ) : null}
            <Section title="最近订单">
              {detail.orders.length === 0 ? (
                <Text size="sm" c="dimmed">
                  暂无订单
                </Text>
              ) : (
                <Table verticalSpacing={6} aria-label="最近订单">
                  <Table.Tbody>
                    {detail.orders.map((o) => {
                      const label = orderStatusLabel[o.status];
                      return (
                        <Table.Tr key={o.id}>
                          <Table.Td>{o.plan_name}</Table.Td>
                          <Table.Td>{money(o.amount_cents || o.price_cents)}</Table.Td>
                          <Table.Td>{payTypeLabel[o.pay_type] ?? '—'}</Table.Td>
                          <Table.Td>
                            <Badge color={label?.color ?? 'gray'} variant="light">
                              {label?.text ?? o.status}
                            </Badge>
                          </Table.Td>
                          <Table.Td>
                            <When value={o.created_at} />
                          </Table.Td>
                        </Table.Tr>
                      );
                    })}
                  </Table.Tbody>
                </Table>
              )}
            </Section>
            <Section title="最近登录">
              {detail.logins.length === 0 ? (
                <Text size="sm" c="dimmed">
                  暂无记录
                </Text>
              ) : (
                <Table verticalSpacing={6} aria-label="最近登录">
                  <Table.Tbody>
                    {detail.logins.map((l) => (
                      <Table.Tr key={l.id}>
                        <Table.Td>
                          <When value={l.occurred_at} />
                        </Table.Td>
                        <Table.Td>
                          {l.channel === 'web' ? '网站' : (platformLabel[l.platform] ?? '客户端')}
                        </Table.Td>
                        <Table.Td>{l.ip}</Table.Td>
                        <Table.Td>{geoLabel(l.geo_status, l.geo_text)}</Table.Td>
                        <Table.Td>
                          <Badge color={l.result === 'success' ? 'teal' : 'red'} variant="light">
                            {l.result === 'success' ? '成功' : '失败'}
                          </Badge>
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              )}
            </Section>
            <Section title="已登录设备">
              {detail.devices.online.length + detail.devices.signed_in.length === 0 ? (
                <Text size="sm" c="dimmed">
                  没有已登录的客户端
                </Text>
              ) : (
                <Table verticalSpacing={6} aria-label="已登录设备">
                  <Table.Tbody>
                    {detail.devices.online.map((d) => (
                      <Table.Tr key={'o' + d.device}>
                        <Table.Td>{platformLabel[d.platform] ?? d.platform}</Table.Td>
                        <Table.Td>…{d.device}</Table.Td>
                        <Table.Td>{d.app_version}</Table.Td>
                        <Table.Td>
                          <Badge color="teal" variant="light">
                            在线 {d.line}
                          </Badge>
                        </Table.Td>
                      </Table.Tr>
                    ))}
                    {detail.devices.signed_in.map((d) => (
                      <Table.Tr key={'s' + d.device}>
                        <Table.Td>{platformLabel[d.platform] ?? d.platform}</Table.Td>
                        <Table.Td>…{d.device}</Table.Td>
                        <Table.Td>{d.app_version}</Table.Td>
                        <Table.Td>
                          <Badge color="gray" variant="light">
                            已登录未连接
                          </Badge>
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              )}
            </Section>
            {action === 'password' ? (
              <PasswordModal user={u} onClose={() => setAction(null)} onDone={refresh} />
            ) : null}
            {action === 'subscription' ? (
              <SubscriptionModal
                user={u}
                plans={plans}
                onClose={() => setAction(null)}
                onDone={refresh}
              />
            ) : null}
            {action === 'impersonate' ? (
              <ImpersonateModal user={u} onClose={() => setAction(null)} onDone={() => undefined} />
            ) : null}
            {action === 'end' ? (
              <EndModal user={u} onClose={() => setAction(null)} onDone={refresh} />
            ) : null}
            {action === 'delete' ? (
              <DeleteModal
                user={u}
                onClose={() => setAction(null)}
                onDone={() => {
                  onClose();
                  onChanged();
                }}
              />
            ) : null}
          </>
        ) : null}
      </Stack>
    </Drawer>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <Stack gap={6}>
      <Text fw={600}>{title}</Text>
      {children}
    </Stack>
  );
}

function Info({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <Group gap="lg" wrap="nowrap" align="baseline">
      <Text size="sm" c="dimmed" w={72} style={{ flexShrink: 0 }}>
        {label}
      </Text>
      <Text size="sm" component="div" style={{ wordBreak: 'break-all' }}>
        {value}
      </Text>
    </Group>
  );
}

function SubscriptionBlock({ sub }: { sub: ConsoleSub | null }) {
  const state = subscriptionText(sub);
  return (
    <Paper withBorder radius="md" p="md" data-testid="subscription-block">
      <Group justify="space-between" mb={6}>
        <Text fw={600}>订阅</Text>
        <Badge color={state.color} variant="light">
          {state.text}
        </Badge>
      </Group>
      {sub ? (
        <Stack gap={4}>
          <Info label="套餐" value={`${sub.plan_name}（${sub.plan_id}）`} />
          <Info label="开始" value={<When value={sub.starts_at} />} />
          <Info label="到期" value={<When value={sub.expires_at} />} />
          <Info
            label="流量"
            value={`已用 ${fmtGib(unitsToGib(sub.used_units))} GiB / 总量 ${gib(sub.traffic_limit_bytes)} GiB`}
          />
          <Info
            label="设备上限"
            value={`${sub.max_devices}${sub.allow_dedicated ? '，含专线' : ''}`}
          />
        </Stack>
      ) : (
        <Text size="sm" c="dimmed">
          该用户当前没有订阅
        </Text>
      )}
    </Paper>
  );
}

type ModalProps = { user: ConsoleUser; onClose: () => void; onDone: () => void };

function PasswordModal({ user, onClose, onDone }: ModalProps) {
  const [adminPassword, setAdminPassword] = useState('');
  const [next, setNext] = useState('');
  const [notify, setNotify] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const bytes = utf8Bytes(next);
  const valid = bytes >= passwordMinBytes && bytes <= passwordMaxBytes && adminPassword !== '';
  const submit = async () => {
    setBusy(true);
    setError('');
    try {
      await api(`/admin/console/users/${user.id}/password`, {
        method: 'POST',
        body: JSON.stringify({ admin_password: adminPassword, new_password: next, notify }),
      });
      notifications.show({
        color: 'teal',
        title: '密码已修改',
        message: '该用户所有设备已退出登录。',
      });
      onDone();
      onClose();
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal opened onClose={onClose} title={`修改密码 · ${user.email}`} closeOnClickOutside={false}>
      <Stack>
        <Text size="sm" c="dimmed">
          设置后，该用户在所有设备上的登录会立即退出。你看不到用户的旧密码。
        </Text>
        <PasswordInput
          label="新密码"
          description={`${passwordMinBytes}–${passwordMaxBytes} 字节（普通用户）`}
          autoComplete="new-password"
          value={next}
          onChange={(e) => setNext(e.currentTarget.value)}
          error={
            next && !(bytes >= passwordMinBytes && bytes <= passwordMaxBytes)
              ? '长度不符合要求'
              : undefined
          }
        />
        <Checkbox
          label="通知用户（邮件已配置时发送）"
          checked={notify}
          onChange={(e) => setNotify(e.currentTarget.checked)}
        />
        <ReauthField value={adminPassword} onChange={setAdminPassword} />
        <ErrorBox text={error} />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            取消
          </Button>
          <Button onClick={() => void submit()} loading={busy} disabled={!valid}>
            确认修改
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

function SubscriptionModal({ user, plans, onClose, onDone }: ModalProps & { plans: Plan[] }) {
  const sub = user.subscription;
  const [planId, setPlanId] = useState<string | null>(sub?.plan_id ?? null);
  const [expires, setExpires] = useState(sub ? toLocalInput(sub.expires_at) : '');
  const [starts, setStarts] = useState(sub ? toLocalInput(sub.starts_at) : '');
  const [limit, setLimit] = useState<number | string>(
    sub ? sub.traffic_limit_bytes / 1024 ** 3 : '',
  );
  const [usedMode, setUsedMode] = useState<'keep' | 'reset' | 'set'>('keep');
  const [used, setUsed] = useState<number | string>(sub ? unitsToGib(sub.used_units) : 0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [conflict, setConflict] = useState(false);
  const planChanged = !!planId && planId !== sub?.plan_id;
  const startsChanged = !!sub && starts !== toLocalInput(sub.starts_at);
  const reauthorises = !!sub && (planChanged || startsChanged);
  const submit = async () => {
    setBusy(true);
    setError('');
    const body: Record<string, unknown> = { version: sub?.version ?? 0, used: usedMode };
    if (planChanged || (!sub && planId)) body.plan_id = planId;
    if (expires && (!sub || expires !== toLocalInput(sub.expires_at)))
      body.expires_at = fromLocalInput(expires);
    if (startsChanged) body.starts_at = fromLocalInput(starts);
    if (limit !== '' && (!sub || Number(limit) !== sub.traffic_limit_bytes / 1024 ** 3))
      body.traffic_limit_bytes = gibToBytes(Number(limit));
    if (usedMode === 'set') body.used_units = gibToUnits(Number(used) || 0);
    try {
      await api(`/admin/console/users/${user.id}/subscription`, {
        method: 'POST',
        body: JSON.stringify(body),
      });
      notifications.show({ color: 'teal', title: '套餐已保存', message: user.email });
      onDone();
      onClose();
    } catch (e) {
      setError(describeError(e));
      setConflict(e instanceof APIError && e.code === 'version_conflict');
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      opened
      onClose={onClose}
      title={`修改套餐 · ${user.email}`}
      size="lg"
      closeOnClickOutside={false}
    >
      <Stack>
        <Select
          label="套餐"
          placeholder="选择套餐"
          data={plans.map((p) => ({ value: p.id, label: `${p.name}（${p.id}）` }))}
          value={planId}
          onChange={setPlanId}
          allowDeselect={false}
        />
        <Group grow align="flex-start">
          <TextInput
            label="开始时间"
            type="datetime-local"
            value={starts}
            onChange={(e) => setStarts(e.currentTarget.value)}
            disabled={!sub}
            description={sub ? '修改开始时间会开启新周期' : '新开套餐从现在开始'}
          />
          <TextInput
            label="到期时间"
            type="datetime-local"
            value={expires}
            onChange={(e) => setExpires(e.currentTarget.value)}
          />
        </Group>
        <NumberInput
          label="总流量（GiB）"
          description={!sub && planId ? '留空则使用套餐默认流量' : undefined}
          min={0.01}
          decimalScale={2}
          value={limit}
          onChange={setLimit}
        />
        <div>
          <Text size="sm" fw={500} mb={4}>
            已用流量
          </Text>
          <SegmentedControl
            value={usedMode}
            onChange={(v) => setUsedMode(v as 'keep' | 'reset' | 'set')}
            data={[
              { value: 'keep', label: '保持' },
              { value: 'reset', label: '清零' },
              { value: 'set', label: '指定值' },
            ]}
          />
          {usedMode === 'set' ? (
            <NumberInput
              mt="xs"
              label="已用（GiB）"
              min={0}
              decimalScale={3}
              value={used}
              onChange={setUsed}
            />
          ) : null}
        </div>
        {reauthorises ? (
          <Alert color="yellow">更换套餐或开新周期会使该用户的客户端重新授权。</Alert>
        ) : (
          <Text size="xs" c="dimmed">
            只延长到期时间或增加流量，不会打断用户已连接的客户端。
          </Text>
        )}
        <ErrorBox text={error} />
        {conflict ? (
          <Button
            variant="light"
            onClick={() => {
              onDone();
              onClose();
            }}
          >
            刷新最新数据
          </Button>
        ) : null}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            取消
          </Button>
          <Button onClick={() => void submit()} loading={busy} disabled={!sub && !planId}>
            保存
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

function EndModal({ user, onClose, onDone }: ModalProps) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const submit = async () => {
    setBusy(true);
    try {
      await api(`/admin/console/users/${user.id}/subscription/end`, {
        method: 'POST',
        body: JSON.stringify({ version: user.subscription?.version ?? 0 }),
      });
      notifications.show({ color: 'teal', title: '订阅已结束', message: user.email });
      onDone();
      onClose();
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal opened onClose={onClose} title="结束订阅">
      <Stack>
        <Text size="sm">
          立即结束 {user.email} 的“{user.subscription?.plan_name}
          ”订阅。该用户的客户端会立刻失去授权，剩余流量不退还。
        </Text>
        <ErrorBox text={error} />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            取消
          </Button>
          <Button color="orange" onClick={() => void submit()} loading={busy}>
            确认结束
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

// Opens a read-only session as the user. The browser then carries the user's session (and the
// return cookie), so the page is replaced instead of patched.
function ImpersonateModal({ user, onClose }: ModalProps) {
  const [adminPassword, setAdminPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const submit = async () => {
    setBusy(true);
    setError('');
    try {
      await api(`/admin/console/users/${user.id}/impersonate`, {
        method: 'POST',
        body: JSON.stringify({ admin_password: adminPassword }),
      });
      window.location.replace('/dashboard');
    } catch (e) {
      setError(describeError(e));
      setBusy(false);
    }
  };
  return (
    <Modal opened onClose={onClose} title={`模拟登录 · ${user.email}`} closeOnClickOutside={false}>
      <Stack>
        <Alert color="violet">
          你将以该用户的身份查看网站，期间只读：不能修改任何数据，不会显示公告，也不会改变该用户的最近登录时间。到时间会自动结束，随时可点“返回管理员”。
        </Alert>
        <ReauthField value={adminPassword} onChange={setAdminPassword} />
        <ErrorBox text={error} />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            取消
          </Button>
          <Button onClick={() => void submit()} loading={busy} disabled={adminPassword === ''}>
            开始模拟
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

function DeleteModal({ user, onClose, onDone }: ModalProps) {
  const [adminPassword, setAdminPassword] = useState('');
  const [email, setEmail] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const match = email.trim().toLowerCase() === user.email;
  const submit = async () => {
    setBusy(true);
    setError('');
    try {
      await api(`/admin/console/users/${user.id}/delete`, {
        method: 'POST',
        body: JSON.stringify({ admin_password: adminPassword, email }),
      });
      notifications.show({
        color: 'teal',
        title: '账号已删除',
        message: '关联数据将在节点结清后自动清除。',
      });
      onDone();
      onClose();
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal opened onClose={onClose} title="删除用户" closeOnClickOutside={false}>
      <Stack>
        <Alert color="red" title="危险操作，无法恢复">
          将立即停用该账号并退出所有设备，随后清除其套餐、订单、设备会话、登录记录等全部关联数据。剩余权益不退款。
        </Alert>
        <TextInput
          label={`输入目标邮箱 ${user.email} 以确认`}
          value={email}
          onChange={(e) => setEmail(e.currentTarget.value)}
          autoComplete="off"
        />
        <ReauthField value={adminPassword} onChange={setAdminPassword} />
        <ErrorBox text={error} />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            取消
          </Button>
          <Button
            color="red"
            onClick={() => void submit()}
            loading={busy}
            disabled={!match || adminPassword === ''}
          >
            永久删除
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
