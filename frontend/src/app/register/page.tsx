import { AuthForm } from '@/components/auth-form';
import { indexableMetadata } from '@/lib/server/seo';

// The canonical address has no ?ref= (handoff_seo.md D11).
export function generateMetadata() {
  return indexableMetadata('/register', { title: '注册' });
}

export default function RegisterPage() {
  return <AuthForm register />;
}
