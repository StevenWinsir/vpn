import { Suspense } from 'react';
import { RequireAuth } from '@/components/require-auth';
import { PayResultView } from '@/components/pay-result-view';
export default function PayResultPage() {
  return (
    <RequireAuth>
      <Suspense fallback={null}>
        <PayResultView />
      </Suspense>
    </RequireAuth>
  );
}
