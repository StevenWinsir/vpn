'use client';
import Link from 'next/link';
import { useCallback, useState } from 'react';
import {
  Alert,
  Badge,
  Button,
  Divider,
  Group,
  Paper,
  RingProgress,
  SimpleGrid,
  Skeleton,
  Stack,
  Text,
  Title,
} from '@mantine/core';
import { IconArrowRight, IconRefresh, IconShieldCheck } from '@tabler/icons-react';
import { api, message } from '@/lib/api';
import { date, gib, type Dashboard, type DeviceStatus, type UsageHistory } from '@/lib/types';
import { formatBytes, isDeviceStatus, isUsageHistory } from '@/lib/dashboard';
import { usePolling } from '@/lib/use-polling';
import { DevicesCard } from './devices-card';
import { ReferralCard } from './referral-card';
import { UsageChart } from './usage-chart';

export function DashboardView({ refreshMs }: { refreshMs: number }) {
  const [data, setData] = useState<Dashboard | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [devices, setDevices] = useState<DeviceStatus | null>(null);
  const [devicesError, setDevicesError] = useState('');
  const [history, setHistory] = useState<UsageHistory | null>(null);
  const [historyError, setHistoryError] = useState('');
  // The three requests are independent: one failing never blanks the others.
  const load = useCallback(async (signal: AbortSignal) => {
    const [summary, presence, usage] = await Promise.allSettled([
      api<Dashboard>('/me/dashboard', { signal }),
      api<DeviceStatus>('/me/devices', { signal }),
      api<UsageHistory>('/me/usage-history', { signal }),
    ]);
    if (signal.aborted) return;
    if (summary.status === 'fulfilled') {
      setData(summary.value);
      setError(null);
    } else setError(message(summary.reason));
    // A failed or malformed presence/history response clears that card rather than
    // presenting stale online state as current.
    if (presence.status === 'fulfilled' && isDeviceStatus(presence.value)) {
      setDevices(presence.value);
      setDevicesError('');
    } else {
      setDevices(null);
      setDevicesError(
        presence.status === 'rejected'
          ? message(presence.reason)
          : '设备状态暂时不可用，请稍后重试。',
      );
    }
    if (usage.status === 'fulfilled' && isUsageHistory(usage.value)) {
      setHistory(usage.value);
      setHistoryError('');
    } else {
      setHistory(null);
      setHistoryError(
        usage.status === 'rejected' ? message(usage.reason) : '流量历史暂时不可用，请稍后重试。',
      );
    }
  }, []);
  const { syncing, refresh } = usePolling(load, refreshMs);
  const sub = data?.subscription;
  const usedBytes = (sub?.used_units || 0) / 1000;
  const limit = sub?.traffic_limit_bytes || 0;
  const usedPercent = limit ? Math.min(100, (usedBytes / limit) * 100) : 0;
  // A yearly subscription refreshes its allowance every 30 days (handoff_shop.md D2 revised).
  const resetsAt = data?.quota_resets_at ?? null;
  const cycleBytes = sub?.quota_cycle_days ? sub.quota_cycle_bytes || 0 : 0;
  return (
    <div className="page-wrap">
      <Group justify="space-between" align="flex-start" mb={30}>
        <div>
          <Title order={1} size={29} className="page-title">
            {data ? `${data.user.name}，欢迎回来` : '账户总览'}
          </Title>
          <Text c="dimmed" size="sm" mt={8}>
            你的订阅、流量与账户动态，都在这里。
          </Text>
        </div>
        <Button
          variant="default"
          size="xs"
          leftSection={<IconRefresh size={15} />}
          loading={syncing}
          onClick={refresh}
        >
          同步数据
        </Button>
      </Group>
      {error ? (
        <Alert color="red" title="加载失败" mb="lg" role="alert">
          {error}
        </Alert>
      ) : null}
      {!data ? (
        <Stack>
          <Skeleton height={160} radius="lg" />
          <SimpleGrid cols={{ base: 1, md: 3 }}>
            <Skeleton height={116} />
            <Skeleton height={116} />
            <Skeleton height={116} />
          </SimpleGrid>
        </Stack>
      ) : (
        <Stack gap={24}>
          <section className="subscription-banner">
            <div>
              <Group gap="sm" mb={12}>
                <IconShieldCheck size={20} />
                <Text size="sm" className="muted">
                  当前订阅
                </Text>
                {sub?.is_test ? (
                  <Badge color="teal" variant="light">
                    测试套餐
                  </Badge>
                ) : null}
              </Group>
              <Title order={2} size={26}>
                {sub ? sub.plan_name : '还没有开通套餐'}
              </Title>
              <Text size="sm" className="muted" mt={10}>
                {sub
                  ? `${data.entitlement_active ? '有效期至' : '权益已失效 · 到期时间'} ${date(sub.expires_at)}`
                  : '选择适合你的计划，体验套餐开通与订单流程。'}
              </Text>
            </div>
            <Button
              component={Link}
              href="/plans"
              color="gray"
              variant="white"
              rightSection={<IconArrowRight size={16} />}
            >
              {sub ? '管理套餐' : '选择套餐'}
            </Button>
          </section>
          <SimpleGrid cols={{ base: 1, sm: 3 }} spacing={20}>
            <Metric
              label="套餐权益"
              value={data.entitlement_active ? '已开通' : '未生效'}
              detail={sub?.is_test ? '测试权益 · 不等于代理已连接' : '有效期和额度以订阅快照为准'}
            />
            <Metric
              label="剩余计费额度"
              value={`${gib(Math.max(0, limit - usedBytes))} GiB`}
              detail={
                resetsAt
                  ? `本周期剩余 · ${date(resetsAt)} 重置为 ${gib(cycleBytes)} GiB`
                  : data.metering_connected
                    ? '按所连线路倍率折算后扣减'
                    : '近 24 小时暂无计量数据'
              }
              bonus={data.bonus_bytes ?? 0}
            />
            <Metric
              label="累计订单"
              value={`${data.order_count}`}
              detail="订单来自当前账户的真实数据库记录"
            />
          </SimpleGrid>
          <ReferralCard refreshMs={refreshMs} />
          <SimpleGrid cols={{ base: 1, lg: 2 }} spacing={24}>
            <Paper withBorder p={26} radius="lg">
              <Group justify="space-between">
                <Title order={2} size={17}>
                  流量概览
                </Title>
                <Badge variant="light" color="gray">
                  {data.metering_connected ? '24 小时内有计量' : '24 小时内无计量'}
                </Badge>
              </Group>
              <Group justify="center" gap={35} my={22}>
                <RingProgress
                  size={166}
                  thickness={12}
                  roundCaps
                  sections={[{ value: usedPercent, color: 'teal' }]}
                  label={
                    <div style={{ textAlign: 'center' }}>
                      <div className="quota-label">{usedPercent.toFixed(0)}%</div>
                      <Text size="xs" c="dimmed">
                        已用比例
                      </Text>
                    </div>
                  }
                />
                <Stack gap={15}>
                  <div>
                    <Text size="xs" c="dimmed">
                      已用计费流量
                    </Text>
                    <Text size="lg" fw={600}>
                      {gib(usedBytes)} GiB
                    </Text>
                  </div>
                  <div>
                    <Text size="xs" c="dimmed">
                      {cycleBytes ? '本周期额度' : '套餐总额度'}
                    </Text>
                    <Text size="lg" fw={600}>
                      {gib(limit)} GiB
                    </Text>
                  </div>
                </Stack>
              </Group>
              <Divider />
              <Text size="xs" c="dimmed" mt="lg" lh={1.8}>
                实际上下行流量 × 所连线路的倍率 = 扣除的计费流量。
                <Link href="/nodes">查看各线路倍率</Link>
              </Text>
            </Paper>
            <DevicesCard
              devices={devices}
              error={devicesError}
              fallbackLimit={data.device_limit ?? 0}
              allowDedicated={!!sub?.allow_dedicated}
            />
          </SimpleGrid>
          <UsageChart history={history} error={historyError} />
          <Text size="xs" c="dimmed" ta="right">
            页面可见时每 {refreshMs / 1000} 秒自动刷新，返回页面时也会刷新。
          </Text>
          {sub?.is_test ? (
            <Alert color="gray" title="测试套餐">
              测试订单不会扣款；套餐权益不代表客户端已连接，连接状态以“连接与客户端”为准。
            </Alert>
          ) : null}
        </Stack>
      )}
      <div className="page-footer">
        统计口径：1 GiB = 1,073,741,824 字节。套餐权限最终由后端校验。
      </div>
    </div>
  );
}
function Metric({
  label,
  value,
  detail,
  bonus = 0,
}: {
  label: string;
  value: string;
  detail: string;
  bonus?: number;
}) {
  return (
    <Paper withBorder p={23} radius="lg">
      <Text size="xs" c="dimmed" mb={12}>
        {label}
      </Text>
      <div className="metric-value">{value}</div>
      <Text c="dimmed" size="xs" mt={12} lh={1.6}>
        {detail}
        {bonus > 0 ? ` · 含推荐奖励 ${formatBytes(bonus)}` : ''}
      </Text>
    </Paper>
  );
}
