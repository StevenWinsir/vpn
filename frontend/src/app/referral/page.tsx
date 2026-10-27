import { RequireAuth } from '@/components/require-auth';
import { ReferralView } from '@/components/referral-view';
export const metadata = { title: '推荐有礼' };

export default function ReferralPage() {
  return (
    <RequireAuth>
      <ReferralView />
    </RequireAuth>
  );
}
