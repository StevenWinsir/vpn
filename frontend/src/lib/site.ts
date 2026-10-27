// Optional public facts of the operator (handoff_ui.md 8.6), read from the server environment on
// every request, so a change needs a restart, not a rebuild. A missing or malformed value hides
// the element rather than showing something wrong.

type Env = Record<string, string | undefined>;

export type SiteFacts = {
  // The ICP filing number shown in the footer, e.g. "京ICP备12345678号-1".
  icpRecord: string | null;
  // The customer-service address shown in the footer and the FAQ.
  supportEmail: string | null;
};

// MIIT's public filing lookup, which the filing number must link to.
export const icpLookupUrl = 'https://beian.miit.gov.cn/';

const EMAIL =
  /^[^\s@<>()[\]\\,;:"]{1,64}@[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$/;

function icp(value: string | undefined): string | null {
  const text = value?.trim() ?? '';
  // Plain text only: Chinese characters, letters, digits, spaces and the usual "号-1" suffix.
  if (!text || text.length > 40 || !/^[\p{Script=Han}A-Za-z0-9 \-—()（）]+$/u.test(text))
    return null;
  return text;
}

function email(value: string | undefined): string | null {
  const text = value?.trim() ?? '';
  return text.length <= 254 && EMAIL.test(text) ? text : null;
}

export function siteFacts(env: Env): SiteFacts {
  return { icpRecord: icp(env.SITE_ICP_RECORD), supportEmail: email(env.SITE_SUPPORT_EMAIL) };
}
