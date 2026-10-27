'use client';

import { useState } from 'react';
import { Alert, Badge, Group, Paper, Skeleton, Table, Text, Title } from '@mantine/core';
import { IconChartBar } from '@tabler/icons-react';
import {
  chargedBytes,
  chartPeak,
  formatBytes,
  historyEmpty,
  intervalLabel,
  intervalTotal,
} from '@/lib/dashboard';
import type { UsageHistory, UsageInterval } from '@/lib/types';

function describe(interval: UsageInterval, newest: boolean) {
  const label = intervalLabel(interval, newest).text;
  return `${label}：上传 ${formatBytes(interval.upload_bytes)}，下载 ${formatBytes(
    interval.download_bytes,
  )}，合计 ${formatBytes(intervalTotal(interval))}，计费 ${formatBytes(
    chargedBytes(interval.charged_units),
  )}`;
}

/**
 * The last 24 hours as eight 3-hour stacked bars (upload + download), drawn with CSS only.
 * Every bar is a focusable button whose accessible name carries its values; a table view
 * repeats them for screen readers and copy-paste.
 */
export function UsageChart({ history, error }: { history: UsageHistory | null; error: string }) {
  const [active, setActive] = useState<number | null>(null);
  const intervals = history?.buckets ?? [];
  const peak = chartPeak(intervals);
  const newest = intervals.length - 1;
  const shown = active ?? newest;
  return (
    <Paper withBorder p={26} radius="lg" component="section" aria-labelledby="usage-history-title">
      <Group justify="space-between" mb={18}>
        <Title order={2} size={17} id="usage-history-title">
          近 24 小时流量
        </Title>
        {history ? (
          <Badge variant="light" color="gray">
            合计 {formatBytes(history.total_upload_bytes + history.total_download_bytes)}
          </Badge>
        ) : null}
      </Group>
      {error ? (
        <Alert color="red" title="流量历史加载失败" role="alert">
          {error}
        </Alert>
      ) : !history ? (
        <Skeleton height={230} radius="md" />
      ) : historyEmpty(intervals) ? (
        <div className="empty-state">
          <IconChartBar size={34} stroke={1.3} aria-hidden />
          <Text size="sm" fw={600} c="var(--al-text)" mt="sm">
            近 24 小时没有流量
          </Text>
          <Text size="xs" mt={8} lh={1.8}>
            客户端连接并产生流量后，这里会按每 3 小时一段显示上传和下载。
          </Text>
        </div>
      ) : (
        <>
          <Group gap="md" mb={10}>
            <Group gap={6}>
              <span className="usage-swatch usage-upload" aria-hidden />
              <Text size="xs" c="dimmed">
                上传
              </Text>
            </Group>
            <Group gap={6}>
              <span className="usage-swatch usage-download" aria-hidden />
              <Text size="xs" c="dimmed">
                下载
              </Text>
            </Group>
            <Text size="xs" c="dimmed" ml="auto">
              峰值 {formatBytes(peak)}
            </Text>
          </Group>
          <div
            className="usage-plot"
            role="group"
            aria-label="近 24 小时流量柱状图，每根柱为 3 小时"
          >
            {intervals.map((interval, index) => {
              const total = intervalTotal(interval);
              return (
                <button
                  key={interval.start}
                  type="button"
                  className="usage-bar"
                  data-active={shown === index || undefined}
                  aria-label={describe(interval, index === newest)}
                  onMouseEnter={() => setActive(index)}
                  onMouseLeave={() => setActive(null)}
                  onFocus={() => setActive(index)}
                  onBlur={() => setActive(null)}
                >
                  <span
                    className="usage-stack"
                    style={{ height: `${(total / peak) * 100}%`, minHeight: total > 0 ? 3 : 0 }}
                  >
                    <span
                      className="usage-upload"
                      style={{ flexGrow: interval.upload_bytes, flexBasis: 0 }}
                    />
                    <span
                      className="usage-download"
                      style={{ flexGrow: interval.download_bytes, flexBasis: 0 }}
                    />
                  </span>
                </button>
              );
            })}
          </div>
          <div className="usage-axis" aria-hidden>
            {intervals.map((interval, index) => {
              const label = intervalLabel(interval, index === newest);
              return (
                <span key={interval.start}>
                  {label.from}
                  <br />
                  {index === newest ? label.to : `–${label.to}`}
                </span>
              );
            })}
          </div>
          {intervals[shown] ? (
            <Text size="xs" c="dimmed" mt="md" lh={1.7} aria-live="polite">
              {describe(intervals[shown], shown === newest)}
            </Text>
          ) : null}
          <details className="usage-table">
            <summary>以表格查看</summary>
            <Table.ScrollContainer minWidth={460}>
              <Table fz="xs" verticalSpacing={6}>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>时段</Table.Th>
                    <Table.Th>上传</Table.Th>
                    <Table.Th>下载</Table.Th>
                    <Table.Th>合计</Table.Th>
                    <Table.Th>计费流量</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {intervals.map((interval, index) => (
                    <Table.Tr key={interval.start}>
                      <Table.Td>{intervalLabel(interval, index === newest).text}</Table.Td>
                      <Table.Td>{formatBytes(interval.upload_bytes)}</Table.Td>
                      <Table.Td>{formatBytes(interval.download_bytes)}</Table.Td>
                      <Table.Td>{formatBytes(intervalTotal(interval))}</Table.Td>
                      <Table.Td>{formatBytes(chargedBytes(interval.charged_units))}</Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          </details>
        </>
      )}
      <Text size="xs" c="dimmed" mt="md" lh={1.7}>
        数据每 5 分钟更新一次，保留最近 24 小时。计费流量按线路倍率计算。
      </Text>
    </Paper>
  );
}
