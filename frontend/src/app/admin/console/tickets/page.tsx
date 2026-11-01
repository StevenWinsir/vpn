import { Suspense } from 'react';
import { ConsoleTickets } from '@/components/console/console-tickets';

export default function Page() {
  return (
    <Suspense fallback={null}>
      <ConsoleTickets />
    </Suspense>
  );
}
