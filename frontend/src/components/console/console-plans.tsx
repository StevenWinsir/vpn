'use client';
import { useState } from 'react';
import {
  Alert,
  Badge,
  Button,
  Group,
  Modal,
  Paper,
  ScrollArea,
  Skeleton,
  Stack,
  Switch,
  Table,
  Text,
  TextInput,
  Tooltip,
} from '@mantine/core';
import { IconAlertTriangle } from '@tabler/icons-react';
import { notifications } from '@mantine/notifications';
import { api, APIError } from '@/lib/api';
import { gib, money } from '@/lib/types';
import type { AdminPlan, AdminPlansResponse, ShopSettings } from '@/lib/console';
import { foldText, integerText, previewYearly } from '@/lib/plan-math';
import { trafficLabel } from '@/lib/shop';
import { describeError, ErrorBox, useFetched } from './console-common';
import { PlanForm } from './plan-form';

// The plan console (handoff_shop.md 6). Names, descriptions and notes are rendered as plain
// text only. Every price shown here comes from the server.
export function ConsolePlans() {
  const [nonce, setNonce] = useState(0);
  const [showDeleted, setShowDeleted] = useState(false);
  const [editing, setEditing] = useState<AdminPlan | 'new' | null>(null);
  const [removing, setRemoving] = useState<AdminPlan | null>(null);
  const [notice, setNotice] = useState<{ text: string; lines: string[] } | null>(null);
  const { data, error, loading } = useFetched<AdminPlansResponse>(
    `/admin/console/plans?include_deleted=${showDeleted}`,
    nonce,
  );
  const refresh = () => setNonce((n) => n + 1);
  const plans = data?.plans ?? [];
  return (
    <Stack gap="lg">
      {data ? (
        <DiscountBar
          settings={data.settings}
          plans={plans}
          following={data.following_global_plans}
          onSaved={refresh}
        />
      ) : null}
      <Group justify="space-between">
        <Switch
          label="显示已删除"
          checked={showDeleted}
          onChange={(e) => setShowDeleted(e.currentTarget.checked)}
        />
        <Button onClick={() => setEditing('new')}>新建套餐</Button>
      </Group>
      <ErrorBox text={error} />
      {notice ? (
        <Alert
          color="teal"
          withCloseButton
          onClose={() => setNotice(null)}
          data-testid="plan-saved"
          title={notice.text}
        >
          <Stack gap={2}>
            {notice.lines.map((line) => (
              <Text key={line} size="sm">
                {line}
              </Text>
            ))}
          </Stack>
        </Alert>
      ) : null}
      <Paper withBorder radius="lg" p="md">
        {loading && !data ? (
          <Skeleton height={200} />
        ) : (
          <ScrollArea>
            <Table verticalSpacing="sm" horizontalSpacing="md" miw={1500} aria-label="套餐列表">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>套餐</Table.Th>
                  <Table.Th>月付</Table.Th>
                  <Table.Th>年付</Table.Th>
                  <Table.Th>线路类型</Table.Th>
                  <Table.Th>设备额度</Table.Th>
                  <Table.Th>有效订阅</Table.Th>
                  <Table.Th>待付订单</Table.Th>
                  <Table.Th>引用的节点组</Table.Th>
                  <Table.Th>优惠码</Table.Th>
                  <Table.Th>排序</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {plans.map((p) => (
                  <PlanRow
                    key={p.id}
                    plan={p}
                    cap={data?.client_max_online_devices ?? 0}
                    onEdit={() => setEditing(p)}
                    onDelete={() => setRemoving(p)}
                  />
                ))}
              </Table.Tbody>
            </Table>
            {data && plans.length === 0 ? (
              <Text ta="center" c="dimmed" py="xl">
                还没有套餐
              </Text>
            ) : null}
          </ScrollArea>
        )}
      </Paper>
      <Text size="xs" c="dimmed">
        未删除的套餐最多 30 个，其中上架的最多 12 个。套餐 ID 创建后不能修改；删除前必须先下架。
      </Text>
      {editing && data ? (
        <PlanForm
          plan={editing === 'new' ? null : editing}
          globalPercent={data.settings.yearly_discount_percent}
          cap={data.client_max_online_devices}
          onClose={() => setEditing(null)}
          onRefresh={refresh}
          onSaved={(saved) => {
            refresh();
            setNotice(savedNotice(saved));
          }}
        />
      ) : null}
      {removing ? (
        <DeletePlan
          plan={removing}
          onClose={() => setRemoving(null)}
          onRefresh={refresh}
          onDone={(name) => {
            refresh();
            notifications.show({ color: 'teal', title: '套餐已删除', message: name });
          }}
        />
      ) : null}
    </Stack>
  );
}

function savedNotice(plan: AdminPlan) {
  const lines: string[] = [];
  if (plan.yearly)
    lines.push(
      `服务端年付价：${money(plan.yearly.price_cents)}（原价 ${money(plan.yearly.original_cents)}，立省 ${money(plan.yearly.saving_cents)}，折合 ${money(plan.yearly.monthly_equivalent_cents)} / 月，${foldText(plan.yearly.discount_percent)}）`,
    );
  else if (!plan.yearly_enabled) lines.push('年付已关闭。');
  if (plan.active_subscriptions > 0)
    lines.push(`修改只对新订单生效，现有 ${plan.active_subscriptions} 位订阅用户保持原有权益`);
  return { text: `套餐“${plan.name}”已保存`, lines };
}

function trafficText(bytes: number) {
  return `${gib(bytes)} GiB`;
}

function PlanRow({
  plan: p,
  cap,
  onEdit,
  onDelete,
}: {
  plan: AdminPlan;
  cap: number;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const deleted = !!p.deleted_at;
  return (
    <Table.Tr data-testid="plan-row" data-plan-id={p.id} opacity={deleted ? 0.6 : 1}>
      <Table.Td>
        <Group gap={6} wrap="nowrap">
          <Text size="sm" fw={700}>
            {p.name}
          </Text>
          {p.featured ? (
            <Badge color="orange" variant="light">
              推荐
            </Badge>
          ) : null}
          {deleted ? (
            <Badge color="gray">已删除</Badge>
          ) : !p.active ? (
            <Badge color="gray" variant="light">
              已下架
            </Badge>
          ) : null}
        </Group>
        <Text size="xs" c="dimmed" style={{ fontFamily: 'monospace' }}>
          {p.id}
        </Text>
      </Table.Td>
      <Table.Td data-testid="plan-monthly">
        {money(p.price_cents)} / {p.duration_days} 天 / {trafficText(p.traffic_bytes)}
      </Table.Td>
      <Table.Td data-testid="plan-yearly">
        {!p.yearly_enabled ? (
          <Text span c="dimmed">
            关闭
          </Text>
        ) : p.yearly ? (
          <>
            {money(p.yearly.price_cents)}（{foldText(p.yearly.discount_percent)}，
            {p.yearly_discount_source === 'plan' ? '单独设置' : '跟随全局'}）/{' '}
            {p.yearly.duration_days} 天 / {trafficLabel(p.yearly)}
            <Text size="xs" c="dimmed">
              原价 {money(p.yearly.original_cents)}，折合 {money(p.yearly.monthly_equivalent_cents)}{' '}
              / 月
            </Text>
          </>
        ) : (
          <Text span c="red">
            年付参数不可用
          </Text>
        )}
      </Table.Td>
      <Table.Td>{p.allow_dedicated ? '普通线路 + 专线' : '仅普通线路'}</Table.Td>
      <Table.Td data-testid="plan-devices">
        <Group gap={6} wrap="nowrap">
          <span>
            设定 {p.max_devices} 台 · 实际 {p.effective_devices} 台
          </span>
          {p.devices_over_cap ? (
            <Tooltip
              label={`设定值超过全局上限 ${cap}，用户实际最多同时在线 ${p.effective_devices} 台`}
            >
              <IconAlertTriangle
                size={16}
                color="var(--mantine-color-yellow-7)"
                aria-label="设备额度超过全局上限"
                role="img"
              />
            </Tooltip>
          ) : null}
        </Group>
      </Table.Td>
      <Table.Td>{p.active_subscriptions}</Table.Td>
      <Table.Td>{p.pending_orders}</Table.Td>
      <Table.Td data-testid="plan-groups">
        {p.group_count === 0
          ? '—'
          : p.group_count > p.groups.length
            ? `${p.groups.join('、')} 等 ${p.group_count} 个`
            : p.groups.join('、')}
      </Table.Td>
      <Table.Td>{p.coupon_count}</Table.Td>
      <Table.Td>{p.sort}</Table.Td>
      <Table.Td>
        {deleted ? null : (
          <Group gap={6} wrap="nowrap">
            <Button size="xs" variant="light" onClick={onEdit} aria-label={`编辑 ${p.name}`}>
              编辑
            </Button>
            {p.active ? null : (
              <Button
                size="xs"
                variant="light"
                color="red"
                onClick={onDelete}
                aria-label={`删除 ${p.name}`}
              >
                删除
              </Button>
            )}
          </Group>
        )}
      </Table.Td>
    </Table.Tr>
  );
}

// Global yearly discount (6.5).
function DiscountBar({
  settings,
  plans,
  following,
  onSaved,
}: {
  settings: ShopSettings;
  plans: AdminPlan[];
  following: number;
  onSaved: () => void;
}) {
  const [text, setText] = useState(String(settings.yearly_discount_percent));
  const [confirming, setConfirming] = useState<number | null>(null);
  const [problem, setProblem] = useState('');
  const current = settings.yearly_discount_percent;
  const start = () => {
    const value = text.trim();
    const n = Number(value);
    if (!integerText(value) || n < 50 || n > 100) {
      setProblem('年付折扣须为 50–100 的整数百分比（90 表示 9 折）');
      return;
    }
    if (n === current) {
      setProblem('没有任何改动');
      return;
    }
    setProblem('');
    setConfirming(n);
  };
  return (
    <Paper withBorder radius="lg" p="md" data-testid="discount-bar">
      <Group justify="space-between" align="flex-end" gap="md">
        <div>
          <Text fw={700} data-testid="discount-current">
            年付默认 {foldText(current)}，省 {100 - current}%
          </Text>
          <Text size="xs" c="dimmed">
            跟随全局的上架套餐 {following} 个；套餐表单中填写折扣可单独覆盖。
            {settings.row_missing ? ' 设置行缺失，当前按默认值处理，保存后会重新创建。' : ''}
          </Text>
        </div>
        <Group align="flex-end" gap="xs">
          <TextInput
            label="全局年付折扣（%）"
            description="50–100，90 表示 9 折"
            w={190}
            value={text}
            error={problem || undefined}
            inputMode="numeric"
            onChange={(e) => {
              setText(e.currentTarget.value);
              setProblem('');
            }}
          />
          <Button onClick={start}>修改折扣</Button>
        </Group>
      </Group>
      {confirming !== null ? (
        <DiscountConfirm
          settings={settings}
          plans={plans}
          next={confirming}
          onClose={() => setConfirming(null)}
          onSaved={() => {
            setConfirming(null);
            onSaved();
          }}
        />
      ) : null}
    </Paper>
  );
}

function DiscountConfirm({
  settings,
  plans,
  next,
  onClose,
  onSaved,
}: {
  settings: ShopSettings;
  plans: AdminPlan[];
  next: number;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const onSale = plans.filter((p) => p.active && !p.deleted_at && p.yearly_enabled);
  const submit = async () => {
    setBusy(true);
    setError('');
    try {
      await api('/admin/console/shop-settings', {
        method: 'POST',
        body: JSON.stringify({ yearly_discount_percent: next, version: settings.version }),
      });
      notifications.show({
        color: 'teal',
        title: '全局年付折扣已保存',
        message: `${foldText(next)}，对下一次报价立即生效`,
      });
      onSaved();
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal opened onClose={onClose} title="修改全局年付折扣" size="lg" closeOnClickOutside={false}>
      <Stack>
        <Text size="sm">
          全局年付折扣将由 {foldText(settings.yearly_discount_percent)} 改为 {foldText(next)}
          。保存后对下一次报价立即生效；已创建的待付订单和已有订阅不受影响。
        </Text>
        {onSale.length === 0 ? (
          <Text size="sm" c="dimmed">
            当前没有开启年付的上架套餐。
          </Text>
        ) : (
          <Table aria-label="年付价格对照" data-testid="discount-compare">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>套餐</Table.Th>
                <Table.Th>现年付价</Table.Th>
                <Table.Th>新年付价</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {onSale.map((p) => {
                const own = p.yearly_discount_source === 'plan';
                const after = own
                  ? p.yearly?.price_cents
                  : previewYearly(p.price_cents, next).price;
                return (
                  <Table.Tr key={p.id} data-plan-id={p.id}>
                    <Table.Td>{p.name}</Table.Td>
                    <Table.Td>{p.yearly ? money(p.yearly.price_cents) : '—'}</Table.Td>
                    <Table.Td>
                      {after != null ? money(after) : '—'}
                      {own ? (
                        <Text size="xs" c="dimmed">
                          单独设置，不变
                        </Text>
                      ) : null}
                    </Table.Td>
                  </Table.Tr>
                );
              })}
            </Table.Tbody>
          </Table>
        )}
        <ErrorBox text={error} />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            取消
          </Button>
          <Button onClick={() => void submit()} loading={busy}>
            确认修改
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

function DeletePlan({
  plan,
  onClose,
  onDone,
  onRefresh,
}: {
  plan: AdminPlan;
  onClose: () => void;
  onDone: (name: string) => void;
  onRefresh: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [reasons, setReasons] = useState<string[]>([]);
  const [groups, setGroups] = useState<string[]>([]);
  const [conflict, setConflict] = useState(false);
  const submit = async () => {
    setBusy(true);
    setError('');
    setReasons([]);
    setGroups([]);
    try {
      await api(`/admin/console/plans/${encodeURIComponent(plan.id)}/delete`, {
        method: 'POST',
        body: JSON.stringify({ version: plan.version }),
      });
      onDone(plan.name);
      onClose();
    } catch (e) {
      setError(describeError(e));
      if (e instanceof APIError) {
        setConflict(e.code === 'version_conflict');
        if (e.code === 'plan_in_use') {
          const list = e.details.reasons;
          const names = e.details.groups;
          setReasons(Array.isArray(list) ? list.map(String) : []);
          setGroups(Array.isArray(names) ? names.map(String) : []);
        }
      }
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal opened onClose={onClose} title={`删除套餐 · ${plan.name}`} closeOnClickOutside={false}>
      <Stack>
        <Text size="sm">
          删除是软删除：套餐不再出现在任何选项中，历史订单、已过期的订阅和优惠码中的引用都会保留。
          只有已下架、没有有效订阅、没有待付订单、没有被节点组引用的套餐才能删除。删除后不能恢复，套餐
          ID 也不能再使用。
        </Text>
        {reasons.length > 0 ? (
          <Alert color="red" title="无法删除" data-testid="plan-in-use">
            <Stack gap={2}>
              {reasons.map((r) => (
                <Text key={r} size="sm">
                  {r}
                </Text>
              ))}
              {groups.length > 0 ? (
                <Text size="sm" fw={600}>
                  请先在“节点管理”中，从这些线路组的“限定套餐”里移除本套餐：{groups.join('、')}
                </Text>
              ) : null}
            </Stack>
          </Alert>
        ) : (
          <ErrorBox text={error} />
        )}
        {conflict ? (
          <Button
            variant="light"
            onClick={() => {
              onRefresh();
              onClose();
            }}
          >
            刷新最新数据
          </Button>
        ) : null}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            取消
          </Button>
          <Button color="red" onClick={() => void submit()} loading={busy}>
            确认删除
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
