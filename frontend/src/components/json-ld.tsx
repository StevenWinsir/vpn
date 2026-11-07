import { serializeJsonLd } from '@/lib/structured-data';

// Structured data for search engines (handoff_seo.md 6.2): one script per object, rendered on the
// server. The text is escaped by serializeJsonLd, so free text cannot leave the script element.
export function JsonLd({ data }: { data: Record<string, unknown>[] }) {
  return (
    <>
      {data.map((item, index) => (
        <script
          key={index}
          type="application/ld+json"
          dangerouslySetInnerHTML={{ __html: serializeJsonLd(item) }}
        />
      ))}
    </>
  );
}
