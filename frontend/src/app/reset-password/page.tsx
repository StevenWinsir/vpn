import type { Metadata } from 'next';
import { ResetPasswordForm } from '@/components/password-reset';
import { closedRobots } from '@/lib/seo';

// The token arrives in the URL fragment and is read only in the browser: this page receives
// no token through props, query or cookies. Rendering per request keeps the response no-store.
export const dynamic = 'force-dynamic';
export const metadata: Metadata = {
  title: '设置新密码',
  referrer: 'no-referrer',
  robots: closedRobots('/reset-password'),
};

export default function ResetPasswordPage() {
  return <ResetPasswordForm />;
}
