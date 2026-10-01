import { RequireAuth } from '@/components/require-auth';
import { OrdersView } from '@/components/orders-view';
export default function OrdersPage() {
  return (
    <RequireAuth>
      <OrdersView />
    </RequireAuth>
  );
}
