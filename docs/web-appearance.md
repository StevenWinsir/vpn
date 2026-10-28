# Website appearance: light/dark theme, design tokens, brand icons

Scope: the Next.js website in `frontend/` (handoff_ui.md sections 6 and 7, round 2).

## Light and dark

- The first visit follows the operating system. The visitor can pick 浅色 (light), 深色 (dark) or 跟随系统 (follow the system) from the 切换外观 button. It sits in the site header, and on the sign-in pages in the top-right corner. The choice is kept in this browser's `localStorage` under `asterlink-color-scheme` (`light`, `dark` or `auto`). It is not tied to the account: signing out keeps it, and other browsers do not see it.
- The scheme is applied before the first paint by the head script in `src/app/layout.tsx`: Mantine's `ColorSchemeScript`, plus a fallback that follows the system when storage throws. The MantineProvider (`src/components/providers.tsx`) uses the same key from `src/lib/theme.ts`. If the head script and the provider ever disagree, the page flashes or forgets the choice.
- Open tabs follow a change made in another tab (the `storage` event).
- The browser bar colour (`theme-color`) follows the system appearance only. When a manual choice goes against the system, the bar does not change; this is a known small limitation.
- The Crisp chat window (round 4) keeps its own look, set in the Crisp console.

## Design tokens

- `src/app/globals.css` starts with one `tokens:start … tokens:end` block. It defines `--al-*` variables twice: under `:root` / `[data-mantine-color-scheme='light']` and under `[data-mantine-color-scheme='dark']`. The rules below the block use only these variables and Mantine's own.
- The file does not use `light-dark()`, which older browsers (some Chinese browsers, older Safari) lack.
- `npm run test:theme` (in CI) fails when a colour literal appears outside the block, or when a token used by a rule has no definition.
- Mantine's dark palette (`theme.colors.dark`) is a cool, brand-tinted scale. Shade 7 is cards and the body, and `--al-bg` (shade 8) is the page behind them.
- Filled primary buttons are teal 7 with white text in light (5.2:1), and teal 5 with dark text in dark (5.8:1).

## Brand icons

All icons come from one file, `frontend/brand/mark.svg`. Its first element must be the background `<rect>`, whose fill is also the opaque background of the Apple icon. To change the logo, replace that file, then run:

```bash
cd frontend
PLAYWRIGHT_CHROME_PATH="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" npm run brand:icons
```

`PLAYWRIGHT_CHROME_PATH` is only needed when Playwright has no browser of its own. The command regenerates these files, which you then commit (CI never regenerates them):

| File | Use |
| --- | --- |
| `src/app/icon.svg` | the source as is; modern browsers |
| `src/app/favicon.ico` | 16, 32 and 48 px PNGs; old browsers and direct `/favicon.ico` requests |
| `src/app/apple-icon.png` | 180 px, opaque; iOS home screen |
| `public/icons/icon-192.png`, `icon-512.png` | web app manifest (`src/app/manifest.ts`); Android home screen |
| `src/components/brand-mark-art.ts` | artwork of the `BrandMark` component (header, sign-in pages) |

Next.js adds the `src/app` icon files to every route. Do not put icon, favicon or apple-icon files in any route folder, and do not set `icons` in a page's metadata. `npm run test:theme` checks both rules, and also that the generated files match the source.

The native apps' icons are not yet the same as this mark (handoff_ui.md 7.5).

## Online support

The Crisp chat (handoff_ui.md 9) lives in `src/lib/support.ts` (the loader: one injection per page,
idle preload, hidden Crisp bubble, session reset), `src/components/support-widget.tsx` (where it is
offered, the launcher, the sidebar entry) and `src/lib/support-config.ts` (`CRISP_WEBSITE_ID`).
The launcher uses the `--al-*` tokens like everything else; Crisp's own window is styled in the
Crisp console and does not follow the site's dark theme. Every Playwright config resolves the
Crisp hosts to nothing (`tests/crisp-hosts.ts`), so no test ever reaches the real service;
`tests/support-widget.spec.ts` stands in for it with request interception. Operations notes:
docs/deployment.md "在线客服".

## Home page

The home page (`src/app/page.tsx`, handoff_ui.md section 8, round 3) does not use the workspace shell: `SiteShell` hands `/` straight through, and the page brings its own sticky marketing header (`src/components/home/`), main content and footer.

- It is rendered on every request (`force-dynamic`), like `/download`, because it reads `ANDROID_*`, `IOS_*`, `SITE_ICP_RECORD` and `SITE_SUPPORT_EMAIL` from the server environment. Rendering reads no database and calls no API.
- In the browser, one `/meta` request and the signed-in state decide the account actions (sign in / free registration / user centre). They stay blank until both are known, so a visitor never sees one label turn into another. `/plans` feeds the plan preview (at most three plans, featured first); a failure or an empty shop hides the cards silently.
- The hero illustration is HTML, CSS and inline SVG, colours from the tokens. The page loads no font, image, CDN or analytics from another host (`tests/home.spec.ts` checks every request).
- Motion: sections below the fold fade in once (`Reveal`, IntersectionObserver), and the illustration's status dot breathes. With `prefers-reduced-motion: reduce` there is no animation and anchor scrolling is instant.
- Copy follows handoff_ui.md D12: no figures that cannot be proved and no superlatives. `npm run test:copy` and the specs reject the D12 vocabulary on every user page.
