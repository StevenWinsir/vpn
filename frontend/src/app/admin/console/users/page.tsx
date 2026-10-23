import { Suspense } from 'react';
import { ConsoleUsers } from '@/components/console/console-users';

export default function Page() {
  return (
    <Suspense fallback={null}>
      <ConsoleUsers />
    </Suspense>
  );
}
