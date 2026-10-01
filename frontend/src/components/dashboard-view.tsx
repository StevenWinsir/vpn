'use client';
import Link from 'next/link';
import { useCallback, useEffect, useState } from 'react';
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
import { IconArrowRight, IconRefresh, IconShieldCheck, IconWifiOff } from '@tabler/icons-react';
import { api, message } from '@/lib/api';
import { date, gib, type Dashboard } from '@/lib/types';

export function DashboardView() {
  const [data, setData] = useState<Dashboard | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const load = useCallback(async () => {
    setBusy(true);
    try {
      setData(await api<Dashboard>('/me/dashboard'));
      setError(null);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }, []);
  useEffect(() => {
    let active = true;
    void api<Dashboard>('/me/dashboard')
      .then((result) => {
        if (active) {
          setData(result);
          setError(null);
        }
      })
      .catch((e) => {
        if (active) setError(message(e));
      });
    return () => {
      active = false;
    };
  }, []);
  const sub = data?.subscription;
  const usedBytes = (sub?.used_units || 0) / 1000;
  const limit = sub?.traffic_limit_bytes || 0;
  const usedPercent = limit ? Math.min(100, (usedBytes / limit) * 100) : 0;
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
          loading={busy}
          onClick={() => void load()}
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
              detail={sub?.is_test ? '测试权益 · 不等于代理已连接' : '开通后可查看有效期和额度'}
            />
            <Metric
              label="剩余计费额度"
              value={`${gib(Math.max(0, limit - usedBytes))} GiB`}
              detail={data.metering_connected ? '按线路倍率折算后扣减' : '尚未接入代理节点计量'}
            />
            <Metric
              label="累计订单"
              value={`${data.order_count}`}
              detail="订单来自当前账户的真实数据库记录"
            />
          </SimpleGrid>
          <SimpleGrid cols={{ base: 1, lg: 2 }} spacing={24}>
            <Paper withBorder p={26} radius="lg">
              <Group justify="space-between">
                <Title order={2} size={17}>
                  流量概览
                </Title>
                <Badge variant="light" color="gray">
                  {data.metering_connected ? '已接入计量' : '待接入计量'}
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
                      套餐总额度
                    </Text>
                    <Text size="lg" fw={600}>
                      {gib(limit)} GiB
                    </Text>
                  </div>
                </Stack>
              </Group>
              <Divider />
              <Group grow mt="lg">
                <div>
                  <Text c="dimmed" size="xs">
                    专线倍率（设计值）
                  </Text>
                  <Text size="sm" fw={600} mt={5}>
                    实际 1 GiB = 计费 1 GiB
                  </Text>
                </div>
                <div>
                  <Text c="dimmed" size="xs">
                    普通线路倍率（设计值）
                  </Text>
                  <Text size="sm" fw={600} mt={5}>
                    实际 1 GiB = 计费 0.5 GiB
                  </Text>
                </div>
              </Group>
            </Paper>
            <Paper withBorder p={26} radius="lg">
              <Title order={2} size={17} mb={22}>
                连接与客户端
              </Title>
              <div className="empty-state">
                <IconWifiOff size={34} stroke={1.3} />
                <Text size="sm" fw={600} c="dark.4" mt="sm">
                  尚未接入代理服务
                </Text>
                <Text size="xs" mt={8} lh={1.8}>
                  当前没有可用的原生客户端或节点连接。
                  <br />
                  开通测试套餐不会建立 VPN 连接。
                </Text>
              </div>
              <Group justify="space-between" mt="lg">
                <Text size="sm" c="dimmed">
                  设备额度
                </Text>
                <Text size="sm" fw={600}>
                  {sub ? `${sub.max_devices} 台（待客户端执行）` : '—'}
                </Text>
              </Group>
              <Group justify="space-between" mt="sm">
                <Text size="sm" c="dimmed">
                  专线权限
                </Text>
                <Text size="sm" fw={600}>
                  {sub?.allow_dedicated ? '套餐包含' : '未包含'}
                </Text>
              </Group>
            </Paper>
          </SimpleGrid>
          <Alert color="gray" title="开发环境说明">
            流量图表展示数据库中的额度与计数，尚未连接节点侧计量。所有测试订单均不会扣款，也不会下发代理密码或
            YAML 配置。
          </Alert>
        </Stack>
      )}
      <div className="page-footer">
        统计口径：1 GiB = 1,073,741,824 字节。套餐权限最终由后端校验。
      </div>
    </div>
  );
}
function Metric({ label, value, detail }: { label: string; value: string; detail: string }) {
  return (
    <Paper withBorder p={23} radius="lg">
      <Text size="xs" c="dimmed" mb={12}>
        {label}
      </Text>
      <div className="metric-value">{value}</div>
      <Text c="dimmed" size="xs" mt={12} lh={1.6}>
        {detail}
      </Text>
    </Paper>
  );
}
