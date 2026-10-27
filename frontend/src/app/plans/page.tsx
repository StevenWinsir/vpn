import { Suspense } from 'react';
import { PlansView } from '@/components/plans-view';
export const metadata = { title: '套餐与订阅' };

export default function PlansPage() {
  return (
    <Suspense fallback={null}>
      <PlansView />
    </Suspense>
  );
}
