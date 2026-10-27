import { RequireAuth } from '@/components/require-auth';
import { AccountView } from '@/components/account-view';
export const metadata = { title: '账号设置' };

export default function AccountPage() {
  return (
    <RequireAuth>
      <AccountView />
    </RequireAuth>
  );
}
