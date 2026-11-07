import { Suspense } from 'react';
import { RequireAuth } from '@/components/require-auth';
import { TicketsView } from '@/components/tickets-view';
import { closedRobots } from '@/lib/seo';
export const metadata = { title: '我的工单', robots: closedRobots('/tickets') };

// One page for the list and a ticket (?id=): the sidebar matches exact paths, so a nested route
// would lose it (handoff_ticket.md 2.2).
export default function TicketsPage() {
  return (
    <RequireAuth>
      <Suspense fallback={null}>
        <TicketsView />
      </Suspense>
    </RequireAuth>
  );
}
