import { Suspense } from 'react';
import { PlansView } from '@/components/plans-view';
export default function PlansPage() {
  return (
    <Suspense fallback={null}>
      <PlansView />
    </Suspense>
  );
}
