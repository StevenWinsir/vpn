import Link from 'next/link';
import { parseInline } from '@/lib/guides';
import type { LegalDocument } from '@/lib/legal';

// The terms and the privacy policy (handoff_seo.md 6.3): server-rendered text in the public page
// frame (the header without the user centre's sidebar), one h1, a contents list and one h2 per
// section, both dates at the top.
function Rich({ text }: { text: string }) {
  return (
    <>
      {parseInline(text).map((part, index) => {
        if (part.type === 'strong') return <strong key={index}>{part.text}</strong>;
        if (part.type === 'link')
          return (
            <Link key={index} href={part.href}>
              {part.text}
            </Link>
          );
        return part.text;
      })}
    </>
  );
}

export function LegalPage({
  doc,
  other,
}: {
  doc: LegalDocument;
  other: { href: string; label: string };
}) {
  return (
    <article className="legal-page" data-testid="legal-page">
      <header>
        <h1>{doc.title}</h1>
        <p className="legal-dates" data-testid="legal-dates">
          生效日期：{doc.effective}
          <span aria-hidden> · </span>
          最后更新：{doc.updated}
        </p>
        {doc.intro.map((text) => (
          <p key={text}>
            <Rich text={text} />
          </p>
        ))}
      </header>
      <nav aria-label="目录" className="legal-toc">
        {doc.sections.map((section) => (
          <a key={section.id} href={`#${section.id}`}>
            {section.title}
          </a>
        ))}
      </nav>
      {doc.sections.map((section) => (
        <section key={section.id} id={section.id} aria-labelledby={`${section.id}-title`}>
          <h2 id={`${section.id}-title`}>{section.title}</h2>
          {section.blocks.map((block, index) =>
            block.kind === 'p' ? (
              <p key={index}>
                <Rich text={block.text} />
              </p>
            ) : (
              <ul key={index}>
                {block.items.map((item) => (
                  <li key={item}>
                    <Rich text={item} />
                  </li>
                ))}
              </ul>
            ),
          )}
        </section>
      ))}
      <footer className="legal-footer">
        <Link href={other.href}>{other.label}</Link>
        <Link href="/">返回首页</Link>
      </footer>
    </article>
  );
}
