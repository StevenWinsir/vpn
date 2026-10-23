'use client';
import Link from 'next/link';
import { useCallback, useEffect, useState } from 'react';
import {
  Alert,
  Badge,
  Button,
  Group,
  Pagination,
  Paper,
  ScrollArea,
  Skeleton,
  Stack,
  Table,
  Text,
  Title,
} from '@mantine/core';
import { IconReceipt } from '@tabler/icons-react';
import { api, message } from '@/lib/api';
import {
  date,
  gib,
  livePending,
  money,
  orderStatusLabel,
  payTypeLabel,
  type Order,
} from '@/lib/types';
export function OrdersView() {
  const [orders, setOrders] = useState<Order[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(() => Date.now());
  const load = useCallback(async () => {
    setLoading(true);
    try {
      const result = await api<{ orders: Order[]; total: number }>(`/orders?page=${page}`);
      setOrders(result.orders);
      setTotal(result.total);
      setError(null);
    } catch (e) {
      setError(message(e));
    } finally {
      setLoading(false);
    }
  }, [page]);
  useEffect(() => {
    let active = true;
    void api<{ orders: Order[]; total: number }>(`/orders?page=${page}`)
      .then((result) => {
        if (active) {
          setOrders(result.orders);
          setTotal(result.total);
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
  }, [page]);
  const waiting = orders.filter((o) => livePending(o, tick));
  const anyPending = orders.some((o) => o.status === 'pending');
  useEffect(() => {
    // Pending orders count down and settle on their own: keep the clock and the list moving.
    if (!anyPending) return;
    const clock = setInterval(() => setTick(Date.now()), 1000);
    const refresh = setInterval(() => {
      if (document.visibilityState === 'visible') void load();
    }, 5000);
    return () => {
      clearInterval(clock);
      clearInterval(refresh);
    };
  }, [anyPending, load]);
  return (
    <div className="page-wrap">
      <Group justify="space-between" mb={30}>
        <div>
          <Title order={1} size={29} className="page-title">
            订单记录
          </Title>
          <Text size="sm" c="dimmed" mt={8}>
            查看每一次订阅。共 {total} 笔订单。
          </Text>
        </div>
        <Button component={Link} href="/plans" variant="light">
          浏览套餐
        </Button>
      </Group>
      {waiting.length > 1 ? (
        <Alert color="yellow" title="你有多笔未完成的订单" mb="lg">
          请勿重复付款。未付款的订单会在几分钟内自动失效；需要时重新购买即可。
        </Alert>
      ) : null}
      {error ? (
        <Alert color="red" title="加载失败" mb="lg">
          <Stack>
            {error}
            <Button onClick={() => void load()} variant="light" size="xs">
              重试
            </Button>
          </Stack>
        </Alert>
      ) : null}
      {loading ? (
        <Skeleton height={260} radius="lg" />
      ) : orders.length === 0 ? (
        <Paper withBorder radius="lg" p={50} ta="center">
          <IconReceipt size={36} stroke={1.3} color="var(--mantine-color-gray-5)" />
          <Title order={2} size={18} mt={15}>
            还没有订单
          </Title>
          <Text size="sm" c="dimmed" mt="sm" mb="lg">
            完成购买后，订单就会显示在这里。
          </Text>
          <Button component={Link} href="/plans">
            选择套餐
          </Button>
        </Paper>
      ) : (
        <Paper withBorder radius="lg" p="md">
          <ScrollArea>
            <Table verticalSpacing="lg" horizontalSpacing="md" miw={900}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>套餐 / 订单号</Table.Th>
                  <Table.Th>额度与期限</Table.Th>
                  <Table.Th>金额</Table.Th>
                  <Table.Th>支付方式</Table.Th>
                  <Table.Th>状态</Table.Th>
                  <Table.Th>创建时间</Table.Th>
                  <Table.Th>支付时间</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {orders.map((order) => (
                  <Table.Tr key={order.id}>
                    <Table.Td>
                      <Text size="sm" fw={600}>
                        {order.plan_name}
                      </Text>
                      <div className="order-id">
                        {order.id.slice(0, 8)}…{order.id.slice(-4)}
                      </div>
                    </Table.Td>
                    <Table.Td>
                      {gib(order.traffic_bytes)} GiB / {order.duration_days} 天
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" fw={600}>
                        {money(order.amount_cents || order.price_cents)}
                      </Text>
                      {order.discount_cents ? (
                        <Text size="xs" c="dimmed">
                          优惠 {money(order.discount_cents)}
                          {order.coupon_code ? `（${order.coupon_code}）` : ''}
                        </Text>
                      ) : null}
                      {order.paid_cents != null && order.paid_cents !== order.amount_cents ? (
                        <Text size="xs" c="dimmed">
                          实付 {money(order.paid_cents)}
                        </Text>
                      ) : null}
                    </Table.Td>
                    <Table.Td>
                      {order.pay_type ? (payTypeLabel[order.pay_type] ?? order.pay_type) : '—'}
                    </Table.Td>
                    <Table.Td>
                      <Badge
                        color={orderStatusLabel[order.status]?.color ?? 'gray'}
                        variant="light"
                      >
                        {orderStatusLabel[order.status]?.text ?? order.status}
                      </Badge>
                      {livePending(order, tick) ? (
                        <Text size="xs" c="dimmed" mt={4}>
                          剩余 {remaining(order, tick)}
                        </Text>
                      ) : null}
                    </Table.Td>
                    <Table.Td>{date(order.created_at)}</Table.Td>
                    <Table.Td>{order.paid_at ? date(order.paid_at) : '—'}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </ScrollArea>
          {total > 20 ? (
            <Group justify="center" mt="lg">
              <Pagination value={page} onChange={setPage} total={Math.ceil(total / 20)} />
            </Group>
          ) : null}
        </Paper>
      )}
      <Text c="dimmed" size="xs" mt="lg">
        “测试成功”表示模拟订单已入库，不代表真实支付成功。待付款的订单不能继续支付，请重新购买；支付结果以“已支付”为准。
      </Text>
    </div>
  );
}

function remaining(order: Order, now: number) {
  const seconds = Math.max(0, Math.ceil((new Date(order.expires_at ?? 0).getTime() - now) / 1000));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
}
