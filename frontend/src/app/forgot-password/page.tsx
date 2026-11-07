import { ForgotPasswordForm } from '@/components/password-reset';
import { closedRobots } from '@/lib/seo';

export const metadata = { title: '找回密码', robots: closedRobots('/forgot-password') };

export default function ForgotPasswordPage() {
  return <ForgotPasswordForm />;
}
