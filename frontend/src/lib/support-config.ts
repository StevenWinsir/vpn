// The online support chat (handoff_ui.md 9.6, D10), read from the server environment on every
// request to /api/runtime-config, so switching it needs a restart, not a rebuild. Unset uses the
// operator's website; "off" disables it; anything else that is not a website ID also disables it
// and is reported once in the server log. None of these may ever fail the runtime configuration,
// which every API call of the browser depends on.

type Env = Record<string, string | undefined>;

// The operator's Crisp website (from the requirement).
export const defaultCrispWebsiteId = '83336a84-ae9f-4345-ba1f-583c0790002d';

// Crisp website IDs are lowercase UUIDs; upper case is accepted and normalised.
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export type CrispSetting = {
  websiteId: string | null;
  // Why a configured value was ignored; null when it was used, unset or deliberately "off".
  problem: string | null;
};

export function crispSetting(env: Env): CrispSetting {
  const raw = env.CRISP_WEBSITE_ID;
  const value = (raw ?? '').trim().toLowerCase();
  if (!value) return { websiteId: defaultCrispWebsiteId, problem: null };
  if (value === 'off') return { websiteId: null, problem: null };
  if (UUID.test(value)) return { websiteId: value, problem: null };
  return {
    websiteId: null,
    problem: 'CRISP_WEBSITE_ID is neither "off" nor a website ID; online support is disabled.',
  };
}
