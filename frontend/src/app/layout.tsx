import type { Metadata, Viewport } from 'next';
import { ColorSchemeScript, mantineHtmlProps } from '@mantine/core';
import '@mantine/core/styles.css';
import '@mantine/notifications/styles.css';
import './globals.css';
import { Providers } from '@/components/providers';
import { SiteShell } from '@/components/site-shell';
import { colorSchemeFallbackScript, colorSchemeKey } from '@/lib/theme';
const name = process.env.NEXT_PUBLIC_APP_NAME || 'AsterLink';
// Icons come from the files next to this layout (icon.svg, favicon.ico, apple-icon.png,
// manifest.ts) for every route; no page sets its own (handoff_ui.md 7.4).
export const metadata: Metadata = {
  title: { default: `${name} · 连接，自有边界`, template: `%s · ${name}` },
  description: '稳定、透明的全平台安全连接服务。多端客户端，线路倍率公开，按量计费清晰可查。',
  robots: { index: false, follow: false },
};
// The browser bar follows the system appearance (a manual choice against it is not reflected).
export const viewport: Viewport = {
  themeColor: [
    { media: '(prefers-color-scheme: light)', color: '#ffffff' },
    { media: '(prefers-color-scheme: dark)', color: '#19221f' },
  ],
};
export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="zh-CN" {...mantineHtmlProps}>
      <head>
        {/* Sets the scheme before the first paint: the stored choice, else the system's. */}
        <ColorSchemeScript defaultColorScheme="auto" localStorageKey={colorSchemeKey} />
        <script dangerouslySetInnerHTML={{ __html: colorSchemeFallbackScript }} />
      </head>
      <body>
        <Providers>
          <SiteShell>{children}</SiteShell>
        </Providers>
      </body>
    </html>
  );
}
