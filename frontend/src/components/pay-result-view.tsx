'use client';
import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
import { useEffect, useState } from 'react';
import { Alert, Badge, Button, Group, Loader, Paper, Stack, Text, Title } from '@mantine/core';
import {
  IconCircleCheck,
  IconClockOff,
  IconHourglass,
  IconAlertTriangle,
} from '@tabler/icons-react';
import { api, APIError, message } from '@/lib/api';
import { money, orderIdPattern, payTypeLabel, type PaymentOrder } from '@/lib/types';

const pollMs = 2000;
const giveUpMs = 5 * 60 * 1000;

// The page the gateway returns the customer to. What it shows comes only from the backend's
// order state for the signed-in account: the address bar carries a local order id and nothing
// else, and reaching this page proves nothing about payment (handoff_payment_account.md 6.8).
export function PayResultView() {
  // Anything in the address that is not shaped like an order id is an unknown order, with no
  // request made.
  const raw = useSearchParams().get('order') ?? '';
  const orderId = orderIdPattern.test(raw) ? raw.toLowerCase() : '';
  const [data, setData] = useState<PaymentOrder['order'] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [missing, setMissing] = useState(false);
  const [gaveUp, setGaveUp] = useState(false);
  const status = data?.status;
  const settled = status !== undefined && status !== 'pending';

  useEffect(() => {
    if (!orderId) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const started = Date.now();
    const tick = async () => {
      if (stopped) return;
      if (document.visibilityState === 'hidden') {
        timer = setTimeout(() => void tick(), pollMs);
        return;
      }
      let done = false;
      try {
        const result = await api<PaymentOrder>(`/payments/orders/${orderId}`);
        if (stopped) return;
        setData(result.order);
        setError(null);
        done = result.order.status !== 'pending';
      } catch (e) {
        if (stopped) return;
        if (e instanceof APIError && e.status === 404) {
          setMissing(true);
          done = true;
        } else setError(message(e));
      }
      if (done) return;
      if (Date.now() - started >= giveUpMs) {
        setGaveUp(true);
        return;
      }
      timer = setTimeout(() => void tick(), pollMs);
    };
    void tick();
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [orderId]);

  if (missing || orderId === '')
    return (
      <div className="page-wrap">
        <Paper withBorder radius="lg" p={40} ta="center" maw={560} mx="auto">
          <IconAlertTriangle size={38} stroke={1.4} color="var(--mantine-color-orange-6)" />
          <Title order={1} size={22} mt={14}>
            订单不存在
          </Title>
          <Text size="sm" c="dimmed" mt={8} mb="lg">
            没有找到这笔订单。请在订单记录中查看你的订单。
          </Text>
          <Button component={Link} href="/orders">
            查看订单记录
          </Button>
        </Paper>
      </div>
    );

  const gatewayPaid = data?.gateway_paid === true;
  let view: { icon: React.ReactNode; title: string; body: string; tone: string };
  if (!data)
    view = {
      icon: <Loader size={34} aria-label="正在查询订单" />,
      title: '正在查询订单…',
      body: '请稍候。',
      tone: 'gray',
    };
  else if (status === 'paid' || status === 'paid_test')
    view = {
      icon: <IconCircleCheck size={42} stroke={1.4} color="var(--mantine-color-teal-6)" />,
      title: '已到账',
      body: '套餐已开通或续期。你可以回到总览查看有效期与流量。',
      tone: 'teal',
    };
  else if (status === 'expired')
    view = {
      icon: <IconClockOff size={42} stroke={1.4} color="var(--mantine-color-gray-6)" />,
      title: '订单已超时，未扣款',
      body: '这笔订单没有完成支付。需要时请重新购买。',
      tone: 'gray',
    };
  else if (status === 'failed')
    view = {
      icon: <IconAlertTriangle size={42} stroke={1.4} color="var(--mantine-color-red-6)" />,
      title: '支付未成功',
      body: '支付网关报告这笔订单未成功。需要时请重新购买。',
      tone: 'red',
    };
  else if (status === 'review')
    view = {
      icon: <IconAlertTriangle size={42} stroke={1.4} color="var(--mantine-color-orange-6)" />,
      title: '待人工核对',
      body: '付款需要人工核对后才能开通。请联系客服并提供下方的订单号。',
      tone: 'orange',
    };
  else if (gatewayPaid)
    view = {
      icon: <Loader size={34} color="teal" aria-label="正在确认到账" />,
      title: '支付已完成，正在确认到账',
      body: '正在等待支付网关通知，通常几秒内完成。请不要重复付款。',
      tone: 'teal',
    };
  else
    view = {
      icon: <IconHourglass size={42} stroke={1.4} color="var(--mantine-color-yellow-7)" />,
      title: '待支付',
      body: '尚未收到付款。如果你已付款，请稍候，页面会自动更新。',
      tone: 'yellow',
    };

  return (
    <div className="page-wrap">
      <Paper withBorder radius="lg" p={40} ta="center" maw={560} mx="auto" data-testid="pay-result">
        <Stack align="center" gap="sm">
          {view.icon}
          <Title order={1} size={24}>
            {view.title}
          </Title>
          <Text size="sm" c="dimmed">
            {view.body}
          </Text>
          {data ? (
            <Stack gap={4} mt="sm" align="center">
              <Group gap="xs">
                <Badge variant="light" color={view.tone}>
                  {data.plan_name}
                </Badge>
                {data.billing_cycle === 'yearly' ? (
                  <Badge variant="light" color="orange">
                    年付
                  </Badge>
                ) : null}
                {data.pay_type ? (
                  <Badge variant="light" color="gray">
                    {payTypeLabel[data.pay_type] ?? data.pay_type}
                  </Badge>
                ) : null}
              </Group>
              <Text size="sm">
                应付 {money(data.amount_cents || data.price_cents)}
                {data.discount_cents ? `（已优惠 ${money(data.discount_cents)}）` : ''}
              </Text>
              <Text size="xs" c="dimmed" className="order-id">
                订单号 {data.id}
              </Text>
            </Stack>
          ) : null}
          {error ? (
            <Alert color="red" role="alert">
              {error}
            </Alert>
          ) : null}
          {gaveUp && !settled ? (
            <Alert color="gray">结果确认中，可稍后在订单记录中查看。</Alert>
          ) : null}
          <Group justify="center" mt="md">
            {status === 'expired' || status === 'failed' ? (
              <Button
                component={Link}
                href={`/plans?plan=${encodeURIComponent(data?.plan_id ?? '')}${
                  data?.billing_cycle === 'yearly' ? '&cycle=yearly' : ''
                }${data?.pay_type ? `&pay=${encodeURIComponent(data.pay_type)}` : ''}`}
              >
                重新购买
              </Button>
            ) : null}
            {status === 'paid' || status === 'paid_test' ? (
              <Button component={Link} href="/dashboard">
                前往总览
              </Button>
            ) : null}
            <Button component={Link} href="/orders" variant="light">
              订单记录
            </Button>
          </Group>
        </Stack>
      </Paper>
    </div>
  );
}
