import { brandMarkArt, brandMarkViewBox } from './brand-mark-art';

// The brand mark: the same artwork as the favicon set, generated from brand/mark.svg
// (scripts/brand-icons.mjs). Decorative; the brand name next to it carries the meaning.
export function BrandMark({ size = 39 }: { size?: number }) {
  return (
    <svg
      className="brand-mark"
      width={size}
      height={size}
      viewBox={brandMarkViewBox}
      aria-hidden="true"
      focusable="false"
      dangerouslySetInnerHTML={{ __html: brandMarkArt }}
    />
  );
}
