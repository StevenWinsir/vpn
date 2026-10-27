'use client';
import { useId, useState } from 'react';
import { IconChevronDown } from '@tabler/icons-react';
import { paymentMethodsText, useHomeState } from './home-state';

type Item = { q: string; a: React.ReactNode };

// Frequently asked questions (handoff_ui.md 8.2). The answers restate the rules of the plan page
// and the checkout; nothing here promises more than they do (no refunds: there is no refund flow).
function items(payment: string, supportEmail: string | null, chat: boolean): Item[] {
  const list: Item[] = [
    {
      q: '流量是如何计算的？',
      a: '计费流量 = 实际上传与下载流量 × 线路倍率。不同线路的倍率可能不同，登录后可在节点列表中查看每条线路的倍率。',
    },
    {
      q: '可以同时在线几台设备？',
      a: '同时在线的设备数以所购套餐为准。只登录、未连接的客户端不占用名额；设备异常断开后，名额会在短时间内自动释放。',
    },
    {
      q: '年付套餐的流量如何刷新？',
      a: '年付套餐从开通起每 30 天把流量重置为一个月的额度，上个周期未用完的部分不累计；周期内用完需等到下次重置。',
    },
    {
      q: '支持哪些付款方式？',
      a: `支持${payment}在线付款。下单后跳转到支付页面完成付款，付款成功后套餐立即开通。`,
    },
    {
      q: '如何续费？',
      a: '在套餐页选择当前套餐即可续费，有效期在当前到期时间上顺延。月付续费时流量累加；年付订阅续费只顺延有效期，流量仍每 30 天刷新。有效期内不能更换为其他套餐，到期后可重新选择。',
    },
    {
      q: '忘记密码怎么办？',
      a: '在登录页点击“忘记密码”，按提示通过注册邮箱设置新密码。设置成功后，其他设备上的登录会自动退出。',
    },
  ];
  // The online chat (handoff_ui.md 9) is offered on the signed-in user pages; the address is the
  // operator's optional SITE_SUPPORT_EMAIL. With neither there is nothing to promise.
  const mail = supportEmail ? <a href={`mailto:${supportEmail}`}>{supportEmail}</a> : null;
  if (chat)
    list.push({
      q: '如何联系客服？',
      a: (
        <>
          登录后，点击用户中心页面右下角的“在线客服”按钮，即可与客服在线沟通。
          {mail ? <>也可以发送邮件至 {mail}，我们会尽快回复。</> : null}
        </>
      ),
    });
  else if (mail)
    list.push({
      q: '如何联系客服？',
      a: <>发送邮件至 {mail}，我们会尽快回复。</>,
    });
  return list;
}

export function HomeFaq({ supportEmail, chat }: { supportEmail: string | null; chat: boolean }) {
  const { meta } = useHomeState();
  const [open, setOpen] = useState<number | null>(0);
  const base = useId();
  return (
    <div className="home-faq" data-testid="home-faq">
      {items(paymentMethodsText(meta), supportEmail, chat).map(({ q, a }, index) => {
        const expanded = open === index;
        return (
          <div className="home-faq-item" key={q} data-open={expanded ? '' : undefined}>
            <h3>
              <button
                type="button"
                id={`${base}-q${index}`}
                aria-expanded={expanded}
                aria-controls={`${base}-a${index}`}
                onClick={() => setOpen(expanded ? null : index)}
              >
                <span>{q}</span>
                <IconChevronDown size={18} aria-hidden />
              </button>
            </h3>
            <div
              className="home-faq-panel"
              id={`${base}-a${index}`}
              role="region"
              aria-labelledby={`${base}-q${index}`}
              hidden={!expanded}
            >
              <p>{a}</p>
            </div>
          </div>
        );
      })}
    </div>
  );
}
