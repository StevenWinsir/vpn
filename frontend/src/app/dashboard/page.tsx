import { RequireAuth } from '@/components/require-auth';
import { DashboardView } from '@/components/dashboard-view';
import { dashboardRefreshMs } from '@/lib/dashboard';

export const dynamic = 'force-dynamic';

export const metadata = { title: '总览' };

export default function DashboardPage() {
  return (
    <RequireAuth>
      <DashboardView refreshMs={dashboardRefreshMs(process.env.DASHBOARD_REFRESH_MS)} />
    </RequireAuth>
  );
}
