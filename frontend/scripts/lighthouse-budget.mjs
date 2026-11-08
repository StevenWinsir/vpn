// The performance budget of handoff_seo.md D18 and the arithmetic of the Lighthouse audit
// (scripts/lighthouse-audit.mjs): plain functions, checked by test:seo.

/** Lighthouse mobile lab data on a local production build (D18). */
export const budget = {
  lcpMs: 2500,
  cls: 0.1,
  tbtMs: 200,
  seo: 1,
  accessibility: 0.9,
};

/** The pages D18 measures; the platform guide is the one most visitors arrive on from a search. */
export const auditPages = ['/', '/plans', '/download', '/guide/windows'];

export function median(values) {
  const sorted = values
    .filter((v) => typeof v === 'number' && Number.isFinite(v))
    .sort((a, b) => a - b);
  if (sorted.length === 0) return null;
  const mid = Math.floor(sorted.length / 2);
  return sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2;
}

/** The figures of one Lighthouse JSON report (lhr) that the budget and the record need. */
export function figures(lhr) {
  const audit = (id) => lhr.audits?.[id]?.numericValue ?? null;
  const score = (id) => lhr.categories?.[id]?.score ?? null;
  return {
    performance: score('performance'),
    accessibility: score('accessibility'),
    seo: score('seo'),
    fcpMs: audit('first-contentful-paint'),
    lcpMs: audit('largest-contentful-paint'),
    cls: audit('cumulative-layout-shift'),
    tbtMs: audit('total-blocking-time'),
    speedIndexMs: audit('speed-index'),
  };
}

/** Each figure's median over the runs of one page (D18: three runs, the median). */
export function medians(runs) {
  const keys = Object.keys(figures({}));
  return Object.fromEntries(keys.map((key) => [key, median(runs.map((run) => run[key]))]));
}

/** The audits of the SEO and accessibility categories that did not pass in any run. */
export function failingAudits(lhrs) {
  const out = new Map();
  for (const lhr of lhrs)
    for (const category of ['seo', 'accessibility'])
      for (const ref of lhr.categories?.[category]?.auditRefs ?? []) {
        const audit = lhr.audits?.[ref.id];
        if (!audit || ref.weight === 0 || audit.score === null || audit.score === 1) continue;
        out.set(ref.id, { category, title: audit.title });
      }
  return [...out].map(([id, value]) => ({ id, ...value }));
}

/**
 * What exceeds the budget, in words; an empty list means the page is within it. With
 * stableOnly the timing figures (LCP, TBT) are left out: on shared CI machines they move with
 * the machine's load, so CI only gates on SEO, accessibility and CLS (handoff_seo.md 7.1).
 */
export function overBudget(m, { stableOnly = false } = {}) {
  const out = [];
  const missing = (name) => out.push(`${name} not measured`);
  if (!stableOnly) {
    if (m.lcpMs === null) missing('LCP');
    else if (m.lcpMs > budget.lcpMs) out.push(`LCP ${Math.round(m.lcpMs)} ms > ${budget.lcpMs} ms`);
    if (m.tbtMs === null) missing('TBT');
    else if (m.tbtMs > budget.tbtMs) out.push(`TBT ${Math.round(m.tbtMs)} ms > ${budget.tbtMs} ms`);
  }
  if (m.cls === null) missing('CLS');
  else if (m.cls > budget.cls) out.push(`CLS ${m.cls.toFixed(3)} > ${budget.cls}`);
  if (m.seo === null) missing('SEO');
  else if (m.seo < budget.seo) out.push(`SEO ${Math.round(m.seo * 100)} < 100`);
  if (m.accessibility === null) missing('accessibility');
  else if (m.accessibility < budget.accessibility)
    out.push(`accessibility ${Math.round(m.accessibility * 100)} < ${budget.accessibility * 100}`);
  return out;
}

/** One table row per page for the summary and section 14 of handoff_seo.md. */
export function tableRow(page, m, problems) {
  const ms = (v) => (v === null ? '—' : `${(v / 1000).toFixed(2)} s`);
  const pct = (v) => (v === null ? '—' : String(Math.round(v * 100)));
  return `| ${page} | ${pct(m.performance)} | ${ms(m.fcpMs)} | ${ms(m.lcpMs)} | ${
    m.cls === null ? '—' : m.cls.toFixed(3)
  } | ${m.tbtMs === null ? '—' : `${Math.round(m.tbtMs)} ms`} | ${pct(m.seo)} | ${pct(
    m.accessibility,
  )} | ${problems.length ? problems.join('; ') : 'within budget'} |`;
}

export const tableHead = [
  '| Page | Performance | FCP | LCP | CLS | TBT | SEO | Accessibility | Budget (D18) |',
  '| --- | --- | --- | --- | --- | --- | --- | --- | --- |',
];
