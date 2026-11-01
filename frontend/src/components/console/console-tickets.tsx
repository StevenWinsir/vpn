'use client';
import Link from 'next/link';
import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Alert,
  Anchor,
  Badge,
  Button,
  Drawer,
  Group,
  Modal,
  Paper,
  ScrollArea,
  Select,
  Skeleton,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
} from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { IconHeadset, IconSend, IconUser } from '@tabler/icons-react';
import { api, APIError } from '@/lib/api';
import {
  utc,
  type ConsoleTicket,
  type ConsoleTicketDetail,
  type ConsoleTicketList,
} from '@/lib/console';
import { refreshTicketBadges } from '@/lib/ticket-badges';
import {
  bodyError,
  categoryLabel,
  categoryOptions,
  consoleStatus,
  deletionText,
  localMinute,
  platformLabel,
  textLength,
  ticketLimits,
  ticketRefresh,
  waitingText,
} from '@/lib/tickets';
import { money, orderStatusLabel } from '@/lib/types';
import { useMeta } from '@/lib/use-meta';
import { usePolling } from '@/lib/use-polling';
import {
  describeError,
  ErrorBox,
  PageSizeSelect,
  Pager,
  useConsoleList,
  When,
  type Values,
} from './console-common';

// The support queue (handoff_ticket.md 7, 11.3). "Real time" is polling (D7): the list every 15
// seconds and an open ticket every 10, paused while the page is hidden. The default view is the
// waiting queue, longest wait first.
const keys = ['status', 'category', 'keyword', 'page_size', 'page', 'ticket'] as const;
function toApi(v: Values): Values {
  return { status: v.status, category: v.category, keyword: v.keyword, page_size: v.page_size };
}
const statusData = [
  { value: 'open', label: '待处理' },
  { value: 'answered', label: '已回复' },
  { value: 'closed', label: '已关闭' },
  { value: 'all', label: '全部' },
];
// How long a ticket that just entered the first page stays highlighted.
const flashMs = 5000;

// Re-asks on the interval while the page is visible, and at once on focus and return.
function useVisibleRefresh(refresh: () => void, ms: number) {
  useEffect(() => {
    const visible = () => {
      if (document.visibilityState === 'visible') refresh();
    };
    const timer = window.setInterval(visible, ms);
    window.addEventListener('focus', visible);
    document.addEventListener('visibilitychange', visible);
    return () => {
      window.clearInterval(timer);
      window.removeEventListener('focus', visible);
      document.removeEventListener('visibilitychange', visible);
    };
  }, [refresh, ms]);
}

export function ConsoleTickets() {
  const { meta, loading } = useMeta();
  if (loading && !meta) return <Skeleton height={260} />;
  if (meta && meta.tickets_enabled === undefined)
    return (
      <Alert color="gray" title="当前版本暂不支持工单" data-testid="console-tickets-unsupported">
        API 没有声明 tickets_enabled，说明后端版本早于工单功能。请先升级后端。
      </Alert>
    );
  return <TicketQueue paused={meta?.tickets_enabled === false} />;
}

function TicketQueue({ paused }: { paused: boolean }) {
  const list = useConsoleList<ConsoleTicketList>('/admin/console/tickets', keys, toApi);
  const { values, apply, reload } = list;
  useVisibleRefresh(reload, ticketRefresh.consoleList);
  const [edits, setEdits] = useState<Values>({});
  const draft = { ...values, ...edits };
  const set = (key: string, value: string | null) =>
    setEdits((d) => ({ ...d, [key]: value ?? '' }));
  const go = (patch: Values) => {
    setEdits({});
    apply(patch);
  };
  const size = values.page_size === '50' ? 50 : 20;
  const status = values.status || 'open';
  const rows = list.data?.tickets ?? [];

  // Tickets that were not on the page at the previous refresh of the same view are tinted.
  const [flash, setFlash] = useState<string[]>([]);
  const known = useRef<{ view: string; ids: Set<string> } | null>(null);
  const view = `${status}|${values.category}|${values.keyword}|${values.page}|${values.page_size}`;
  useEffect(() => {
    if (!list.data) return;
    const ids = new Set(list.data.tickets.map((t) => t.id));
    const before = known.current;
    known.current = { view, ids };
    if (!before || before.view !== view) return;
    const fresh = [...ids].filter((id) => !before.ids.has(id));
    if (fresh.length === 0) return;
    setFlash((current) => [...current, ...fresh]);
    const timer = window.setTimeout(
      () => setFlash((current) => current.filter((id) => !fresh.includes(id))),
      flashMs,
    );
    return () => window.clearTimeout(timer);
  }, [list.data, view]);

  const changed = useCallback(() => {
    reload();
    refreshTicketBadges();
  }, [reload]);

  return (
    <Stack gap="lg">
      {paused ? (
        <Alert color="yellow" data-testid="console-tickets-paused">
          用户侧已暂停受理工单（TICKETS_ENABLED=false）：用户不能提交或回复，已有工单仍可在此回复和关闭。
        </Alert>
      ) : null}
      <Paper withBorder radius="lg" p="lg">
        <form
          aria-label="工单筛选"
          onSubmit={(e) => {
            e.preventDefault();
            go({ ...draft, ticket: '', page: '' });
          }}
        >
          <Group align="flex-end" gap="md" wrap="wrap">
            <Select
              label="状态"
              w={130}
              allowDeselect={false}
              data={statusData}
              value={draft.status || 'open'}
              onChange={(v) => set('status', v === 'open' ? '' : v)}
            />
            <Select
              label="分类"
              clearable
              w={180}
              data={categoryOptions.map((c) => ({ value: c.value, label: c.label }))}
              value={draft.category || null}
              onChange={(v) => set('category', v)}
            />
            <TextInput
              label="关键词"
              placeholder="工单号、邮箱或标题"
              maxLength={80}
              w={240}
              value={draft.keyword}
              onChange={(e) => set('keyword', e.currentTarget.value)}
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
              <Table
                verticalSpacing="sm"
                horizontalSpacing="md"
                miw={1240}
                aria-label="工单列表"
                className="ticket-queue"
              >
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>工单号</Table.Th>
                    <Table.Th>标题</Table.Th>
                    <Table.Th>分类</Table.Th>
                    <Table.Th>用户邮箱</Table.Th>
                    <Table.Th>状态</Table.Th>
                    <Table.Th>等待时长</Table.Th>
                    <Table.Th>消息数</Table.Th>
                    <Table.Th>最后更新</Table.Th>
                    <Table.Th>删除日期</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {rows.map((t) => (
                    <QueueRow
                      key={t.id}
                      ticket={t}
                      fresh={flash.includes(t.id)}
                      onOpen={() => apply({ ticket: t.id, page: values.page })}
                    />
                  ))}
                </Table.Tbody>
              </Table>
              {list.data && rows.length === 0 ? (
                <Text ta="center" c="dimmed" py="xl" data-testid="console-tickets-empty">
                  {status === 'open' ? '没有待处理的工单' : '没有符合条件的工单'}
                </Text>
              ) : null}
            </ScrollArea>
            <Pager
              page={list.page}
              total={list.data?.total ?? 0}
              size={size}
              onChange={(p) => apply({ page: p === 1 ? '' : String(p), ticket: '' })}
            />
          </>
        )}
      </Paper>
      <Drawer
        opened={!!values.ticket}
        onClose={() => apply({ ticket: '', page: values.page })}
        position="right"
        size="xl"
        title="工单详情"
      >
        {values.ticket ? (
          <TicketPanel key={values.ticket} id={values.ticket} onChanged={changed} />
        ) : null}
      </Drawer>
    </Stack>
  );
}

function StatusBadge({ status }: { status: ConsoleTicket['status'] }) {
  const label = consoleStatus(status);
  return (
    <Badge color={label.color} variant="light">
      {label.text}
    </Badge>
  );
}

function QueueRow({
  ticket: t,
  fresh,
  onOpen,
}: {
  ticket: ConsoleTicket;
  fresh: boolean;
  onOpen: () => void;
}) {
  return (
    <Table.Tr
      data-testid="console-ticket-row"
      data-fresh={fresh ? 'true' : undefined}
      className={fresh ? 'ticket-row-new' : undefined}
      style={{ cursor: 'pointer' }}
      onClick={onOpen}
    >
      <Table.Td>
        <Anchor
          component="button"
          type="button"
          size="sm"
          ff="monospace"
          onClick={(e) => {
            e.stopPropagation();
            onOpen();
          }}
          aria-label={`查看工单 ${t.ticket_no}`}
        >
          {t.ticket_no}
        </Anchor>
      </Table.Td>
      <Table.Td maw={280} className="ticket-queue-wrap">
        <Text size="sm" lineClamp={2} style={{ overflowWrap: 'anywhere' }}>
          {t.subject}
        </Text>
      </Table.Td>
      <Table.Td>{categoryLabel(t.category)}</Table.Td>
      <Table.Td>
        <Text size="sm">{t.user_email}</Text>
        {t.user_status === 'deleting' ? (
          <Badge color="red" variant="light" size="xs">
            账号注销中
          </Badge>
        ) : null}
      </Table.Td>
      <Table.Td>
        <StatusBadge status={t.status} />
      </Table.Td>
      <Table.Td>{t.status === 'open' ? waitingText(t.waiting_since) : '—'}</Table.Td>
      <Table.Td>{t.message_count}</Table.Td>
      <Table.Td>
        <When value={t.last_message_at} />
      </Table.Td>
      <Table.Td>
        <Text size="sm" title={utc(t.expires_at)}>
          {localMinute(t.expires_at)}
        </Text>
      </Table.Td>
    </Table.Tr>
  );
}

function TicketPanel({ id, onChanged }: { id: string; onChanged: () => void }) {
  const [detail, setDetail] = useState<ConsoleTicketDetail | null>(null);
  const [error, setError] = useState('');
  const [gone, setGone] = useState(false);
  // "用户有新消息": a refresh brought user messages this panel had not shown yet.
  const [news, setNews] = useState(false);
  const shown = useRef<number | null>(null);
  const take = useCallback((next: ConsoleTicketDetail, own: boolean) => {
    const before = shown.current;
    shown.current = next.messages.length;
    if (!own && before !== null && next.messages.slice(before).some((m) => m.author === 'user'))
      setNews(true);
    if (own) setNews(false);
    setDetail(next);
  }, []);
  const load = useCallback(
    async (signal: AbortSignal) => {
      try {
        const data = await api<{ ticket: ConsoleTicketDetail }>(
          `/admin/console/tickets/${encodeURIComponent(id)}`,
          { signal },
        );
        if (signal.aborted) return;
        take(data.ticket, false);
        setError('');
      } catch (e) {
        if (signal.aborted) return;
        if (e instanceof APIError && e.status === 404) setGone(true);
        else setError(describeError(e));
      }
    },
    [id, take],
  );
  const { refresh } = usePolling(load, ticketRefresh.consoleDetail);
  const [draft, setDraft] = useState('');
  const [replyError, setReplyError] = useState('');
  const [sending, setSending] = useState(false);
  const [closing, setClosing] = useState(false);
  const [notice, setNotice] = useState('');
  const attempt = useRef<{ signature: string; key: string } | null>(null);
  const bottom = useRef<HTMLDivElement>(null);

  if (gone)
    return (
      <Alert color="gray" data-testid="console-ticket-gone">
        工单不存在或已超过保留期限（到期后自动删除，不可恢复）。
      </Alert>
    );
  if (!detail) return error ? <ErrorBox text={error} /> : <Skeleton height={320} />;
  const t = detail;
  const deleting = t.user.status !== 'active';
  const closed = t.status === 'closed';
  const full = t.message_count >= 100;

  async function reply() {
    if (sending) return;
    const problem = bodyError(draft, ticketLimits.body, '回复');
    setReplyError(problem);
    if (problem) return;
    const text = draft.trim();
    if (attempt.current?.signature !== text)
      attempt.current = { signature: text, key: crypto.randomUUID() };
    setSending(true);
    try {
      const out = await api<{ ticket: ConsoleTicketDetail }>(
        `/admin/console/tickets/${t.id}/messages`,
        {
          method: 'POST',
          headers: { 'Idempotency-Key': attempt.current.key },
          body: JSON.stringify({ body: text }),
        },
      );
      take(out.ticket, true);
      attempt.current = null;
      setDraft('');
      setNotice('');
      onChanged();
      window.setTimeout(() => bottom.current?.scrollIntoView({ block: 'end' }), 0);
    } catch (e) {
      if (e instanceof APIError && e.status === 404) setGone(true);
      else setReplyError(describeError(e));
      if (e instanceof APIError && e.status === 409) refresh();
    } finally {
      setSending(false);
    }
  }

  return (
    <Stack gap="lg" data-testid="console-ticket-detail">
      <ErrorBox text={error} />
      <Paper withBorder radius="md" p="md">
        <Stack gap={4}>
          <Row label="工单号" value={t.ticket_no} mono />
          <Row label="标题" value={t.subject} />
          <Row
            label="状态"
            value={
              <Group gap={6}>
                <span data-testid="console-ticket-status">
                  <StatusBadge status={t.status} />
                </span>
                {closed ? (
                  <Text size="xs" c="dimmed">
                    {t.closed_by === 'staff'
                      ? `由 ${t.closed_by_admin_email || '管理员'} 关闭`
                      : '由用户关闭'}
                    {t.closed_at ? (
                      <>
                        {' · '}
                        <When value={t.closed_at} />
                      </>
                    ) : null}
                  </Text>
                ) : t.status === 'open' ? (
                  <Text size="xs" c="dimmed">
                    {waitingText(t.waiting_since)}
                  </Text>
                ) : null}
              </Group>
            }
          />
          <Row label="分类" value={categoryLabel(t.category)} />
          <Row label="设备" value={platformLabel(t.platform)} />
          <Row
            label="提交人"
            value={
              <Stack gap={2}>
                <Group gap={6}>
                  <Anchor
                    component={Link}
                    href={`/admin/console/users?user=${t.user.id}`}
                    size="sm"
                    data-testid="console-ticket-user"
                  >
                    {t.user.email}
                  </Anchor>
                  {deleting ? (
                    <Badge color="red" variant="light" data-testid="console-ticket-deleting">
                      账号注销中
                    </Badge>
                  ) : null}
                </Group>
                <Text size="xs" c="dimmed">
                  {t.user.plan_name ? (
                    <>
                      当前套餐 {t.user.plan_name}，到期 <When value={t.user.plan_expires_at} />
                    </>
                  ) : (
                    '没有有效套餐'
                  )}
                </Text>
              </Stack>
            }
          />
          <Row
            label="关联订单"
            value={
              t.order ? (
                <Anchor
                  component={Link}
                  href={`/admin/console/orders?order=${t.order.id}`}
                  size="sm"
                  data-testid="console-ticket-order"
                >
                  {t.order.trade_no ?? t.order.id.slice(0, 8)} · {t.order.plan_name} ·{' '}
                  {money(t.order.price_cents)} ·{' '}
                  {orderStatusLabel[t.order.status]?.text ?? t.order.status}
                </Anchor>
              ) : (
                '未关联'
              )
            }
          />
          <Row label="创建时间" value={<When value={t.created_at} />} />
          <Row
            label="删除日期"
            value={<span title={utc(t.expires_at)}>{deletionText(t.expires_at)}</span>}
          />
        </Stack>
      </Paper>
      <div className="ticket-thread" data-testid="console-ticket-thread">
        {t.messages.map((m) => (
          <div
            key={m.id}
            className={`ticket-bubble ${m.author === 'staff' ? 'mine' : 'theirs'}`}
            data-testid="console-ticket-message"
            data-author={m.author}
          >
            <Group gap={8} justify="space-between" wrap="nowrap">
              <Group gap={6} wrap="nowrap">
                {m.author === 'staff' ? <IconHeadset size={15} /> : <IconUser size={15} />}
                <Text size="xs" fw={700}>
                  {m.author === 'staff' ? `客服 · ${m.author_email || '已删除的管理员'}` : '用户'}
                </Text>
              </Group>
              <Text size="xs" c="dimmed">
                <When value={m.created_at} />
              </Text>
            </Group>
            <p className="ticket-text">{m.body}</p>
          </div>
        ))}
        <div ref={bottom} />
      </div>
      {news ? (
        <Alert
          color="blue"
          data-testid="console-ticket-news"
          withCloseButton
          onClose={() => setNews(false)}
        >
          用户有新消息
        </Alert>
      ) : null}
      {notice ? (
        <Alert color="orange" data-testid="console-ticket-notice">
          {notice}
        </Alert>
      ) : null}
      {closed ? (
        <Alert color="gray">工单已关闭，不能再回复。</Alert>
      ) : (
        <Paper withBorder radius="md" p="md">
          <Stack gap="sm">
            {deleting ? (
              <Text size="sm" c="red.7">
                该账号正在注销，不能回复，只能关闭工单。
              </Text>
            ) : full ? (
              <Text size="sm" c="dimmed">
                该工单的消息已达上限（100 条），只能关闭。
              </Text>
            ) : null}
            <Textarea
              label="回复"
              placeholder="回复内容会以“客服”的名义显示给用户"
              autosize
              minRows={4}
              maxRows={12}
              value={draft}
              disabled={deleting || full}
              onChange={(e) => {
                setDraft(e.currentTarget.value);
                setReplyError('');
              }}
              error={replyError || undefined}
              description={
                <Text span size="xs" c={textLength(draft) > ticketLimits.body ? 'red.7' : 'dimmed'}>
                  {textLength(draft)}/{ticketLimits.body}
                </Text>
              }
              inputWrapperOrder={['label', 'input', 'description', 'error']}
              data-testid="console-ticket-reply"
            />
            <Group justify="flex-end" gap="sm">
              <Button variant="light" color="gray" onClick={() => setClosing(true)}>
                关闭工单
              </Button>
              <Button
                leftSection={<IconSend size={16} />}
                loading={sending}
                disabled={deleting || full}
                onClick={() => void reply()}
              >
                发送回复
              </Button>
            </Group>
          </Stack>
        </Paper>
      )}
      {closing ? (
        <CloseModal
          ticket={t}
          onClose={() => setClosing(false)}
          onClosed={(next) => {
            take(next, true);
            setNotice('');
            onChanged();
          }}
          onStale={(text) => {
            setNotice(text);
            refresh();
          }}
          onGone={() => setGone(true)}
        />
      ) : null}
    </Stack>
  );
}

function Row({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <Group gap="lg" wrap="nowrap" align="baseline">
      <Text size="sm" c="dimmed" w={80} style={{ flexShrink: 0 }}>
        {label}
      </Text>
      <Text
        size="sm"
        component="div"
        style={{ overflowWrap: 'anywhere', fontFamily: mono ? 'monospace' : undefined }}
      >
        {value}
      </Text>
    </Group>
  );
}

// Closing carries the version this panel shows: a message the user sent meanwhile makes the
// server refuse (409 ticket_changed, D10), and the panel reloads to show it.
function CloseModal({
  ticket,
  onClose,
  onClosed,
  onStale,
  onGone,
}: {
  ticket: ConsoleTicketDetail;
  onClose: () => void;
  onClosed: (ticket: ConsoleTicketDetail) => void;
  onStale: (text: string) => void;
  onGone: () => void;
}) {
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const submit = async () => {
    const problem = note.trim() ? bodyError(note, ticketLimits.note, '关闭说明') : '';
    setError(problem);
    if (problem) return;
    setBusy(true);
    try {
      const out = await api<{ ticket: ConsoleTicketDetail }>(
        `/admin/console/tickets/${ticket.id}/close`,
        {
          method: 'POST',
          body: JSON.stringify({ version: ticket.version, note: note.trim() }),
        },
      );
      notifications.show({ color: 'teal', title: '工单已关闭', message: ticket.ticket_no });
      onClosed(out.ticket);
      onClose();
    } catch (e) {
      if (e instanceof APIError && (e.code === 'ticket_changed' || e.code === 'ticket_closed')) {
        onStale(
          e.code === 'ticket_changed'
            ? '用户有新回复，请先查看后再关闭。详情已刷新。'
            : '工单已被关闭。详情已刷新。',
        );
        onClose();
      } else if (e instanceof APIError && e.status === 404) {
        onGone();
        onClose();
      } else setError(describeError(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal opened onClose={onClose} title={`关闭工单 ${ticket.ticket_no}`}>
      <Stack>
        <Text size="sm">
          关闭后双方都不能再回复，也不能重新打开。可以不填说明直接关闭，用户会看到“客服已关闭此工单”。
        </Text>
        <Textarea
          label="关闭说明（选填）"
          description={`填写后作为一条客服消息发给用户，最多 ${ticketLimits.note} 字`}
          autosize
          minRows={3}
          value={note}
          onChange={(e) => {
            setNote(e.currentTarget.value);
            setError('');
          }}
          error={error || undefined}
          data-testid="console-ticket-note"
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button color="gray" onClick={() => void submit()} loading={busy}>
            确认关闭
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
