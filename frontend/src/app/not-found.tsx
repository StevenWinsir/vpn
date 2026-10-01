import Link from 'next/link';
export default function NotFound() {
  return (
    <div className="page-wrap">
      <h1>页面不存在</h1>
      <p>这个地址暂时没有内容。</p>
      <Link className="primary-link" href="/dashboard">
        返回工作台
      </Link>
    </div>
  );
}
