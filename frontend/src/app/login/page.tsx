import { AuthForm } from '@/components/auth-form';
import { indexableMetadata } from '@/lib/server/seo';

export function generateMetadata() {
  return indexableMetadata('/login');
}

export default function LoginPage() {
  return <AuthForm />;
}
