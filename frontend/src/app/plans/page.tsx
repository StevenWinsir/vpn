import { Suspense } from 'react';
import { JsonLd } from '@/components/json-ld';
import { PlansView } from '@/components/plans-view';
import { indexableMetadata, siteOrigin } from '@/lib/server/seo';
import { loadShop } from '@/lib/server/shop';
import { breadcrumbs, products } from '@/lib/structured-data';

// The canonical address has no ?cycle=, ?plan= or ?pay= (handoff_seo.md D11).
export function generateMetadata() {
  return indexableMetadata('/plans');
}

// The cards start from the server's one-minute copy of the plans and the public capabilities, so
// prices are in the first HTML (handoff_seo.md D8); without a copy the page loads them itself.
// The products are described only when their prices are on the page (6.2).
export default async function PlansPage() {
  const initial = await loadShop();
  const origin = siteOrigin();
  return (
    <>
      {origin ? (
        <JsonLd
          data={[
            breadcrumbs(origin, [{ name: '套餐与订阅', path: '/plans' }]),
            ...(initial ? products(origin, initial.plans) : []),
          ]}
        />
      ) : null}
      <Suspense fallback={null}>
        <PlansView initial={initial} />
      </Suspense>
    </>
  );
}
