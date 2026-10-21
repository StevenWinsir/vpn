'use client';

import { Alert, Badge, Group, Paper, Skeleton, Stack, Text, ThemeIcon, Title } from '@mantine/core';
import {
  IconBrandAndroid,
  IconBrandApple,
  IconBrandWindows,
  IconDeviceDesktop,
  IconPlugConnected,
  IconWifiOff,
} from '@tabler/icons-react';
import { ago, duration, elapsedSeconds, formatBytes, platformName } from '@/lib/dashboard';
import type { DeviceStatus } from '@/lib/types';

function PlatformIcon({ platform }: { platform: string }) {
  const Icon =
    platform === 'macos' || platform === 'ios'
      ? IconBrandApple
      : platform === 'windows'
        ? IconBrandWindows
        : platform === 'android'
          ? IconBrandAndroid
          : IconDeviceDesktop;
  return <Icon size={19} stroke={1.6} aria-hidden />;
}

const deviceName = (platform: string, version: string, device: string) =>
  `${platformName(platform)}${version ? ` ${version}` : ''} · 设备 …${device}`;

/**
 * Real presence from GET /me/devices: a device is online while it holds a live connection
 * lease; signed-in devices that are not connected are listed apart and take no slot.
 * Durations are measured against the server's clock, not the browser's.
 */
export function DevicesCard({
  devices,
  error,
  fallbackLimit,
  allowDedicated,
}: {
  devices: DeviceStatus | null;
  error: string;
  fallbackLimit: number;
  allowDedicated: boolean;
}) {
  const limit = devices?.device_limit ?? fallbackLimit;
  const online = devices?.online_count ?? 0;
  const full = limit > 0 && online >= limit;
  return (
    <Paper withBorder p={26} radius="lg" component="section" aria-labelledby="devices-title">
      <Group justify="space-between" mb={18} gap="xs">
        <Title order={2} size={17} id="devices-title">
          连接与客户端
        </Title>
        {devices ? (
          <Badge
            variant={full ? 'filled' : 'light'}
            color={full ? 'orange' : online ? 'teal' : 'gray'}
            aria-label={`在线设备 ${online} / 上限 ${limit}`}
          >
            在线设备 {online} / {limit}
          </Badge>
        ) : null}
      </Group>
      {error ? (
        <Alert color="red" title="设备状态加载失败" role="alert" mb="md">
          {error}
        </Alert>
      ) : null}
      {!devices ? (
        error ? null : (
          <Skeleton height={150} radius="md" />
        )
      ) : devices.online.length === 0 ? (
        <div className="empty-state">
          <IconWifiOff size={34} stroke={1.3} aria-hidden />
          <Text size="sm" fw={600} c="dark.4" mt="sm">
            当前没有在线设备
          </Text>
          <Text size="xs" mt={8} lh={1.8}>
            在客户端点击连接后，设备会在这里显示。
            <br />
            只登录、未连接的客户端不占用在线名额。
          </Text>
        </div>
      ) : (
        <Stack gap={0} aria-label="在线设备列表" role="list">
          {devices.online.map((device) => (
            <div
              className="device-row"
              role="listitem"
              key={`${device.device}-${device.connected_at}`}
              aria-label={`在线设备 ${deviceName(device.platform, device.app_version, device.device)}`}
            >
              <Group gap="sm" wrap="nowrap" align="flex-start">
                <ThemeIcon variant="light" color="teal" radius="md" size={34}>
                  <PlatformIcon platform={device.platform} />
                </ThemeIcon>
                <div style={{ minWidth: 0, flex: 1 }}>
                  <Group justify="space-between" gap={6}>
                    <Text size="sm" fw={600} style={{ overflowWrap: 'anywhere' }}>
                      {deviceName(device.platform, device.app_version, device.device)}
                    </Text>
                    <Badge size="sm" variant="dot" color="teal">
                      {device.line || '线路'}
                    </Badge>
                  </Group>
                  <Text size="xs" c="dimmed" mt={4}>
                    已连接 {duration(elapsedSeconds(device.connected_at, devices.server_time))} ·
                    最近心跳 {ago(elapsedSeconds(device.last_heartbeat_at, devices.server_time))}
                  </Text>
                  <Text size="xs" c="dimmed" mt={2}>
                    本次连接 上传 {formatBytes(device.upload_bytes)} · 下载{' '}
                    {formatBytes(device.download_bytes)}
                  </Text>
                </div>
              </Group>
            </div>
          ))}
        </Stack>
      )}
      {devices && devices.signed_in.length > 0 ? (
        <div style={{ marginTop: 16 }}>
          <Group gap={6} mb={4}>
            <IconPlugConnected size={15} aria-hidden />
            <Text size="xs" fw={600} c="dimmed">
              已登录未连接（不占名额）
            </Text>
          </Group>
          <Stack gap={0} role="list" aria-label="已登录未连接的设备">
            {devices.signed_in.map((device) => (
              <Group
                key={`${device.device}-${device.last_seen_at}`}
                className="device-row"
                role="listitem"
                justify="space-between"
                gap="xs"
              >
                <Group gap={8} wrap="nowrap">
                  <PlatformIcon platform={device.platform} />
                  <Text size="xs">
                    {deviceName(device.platform, device.app_version, device.device)}
                  </Text>
                </Group>
                <Text size="xs" c="dimmed">
                  最近活动 {ago(elapsedSeconds(device.last_seen_at, devices.server_time))}
                </Text>
              </Group>
            ))}
          </Stack>
        </div>
      ) : null}
      <Group justify="space-between" mt="lg">
        <Text size="sm" c="dimmed">
          设备额度
        </Text>
        <Text size="sm" fw={600}>
          {limit ? `同时在线 ${limit} 台` : '—'}
        </Text>
      </Group>
      <Text size="xs" c="dimmed" mt={6} lh={1.7}>
        超过上限的新设备连接时会被拒绝；异常离线的设备最长约 {devices?.lease_seconds ?? 90}{' '}
        秒后自动让出名额。
      </Text>
      <Group justify="space-between" mt="sm">
        <Text size="sm" c="dimmed">
          专线权限
        </Text>
        <Text size="sm" fw={600}>
          {allowDedicated ? '套餐包含' : '未包含'}
        </Text>
      </Group>
    </Paper>
  );
}
