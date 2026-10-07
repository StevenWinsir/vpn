'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Alert,
  Badge,
  Button,
  Center,
  Group,
  Loader,
  Modal,
  MultiSelect,
  NumberInput,
  Paper,
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
import type { Plan } from '@/lib/types';
import { useAuth } from './providers';
import { RequireAuth } from './require-auth';

type AdminNode = {
  id: string;
  name: string;
  region: string;
  line_type: 'direct' | 'dedicated';
  rate_permille: number;
  version: number;
  enabled: boolean;
  plan_ids: string[];
  yaml?: string;
};
type NodeForm = {
  id: string;
  version: number;
  yaml: string;
  region: string;
  line_type: 'direct' | 'dedicated';
  rate: number | string;
  enabled: boolean;
  plan_ids: string[];
};
const emptyForm = (): NodeForm => ({
  id: '',
  version: 0,
  yaml: '',
  region: 'HK',
  line_type: 'direct',
  rate: 0.5,
  enabled: true,
  plan_ids: [],
});

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
  return <NodeManager key={user.id} />;
}

function NodeManager() {
  const [nodes, setNodes] = useState<AdminNode[]>([]);
  const [protocols, setProtocols] = useState<string[]>([]);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [formError, setFormError] = useState('');
  const [notice, setNotice] = useState('');
  const [opened, setOpened] = useState(false);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<AdminNode | null>(null);
  const [deleteError, setDeleteError] = useState('');
  const [editing, setEditing] = useState('');
  const [form, setForm] = useState<NodeForm>(emptyForm);
  const lifetime = useRef<AbortController | null>(null);
  const editRequest = useRef<AbortController | null>(null);
  const savePending = useRef(false);
  const loadRevision = useRef(0);

  const load = useCallback((signal?: AbortSignal) => {
    const revision = ++loadRevision.current;
    const isCurrent = () => !signal?.aborted && revision === loadRevision.current;
    return Promise.all([
      api<{ nodes: AdminNode[]; supported_protocols?: string[] }>('/admin/nodes', { signal }),
      api<{ plans: Plan[] }>('/plans', { signal }),
    ])
      .then(([catalog, available]) => {
        if (!isCurrent()) return;
        setNodes(catalog.nodes);
        setProtocols(catalog.supported_protocols ?? []);
        setPlans(available.plans);
        setError('');
      })
      .catch((failure: unknown) => {
        if (isCurrent()) setError(message(failure));
      })
      .finally(() => {
        if (isCurrent()) setLoading(false);
      });
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    lifetime.current = controller;
    void load(controller.signal);
    return () => {
      controller.abort();
      editRequest.current?.abort();
    };
  }, [load]);

  const edit = async (node: AdminNode) => {
    if (savePending.current) return;
    editRequest.current?.abort();
    const controller = new AbortController();
    editRequest.current = controller;
    setEditing(node.id);
    const isCurrent = () =>
      editRequest.current === controller &&
      !controller.signal.aborted &&
      !lifetime.current?.signal.aborted;
    try {
      const detail = await api<AdminNode>(`/admin/nodes/${node.id}`, {
        signal: controller.signal,
      });
      if (!isCurrent()) return;
      setForm({ ...detail, yaml: detail.yaml || '', rate: detail.rate_permille / 1000 });
      setFormError('');
      setOpened(true);
    } catch (failure) {
      if (isCurrent()) setError(message(failure));
    } finally {
      if (isCurrent()) {
        editRequest.current = null;
        setEditing('');
      }
    }
  };

  const cancelEdit = () => {
    editRequest.current?.abort();
    editRequest.current = null;
    setEditing('');
  };

  const close = () => {
    if (savePending.current) return;
    cancelEdit();
    setOpened(false);
    setForm(emptyForm());
    setFormError('');
  };

  const closeDelete = () => {
    if (savePending.current) return;
    setDeleteTarget(null);
    setDeleteError('');
  };

  const remove = async () => {
    if (!deleteTarget || savePending.current) return;
    const target = deleteTarget;
    savePending.current = true;
    loadRevision.current++;
    setDeleting(true);
    setDeleteError('');
    setNotice('');
    try {
      await api(`/admin/nodes/${target.id}`, {
        method: 'DELETE',
        signal: lifetime.current?.signal,
        body: JSON.stringify({ version: target.version }),
      });
      if (lifetime.current?.signal.aborted) return;
      setNodes((current) => current.filter((node) => node.id !== target.id));
      setDeleteTarget(null);
      setNotice('节点已删除。在线新版客户端会自动同步目录、停止旧连接并结清流量；剩余节点需重新点击连接。');
      await load(lifetime.current?.signal);
    } catch (failure) {
      if (!lifetime.current?.signal.aborted) setDeleteError(message(failure));
    } finally {
      savePending.current = false;
      if (!lifetime.current?.signal.aborted) setDeleting(false);
    }
  };

  const save = async (event: React.FormEvent) => {
    event.preventDefault();
    if (savePending.current) return;
    const rate = Number(form.rate);
    if (!Number.isFinite(rate) || rate < 0.001 || rate > 10 || !form.yaml.trim()) {
      setFormError('请填写 YAML，并输入 0.001–10 之间的流量倍率。');
      return;
    }
    savePending.current = true;
    setSaving(true);
    setFormError('');
    try {
      const result = await api<{ nodes: AdminNode[] }>(
        `/admin/nodes${form.id ? `/${form.id}` : ''}`,
        {
          method: 'POST',
          signal: lifetime.current?.signal,
          body: JSON.stringify({
            yaml: form.yaml,
            region: form.region.trim(),
            line_type: form.line_type,
            rate_permille: Math.round(rate * 1000),
            enabled: form.enabled,
            plan_ids: form.plan_ids,
            version: form.version,
          }),
        },
      );
      if (lifetime.current?.signal.aborted) return;
      setNotice(
        `已保存 ${result.nodes.length} 个节点。客户端下次同步会读取新配置；正在使用旧版本的连接会在授权检查后停止。`,
      );
      setOpened(false);
      setForm(emptyForm());
      await load(lifetime.current?.signal);
    } catch (failure) {
      if (!lifetime.current?.signal.aborted) setFormError(message(failure));
    } finally {
      savePending.current = false;
      if (!lifetime.current?.signal.aborted) setSaving(false);
    }
  };

  return (
    <Stack p={{ base: 'md', sm: 'xl' }} gap="lg" maw={1250} mx="auto">
      <Group justify="space-between" align="flex-start">
        <div>
          <Text c="dimmed" size="sm">
            管理员工作台
          </Text>
          <Title order={1}>节点与流量倍率</Title>
        </div>
        <Group>
          <Button
            variant="default"
            loading={loading}
            disabled={saving || deleting}
            onClick={() => {
              setLoading(true);
              void load(lifetime.current?.signal);
            }}
          >
            刷新目录
          </Button>
          <Button
            disabled={saving || deleting}
            onClick={() => {
              if (savePending.current) return;
              cancelEdit();
              setForm(emptyForm());
              setFormError('');
              setOpened(true);
            }}
          >
            导入节点 YAML
          </Button>
        </Group>
      </Group>
      <Alert color="yellow" title="开发联调计费，不是服务端强制配额">
        当前流量仅由官方客户端上报，后端按节点倍率记账。共享代理密码仍可能被提取；即使保留客户端计费，服务节点也需要每用户鉴权与到期撤销。客户端被篡改后的少报无法仅靠请求签名杜绝。普通线路不是本地
        DIRECT 绕过。
      </Alert>
      {error && (
        <Alert color="red" title="读取失败">
          {error}
        </Alert>
      )}
      {notice && (
        <Alert color="teal" title="节点已更新" withCloseButton onClose={() => setNotice('')}>
          {notice}
        </Alert>
      )}
      <Paper withBorder radius="md" p="md">
        <Group justify="space-between" mb="md">
          <Text fw={600}>节点目录</Text>
          <Badge variant="light">{nodes.length} / 128</Badge>
        </Group>
        {loading ? (
          <Center mih={160}>
            <Loader aria-label="正在读取节点" />
          </Center>
        ) : nodes.length === 0 ? (
          <Text c="dimmed" py="xl" ta="center">
            尚未配置节点。导入后，符合套餐权益的客户端才会获得线路。
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={780}>
            <Table verticalSpacing="md" highlightOnHover>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>节点</Table.Th>
                  <Table.Th>线路</Table.Th>
                  <Table.Th>扣费倍率</Table.Th>
                  <Table.Th>可用套餐</Table.Th>
                  <Table.Th>状态</Table.Th>
                  <Table.Th>操作</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {nodes.map((node) => (
                  <Table.Tr key={node.id}>
                    <Table.Td>
                      <Text fw={500}>{node.name}</Text>
                      <Text size="xs" c="dimmed">
                        {node.region} · 版本 {node.version}
                      </Text>
                    </Table.Td>
                    <Table.Td>{node.line_type === 'dedicated' ? '专线' : '普通线路'}</Table.Td>
                    <Table.Td>{node.rate_permille / 1000}×</Table.Td>
                    <Table.Td>
                      {node.plan_ids.length
                        ? node.plan_ids
                            .map((id) => plans.find((plan) => plan.id === id)?.name || id)
                            .join('、')
                        : '所有符合权益的套餐'}
                    </Table.Td>
                    <Table.Td>
                      <Badge color={node.enabled ? 'teal' : 'gray'}>
                        {node.enabled ? '已启用' : '已停用'}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      <Button
                        variant="subtle"
                        size="xs"
                        loading={editing === node.id}
                        disabled={saving || deleting || (!!editing && editing !== node.id)}
                        onClick={() => void edit(node)}
                        aria-label={`编辑 ${node.name}`}
                      >
                        编辑
                      </Button>
                      <Button
                        variant="subtle"
                        color="red"
                        size="xs"
                        disabled={saving || deleting}
                        aria-label={`删除 ${node.name}`}
                        onClick={() => {
                          if (savePending.current) return;
                          cancelEdit();
                          setDeleteError('');
                          setDeleteTarget(node);
                        }}
                      >
                        删除
                      </Button>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Paper>
      <Modal
        opened={deleteTarget !== null}
        onClose={closeDelete}
        title="确认删除节点"
        closeOnClickOutside={!deleting}
        closeOnEscape={!deleting}
        withCloseButton={!deleting}
      >
        <Stack>
          <Text>确定永久删除「{deleteTarget?.name}」吗？此操作不可撤销。</Text>
          <Alert color="yellow" title="连接影响">
            在线新版客户端会同步删除并停止旧配置的连接。删除最后一个可用节点后，用户将无法连接。历史流量及扣费记录会保留；共享代理凭据仍需在实际服务器上另行撤销。
          </Alert>
          {deleteError && <Alert color="red" title="未删除">{deleteError}</Alert>}
          <Group justify="flex-end">
            <Button variant="default" disabled={deleting} onClick={closeDelete}>取消</Button>
            <Button color="red" loading={deleting} onClick={() => void remove()}>确认删除</Button>
          </Group>
        </Stack>
      </Modal>
      <Modal
        opened={opened}
        onClose={close}
        title={form.id ? '编辑节点配置' : '批量导入节点'}
        size="xl"
        closeOnClickOutside={!saving}
        closeOnEscape={!saving}
        withCloseButton={!saving}
      >
        <form onSubmit={(event) => void save(event)}>
          <Stack>
            {formError && (
              <Alert color="red" title="未保存">
                {formError}
              </Alert>
            )}
            <Textarea
              label="节点 YAML"
              description={`仅填写 proxies 列表。${protocols.length ? `支持：${protocols.join(' / ')}。` : ''}批量导入时，下方属性应用到全部节点。配置不写入浏览器存储；不接受文件路径、外部配置和跳过证书验证。`}
              required
              autosize
              minRows={9}
              maxRows={20}
              maxLength={65536}
              value={form.yaml}
              disabled={saving}
              onChange={(event) => setForm({ ...form, yaml: event.currentTarget.value })}
              styles={{ input: { fontFamily: 'monospace' } }}
              spellCheck={false}
            />
            <Group grow align="flex-start">
              <TextInput
                label="地区"
                required
                maxLength={16}
                value={form.region}
                disabled={saving}
                onChange={(event) => setForm({ ...form, region: event.currentTarget.value })}
              />
              <Select
                label="线路类型"
                allowDeselect={false}
                data={[
                  { value: 'direct', label: '普通线路' },
                  { value: 'dedicated', label: '专线（需专线权益）' },
                ]}
                value={form.line_type}
                disabled={saving}
                onChange={(value) => {
                  if (value === 'direct' || value === 'dedicated')
                    setForm({ ...form, line_type: value, rate: value === 'direct' ? 0.5 : 1 });
                }}
              />
              <NumberInput
                label="流量倍率"
                description="1 GiB 实际流量 × 倍率 = 扣除量"
                min={0.001}
                max={10}
                step={0.1}
                decimalScale={3}
                value={form.rate}
                disabled={saving}
                onChange={(rate) => setForm({ ...form, rate })}
              />
            </Group>
            <MultiSelect
              label="限定套餐"
              description="不选择表示所有符合线路权益的套餐；专线仍会校验专线权益。"
              data={plans.map((plan) => ({ value: plan.id, label: plan.name }))}
              value={form.plan_ids}
              disabled={saving}
              onChange={(plan_ids) => setForm({ ...form, plan_ids })}
            />
            <Switch
              label="启用节点"
              checked={form.enabled}
              disabled={saving}
              onChange={(event) => setForm({ ...form, enabled: event.currentTarget.checked })}
            />
            <Text size="sm" c="dimmed">
              保存后服务端通知在线新版客户端同步目录；跨后端实例按秒检查。计费仍每 60 秒上报，断网时 90
              秒授权租约到期会停止；目录变更会先断开并结清旧流量，需要重新点击连接。
            </Text>
            <Group justify="flex-end">
              <Button variant="default" disabled={saving} onClick={close}>
                取消
              </Button>
              <Button type="submit" loading={saving}>
                保存节点
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Stack>
  );
}
