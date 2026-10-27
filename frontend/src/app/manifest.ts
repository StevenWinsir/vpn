import type { MetadataRoute } from 'next';
import { appName } from '@/lib/types';

// "Add to home screen" on Android uses these icons; they come from brand/mark.svg like the
// favicon (scripts/brand-icons.mjs).
export default function manifest(): MetadataRoute.Manifest {
  return {
    name: appName,
    short_name: appName,
    start_url: '/',
    display: 'browser',
    background_color: '#f7f8fa',
    theme_color: '#0d7b61',
    icons: [
      { src: '/icons/icon-192.png', sizes: '192x192', type: 'image/png' },
      { src: '/icons/icon-512.png', sizes: '512x512', type: 'image/png' },
    ],
  };
}
