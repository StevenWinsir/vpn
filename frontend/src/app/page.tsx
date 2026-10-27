import Link from 'next/link';
import {
  IconArrowRight,
  IconBolt,
  IconBrandAndroid,
  IconBrandApple,
  IconBrandWindows,
  IconChartBar,
  IconCheck,
  IconCreditCard,
  IconDevices,
  IconDeviceLaptop,
  IconDownload,
  IconRoute,
  IconShieldLock,
} from '@tabler/icons-react';
import { BrandMark } from '@/components/brand-mark';
import { PrimaryAction } from '@/components/home/home-actions';
import { HomeFaq } from '@/components/home/home-faq';
import { HomeHeader } from '@/components/home/home-header';
import { HomeStateProvider } from '@/components/home/home-state';
import { PlanPreview } from '@/components/home/plan-preview';
import { ProductVisual } from '@/components/home/product-visual';
import { Reveal } from '@/components/home/reveal';
import { androidDownload, iosDownload } from '@/lib/downloads';
import { icpLookupUrl, siteFacts } from '@/lib/site';
import { crispSetting } from '@/lib/support-config';
import { appName } from '@/lib/types';

// The download status and the operator facts come from the server environment on every request,
// exactly like /download (handoff_ui.md 8.3): a new release or a filing number needs a restart,
// not a rebuild. Rendering reads no database and calls no API, so the first byte stays fast; the
// plan preview and the account actions load in the browser.
export const dynamic = 'force-dynamic';

const features = [
  {
    icon: IconRoute,
    title: '智能线路分配',
    text: '每条线路由一组服务器共同提供，连接时自动为你分配可用的服务器，无需手动挑选。',
  },
  {
    icon: IconBolt,
    title: '专线权益',
    text: '部分套餐包含专线权限，适合对连接质量要求更高的使用场景。',
  },
  {
    icon: IconChartBar,
    title: '透明计费',
    text: '计费流量 = 实际流量 × 线路倍率，各线路倍率公开可查，近 24 小时用量随时查看。',
  },
  {
    icon: IconDevices,
    title: '多设备同时在线',
    text: '按套餐支持多台设备同时在线，在线设备一目了然，设备断开后名额自动释放。',
  },
  {
    icon: IconShieldLock,
    title: '账户安全',
    text: '邮箱验证注册，登录记录随时可查；修改密码后，其他设备上的登录自动退出。',
  },
  {
    icon: IconCreditCard,
    title: '灵活订阅',
    text: '月付、年付自由选择，年付每 30 天刷新流量额度；微信、支付宝在线付款，即买即用。',
  },
];

const steps = [
  { title: '注册账户', text: '使用常用邮箱注册，完成验证即可登录用户中心。' },
  { title: '选择套餐', text: '按需选择月付或年付套餐，在线付款后立即开通。' },
  { title: '下载客户端', text: '在手机或电脑上安装客户端，登录同一账户即可一键连接。' },
];

export default function Home() {
  const android = androidDownload(process.env) !== null;
  const ios = iosDownload(process.env) !== null;
  const { icpRecord, supportEmail } = siteFacts(process.env);
  const chat = crispSetting(process.env).websiteId !== null;
  const platforms = [
    { name: 'macOS', note: 'Mac 电脑', icon: IconDeviceLaptop, ready: false },
    { name: 'Windows', note: 'Windows 电脑', icon: IconBrandWindows, ready: false },
    { name: 'Android', note: 'Android 手机', icon: IconBrandAndroid, ready: android },
    { name: 'iOS', note: 'iPhone', icon: IconBrandApple, ready: ios },
  ];
  const year = new Date().getFullYear();
  return (
    <HomeStateProvider>
      <div className="home">
        <HomeHeader />
        <main>
          <section className="home-hero" aria-labelledby="home-title">
            <div className="home-container home-hero-grid">
              <div className="home-hero-copy">
                <p className="home-eyebrow">
                  <span />
                  全平台安全连接服务
                </p>
                <h1 id="home-title">
                  每一次连接，
                  <br />
                  <em>都稳定而从容。</em>
                </h1>
                <p className="home-lead">
                  智能线路分配、公开透明的计费规则与多设备同时在线。一个账户，在手机与电脑上随时安全连接。
                </p>
                <div className="home-hero-actions" data-testid="hero-actions">
                  <PrimaryAction />
                  <Link href="/plans" className="home-btn home-btn-ghost">
                    查看套餐
                  </Link>
                </div>
                <ul className="home-points">
                  <li>
                    <IconCheck size={16} aria-hidden />
                    线路倍率公开透明
                  </li>
                  <li>
                    <IconCheck size={16} aria-hidden />
                    多设备同时在线
                  </li>
                  <li>
                    <IconCheck size={16} aria-hidden />
                    月付年付灵活订阅
                  </li>
                </ul>
              </div>
              <ProductVisual />
            </div>
          </section>

          <section id="features" className="home-section" aria-labelledby="features-title">
            <div className="home-container">
              <div className="home-section-head" data-reveal="">
                <p className="home-kicker">产品能力</p>
                <h2 id="features-title">为稳定连接而设计的每一处细节</h2>
                <p>从线路分配到计费规则，每一项能力都清晰可见，用得明白，付得放心。</p>
              </div>
              <div className="home-features" data-reveal="">
                {features.map(({ icon: Icon, title, text }) => (
                  <article key={title} className="home-feature">
                    <span className="home-feature-icon">
                      <Icon size={22} stroke={1.7} aria-hidden />
                    </span>
                    <h3>{title}</h3>
                    <p>{text}</p>
                  </article>
                ))}
              </div>
            </div>
          </section>

          <section className="home-section home-section-alt" aria-labelledby="steps-title">
            <div className="home-container">
              <div className="home-section-head" data-reveal="">
                <p className="home-kicker">快速开始</p>
                <h2 id="steps-title">三步开始使用</h2>
              </div>
              <ol className="home-steps" data-reveal="">
                {steps.map(({ title, text }, index) => (
                  <li key={title} className="home-step">
                    <span className="home-step-index">{String(index + 1).padStart(2, '0')}</span>
                    <h3>{title}</h3>
                    <p>{text}</p>
                  </li>
                ))}
              </ol>
            </div>
          </section>

          <section id="pricing" className="home-section" aria-labelledby="pricing-title">
            <div className="home-container">
              <div className="home-section-head" data-reveal="">
                <p className="home-kicker">套餐价格</p>
                <h2 id="pricing-title">简单透明的套餐价格</h2>
                <p>月付灵活，年付更划算。价格与套餐页实时一致，付款后立即开通。</p>
              </div>
              <PlanPreview />
            </div>
          </section>

          <section
            id="clients"
            className="home-section home-section-alt"
            aria-labelledby="clients-title"
          >
            <div className="home-container">
              <div className="home-section-head" data-reveal="">
                <p className="home-kicker">多平台客户端</p>
                <h2 id="clients-title">一个账户，多端通用</h2>
                <p>套餐、线路与流量在各设备间实时同步，登录即可连接。</p>
              </div>
              <div className="home-platforms" data-reveal="" data-testid="home-platforms">
                {platforms.map(({ name, note, icon: Icon, ready }) => (
                  <article
                    key={name}
                    className="home-platform"
                    data-testid="home-platform"
                    data-platform={name}
                    data-ready={ready ? 'true' : 'false'}
                  >
                    <Icon size={30} stroke={1.5} aria-hidden />
                    <h3>{name}</h3>
                    <p>{note}</p>
                    {ready ? (
                      <Link
                        href="/download"
                        className="home-btn home-btn-primary home-btn-sm"
                        aria-label={`立即下载 ${name} 客户端`}
                      >
                        <IconDownload size={16} aria-hidden />
                        立即下载
                      </Link>
                    ) : (
                      <span className="home-soon">即将上线</span>
                    )}
                  </article>
                ))}
              </div>
            </div>
          </section>

          <section id="faq" className="home-section" aria-labelledby="faq-title">
            <div className="home-container home-faq-wrap">
              <div className="home-section-head" data-reveal="">
                <p className="home-kicker">常见问题</p>
                <h2 id="faq-title">你可能想了解</h2>
              </div>
              <HomeFaq supportEmail={supportEmail} chat={chat} />
            </div>
          </section>

          <section className="home-section home-cta-section" aria-labelledby="cta-title">
            <div className="home-container">
              <div className="home-cta" data-reveal="">
                <div>
                  <h2 id="cta-title">现在开始，享受稳定安心的连接</h2>
                  <p>注册账户，选择适合你的套餐，几步即可开始使用。</p>
                </div>
                <div className="home-cta-actions">
                  <PrimaryAction tone="inverse" />
                  <Link href="/plans" className="home-btn home-btn-on-dark">
                    查看套餐
                    <IconArrowRight size={17} aria-hidden />
                  </Link>
                </div>
              </div>
            </div>
          </section>
        </main>

        <footer className="home-footer">
          <div className="home-container">
            <div className="home-footer-grid">
              <div className="home-footer-brand">
                <Link href="/" className="brand">
                  <BrandMark size={34} />
                  <span>
                    {appName}
                    <small>连接，自有边界。</small>
                  </span>
                </Link>
                <p>稳定、透明的全平台安全连接服务。</p>
              </div>
              <nav aria-label="产品">
                <h2>产品</h2>
                <Link href="/plans">套餐价格</Link>
                <Link href="/download">下载客户端</Link>
              </nav>
              <nav aria-label="账户">
                <h2>账户</h2>
                <Link href="/login">登录</Link>
                <Link href="/register">注册</Link>
                <Link href="/forgot-password">找回密码</Link>
              </nav>
              {supportEmail ? (
                <div>
                  <h2>联系我们</h2>
                  <a href={`mailto:${supportEmail}`} data-testid="support-email">
                    {supportEmail}
                  </a>
                </div>
              ) : null}
            </div>
            <div className="home-footer-bottom">
              <span>
                © {year} {appName}. 保留所有权利。
              </span>
              {icpRecord ? (
                <a
                  href={icpLookupUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                  data-testid="icp-record"
                >
                  {icpRecord}
                </a>
              ) : null}
            </div>
          </div>
        </footer>
        <Reveal />
      </div>
    </HomeStateProvider>
  );
}
