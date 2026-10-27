'use client';
import { useEffect, useState } from 'react';
import Link from 'next/link';
import { IconArrowRight, IconCheck } from '@tabler/icons-react';
import { api } from '@/lib/api';
import { previewPlans, yearlySavingPercent } from '@/lib/home';
import { tierOf, trafficLabel } from '@/lib/shop';
import { money, type Plan, type PlansResponse } from '@/lib/types';

type State =
  | { kind: 'loading' }
  | { kind: 'hidden' }
  | { kind: 'ready'; plans: Plan[]; saving: number | null };

// Live plan preview (handoff_ui.md 8.2, 8.3): the plan page's own data, monthly terms, at most three
// plans. Skeletons while loading; on failure or with nothing on sale the cards disappear without
// a message and only the link to the plan page stays.
export function PlanPreview() {
  const [state, setState] = useState<State>({ kind: 'loading' });
  useEffect(() => {
    let active = true;
    api<PlansResponse>('/plans', {}, false)
      .then((response) => {
        if (!active) return;
        const plans = previewPlans(response.plans);
        setState(
          plans.length
            ? { kind: 'ready', plans, saving: yearlySavingPercent(response) }
            : { kind: 'hidden' },
        );
      })
      .catch(() => {
        if (active) setState({ kind: 'hidden' });
      });
    return () => {
      active = false;
    };
  }, []);
  return (
    <>
      {state.kind === 'loading' ? (
        <div className="home-plans" aria-hidden="true">
          {[0, 1, 2].map((i) => (
            <div key={i} className="home-plan home-plan-skeleton" data-testid="plan-skeleton" />
          ))}
        </div>
      ) : null}
      {state.kind === 'ready' ? (
        <>
          {state.saving ? (
            <p className="home-plans-promo" data-testid="home-yearly-promo">
              <span>年付更划算</span>年付最高省 {state.saving}%
            </p>
          ) : null}
          <div
            className={`home-plans count-${state.plans.length}`}
            data-testid="home-plans"
            role="list"
          >
            {state.plans.map((plan) => (
              <PlanCard key={plan.id} plan={plan} />
            ))}
          </div>
        </>
      ) : null}
      <div className="home-plans-more">
        <Link href="/plans" className="home-btn home-btn-ghost">
          查看全部套餐
          <IconArrowRight size={17} />
        </Link>
      </div>
    </>
  );
}

function PlanCard({ plan }: { plan: Plan }) {
  const tier = tierOf(plan, 'monthly')!;
  return (
    <article
      className={`home-plan ${plan.featured ? 'featured' : ''}`}
      data-testid="home-plan"
      data-plan={plan.id}
      role="listitem"
      aria-label={plan.name}
    >
      <div className="home-plan-head">
        <h3>{plan.name}</h3>
        {plan.featured ? <span className="home-plan-tag">推荐</span> : null}
      </div>
      {plan.description ? <p className="home-plan-desc">{plan.description}</p> : null}
      <div className="home-plan-price">
        <strong data-field="price">{money(tier.price_cents)}</strong>
        <span>/ {tier.duration_days} 天</span>
      </div>
      <ul>
        <li>
          <IconCheck size={16} aria-hidden />
          <span data-field="traffic">{trafficLabel(tier)} 计费流量</span>
        </li>
        <li>
          <IconCheck size={16} aria-hidden />
          <span data-field="devices">最多 {plan.max_devices} 台设备同时在线</span>
        </li>
        <li>
          <IconCheck size={16} aria-hidden />
          <span>{plan.allow_dedicated ? '普通直连 + 专线权限' : '普通直连线路权限'}</span>
        </li>
      </ul>
      <Link
        href="/plans"
        className={`home-btn ${plan.featured ? 'home-btn-primary' : 'home-btn-ghost'} home-btn-block`}
        aria-label={`选择 ${plan.name}`}
      >
        选择套餐
      </Link>
    </article>
  );
}
