import { Suspense } from 'react';
import { ConsoleCoupons } from '@/components/console/console-coupons';

export default function Page() {
  return (
    <Suspense fallback={null}>
      <ConsoleCoupons />
    </Suspense>
  );
}
