import { RequireAuth } from '@/components/require-auth';
import { OrdersView } from '@/components/orders-view';
import { closedRobots } from '@/lib/seo';
export const metadata = { title: '订单记录', robots: closedRobots('/orders') };

export default function OrdersPage() {
  return (
    <RequireAuth>
      <OrdersView />
    </RequireAuth>
  );
}
