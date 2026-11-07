import { RequireAuth } from '@/components/require-auth';
import { AccountView } from '@/components/account-view';
import { closedRobots } from '@/lib/seo';
export const metadata = { title: '账号设置', robots: closedRobots('/account') };

export default function AccountPage() {
  return (
    <RequireAuth>
      <AccountView />
    </RequireAuth>
  );
}
