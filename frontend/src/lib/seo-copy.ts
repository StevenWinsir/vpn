// The title and description of every page search engines may index (handoff_seo.md 5.1, D6, D7):
// plain data next to the pages, scanned by copy-guard, checked for length, uniqueness and wording
// by test:seo. Private pages keep their short titles and say nothing more.
import type { DownloadState, PlatformId } from './downloads.ts';
import { platformOrder } from './downloads.ts';

export type IndexableRoute =
  | '/'
  | '/plans'
  | '/download'
  | '/guide'
  | '/guide/windows'
  | '/guide/macos'
  | '/guide/android'
  | '/guide/ios'
  | '/terms'
  | '/privacy'
  | '/login'
  | '/register';

export type PageCopy = { title: string; description: string };

export const pageCopy: Record<IndexableRoute, PageCopy> = {
  '/': {
    title: 'AsterLink · 住宅原生 IP 与专线稳定连接',
    description:
      'AsterLink 提供住宅宽带原生 IP 出口与专线线路，一个账户可在手机和电脑上同时使用。各线路倍率公开透明，月付、年付自由选择，微信、支付宝在线付款，即买即用。',
  },
  '/plans': {
    title: '套餐价格与订阅 · AsterLink',
    description:
      '查看 AsterLink 月付与年付套餐的价格、计费流量、同时在线设备数和专线权益。年付套餐每 30 天刷新一次流量额度，微信、支付宝在线付款后立即开通。',
  },
  '/download': {
    title: '下载客户端 · AsterLink',
    // Replaced per request by downloadDescription; this is its wording without any platform.
    description: '',
  },
  '/guide': {
    title: '使用教程 · AsterLink',
    description:
      'AsterLink 使用教程：从注册账户、选择套餐，到在 Windows 电脑、Mac、Android 手机和 iPhone 上安装客户端、登录并连接的完整图文步骤，附各平台的常见问题解答。',
  },
  '/guide/windows': {
    title: 'Windows 客户端使用教程 · AsterLink',
    description:
      'AsterLink Windows 客户端图文教程：适用系统、下载安装、首次授权后台服务、登录账户、选择线路、连接与断开、查看套餐与用量，以及后台运行、升级卸载和常见问题解答。',
  },
  '/guide/macos': {
    title: 'macOS 客户端使用教程 · AsterLink',
    description:
      'AsterLink macOS 客户端图文教程：适用的 Apple 芯片 Mac 与系统版本、拖入“应用程序”安装、首次授权、登录账户、选择线路、连接与断开，以及升级卸载和常见问题解答。',
  },
  '/guide/android': {
    title: 'Android 客户端使用教程 · AsterLink',
    description:
      'AsterLink Android 客户端图文教程：用手机浏览器下载安装、首次授权与通知、登录账户、选择线路、一键连接，以及各品牌手机的后台运行与省电设置、升级卸载和常见问题。',
  },
  '/guide/ios': {
    title: 'iPhone 客户端使用教程 · AsterLink',
    description:
      'AsterLink iPhone 客户端图文教程：用 Safari 下载安装、处理“未受信任”提示、首次授权、登录账户、选择线路、一键连接，以及后台运行与通知、升级卸载和常见问题。',
  },
  '/terms': {
    title: '服务条款 · AsterLink',
    description:
      'AsterLink 服务条款：账户注册与安全、套餐购买与付款、流量与设备规则、禁止的使用行为、违规处理、服务变更与中止、责任限制和争议解决。使用本服务前请仔细阅读。',
  },
  '/privacy': {
    title: '隐私政策 · AsterLink',
    description:
      'AsterLink 隐私政策：我们收集哪些个人信息、用于什么目的、保存多久、与哪些第三方共享，你如何查询、更正、删除个人信息和注销账户，以及未成年人保护与联系方式。',
  },
  '/login': {
    title: '登录 · AsterLink',
    description:
      '登录 AsterLink 账户，在用户中心查看套餐有效期、剩余流量和近 24 小时用量，管理在线设备与订单记录，随时续费套餐，并在手机和电脑上使用同一账户连接。',
  },
  '/register': {
    title: '注册账户 · AsterLink',
    description:
      '使用常用邮箱注册 AsterLink 账户，完成邮箱验证后即可选择月付或年付套餐。一个账户可在 Windows、Mac、Android 和 iPhone 等多台设备上登录，按套餐支持多台设备同时在线。',
  },
};

const platformNames: Record<PlatformId, string> = {
  windows: 'Windows',
  macos: 'macOS',
  android: 'Android',
  ios: 'iPhone',
  linux: 'Linux',
};

/** The platforms the download page shows as released, in its own order. */
export function releasedPlatforms(state: DownloadState): PlatformId[] {
  if (state.status !== 'ok') return [];
  return platformOrder.filter((p) => state.entries.some((e) => e.platform === p));
}

/**
 * The download page's description names exactly the clients it offers right now (handoff_seo.md
 * 8: never a platform the page shows as not yet released); none, or a list that cannot be read,
 * gives the sentence without platforms.
 */
export function downloadDescription(state: DownloadState): string {
  const names = releasedPlatforms(state).map((p) => platformNames[p]);
  const which = names.length ? `官方 ${names.join('、')} 客户端` : '官方客户端';
  return `下载 AsterLink ${which}。安装后登录账户，即可选择线路、一键连接，同一账户可在多台设备上使用；各平台的系统要求、安装与首次授权步骤，请查看使用教程。`;
}

/** The text of the share image (scripts/brand-icons.mjs draws it; handoff_seo.md D14). */
export const shareText = {
  name: 'AsterLink',
  headline: '住宅原生 IP · 专线稳定连接',
  line: '一个账户，手机与电脑随时连接',
  alt: 'AsterLink：住宅原生 IP 与专线稳定连接',
};
