import { Suspense } from 'react';
import { PlansView } from '@/components/plans-view';
import { indexableMetadata } from '@/lib/server/seo';
import { loadShop } from '@/lib/server/shop';

// The canonical address has no ?cycle=, ?plan= or ?pay= (handoff_seo.md D11).
export function generateMetadata() {
  return indexableMetadata('/plans');
}

// The cards start from the server's one-minute copy of the plans and the public capabilities, so
// prices are in the first HTML (handoff_seo.md D8); without a copy the page loads them itself.
export default async function PlansPage() {
  const initial = await loadShop();
  return (
    <Suspense fallback={null}>
      <PlansView initial={initial} />
    </Suspense>
  );
}
