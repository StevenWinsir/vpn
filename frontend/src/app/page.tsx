import Link from 'next/link';
import { IconDeviceLaptop, IconReceipt, IconShieldCheck } from '@tabler/icons-react';
import { StartLink } from '@/components/start-link';
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
            稳定的线路，透明的计费。
            <br />
            一个账户，在手机与电脑上随时安全连接。
          </p>
          <div className="hero-actions">
            <StartLink />
            <Link href="/plans" className="secondary-link">
              查看套餐
            </Link>
          </div>
        </div>
        <div className="hero-side">
          <div className="connection-symbol">
            <IconShieldCheck size={68} stroke={1.1} />
          </div>
          <div className="hero-side-heading">连接，自有边界。</div>
          <p>
            线路、流量与订阅
            <br />
            一目了然
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
          <p>邮箱验证注册，登录记录可查，改密即退出其他设备。</p>
        </div>
        <div>
          <IconDeviceLaptop size={26} stroke={1.5} />
          <h2>权益分明</h2>
          <p>套餐有效期、线路权限与流量额度集中展示。</p>
        </div>
        <div>
          <IconReceipt size={26} stroke={1.5} />
          <h2>订阅透明</h2>
          <p>支持微信与支付宝付款，订单记录随时可查。</p>
        </div>
      </section>
      <footer className="landing-footer">
        <span>{appName}</span>
        <span>© {new Date().getFullYear()} 保留所有权利。</span>
      </footer>
    </div>
  );
}
