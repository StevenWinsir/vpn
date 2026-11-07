import { AdminNodesView } from '@/components/admin-nodes-view';
import { closedRobots } from '@/lib/seo';

export const metadata = { robots: closedRobots('/admin/nodes') };

export default function AdminNodesPage() {
  return <AdminNodesView />;
}
