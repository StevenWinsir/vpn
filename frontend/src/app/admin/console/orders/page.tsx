import { Suspense } from 'react';
import { ConsoleOrders } from '@/components/console/console-orders';

export default function Page() {
  return (
    <Suspense fallback={null}>
      <ConsoleOrders />
    </Suspense>
  );
}
