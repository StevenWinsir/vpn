import { Suspense } from 'react';
import { ConsoleAnnouncements } from '@/components/console/console-announcements';

export default function Page() {
  return (
    <Suspense fallback={null}>
      <ConsoleAnnouncements />
    </Suspense>
  );
}
