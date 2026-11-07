import type { NextConfig } from 'next';
const config: NextConfig = {
  poweredByHeader: false,
  reactStrictMode: true,
  // Page metadata always goes into <head>, for every visitor and crawler (handoff_seo.md round 1).
  // By default Next.js streams it into <body> for anyone not on its list of limited bots, which
  // includes Googlebot. Here the metadata is cheap to compute (the environment and the Host
  // header), so waiting for it costs nothing.
  htmlLimitedBots: /.*/,
  experimental: {
    optimizePackageImports: ['@mantine/core', '@mantine/hooks', '@tabler/icons-react'],
  },
  async headers() {
    return [
      {
        source: '/(.*)',
        headers: [
          { key: 'X-Content-Type-Options', value: 'nosniff' },
          { key: 'X-Frame-Options', value: 'DENY' },
          { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
          { key: 'Permissions-Policy', value: 'camera=(), microphone=(), geolocation=()' },
        ],
      },
      {
        // The API proxy and the runtime configuration are never search results (handoff_seo.md D10).
        source: '/api/:path*',
        headers: [{ key: 'X-Robots-Tag', value: 'noindex, nofollow' }],
      },
      {
        // The reset page briefly holds a secret: never leak the URL, never index it.
        source: '/reset-password',
        headers: [
          { key: 'Referrer-Policy', value: 'no-referrer' },
          { key: 'X-Robots-Tag', value: 'noindex, nofollow' },
        ],
      },
      {
        // The download redirects (handoff_download.md 6.3): the file host never learns which page
        // sent the browser. This rule comes after the site-wide one, which it overrides.
        source: '/download/get/:path*',
        headers: [
          { key: 'Referrer-Policy', value: 'no-referrer' },
          { key: 'X-Robots-Tag', value: 'noindex' },
        ],
      },
    ];
  },
};
export default config;
