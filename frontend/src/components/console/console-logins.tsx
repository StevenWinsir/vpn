'use client';
import { useState } from 'react';
import {
  Badge,
  Button,
  Group,
  Paper,
  ScrollArea,
  SegmentedControl,
  Select,
  Skeleton,
  Stack,
  Table,
  Text,
  TextInput,
} from '@mantine/core';
import {
  dayEnd,
  dayStart,
  geoLabel,
  platformLabel,
  validDay,
  type ConsoleLogin,
} from '@/lib/console';
import {
  ErrorBox,
  PageSizeSelect,
  Pager,
  useConsoleList,
  When,
  type Values,
} from './console-common';

const keys = [
  'email',
  'ip',
  'channel',
  'result',
  'quick',
  'from',
  'to',
  'page_size',
  'page',
] as const;

function toApi(v: Values): Values {
  return {
    email: v.email,
    ip: v.ip,
    channel: v.channel,
    result: v.result,
    quick: v.quick,
    page_size: v.page_size,
    from: validDay(v.from) ? dayStart(v.from) : '',
    to: validDay(v.to) ? dayEnd(v.to) : '',
  };
}

type LoginList = { logins: ConsoleLogin[]; total: number };

// A short, readable form of a user agent; the log keeps at most 200 characters of it.
function agentSummary(ua: string) {
  if (!ua) return '—';
  const browser = /(Edg|OPR|Chrome|Firefox|Safari)\/[\d.]+/.exec(ua);
  const os = /(Windows|Mac OS X|Android|iPhone|iPad|Linux)/.exec(ua);
  const name = browser
    ? browser[0].replace('Edg/', 'Edge/').replace('OPR/', 'Opera/')
    : ua.slice(0, 24);
  return os ? `${name} · ${os[1]}` : name;
}

export function ConsoleLogins() {
  const list = useConsoleList<LoginList>('/admin/console/logins', keys, toApi);
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
    go({ ...draft, page: '' });
  };
  const size = values.page_size === '50' ? 50 : 20;
  return (
    <Stack gap="lg">
      <Paper withBorder radius="lg" p="lg">
        <Stack gap="md">
          <SegmentedControl
            aria-label="快捷筛选"
            value={values.quick || 'all'}
            onChange={(v) => go({ quick: v === 'all' ? '' : v })}
            data={[
              { value: 'all', label: '全部' },
              { value: 'shared_ip', label: '同一 IP 多账号（24 小时）' },
              { value: 'burst_failures', label: '短时间多次失败（1 小时内 ≥5 次）' },
            ]}
          />
          <form onSubmit={submit} aria-label="登录记录筛选">
            <Group align="flex-end" gap="md" wrap="wrap">
              <TextInput
                label="用户邮箱"
                value={draft.email}
                onChange={(e) => set('email', e.currentTarget.value)}
                w={210}
              />
              <TextInput
                label="IP"
                value={draft.ip}
                onChange={(e) => set('ip', e.currentTarget.value)}
                w={170}
              />
              <Select
                label="渠道"
                clearable
                w={120}
                data={[
                  { value: 'web', label: '网站' },
                  { value: 'native', label: '客户端' },
                ]}
                value={draft.channel || null}
                onChange={(v) => set('channel', v)}
              />
              <Select
                label="结果"
                clearable
                w={110}
                data={[
                  { value: 'success', label: '成功' },
                  { value: 'failure', label: '失败' },
                ]}
                value={draft.result || null}
                onChange={(v) => set('result', v)}
              />
              <TextInput
                label="自"
                type="date"
                value={draft.from}
                onChange={(e) => set('from', e.currentTarget.value)}
              />
              <TextInput
                label="至"
                type="date"
                value={draft.to}
                onChange={(e) => set('to', e.currentTarget.value)}
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
        </Stack>
      </Paper>
      <ErrorBox text={list.error} />
      <Paper withBorder radius="lg" p="md">
        {list.loading && !list.data ? (
          <Skeleton height={260} />
        ) : (
          <>
            <ScrollArea>
              <Table verticalSpacing="sm" horizontalSpacing="md" miw={1100} aria-label="登录记录">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>时间</Table.Th>
                    <Table.Th>用户</Table.Th>
                    <Table.Th>渠道 / 平台</Table.Th>
                    <Table.Th>IP</Table.Th>
                    <Table.Th>归属地</Table.Th>
                    <Table.Th>设备摘要</Table.Th>
                    <Table.Th>结果</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {(list.data?.logins ?? []).map((l) => (
                    <Table.Tr key={l.id} data-testid="login-row">
                      <Table.Td>
                        <When value={l.occurred_at} />
                      </Table.Td>
                      <Table.Td>{l.email}</Table.Td>
                      <Table.Td>
                        {l.channel === 'web'
                          ? '网站'
                          : `客户端 · ${platformLabel[l.platform] ?? (l.platform || '—')}`}
                      </Table.Td>
                      <Table.Td style={{ fontFamily: 'monospace' }}>{l.ip}</Table.Td>
                      <Table.Td>{geoLabel(l.geo_status, l.geo_text)}</Table.Td>
                      <Table.Td title={l.user_agent}>{agentSummary(l.user_agent)}</Table.Td>
                      <Table.Td>
                        <Badge color={l.result === 'success' ? 'teal' : 'red'} variant="light">
                          {l.result === 'success' ? '成功' : '失败'}
                        </Badge>
                        {l.failure_reason ? (
                          <Text size="xs" c="dimmed">
                            {l.failure_reason}
                          </Text>
                        ) : null}
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
              {list.data && list.data.logins.length === 0 ? (
                <Text ta="center" c="dimmed" py="xl">
                  没有符合条件的记录
                </Text>
              ) : null}
            </ScrollArea>
            <Pager
              page={list.page}
              total={list.data?.total ?? 0}
              size={size}
              onChange={(p) => apply({ page: p === 1 ? '' : String(p) })}
            />
          </>
        )}
      </Paper>
    </Stack>
  );
}
