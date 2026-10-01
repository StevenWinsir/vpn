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
import { date, gib, money, type Order } from '@/lib/types';
export function OrdersView() {
  const [orders, setOrders] = useState<Order[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
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
            完成测试购买后，订单就会显示在这里。
          </Text>
          <Button component={Link} href="/plans">
            选择测试套餐
          </Button>
        </Paper>
      ) : (
        <Paper withBorder radius="lg" p="md">
          <ScrollArea>
            <Table verticalSpacing="lg" horizontalSpacing="md" miw={700}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>套餐 / 订单号</Table.Th>
                  <Table.Th>额度与期限</Table.Th>
                  <Table.Th>演示金额</Table.Th>
                  <Table.Th>状态</Table.Th>
                  <Table.Th>创建时间</Table.Th>
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
                        {money(order.price_cents)}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Badge color={order.status === 'paid_test' ? 'teal' : 'gray'} variant="light">
                        {order.status === 'paid_test' ? '测试成功' : order.status}
                      </Badge>
                    </Table.Td>
                    <Table.Td>{date(order.created_at)}</Table.Td>
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
        “测试成功”表示模拟订单已入库，不代表真实支付成功，也不代表实际代理服务已提供。
      </Text>
    </div>
  );
}
