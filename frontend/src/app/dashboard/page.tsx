import { RequireAuth } from '@/components/require-auth';
import { DashboardView } from '@/components/dashboard-view';
export default function DashboardPage() {
  return (
    <RequireAuth>
      <DashboardView />
    </RequireAuth>
  );
}
