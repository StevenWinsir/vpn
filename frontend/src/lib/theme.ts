// The appearance preference (handoff_ui.md 6, D7/D8): "light", "dark" or "auto", kept only in this
// browser. The head script and the MantineProvider must read the same key, or the first paint and
// the app disagree. Nothing is written until the user picks an appearance, so "auto" is the default.
export const colorSchemeKey = 'asterlink-color-scheme';

// Mantine's head script gives up silently when storage throws (some private modes) and leaves the
// server's "light". This one then follows the system instead, still before the first paint.
export const colorSchemeFallbackScript = `try{window.localStorage.getItem(${JSON.stringify(colorSchemeKey)})}catch(e){try{document.documentElement.setAttribute("data-mantine-color-scheme",window.matchMedia("(prefers-color-scheme: dark)").matches?"dark":"light")}catch(f){}}`;
