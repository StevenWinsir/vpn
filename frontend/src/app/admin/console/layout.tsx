import { ConsoleShell } from '@/components/console/console-shell';
import { closedRobots } from '@/lib/seo';

// Every console page takes its robots value from here (handoff_seo.md 4).
export const metadata = { robots: closedRobots('/admin/console') };

export default function ConsoleLayout({ children }: { children: React.ReactNode }) {
  return <ConsoleShell>{children}</ConsoleShell>;
}
