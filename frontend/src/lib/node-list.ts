import type { PublicNode } from './types';

// Deployment-time setting: only this non-secret interval is passed to the browser.
export function nodeListRefreshMs(value: string | undefined): number {
  const interval = Number(value ?? 30000);
  return Number.isInteger(interval) && interval >= 5000 && interval <= 300000 ? interval : 30000;
}

const rateFormatter = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 3 });
export const nodeRate = (permille: number) => rateFormatter.format(permille / 1000);

export function filterNodes(nodes: PublicNode[], search: string, lineType: string): PublicNode[] {
  const query = search.trim().toLocaleLowerCase('zh-CN');
  return nodes.filter(
    (node) =>
      (lineType === 'all' || node.line_type === lineType) &&
      (!query || `${node.name} ${node.region}`.toLocaleLowerCase('zh-CN').includes(query)),
  );
}
