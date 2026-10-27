'use client';
import Link from 'next/link';
import { useCallback, useState } from 'react';
import { Anchor, Group, Paper, Text } from '@mantine/core';
import { IconGift } from '@tabler/icons-react';
import { api } from '@/lib/api';
import { formatBytes } from '@/lib/dashboard';
import { isReferralSummary } from '@/lib/referral';
import type { ReferralSummary } from '@/lib/types';
import { useMeta } from '@/lib/use-meta';
import { usePolling } from '@/lib/use-polling';

// The dashboard's referral summary. It asks for nothing until /meta announces the programme,
// and a failed, malformed or disabled answer removes the card: the overview never shows an
// error for it (handoff_coupon.md 12.3).
export function ReferralCard({ refreshMs }: { refreshMs: number }) {
  const { meta } = useMeta();
  return meta?.referral_enabled === true ? <ReferralCardBody refreshMs={refreshMs} /> : null;
}

function ReferralCardBody({ refreshMs }: { refreshMs: number }) {
  const [summary, setSummary] = useState<ReferralSummary | null>(null);
  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const body = await api<unknown>('/me/referral', { signal });
      if (!signal.aborted) setSummary(isReferralSummary(body) && body.enabled ? body : null);
    } catch {
      if (!signal.aborted) setSummary(null);
    }
  }, []);
  usePolling(load, refreshMs);
  if (!summary) return null;
  return (
    <Paper withBorder radius="lg" p={{ base: 20, sm: 24 }} data-testid="referral-card">
      <Group justify="space-between" align="center" gap="lg">
        <Group gap="md" wrap="nowrap">
          <IconGift size={26} stroke={1.5} color="var(--mantine-color-teal-7)" />
          <div>
            <Text size="xs" c="dimmed">
              推荐有礼 · 我的推荐码
            </Text>
            <div className="referral-code referral-code-small" data-testid="referral-card-code">
              {summary.code}
            </div>
          </div>
        </Group>
        <div>
          <Text size="xs" c="dimmed">
            已获得流量奖励
          </Text>
          <Text fw={650} size="xl" data-testid="referral-card-earned">
            {formatBytes(summary.stats.rewarded_bytes)}
          </Text>
        </div>
        <Anchor component={Link} href="/referral" size="sm" fw={600}>
          查看推荐详情
        </Anchor>
      </Group>
    </Paper>
  );
}
