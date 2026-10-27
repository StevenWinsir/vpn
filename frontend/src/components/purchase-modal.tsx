'use client';
import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Group, Modal, Stack, Text, TextInput, UnstyledButton } from '@mantine/core';
import { api, APIError, message } from '@/lib/api';
import { refreshes, tierOf, trafficLabel, zhe } from '@/lib/shop';
import {
  money,
  payTypeLabel,
  type BillingCycle,
  type PayType,
  type PlacedOrder,
  type Plan,
  type Quote,
} from '@/lib/types';

const methods: { type: PayType; hint: string; color: string }[] = [
  { type: 'wxpay', hint: '使用微信扫码支付', color: '#07c160' },
  { type: 'alipay', hint: '使用支付宝扫码支付', color: '#1677ff' },
];
const couponPattern = /^[A-Za-z0-9-]{4,32}$/;
const normalize = (value: string) => value.trim().toUpperCase();

// The checkout the gateway flow needs: pick a payment method, optionally apply a coupon,
// review the server's quote, then leave for the gateway. Prices are only ever displayed from
// the quote; the order request carries a plan, a method and a coupon code, never an amount.
export function PurchaseModal({
  plan,
  cycle,
  renew,
  guardAmount,
  initialPayType,
  allowed,
  onClose,
}: {
  plan: Plan | null;
  cycle: BillingCycle;
  // The account already holds this plan: the yearly title says 续费.
  renew: boolean;
  // The backend understands expected_amount_cents (it serves the yearly plan fields).
  guardAmount: boolean;
  initialPayType: PayType | null;
  allowed: string[];
  onClose: () => void;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <Modal
      opened={plan !== null}
      onClose={() => {
        if (!busy) onClose();
      }}
      title={
        plan
          ? `${cycle === 'yearly' && renew ? '续费' : '购买'} ${plan.name}${cycle === 'yearly' ? '（年付）' : ''}`
          : ''
      }
      centered
      radius="lg"
      closeOnClickOutside={!busy}
      closeOnEscape={!busy}
      withCloseButton={!busy}
    >
      {plan ? (
        <Checkout
          key={`${plan.id}|${cycle}`}
          plan={plan}
          cycle={cycle}
          guardAmount={guardAmount}
          initialPayType={initialPayType}
          allowed={allowed}
          busy={busy}
          setBusy={setBusy}
        />
      ) : null}
    </Modal>
  );
}

// Mounted fresh for every plan the dialog opens on, so no state leaks between purchases.
function Checkout({
  plan,
  cycle,
  guardAmount,
  initialPayType,
  allowed,
  busy,
  setBusy,
}: {
  plan: Plan;
  cycle: BillingCycle;
  guardAmount: boolean;
  initialPayType: PayType | null;
  allowed: string[];
  busy: boolean;
  setBusy: (busy: boolean) => void;
}) {
  const [payType, setPayType] = useState<PayType | null>(initialPayType);
  const [coupon, setCoupon] = useState('');
  const [quote, setQuote] = useState<Quote | null>(null);
  const [pending, setPending] = useState(0);
  const [quoting, setQuoting] = useState(true);
  const [quoteError, setQuoteError] = useState<string | null>(null);
  const [orderError, setOrderError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const yearly = cycle === 'yearly';
  const tier = tierOf(plan, cycle);
  const attempt = useRef<{ signature: string; key: string } | null>(null);
  const revision = useRef(0);
  const planId = plan.id;
  const quoted = quote?.coupon?.code ?? '';
  // The coupon box differs from the quoted code: the shown price is stale until applied.
  const trimmed = normalize(coupon);
  const stale = trimmed !== quoted;
  const couponBad = !!quote?.coupon && !quote.coupon.applied;

  // Every quote request supersedes the earlier ones; only the latest may update the dialog.
  function ask(code: string) {
    const current = ++revision.current;
    return api<{ quote: Quote; pending_orders: number }>('/payments/quote', {
      method: 'POST',
      // Monthly requests keep the exact shape an older backend accepts.
      body: JSON.stringify(
        yearly
          ? { plan_id: planId, coupon_code: code, billing_cycle: 'yearly' }
          : { plan_id: planId, coupon_code: code },
      ),
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
    void ask('');
    return () => {
      revisions.current++;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function apply() {
    if (trimmed && !couponPattern.test(trimmed)) {
      revision.current++;
      setQuoting(false);
      setQuote((q) =>
        q ? { ...q, coupon: { code: trimmed, applied: false, message: '优惠码无效或已失效' } } : q,
      );
      return;
    }
    setQuoting(true);
    void ask(trimmed);
  }

  async function pay() {
    if (!payType || !quote?.purchasable || busy || stale || couponBad) return;
    const code = quoted;
    // One Idempotency-Key per distinct (plan, method, coupon): a retry of the same request
    // replays the same order, a changed choice is a new order.
    const signature = `${planId}|${cycle}|${payType}|${code}`;
    if (attempt.current?.signature !== signature)
      attempt.current = { signature, key: crypto.randomUUID() };
    setBusy(true);
    setOrderError(null);
    setNotice(null);
    try {
      const placed = await api<PlacedOrder>('/payments/orders', {
        method: 'POST',
        headers: { 'Idempotency-Key': attempt.current.key },
        // The amount can only make the server refuse the order when the price moved; the
        // server prices the order itself.
        body: JSON.stringify({
          plan_id: planId,
          pay_type: payType,
          coupon_code: code,
          ...(yearly ? { billing_cycle: 'yearly' } : {}),
          ...(guardAmount ? { expected_amount_cents: quote.amount_cents } : {}),
        }),
      });
      const target = new URL(placed.pay_url);
      if (target.protocol !== 'https:' && target.protocol !== 'http:')
        throw new Error('支付链接无效，请重试。');
      // Whole-page navigation: the gateway's countdown and its redirect back need the full page.
      window.location.assign(target.href);
    } catch (e) {
      setBusy(false);
      if (e instanceof APIError && e.code === 'price_changed') {
        // Nothing was ordered and the gateway is not opened: show the new price first.
        attempt.current = null;
        setNotice('价格已更新，请确认后再支付');
        setQuoting(true);
        void ask(code);
        return;
      }
      if (e instanceof APIError && e.code === 'idempotency_conflict') attempt.current = null;
      setOrderError(message(e));
    }
  }

  const couponProblem = quote?.coupon && !quote.coupon.applied ? quote.coupon.message : null;
  return (
    <Stack gap="md">
      <Text size="sm" c="dimmed">
        {trafficLabel(quote ?? tier ?? plan)} ·{' '}
        {quote?.duration_days ?? tier?.duration_days ?? plan.duration_days} 天
        {quote?.kind === 'renew'
          ? yearly
            ? ` · 续费：在当前到期时间基础上顺延 ${quote.duration_days} 天；月付续成年付时剩余流量并入第一个周期，已是年付则流量仍按原日期刷新`
            : ' · 续费：延长有效期并累加流量（年付订阅只顺延有效期，流量仍每 30 天刷新）'
          : refreshes(quote ?? tier)
            ? ' · 流量每 30 天重置为整月额度，未用完不累计'
            : ''}
      </Text>
      <div>
        <Text size="sm" fw={600} mb={8} id="pay-method-label">
          支付方式
        </Text>
        <div role="radiogroup" aria-labelledby="pay-method-label" className="pay-methods">
          {methods
            .filter((m) => allowed.includes(m.type))
            .map((m) => {
              const on = payType === m.type;
              return (
                <UnstyledButton
                  key={m.type}
                  role="radio"
                  aria-checked={on}
                  aria-label={payTypeLabel[m.type]}
                  className={`pay-method ${on ? 'selected' : ''}`}
                  style={{ ['--pay-color' as string]: m.color }}
                  disabled={busy}
                  onClick={() => setPayType(m.type)}
                >
                  <span className="pay-dot" />
                  <span>
                    <Text size="sm" fw={600}>
                      {payTypeLabel[m.type]}
                    </Text>
                    <Text size="xs" c="dimmed">
                      {m.hint}
                    </Text>
                  </span>
                </UnstyledButton>
              );
            })}
        </div>
      </div>
      <div>
        <Group gap="xs" align="flex-end" wrap="nowrap">
          <TextInput
            label="优惠码（可留空）"
            placeholder="输入优惠码"
            value={coupon}
            maxLength={32}
            disabled={busy}
            onChange={(e) => setCoupon(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault();
                apply();
              }
            }}
            style={{ flex: 1 }}
            autoComplete="off"
            spellCheck={false}
          />
          <Button variant="light" onClick={apply} disabled={busy || quoting || !stale}>
            应用
          </Button>
        </Group>
        {couponProblem ? (
          <Text size="xs" c="red" mt={6} role="alert">
            {couponProblem}
          </Text>
        ) : quote?.coupon?.applied ? (
          <Text size="xs" c="teal" mt={6}>
            优惠码 {quote.coupon.code} 已应用
          </Text>
        ) : null}
      </div>
      {quoteError ? (
        <Alert color="red" title="报价失败" role="alert">
          {quoteError}
        </Alert>
      ) : null}
      {quote ? (
        <Stack gap={6} className="quote-lines">
          {quote.test_price ? (
            <Alert color="yellow" py={6}>
              专属价：本账户按专属价格结算，付款将实际扣款。
            </Alert>
          ) : null}
          {yearly ? (
            <>
              <Group justify="space-between">
                <Text size="sm">原价（12 个月）</Text>
                <Text size="sm" aria-label="原价">
                  {money(quote.original_cents ?? quote.list_price_cents)}
                </Text>
              </Group>
              <Group justify="space-between">
                <Text size="sm" className="quote-saving">
                  年付优惠
                  {quote.cycle_discount_percent ? `（${zhe(quote.cycle_discount_percent)}）` : ''}
                </Text>
                <Text size="sm" className="quote-saving" aria-label="年付优惠">
                  -{money(quote.cycle_saving_cents ?? 0)}
                </Text>
              </Group>
            </>
          ) : (
            <Group justify="space-between">
              <Text size="sm">标价</Text>
              <Text size="sm">{money(quote.list_price_cents)}</Text>
            </Group>
          )}
          {quote.test_price ? (
            <Group justify="space-between">
              <Text size="sm">专属价</Text>
              <Text size="sm">{money(quote.base_cents)}</Text>
            </Group>
          ) : null}
          {quote.discount_cents > 0 ? (
            <Group justify="space-between">
              <Text size="sm">优惠</Text>
              <Text size="sm" c="teal">
                -{money(quote.discount_cents)}
              </Text>
            </Group>
          ) : null}
          <Group justify="space-between">
            <Text size="sm" fw={600}>
              应付
            </Text>
            <Text size="xl" fw={650} aria-label="应付金额">
              {money(quote.amount_cents)}
            </Text>
          </Group>
          {!quote.purchasable ? (
            <Alert color="orange" role="alert">
              {quote.message || '当前无法购买此套餐。'}
            </Alert>
          ) : null}
        </Stack>
      ) : null}
      {notice ? (
        <Alert color="orange" role="status" data-testid="price-updated">
          {notice}
        </Alert>
      ) : null}
      {pending > 0 ? (
        <Alert color="yellow">
          你还有 {pending} 笔未完成的订单，请勿重复付款。已关闭支付窗口的订单会在几分钟内自动失效。
        </Alert>
      ) : null}
      <Text size="xs" c="dimmed">
        点击“去支付”后将跳转到支付页面，请在 2 分钟内完成付款，付款结果以订单状态为准。
      </Text>
      {orderError ? (
        <Alert color="red" title="下单未完成" role="alert">
          {orderError}
        </Alert>
      ) : null}
      <Button
        size="md"
        loading={busy}
        disabled={!payType || !quote?.purchasable || stale || couponBad || quoting}
        onClick={() => void pay()}
      >
        去支付
      </Button>
      {!payType ? (
        <Text size="xs" c="dimmed" ta="center">
          请先选择支付方式
        </Text>
      ) : stale ? (
        <Text size="xs" c="dimmed" ta="center">
          请先点击“应用”确认优惠码，或清空优惠码
        </Text>
      ) : couponBad ? (
        <Text size="xs" c="dimmed" ta="center">
          优惠码不可用：请更换优惠码，或清空后继续
        </Text>
      ) : null}
    </Stack>
  );
}
