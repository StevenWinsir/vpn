// Structured data (JSON-LD, handoff_seo.md 6.2, D9): plain functions, no React and no server APIs,
// so test:seo checks them. Only what the page itself shows and the service keeps: no ratings, no
// reviews, no HowTo, no SearchAction, and never a download address (handoff_download.md D4).
// Every address is absolute and built from SITE_URL, never from the request (D2).
import type { DownloadState, PlatformId } from './downloads.ts';
import { guideVersion, platformOrder } from './downloads.ts';
import { faqAnswerText, type FaqItem } from './home-faq.ts';
import { gib, type Plan, type PlanTier, type PlansResponse } from './types.ts';

type Thing = Record<string, unknown>;

const context = 'https://schema.org';
const brandName = 'AsterLink';

/**
 * The text of a <script type="application/ld+json">. Plan names and descriptions are free text the
 * administrator edits, so "<", ">" and "&" are written as JSON escapes: a "</script>" inside them
 * stays text and cannot end the script element. U+2028 and U+2029 are escaped as well.
 */
export function serializeJsonLd(data: Thing): string {
  return JSON.stringify(data)
    .replace(/</g, '\\u003c')
    .replace(/>/g, '\\u003e')
    .replace(/&/g, '\\u0026')
    .replace(/\u2028/g, '\\u2028')
    .replace(/\u2029/g, '\\u2029');
}

const page = (origin: string, path: string) => (path === '/' ? `${origin}/` : `${origin}${path}`);
const organizationId = (origin: string) => `${origin}/#organization`;

/** The home page: who runs the site (D9). The contact point needs SITE_SUPPORT_EMAIL. */
export function organization(origin: string, supportEmail: string | null): Thing {
  return {
    '@context': context,
    '@type': 'Organization',
    '@id': organizationId(origin),
    name: brandName,
    url: page(origin, '/'),
    logo: `${origin}/icons/icon-512.png`,
    ...(supportEmail
      ? {
          contactPoint: {
            '@type': 'ContactPoint',
            contactType: 'customer support',
            email: supportEmail,
            availableLanguage: ['zh-CN'],
          },
        }
      : {}),
  };
}

/** The home page: the site itself, without a SearchAction (there is no site search). */
export function website(origin: string): Thing {
  return {
    '@context': context,
    '@type': 'WebSite',
    '@id': `${origin}/#website`,
    name: brandName,
    url: page(origin, '/'),
    inLanguage: 'zh-CN',
    publisher: { '@id': organizationId(origin) },
  };
}

export type Crumb = { name: string; path: string };

/** "首页 › 使用教程 › Windows": the trail below the home page, in order. */
export function breadcrumbs(origin: string, trail: Crumb[]): Thing {
  const items = [{ name: '首页', path: '/' }, ...trail];
  return {
    '@context': context,
    '@type': 'BreadcrumbList',
    itemListElement: items.map((item, index) => ({
      '@type': 'ListItem',
      position: index + 1,
      name: item.name,
      item: page(origin, item.path),
    })),
  };
}

const systems: Record<PlatformId, { name: string; os: string }> = {
  windows: { name: 'Windows', os: 'Windows 10, Windows 11' },
  macos: { name: 'macOS', os: 'macOS' },
  android: { name: 'Android', os: 'Android' },
  ios: { name: 'iPhone', os: 'iOS' },
  linux: { name: 'Linux', os: 'Linux' },
};

/**
 * /download: one entry per released platform, with the version the page recommends. A network
 * connection client is a utility; it is not used to run a business. No address, offer or rating.
 */
export function softwareApplications(origin: string, state: DownloadState): Thing[] {
  if (state.status !== 'ok') return [];
  return platformOrder.flatMap((platform) => {
    const version = guideVersion(state.entries, platform);
    if (!version) return [];
    return [
      {
        '@context': context,
        '@type': 'SoftwareApplication',
        name: `${brandName} for ${systems[platform].name}`,
        operatingSystem: systems[platform].os,
        applicationCategory: 'UtilitiesApplication',
        softwareVersion: version,
        url: `${origin}/download#${platform}`,
        publisher: { '@id': organizationId(origin) },
      },
    ];
  });
}

/** The price exactly as the plan cards show it, in yuan with two decimals. */
export const yuan = (cents: number) => (cents / 100).toFixed(2);

function monthlyTier(plan: Plan): PlanTier | { price_cents: number } {
  return plan.monthly ?? { price_cents: plan.price_cents };
}

/** "100 GB 计费流量，最多 3 台设备同时在线": the line used when a plan has no description. */
export function planSummary(plan: Plan): string {
  return `${gib(plan.traffic_bytes)} GB 计费流量，最多 ${plan.max_devices} 台设备同时在线`;
}

/** /plans: one Product per plan on sale, its monthly and (when offered) yearly price. */
export function products(origin: string, response: PlansResponse): Thing[] {
  return response.plans.map((plan) => {
    const offer = (name: string, cents: number) => ({
      '@type': 'Offer',
      name,
      price: yuan(cents),
      priceCurrency: 'CNY',
      availability: 'https://schema.org/InStock',
      url: page(origin, '/plans'),
    });
    const offers = [offer('月付', monthlyTier(plan).price_cents)];
    if (plan.yearly) offers.push(offer('年付', plan.yearly.price_cents));
    return {
      '@context': context,
      '@type': 'Product',
      name: plan.name,
      description: plan.description.trim() || planSummary(plan),
      brand: { '@type': 'Brand', name: brandName },
      offers,
    };
  });
}

/** The home page's questions and answers, word for word as shown (S3-08). */
export function faqPage(items: FaqItem[]): Thing {
  return {
    '@context': context,
    '@type': 'FAQPage',
    mainEntity: items.map((item) => ({
      '@type': 'Question',
      name: item.q,
      acceptedAnswer: { '@type': 'Answer', text: faqAnswerText(item) },
    })),
  };
}
