'use client';

import Link from 'next/link';
import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Alert,
  Badge,
  Button,
  Divider,
  Group,
  Paper,
  Select,
  SimpleGrid,
  Skeleton,
  Stack,
  Text,
  TextInput,
  ThemeIcon,
  Title,
} from '@mantine/core';
import {
  IconArrowRight,
  IconRefresh,
  IconSearch,
  IconServer2,
  IconShieldCheck,
} from '@tabler/icons-react';
import { api, APIError, message } from '@/lib/api';
import { filterNodes, nodeRate } from '@/lib/node-list';
import { date, type NodeCatalog, type PublicNode } from '@/lib/types';
import { useAuth } from './providers';
import { RequireAuth } from './require-auth';

export function NodesView({ refreshMs }: { refreshMs: number }) {
  const { user } = useAuth();
  return (
    <RequireAuth>{user ? <NodeList key={user.id} refreshMs={refreshMs} /> : null}</RequireAuth>
  );
}

function NodeList({ refreshMs }: { refreshMs: number }) {
  const { setUser } = useAuth();
  const [catalog, setCatalog] = useState<NodeCatalog | null>(null);
  const [error, setError] = useState('');
  const [inactive, setInactive] = useState(false);
  const [syncing, setSyncing] = useState(true);
  const [syncedAt, setSyncedAt] = useState<number | null>(null);
  const [search, setSearch] = useState('');
  const [lineType, setLineType] = useState('all');
  const lifetime = useRef<AbortController | null>(null);
  const inFlight = useRef<AbortSignal | null>(null);

  const load = useCallback(
    async (signal: AbortSignal) => {
      // Focus, visibility, manual refresh and polling share one request per mounted account.
      if (signal.aborted || inFlight.current === signal) return;
      inFlight.current = signal;
      try {
        const result = await api<NodeCatalog>('/client/bootstrap', { signal });
        if (signal.aborted) return;
        setCatalog(result);
        setSyncedAt(Date.now());
        setInactive(false);
        setError('');
      } catch (failure) {
        if (signal.aborted) return;
        // Do not present stale nodes after entitlement or network failure.
        setCatalog(null);
        if (failure instanceof APIError && failure.status === 401) {
          setUser(null);
          return;
        }
        const entitlementInactive =
          failure instanceof APIError && failure.code === 'entitlement_inactive';
        setInactive(entitlementInactive);
        setError(entitlementInactive ? '' : message(failure));
      } finally {
        if (inFlight.current === signal) inFlight.current = null;
        if (!signal.aborted) setSyncing(false);
      }
    },
    [setUser],
  );

  useEffect(() => {
    const controller = new AbortController();
    lifetime.current = controller;
    const syncVisible = () => {
      if (document.visibilityState === 'visible' && !inFlight.current) {
        setSyncing(true);
        void load(controller.signal);
      }
    };
    void load(controller.signal);
    const timer = window.setInterval(syncVisible, refreshMs);
    window.addEventListener('focus', syncVisible);
    window.addEventListener('online', syncVisible);
    document.addEventListener('visibilitychange', syncVisible);
    return () => {
      controller.abort();
      window.clearInterval(timer);
      window.removeEventListener('focus', syncVisible);
      window.removeEventListener('online', syncVisible);
      document.removeEventListener('visibilitychange', syncVisible);
    };
  }, [load, refreshMs]);

  const nodes = catalog?.nodes ?? [];
  const visible = filterNodes(nodes, search, lineType);
  const dedicated = nodes.filter((node) => node.line_type === 'dedicated').length;
  const refresh = () => {
    if (lifetime.current && !inFlight.current) {
      setSyncing(true);
      void load(lifetime.current.signal);
    }
  };

  return (
    <div className="page-wrap">
      <Group justify="space-between" align="flex-start" mb={30}>
        <div>
          <Title order={1} size={29} className="page-title">
            节点列表
          </Title>
          <Text c="dimmed" size="sm" mt={8}>
            查看当前套餐的线路与流量倍率，找到适合你的连接。
          </Text>
        </div>
        <Button
          variant="default"
          leftSection={<IconRefresh size={16} />}
          loading={syncing}
          onClick={refresh}
        >
          同步节点
        </Button>
      </Group>
      <Stack gap={24}>
        <Alert color="teal" icon={<IconShieldCheck size={20} />} title="线路信息，与管理员配置同步">
          这里只展示节点名称、地区、线路类型和计费倍率，不提供连接参数或配置下载。
          节点可见不代表实时在线，请在客户端连接并测试线路。
        </Alert>
        <Group justify="space-between" gap="xs">
          <Text size="xs" c="dimmed">
            页面可见时每 {refreshMs / 1000} 秒同步，返回页面时也会刷新。
          </Text>
          <Text size="xs" c="dimmed" role="status" aria-live="polite">
            {syncing
              ? '正在同步节点…'
              : error || inactive
                ? '当前节点列表不可用'
                : syncedAt
                  ? `最近同步 ${new Date(syncedAt).toLocaleTimeString('zh-CN')}`
                  : '尚未同步'}
          </Text>
        </Group>
        {error ? (
          <Alert color="red" title="节点同步失败" role="alert">
            <Text size="sm">{error}</Text>
            <Text size="sm" mt={6}>
              已收起旧列表，避免展示过时信息。请点击“同步节点”重试。
            </Text>
          </Alert>
        ) : inactive ? (
          <Paper withBorder radius="lg" p="xl">
            <Stack align="flex-start">
              <Title order={2} size={20}>
                套餐权益不可用
              </Title>
              <Text c="dimmed" size="sm">
                你尚未开通有效套餐、套餐已到期或流量已用尽。恢复有效权益后才能查看节点。
              </Text>
              <Button component={Link} href="/plans" rightSection={<IconArrowRight size={16} />}>
                查看套餐
              </Button>
            </Stack>
          </Paper>
        ) : !catalog ? (
          <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }}>
            {[0, 1, 2].map((key) => (
              <Skeleton key={key} height={215} radius="lg" />
            ))}
          </SimpleGrid>
        ) : (
          <>
            <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="lg">
              <NodeCount label="套餐可见节点" count={nodes.length} />
              <NodeCount label="普通线路" count={nodes.length - dedicated} />
              <NodeCount label="专线" count={dedicated} />
            </SimpleGrid>
            <Paper withBorder radius="lg" p={{ base: 'md', sm: 'xl' }}>
              <Stack gap="lg">
                <Group justify="space-between" align="flex-end" gap="md">
                  <TextInput
                    label="搜索节点"
                    placeholder="输入名称或地区"
                    leftSection={<IconSearch size={17} />}
                    value={search}
                    onChange={(event) => setSearch(event.currentTarget.value)}
                    style={{ flex: '1 1 240px', minWidth: 0 }}
                  />
                  <Select
                    label="线路类型"
                    value={lineType}
                    onChange={(value) => setLineType(value ?? 'all')}
                    data={[
                      { value: 'all', label: '全部线路' },
                      { value: 'direct', label: '普通线路' },
                      { value: 'dedicated', label: '专线' },
                    ]}
                    allowDeselect={false}
                    style={{ flex: '0 1 180px', minWidth: 0 }}
                  />
                </Group>
                <Group justify="space-between" gap="xs">
                  <Text size="sm" c="dimmed" role="status">
                    显示 {visible.length} / {nodes.length} 个节点
                  </Text>
                  <Text size="xs" c="dimmed">
                    {catalog.is_test ? '测试套餐 · ' : ''}有效期至 {date(catalog.expires_at)}
                  </Text>
                </Group>
              </Stack>
            </Paper>
            {nodes.length === 0 ? (
              <Paper withBorder radius="lg" p="xl" ta="center">
                <IconServer2 size={32} stroke={1.4} aria-hidden />
                <Title order={2} size={19} mt="sm">
                  暂无可见节点
                </Title>
                <Text c="dimmed" size="sm" mt="sm">
                  当前套餐没有已启用且允许访问的节点，请联系管理员或稍后同步。
                </Text>
              </Paper>
            ) : visible.length === 0 ? (
              <Paper withBorder radius="lg" p="xl" ta="center">
                <Title order={2} size={19}>
                  没有匹配的节点
                </Title>
                <Text c="dimmed" size="sm" my="sm">
                  试试其他名称、地区或线路类型。
                </Text>
                <Button
                  variant="light"
                  onClick={() => {
                    setSearch('');
                    setLineType('all');
                  }}
                >
                  清除筛选
                </Button>
              </Paper>
            ) : (
              <SimpleGrid cols={{ base: 1, md: 2, xl: 3 }} spacing="lg">
                {visible.map((node) => (
                  <NodeCard key={node.id} node={node} />
                ))}
              </SimpleGrid>
            )}
          </>
        )}
      </Stack>
      <div className="page-footer">
        倍率由管理员配置：实际上下行合计流量 × 倍率 = 扣除的套餐流量。节点列表不会建立代理连接。
      </div>
    </div>
  );
}

function NodeCount({ label, count }: { label: string; count: number }) {
  return (
    <Paper withBorder radius="lg" p="lg">
      <Text size="xs" c="dimmed">
        {label}
      </Text>
      <Text size="xl" fw={650} mt={6}>
        {count}{' '}
        <Text span size="sm" c="dimmed">
          个
        </Text>
      </Text>
    </Paper>
  );
}

function NodeCard({ node }: { node: PublicNode }) {
  const dedicated = node.line_type === 'dedicated';
  const rate = nodeRate(node.rate_permille);
  return (
    <Paper
      component="article"
      aria-label={`节点 ${node.name}`}
      withBorder
      radius="lg"
      p="xl"
      style={{ minWidth: 0 }}
    >
      <Stack gap="md">
        <Group justify="space-between" gap="sm">
          <ThemeIcon size={40} variant="light" color={dedicated ? 'teal' : 'blue'} radius="md">
            <IconServer2 size={22} stroke={1.5} aria-hidden />
          </ThemeIcon>
          <Badge variant="light" color={dedicated ? 'teal' : 'gray'}>
            {dedicated ? '专线' : '普通线路'}
          </Badge>
        </Group>
        <div>
          <Title order={2} size={18} style={{ overflowWrap: 'anywhere' }}>
            {node.name}
          </Title>
          <Text size="sm" c="dimmed" mt={5} style={{ overflowWrap: 'anywhere' }}>
            地区 · {node.region || '未标注'}
          </Text>
        </div>
        <Divider />
        <Group justify="space-between">
          <Text size="sm" c="dimmed">
            流量倍率
          </Text>
          <Text fw={650} size="lg">
            {rate}×
          </Text>
        </Group>
        <Text size="xs" c="dimmed" lh={1.8}>
          实际使用 1 GiB，扣除 {rate} GiB 套餐流量（上传 + 下载）。
        </Text>
      </Stack>
    </Paper>
  );
}
