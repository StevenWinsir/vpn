import { RequireAuth } from '@/components/require-auth';
import { DashboardView } from '@/components/dashboard-view';
import { dashboardRefreshMs } from '@/lib/dashboard';
import { closedRobots } from '@/lib/seo';

export const dynamic = 'force-dynamic';

export const metadata = { title: '总览', robots: closedRobots('/dashboard') };

export default function DashboardPage() {
  return (
    <RequireAuth>
      <DashboardView refreshMs={dashboardRefreshMs(process.env.DASHBOARD_REFRESH_MS)} />
    </RequireAuth>
  );
}
