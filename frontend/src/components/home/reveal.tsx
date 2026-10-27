'use client';
import { useEffect } from 'react';

// Sections marked data-reveal fade in once as they enter the viewport (handoff_ui.md 8.4). Content
// is visible without this script; only blocks still below the fold are hidden, so nothing on
// screen blinks. With reduced motion asked for, or without IntersectionObserver, nothing happens.
export function Reveal() {
  useEffect(() => {
    if (
      typeof IntersectionObserver === 'undefined' ||
      window.matchMedia('(prefers-reduced-motion: reduce)').matches
    )
      return;
    const pending = [...document.querySelectorAll<HTMLElement>('[data-reveal]')].filter(
      (element) => element.getBoundingClientRect().top > window.innerHeight,
    );
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (!entry.isIntersecting) continue;
          entry.target.setAttribute('data-reveal', 'shown');
          observer.unobserve(entry.target);
        }
      },
      { rootMargin: '0px 0px -8% 0px' },
    );
    for (const element of pending) {
      element.setAttribute('data-reveal', 'hidden');
      observer.observe(element);
    }
    return () => {
      observer.disconnect();
      for (const element of pending) element.setAttribute('data-reveal', '');
    };
  }, []);
  return null;
}
