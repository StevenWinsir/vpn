import type { ConsoleMessage } from '@playwright/test';

// Chromium host rules that make every Crisp host fail to resolve (handoff_ui.md 11, round 4).
export const crispUnreachable =
  '--host-resolver-rules=MAP crisp.chat ~NOTFOUND, MAP *.crisp.chat ~NOTFOUND, MAP crisp.help ~NOTFOUND, MAP *.crisp.help ~NOTFOUND';

// The browser's own "Failed to load resource" line for the unreachable chat script: logged by
// Chromium itself, as it would be for anyone whose network blocks Crisp; not an error of the page.
export const crispLoadFailure = (entry: ConsoleMessage) =>
  entry.text().startsWith('Failed to load resource') &&
  /^https:\/\/([a-z0-9-]+\.)*crisp\.(chat|help)\//.test(entry.location().url);
