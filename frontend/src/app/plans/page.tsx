import { Suspense } from 'react';
import { PlansView } from '@/components/plans-view';
import { indexableMetadata } from '@/lib/server/seo';

// The canonical address has no ?cycle=, ?plan= or ?pay= (handoff_seo.md D11).
export function generateMetadata() {
  return indexableMetadata('/plans', { title: '套餐与订阅' });
}

export default function PlansPage() {
  return (
    <Suspense fallback={null}>
      <PlansView />
    </Suspense>
  );
}
