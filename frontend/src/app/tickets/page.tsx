import { Suspense } from 'react';
import { RequireAuth } from '@/components/require-auth';
import { TicketsView } from '@/components/tickets-view';
export const metadata = { title: '我的工单' };

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
