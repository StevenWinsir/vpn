import { RequireAuth } from '@/components/require-auth';
import { AccountView } from '@/components/account-view';
export default function AccountPage() {
  return (
    <RequireAuth>
      <AccountView />
    </RequireAuth>
  );
}
