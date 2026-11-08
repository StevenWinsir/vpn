import { notFound } from 'next/navigation';
import { GuideView } from '@/components/guide-view';
import { JsonLd } from '@/components/json-ld';
import { guidePath, isPlatformId, platforms } from '@/lib/guides';
import { guideDownloads } from '@/lib/server/guide';
import { indexableMetadata, siteOrigin } from '@/lib/server/seo';
import { breadcrumbs } from '@/lib/structured-data';

// Read on every request, like the overview (the download status comes from the same cache).
export const dynamic = 'force-dynamic';

type Props = { params: Promise<{ platform: string }> };

// One page per platform (handoff_seo.md D5), each with its own title, description and canonical
// address. An unknown platform is a missing page (404).
export async function generateMetadata({ params }: Props) {
  const { platform } = await params;
  if (!isPlatformId(platform)) notFound();
  return indexableMetadata(guidePath(platform));
}

export default async function PlatformGuidePage({ params }: Props) {
  const { platform } = await params;
  if (!isPlatformId(platform)) notFound();
  const origin = siteOrigin();
  return (
    <>
      {origin ? (
        <JsonLd
          data={[
            breadcrumbs(origin, [
              { name: '使用教程', path: '/guide' },
              { name: platforms[platform].name, path: guidePath(platform) },
            ]),
          ]}
        />
      ) : null}
      <GuideView id={platform} downloads={await guideDownloads()} />
    </>
  );
}
