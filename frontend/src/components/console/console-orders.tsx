'use client';
import { useState } from 'react';
import {
  Alert,
  Badge,
  Button,
  Drawer,
  Group,
  Modal,
  NumberInput,
  Paper,
  ScrollArea,
  Select,
  SimpleGrid,
  Skeleton,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
} from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { api } from '@/lib/api';
import { money, orderStatusLabel, payTypeLabel } from '@/lib/types';
import {
  dayEnd,
  dayStart,
  notifyResultLabel,
  priceSourceLabel,
  reviewReasonLabel,
  validDay,
  type ConsoleNotification,
  type ConsoleOrder,
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

const keys = [
  'status',
  'pay_type',
  'source',
  'billing_cycle',
  'email',
  'trade_no',
  'order_id',
  'created_from',
  'created_to',
  'paid_from',
  'paid_to',
  'page_size',
  'page',
  'order',
] as const;

function toApi(v: Values): Values {
  return {
    status: v.status,
    pay_type: v.pay_type,
    source: v.source,
    billing_cycle: v.billing_cycle,
    email: v.email,
    trade_no: v.trade_no,
    order_id: v.order_id,
    page_size: v.page_size,
    created_from: validDay(v.created_from) ? dayStart(v.created_from) : '',
    created_to: validDay(v.created_to) ? dayEnd(v.created_to) : '',
    paid_from: validDay(v.paid_from) ? dayStart(v.paid_from) : '',
    paid_to: validDay(v.paid_to) ? dayEnd(v.paid_to) : '',
  };
}

type Summary = { count: number; cents: number };
type OrderList = {
  orders: ConsoleOrder[];
  total: number;
  summary: { today: Summary; month: Summary; note: string };
};

const statusData = [
  { value: 'pending', label: '待付款' },
  { value: 'paid', label: '已支付' },
  { value: 'expired', label: '已超时' },
  { value: 'failed', label: '失败 / 已关闭' },
  { value: 'review', label: '待人工核对' },
  { value: 'paid_test', label: '模拟成功' },
];

export function ConsoleOrders() {
  const list = useConsoleList<OrderList>('/admin/console/orders', keys, toApi);
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
  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    go({ ...draft, order: '', page: '' });
  };
  const size = values.page_size === '50' ? 50 : 20;
  const summary = list.data?.summary;
  return (
    <Stack gap="lg">
      {summary ? (
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <Paper withBorder radius="lg" p="lg" data-testid="summary-today">
            <Text size="sm" c="dimmed">
              今日已支付
            </Text>
            <Text fw={700} size="xl">
              {summary.today.count} 笔 · {money(summary.today.cents)}
            </Text>
          </Paper>
          <Paper withBorder radius="lg" p="lg" data-testid="summary-month">
            <Text size="sm" c="dimmed">
              本月已支付
            </Text>
            <Text fw={700} size="xl">
              {summary.month.count} 笔 · {money(summary.month.cents)}
            </Text>
          </Paper>
          <Text size="xs" c="dimmed">
            {summary.note}
          </Text>
        </SimpleGrid>
      ) : null}
      <Paper withBorder radius="lg" p="lg">
        <form onSubmit={submit} aria-label="订单筛选">
          <Group align="flex-end" gap="md" wrap="wrap">
            <Select
              label="状态"
              clearable
              w={150}
              data={statusData}
              value={draft.status || null}
              onChange={(v) => set('status', v)}
            />
            <Select
              label="支付方式"
              clearable
              w={130}
              data={[
                { value: 'wxpay', label: '微信支付' },
                { value: 'alipay', label: '支付宝' },
              ]}
              value={draft.pay_type || null}
              onChange={(v) => set('pay_type', v)}
            />
            <Select
              label="价格来源"
              clearable
              w={130}
              data={[
                { value: 'list', label: '标价' },
                { value: 'test_price', label: '测试价' },
                { value: 'test_purchase', label: '模拟购买' },
              ]}
              value={draft.source || null}
              onChange={(v) => set('source', v)}
            />
            <Select
              label="付费周期"
              clearable
              w={120}
              data={[
                { value: 'monthly', label: '月付' },
                { value: 'yearly', label: '年付' },
              ]}
              value={draft.billing_cycle || null}
              onChange={(v) => set('billing_cycle', v)}
            />
            <TextInput
              label="用户邮箱"
              value={draft.email}
              onChange={(e) => set('email', e.currentTarget.value)}
              w={200}
            />
            <TextInput
              label="网关订单号"
              value={draft.trade_no}
              onChange={(e) => set('trade_no', e.currentTarget.value)}
              w={190}
            />
            <TextInput
              label="订单 ID"
              value={draft.order_id}
              onChange={(e) => set('order_id', e.currentTarget.value)}
              w={190}
            />
            <TextInput
              label="创建自"
              type="date"
              value={draft.created_from}
              onChange={(e) => set('created_from', e.currentTarget.value)}
            />
            <TextInput
              label="创建至"
              type="date"
              value={draft.created_to}
              onChange={(e) => set('created_to', e.currentTarget.value)}
            />
            <TextInput
              label="支付自"
              type="date"
              value={draft.paid_from}
              onChange={(e) => set('paid_from', e.currentTarget.value)}
            />
            <TextInput
              label="支付至"
              type="date"
              value={draft.paid_to}
              onChange={(e) => set('paid_to', e.currentTarget.value)}
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
              <Table verticalSpacing="sm" horizontalSpacing="md" miw={1300} aria-label="订单列表">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>订单 / 网关订单号</Table.Th>
                    <Table.Th>用户</Table.Th>
                    <Table.Th>周期</Table.Th>
                    <Table.Th>标价</Table.Th>
                    <Table.Th>优惠</Table.Th>
                    <Table.Th>应付</Table.Th>
                    <Table.Th>实付</Table.Th>
                    <Table.Th>支付方式</Table.Th>
                    <Table.Th>状态</Table.Th>
                    <Table.Th>创建时间</Table.Th>
                    <Table.Th>支付时间</Table.Th>
                    <Table.Th />
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {(list.data?.orders ?? []).map((o) => (
                    <OrderRow
                      key={o.id}
                      order={o}
                      onOpen={() => apply({ order: o.id, page: values.page })}
                    />
                  ))}
                </Table.Tbody>
              </Table>
              {list.data && list.data.orders.length === 0 ? (
                <Text ta="center" c="dimmed" py="xl">
                  没有符合条件的订单
                </Text>
              ) : null}
            </ScrollArea>
            <Pager
              page={list.page}
              total={list.data?.total ?? 0}
              size={size}
              onChange={(p) => apply({ page: p === 1 ? '' : String(p), order: '' })}
            />
          </>
        )}
      </Paper>
      <OrderDrawer
        id={values.order}
        onClose={() => apply({ order: '', page: values.page })}
        onChanged={list.reload}
      />
    </Stack>
  );
}

function StatusBadge({ status }: { status: string }) {
  const label = orderStatusLabel[status];
  return (
    <Badge color={label?.color ?? 'gray'} variant="light">
      {label?.text ?? status}
    </Badge>
  );
}

const cycleLabel = (o: ConsoleOrder) => (o.billing_cycle === 'yearly' ? '年付' : '月付');

function OrderRow({ order: o, onOpen }: { order: ConsoleOrder; onOpen: () => void }) {
  return (
    <Table.Tr data-testid="order-row">
      <Table.Td>
        <Text size="sm" fw={600}>
          {o.plan_name}
        </Text>
        <Text size="xs" c="dimmed" style={{ fontFamily: 'monospace' }}>
          {o.trade_no ?? o.id.slice(0, 8)}
        </Text>
      </Table.Td>
      <Table.Td>{o.email}</Table.Td>
      <Table.Td data-testid="order-cycle">{cycleLabel(o)}</Table.Td>
      <Table.Td>{money(o.list_price_cents || o.price_cents)}</Table.Td>
      <Table.Td>
        {o.discount_cents ? `-${money(o.discount_cents)}` : '—'}
        {o.coupon_code ? (
          <Text size="xs" c="dimmed">
            {o.coupon_code}
          </Text>
        ) : null}
      </Table.Td>
      <Table.Td>
        {money(o.amount_cents || o.price_cents)}
        {o.price_source === 'test_price' ? (
          <Badge ml={6} color="yellow" variant="light">
            测试价
          </Badge>
        ) : null}
      </Table.Td>
      <Table.Td>{o.paid_cents != null ? money(o.paid_cents) : '—'}</Table.Td>
      <Table.Td>{payTypeLabel[o.pay_type] ?? (o.provider === 'test' ? '模拟' : '—')}</Table.Td>
      <Table.Td>
        <StatusBadge status={o.status} />
        {o.status === 'review' ? (
          <Text size="xs" c="orange">
            {reviewReasonLabel[o.review_reason] ?? o.review_reason}
          </Text>
        ) : null}
      </Table.Td>
      <Table.Td>
        <When value={o.created_at} />
      </Table.Td>
      <Table.Td>
        <When value={o.paid_at} />
      </Table.Td>
      <Table.Td>
        <Button
          size="xs"
          variant="light"
          onClick={onOpen}
          aria-label={`查看订单 ${o.trade_no ?? o.id}`}
        >
          详情
        </Button>
      </Table.Td>
    </Table.Tr>
  );
}

type Detail = {
  order: ConsoleOrder;
  notifications: ConsoleNotification[];
  gateway_checked_at: string | null;
};

function OrderDrawer({
  id,
  onClose,
  onChanged,
}: {
  id: string;
  onClose: () => void;
  onChanged: () => void;
}) {
  const [nonce, setNonce] = useState(0);
  const [checking, setChecking] = useState(false);
  const [checked, setChecked] = useState<{ id: string; text: string } | null>(null);
  const [modal, setModal] = useState<'confirm' | 'close' | null>(null);
  const { data: detail, error } = useFetched<Detail>(
    id ? `/admin/console/orders/${encodeURIComponent(id)}` : null,
    nonce,
  );
  const gateway = checked?.id === id ? checked.text : null;
  const refresh = () => {
    setNonce((n) => n + 1);
    onChanged();
  };
  const check = async () => {
    setChecking(true);
    setChecked(null);
    try {
      const r = await api<{ gateway_status: string; expired: boolean }>(
        `/admin/console/orders/${id}/check`,
        { method: 'POST', body: '{}' },
      );
      const text: Record<string, string> = {
        paid: '网关显示：已支付',
        pending: '网关显示：待支付',
        timeout: '网关显示：已超时',
        not_found: '网关显示：订单不存在',
      };
      setChecked({
        id,
        text:
          (text[r.gateway_status] ?? `网关状态：${r.gateway_status}`) +
          (r.expired ? '（已过期）' : ''),
      });
    } catch (e) {
      setChecked({ id, text: describeError(e) });
    } finally {
      setChecking(false);
    }
  };
  const o = detail?.order.id === id ? detail.order : null;
  return (
    <Drawer opened={!!id} onClose={onClose} position="right" size="xl" title="订单详情">
      <Stack gap="lg" data-testid="order-detail">
        <ErrorBox text={error} />
        {!o && !error ? <Skeleton height={200} /> : null}
        {o && detail ? (
          <>
            <Paper withBorder radius="md" p="md">
              <Stack gap={4}>
                <Row label="套餐" value={o.plan_name} />
                <Row label="付费周期" value={cycleLabel(o)} />
                <Row label="用户" value={o.email} />
                <Row label="订单 ID" value={o.id} mono />
                <Row label="网关订单号" value={o.trade_no ?? '—'} mono />
                <Row label="状态" value={<StatusBadge status={o.status} />} />
                {o.review_reason ? (
                  <Row
                    label="核对原因"
                    value={reviewReasonLabel[o.review_reason] ?? o.review_reason}
                  />
                ) : null}
                <Row
                  label="价格来源"
                  value={priceSourceLabel[o.price_source] ?? (o.price_source || '—')}
                />
                <Row
                  label="金额"
                  value={`标价 ${money(o.list_price_cents || o.price_cents)}，优惠 ${money(o.discount_cents)}${o.coupon_code ? `（${o.coupon_code}）` : ''}，应付 ${money(o.amount_cents || o.price_cents)}，实付 ${o.paid_cents != null ? money(o.paid_cents) : '—'}`}
                />
                {o.original_cents > 0 ? (
                  <Row
                    label="原价与年付优惠"
                    value={
                      o.billing_cycle === 'yearly'
                        ? `原价 ${money(o.original_cents)}，年付折扣 ${o.cycle_discount_percent}%（立省 ${money(o.original_cents - (o.list_price_cents || o.price_cents))}）`
                        : `原价 ${money(o.original_cents)}`
                    }
                  />
                ) : null}
                <Row
                  label="权益快照"
                  value={
                    o.ent_max_devices != null || o.ent_allow_dedicated != null
                      ? `设备额度 ${o.ent_max_devices ?? '—'}，${o.ent_allow_dedicated ? '含专线' : '仅普通线路'}`
                      : '无（旧订单，开通时读取套餐当前权益）'
                  }
                />
                <Row label="支付方式" value={payTypeLabel[o.pay_type] ?? '—'} />
                <Row label="创建" value={<When value={o.created_at} />} />
                <Row label="支付" value={<When value={o.paid_at} />} />
                <Row label="上次查询网关" value={<When value={detail.gateway_checked_at} />} />
              </Stack>
            </Paper>
            <Group gap="sm">
              {o.trade_no ? (
                <Button variant="light" onClick={() => void check()} loading={checking}>
                  查询网关状态
                </Button>
              ) : null}
              {o.status === 'review' ? (
                <>
                  <Button onClick={() => setModal('confirm')}>人工确认到账</Button>
                  <Button color="gray" variant="light" onClick={() => setModal('close')}>
                    标记关闭
                  </Button>
                </>
              ) : null}
            </Group>
            {gateway ? (
              <Alert color="blue" data-testid="gateway-status">
                {gateway}（仅供核对，不会改变订单）
              </Alert>
            ) : null}
            <Stack gap={6}>
              <Text fw={600}>网关回调流水</Text>
              {detail.notifications.length === 0 ? (
                <Text size="sm" c="dimmed">
                  没有收到过回调
                </Text>
              ) : (
                <Table verticalSpacing={6} aria-label="回调流水">
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>时间</Table.Th>
                      <Table.Th>来源 IP</Table.Th>
                      <Table.Th>状态 / 金额</Table.Th>
                      <Table.Th>处理结果</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {detail.notifications.map((n) => (
                      <Table.Tr key={n.id}>
                        <Table.Td>
                          <When value={n.received_at} />
                        </Table.Td>
                        <Table.Td data-testid="notify-ip">{n.source_ip}</Table.Td>
                        <Table.Td>
                          {n.trade_status} · {n.money_text}
                        </Table.Td>
                        <Table.Td>{notifyResultLabel[n.result] ?? n.result}</Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              )}
              <Text size="xs" c="dimmed">
                网关 IP 白名单（PAYMENT_NOTIFY_ALLOWED_IPS）可根据首笔真实回调的来源 IP 填写。
              </Text>
            </Stack>
            {modal === 'confirm' ? (
              <ConfirmModal order={o} onClose={() => setModal(null)} onDone={refresh} />
            ) : null}
            {modal === 'close' ? (
              <CloseModal order={o} onClose={() => setModal(null)} onDone={refresh} />
            ) : null}
          </>
        ) : null}
      </Stack>
    </Drawer>
  );
}

function Row({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <Group gap="lg" wrap="nowrap" align="baseline">
      <Text size="sm" c="dimmed" w={90} style={{ flexShrink: 0 }}>
        {label}
      </Text>
      <Text
        size="sm"
        component="div"
        style={{ wordBreak: 'break-all', fontFamily: mono ? 'monospace' : undefined }}
      >
        {value}
      </Text>
    </Group>
  );
}

type ModalProps = { order: ConsoleOrder; onClose: () => void; onDone: () => void };

function ConfirmModal({ order, onClose, onDone }: ModalProps) {
  const [adminPassword, setAdminPassword] = useState('');
  const [yuan, setYuan] = useState<number | string>('');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const cents = Math.round(Number(yuan) * 100);
  const valid = adminPassword !== '' && note.trim() !== '' && yuan !== '' && cents >= 1;
  const submit = async () => {
    setBusy(true);
    setError('');
    try {
      await api(`/admin/console/orders/${order.id}/confirm`, {
        method: 'POST',
        body: JSON.stringify({ admin_password: adminPassword, paid_cents: cents, note }),
      });
      notifications.show({ color: 'teal', title: '已确认到账', message: '订阅已开通。' });
      onDone();
      onClose();
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal opened onClose={onClose} title="人工确认到账" closeOnClickOutside={false}>
      <Stack>
        <Alert color="orange">
          请先在支付网关商户后台核实这笔款项确已到账，再确认。确认后将按订单开通订阅，且不可撤销。应付金额{' '}
          {money(order.amount_cents)}。
        </Alert>
        <NumberInput
          label="商户后台核实的实付金额（元）"
          min={0.01}
          decimalScale={2}
          value={yuan}
          onChange={setYuan}
        />
        <Textarea
          label="核实备注"
          placeholder="例如：商户后台已查到 2026-10-23 14:05 微信到账 19.90 元"
          maxLength={200}
          value={note}
          onChange={(e) => setNote(e.currentTarget.value)}
        />
        <ReauthField value={adminPassword} onChange={setAdminPassword} />
        <ErrorBox text={error} />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            取消
          </Button>
          <Button onClick={() => void submit()} loading={busy} disabled={!valid}>
            确认到账并开通
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

function CloseModal({ order, onClose, onDone }: ModalProps) {
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const submit = async () => {
    setBusy(true);
    setError('');
    try {
      await api(`/admin/console/orders/${order.id}/close`, {
        method: 'POST',
        body: JSON.stringify({ note }),
      });
      notifications.show({ color: 'teal', title: '订单已关闭', message: '未开通任何权益。' });
      onDone();
      onClose();
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal opened onClose={onClose} title="标记关闭">
      <Stack>
        <Text size="sm">
          关闭后不会开通任何权益，此后针对该订单的回调也只记录、不处理。退款请在商户后台线下处理。
        </Text>
        <Textarea
          label="关闭原因"
          maxLength={200}
          value={note}
          onChange={(e) => setNote(e.currentTarget.value)}
        />
        <ErrorBox text={error} />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            取消
          </Button>
          <Button
            color="gray"
            onClick={() => void submit()}
            loading={busy}
            disabled={note.trim() === ''}
          >
            确认关闭
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
