import { RequireAuth } from '@/components/require-auth';
import { ReferralView } from '@/components/referral-view';
export default function ReferralPage() {
  return (
    <RequireAuth>
      <ReferralView />
    </RequireAuth>
  );
}
