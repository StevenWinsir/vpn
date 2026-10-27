'use client';
import Link from 'next/link';
import { useCallback, useRef, useState } from 'react';
import {
  Alert,
  Badge,
  Button,
  Group,
  Pagination,
  Paper,
  ScrollArea,
  SimpleGrid,
  Skeleton,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { IconCheck, IconCopy, IconGift, IconLink, IconRefresh } from '@tabler/icons-react';
import { api, message } from '@/lib/api';
import { formatBytes } from '@/lib/dashboard';
import {
  isReferralPage,
  isReferralSummary,
  referralBadge,
  referralReward,
  type ReferralBadge,
} from '@/lib/referral';
import { date, type ReferralItem, type ReferralPage, type ReferralSummary } from '@/lib/types';
import { useMeta } from '@/lib/use-meta';
import { usePolling } from '@/lib/use-polling';

// The list follows a reward from "待发放" to "已发放" on its own: refreshed while visible.
const refreshMs = 30000;

export function ReferralView() {
  const { meta, loading } = useMeta();
  return (
    <div className="page-wrap">
      <Group justify="space-between" align="flex-start" mb={30}>
        <div>
          <Title order={1} size={29} className="page-title">
            推荐有礼
          </Title>
          <Text c="dimmed" size="sm" mt={8}>
            邀请好友注册，好友首次付费成功后，你获得等额的流量奖励。
          </Text>
        </div>
      </Group>
      {loading && !meta ? (
        <Skeleton height={220} radius="lg" />
      ) : meta?.referral_enabled === undefined ? (
        // A backend from before the programme answers /meta without the flag.
        <Alert color="gray" title="功能暂不可用" role="status">
          推荐有礼暂时不可用，请稍后再来看看。
        </Alert>
      ) : (
        <ReferralBody />
      )}
    </div>
  );
}

function ReferralBody() {
  const [summary, setSummary] = useState<ReferralSummary | null>(null);
  const [list, setList] = useState<ReferralPage | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [page, setPage] = useState(1);
  const load = useCallback(
    async (signal: AbortSignal) => {
      const [head, rows] = await Promise.allSettled([
        api<unknown>('/me/referral', { signal }),
        api<unknown>(`/me/referrals?page=${page}`, { signal }),
      ]);
      if (signal.aborted) return;
      // Both parts keep what they last showed when a refresh fails.
      if (head.status === 'fulfilled' && isReferralSummary(head.value)) setSummary(head.value);
      if (rows.status === 'fulfilled' && isReferralPage(rows.value)) setList(rows.value);
      const failed =
        head.status === 'rejected'
          ? message(head.reason)
          : rows.status === 'rejected'
            ? message(rows.reason)
            : head.status === 'fulfilled' && !isReferralSummary(head.value)
              ? '推荐信息暂时不可用，请稍后重试。'
              : null;
      setError(failed);
    },
    [page],
  );
  const { syncing, refresh } = usePolling(load, refreshMs);
  const wide = useMediaQuery('(min-width: 48em)');

  return (
    <Stack gap={24}>
      {error ? (
        <Alert color="red" title="加载失败" role="alert">
          <Stack gap="xs" align="flex-start">
            {error}
            <Button variant="light" size="xs" onClick={refresh} loading={syncing}>
              重试
            </Button>
          </Stack>
        </Alert>
      ) : null}
      {!summary ? (
        error ? null : (
          <Stack>
            <Skeleton height={190} radius="lg" />
            <SimpleGrid cols={{ base: 2, md: 4 }}>
              <Skeleton height={100} />
              <Skeleton height={100} />
              <Skeleton height={100} />
              <Skeleton height={100} />
            </SimpleGrid>
          </Stack>
        )
      ) : (
        <>
          {!summary.enabled ? (
            <Alert color="gray" title="推荐功能暂时关闭" role="status">
              暂停期间新注册不再绑定推荐关系、奖励也不会发放；重新开启后会继续处理。
            </Alert>
          ) : null}
          {!summary.has_active_plan ? (
            <Alert color="yellow" title="你当前没有有效套餐" role="status" data-testid="no-plan">
              你当前没有有效套餐，好友首单奖励将不生效。
              <Link href="/plans"> 去订阅</Link>
            </Alert>
          ) : null}
          <CodeCard summary={summary} />
          <SimpleGrid cols={{ base: 2, md: 4 }} spacing={16} data-testid="referral-stats">
            <Stat label="邀请注册" value={`${summary.stats.registered_count}`} unit="人" />
            <Stat label="已消费" value={`${summary.stats.purchased_count}`} unit="人" />
            <Stat label="待发放" value={`${summary.stats.pending_count}`} unit="笔" />
            <Stat
              label="累计获得流量"
              value={formatBytes(summary.stats.rewarded_bytes)}
              detail={
                summary.current_bonus_bytes > 0
                  ? `当前套餐内剩余 ${formatBytes(summary.current_bonus_bytes)}`
                  : undefined
              }
            />
          </SimpleGrid>
          <Rules maxGib={summary.reward_rule.max_gib} />
          <Records
            list={list}
            wide={wide !== false}
            page={page}
            onPage={setPage}
            summary={summary}
          />
        </>
      )}
      <Group justify="flex-end">
        <Button
          variant="default"
          size="xs"
          leftSection={<IconRefresh size={15} />}
          loading={syncing}
          onClick={refresh}
        >
          刷新
        </Button>
      </Group>
    </Stack>
  );
}

type Copied = 'code' | 'link' | 'select' | null;

function CodeCard({ summary }: { summary: ReferralSummary }) {
  const [copied, setCopied] = useState<Copied>(null);
  const field = useRef<HTMLInputElement>(null);
  const timer = useRef<number | null>(null);
  const link = `${window.location.origin}${summary.share_path}`;
  const flash = (what: Copied) => {
    setCopied(what);
    if (timer.current) window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setCopied(null), 2500);
  };
  async function copy(what: 'code' | 'link') {
    const text = what === 'code' ? summary.code : link;
    try {
      await navigator.clipboard.writeText(text);
      flash(what);
    } catch {
      // No clipboard permission (or no secure context): leave the link selected instead.
      field.current?.focus();
      field.current?.select();
      flash('select');
    }
  }
  return (
    <Paper withBorder radius="lg" p={{ base: 20, sm: 28 }} data-testid="referral-code-card">
      <Group gap="sm" mb={14}>
        <IconGift size={20} />
        <Text size="sm" c="dimmed">
          我的推荐码
        </Text>
      </Group>
      <div className="referral-code" aria-label="我的推荐码" data-testid="referral-code">
        {summary.code}
      </div>
      <Group gap="sm" mt={18}>
        <Button
          variant="light"
          leftSection={copied === 'code' ? <IconCheck size={16} /> : <IconCopy size={16} />}
          onClick={() => void copy('code')}
        >
          复制推荐码
        </Button>
        <Button
          leftSection={copied === 'link' ? <IconCheck size={16} /> : <IconLink size={16} />}
          onClick={() => void copy('link')}
        >
          复制邀请链接
        </Button>
        <Text size="sm" c="teal.7" role="status" aria-live="polite" data-testid="copy-notice">
          {copied === 'code'
            ? '推荐码已复制'
            : copied === 'link'
              ? '邀请链接已复制'
              : copied === 'select'
                ? '无法自动复制，已选中邀请链接，请手动复制'
                : ''}
        </Text>
      </Group>
      <TextInput
        ref={field}
        mt={16}
        readOnly
        label="邀请链接"
        value={link}
        onFocus={(e) => e.currentTarget.select()}
        aria-label="邀请链接"
      />
    </Paper>
  );
}

function Stat({
  label,
  value,
  unit,
  detail,
}: {
  label: string;
  value: string;
  unit?: string;
  detail?: string;
}) {
  return (
    <Paper withBorder p={20} radius="lg">
      <Text size="xs" c="dimmed" mb={10}>
        {label}
      </Text>
      <div className="metric-value">
        {value}
        {unit ? (
          <Text span size="sm" c="dimmed" ml={4}>
            {unit}
          </Text>
        ) : null}
      </div>
      {detail ? (
        <Text size="xs" c="dimmed" mt={8}>
          {detail}
        </Text>
      ) : null}
    </Paper>
  );
}

function Rules({ maxGib }: { maxGib: number }) {
  const rules = [
    '好友通过你的推荐码注册，首次付费成功后，你获得与其实付金额等额的流量（1 元 = 1 GB）。',
    '只计算好友的第一次消费，之后的消费不再奖励。',
    '发放时你需要有有效套餐，否则这笔奖励不生效，也不会补发。',
    '奖励流量加在你当前的套餐上，随套餐到期失效；年付套餐的奖励不会因每 30 天的流量刷新而清零。',
  ];
  if (maxGib > 0) rules.push(`单笔奖励最多 ${maxGib} GB。`);
  return (
    <Paper withBorder radius="lg" p={{ base: 20, sm: 26 }}>
      <Title order={2} size={17} mb="sm">
        奖励规则
      </Title>
      <Stack gap={6} component="ul" m={0} pl={20}>
        {rules.map((rule) => (
          <Text key={rule} component="li" size="sm" c="dimmed" lh={1.7}>
            {rule}
          </Text>
        ))}
      </Stack>
    </Paper>
  );
}

function StatusBadge({ item }: { item: ReferralItem }) {
  const badge: ReferralBadge = referralBadge(item, formatBytes);
  return (
    <Group gap={6} wrap="wrap">
      <Badge color={badge.color} variant="light" style={{ textTransform: 'none' }}>
        {badge.text}
      </Badge>
      {item.referee_deleted ? (
        <Badge color="gray" variant="outline" style={{ textTransform: 'none' }}>
          已注销
        </Badge>
      ) : null}
    </Group>
  );
}

function Records({
  list,
  wide,
  page,
  onPage,
  summary,
}: {
  list: ReferralPage | null;
  wide: boolean;
  page: number;
  onPage: (page: number) => void;
  summary: ReferralSummary;
}) {
  const [copied, setCopied] = useState(false);
  if (!list) return <Skeleton height={200} radius="lg" />;
  const size = list.page_size > 0 ? list.page_size : 20;
  return (
    <Paper withBorder radius="lg" p="md" data-testid="referral-records">
      <Group justify="space-between" px="xs" pt="xs" pb="sm">
        <Title order={2} size={17}>
          推荐记录
        </Title>
        <Text size="xs" c="dimmed">
          共 {list.total} 人 · 邮箱已脱敏
        </Text>
      </Group>
      {list.items.length === 0 ? (
        <Stack align="center" gap="xs" py={40} data-testid="referral-empty">
          <IconGift size={34} stroke={1.3} color="var(--mantine-color-gray-5)" />
          <Title order={3} size={16}>
            还没有好友通过你的推荐码注册
          </Title>
          <Text size="sm" c="dimmed" ta="center">
            把邀请链接发给好友，注册后的推荐记录会显示在这里。
          </Text>
          <Button
            mt="xs"
            leftSection={copied ? <IconCheck size={16} /> : <IconLink size={16} />}
            onClick={() => {
              void navigator.clipboard
                .writeText(`${window.location.origin}${summary.share_path}`)
                .then(() => setCopied(true))
                .catch(() => setCopied(false));
            }}
          >
            {copied ? '邀请链接已复制' : '复制邀请链接'}
          </Button>
        </Stack>
      ) : wide ? (
        <ScrollArea>
          <Table verticalSpacing="md" horizontalSpacing="md" miw={640} aria-label="推荐记录">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>好友邮箱</Table.Th>
                <Table.Th>注册时间</Table.Th>
                <Table.Th>状态</Table.Th>
                <Table.Th>奖励</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {list.items.map((item, index) => (
                <Table.Tr key={`${item.registered_at}-${index}`}>
                  <Table.Td>
                    <Text size="sm" ff="monospace">
                      {item.email_masked}
                    </Text>
                  </Table.Td>
                  <Table.Td>{date(item.registered_at)}</Table.Td>
                  <Table.Td>
                    <StatusBadge item={item} />
                  </Table.Td>
                  <Table.Td>{referralReward(item, formatBytes)}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </ScrollArea>
      ) : (
        <Stack gap="sm" data-testid="referral-cards">
          {list.items.map((item, index) => (
            <Paper key={`${item.registered_at}-${index}`} withBorder radius="md" p="md">
              <Text size="sm" fw={600} ff="monospace">
                {item.email_masked}
              </Text>
              <Group justify="space-between" mt={8} wrap="nowrap" align="flex-start">
                <StatusBadge item={item} />
                <Text size="sm" fw={600} style={{ flexShrink: 0 }}>
                  {referralReward(item, formatBytes)}
                </Text>
              </Group>
              <Text size="xs" c="dimmed" mt={8}>
                注册于 {date(item.registered_at)}
              </Text>
            </Paper>
          ))}
        </Stack>
      )}
      {list.total > size ? (
        <Group justify="center" mt="lg">
          <Pagination value={page} onChange={onPage} total={Math.ceil(list.total / size)} />
        </Group>
      ) : null}
    </Paper>
  );
}
