import Link from 'next/link';

export const metadata = { title: '页面不存在' };

export default function NotFound() {
  return (
    <div className="page-wrap">
      <h1>页面不存在</h1>
      <p>你访问的页面可能已被移除，或链接地址有误。</p>
      <div className="hero-actions">
        <Link className="primary-link" href="/">
          返回首页
        </Link>
        <Link className="secondary-link" href="/dashboard">
          前往用户中心
        </Link>
      </div>
    </div>
  );
}
