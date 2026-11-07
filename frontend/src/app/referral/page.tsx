import { RequireAuth } from '@/components/require-auth';
import { ReferralView } from '@/components/referral-view';
import { closedRobots } from '@/lib/seo';
export const metadata = { title: '推荐有礼', robots: closedRobots('/referral') };

export default function ReferralPage() {
  return (
    <RequireAuth>
      <ReferralView />
    </RequireAuth>
  );
}
