'use client';
import Link from 'next/link';
import { useEffect, useRef, useState } from 'react';
import { Alert, Anchor, Button, Divider, Group, Modal, Stack, Text } from '@mantine/core';
import { api, APIError, message } from '@/lib/api';
import {
  date,
  gib,
  money,
  type PayType,
  type PlacedOrder,
  type Plan,
  type UpgradeQuote,
} from '@/lib/types';
import { openGateway, PayMethods } from './purchase-modal';

// The upgrade checkout (handoff_plans.md 11.2): the server's quote line by line, the change of
// entitlements, then a payment method and the gateway. The page computes nothing; the order
// carries the plan, the method and the quoted amount, which only lets the server refuse an order
// whose price moved. Upgrades take no coupon.
export function UpgradeModal({
  plan,
  allowed,
  yearlyView,
  onClose,
}: {
  plan: Plan | null;
  allowed: string[];
  // The yearly cards are on screen: say that the upgrade keeps the current way of paying.
  yearlyView: boolean;
  onClose: () => void;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <Modal
      opened={plan !== null}
      onClose={() => {
        if (!busy) onClose();
      }}
      title={plan ? `升级到 ${plan.name}` : ''}
      centered
      radius="lg"
      size="lg"
      closeOnClickOutside={!busy}
      closeOnEscape={!busy}
      withCloseButton={!busy}
    >
      {plan ? (
        <UpgradeCheckout
          key={plan.id}
          plan={plan}
          allowed={allowed}
          yearlyView={yearlyView}
          busy={busy}
          setBusy={setBusy}
        />
      ) : null}
    </Modal>
  );
}

const gb = (bytes: number) => `${gib(bytes)} GB`;

function Line({
  label,
  value,
  strong,
  tone,
}: {
  label: string;
  value: string;
  strong?: boolean;
  tone?: string;
}) {
  return (
    <Group justify="space-between" wrap="nowrap">
      <Text size="sm" fw={strong ? 600 : undefined}>
        {label}
      </Text>
      <Text
        size={strong ? 'xl' : 'sm'}
        fw={strong ? 650 : undefined}
        c={tone}
        aria-label={label}
        ta="right"
      >
        {value}
      </Text>
    </Group>
  );
}

// Mounted fresh for every plan the dialog opens on, so no state leaks between upgrades.
function UpgradeCheckout({
  plan,
  allowed,
  yearlyView,
  busy,
  setBusy,
}: {
  plan: Plan;
  allowed: string[];
  yearlyView: boolean;
  busy: boolean;
  setBusy: (busy: boolean) => void;
}) {
  const [payType, setPayType] = useState<PayType | null>(null);
  const [quote, setQuote] = useState<UpgradeQuote | null>(null);
  const [pending, setPending] = useState(0);
  const [quoting, setQuoting] = useState(true);
  const [quoteError, setQuoteError] = useState<string | null>(null);
  const [orderError, setOrderError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [blocked, setBlocked] = useState(false);
  const attempt = useRef<{ signature: string; key: string } | null>(null);
  const revision = useRef(0);
  const planId = plan.id;

  // Every quote request supersedes the earlier ones; only the latest may update the dialog.
  function ask() {
    const current = ++revision.current;
    return api<{ quote: UpgradeQuote; pending_orders: number }>('/payments/upgrade-quote', {
      method: 'POST',
      body: JSON.stringify({ plan_id: planId }),
    })
      .then((data) => {
        if (current !== revision.current) return;
        setQuote(data.quote);
        setPending(data.pending_orders);
        setQuoteError(null);
      })
      .catch((e) => {
        if (current !== revision.current) return;
        setQuote(null);
        setQuoteError(message(e));
      })
      .finally(() => {
        if (current === revision.current) setQuoting(false);
      });
  }
  useEffect(() => {
    const revisions = revision;
    void ask();
    return () => {
      revisions.current++;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function pay() {
    if (!payType || !quote?.purchasable || busy || quoting) return;
    // One Idempotency-Key per (plan, method): a retry replays the same order.
    const signature = `${planId}|${payType}`;
    if (attempt.current?.signature !== signature)
      attempt.current = { signature, key: crypto.randomUUID() };
    setBusy(true);
    setOrderError(null);
    setNotice(null);
    setBlocked(false);
    try {
      const placed = await api<PlacedOrder>('/payments/upgrade-orders', {
        method: 'POST',
        headers: { 'Idempotency-Key': attempt.current.key },
        body: JSON.stringify({
          plan_id: planId,
          pay_type: payType,
          expected_amount_cents: quote.amount_cents,
        }),
      });
      openGateway(placed.pay_url);
    } catch (e) {
      setBusy(false);
      const code = e instanceof APIError ? e.code : '';
      if (code === 'price_changed') {
        // Nothing was ordered and the gateway is not opened: show the new price first.
        attempt.current = null;
        setNotice('价格已更新，请确认后再支付');
        setQuoting(true);
        void ask();
        return;
      }
      if (code === 'idempotency_conflict') attempt.current = null;
      if (code === 'pending_order_exists') setBlocked(true);
      setOrderError(message(e));
      // The switch, the remaining time or the plans may have changed: show where things stand.
      if (
        [
          'upgrade_disabled',
          'remaining_too_short',
          'not_upgradable',
          'plan_not_purchasable',
        ].includes(code)
      ) {
        setQuoting(true);
        void ask();
      }
    }
  }

  const yearlyHeld = quote?.billing_cycle === 'yearly';
  return (
    <Stack gap="md" data-testid="upgrade-checkout">
      {quoteError ? (
        <Alert color="red" title="报价失败" role="alert">
          {quoteError}
        </Alert>
      ) : null}
      {quote ? (
        <>
          <Stack gap={6} className="quote-lines">
            <Line label="套餐变更" value={`${quote.from_plan_name} → ${quote.plan_name}`} />
            <Line label="到期时间（不变）" value={date(quote.expires_at)} />
            <Line
              label="剩余天数"
              value={`收费 ${quote.upgrade_days} 天，抵扣 ${quote.credited_days} 天`}
            />
            <Divider my={4} />
            <Line label="新套餐剩余价值" value={money(quote.value_cents)} />
            <Line label="已付金额抵扣" value={`-${money(quote.credit_cents)}`} tone="teal" />
            {quote.min_charge_applied ? (
              <Text size="xs" c="orange" data-testid="min-charge">
                抵扣已达上限，按最低补差 {money(quote.min_charge_cents)} 收取。
              </Text>
            ) : null}
            {quote.test_price ? (
              <>
                <Alert color="yellow" py={6}>
                  专属价：本账户按专属价格结算，付款将实际扣款。
                </Alert>
                <Line label="原应付" value={money(quote.list_amount_cents)} />
              </>
            ) : null}
            <Line label="应付金额" value={money(quote.amount_cents)} strong />
            <Text size="xs" c="dimmed">
              剩余时间按整天计：收费天数向上取整，抵扣天数向下取整。抵扣只按本周期实际支付的金额计算，赠送的天数和流量不折算。
            </Text>
          </Stack>
          <Stack gap={4} data-testid="upgrade-gains">
            <Text size="sm" fw={600}>
              升级后
            </Text>
            {yearlyHeld ? (
              <>
                <Text size="sm" aria-label="本周期新增流量">
                  本周期新增 {gb(quote.traffic_add_bytes)}，剩余流量保留
                </Text>
                <Text size="sm" aria-label="之后每周期流量">
                  之后每 30 天重置为 {gb(quote.new_cycle_bytes)}
                </Text>
              </>
            ) : (
              <Text size="sm" aria-label="新增流量">
                新增 {gb(quote.traffic_add_bytes)} 流量，剩余流量保留
              </Text>
            )}
            <Text size="sm" aria-label="设备数">
              最多 {quote.device_limit} 台设备同时在线
            </Text>
            <Text size="sm" aria-label="线路权限">
              {quote.allow_dedicated ? '普通直连 + 专线权限' : '普通直连线路权限'}
            </Text>
          </Stack>
          <Alert color="blue" data-testid="reauth-notice">
            升级成功后，已登录的客户端会断开一次，需要重新登录以获得新权益。
          </Alert>
          {quote.lost_groups.length > 0 ? (
            <Alert color="red" title="升级后不可再使用的线路" data-testid="lost-groups">
              {quote.lost_groups.join('、')}
            </Alert>
          ) : null}
          {yearlyView ? (
            <Text size="xs" c="dimmed">
              升级按你当前的付费方式补差价；如需年付，可在升级后续费为年付。
            </Text>
          ) : null}
          {!quote.purchasable ? (
            <Alert color="orange" role="alert">
              {quote.message || '当前无法升级到此套餐。'}
            </Alert>
          ) : null}
        </>
      ) : null}
      {notice ? (
        <Alert color="orange" role="status" data-testid="price-updated">
          {notice}
        </Alert>
      ) : null}
      {pending > 0 || blocked ? (
        <Alert color="yellow" data-testid="upgrade-pending">
          你有未完成的订单，请先完成支付或等待约 2 分钟后再试。{' '}
          <Anchor component={Link} href="/orders" size="sm">
            查看订单记录
          </Anchor>
        </Alert>
      ) : null}
      <PayMethods allowed={allowed} value={payType} onChange={setPayType} disabled={busy} />
      <Text size="xs" c="dimmed">
        点击“去支付”后将跳转到支付页面，请在 2 分钟内完成付款，付款结果以订单状态为准。
      </Text>
      {orderError && !blocked ? (
        <Alert color="red" title="下单未完成" role="alert">
          {orderError}
        </Alert>
      ) : null}
      <Button
        size="md"
        loading={busy}
        disabled={!payType || !quote?.purchasable || quoting}
        onClick={() => void pay()}
      >
        去支付
      </Button>
      {!payType ? (
        <Text size="xs" c="dimmed" ta="center">
          请先选择支付方式
        </Text>
      ) : null}
    </Stack>
  );
}
