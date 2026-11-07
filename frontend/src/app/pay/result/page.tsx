import { Suspense } from 'react';
import { RequireAuth } from '@/components/require-auth';
import { PayResultView } from '@/components/pay-result-view';
import { closedRobots } from '@/lib/seo';
export const metadata = { title: '支付结果', robots: closedRobots('/pay/result') };

export default function PayResultPage() {
  return (
    <RequireAuth>
      <Suspense fallback={null}>
        <PayResultView />
      </Suspense>
    </RequireAuth>
  );
}
