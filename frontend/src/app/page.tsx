import Link from 'next/link';
import {
  IconArrowRight,
  IconDeviceLaptop,
  IconReceipt,
  IconShieldCheck,
} from '@tabler/icons-react';
import { appName } from '@/lib/types';
export default function Home() {
  return (
    <div className="landing">
      <section className="hero">
        <div className="hero-copy">
          <h1>
            让每一次连接，
            <br />
            <span>尽在掌握。</span>
          </h1>
          <p>
            一个清晰、有序的工作台。
            <br />
            管理你的账户、订阅与流量，让连接回归简单。
          </p>
          <div className="hero-actions">
            <Link href="/register" className="primary-link">
              创建账户 <IconArrowRight size={19} />
            </Link>
            <Link href="/plans" className="secondary-link">
              查看套餐
            </Link>
          </div>
          <div className="hero-disclaimer">开发联调版 · 支持模拟购买 · 尚未提供实际代理服务</div>
        </div>
        <div className="hero-side">
          <div className="connection-symbol">
            <IconShieldCheck size={68} stroke={1.1} />
          </div>
          <div className="hero-side-heading">连接，自有边界。</div>
          <p>
            从登录到订阅
            <br />
            每一步都清晰可见
          </p>
          <div className="hero-side-bottom">
            <span>账户</span>
            <span>订阅</span>
            <span>流量</span>
          </div>
        </div>
      </section>
      <section className="feature-strip">
        <div>
          <IconShieldCheck size={26} stroke={1.5} />
          <h2>账户安全</h2>
          <p>独立身份认证，可撤销登录会话。</p>
        </div>
        <div>
          <IconDeviceLaptop size={26} stroke={1.5} />
          <h2>权益分明</h2>
          <p>套餐有效期、线路权限与流量额度集中展示。</p>
        </div>
        <div>
          <IconReceipt size={26} stroke={1.5} />
          <h2>订阅透明</h2>
          <p>从套餐选择到测试开通，订单全程可查。</p>
        </div>
      </section>
      <footer className="landing-footer">
        <span>{appName}</span>
        <span>网站账户服务已搭建；真实支付和各平台客户端待接入。</span>
      </footer>
    </div>
  );
}
