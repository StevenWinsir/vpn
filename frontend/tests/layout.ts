import type { Page } from '@playwright/test';

// Elements that stick out past the viewport's right edge without a clipping or scrolling
// ancestor; an empty list means the page has no horizontal overflow. Names the offenders so
// a failure says what to fix.
export async function overflowing(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const clipped = (element: Element) => {
      for (let node = element.parentElement; node; node = node.parentElement) {
        if (node === document.body) return false;
        if (['auto', 'scroll', 'hidden', 'clip'].includes(getComputedStyle(node).overflowX))
          return true;
      }
      return false;
    };
    const out: string[] = [];
    if (document.documentElement.scrollWidth > window.innerWidth + 1)
      out.push(`document scrollWidth ${document.documentElement.scrollWidth}`);
    for (const element of document.body.querySelectorAll('*')) {
      const box = element.getBoundingClientRect();
      if (box.width === 0 || box.right <= window.innerWidth + 1 || clipped(element)) continue;
      const text = (element.textContent || '').trim().slice(0, 40);
      out.push(
        `${element.tagName.toLowerCase()}.${String(element.className).slice(0, 60)} right=${Math.round(box.right)} "${text}"`,
      );
      if (out.length > 10) break;
    }
    return out;
  });
}
