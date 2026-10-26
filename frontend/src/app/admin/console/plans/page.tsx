import { Suspense } from 'react';
import { ConsolePlans } from '@/components/console/console-plans';

export default function Page() {
  return (
    <Suspense fallback={null}>
      <ConsolePlans />
    </Suspense>
  );
}
