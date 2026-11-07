// The home page's frequently asked questions (handoff_ui.md 8.2) as plain data: the server renders
// them into the page and into its FAQPage structured data (handoff_seo.md 6.2, S3-08), the browser
// re-renders them once /meta is known. One function, so the two can never say different things.
// The answers restate the rules of the plan page and the checkout; nothing here promises more than
// they do (no refunds: there is no refund flow).
import { payTypeLabel, type Meta } from './types.ts';

/** A piece of an answer: text, or the support address (shown as a mail link). */
export type FaqPart = string | { mail: string };
export type FaqItem = { q: string; a: FaqPart[] };

export type FaqFacts = {
  /** "微信支付和支付宝": the methods the shop offers. */
  payment: string;
  /** The operator's optional SITE_SUPPORT_EMAIL. */
  supportEmail: string | null;
  /** Whether the online chat is switched on (handoff_ui.md 9). */
  chat: boolean;
  /** /meta tickets_enabled (handoff_ticket.md 11.4). */
  tickets: boolean;
  /** /meta plan_upgrade_supported (handoff_plans.md 11.1). */
  upgrades: boolean;
};

// "微信支付和支付宝" from the methods the shop offers; the usual pair when /meta has none.
export function paymentMethodsText(meta: Meta | null): string {
  const names = (meta?.payment_enabled ? (meta.payment_methods ?? []) : [])
    .map((m) => payTypeLabel[m])
    .filter(Boolean);
  if (names.length === 0) return '微信支付和支付宝';
  return names.length === 1 ? names[0] : `${names.slice(0, -1).join('、')}和${names.at(-1)}`;
}

/** The facts the answers depend on, from /meta (null while unknown: the cautious wording). */
export function faqFacts(meta: Meta | null, supportEmail: string | null, chat: boolean): FaqFacts {
  return {
    payment: paymentMethodsText(meta),
    supportEmail,
    chat,
    tickets: meta?.tickets_enabled === true,
    upgrades: meta?.plan_upgrade_supported === true,
  };
}

export function homeFaq({ payment, supportEmail, chat, tickets, upgrades }: FaqFacts): FaqItem[] {
  const list: FaqItem[] = [
    {
      q: '流量是如何计算的？',
      a: [
        '计费流量 = 实际上传与下载流量 × 线路倍率。不同线路的倍率可能不同，登录后可在节点列表中查看每条线路的倍率。',
      ],
    },
    {
      q: '可以同时在线几台设备？',
      a: [
        '同时在线的设备数以所购套餐为准。只登录、未连接的客户端不占用名额；设备异常断开后，名额会在短时间内自动释放。',
      ],
    },
    {
      q: '年付套餐的流量如何刷新？',
      a: [
        '年付套餐从开通起每 30 天把流量重置为一个月的额度，上个周期未用完的部分不累计；周期内用完需等到下次重置。',
      ],
    },
    {
      q: '支持哪些付款方式？',
      a: [`支持${payment}在线付款。下单后跳转到支付页面完成付款，付款成功后套餐立即开通。`],
    },
    {
      q: '如何续费或更换套餐？',
      a: [
        '在套餐页选择当前套餐即可续费，有效期在当前到期时间上顺延。月付续费时流量累加；年付订阅续费只顺延有效期，流量仍每 30 天刷新。' +
          (upgrades
            ? '有效期内可以升级到更高的套餐，只需为剩余天数补差价，到期时间不变；有效期内不支持降级，降级不退还差价，到期后可选择任意套餐。'
            : '有效期内不能更换为其他套餐，到期后可重新选择。'),
      ],
    },
    {
      q: '忘记密码怎么办？',
      a: [
        '在登录页点击“忘记密码”，按提示通过注册邮箱设置新密码。设置成功后，其他设备上的登录会自动退出。',
      ],
    },
  ];
  // The chat is offered on the signed-in user pages; the address is optional; tickets are there
  // when /meta says so. With none of them there is nothing to promise.
  const ticket = tickets ? '登录后也可以在“我的工单”提交问题，客服会在工单中回复。' : '';
  const mail = supportEmail ? { mail: supportEmail } : null;
  const parts = (...items: (FaqPart | null | '')[]) =>
    items.filter((item): item is FaqPart => item !== null && item !== '');
  if (chat)
    list.push({
      q: '如何联系客服？',
      a: parts(
        '登录后，点击用户中心页面右下角的“在线客服”按钮，即可与客服在线沟通。',
        ticket,
        mail ? '也可以发送邮件至 ' : null,
        mail,
        mail ? '，我们会尽快回复。' : null,
      ),
    });
  else if (mail)
    list.push({ q: '如何联系客服？', a: parts('发送邮件至 ', mail, '，我们会尽快回复。', ticket) });
  else if (tickets)
    list.push({ q: '如何联系客服？', a: ['登录后可以在“我的工单”提交问题，客服会在工单中回复。'] });
  return list;
}

/** An answer as plain text, exactly as the page shows it (the mail link reads as its address). */
export const faqAnswerText = (item: FaqItem) =>
  item.a.map((part) => (typeof part === 'string' ? part : part.mail)).join('');
