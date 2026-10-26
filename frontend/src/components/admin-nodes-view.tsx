'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Alert,
  Badge,
  Button,
  Center,
  Collapse,
  Group,
  Loader,
  Modal,
  MultiSelect,
  NumberInput,
  Paper,
  Progress,
  Select,
  Stack,
  Switch,
  Table,
  Text,
  Textarea,
  TextInput,
  Title,
} from '@mantine/core';
import { api, message } from '@/lib/api';
import { nodeRate } from '@/lib/node-list';
import type { AdminPlan, AdminPlansResponse } from '@/lib/console';
import { planNameOf, planOptions } from '@/lib/plan-math';
import { usePolling } from '@/lib/use-polling';
import { AdminAgentsPanel } from './admin-agents-panel';
import { useAuth } from './providers';
import { RequireAuth } from './require-auth';

// Administrator-only shapes of GET /admin/node-groups. Servers carry no address or YAML here;
// YAML is read only when one server is opened for editing.
type AdminServer = {
  id: string;
  label: string;
  name: string;
  protocol: string;
  enabled: boolean;
  health: 'ok' | 'cooldown' | 'agent_offline' | 'config_error';
  unhealthy_until: string | null;
  max_connections: number;
  online: number;
  over_capacity: boolean;
  agent_managed: boolean;
  version: number;
};
type AdminGroup = {
  id: string;
  name: string;
  region: string;
  line_type: 'direct' | 'dedicated';
  rate_permille: number;
  plan_ids: string[];
  enabled: boolean;
  sort: number;
  version: number;
  metering_source: 'client_reported' | 'node_reported';
  mixed_metering: boolean;
  online: number;
  capacity: number;
  servers: AdminServer[];
};
type GroupList = { groups: AdminGroup[]; max_groups: number; max_servers: number };
type ServerDetail = {
  id: string;
  version: number;
  enabled: boolean;
  yaml?: string;
  group_id: string;
  max_connections: number;
  label: string;
};

type GroupForm = {
  id: string;
  version: number;
  name: string;
  region: string;
  line_type: 'direct' | 'dedicated';
  rate: number | string;
  plan_ids: string[];
  enabled: boolean;
  sort: number | string;
};
type ServerForm = {
  id: string;
  version: number;
  group_id: string;
  yaml: string;
  max_connections: number | string;
  label: string;
  enabled: boolean;
};
type Removal =
  { kind: 'group'; group: AdminGroup } | { kind: 'server'; group: AdminGroup; server: AdminServer };

const refreshMs = 5000;
const migratedCapacity = 1000;
const ALL = 'all';
const UNSCOPED = 'unscoped';

const newGroup = (planIDs: string[]): GroupForm => ({
  id: '',
  version: 0,
  name: '',
  region: 'HK',
  line_type: 'direct',
  rate: 0.5,
  plan_ids: planIDs,
  enabled: true,
  sort: 0,
});
const newServer = (groupID: string): ServerForm => ({
  id: '',
  version: 0,
  group_id: groupID,
  yaml: '',
  max_connections: 5,
  label: '',
  enabled: true,
});
const serverName = (server: AdminServer) => server.label || server.name;
// Stable, readable order inside a group: by label (or proxy name), then id.
const sortedServers = (servers: AdminServer[]) =>
  [...servers].sort(
    (a, b) => serverName(a).localeCompare(serverName(b), 'zh-CN') || a.id.localeCompare(b.id),
  );
const clock = (value: string) =>
  new Date(value).toLocaleTimeString('zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  });

function health(server: AdminServer) {
  switch (server.health) {
    case 'cooldown':
      return {
        color: 'orange',
        text: server.unhealthy_until ? `冷却至 ${clock(server.unhealthy_until)}` : '冷却中',
      };
    case 'agent_offline':
      return { color: 'red', text: 'Agent 离线' };
    case 'config_error':
      return { color: 'red', text: '配置不可读' };
    default:
      return { color: 'teal', text: '正常' };
  }
}

export function AdminNodesView() {
  return (
    <RequireAuth>
      <AdminGate />
    </RequireAuth>
  );
}

function AdminGate() {
  const { user } = useAuth();
  if (user?.role !== 'admin') {
    return (
      <Alert m="xl" color="red" title="无权访问">
        此页面仅对管理员开放。
      </Alert>
    );
  }
  return <GroupManager key={user.id} />;
}

function GroupManager() {
  const [list, setList] = useState<GroupList | null>(null);
  const [plans, setPlans] = useState<AdminPlan[]>([]);
  const [protocols, setProtocols] = useState<string[]>([]);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [filter, setFilter] = useState(ALL);
  const [busy, setBusy] = useState(false);
  const [groupForm, setGroupForm] = useState<GroupForm | null>(null);
  const [serverForm, setServerForm] = useState<ServerForm | null>(null);
  const [formError, setFormError] = useState('');
  const [opening, setOpening] = useState('');
  const [removal, setRemoval] = useState<Removal | null>(null);
  const [removalError, setRemovalError] = useState('');
  const lifetime = useRef<AbortController | null>(null);
  const editRequest = useRef<AbortController | null>(null);
  const pending = useRef(false);
  const loadRevision = useRef(0);
  const protocolsLoaded = useRef(false);

  // A mutation bumps the revision, so a poll that started before it cannot overwrite the
  // newer list with an older one.
  const load = useCallback(async (signal: AbortSignal) => {
    const revision = ++loadRevision.current;
    const current = () => !signal.aborted && revision === loadRevision.current;
    try {
      const [groups, available, legacy] = await Promise.all([
        api<GroupList>('/admin/node-groups', { signal }),
        api<AdminPlansResponse>('/admin/console/plans?include_deleted=true', { signal }),
        protocolsLoaded.current
          ? Promise.resolve(null)
          : api<{ supported_protocols?: string[] }>('/admin/nodes', { signal }),
      ]);
      if (!current()) return;
      setList(groups);
      setPlans(available.plans);
      if (legacy) {
        protocolsLoaded.current = true;
        setProtocols(legacy.supported_protocols ?? []);
      }
      setError('');
    } catch (failure) {
      if (current()) setError(message(failure));
    }
  }, []);
  const { syncing, refresh } = usePolling(load, refreshMs);

  useEffect(() => {
    const controller = new AbortController();
    lifetime.current = controller;
    return () => {
      controller.abort();
      editRequest.current?.abort();
    };
  }, []);

  const reload = () => (lifetime.current ? load(lifetime.current.signal) : Promise.resolve());
  const alive = () => !lifetime.current?.signal.aborted;

  // Runs one administrator write; the poll is fenced off until the list is reloaded.
  const mutate = async (action: () => Promise<void>, onError: (text: string) => void) => {
    if (pending.current) return;
    pending.current = true;
    loadRevision.current++;
    setBusy(true);
    try {
      await action();
      if (alive()) await reload();
    } catch (failure) {
      if (alive()) onError(message(failure));
    } finally {
      pending.current = false;
      if (alive()) setBusy(false);
    }
  };

  const groups = list?.groups ?? [];
  const serverCount = groups.reduce((sum, group) => sum + group.servers.length, 0);
  const visible = groups.filter((group) =>
    filter === ALL
      ? true
      : filter === UNSCOPED
        ? group.plan_ids.length === 0
        : group.plan_ids.length === 0 || group.plan_ids.includes(filter),
  );
  const planName = (id: string) => planNameOf(plans, id);
  const migrated = groups.some((group) =>
    group.servers.some((server) => server.max_connections === migratedCapacity),
  );

  const closeForms = () => {
    if (pending.current) return;
    editRequest.current?.abort();
    editRequest.current = null;
    setOpening('');
    setGroupForm(null);
    setServerForm(null);
    setFormError('');
  };

  const openGroup = (group?: AdminGroup) => {
    if (pending.current) return;
    closeForms();
    setGroupForm(
      group
        ? {
            id: group.id,
            version: group.version,
            name: group.name,
            region: group.region,
            line_type: group.line_type,
            rate: group.rate_permille / 1000,
            plan_ids: group.plan_ids,
            enabled: group.enabled,
            sort: group.sort,
          }
        : newGroup(filter !== ALL && filter !== UNSCOPED ? [filter] : []),
    );
  };

  const groupBody = (form: GroupForm) => ({
    name: form.name.trim(),
    region: form.region.trim(),
    line_type: form.line_type,
    rate_permille: Math.round(Number(form.rate) * 1000),
    plan_ids: form.plan_ids,
    enabled: form.enabled,
    sort: Number(form.sort),
    version: form.version,
  });

  const saveGroup = (event: React.FormEvent) => {
    event.preventDefault();
    const form = groupForm;
    if (!form) return;
    const rate = Number(form.rate);
    const sort = Number(form.sort);
    if (
      !form.name.trim() ||
      !form.region.trim() ||
      !Number.isFinite(rate) ||
      rate < 0.001 ||
      rate > 10 ||
      !Number.isInteger(sort) ||
      sort < 0 ||
      sort > 10000
    ) {
      setFormError('请填写组名与地区，倍率为 0.001–10，排序为 0–10000 的整数。');
      return;
    }
    setFormError('');
    void mutate(async () => {
      await api(`/admin/node-groups${form.id ? `/${form.id}` : ''}`, {
        method: 'POST',
        signal: lifetime.current?.signal,
        body: JSON.stringify(groupBody(form)),
      });
      if (!alive()) return;
      setGroupForm(null);
      setNotice(
        form.id
          ? `节点组「${form.name.trim()}」已保存。改名、倍率、线路或套餐会让该组在线设备重新读取配置。`
          : `节点组「${form.name.trim()}」已创建。请向组内导入服务器，组内至少一台已启用的服务器后用户才会看到此线路。`,
      );
    }, setFormError);
  };

  const toggleGroup = (group: AdminGroup) =>
    void mutate(async () => {
      await api(`/admin/node-groups/${group.id}`, {
        method: 'POST',
        signal: lifetime.current?.signal,
        body: JSON.stringify(
          groupBody({
            ...group,
            rate: group.rate_permille / 1000,
            enabled: !group.enabled,
          }),
        ),
      });
      if (alive())
        setNotice(
          group.enabled
            ? `节点组「${group.name}」已停用，组内在线设备已断开，用户目录中不再显示此线路。`
            : `节点组「${group.name}」已启用。`,
        );
    }, setError);

  const openImport = (group: AdminGroup) => {
    if (pending.current) return;
    closeForms();
    setServerForm(newServer(group.id));
  };

  const openServer = async (server: AdminServer) => {
    if (pending.current) return;
    closeForms();
    const controller = new AbortController();
    editRequest.current = controller;
    setOpening(server.id);
    const current = () =>
      editRequest.current === controller && !controller.signal.aborted && alive();
    try {
      const detail = await api<ServerDetail>(`/admin/nodes/${server.id}`, {
        signal: controller.signal,
      });
      if (!current()) return;
      setServerForm({
        id: detail.id,
        version: detail.version,
        group_id: detail.group_id,
        yaml: detail.yaml || '',
        max_connections: detail.max_connections,
        label: detail.label,
        enabled: detail.enabled,
      });
    } catch (failure) {
      if (current()) setError(message(failure));
    } finally {
      if (current()) {
        editRequest.current = null;
        setOpening('');
      }
    }
  };

  const saveServer = (event: React.FormEvent) => {
    event.preventDefault();
    const form = serverForm;
    if (!form) return;
    const capacity = Number(form.max_connections);
    if (
      !form.yaml.trim() ||
      !Number.isInteger(capacity) ||
      capacity < 1 ||
      capacity > 10000 ||
      Array.from(form.label.trim()).length > 40
    ) {
      setFormError('请填写 YAML，最大在线设备数为 1–10000 的整数，备注最多 40 字。');
      return;
    }
    setFormError('');
    void mutate(async () => {
      const result = await api<{ nodes: { id: string }[] }>(
        `/admin/nodes${form.id ? `/${form.id}` : ''}`,
        {
          method: 'POST',
          signal: lifetime.current?.signal,
          body: JSON.stringify({
            yaml: form.yaml,
            group_id: form.group_id,
            max_connections: capacity,
            label: form.label.trim(),
            enabled: form.enabled,
            version: form.version,
          }),
        },
      );
      if (!alive()) return;
      setServerForm(null);
      setNotice(
        form.id
          ? '服务器已保存。修改 YAML、停用或移组时，其上设备会自动改连同组其他服务器；只改容量或备注不影响在线设备。'
          : `已导入 ${result.nodes.length} 台服务器。容量在设备实际连接时生效。`,
      );
    }, setFormError);
  };

  const clearCooldown = (server: AdminServer) =>
    void mutate(async () => {
      await api(`/admin/nodes/${server.id}/health`, {
        method: 'POST',
        signal: lifetime.current?.signal,
        body: '{}',
      });
      if (alive()) setNotice(`已清除「${serverName(server)}」的冷却状态。`);
    }, setError);

  const remove = () => {
    const target = removal;
    if (!target) return;
    setRemovalError('');
    void mutate(async () => {
      const path =
        target.kind === 'group'
          ? `/admin/node-groups/${target.group.id}`
          : `/admin/nodes/${target.server.id}`;
      await api(path, {
        method: 'DELETE',
        signal: lifetime.current?.signal,
        body: JSON.stringify({
          version: target.kind === 'group' ? target.group.version : target.server.version,
        }),
      });
      if (!alive()) return;
      setRemoval(null);
      setNotice(
        target.kind === 'group'
          ? `节点组「${target.group.name}」已删除。`
          : `服务器「${serverName(target.server)}」已删除，其上设备会自动改连同组其他服务器。`,
      );
    }, setRemovalError);
  };

  return (
    <Stack p={{ base: 'md', sm: 'xl' }} gap="lg" maw={1250} mx="auto">
      <Group justify="space-between" align="flex-start">
        <div>
          <Text c="dimmed" size="sm">
            管理员工作台
          </Text>
          <Title order={1}>节点组与服务器</Title>
        </div>
        <Group>
          <Button variant="default" loading={syncing} disabled={busy} onClick={refresh}>
            刷新
          </Button>
          <Button disabled={busy || !list} onClick={() => openGroup()}>
            新建节点组
          </Button>
        </Group>
      </Group>
      <Alert color="blue" title="节点组如何分配">
        用户看到的每条线路是一个节点组，同组服务器统一显示为组名。设备实际连接时，系统在组内挑选一台未满载的服务器：
        优先沿用该用户的固定服务器，否则选负载率最低的一台。容量在设备实际连接时生效；调低容量不踢出在线设备，只拒绝新连接；
        停用或删除服务器会让其上设备自动改连同组其他服务器。在线数每 {refreshMs / 1000} 秒刷新。
      </Alert>
      <Alert color="yellow" title="未绑定节点代理的线路仍是开发联调计费">
        没有节点代理的线路，流量仅由官方客户端上报，共享代理密码仍可能被提取，少报无法仅靠请求签名杜绝，不能用于收费。为服务器登记并上线节点代理后，该组改由节点实测计费、按用户发放独立凭据并由节点强制撤销与到期。普通线路不是本地
        DIRECT 绕过。
      </Alert>
      {migrated ? (
        <Alert color="orange" title="请设置真实容量">
          迁移得到的服务器最大在线设备数为 {migratedCapacity}
          （相当于不限）。请按实际承载能力改为真实值。
        </Alert>
      ) : null}
      {error && (
        <Alert color="red" title="操作失败" withCloseButton onClose={() => setError('')}>
          {error}
        </Alert>
      )}
      {notice && (
        <Alert color="teal" title="已更新" withCloseButton onClose={() => setNotice('')}>
          {notice}
        </Alert>
      )}
      <Paper withBorder radius="md" p="md">
        <Group justify="space-between" align="flex-end" gap="md">
          <Select
            label="套餐筛选"
            allowDeselect={false}
            value={filter}
            onChange={(value) => setFilter(value ?? ALL)}
            data={[
              { value: ALL, label: '全部节点组' },
              { value: UNSCOPED, label: '不限套餐' },
              ...planOptions(plans, filter === ALL || filter === UNSCOPED ? [] : [filter]),
            ]}
            style={{ flex: '0 1 240px', minWidth: 0 }}
          />
          {list ? (
            <Text size="sm" c="dimmed" role="status">
              显示 {visible.length} 个节点组 · 共 {groups.length} / {list.max_groups} 组，
              {serverCount} / {list.max_servers} 台服务器
            </Text>
          ) : null}
        </Group>
        {filter !== ALL && filter !== UNSCOPED ? (
          <Text size="xs" c="dimmed" mt="xs">
            显示「{planName(filter)}
            」用户可见的节点组（含不限套餐的组）；新建节点组时默认限定此套餐。
          </Text>
        ) : null}
      </Paper>
      {!list ? (
        error ? null : (
          <Center mih={160}>
            <Loader aria-label="正在读取节点组" />
          </Center>
        )
      ) : visible.length === 0 ? (
        <Paper withBorder radius="md" p="xl">
          <Text c="dimmed" ta="center">
            {groups.length === 0
              ? '尚未配置节点组。先新建节点组，再向组内导入服务器 YAML。'
              : '当前筛选下没有节点组。'}
          </Text>
        </Paper>
      ) : (
        visible.map((group) => (
          <GroupCard
            key={group.id}
            group={group}
            planName={planName}
            busy={busy}
            opening={opening}
            onEdit={() => openGroup(group)}
            onImport={() => openImport(group)}
            onToggle={() => toggleGroup(group)}
            onDelete={() => {
              setRemovalError('');
              setRemoval({ kind: 'group', group });
            }}
            onEditServer={(server) => void openServer(server)}
            onClearCooldown={clearCooldown}
            onDeleteServer={(server) => {
              setRemovalError('');
              setRemoval({ kind: 'server', group, server });
            }}
          />
        ))
      )}
      <AdminAgentsPanel
        nodes={groups.flatMap((group) =>
          group.servers.map((server) => ({
            id: server.id,
            name: `${group.name} / ${serverName(server)}`,
          })),
        )}
      />
      <Modal
        opened={groupForm !== null}
        onClose={closeForms}
        title={groupForm?.id ? '编辑节点组' : '新建节点组'}
        size="lg"
        closeOnClickOutside={!busy}
        closeOnEscape={!busy}
        withCloseButton={!busy}
      >
        {groupForm ? (
          <form onSubmit={saveGroup}>
            <Stack>
              {formError && (
                <Alert color="red" title="未保存">
                  {formError}
                </Alert>
              )}
              <TextInput
                label="组名"
                description="用户看到的线路名，也是客户端配置中的代理名；同一套餐范围内不能重名。"
                required
                maxLength={120}
                value={groupForm.name}
                disabled={busy}
                onChange={(event) =>
                  setGroupForm({ ...groupForm, name: event.currentTarget.value })
                }
              />
              <Group grow align="flex-start">
                <TextInput
                  label="地区"
                  required
                  maxLength={16}
                  value={groupForm.region}
                  disabled={busy}
                  onChange={(event) =>
                    setGroupForm({ ...groupForm, region: event.currentTarget.value })
                  }
                />
                <Select
                  label="线路类型"
                  allowDeselect={false}
                  data={[
                    { value: 'direct', label: '普通线路' },
                    { value: 'dedicated', label: '专线（需专线权益）' },
                  ]}
                  value={groupForm.line_type}
                  disabled={busy}
                  onChange={(value) => {
                    if (value === 'direct' || value === 'dedicated')
                      setGroupForm({
                        ...groupForm,
                        line_type: value,
                        rate: value === 'direct' ? 0.5 : 1,
                      });
                  }}
                />
                <NumberInput
                  label="流量倍率"
                  description="1 GiB 实际流量 × 倍率 = 扣除量"
                  min={0.001}
                  max={10}
                  step={0.1}
                  decimalScale={3}
                  value={groupForm.rate}
                  disabled={busy}
                  onChange={(rate) => setGroupForm({ ...groupForm, rate })}
                />
              </Group>
              <MultiSelect
                label="限定套餐"
                description="不选择表示所有符合线路权益的套餐；专线仍会校验专线权益。"
                data={planOptions(plans, groupForm.plan_ids)}
                value={groupForm.plan_ids}
                disabled={busy}
                onChange={(plan_ids) => setGroupForm({ ...groupForm, plan_ids })}
              />
              <Group grow align="flex-end">
                <NumberInput
                  label="排序"
                  description="数字越小越靠前"
                  min={0}
                  max={10000}
                  allowDecimal={false}
                  value={groupForm.sort}
                  disabled={busy}
                  onChange={(sort) => setGroupForm({ ...groupForm, sort })}
                />
                <Switch
                  label="启用节点组"
                  checked={groupForm.enabled}
                  disabled={busy}
                  onChange={(event) =>
                    setGroupForm({ ...groupForm, enabled: event.currentTarget.checked })
                  }
                />
              </Group>
              <Text size="sm" c="dimmed">
                停用节点组会断开组内全部在线设备，线路从用户目录中消失，客户端不会自动重连。
              </Text>
              <Group justify="flex-end">
                <Button variant="default" disabled={busy} onClick={closeForms}>
                  取消
                </Button>
                <Button type="submit" loading={busy}>
                  保存节点组
                </Button>
              </Group>
            </Stack>
          </form>
        ) : null}
      </Modal>
      <Modal
        opened={serverForm !== null}
        onClose={closeForms}
        title={serverForm?.id ? '编辑服务器' : '导入服务器'}
        size="xl"
        closeOnClickOutside={!busy}
        closeOnEscape={!busy}
        withCloseButton={!busy}
      >
        {serverForm ? (
          <form onSubmit={saveServer}>
            <Stack>
              {formError && (
                <Alert color="red" title="未保存">
                  {formError}
                </Alert>
              )}
              <Select
                label="所属节点组"
                allowDeselect={false}
                data={groups.map((group) => ({ value: group.id, label: group.name }))}
                value={serverForm.group_id}
                disabled={busy || !serverForm.id}
                description={
                  serverForm.id
                    ? '移到其他组时，其上在线设备会改连原组其他服务器。'
                    : '导入的每个代理成为本组的一台服务器，可与组内其他服务器同名。'
                }
                onChange={(value) => value && setServerForm({ ...serverForm, group_id: value })}
              />
              <Textarea
                label="服务器 YAML"
                description={`仅填写 proxies 列表。${protocols.length ? `支持：${protocols.join(' / ')}。` : ''}${serverForm.id ? '' : '批量导入时，下方容量、备注和启用状态应用到全部服务器。'}配置不写入浏览器存储；不接受文件路径、外部配置和跳过证书验证。`}
                required
                autosize
                minRows={9}
                maxRows={20}
                maxLength={65536}
                value={serverForm.yaml}
                disabled={busy}
                onChange={(event) =>
                  setServerForm({ ...serverForm, yaml: event.currentTarget.value })
                }
                styles={{ input: { fontFamily: 'monospace' } }}
                spellCheck={false}
              />
              <Group grow align="flex-start">
                <NumberInput
                  label="最大在线设备数"
                  description="同时连接到这台服务器的设备上限（1–10000）"
                  min={1}
                  max={10000}
                  allowDecimal={false}
                  value={serverForm.max_connections}
                  disabled={busy}
                  onChange={(max_connections) => setServerForm({ ...serverForm, max_connections })}
                />
                <TextInput
                  label="备注"
                  description="仅后台显示，用于区分组内同名服务器（最多 40 字）"
                  maxLength={40}
                  value={serverForm.label}
                  disabled={busy}
                  onChange={(event) =>
                    setServerForm({ ...serverForm, label: event.currentTarget.value })
                  }
                />
              </Group>
              <Switch
                label="启用服务器"
                checked={serverForm.enabled}
                disabled={busy}
                onChange={(event) =>
                  setServerForm({ ...serverForm, enabled: event.currentTarget.checked })
                }
              />
              <Group justify="flex-end">
                <Button variant="default" disabled={busy} onClick={closeForms}>
                  取消
                </Button>
                <Button type="submit" loading={busy}>
                  保存服务器
                </Button>
              </Group>
            </Stack>
          </form>
        ) : null}
      </Modal>
      <Modal
        opened={removal !== null}
        onClose={() => !busy && setRemoval(null)}
        title={removal?.kind === 'group' ? '确认删除节点组' : '确认删除服务器'}
        closeOnClickOutside={!busy}
        closeOnEscape={!busy}
        withCloseButton={!busy}
      >
        {removal ? (
          <Stack>
            <Text>
              确定永久删除
              {removal.kind === 'group'
                ? `节点组「${removal.group.name}」`
                : `「${removal.group.name}」中的服务器「${serverName(removal.server)}」`}
              吗？此操作不可撤销。
            </Text>
            <Alert color="yellow" title="连接影响">
              {removal.kind === 'group'
                ? '只能删除没有服务器的节点组。'
                : `当前 ${removal.server.online} 台在线设备会自动改连同组其他服务器；组内没有其他可用服务器时这些设备会断开。`}
              历史流量及扣费记录会保留；共享代理凭据仍需在实际服务器上另行撤销。
            </Alert>
            {removalError && (
              <Alert color="red" title="未删除">
                {removalError}
              </Alert>
            )}
            <Group justify="flex-end">
              <Button variant="default" disabled={busy} onClick={() => setRemoval(null)}>
                取消
              </Button>
              <Button color="red" loading={busy} onClick={remove}>
                确认删除
              </Button>
            </Group>
          </Stack>
        ) : null}
      </Modal>
    </Stack>
  );
}

function GroupCard({
  group,
  planName,
  busy,
  opening,
  onEdit,
  onImport,
  onToggle,
  onDelete,
  onEditServer,
  onClearCooldown,
  onDeleteServer,
}: {
  group: AdminGroup;
  planName: (id: string) => string;
  busy: boolean;
  opening: string;
  onEdit: () => void;
  onImport: () => void;
  onToggle: () => void;
  onDelete: () => void;
  onEditServer: (server: AdminServer) => void;
  onClearCooldown: (server: AdminServer) => void;
  onDeleteServer: (server: AdminServer) => void;
}) {
  const [expanded, setExpanded] = useState(true);
  const load = group.capacity ? Math.min(100, (group.online / group.capacity) * 100) : 0;
  const full = group.capacity > 0 && group.online >= group.capacity;
  return (
    <Paper withBorder radius="md" p="md" component="section" aria-label={`节点组 ${group.name}`}>
      <Group justify="space-between" align="flex-start" gap="md" preventGrowOverflow={false}>
        <div style={{ minWidth: 0, flex: '1 1 280px' }}>
          <Group gap="xs">
            <Title order={2} size={18} style={{ overflowWrap: 'anywhere' }}>
              {group.name}
            </Title>
            <Badge color={group.enabled ? 'teal' : 'gray'}>
              {group.enabled ? '已启用' : '已停用'}
            </Badge>
            <Badge
              variant="light"
              color={group.metering_source === 'node_reported' ? 'grape' : 'gray'}
            >
              {group.metering_source === 'node_reported' ? '节点计量' : '客户端上报'}
            </Badge>
          </Group>
          <Text size="sm" c="dimmed" mt={6}>
            {group.region} · {group.line_type === 'dedicated' ? '专线' : '普通线路'} · 倍率{' '}
            {nodeRate(group.rate_permille)}× · 版本 {group.version}
          </Text>
          <Text size="sm" c="dimmed" mt={2}>
            可用套餐：
            {group.plan_ids.length ? group.plan_ids.map(planName).join('、') : '不限套餐'}
          </Text>
        </div>
        <div style={{ flex: '1 1 220px', maxWidth: 320, minWidth: 0 }}>
          <Group justify="space-between" mb={6}>
            <Text
              size="sm"
              fw={600}
              aria-label={`${group.name} 在线 ${group.online} / 容量 ${group.capacity}`}
            >
              在线 {group.online} / 容量 {group.capacity}
            </Text>
            <Text size="xs" c="dimmed">
              {group.servers.length} 台服务器
            </Text>
          </Group>
          <Progress value={load} color={full ? 'orange' : 'teal'} aria-hidden />
        </div>
      </Group>
      {group.mixed_metering ? (
        <Alert color="orange" mt="md" title="组内计量方式混合">
          组内同时有绑定节点代理和未绑定的服务器，只有绑定代理的服务器会被分配。
        </Alert>
      ) : null}
      <Group gap="xs" mt="md">
        <Button
          size="xs"
          variant="light"
          disabled={busy}
          onClick={onEdit}
          aria-label={`编辑节点组 ${group.name}`}
        >
          编辑组
        </Button>
        <Button
          size="xs"
          variant="light"
          disabled={busy}
          onClick={onImport}
          aria-label={`导入服务器到 ${group.name}`}
        >
          导入服务器 YAML
        </Button>
        <Button
          size="xs"
          variant="subtle"
          disabled={busy}
          onClick={onToggle}
          aria-label={`${group.enabled ? '停用' : '启用'}节点组 ${group.name}`}
        >
          {group.enabled ? '停用' : '启用'}
        </Button>
        <Button
          size="xs"
          variant="subtle"
          color="red"
          disabled={busy || group.servers.length > 0}
          title={group.servers.length > 0 ? '请先删除组内全部服务器' : undefined}
          onClick={onDelete}
          aria-label={`删除节点组 ${group.name}`}
        >
          删除
        </Button>
        <Button
          size="xs"
          variant="subtle"
          color="gray"
          ml="auto"
          onClick={() => setExpanded(!expanded)}
          aria-expanded={expanded}
        >
          {expanded ? '收起服务器' : `展开服务器（${group.servers.length}）`}
        </Button>
      </Group>
      <Collapse in={expanded}>
        {group.servers.length === 0 ? (
          <Text c="dimmed" size="sm" py="md" ta="center">
            组内还没有服务器。导入服务器 YAML 后，此线路才会出现在用户目录中。
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={860} mt="md">
            <Table verticalSpacing="sm" highlightOnHover>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>备注</Table.Th>
                  <Table.Th>代理名</Table.Th>
                  <Table.Th>协议</Table.Th>
                  <Table.Th>启用</Table.Th>
                  <Table.Th>健康</Table.Th>
                  <Table.Th>在线 / 上限</Table.Th>
                  <Table.Th>版本</Table.Th>
                  <Table.Th>操作</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {sortedServers(group.servers).map((server) => {
                  const state = health(server);
                  const name = serverName(server);
                  return (
                    <Table.Tr key={server.id} aria-label={`服务器 ${name}`}>
                      <Table.Td>
                        {server.label || (
                          <Text span c="dimmed">
                            —
                          </Text>
                        )}
                      </Table.Td>
                      <Table.Td style={{ overflowWrap: 'anywhere' }}>{server.name}</Table.Td>
                      <Table.Td>{server.protocol || '—'}</Table.Td>
                      <Table.Td>
                        <Badge color={server.enabled ? 'teal' : 'gray'} variant="light">
                          {server.enabled ? '已启用' : '已停用'}
                        </Badge>
                      </Table.Td>
                      <Table.Td>
                        <Badge color={state.color} variant="dot">
                          {state.text}
                        </Badge>
                      </Table.Td>
                      <Table.Td>
                        <Group gap={6}>
                          <Text
                            size="sm"
                            aria-label={`${name} 在线 ${server.online} / 上限 ${server.max_connections}`}
                          >
                            {server.online} / {server.max_connections}
                          </Text>
                          {server.over_capacity ? (
                            <Badge color="orange" size="sm">
                              超出
                            </Badge>
                          ) : null}
                        </Group>
                      </Table.Td>
                      <Table.Td>{server.version}</Table.Td>
                      <Table.Td>
                        <Group gap={2} wrap="nowrap">
                          <Button
                            variant="subtle"
                            size="xs"
                            loading={opening === server.id}
                            disabled={busy || (!!opening && opening !== server.id)}
                            onClick={() => onEditServer(server)}
                            aria-label={`编辑服务器 ${name}`}
                          >
                            编辑
                          </Button>
                          {server.health === 'cooldown' ? (
                            <Button
                              variant="subtle"
                              size="xs"
                              color="orange"
                              disabled={busy}
                              onClick={() => onClearCooldown(server)}
                              aria-label={`清除冷却 ${name}`}
                            >
                              清除冷却
                            </Button>
                          ) : null}
                          <Button
                            variant="subtle"
                            size="xs"
                            color="red"
                            disabled={busy}
                            onClick={() => onDeleteServer(server)}
                            aria-label={`删除服务器 ${name}`}
                          >
                            删除
                          </Button>
                        </Group>
                      </Table.Td>
                    </Table.Tr>
                  );
                })}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Collapse>
    </Paper>
  );
}
