import { Suspense } from 'react';
import { ConsoleLogins } from '@/components/console/console-logins';

export default function Page() {
  return (
    <Suspense fallback={null}>
      <ConsoleLogins />
    </Suspense>
  );
}
