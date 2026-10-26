'use client';
import { useState } from 'react';
import {
  Alert,
  Badge,
  Button,
  Group,
  Modal,
  MultiSelect,
  NumberInput,
  Paper,
  ScrollArea,
  SegmentedControl,
  Select,
  Skeleton,
  Stack,
  Switch,
  Table,
  Text,
  TextInput,
} from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { api, APIError } from '@/lib/api';
import { money } from '@/lib/types';
import {
  fromLocalInput,
  toLocalInput,
  type AdminPlan,
  type AdminPlansResponse,
} from '@/lib/console';
import { planNameOf, planOptions } from '@/lib/plan-math';
import { describeError, ErrorBox, useFetched, When } from './console-common';

// One row of GET /admin/console/coupons.
type Coupon = {
  id: string;
  code: string;
  note: string;
  discount_type: 'percent' | 'amount';
  percent_off: number;
  amount_off_cents: number;
  plan_ids: string[];
  billing_cycles: 'all' | 'monthly' | 'yearly';
  starts_at: string | null;
  ends_at: string | null;
  max_uses: number;
  per_user_limit: number;
  used_count: number;
  enabled: boolean;
  deleted_at?: string | null;
  version: number;
  reserved: number;
  // Some order used it: its discount is frozen and deleting it only hides it.
  referenced: boolean;
};

const cycleScopeLabel: Record<string, string> = {
  all: '全部周期',
  monthly: '仅月付',
  yearly: '仅年付',
};

// The coupon API refuses a coupon that still lists a deleted plan; say what to do about it.
const couponError = (e: unknown) => {
  const text = describeError(e);
  return text.includes('已删除') ? `${text}。请先从“适用套餐”中移除已删除的套餐，再保存。` : text;
};

const discountText = (c: Coupon) =>
  c.discount_type === 'percent' ? `立减 ${c.percent_off}%` : `立减 ${money(c.amount_off_cents)}`;

function status(c: Coupon, now: number) {
  if (c.deleted_at) return { text: '已删除', color: 'gray' };
  if (!c.enabled) return { text: '已停用', color: 'gray' };
  if (c.starts_at && new Date(c.starts_at).getTime() > now)
    return { text: '未开始', color: 'yellow' };
  if (c.ends_at && new Date(c.ends_at).getTime() <= now) return { text: '已过期', color: 'gray' };
  if (c.max_uses > 0 && c.used_count + c.reserved >= c.max_uses)
    return { text: '已用完', color: 'orange' };
  return { text: '有效', color: 'teal' };
}

// The body the API wants when a coupon is saved again unchanged except for one field.
function inputOf(c: Coupon, patch: Partial<Record<string, unknown>> = {}) {
  return {
    note: c.note,
    discount_type: c.discount_type,
    percent_off: c.percent_off,
    amount_off_cents: c.amount_off_cents,
    plan_ids: c.plan_ids,
    billing_cycles: c.billing_cycles,
    starts_at: c.starts_at,
    ends_at: c.ends_at,
    max_uses: c.max_uses,
    per_user_limit: c.per_user_limit,
    enabled: c.enabled,
    version: c.version,
    ...patch,
  };
}

export function ConsoleCoupons() {
  const [nonce, setNonce] = useState(0);
  const [showDeleted, setShowDeleted] = useState(false);
  const [editing, setEditing] = useState<Coupon | 'new' | null>(null);
  const [removing, setRemoving] = useState<Coupon | null>(null);
  const [toggleError, setToggleError] = useState('');
  const { data, error, loading } = useFetched<{ coupons: Coupon[] }>(
    `/admin/console/coupons?include_deleted=${showDeleted}`,
    nonce,
  );
  const plans =
    useFetched<AdminPlansResponse>('/admin/console/plans?include_deleted=true').data?.plans ?? [];
  const planName = (id: string) => planNameOf(plans, id);
  const [now] = useState(() => Date.now());
  const refresh = () => setNonce((n) => n + 1);
  const toggle = async (c: Coupon) => {
    setToggleError('');
    try {
      await api(`/admin/console/coupons/${c.id}`, {
        method: 'POST',
        body: JSON.stringify(inputOf(c, { enabled: !c.enabled })),
      });
      refresh();
    } catch (e) {
      setToggleError(couponError(e));
    }
  };
  return (
    <Stack gap="lg">
      <Group justify="space-between">
        <Switch
          label="显示已删除"
          checked={showDeleted}
          onChange={(e) => setShowDeleted(e.currentTarget.checked)}
        />
        <Button onClick={() => setEditing('new')}>新建优惠码</Button>
      </Group>
      <ErrorBox text={error || toggleError} />
      <Paper withBorder radius="lg" p="md">
        {loading && !data ? (
          <Skeleton height={200} />
        ) : (
          <ScrollArea>
            <Table verticalSpacing="sm" horizontalSpacing="md" miw={1100} aria-label="优惠码列表">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>优惠码</Table.Th>
                  <Table.Th>折扣</Table.Th>
                  <Table.Th>适用套餐</Table.Th>
                  <Table.Th>适用周期</Table.Th>
                  <Table.Th>有效期</Table.Th>
                  <Table.Th>已用 / 上限</Table.Th>
                  <Table.Th>每人限用</Table.Th>
                  <Table.Th>状态</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {(data?.coupons ?? []).map((c) => {
                  const state = status(c, now);
                  return (
                    <Table.Tr key={c.id} data-testid="coupon-row">
                      <Table.Td>
                        <Text size="sm" fw={700} style={{ fontFamily: 'monospace' }}>
                          {c.code}
                        </Text>
                        <Text size="xs" c="dimmed">
                          {c.note}
                        </Text>
                      </Table.Td>
                      <Table.Td>{discountText(c)}</Table.Td>
                      <Table.Td>
                        {c.plan_ids.length ? c.plan_ids.map(planName).join('、') : '全部套餐'}
                      </Table.Td>
                      <Table.Td data-testid="coupon-cycles">
                        {cycleScopeLabel[c.billing_cycles] ?? cycleScopeLabel.all}
                      </Table.Td>
                      <Table.Td>
                        <Text size="xs">
                          {c.starts_at ? <When value={c.starts_at} /> : '立即'} 至{' '}
                          {c.ends_at ? <When value={c.ends_at} /> : '长期'}
                        </Text>
                      </Table.Td>
                      <Table.Td>
                        {c.used_count} / {c.max_uses === 0 ? '不限' : c.max_uses}
                        {c.reserved > 0 ? (
                          <Text size="xs" c="dimmed">
                            待付占用 {c.reserved}
                          </Text>
                        ) : null}
                      </Table.Td>
                      <Table.Td>{c.per_user_limit}</Table.Td>
                      <Table.Td>
                        <Badge color={state.color} variant="light">
                          {state.text}
                        </Badge>
                      </Table.Td>
                      <Table.Td>
                        {c.deleted_at ? null : (
                          <Group gap={6} wrap="nowrap">
                            <Button
                              size="xs"
                              variant="light"
                              onClick={() => setEditing(c)}
                              aria-label={`编辑 ${c.code}`}
                            >
                              编辑
                            </Button>
                            <Button
                              size="xs"
                              variant="light"
                              color={c.enabled ? 'orange' : 'teal'}
                              onClick={() => void toggle(c)}
                              aria-label={`${c.enabled ? '停用' : '启用'} ${c.code}`}
                            >
                              {c.enabled ? '停用' : '启用'}
                            </Button>
                            <Button
                              size="xs"
                              variant="light"
                              color="red"
                              onClick={() => setRemoving(c)}
                              aria-label={`删除 ${c.code}`}
                            >
                              删除
                            </Button>
                          </Group>
                        )}
                      </Table.Td>
                    </Table.Tr>
                  );
                })}
              </Table.Tbody>
            </Table>
            {data && data.coupons.length === 0 ? (
              <Text ta="center" c="dimmed" py="xl">
                还没有优惠码
              </Text>
            ) : null}
          </ScrollArea>
        )}
      </Paper>
      {editing ? (
        <CouponForm
          coupon={editing === 'new' ? null : editing}
          plans={plans}
          onClose={() => setEditing(null)}
          onSaved={refresh}
        />
      ) : null}
      {removing ? (
        <RemoveCoupon coupon={removing} onClose={() => setRemoving(null)} onDone={refresh} />
      ) : null}
    </Stack>
  );
}

function CouponForm({
  coupon,
  plans,
  onClose,
  onSaved,
}: {
  coupon: Coupon | null;
  plans: AdminPlan[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [generate, setGenerate] = useState(false);
  const [code, setCode] = useState('');
  const [note, setNote] = useState(coupon?.note ?? '');
  const [type, setType] = useState<'percent' | 'amount'>(coupon?.discount_type ?? 'percent');
  const [percent, setPercent] = useState<number | string>(coupon?.percent_off || 10);
  const [yuan, setYuan] = useState<number | string>(
    coupon?.amount_off_cents ? coupon.amount_off_cents / 100 : 5,
  );
  const [planIds, setPlanIds] = useState<string[]>(coupon?.plan_ids ?? []);
  const [cycles, setCycles] = useState<string>(coupon?.billing_cycles ?? 'all');
  const [starts, setStarts] = useState(coupon?.starts_at ? toLocalInput(coupon.starts_at) : '');
  const [ends, setEnds] = useState(coupon?.ends_at ? toLocalInput(coupon.ends_at) : '');
  const [maxUses, setMaxUses] = useState<number | string>(coupon?.max_uses ?? 0);
  const [perUser, setPerUser] = useState<number | string>(coupon?.per_user_limit ?? 1);
  const [enabled, setEnabled] = useState(coupon?.enabled ?? true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [conflict, setConflict] = useState(false);
  const frozen = !!coupon?.referenced;
  const submit = async () => {
    setBusy(true);
    setError('');
    const body: Record<string, unknown> = {
      note,
      discount_type: type,
      percent_off: type === 'percent' ? Number(percent) : 0,
      amount_off_cents: type === 'amount' ? Math.round(Number(yuan) * 100) : 0,
      plan_ids: planIds,
      billing_cycles: cycles,
      starts_at: starts ? fromLocalInput(starts) : null,
      ends_at: ends ? fromLocalInput(ends) : null,
      max_uses: Number(maxUses) || 0,
      per_user_limit: Number(perUser) || 1,
      enabled,
    };
    if (coupon) body.version = coupon.version;
    else if (generate) body.generate = true;
    else body.code = code;
    try {
      await api(coupon ? `/admin/console/coupons/${coupon.id}` : '/admin/console/coupons', {
        method: 'POST',
        body: JSON.stringify(body),
      });
      notifications.show({
        color: 'teal',
        title: coupon ? '优惠码已保存' : '优惠码已创建',
        message: coupon?.code ?? '',
      });
      onSaved();
      onClose();
    } catch (e) {
      setError(couponError(e));
      setConflict(e instanceof APIError && e.code === 'version_conflict');
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      opened
      onClose={onClose}
      title={coupon ? `编辑优惠码 · ${coupon.code}` : '新建优惠码'}
      size="lg"
      closeOnClickOutside={false}
    >
      <Stack>
        {coupon ? null : (
          <Group align="flex-end">
            <TextInput
              label="优惠码"
              description="4–32 位字母、数字或连字符，自动转为大写"
              value={generate ? '' : code}
              placeholder={generate ? '保存时由服务端随机生成' : ''}
              disabled={generate}
              onChange={(e) => setCode(e.currentTarget.value)}
              style={{ flex: 1 }}
            />
            <Switch
              label="随机生成 10 位"
              checked={generate}
              onChange={(e) => setGenerate(e.currentTarget.checked)}
            />
          </Group>
        )}
        <TextInput
          label="备注"
          maxLength={120}
          value={note}
          onChange={(e) => setNote(e.currentTarget.value)}
        />
        {frozen ? (
          <Alert color="yellow">
            已有订单使用过这个优惠码，折扣不能再修改。需要调整请停用后新建。
          </Alert>
        ) : null}
        <SegmentedControl
          value={type}
          onChange={(v) => setType(v as 'percent' | 'amount')}
          disabled={frozen}
          data={[
            { value: 'percent', label: '按比例减价' },
            { value: 'amount', label: '固定金额减价' },
          ]}
        />
        {type === 'percent' ? (
          <NumberInput
            label="减价比例（%）"
            min={1}
            max={99}
            allowDecimal={false}
            value={percent}
            onChange={setPercent}
            disabled={frozen}
          />
        ) : (
          <NumberInput
            label="减价金额（元）"
            min={0.01}
            decimalScale={2}
            value={yuan}
            onChange={setYuan}
            disabled={frozen}
          />
        )}
        <MultiSelect
          label="适用套餐"
          description="不选表示全部套餐"
          data={planOptions(plans, planIds)}
          value={planIds}
          onChange={setPlanIds}
          clearable
        />
        <Select
          label="适用周期"
          description="年付价已含年付折扣，优惠码在其基础上再减"
          allowDeselect={false}
          data={[
            { value: 'all', label: '全部' },
            { value: 'monthly', label: '仅月付' },
            { value: 'yearly', label: '仅年付' },
          ]}
          value={cycles}
          onChange={(v) => setCycles(v ?? 'all')}
        />
        <Group grow align="flex-start">
          <TextInput
            label="开始时间（可空）"
            type="datetime-local"
            value={starts}
            onChange={(e) => setStarts(e.currentTarget.value)}
          />
          <TextInput
            label="结束时间（可空）"
            type="datetime-local"
            value={ends}
            onChange={(e) => setEnds(e.currentTarget.value)}
          />
        </Group>
        <Group grow align="flex-start">
          <NumberInput
            label="总使用次数"
            description="0 表示不限"
            min={0}
            allowDecimal={false}
            value={maxUses}
            onChange={setMaxUses}
          />
          <NumberInput
            label="每人限用次数"
            min={1}
            allowDecimal={false}
            value={perUser}
            onChange={setPerUser}
          />
        </Group>
        <Switch
          label="启用"
          checked={enabled}
          onChange={(e) => setEnabled(e.currentTarget.checked)}
        />
        <ErrorBox text={error} />
        {conflict ? (
          <Button
            variant="light"
            onClick={() => {
              onSaved();
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
          <Button
            onClick={() => void submit()}
            loading={busy}
            disabled={!coupon && !generate && code.trim() === ''}
          >
            保存
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

function RemoveCoupon({
  coupon,
  onClose,
  onDone,
}: {
  coupon: Coupon;
  onClose: () => void;
  onDone: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const used = coupon.referenced || coupon.used_count > 0;
  const submit = async () => {
    setBusy(true);
    try {
      const r = await api<{ deleted: 'hard' | 'soft' }>(`/admin/console/coupons/${coupon.id}`, {
        method: 'DELETE',
        body: '{}',
      });
      notifications.show({
        color: 'teal',
        title: r.deleted === 'soft' ? '优惠码已停用并隐藏' : '优惠码已删除',
        message: coupon.code,
      });
      onDone();
      onClose();
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal opened onClose={onClose} title={`删除优惠码 · ${coupon.code}`}>
      <Stack>
        <Text size="sm">
          {used
            ? '这个优惠码已经被订单使用过：它会被停用并隐藏，订单里的优惠记录和使用次数保持不变。'
            : '这个优惠码从未被使用，将被彻底删除。'}
        </Text>
        <ErrorBox text={error} />
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
