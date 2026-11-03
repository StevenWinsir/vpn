'use client';
import { useCallback, useEffect, useRef, useState } from 'react';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import {
  Alert,
  Badge,
  Button,
  Divider,
  Group,
  Modal,
  Paper,
  SimpleGrid,
  Skeleton,
  Stack,
  Text,
  Title,
} from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { IconArrowRight, IconArrowUpRight, IconCheck } from '@tabler/icons-react';
import { api, message } from '@/lib/api';
import { refreshes, tierOf, trafficLabel, yearlyPromo, zhe } from '@/lib/shop';
import {
  money,
  type BillingCycle,
  type Dashboard,
  type PayType,
  type Plan,
  type PlansResponse,
  type Meta,
  type Order,
  type UpgradeOption,
  type UpgradeOptions,
} from '@/lib/types';
import { useAuth } from './providers';
import { PurchaseModal } from './purchase-modal';
import { UpgradeModal } from './upgrade-modal';

// What a card offers the signed-in account. closed: another plan during the validity period
// that cannot be upgraded to (a downgrade or a lateral change); it waits for the expiry.
type CardState = 'new' | 'renew' | 'upgrade' | 'closed';

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
  // The server's relation of every plan to the current subscription (handoff_plans.md 11.1),
  // kept with the account it was read for.
  const [options, setOptions] = useState<{ userId: string; data: UpgradeOptions } | null>(null);
  const [upgradeChoice, setUpgradeChoice] = useState<{ plan: Plan; userId: string } | null>(null);
  const upgrading =
    upgradeChoice && user && upgradeChoice.userId === user.id ? upgradeChoice.plan : null;
  const [dismissed, setDismissed] = useState(false);
  const params = useSearchParams();
  const presetPlan = params.get('plan');
  const presetPay = params.get('pay');
  const payOn = meta?.payment_enabled === true;
  // Only a backend that announces upgrades is asked for them; an older one keeps the rule
  // "other plans after the expiry" (handoff_plans.md 14.1).
  const upgradeOn = payOn && meta?.plan_upgrade_supported === true;
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
  useEffect(() => {
    if (!user || !upgradeOn) return;
    let active = true;
    const userId = user.id;
    void api<UpgradeOptions>('/payments/upgrade-options')
      .then((data) => {
        if (active) setOptions({ userId, data });
      })
      // Without the options the cards fall back to the local rule: nothing can be upgraded.
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, [user, upgradeOn]);
  const upgradeOptions = options && user && options.userId === user.id ? options.data : null;
  const [loadedAt] = useState(() => Date.now());
  // The local rule, for a backend without upgrades (or before its answer arrives): the current
  // plan renews, the others wait for the expiry.
  const localState = (plan: Plan): CardState => {
    if (
      !subscription ||
      subscription.is_test ||
      new Date(subscription.expires_at).getTime() <= loadedAt
    )
      return 'new';
    return subscription.plan_id === plan.id ? 'renew' : 'closed';
  };
  const optionOf = (plan: Plan): UpgradeOption | undefined =>
    upgradeOptions?.plans.find((o) => o.plan_id === plan.id);
  const state = (plan: Plan): CardState => {
    const option = optionOf(plan);
    if (!option) return localState(plan);
    switch (option.relation) {
      case 'current':
        return 'renew';
      case 'upgrade':
        return 'upgrade';
      case 'downgrade':
      case 'lateral':
        return 'closed';
      default:
        return 'new';
    }
  };
  // "Buy again" from an expired order: the link preselects the plan, cycle and method until the
  // dialog is closed or another plan is chosen. A plan that cannot be bought now is not opened.
  const preset =
    !dismissed && !selected && !upgrading && payOn && user
      ? plans.find(
          (p) =>
            p.id === presetPlan &&
            (!yearly || p.yearly) &&
            (state(p) === 'new' || state(p) === 'renew'),
        )
      : undefined;
  const presetType: PayType | null =
    preset && (presetPay === 'wxpay' || presetPay === 'alipay') ? presetPay : null;
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
  function chooseUpgrade(plan: Plan) {
    if (!user) return;
    setDismissed(true);
    setChoice(null);
    setUpgradeChoice({ plan, userId: user.id });
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
        title: '测试套餐已开通', // copy-guard: dev-only
        message: '未发生扣款。',
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
        清晰的额度，明确的权益。月付灵活，年付更划算，付款后立即开通。
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
      {meta && !payOn ? (
        meta.test_purchase_enabled ? (
          // copy-guard: dev-only (test purchase never runs in production)
          <Alert mb={28} color="yellow" title="当前为测试环境，购买不会扣款" />
        ) : (
          <Alert mb={28} color="gray" title="在线购买暂未开放">
            请稍后再来，或联系客服了解开通方式。
          </Alert>
        )
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
              const option = optionOf(plan);
              const label = (verb: string) => `${verb} ${plan.name}${yearly ? '（年付）' : ''}`;
              const signedIn = payOn && user !== null;
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
                    {payOn ? '人民币 · 以结算报价为准' : '人民币'}
                  </Text>
                  <Divider my={24} />
                  {tier ? (
                    <Stack gap={15} mb={30}>
                      <Feature text={`${trafficLabel(tier)} 计费流量`} />
                      {refreshes(tier) ? <Feature text="周期结束自动重置，余量不累计" /> : null}
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
                  {tier && signedIn && st === 'upgrade' ? (
                    <Button
                      fullWidth
                      mt="auto"
                      variant={plan.featured ? 'filled' : 'light'}
                      disabled={!option?.purchasable}
                      onClick={() => chooseUpgrade(plan)}
                      rightSection={<IconArrowUpRight size={16} />}
                      aria-label={`升级到 ${plan.name}`}
                      data-testid="upgrade-button"
                    >
                      升级到 {plan.name}
                    </Button>
                  ) : tier && signedIn && st === 'closed' ? (
                    // The accessible name stays the purchase's: what is closed is buying it now.
                    <Button
                      fullWidth
                      mt="auto"
                      variant="default"
                      disabled
                      aria-label={label('购买')}
                      rightSection={<IconArrowRight size={16} />}
                    >
                      到期后可购买
                    </Button>
                  ) : tier ? (
                    <Button
                      fullWidth
                      mt="auto"
                      variant={plan.featured ? 'filled' : 'light'}
                      disabled={!payOn && !meta?.test_purchase_enabled}
                      onClick={() => choose(plan)}
                      rightSection={<IconArrowRight size={16} />}
                      aria-label={
                        payOn ? label(st === 'renew' ? '续费' : '购买') : label('测试购买') // copy-guard: dev-only
                      }
                    >
                      {
                        payOn
                          ? !user
                            ? '登录后购买'
                            : st === 'renew'
                              ? yearly
                                ? '续费为年付'
                                : '续费'
                              : yearly
                                ? '购买年付'
                                : '立即购买'
                          : !meta?.test_purchase_enabled
                            ? '暂未开放'
                            : user // copy-guard: dev-only (test purchase never runs in production)
                              ? yearly
                                ? '测试购买年付' // copy-guard: dev-only
                                : '测试购买' // copy-guard: dev-only
                              : '登录后测试购买' /* copy-guard: dev-only */
                      }
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
                  {tier && signedIn && st === 'upgrade' ? (
                    <Stack gap={4} mt={8} data-testid="upgrade-hint">
                      <Text size="xs" c="dimmed" ta="center">
                        {option?.purchasable
                          ? option.amount_cents
                            ? `补差价约 ${money(option.amount_cents)}，到期日不变`
                            : '补差价升级，到期日不变'
                          : option?.message || '暂时无法升级到此套餐'}
                      </Text>
                      {yearly ? (
                        <Text size="xs" c="dimmed" ta="center">
                          升级按你当前的付费方式补差价；如需年付，可在升级后续费为年付。
                        </Text>
                      ) : null}
                    </Stack>
                  ) : null}
                  {tier && signedIn && st === 'closed' ? (
                    <Text size="xs" c="dimmed" ta="center" mt={8} data-testid="closed-hint">
                      {option?.message || '当前套餐有效期内不能更换，到期后可购买。'}
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
              计费流量 = 实际上传与下载流量 ×
              线路倍率，不同线路的倍率可在节点列表中查看。年付套餐从开通起每 30
              天把流量重置为一个月的额度，上个周期未用完的部分不累计；周期内用完需等到下次重置。
            </Text>
          </div>
          <div>
            <Text size="sm" fw={600} mb={7}>
              续费与更换套餐
            </Text>
            <Text c="dimmed" size="xs" lh={1.9} data-testid="change-rules">
              同一套餐的月付与年付可以互相续费，有效期在当前到期时间上顺延。月付续月付时流量累加；月付续成年付时，剩余流量并入第一个
              30 天周期，之后每 30 天刷新；已是年付时再续费只顺延有效期，流量仍按原日期每 30
              天刷新。
              {meta?.plan_upgrade_supported
                ? '有效期内可以升级到更高的套餐：按剩余天数补差价，已付金额按剩余时间抵扣，到期日不变，剩余流量保留；有效期内不支持降级，降级不退还差价。到期后可选择任意套餐。'
                : '有效期内不能更换为其他套餐，到期后可重新选择。'}
            </Text>
          </div>
        </SimpleGrid>
      </Paper>
      <UpgradeModal
        plan={payOn ? upgrading : null}
        allowed={meta?.payment_methods ?? []}
        yearlyView={yearly}
        onClose={() => setUpgradeChoice(null)}
      />
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
        // copy-guard: dev-only (this dialog exists only while test purchase is enabled)
        title={selectedCycle === 'yearly' ? '确认测试购买（年付）' : '确认测试购买'}
        centered
        radius="lg"
        closeOnClickOutside={!busy}
        closeOnEscape={!busy}
        withCloseButton={!busy}
      >
        {selected && selectedTier ? (
          <Stack gap="lg">
            {/* copy-guard-next-line: dev-only (test purchase dialog) */}
            <Alert color="yellow">当前为测试环境，购买不会扣款。</Alert>
            <div>
              <Text size="lg" fw={650}>
                {selected.name}
                {selectedCycle === 'yearly' ? '（年付）' : ''}
              </Text>
              <Text size="sm" c="dimmed" mt={5}>
                {trafficLabel(selectedTier)} · {selectedTier.duration_days} 天
              </Text>
            </div>
            <Group justify="space-between">
              <Text size="sm">{selectedCycle === 'yearly' ? '金额（年付）' : '金额'}</Text>
              {/* copy-guard: dev-only (accessible name kept for the end-to-end tests) */}
              <Text size="xl" fw={650} aria-label="演示金额">
                {money(selectedTier.price_cents)}
              </Text>
            </Group>
            {purchaseError ? (
              <Alert color="red" title="开通未完成" role="alert">
                {purchaseError}
              </Alert>
            ) : null}
            <Button size="md" loading={busy} onClick={() => void purchase()}>
              {/* copy-guard-next-line: dev-only */}
              确认模拟开通
            </Button>
          </Stack>
        ) : null}
      </Modal>
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
