'use client';
import { useState } from 'react';
import {
  Alert,
  Button,
  Group,
  Modal,
  Paper,
  Radio,
  Stack,
  Switch,
  Text,
  Textarea,
  TextInput,
} from '@mantine/core';
import { api, APIError } from '@/lib/api';
import { money } from '@/lib/types';
import type { AdminPlan, AdminPlanInput } from '@/lib/console';
import {
  bytesToGibText,
  centsToYuanText,
  foldText,
  gibToBytesInt,
  integerText,
  MAX_GIB,
  MAX_MONTHLY_CENTS,
  previewYearly,
  yuanToCents,
} from '@/lib/plan-math';
import { ErrorBox } from './console-common';

// Create / edit form of a plan (handoff_shop.md 6.3). Typed values stay text until they are
// validated; money goes from text straight to integer cents. The yearly preview applies the
// server's integer rules, and the saved plan comes back from the server.
type Fields = {
  id: string;
  name: string;
  description: string;
  note: string;
  line: 'normal' | 'dedicated';
  devices: string;
  price: string;
  days: string;
  traffic: string;
  yearlyEnabled: boolean;
  yearlyDays: string;
  yearlyTraffic: string;
  yearlyDiscount: string;
  featured: boolean;
  sort: string;
  active: boolean;
};

const planIDPattern = /^[a-z0-9][a-z0-9-]{1,31}$/;
const discountMessage = '年付折扣须为 50–100 的整数百分比（90 表示 9 折）';

function initial(plan: AdminPlan | null): Fields {
  if (!plan)
    return {
      id: '',
      name: '',
      description: '',
      note: '',
      line: 'normal',
      devices: '2',
      price: '',
      days: '30',
      traffic: '100',
      yearlyEnabled: true,
      yearlyDays: '365',
      yearlyTraffic: '',
      yearlyDiscount: '',
      featured: false,
      sort: '0',
      active: false,
    };
  return {
    id: plan.id,
    name: plan.name,
    description: plan.description,
    note: plan.note,
    line: plan.allow_dedicated ? 'dedicated' : 'normal',
    devices: String(plan.max_devices),
    price: centsToYuanText(plan.price_cents),
    days: String(plan.duration_days),
    traffic: bytesToGibText(plan.traffic_bytes),
    yearlyEnabled: plan.yearly_enabled,
    yearlyDays: String(plan.yearly_duration_days),
    yearlyTraffic:
      plan.yearly_traffic_bytes == null ? '' : bytesToGibText(plan.yearly_traffic_bytes),
    yearlyDiscount:
      plan.yearly_discount_percent == null ? '' : String(plan.yearly_discount_percent),
    featured: plan.featured,
    sort: String(plan.sort),
    active: plan.active,
  };
}

const chars = (text: string) => [...text].length;
const hasControl = (text: string) => /[\u0000-\u0009\u000b-\u001f\u007f-\u009f]/.test(text);

function textProblem(value: string, limit: number, required: boolean) {
  if (required && value === '') return '不能为空';
  if (chars(value) > limit) return `最多 ${limit} 个字符`;
  if (hasControl(value)) return '不能包含控制字符';
  return '';
}

function intIn(text: string, min: number, max: number) {
  const t = text.trim();
  if (!integerText(t)) return null;
  const n = Number(t);
  return n >= min && n <= max ? n : null;
}

// Mirrors validatePlanInput on the server; messages name their field.
function validate(f: Fields, creating: boolean) {
  const errors: Record<string, string> = {};
  const name = f.name.trim();
  const description = f.description.trim();
  const note = f.note.trim();
  const id = f.id.trim();
  if (creating && id !== '' && !planIDPattern.test(id))
    errors.id = '套餐 ID 须以小写字母或数字开头，只含小写字母、数字和连字符，长度 2–32';
  let p = textProblem(name, 40, true);
  if (p) errors.name = `名称${p}`;
  p = textProblem(description, 300, false);
  if (p) errors.description = `公开描述${p}`;
  p = textProblem(note, 200, false);
  if (p) errors.note = `内部备注${p}`;
  const cents = yuanToCents(f.price);
  if (cents === null || cents < 1 || cents > MAX_MONTHLY_CENTS)
    errors.price_cents = '月付价格须为 0.01–100000.00 元，最多两位小数';
  const days = intIn(f.days, 1, 365);
  if (days === null) errors.duration_days = '月付有效天数须为 1–365';
  const traffic = intIn(f.traffic, 1, MAX_GIB);
  if (traffic === null) errors.traffic_bytes = '月付流量须为 1–1048576 GiB 的整数';
  const devices = intIn(f.devices, 1, 100);
  if (devices === null) errors.max_devices = '设备额度须为 1–100';
  const sort = intIn(f.sort, 0, 10000);
  if (sort === null) errors.sort = '排序须为 0–10000';
  const yearlyDays = intIn(f.yearlyDays, 1, 365);
  if (yearlyDays === null) errors.yearly_duration_days = '年付有效天数须为 1–365';
  let yearlyTraffic: number | null = null;
  if (f.yearlyTraffic.trim() !== '') {
    yearlyTraffic = intIn(f.yearlyTraffic, 1, MAX_GIB);
    if (yearlyTraffic === null) errors.yearly_traffic_bytes = '年付流量须为 1–1048576 GiB 的整数';
  }
  let discount: number | null = null;
  if (f.yearlyDiscount.trim() !== '') {
    discount = intIn(f.yearlyDiscount, 50, 100);
    if (discount === null) errors.yearly_discount_percent = discountMessage;
  }
  if (
    f.yearlyEnabled &&
    f.yearlyTraffic.trim() === '' &&
    traffic !== null &&
    traffic * 12 > MAX_GIB
  )
    errors.yearly_traffic_bytes = '月流量的 12 倍超出上限，请填写年付流量或关闭年付';
  if (Object.keys(errors).length > 0) return { errors };
  const input: AdminPlanInput & { id?: string } = {
    name,
    description,
    note,
    price_cents: cents as number,
    duration_days: days as number,
    traffic_bytes: gibToBytesInt(traffic as number),
    max_devices: devices as number,
    allow_dedicated: f.line === 'dedicated',
    yearly_enabled: f.yearlyEnabled,
    yearly_duration_days: yearlyDays as number,
    yearly_traffic_bytes: yearlyTraffic === null ? null : gibToBytesInt(yearlyTraffic),
    yearly_discount_percent: discount,
    featured: f.featured,
    sort: sort as number,
    active: f.active,
  };
  if (creating && id !== '') input.id = id;
  return { errors, input };
}

export function PlanForm({
  plan,
  globalPercent,
  cap,
  onClose,
  onRefresh,
  onSaved,
}: {
  plan: AdminPlan | null;
  globalPercent: number;
  cap: number;
  onClose: () => void;
  onRefresh: () => void;
  onSaved: (saved: AdminPlan) => void;
}) {
  const [f, setF] = useState<Fields>(() => initial(plan));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [error, setError] = useState('');
  const [conflict, setConflict] = useState(false);
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState<{ input: AdminPlanInput; lines: string[] } | null>(null);
  const set = <K extends keyof Fields>(key: K, value: Fields[K]) =>
    setF((current) => ({ ...current, [key]: value }));

  // Live preview of the yearly tier, with the server's integer rules.
  const monthly = yuanToCents(f.price);
  const own = f.yearlyDiscount.trim() === '' ? null : intIn(f.yearlyDiscount, 50, 100);
  const percent = f.yearlyDiscount.trim() === '' ? globalPercent : own;
  const preview =
    monthly !== null && monthly >= 1 && monthly <= MAX_MONTHLY_CENTS && percent !== null
      ? previewYearly(monthly, percent)
      : null;
  const monthlyTraffic = intIn(f.traffic, 1, MAX_GIB);
  const yearlyTrafficGib =
    f.yearlyTraffic.trim() !== ''
      ? intIn(f.yearlyTraffic, 1, MAX_GIB)
      : monthlyTraffic === null
        ? null
        : monthlyTraffic * 12;
  const devices = intIn(f.devices, 1, 100);
  const dayCount = intIn(f.days, 1, 365);

  const send = async (input: AdminPlanInput) => {
    setBusy(true);
    setError('');
    setConflict(false);
    setErrors({});
    try {
      const body = plan ? { ...input, version: plan.version } : input;
      const r = await api<{ plan: AdminPlan }>(
        plan ? `/admin/console/plans/${encodeURIComponent(plan.id)}` : '/admin/console/plans',
        { method: 'POST', body: JSON.stringify(body) },
      );
      onSaved(r.plan);
      onClose();
    } catch (e) {
      setConfirm(null);
      if (e instanceof APIError) {
        const field = e.details.field;
        if (e.code === 'invalid_plan' && typeof field === 'string') {
          setErrors({ [field]: e.message });
          return;
        }
        if (e.code === 'plan_exists') {
          setErrors({ id: e.message });
          return;
        }
        if (e.code === 'no_change') {
          setError('没有任何改动，无需保存。');
          return;
        }
        setConflict(e.code === 'version_conflict');
      }
      setError(e instanceof Error ? e.message : '保存失败，请重试');
    } finally {
      setBusy(false);
    }
  };

  const submit = () => {
    const result = validate(f, !plan);
    setErrors(result.errors);
    setError('');
    if (!result.input) return;
    const input = result.input;
    if (!plan) {
      void send(input);
      return;
    }
    const lines: string[] = [];
    if (input.price_cents !== plan.price_cents)
      lines.push(
        `月付价格将由 ${money(plan.price_cents)} 改为 ${money(input.price_cents)}。修改只对新订单生效；活动中的 ${plan.pending_orders} 笔待付订单按下单时的价格和权益开通，现有 ${plan.active_subscriptions} 位订阅用户保持原有权益。`,
      );
    const yearlyChanged =
      input.yearly_enabled !== plan.yearly_enabled ||
      input.yearly_discount_percent !== plan.yearly_discount_percent ||
      input.yearly_duration_days !== plan.yearly_duration_days ||
      input.yearly_traffic_bytes !== plan.yearly_traffic_bytes ||
      input.price_cents !== plan.price_cents;
    if (yearlyChanged && (input.yearly_enabled || plan.yearly_enabled)) {
      const after = input.yearly_enabled
        ? money(
            previewYearly(input.price_cents, input.yearly_discount_percent ?? globalPercent).price,
          )
        : '关闭';
      lines.push(
        `年付价将由 ${plan.yearly ? money(plan.yearly.price_cents) : '关闭'} 改为 ${after}（预览，以服务端保存后的值为准）。`,
      );
    }
    if (plan.active && !input.active)
      lines.push(
        `下架后 /plans 不再展示；现有 ${plan.active_subscriptions} 位有效订阅用户不受影响，但在有效期内无法续费，到期后可以购买其他套餐；活动中的 ${plan.pending_orders} 笔待付订单付款后仍会正常开通。`,
      );
    if (lines.length > 0) setConfirm({ input, lines });
    else void send(input);
  };

  const disabledYearly = !f.yearlyEnabled;
  return (
    <Modal
      opened
      onClose={onClose}
      title={plan ? `编辑套餐 · ${plan.name}` : '新建套餐'}
      size="xl"
      closeOnClickOutside={false}
    >
      <Stack>
        <TextInput
          label="套餐 ID"
          description={
            plan
              ? '创建后不能修改'
              : '小写字母或数字开头，只含小写字母、数字和连字符，2–32 位；留空则自动生成'
          }
          value={f.id}
          readOnly={!!plan}
          error={errors.id}
          onChange={(e) => set('id', e.currentTarget.value)}
        />
        <TextInput
          label="名称"
          description="1–40 个字符"
          value={f.name}
          error={errors.name}
          onChange={(e) => set('name', e.currentTarget.value)}
        />
        <Textarea
          label="公开描述"
          description="显示在套餐卡片上，纯文本，最多 300 字符"
          autosize
          minRows={2}
          value={f.description}
          error={errors.description}
          onChange={(e) => set('description', e.currentTarget.value)}
        />
        <Textarea
          label="内部备注"
          description="只在后台显示，最多 200 字符"
          autosize
          minRows={1}
          value={f.note}
          error={errors.note}
          onChange={(e) => set('note', e.currentTarget.value)}
        />
        <Radio.Group
          label="线路类型"
          value={f.line}
          onChange={(v) => set('line', v === 'dedicated' ? 'dedicated' : 'normal')}
        >
          <Group mt={6}>
            <Radio value="normal" label="仅普通线路" />
            <Radio value="dedicated" label="普通线路加专线" />
          </Group>
        </Radio.Group>
        <Text size="xs" c="dimmed">
          当前可见线路由节点管理中每个线路组的套餐范围决定
          {plan
            ? plan.group_count > 0
              ? `。引用本套餐的线路组：${plan.groups.join('、')}${plan.group_count > plan.groups.length ? ` 等 ${plan.group_count} 个` : ''}`
              : '。目前没有线路组限定本套餐'
            : '。'}
        </Text>
        <TextInput
          label="设备额度"
          description="同时在线设备数，1–100"
          inputMode="numeric"
          value={f.devices}
          error={errors.max_devices}
          onChange={(e) => set('devices', e.currentTarget.value)}
        />
        {devices !== null && cap > 0 && devices > cap ? (
          <Alert color="yellow" data-testid="device-warning">
            全局上限为 {cap}，用户实际最多同时在线 {cap} 台
          </Alert>
        ) : null}
        <Group grow align="flex-start">
          <TextInput
            label="月付价格（元）"
            description="0.01–100000.00，最多两位小数"
            inputMode="decimal"
            value={f.price}
            error={errors.price_cents}
            onChange={(e) => set('price', e.currentTarget.value)}
          />
          <TextInput
            label="月付有效天数"
            description="1–365"
            inputMode="numeric"
            value={f.days}
            error={errors.duration_days}
            onChange={(e) => set('days', e.currentTarget.value)}
          />
          <TextInput
            label="月付流量（GiB）"
            description="1–1048576 的整数"
            inputMode="numeric"
            value={f.traffic}
            error={errors.traffic_bytes}
            onChange={(e) => set('traffic', e.currentTarget.value)}
          />
        </Group>
        {dayCount !== null && (dayCount < 28 || dayCount > 31) ? (
          <Text size="xs" c="orange.8">
            年付原价按 12 × 月价计算，非月度时长可能不适合开启年付
          </Text>
        ) : null}
        <Switch
          label="开启年付"
          checked={f.yearlyEnabled}
          onChange={(e) => set('yearlyEnabled', e.currentTarget.checked)}
        />
        <Group grow align="flex-start">
          <TextInput
            label="年付有效天数"
            description="1–365，默认 365"
            inputMode="numeric"
            disabled={disabledYearly}
            value={f.yearlyDays}
            error={errors.yearly_duration_days}
            onChange={(e) => set('yearlyDays', e.currentTarget.value)}
          />
          <TextInput
            label="年付流量（GiB）"
            description={`留空表示月流量 × 12，当前生效 ${yearlyTrafficGib === null ? '—' : yearlyTrafficGib > MAX_GIB ? '超出上限' : `${yearlyTrafficGib} GiB`}`}
            inputMode="numeric"
            disabled={disabledYearly}
            value={f.yearlyTraffic}
            error={errors.yearly_traffic_bytes}
            onChange={(e) => set('yearlyTraffic', e.currentTarget.value)}
          />
          <TextInput
            label="年付折扣（%）"
            description={`留空表示跟随全局（当前 ${globalPercent}），50–100`}
            inputMode="numeric"
            disabled={disabledYearly}
            value={f.yearlyDiscount}
            error={errors.yearly_discount_percent}
            onChange={(e) => set('yearlyDiscount', e.currentTarget.value)}
          />
        </Group>
        {f.yearlyEnabled ? (
          <Paper withBorder radius="md" p="sm" data-testid="yearly-preview">
            <Text size="xs" c="dimmed" mb={4}>
              年付预览（整数分运算，仅供参考，保存后以服务端返回的值为准）
            </Text>
            {preview && percent !== null ? (
              <Stack gap={2}>
                <Text size="sm">
                  原价（12 个月）：
                  <span data-testid="preview-original">{money(preview.original)}</span>
                </Text>
                <Text size="sm">
                  年付价：<span data-testid="preview-price">{money(preview.price)}</span>（
                  {foldText(percent)} / 省 {100 - percent}%）
                </Text>
                <Text size="sm">
                  立省：<span data-testid="preview-saving">{money(preview.saving)}</span>
                </Text>
                <Text size="sm">
                  折合月价：
                  <span data-testid="preview-monthly">{money(preview.monthlyEquivalent)}</span>
                </Text>
              </Stack>
            ) : (
              <Text size="sm" c="dimmed">
                填写有效的月付价格和折扣后显示
              </Text>
            )}
          </Paper>
        ) : null}
        <Group grow align="flex-start">
          <TextInput
            label="排序"
            description="0–10000，越小越靠前"
            inputMode="numeric"
            value={f.sort}
            error={errors.sort}
            onChange={(e) => set('sort', e.currentTarget.value)}
          />
          <Stack gap="xs" pt={4}>
            <Switch
              label="推荐"
              checked={f.featured}
              onChange={(e) => set('featured', e.currentTarget.checked)}
            />
            <Switch
              label="上架"
              description={plan ? undefined : '新建时默认关闭，检查无误后再上架'}
              checked={f.active}
              onChange={(e) => set('active', e.currentTarget.checked)}
            />
          </Stack>
        </Group>
        <ErrorBox text={error} />
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
          <Button onClick={submit} loading={busy}>
            保存
          </Button>
        </Group>
      </Stack>
      {confirm ? (
        <Modal
          opened
          onClose={() => setConfirm(null)}
          title="确认保存修改"
          closeOnClickOutside={false}
        >
          <Stack>
            {confirm.lines.map((line) => (
              <Text key={line} size="sm">
                {line}
              </Text>
            ))}
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setConfirm(null)}>
                返回修改
              </Button>
              <Button onClick={() => void send(confirm.input)} loading={busy}>
                确认保存
              </Button>
            </Group>
          </Stack>
        </Modal>
      ) : null}
    </Modal>
  );
}
