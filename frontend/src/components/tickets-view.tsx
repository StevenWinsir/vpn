'use client';
import Link from 'next/link';
import { useRouter, useSearchParams } from 'next/navigation';
import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Alert,
  Anchor,
  Badge,
  Button,
  Group,
  Modal,
  Pagination,
  Paper,
  Radio,
  ScrollArea,
  SegmentedControl,
  Select,
  SimpleGrid,
  Skeleton,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
  Title,
} from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import {
  IconArrowLeft,
  IconCheck,
  IconHeadset,
  IconMessageCircleQuestion,
  IconPlus,
  IconRefresh,
  IconSend,
} from '@tabler/icons-react';
import { useAuth } from './providers';
import { APIError, api, message } from '@/lib/api';
import { utc } from '@/lib/console';
import { refreshTicketBadges } from '@/lib/ticket-badges';
import {
  bodyError,
  categoryExtras,
  categoryLabel,
  categoryOptions,
  deletionText,
  expiringSoon,
  isTicketDetail,
  isTicketList,
  platformLabel,
  platformOptions,
  rateLimitText,
  subjectError,
  submitBlock,
  textLength,
  ticketLimits,
  ticketRefresh,
  userStatus,
} from '@/lib/tickets';
import {
  date,
  money,
  orderIdPattern,
  type Ticket,
  type TicketCategory,
  type TicketDetail,
  type TicketList,
  type TicketOrder,
  type TicketPlatform,
} from '@/lib/types';
import { useMeta } from '@/lib/use-meta';
import { usePolling } from '@/lib/use-polling';
import { toast } from '@/lib/toast';

// Support tickets for users (handoff_ticket.md 11.2). The other party is always "客服" (D6). The
// text people write is shown as plain text (pre-wrap), never as HTML or links.
const listRefreshMs = 60000;

function ticketError(e: unknown): string {
  if (e instanceof APIError && e.status === 429) return rateLimitText(e.retryAfter);
  return message(e);
}

function When({ value }: { value: string }) {
  return <span title={utc(value)}>{date(value)}</span>;
}
function Deletion({ value }: { value: string }) {
  return (
    <span title={utc(value)} data-testid="ticket-deletion">
      {deletionText(value)}
    </span>
  );
}
function StatusBadge({ status, closedBy }: { status: Ticket['status']; closedBy?: string }) {
  const label = userStatus(status, closedBy);
  return (
    <Badge color={label.color} variant="light" style={{ textTransform: 'none' }}>
      {label.text}
    </Badge>
  );
}
const orderLabel = (o: TicketOrder) =>
  `${o.trade_no ?? o.id.slice(0, 8)} · ${o.plan_name} · ${money(o.price_cents)}`;

export function TicketsView() {
  const { meta, loading } = useMeta();
  const params = useSearchParams();
  const id = params.get('id') ?? '';
  return (
    <div className="page-wrap">
      {loading && !meta ? (
        <Skeleton height={260} radius="lg" />
      ) : meta?.tickets_enabled === undefined ? (
        <>
          <Header />
          {/* A backend from before tickets answers /meta without the flag (handoff_ticket.md 14.1). */}
          <Alert
            color="gray"
            title="工单服务暂未开放"
            role="status"
            data-testid="tickets-unavailable"
          >
            工单服务暂时不可用，如有问题可以使用在线客服。
          </Alert>
        </>
      ) : orderIdPattern.test(id) ? (
        <TicketThread key={id} id={id} enabled={meta.tickets_enabled} />
      ) : (
        <>
          <Header />
          <TicketListView enabled={meta.tickets_enabled} />
        </>
      )}
    </div>
  );
}

function Header() {
  return (
    <div style={{ marginBottom: 30 }}>
      <Title order={1} size={29} className="page-title">
        我的工单
      </Title>
      <Text c="dimmed" size="sm" mt={8} maw={720}>
        提交问题后客服会尽快回复，工单自提交起保留 30 天，到期自动删除。紧急问题可以使用在线客服。
      </Text>
    </div>
  );
}

function TicketListView({ enabled }: { enabled: boolean }) {
  const { impersonation } = useAuth();
  const router = useRouter();
  const [view, setView] = useState<'active' | 'closed'>('active');
  const [page, setPage] = useState(1);
  // The answer is kept with the view it belongs to, so a tab switch never shows the other tab.
  const [answer, setAnswer] = useState<{ view: string; list: TicketList } | null>(null);
  const list = answer?.list ?? null;
  const [error, setError] = useState<string | null>(null);
  const [composing, setComposing] = useState(false);
  const wide = useMediaQuery('(min-width: 48em)');
  const load = useCallback(
    async (signal: AbortSignal) => {
      try {
        const data = await api<unknown>(`/me/tickets?status=${view}&page=${page}`, { signal });
        if (signal.aborted) return;
        if (!isTicketList(data)) throw new Error('工单暂时无法加载，请稍后重试。');
        setAnswer({ view, list: data });
        setError(null);
      } catch (e) {
        // A failed refresh keeps the list it already shows.
        if (!signal.aborted) setError(message(e));
      }
    },
    [view, page],
  );
  const { syncing, refresh } = usePolling(load, listRefreshMs);
  const shown = list && answer?.view === view && list.page === page ? list : null;
  const writable = enabled && !impersonation;
  const blocked = list ? submitBlock(list.open_count, list.max_open) : '';
  const compose = writable ? (
    <Button
      leftSection={<IconPlus size={16} />}
      disabled={!list || !!blocked}
      onClick={() => setComposing(true)}
    >
      提交工单
    </Button>
  ) : null;
  return (
    <Stack gap={20}>
      {!enabled ? (
        <Alert color="gray" title="工单暂停受理" role="status" data-testid="tickets-paused">
          暂时不能提交新工单或回复，已有工单仍可查看。如有问题可以使用在线客服。
        </Alert>
      ) : null}
      <Group justify="space-between" wrap="wrap" gap="sm">
        <SegmentedControl
          value={view}
          onChange={(value) => {
            setView(value === 'closed' ? 'closed' : 'active');
            setPage(1);
          }}
          data={[
            { value: 'active', label: '处理中' },
            { value: 'closed', label: '已关闭' },
          ]}
          aria-label="工单状态"
        />
        <Group gap="sm">
          <Button
            variant="default"
            leftSection={<IconRefresh size={15} />}
            loading={syncing}
            onClick={refresh}
          >
            刷新
          </Button>
          {compose}
        </Group>
      </Group>
      {blocked && writable ? (
        <Text size="sm" c="dimmed" data-testid="submit-blocked">
          {blocked}
        </Text>
      ) : null}
      {error ? (
        <Alert color="red" title="加载失败" role="alert">
          {error}
        </Alert>
      ) : null}
      {!shown ? (
        error ? null : (
          <Skeleton height={220} radius="lg" />
        )
      ) : shown.tickets.length === 0 ? (
        <Paper withBorder radius="lg" p="xl" data-testid="tickets-empty">
          <Stack align="center" gap="xs" py={20}>
            <IconMessageCircleQuestion size={36} stroke={1.3} color="var(--al-text-3)" />
            <Title order={3} size={16}>
              {view === 'active' ? '没有处理中的工单' : '没有已关闭的工单'}
            </Title>
            <Text size="sm" c="dimmed" ta="center">
              遇到连接、账号、付款或客户端的问题，可以提交工单，客服会在这里回复你。
            </Text>
            {view === 'active' ? <div style={{ marginTop: 8 }}>{compose}</div> : null}
          </Stack>
        </Paper>
      ) : wide !== false ? (
        <Paper withBorder radius="lg" p="md">
          <ScrollArea>
            <Table verticalSpacing="md" horizontalSpacing="md" miw={860} aria-label="工单列表">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>工单号</Table.Th>
                  <Table.Th>标题</Table.Th>
                  <Table.Th>分类</Table.Th>
                  <Table.Th>状态</Table.Th>
                  <Table.Th>最后更新</Table.Th>
                  <Table.Th>删除日期</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {shown.tickets.map((t) => (
                  <Table.Tr
                    key={t.id}
                    data-testid="ticket-row"
                    className={t.user_unread ? 'ticket-unread' : undefined}
                    style={{ cursor: 'pointer' }}
                    onClick={() => router.push(`/tickets?id=${t.id}`)}
                  >
                    <Table.Td>
                      {t.user_unread ? (
                        <span className="ticket-unread-dot" aria-label="有新回复" />
                      ) : null}
                      <Text span size="sm" ff="monospace" fw="inherit">
                        {t.ticket_no}
                      </Text>
                    </Table.Td>
                    <Table.Td maw={320}>
                      <Anchor
                        component={Link}
                        href={`/tickets?id=${t.id}`}
                        size="sm"
                        fw="inherit"
                        onClick={(e) => e.stopPropagation()}
                        style={{ overflowWrap: 'anywhere' }}
                      >
                        {t.subject}
                      </Anchor>
                    </Table.Td>
                    <Table.Td>{categoryLabel(t.category)}</Table.Td>
                    <Table.Td>
                      <StatusBadge status={t.status} />
                    </Table.Td>
                    <Table.Td>
                      <When value={t.last_message_at} />
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" c={expiringSoon(t.expires_at) ? 'orange.8' : undefined}>
                        <Deletion value={t.expires_at} />
                      </Text>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </ScrollArea>
        </Paper>
      ) : (
        <Stack gap="sm" data-testid="ticket-cards">
          {shown.tickets.map((t) => (
            <Paper
              key={t.id}
              withBorder
              radius="md"
              p="md"
              component={Link}
              href={`/tickets?id=${t.id}`}
              data-testid="ticket-row"
              className={t.user_unread ? 'ticket-unread' : undefined}
            >
              <Group justify="space-between" wrap="nowrap" align="flex-start" gap="sm">
                <Text size="sm" fw="inherit" style={{ overflowWrap: 'anywhere' }}>
                  {t.user_unread ? (
                    <span className="ticket-unread-dot" aria-label="有新回复" />
                  ) : null}
                  {t.subject}
                </Text>
                <StatusBadge status={t.status} />
              </Group>
              <Text size="xs" c="dimmed" mt={8} ff="monospace">
                {t.ticket_no} · {categoryLabel(t.category)}
              </Text>
              <Text size="xs" c="dimmed" mt={4}>
                最后更新 <When value={t.last_message_at} />
              </Text>
              <Text size="xs" c={expiringSoon(t.expires_at) ? 'orange.8' : 'dimmed'} mt={4}>
                <Deletion value={t.expires_at} />
              </Text>
            </Paper>
          ))}
        </Stack>
      )}
      {shown && shown.total > shown.page_size ? (
        <Group justify="center">
          <Pagination
            value={page}
            onChange={setPage}
            total={Math.ceil(shown.total / shown.page_size)}
          />
        </Group>
      ) : null}
      {composing && list ? (
        <TicketForm
          orders={list.order_options}
          onClose={() => setComposing(false)}
          onCreated={(created) => {
            setComposing(false);
            refreshTicketBadges();
            router.push(`/tickets?id=${created.id}`);
          }}
          onAlreadySent={() => {
            setComposing(false);
            refresh();
          }}
        />
      ) : null}
    </Stack>
  );
}

type FormErrors = Partial<Record<'category' | 'subject' | 'body' | 'platform' | 'order', string>>;

function Counter({ value, max }: { value: string; max: number }) {
  const length = textLength(value);
  return (
    <Text span size="xs" c={length > max ? 'red.7' : 'dimmed'} data-testid="ticket-counter">
      {length}/{max}
    </Text>
  );
}

function TicketForm({
  orders,
  onClose,
  onCreated,
  onAlreadySent,
}: {
  orders: TicketOrder[];
  onClose: () => void;
  onCreated: (ticket: TicketDetail) => void;
  onAlreadySent: () => void;
}) {
  const [category, setCategory] = useState<TicketCategory | null>(null);
  const [subject, setSubject] = useState('');
  const [body, setBody] = useState('');
  const [platform, setPlatform] = useState<TicketPlatform | null>(null);
  const [orderId, setOrderId] = useState<string | null>(null);
  const [opened, setOpened] = useState({ platform: false, order: false });
  const [errors, setErrors] = useState<FormErrors>({});
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // One Idempotency-Key per distinct submission while the form is open: a retry or a double
  // click replays the same ticket (handoff_ticket.md 6.1).
  const attempt = useRef<{ signature: string; key: string } | null>(null);
  const extras = categoryExtras(category);
  const showPlatform = extras.platform || opened.platform || platform !== null;
  const showOrder = orders.length > 0 && (extras.order || opened.order || orderId !== null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (busy) return;
    const found: FormErrors = {};
    if (!category) found.category = '请选择问题分类';
    const s = subjectError(subject);
    if (s) found.subject = s;
    const b = bodyError(body, ticketLimits.body, '问题描述');
    if (b) found.body = b;
    setErrors(found);
    setFailure(null);
    if (Object.keys(found).length > 0) return;
    const payload = {
      category,
      subject: subject.trim(),
      body: body.trim(),
      ...(platform ? { platform } : {}),
      ...(orderId ? { order_id: orderId } : {}),
    };
    const signature = JSON.stringify(payload);
    if (attempt.current?.signature !== signature)
      attempt.current = { signature, key: crypto.randomUUID() };
    setBusy(true);
    try {
      const created = await api<{ ticket: unknown }>('/me/tickets', {
        method: 'POST',
        headers: { 'Idempotency-Key': attempt.current.key },
        body: signature,
      });
      if (!isTicketDetail(created.ticket)) throw new Error('提交结果无法识别，请刷新列表查看。');
      toast.success('工单已提交', `工单号 ${created.ticket.ticket_no}，客服会尽快回复`, {
        id: 'ticket-created',
      });
      onCreated(created.ticket);
    } catch (err) {
      setBusy(false);
      if (err instanceof APIError && err.code === 'invalid_ticket') {
        const field = String(err.details.field ?? '');
        if (field === 'category' || field === 'subject' || field === 'body' || field === 'platform')
          setErrors({ [field]: err.message });
        else setFailure(err.message);
      } else if (err instanceof APIError && err.code === 'invalid_order') {
        setErrors({ order: err.message });
      } else if (err instanceof APIError && err.code === 'idempotency_conflict') {
        // This form already submitted a ticket whose answer was lost.
        toast.success('工单已提交', '请在列表中查看', { id: 'ticket-created' });
        onAlreadySent();
      } else setFailure(ticketError(err));
    }
  }

  return (
    <Modal opened onClose={onClose} title="提交工单" size="lg" closeOnClickOutside={false}>
      <form onSubmit={(e) => void submit(e)} noValidate aria-label="提交工单">
        <Stack gap="md">
          <Radio.Group
            label="问题分类"
            withAsterisk
            value={category}
            onChange={(value) => {
              setCategory(value as TicketCategory);
              setErrors((current) => ({ ...current, category: undefined }));
            }}
            error={errors.category}
          >
            <SimpleGrid cols={{ base: 1, xs: 2 }} spacing="xs" mt={6}>
              {categoryOptions.map((option) => (
                <Radio.Card key={option.value} value={option.value} radius="md" p="sm">
                  <Group wrap="nowrap" align="flex-start" gap="sm">
                    <Radio.Indicator mt={2} />
                    <div>
                      <Text size="sm" fw={600}>
                        {option.label}
                      </Text>
                      <Text size="xs" c="dimmed">
                        {option.hint}
                      </Text>
                    </div>
                  </Group>
                </Radio.Card>
              ))}
            </SimpleGrid>
          </Radio.Group>
          <TextInput
            label="标题"
            withAsterisk
            placeholder="一句话概括遇到的问题"
            value={subject}
            onChange={(e) => {
              setSubject(e.currentTarget.value);
              setErrors((current) => ({ ...current, subject: undefined }));
            }}
            error={errors.subject}
            rightSection={<Counter value={subject} max={ticketLimits.subject} />}
            rightSectionWidth={56}
          />
          <Textarea
            label="问题描述"
            withAsterisk
            placeholder="请描述遇到的问题、出现的时间和操作步骤。连接类问题可以在客户端“账号”页点击“复制诊断信息”，粘贴到这里。"
            autosize
            minRows={6}
            maxRows={14}
            value={body}
            onChange={(e) => {
              setBody(e.currentTarget.value);
              setErrors((current) => ({ ...current, body: undefined }));
            }}
            error={errors.body}
            description={<Counter value={body} max={ticketLimits.body} />}
            inputWrapperOrder={['label', 'input', 'description', 'error']}
          />
          {showPlatform ? (
            <Select
              label="使用的设备（选填）"
              clearable
              data={platformOptions}
              value={platform}
              onChange={(value) => setPlatform(value as TicketPlatform | null)}
              error={errors.platform}
              comboboxProps={{ withinPortal: true }}
            />
          ) : null}
          {showOrder ? (
            <Select
              label="关联订单（选填）"
              description="只能选择你自己最近的订单"
              clearable
              data={orders.map((o) => ({ value: o.id, label: orderLabel(o) }))}
              value={orderId}
              onChange={(value) => {
                setOrderId(value);
                setErrors((current) => ({ ...current, order: undefined }));
              }}
              error={errors.order}
              comboboxProps={{ withinPortal: true }}
            />
          ) : null}
          {!showPlatform || (orders.length > 0 && !showOrder) ? (
            <Group gap="md">
              {!showPlatform ? (
                <Anchor
                  component="button"
                  type="button"
                  size="sm"
                  onClick={() => setOpened((o) => ({ ...o, platform: true }))}
                >
                  + 填写使用的设备
                </Anchor>
              ) : null}
              {orders.length > 0 && !showOrder ? (
                <Anchor
                  component="button"
                  type="button"
                  size="sm"
                  onClick={() => setOpened((o) => ({ ...o, order: true }))}
                >
                  + 关联订单
                </Anchor>
              ) : null}
            </Group>
          ) : null}
          {failure ? (
            <Alert color="red" role="alert" data-testid="ticket-form-error">
              {failure}
            </Alert>
          ) : null}
          <Text size="xs" c="dimmed">
            工单自提交起保留 30 天，到期后连同对话一起自动删除。请勿在工单中填写密码。
          </Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose} disabled={busy}>
              取消
            </Button>
            <Button type="submit" loading={busy} leftSection={<IconSend size={16} />}>
              提交
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
}

function TicketThread({ id, enabled }: { id: string; enabled: boolean }) {
  const { impersonation } = useAuth();
  const [detail, setDetail] = useState<TicketDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [gone, setGone] = useState<'deleted' | 'missing' | null>(null);
  const seen = useRef(false);
  const vanished = useCallback(() => {
    setGone(seen.current ? 'deleted' : 'missing');
    setDetail(null);
    refreshTicketBadges();
  }, []);
  const load = useCallback(
    async (signal: AbortSignal) => {
      try {
        const data = await api<{ ticket: unknown }>(`/me/tickets/${id}`, { signal });
        if (signal.aborted) return;
        if (!isTicketDetail(data.ticket)) throw new Error('工单暂时无法加载，请稍后重试。');
        seen.current = true;
        setDetail(data.ticket);
        setError(null);
      } catch (e) {
        if (signal.aborted) return;
        if (e instanceof APIError && e.status === 404) vanished();
        else setError(message(e));
      }
    },
    [id, vanished],
  );
  const { syncing, refresh } = usePolling(load, ticketRefresh.userDetail);
  const readOnly = !!impersonation;
  // Opening a ticket with an unread reply marks it read once (D8); never while an administrator
  // looks at the account, whose session may not change anything.
  const marked = useRef('');
  useEffect(() => {
    if (!detail?.user_unread || readOnly) return;
    const marker = `${detail.id}:${detail.message_count}:${detail.status}`;
    if (marked.current === marker) return;
    marked.current = marker;
    api(`/me/tickets/${detail.id}/read`, { method: 'POST', body: '{}' })
      .then(() => {
        setDetail((d) => (d && d.id === detail.id ? { ...d, user_unread: false } : d));
        refreshTicketBadges();
      })
      .catch(() => undefined);
  }, [detail, readOnly]);

  const back = (
    <Anchor component={Link} href="/tickets" size="sm" data-testid="back-to-list">
      <Group gap={4} component="span">
        <IconArrowLeft size={15} /> 返回工单列表
      </Group>
    </Anchor>
  );
  if (gone)
    return (
      <Stack gap="md">
        {back}
        <Alert color="gray" title="工单已删除" role="status" data-testid="ticket-gone">
          {gone === 'deleted' ? '该工单已超过保留期限并被删除。' : '工单不存在或已超过保留期限。'}
        </Alert>
      </Stack>
    );
  if (!detail)
    return (
      <Stack gap="md">
        {back}
        {error ? (
          <Alert color="red" title="加载失败" role="alert">
            <Stack gap="xs" align="flex-start">
              {error}
              <Button variant="light" size="xs" onClick={refresh} loading={syncing}>
                重试
              </Button>
            </Stack>
          </Alert>
        ) : (
          <Skeleton height={320} radius="lg" />
        )}
      </Stack>
    );
  const writable = enabled && !readOnly;
  const closed = detail.status === 'closed';
  return (
    <Stack gap={20} data-testid="ticket-detail">
      {back}
      <div>
        <Text size="sm" c="dimmed" ff="monospace" data-testid="ticket-no">
          工单号 {detail.ticket_no}
        </Text>
        <Title
          order={1}
          size={25}
          className="page-title"
          mt={6}
          style={{ overflowWrap: 'anywhere' }}
        >
          {detail.subject}
        </Title>
        <Group gap="xs" mt={12} wrap="wrap">
          <span data-testid="ticket-status">
            <StatusBadge status={detail.status} closedBy={detail.closed_by} />
          </span>
          <Badge variant="outline" color="gray" style={{ textTransform: 'none' }}>
            {categoryLabel(detail.category)}
          </Badge>
          {syncing ? (
            <Text size="xs" c="dimmed">
              正在同步…
            </Text>
          ) : null}
        </Group>
      </div>
      {error ? (
        <Alert color="red" role="alert">
          {error}
        </Alert>
      ) : null}
      {expiringSoon(detail.expires_at) ? (
        <Alert color="yellow" role="status" data-testid="ticket-expiring">
          即将删除，如需保留请自行复制重要内容。
        </Alert>
      ) : null}
      <Paper withBorder radius="lg" p={{ base: 16, sm: 20 }}>
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="xs" verticalSpacing={6}>
          <Info label="提交时间" value={<When value={detail.created_at} />} />
          <Info label="删除日期" value={<Deletion value={detail.expires_at} />} />
          <Info label="使用的设备" value={platformLabel(detail.platform)} />
          <Info
            label="关联订单"
            value={detail.order ? orderLabel(detail.order) : '未关联'}
            testId="ticket-order"
          />
        </SimpleGrid>
      </Paper>
      <div className="ticket-thread" data-testid="ticket-thread">
        {detail.messages.map((m) => (
          <div
            key={m.id}
            className={`ticket-bubble ${m.author === 'user' ? 'mine' : 'theirs'}`}
            data-testid="ticket-message"
            data-author={m.author === 'user' ? 'user' : 'staff'}
          >
            <Group gap={8} justify="space-between" wrap="nowrap">
              <Group gap={6} wrap="nowrap">
                {m.author === 'user' ? null : <IconHeadset size={15} />}
                <Text size="xs" fw={700}>
                  {m.author === 'user' ? '我' : '客服'}
                </Text>
              </Group>
              <Text size="xs" c="dimmed">
                <When value={m.created_at} />
              </Text>
            </Group>
            <p className="ticket-text">{m.body}</p>
          </div>
        ))}
      </div>
      {closed ? (
        <Alert color="gray" title="工单已关闭" role="status" data-testid="ticket-closed">
          {detail.closed_by === 'staff' ? '客服已关闭此工单。' : '你已关闭此工单。'}
          如有新的问题，请重新提交工单。
        </Alert>
      ) : writable ? (
        <ReplyBox ticket={detail} onChanged={setDetail} onGone={vanished} />
      ) : !enabled && !readOnly ? (
        <Alert color="gray" role="status">
          工单暂停受理，暂时不能回复。如有问题可以使用在线客服。
        </Alert>
      ) : null}
    </Stack>
  );
}

function Info({
  label,
  value,
  testId,
}: {
  label: string;
  value: React.ReactNode;
  testId?: string;
}) {
  return (
    <Group gap="md" wrap="nowrap" align="baseline">
      <Text size="sm" c="dimmed" w={80} style={{ flexShrink: 0 }}>
        {label}
      </Text>
      <Text size="sm" component="div" style={{ overflowWrap: 'anywhere' }} data-testid={testId}>
        {value}
      </Text>
    </Group>
  );
}

function ReplyBox({
  ticket,
  onChanged,
  onGone,
}: {
  ticket: TicketDetail;
  onChanged: (ticket: TicketDetail) => void;
  onGone: () => void;
}) {
  const [draft, setDraft] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [closing, setClosing] = useState(false);
  const attempt = useRef<{ signature: string; key: string } | null>(null);
  const full = ticket.message_count >= 100;

  async function send() {
    if (busy) return;
    const problem = bodyError(draft, ticketLimits.body, '回复');
    setError(problem);
    if (problem) return;
    const text = draft.trim();
    if (attempt.current?.signature !== text)
      attempt.current = { signature: text, key: crypto.randomUUID() };
    setBusy(true);
    try {
      const out = await api<{ ticket: unknown }>(`/me/tickets/${ticket.id}/messages`, {
        method: 'POST',
        headers: { 'Idempotency-Key': attempt.current.key },
        body: JSON.stringify({ body: text }),
      });
      if (isTicketDetail(out.ticket)) onChanged(out.ticket);
      attempt.current = null;
      setDraft('');
      toast.success('回复已发送', undefined, { id: 'ticket-reply' });
    } catch (e) {
      if (e instanceof APIError && e.status === 404) onGone();
      else setError(ticketError(e));
    } finally {
      setBusy(false);
    }
  }

  async function close() {
    setClosing(true);
    try {
      const out = await api<{ ticket: unknown }>(`/me/tickets/${ticket.id}/close`, {
        method: 'POST',
        body: '{}',
      });
      if (isTicketDetail(out.ticket)) onChanged(out.ticket);
      setConfirming(false);
      refreshTicketBadges();
      toast.success('工单已关闭', '感谢你的反馈', { id: 'ticket-closed' });
    } catch (e) {
      setConfirming(false);
      if (e instanceof APIError && e.status === 404) onGone();
      else setError(ticketError(e));
    } finally {
      setClosing(false);
    }
  }

  return (
    <Paper withBorder radius="lg" p={{ base: 16, sm: 20 }}>
      <Stack gap="sm">
        {full ? (
          <Alert color="gray" role="status">
            该工单的消息已达上限，请关闭后重新提交。
          </Alert>
        ) : (
          <>
            <Textarea
              label="补充说明"
              placeholder="继续描述问题，或回复客服的提问"
              autosize
              minRows={3}
              maxRows={10}
              value={draft}
              onChange={(e) => {
                setDraft(e.currentTarget.value);
                setError('');
              }}
              error={error || undefined}
              description={<Counter value={draft} max={ticketLimits.body} />}
              inputWrapperOrder={['label', 'input', 'description', 'error']}
              data-testid="ticket-reply"
            />
          </>
        )}
        {full && error ? (
          <Text size="sm" c="red.7" role="alert">
            {error}
          </Text>
        ) : null}
        <Group justify="space-between" wrap="wrap" gap="sm">
          <Button
            variant="subtle"
            color="gray"
            leftSection={<IconCheck size={16} />}
            onClick={() => setConfirming(true)}
          >
            问题已解决，关闭工单
          </Button>
          {full ? null : (
            <Button leftSection={<IconSend size={16} />} loading={busy} onClick={() => void send()}>
              发送
            </Button>
          )}
        </Group>
      </Stack>
      <Modal opened={confirming} onClose={() => setConfirming(false)} title="关闭工单">
        <Stack>
          <Text size="sm">
            关闭后不能再回复，也不能重新打开。如有新的问题，可以重新提交工单。确定关闭吗？
          </Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setConfirming(false)} disabled={closing}>
              取消
            </Button>
            <Button onClick={() => void close()} loading={closing}>
              确认关闭
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Paper>
  );
}
