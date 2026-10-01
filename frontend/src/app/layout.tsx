import type { Metadata } from 'next';
import { ColorSchemeScript, mantineHtmlProps } from '@mantine/core';
import '@mantine/core/styles.css';
import '@mantine/notifications/styles.css';
import './globals.css';
import { Providers } from '@/components/providers';
import { SiteShell } from '@/components/site-shell';
const name = process.env.NEXT_PUBLIC_APP_NAME || 'AsterLink';
export const metadata: Metadata = {
  title: { default: `${name} · 连接，自有边界`, template: `%s · ${name}` },
  description: '账户、套餐与订阅管理。VPN 服务开发联调版。',
  robots: { index: false, follow: false },
};
export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="zh-CN" {...mantineHtmlProps}>
      <head>
        <ColorSchemeScript defaultColorScheme="light" />
      </head>
      <body>
        <Providers>
          <SiteShell>{children}</SiteShell>
        </Providers>
      </body>
    </html>
  );
}
