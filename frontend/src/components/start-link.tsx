'use client';
import Link from 'next/link';
import { IconArrowRight } from '@tabler/icons-react';
import { useMeta } from '@/lib/use-meta';

// The landing page's primary action: "create account" only while the backend reports
// registration open; otherwise (also while loading or on errors) "sign in".
export function StartLink() {
  const { meta } = useMeta();
  const register = meta?.allow_registration === true;
  return (
    <Link href={register ? '/register' : '/login'} className="primary-link">
      {register ? '创建账户' : '登录账户'} <IconArrowRight size={19} />
    </Link>
  );
}
