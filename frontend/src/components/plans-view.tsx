'use client';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useRouter } from 'next/navigation';
import {
  Alert,
  Badge,
  Button,
  Divider,
  Group,
  List,
  Modal,
  Paper,
  SimpleGrid,
  Skeleton,
  Stack,
  Text,
  Title,
} from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { IconArrowRight, IconCheck } from '@tabler/icons-react';
import { api, message } from '@/lib/api';
import { gib, money, type Plan, type Meta, type Order } from '@/lib/types';
import { useAuth } from './providers';

export function PlansView() {
  const { user } = useAuth();
  const router = useRouter();
  const [plans, setPlans] = useState<Plan[]>([]);
  const [meta, setMeta] = useState<Meta | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<Plan | null>(null);
  const [busy, setBusy] = useState(false);
  const [purchaseError, setPurchaseError] = useState<string | null>(null);
  const requestKey = useRef<string | null>(null);
  const load = useCallback(async () => {
    try {
      const [p, m] = await Promise.all([api<{ plans: Plan[] }>('/plans'), api<Meta>('/meta')]);
      setPlans(p.plans);
      setMeta(m);
      setError(null);
    } catch (e) {
      setError(message(e));
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => {
    let active = true;
    void Promise.all([api<{ plans: Plan[] }>('/plans'), api<Meta>('/meta')])
      .then(([p, m]) => {
        if (active) {
          setPlans(p.plans);
          setMeta(m);
          setError(null);
        }
      })
      .catch((e) => {
        if (active) setError(message(e));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, []);
  function choose(plan: Plan) {
    if (!user) {
      router.push('/login');
      return;
    }
    requestKey.current = crypto.randomUUID();
    setPurchaseError(null);
    setSelected(plan);
  }
  async function purchase() {
    if (!selected || !requestKey.current || busy) return;
    setBusy(true);
    setPurchaseError(null);
    try {
      await api<{ order: Order; replayed: boolean }>('/orders/test-purchase', {
        method: 'POST',
        headers: { 'Idempotency-Key': requestKey.current },
        body: JSON.stringify({ plan_id: selected.id }),
      });
      setSelected(null);
      requestKey.current = null;
      notifications.show({
        title: '测试套餐已开通',
        message: '订单已保存，未发生真实扣款。',
        color: 'teal',
      });
      router.push('/dashboard');
    } catch (e) {
      setPurchaseError(message(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="page-wrap">
      <Title order={1} size={29} className="page-title">
        找到适合你的连接计划
      </Title>
      <Text c="dimmed" size="sm" mt={10} mb={28} className="page-subtitle">
        清晰的额度、明确的权益。选择套餐，体验完整的订阅流程。
      </Text>
      {error ? (
        <Alert color="red" title="套餐加载失败" mb="lg">
          <Stack gap="sm">
            {error}
            <Button size="xs" variant="light" onClick={() => void load()}>
              重新加载
            </Button>
          </Stack>
        </Alert>
      ) : null}
      {meta ? (
        <Alert
          mb={28}
          color={meta.test_purchase_enabled ? 'yellow' : 'gray'}
          title={meta.test_purchase_enabled ? '当前为测试购买模式' : '测试购买已关闭'}
        >
          {meta.test_purchase_enabled
            ? '所有价格仅用于流程演示。模拟开通不会产生扣款，也不提供实际代理服务。'
            : '后端已禁用模拟购买；真实支付通道尚未接入。'}
        </Alert>
      ) : null}
      <SimpleGrid cols={{ base: 1, md: 3 }} spacing={22}>
        {loading
          ? [1, 2, 3].map((i) => <Skeleton key={i} height={410} radius="lg" />)
          : plans.map((plan) => (
              <Paper
                key={plan.id}
                withBorder
                p={27}
                radius="lg"
                className={`plan-card ${plan.id === 'pro' ? 'featured' : ''}`}
              >
                <Group justify="space-between" mb={16}>
                  <Title order={2} size={19}>
                    {plan.name}
                  </Title>
                  {plan.allow_dedicated ? (
                    <Badge variant="light" size="sm">
                      含专线权益
                    </Badge>
                  ) : null}
                </Group>
                <Text size="xs" c="dimmed" mih={38}>
                  {plan.description}
                </Text>
                <Group gap={5} align="baseline" mt={24} mb={6}>
                  <Text fz={36} fw={650} style={{ letterSpacing: '-1.5px' }}>
                    {money(plan.price_cents)}
                  </Text>
                  <Text c="dimmed" size="xs">
                    / {plan.duration_days} 天
                  </Text>
                </Group>
                <Text size="xs" c="dimmed">
                  测试定价 · 人民币
                </Text>
                <Divider my={24} />
                <Stack gap={15} mb={30}>
                  <Feature text={`${gib(plan.traffic_bytes)} GiB 计费流量`} />
                  <Feature text={`${plan.max_devices} 台设备额度（待执行）`} />
                  <Feature
                    text={plan.allow_dedicated ? '普通直连 + 专线权限' : '普通直连线路权限'}
                  />
                  <Feature text={`有效期 ${plan.duration_days} 天`} />
                </Stack>
                <Button
                  fullWidth
                  mt="auto"
                  variant={plan.id === 'pro' ? 'filled' : 'light'}
                  disabled={!meta?.test_purchase_enabled}
                  onClick={() => choose(plan)}
                  rightSection={<IconArrowRight size={16} />}
                  aria-label={`测试购买 ${plan.name}`}
                >
                  {user ? '测试购买' : '登录后测试购买'}
                </Button>
              </Paper>
            ))}
      </SimpleGrid>
      {!loading && !error && plans.length === 0 ? (
        <div className="empty-state">暂时没有上架的套餐。</div>
      ) : null}
      <Paper withBorder radius="lg" p={28} mt={28}>
        <Title order={2} size={16} mb={16}>
          流量与续购，怎么算？
        </Title>
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing={30}>
          <div>
            <Text size="sm" fw={600} mb={7}>
              按线路倍率扣减
            </Text>
            <Text c="dimmed" size="xs" lh={1.9}>
              预留普通线路 0.5 倍、专线 1 倍的计费模型。例如实际使用 10 GiB，分别计费 5 GiB 和 10
              GiB。最终倍率由节点侧配置决定。
            </Text>
          </div>
          <div>
            <Text size="sm" fw={600} mb={7}>
              相同套餐续购
            </Text>
            <Text c="dimmed" size="xs" lh={1.9}>
              有效期内续购同一测试套餐，会延长有效期并累加额度；已用流量不会清零。到期后重新开通则重置额度。暂不支持有效期内跨套餐升级。
            </Text>
          </div>
        </SimpleGrid>
      </Paper>
      <Modal
        opened={selected !== null}
        onClose={() => {
          if (!busy) setSelected(null);
        }}
        title="确认测试购买"
        centered
        radius="lg"
        closeOnClickOutside={!busy}
        closeOnEscape={!busy}
        withCloseButton={!busy}
      >
        {selected ? (
          <Stack gap="lg">
            <Alert color="yellow">这是一笔模拟订单。不会扣款，也不会建立 VPN 连接。</Alert>
            <div>
              <Text size="lg" fw={650}>
                {selected.name}
              </Text>
              <Text size="sm" c="dimmed" mt={5}>
                {gib(selected.traffic_bytes)} GiB · {selected.duration_days} 天
              </Text>
            </div>
            <Group justify="space-between">
              <Text size="sm">演示金额</Text>
              <Text size="xl" fw={650}>
                {money(selected.price_cents)}
              </Text>
            </Group>
            <List size="xs" c="dimmed" spacing="xs">
              <List.Item>测试订单将保存到数据库。</List.Item>
              <List.Item>重试同一请求不会重复增加有效期或额度。</List.Item>
              <List.Item>有效期内仅支持续购相同测试套餐。</List.Item>
            </List>
            {purchaseError ? (
              <Alert color="red" title="开通未完成" role="alert">
                {purchaseError}
              </Alert>
            ) : null}
            <Button size="md" loading={busy} onClick={() => void purchase()}>
              确认模拟开通
            </Button>
          </Stack>
        ) : null}
      </Modal>
      <div className="page-footer">价格和权限由后端提供，浏览器不能自行指定订单金额。</div>
    </div>
  );
}
function Feature({ text }: { text: string }) {
  return (
    <div className="plan-feature">
      <IconCheck size={17} />
      {text}
    </div>
  );
}
