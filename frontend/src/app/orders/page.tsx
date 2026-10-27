import { RequireAuth } from '@/components/require-auth';
import { OrdersView } from '@/components/orders-view';
export const metadata = { title: '订单记录' };

export default function OrdersPage() {
  return (
    <RequireAuth>
      <OrdersView />
    </RequireAuth>
  );
}
