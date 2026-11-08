'use client';
import { useId, useState } from 'react';
import { IconChevronDown } from '@tabler/icons-react';
import { useHomeState } from './home-state';
import { faqFacts, homeFaq } from '@/lib/home-faq';
import type { Meta } from '@/lib/types';

// Frequently asked questions (handoff_ui.md 8.2). The words come from src/lib/home-faq.ts, which
// also feeds the page's FAQPage structured data (handoff_seo.md 6.2). The server's copy of /meta
// gives the first HTML; the browser's own answer takes over once it is there, and a failed one
// keeps the server's, so the visible text and the structured data start out identical.
export function HomeFaq({
  supportEmail,
  chat,
  initialMeta,
}: {
  supportEmail: string | null;
  chat: boolean;
  initialMeta: Meta | null;
}) {
  const { meta } = useHomeState();
  const [open, setOpen] = useState<number | null>(0);
  const base = useId();
  return (
    <div className="home-faq" data-testid="home-faq">
      {homeFaq(faqFacts(meta ?? initialMeta, supportEmail, chat)).map(({ q, a }, index) => {
        const expanded = open === index;
        return (
          <div className="home-faq-item" key={q} data-open={expanded ? '' : undefined}>
            <h3>
              <button
                type="button"
                id={`${base}-q${index}`}
                aria-expanded={expanded}
                aria-controls={`${base}-a${index}`}
                onClick={() => setOpen(expanded ? null : index)}
              >
                <span>{q}</span>
                <IconChevronDown size={18} aria-hidden />
              </button>
            </h3>
            <div
              className="home-faq-panel"
              id={`${base}-a${index}`}
              role="region"
              aria-labelledby={`${base}-q${index}`}
              hidden={!expanded}
            >
              <p>
                {a.map((part, i) =>
                  typeof part === 'string' ? (
                    part
                  ) : (
                    <a key={i} href={`mailto:${part.mail}`}>
                      {part.mail}
                    </a>
                  ),
                )}
              </p>
            </div>
          </div>
        );
      })}
    </div>
  );
}
