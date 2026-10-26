'use client';
import { useCallback, useEffect, useRef, useState } from 'react';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
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
import { tierOf, yearlyPromo, zhe } from '@/lib/shop';
import {
  gib,
  money,
  type BillingCycle,
  type Dashboard,
  type PayType,
  type Plan,
  type PlansResponse,
  type Meta,
  type Order,
} from '@/lib/types';
import { useAuth } from './providers';
import { PurchaseModal } from './purchase-modal';

export function PlansView() {
  const { user } = useAuth();
  const router = useRouter();
  const pathname = usePathname();
  const [plans, setPlans] = useState<Plan[]>([]);
  const [shop, setShop] = useState<{ maxSavingPercent: number; yearlyAware: boolean }>({
    maxSavingPercent: 0,
    yearlyAware: false,
  });
  const [meta, setMeta] = useState<Meta | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // The confirmation belongs to the account that opened it: when the session ends (or another
  // account signs in) it disappears instead of submitting for whoever is current.
  const [choice, setChoice] = useState<{ plan: Plan; userId: string; cycle: BillingCycle } | null>(
    null,
  );
  const selected = choice && user && choice.userId === user.id ? choice.plan : null;
  const selectedCycle: BillingCycle = choice?.cycle ?? 'monthly';
  const [subscription, setSubscription] = useState<Dashboard['subscription']>(null);
  const [dismissed, setDismissed] = useState(false);
  const params = useSearchParams();
  const presetPlan = params.get('plan');
  const presetPay = params.get('pay');
  const payOn = meta?.payment_enabled === true;
  const [busy, setBusy] = useState(false);
  const [purchaseError, setPurchaseError] = useState<string | null>(null);
  const requestKey = useRef<string | null>(null);

  // The cycle lives in the address (?cycle=yearly) so refresh, back and shared links keep it.
  // Without any yearly tier there is nothing to switch to, whatever the address says.
  const promo = yearlyPromo(plans, shop.maxSavingPercent);
  const hasYearly = promo !== null;
  const cycle: BillingCycle = hasYearly && params.get('cycle') === 'yearly' ? 'yearly' : 'monthly';
  const yearly = cycle === 'yearly';
  function setCycle(next: BillingCycle) {
    const query = new URLSearchParams(params.toString());
    if (next === 'yearly') query.set('cycle', 'yearly');
    else query.delete('cycle');
    const text = query.toString();
    router.replace(text ? `${pathname}?${text}` : pathname, { scroll: false });
  }

  const accept = useCallback((p: PlansResponse) => {
    setPlans(p.plans);
    // The order guard (expected_amount_cents) is only sent to a backend that knows the yearly
    // fields; an older one rejects unknown fields.
    setShop({
      maxSavingPercent: p.max_yearly_saving_percent ?? 0,
      yearlyAware: p.plans.some((x) => x.yearly !== undefined),
    });
  }, []);
  const load = useCallback(async () => {
    try {
      const [p, m] = await Promise.all([api<PlansResponse>('/plans'), api<Meta>('/meta')]);
      accept(p);
      setMeta(m);
      setError(null);
    } catch (e) {
      setError(message(e));
    } finally {
      setLoading(false);
    }
  }, [accept]);
  useEffect(() => {
    let active = true;
    void Promise.all([api<PlansResponse>('/plans'), api<Meta>('/meta')])
      .then(([p, m]) => {
        if (active) {
          accept(p);
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
  }, [accept]);
  useEffect(() => {
    // The current plan decides the button labels; the server re-checks every purchase.
    if (!user || !payOn) return;
    let active = true;
    void api<Dashboard>('/me/dashboard')
      .then((d) => {
        if (active) setSubscription(d.subscription);
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, [user, payOn]);
  // "Buy again" from an expired order: the link preselects the plan, cycle and method until the
  // dialog is closed or another plan is chosen.
  const preset =
    !dismissed && !selected && payOn && user
      ? plans.find((p) => p.id === presetPlan && (!yearly || p.yearly))
      : undefined;
  const presetType: PayType | null =
    preset && (presetPay === 'wxpay' || presetPay === 'alipay') ? presetPay : null;
  const [loadedAt] = useState(() => Date.now());
  const state = (plan: Plan) => {
    if (
      !subscription ||
      subscription.is_test ||
      new Date(subscription.expires_at).getTime() <= loadedAt
    )
      return 'new';
    return subscription.plan_id === plan.id ? 'renew' : 'blocked';
  };
  const modalPlan = payOn ? (selected ?? preset ?? null) : null;
  const modalCycle: BillingCycle = selected ? selectedCycle : cycle;
  function choose(plan: Plan) {
    if (!user) {
      router.push('/login');
      return;
    }
    setDismissed(true);
    requestKey.current = crypto.randomUUID();
    setPurchaseError(null);
    setChoice({ plan, userId: user.id, cycle });
  }
  async function purchase() {
    if (!selected || !requestKey.current || busy) return;
    setBusy(true);
    setPurchaseError(null);
    try {
      await api<{ order: Order; replayed: boolean }>('/orders/test-purchase', {
        method: 'POST',
        headers: { 'Idempotency-Key': requestKey.current },
        // Monthly requests keep the exact shape an older backend accepts.
        body: JSON.stringify(
          selectedCycle === 'yearly'
            ? { plan_id: selected.id, billing_cycle: 'yearly' }
            : { plan_id: selected.id },
        ),
      });
      setChoice(null);
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
  const selectedTier = selected ? tierOf(selected, selectedCycle) : null;
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
      {meta && payOn ? (
        <Alert mb={28} color="blue" title="支持微信支付与支付宝支付">
          选择套餐后可使用微信或支付宝扫码付款，并可输入优惠码。付款成功后套餐自动开通或续期。
        </Alert>
      ) : meta ? (
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
      {hasYearly && !loading ? (
        <>
          <CycleSwitch cycle={cycle} badge={promo.badge} onChange={setCycle} />
          {!yearly && promo.badge ? (
            <button
              type="button"
              className="cycle-banner"
              data-testid="yearly-banner"
              onClick={() => setCycle('yearly')}
            >
              切换年付，{promo.headline}，最多立省 {money(promo.maxSavingCents)}
            </button>
          ) : null}
        </>
      ) : null}
      <SimpleGrid cols={{ base: 1, md: 3 }} spacing={22}>
        {loading
          ? [1, 2, 3].map((i) => <Skeleton key={i} height={410} radius="lg" />)
          : plans.map((plan) => {
              const tier = tierOf(plan, cycle);
              const st = state(plan);
              const label = (verb: string) => `${verb} ${plan.name}${yearly ? '（年付）' : ''}`;
              return (
                <Paper
                  key={plan.id}
                  withBorder
                  p={27}
                  radius="lg"
                  className={`plan-card ${plan.featured ? 'featured' : ''}`}
                  data-testid="plan-card"
                  data-plan={plan.id}
                >
                  {tier && yearly && tier.discount_percent < 100 ? (
                    <div className="plan-ribbon" data-testid="plan-ribbon">
                      年付 {zhe(tier.discount_percent)}
                    </div>
                  ) : null}
                  <Group justify="space-between" mb={16} wrap="nowrap" pr={yearly ? 40 : 0}>
                    <Title order={2} size={19}>
                      {plan.name}
                    </Title>
                    {plan.allow_dedicated ? (
                      <Badge variant="light" size="sm" style={{ flexShrink: 0 }}>
                        含专线权益
                      </Badge>
                    ) : null}
                  </Group>
                  <Text size="xs" c="dimmed" mih={38}>
                    {plan.description}
                  </Text>
                  {tier ? (
                    yearly ? (
                      <div className="plan-price-row" style={{ marginTop: 24, marginBottom: 6 }}>
                        <Text
                          className="plan-price-main"
                          fz={36}
                          fw={650}
                          style={{ letterSpacing: '-1.5px' }}
                          aria-label="年付价格"
                        >
                          {money(tier.price_cents)}
                        </Text>
                        <Text c="dimmed" size="xs">
                          / 年
                        </Text>
                        {tier.original_cents > tier.price_cents ? (
                          <span className="plan-original" aria-label="原价">
                            {money(tier.original_cents)}
                          </span>
                        ) : null}
                      </div>
                    ) : (
                      <Group gap={5} align="baseline" mt={24} mb={6}>
                        <Text fz={36} fw={650} style={{ letterSpacing: '-1.5px' }}>
                          {money(tier.price_cents)}
                        </Text>
                        <Text c="dimmed" size="xs">
                          / {tier.duration_days} 天
                        </Text>
                      </Group>
                    )
                  ) : (
                    <Text
                      fw={650}
                      size="lg"
                      mt={24}
                      mb={6}
                      mih={44}
                      data-testid="no-yearly"
                      style={{ display: 'flex', alignItems: 'center' }}
                    >
                      该套餐暂不提供年付
                    </Text>
                  )}
                  {tier && yearly ? (
                    <Group gap="xs" mb={4}>
                      {tier.saving_cents > 0 ? (
                        <span className="plan-saving" aria-label="立省金额">
                          立省 {money(tier.saving_cents)}
                        </span>
                      ) : null}
                      <Text size="xs" c="dimmed" aria-label="折合月价">
                        折合 {money(tier.monthly_equivalent_cents)} / 月
                      </Text>
                    </Group>
                  ) : null}
                  <Text size="xs" c="dimmed">
                    {payOn ? '人民币 · 以结算报价为准' : '测试定价 · 人民币'}
                  </Text>
                  <Divider my={24} />
                  {tier ? (
                    <Stack gap={15} mb={30}>
                      <Feature text={`${gib(tier.traffic_bytes)} GiB 计费流量`} />
                      <Feature text={`最多 ${plan.max_devices} 台设备同时在线`} />
                      <Feature
                        text={plan.allow_dedicated ? '普通直连 + 专线权限' : '普通直连线路权限'}
                      />
                      <Feature text={`有效期 ${tier.duration_days} 天`} />
                    </Stack>
                  ) : (
                    <Text size="sm" c="dimmed" mb={30} mih={110}>
                      月付档位仍可购买，切换到月付查看流量与有效期。
                    </Text>
                  )}
                  {tier ? (
                    <Button
                      fullWidth
                      mt="auto"
                      variant={plan.featured ? 'filled' : 'light'}
                      disabled={
                        payOn ? user !== null && st === 'blocked' : !meta?.test_purchase_enabled
                      }
                      onClick={() => choose(plan)}
                      rightSection={<IconArrowRight size={16} />}
                      aria-label={
                        payOn ? label(st === 'renew' ? '续费' : '购买') : label('测试购买')
                      }
                    >
                      {payOn
                        ? !user
                          ? '登录后购买'
                          : st === 'renew'
                            ? yearly
                              ? '续费为年付'
                              : '续费'
                            : yearly
                              ? '购买年付'
                              : '立即购买'
                        : user
                          ? yearly
                            ? '测试购买年付'
                            : '测试购买'
                          : '登录后测试购买'}
                    </Button>
                  ) : (
                    <Button
                      fullWidth
                      mt="auto"
                      variant="default"
                      onClick={() => setCycle('monthly')}
                    >
                      查看月付
                    </Button>
                  )}
                  {tier && payOn && user && st === 'blocked' ? (
                    <Text size="xs" c="dimmed" ta="center" mt={8}>
                      当前套餐有效期内不能更换套餐，到期后可购买。
                    </Text>
                  ) : null}
                </Paper>
              );
            })}
      </SimpleGrid>
      {!loading && !error && plans.length === 0 ? (
        <div className="empty-state">暂时没有上架的套餐。</div>
      ) : null}
      <Paper withBorder radius="lg" p={28} mt={28}>
        <Title order={2} size={16} mb={16}>
          流量与续购规则
        </Title>
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing={30}>
          <div>
            <Text size="sm" fw={600} mb={7}>
              流量按线路倍率扣减
            </Text>
            <Text c="dimmed" size="xs" lh={1.9}>
              普通线路按 0.5 倍、专线按 1 倍计费。例如实际使用 10 GiB，分别计费 5 GiB 和 10
              GiB。具体倍率以节点配置为准。年付套餐的全年流量在开通时一次性发放。
            </Text>
          </div>
          <div>
            <Text size="sm" fw={600} mb={7}>
              续费与更换套餐
            </Text>
            <Text c="dimmed" size="xs" lh={1.9}>
              同一套餐的月付与年付可以互相续费：有效期在当前到期时间上顺延，流量累加，已用流量不会清零。有效期内不能更换为其他套餐，到期后可重新选择。
            </Text>
          </div>
        </SimpleGrid>
      </Paper>
      <PurchaseModal
        plan={modalPlan}
        cycle={modalCycle}
        renew={modalPlan !== null && state(modalPlan) === 'renew'}
        guardAmount={shop.yearlyAware}
        initialPayType={presetType}
        allowed={meta?.payment_methods ?? []}
        onClose={() => {
          setDismissed(true);
          setChoice(null);
        }}
      />
      <Modal
        opened={!payOn && selected !== null}
        onClose={() => {
          if (!busy) setChoice(null);
        }}
        title={selectedCycle === 'yearly' ? '确认测试购买（年付）' : '确认测试购买'}
        centered
        radius="lg"
        closeOnClickOutside={!busy}
        closeOnEscape={!busy}
        withCloseButton={!busy}
      >
        {selected && selectedTier ? (
          <Stack gap="lg">
            <Alert color="yellow">这是一笔模拟订单。不会扣款，也不会建立 VPN 连接。</Alert>
            <div>
              <Text size="lg" fw={650}>
                {selected.name}
                {selectedCycle === 'yearly' ? '（年付）' : ''}
              </Text>
              <Text size="sm" c="dimmed" mt={5}>
                {gib(selectedTier.traffic_bytes)} GiB · {selectedTier.duration_days} 天
              </Text>
            </div>
            <Group justify="space-between">
              <Text size="sm">{selectedCycle === 'yearly' ? '演示金额（年付）' : '演示金额'}</Text>
              <Text size="xl" fw={650} aria-label="演示金额">
                {money(selectedTier.price_cents)}
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

// A two-option radio group; arrow keys move the selection like native radios.
function CycleSwitch({
  cycle,
  badge,
  onChange,
}: {
  cycle: BillingCycle;
  badge: string | null;
  onChange: (cycle: BillingCycle) => void;
}) {
  const refs = useRef<Record<BillingCycle, HTMLButtonElement | null>>({
    monthly: null,
    yearly: null,
  });
  const options: { value: BillingCycle; text: string }[] = [
    { value: 'monthly', text: '月付' },
    { value: 'yearly', text: '年付' },
  ];
  function move(next: BillingCycle) {
    onChange(next);
    refs.current[next]?.focus();
  }
  return (
    <div className="cycle-switch">
      <div className="cycle-seg" role="radiogroup" aria-label="付费周期" data-testid="cycle-switch">
        {options.map((o) => (
          <button
            key={o.value}
            type="button"
            role="radio"
            ref={(el) => {
              refs.current[o.value] = el;
            }}
            aria-checked={cycle === o.value}
            tabIndex={cycle === o.value ? 0 : -1}
            onClick={() => onChange(o.value)}
            onKeyDown={(e) => {
              if (e.key === 'ArrowRight' || e.key === 'ArrowDown') {
                e.preventDefault();
                move(o.value === 'monthly' ? 'yearly' : 'monthly');
              } else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') {
                e.preventDefault();
                move(o.value === 'yearly' ? 'monthly' : 'yearly');
              }
            }}
          >
            {o.text}
            {o.value === 'yearly' && badge ? (
              <span className="cycle-badge" data-testid="cycle-badge">
                {badge}
              </span>
            ) : null}
          </button>
        ))}
      </div>
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
