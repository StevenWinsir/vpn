import { NodesView } from '@/components/nodes-view';
import { nodeListRefreshMs } from '@/lib/node-list';

export const dynamic = 'force-dynamic';
export const metadata = { title: '节点列表' };

export default function NodesPage() {
  return <NodesView refreshMs={nodeListRefreshMs(process.env.NODE_LIST_REFRESH_MS)} />;
}
