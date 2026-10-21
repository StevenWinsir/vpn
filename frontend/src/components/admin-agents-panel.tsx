'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Alert,
  Badge,
  Button,
  Group,
  Loader,
  Modal,
  Paper,
  Select,
  Stack,
  Switch,
  Table,
  Text,
  TextInput,
} from '@mantine/core';
import { api, message } from '@/lib/api';

type AgentNode = { id: string; name: string };
type NodeAgent = {
  id: string;
  node_id: string;
  name: string;
  cert_fingerprint: string;
  enabled: boolean;
  protocols: string[];
  agent_version: string;
  epoch: number;
  last_seen_at: string | null;
  online: boolean;
  open_grants: number;
  needs_reconcile: number;
};
type AgentList = { agents: NodeAgent[]; stale_seconds: number; agent_listener_enabled: boolean };
type AgentForm = {
  id: string;
  node_id: string;
  name: string;
  fingerprint: string;
  enabled: boolean;
};
const emptyForm = (): AgentForm => ({
  id: '',
  node_id: '',
  name: '',
  fingerprint: '',
  enabled: true,
});

const refreshMs = 5000;

function seen(value: string | null) {
  if (!value) return '从未连接';
  const seconds = Math.max(0, Math.round((Date.now() - new Date(value).getTime()) / 1000));
  return seconds < 90 ? `${seconds} 秒前` : new Date(value).toLocaleString();
}

/**
 * Node agents make a line "node-metered": the node itself issues per-user credentials,
 * counts the bytes and stops revoked users. Only an agent that is online and claims a
 * protocol can serve grants; offline agents make that line unavailable (never fall back
 * to shared credentials).
 */
export function AdminAgentsPanel({ nodes }: { nodes: AgentNode[] }) {
  const [data, setData] = useState<AgentList | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [formError, setFormError] = useState('');
  const [opened, setOpened] = useState(false);
  const [busy, setBusy] = useState(false);
  const [form, setForm] = useState<AgentForm>(emptyForm);
  const [removeTarget, setRemoveTarget] = useState<NodeAgent | null>(null);
  const lifetime = useRef<AbortController | null>(null);
  const pending = useRef(false);

  const load = useCallback(
    (signal?: AbortSignal) =>
      api<AgentList>('/admin/node-agents', { signal })
        .then((list) => {
          if (signal?.aborted) return;
          setData(list);
          setError('');
        })
        .catch((failure: unknown) => {
          if (!signal?.aborted) setError(message(failure));
        }),
    [],
  );

  useEffect(() => {
    const controller = new AbortController();
    lifetime.current = controller;
    void load(controller.signal);
    const timer = window.setInterval(() => {
      if (!pending.current) void load(controller.signal);
    }, refreshMs);
    return () => {
      controller.abort();
      window.clearInterval(timer);
    };
  }, [load]);

  const nodeName = (id: string) => nodes.find((node) => node.id === id)?.name ?? id.slice(0, 8);
  const bound = new Set((data?.agents ?? []).map((agent) => agent.node_id));
  const unbound = nodes.filter((node) => !bound.has(node.id));

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (pending.current) return;
    const fingerprint = form.fingerprint.replace(/:/g, '').trim().toLowerCase();
    if (!form.name.trim() || !/^[0-9a-f]{64}$/.test(fingerprint) || (!form.id && !form.node_id)) {
      setFormError('请选择服务器、填写名称，并粘贴 64 位十六进制的客户端证书 SHA-256 指纹。');
      return;
    }
    pending.current = true;
    setBusy(true);
    setFormError('');
    try {
      await api(`/admin/node-agents${form.id ? `/${form.id}` : ''}`, {
        method: 'POST',
        signal: lifetime.current?.signal,
        body: JSON.stringify({
          node_id: form.node_id,
          name: form.name.trim(),
          cert_fingerprint: fingerprint,
          enabled: form.enabled,
        }),
      });
      setNotice('节点代理已保存。代理上线并上报支持的协议后，该线路才会向客户端发放独立凭据。');
      setOpened(false);
      setForm(emptyForm());
      await load(lifetime.current?.signal);
    } catch (failure) {
      if (!lifetime.current?.signal.aborted) setFormError(message(failure));
    } finally {
      pending.current = false;
      setBusy(false);
    }
  };

  const toggle = async (agent: NodeAgent) => {
    if (pending.current) return;
    pending.current = true;
    setBusy(true);
    try {
      await api(`/admin/node-agents/${agent.id}`, {
        method: 'POST',
        signal: lifetime.current?.signal,
        body: JSON.stringify({
          name: agent.name,
          cert_fingerprint: agent.cert_fingerprint,
          enabled: !agent.enabled,
        }),
      });
      await load(lifetime.current?.signal);
    } catch (failure) {
      if (!lifetime.current?.signal.aborted) setError(message(failure));
    } finally {
      pending.current = false;
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!removeTarget || pending.current) return;
    pending.current = true;
    setBusy(true);
    try {
      await api(`/admin/node-agents/${removeTarget.id}`, {
        method: 'DELETE',
        signal: lifetime.current?.signal,
        body: JSON.stringify({}),
      });
      setRemoveTarget(null);
      setNotice('已移除节点代理。该线路在授权租约到期后停止服务，且不再发放新凭据。');
      await load(lifetime.current?.signal);
    } catch (failure) {
      if (!lifetime.current?.signal.aborted) setError(message(failure));
    } finally {
      pending.current = false;
      setBusy(false);
    }
  };

  return (
    <Paper withBorder radius="md" p="md" data-testid="agent-panel">
      <Group justify="space-between" mb="xs">
        <div>
          <Text fw={600}>节点代理（节点侧独立计量）</Text>
          <Text size="xs" c="dimmed">
            绑定后，该线路改由节点实测计费并按用户发放独立凭据；撤销、到期、额度耗尽由节点强制执行。
          </Text>
        </div>
        <Button
          size="xs"
          disabled={busy || unbound.length === 0}
          onClick={() => {
            setForm(emptyForm());
            setFormError('');
            setOpened(true);
          }}
        >
          登记节点代理
        </Button>
      </Group>
      {data && !data.agent_listener_enabled && (
        <Alert color="orange" mb="sm" title="后端未开启代理接口">
          后端 .env 未配置 NODE_AGENT_ADDR 及 mTLS 证书，节点代理无法连接。
        </Alert>
      )}
      {error && (
        <Alert color="red" mb="sm" title="读取失败">
          {error}
        </Alert>
      )}
      {notice && (
        <Alert color="teal" mb="sm" withCloseButton onClose={() => setNotice('')}>
          {notice}
        </Alert>
      )}
      {!data && !error ? (
        <Loader size="sm" aria-label="正在读取节点代理" />
      ) : data && data.agents.length === 0 ? (
        <Text c="dimmed" size="sm" py="md">
          尚未登记节点代理。未绑定代理的线路仍使用共享凭据与客户端上报（仅限开发联调）。
        </Text>
      ) : (
        data && (
          <Table.ScrollContainer minWidth={820}>
            <Table verticalSpacing="sm">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>代理</Table.Th>
                  <Table.Th>服务器</Table.Th>
                  <Table.Th>状态</Table.Th>
                  <Table.Th>已验证协议</Table.Th>
                  <Table.Th>在用授权</Table.Th>
                  <Table.Th>待对账</Table.Th>
                  <Table.Th>操作</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {data.agents.map((agent) => (
                  <Table.Tr key={agent.id}>
                    <Table.Td>
                      <Text fw={500}>{agent.name}</Text>
                      <Text size="xs" c="dimmed">
                        {agent.agent_version || '未上报版本'} · 代次 {agent.epoch || '—'}
                      </Text>
                    </Table.Td>
                    <Table.Td>{nodeName(agent.node_id)}</Table.Td>
                    <Table.Td>
                      <Badge color={!agent.enabled ? 'gray' : agent.online ? 'teal' : 'red'}>
                        {!agent.enabled ? '已停用' : agent.online ? '在线' : '离线'}
                      </Badge>
                      <Text size="xs" c="dimmed">
                        心跳 {seen(agent.last_seen_at)}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      {agent.protocols.length ? agent.protocols.join(' / ') : '—'}
                    </Table.Td>
                    <Table.Td>{agent.open_grants}</Table.Td>
                    <Table.Td>
                      {agent.needs_reconcile > 0 ? (
                        <Badge color="orange" title="代理重启或失联导致用量可能缺失，需人工核对">
                          {agent.needs_reconcile}
                        </Badge>
                      ) : (
                        0
                      )}
                    </Table.Td>
                    <Table.Td>
                      <Group gap={4} wrap="nowrap">
                        <Switch
                          size="xs"
                          checked={agent.enabled}
                          disabled={busy}
                          aria-label={`${agent.enabled ? '停用' : '启用'} ${agent.name}`}
                          onChange={() => void toggle(agent)}
                        />
                        <Button
                          variant="subtle"
                          color="red"
                          size="xs"
                          disabled={busy}
                          aria-label={`移除 ${agent.name}`}
                          onClick={() => setRemoveTarget(agent)}
                        >
                          移除
                        </Button>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )
      )}
      <Modal
        opened={opened}
        onClose={() => !busy && setOpened(false)}
        title="登记节点代理"
        size="lg"
      >
        <form onSubmit={(event) => void submit(event)}>
          <Stack>
            {formError && (
              <Alert color="red" title="未保存">
                {formError}
              </Alert>
            )}
            <Select
              label="服务器（节点组 / 备注）"
              required
              placeholder="选择尚未绑定代理的服务器"
              data={unbound.map((node) => ({ value: node.id, label: node.name }))}
              value={form.node_id}
              disabled={busy}
              onChange={(value) => setForm({ ...form, node_id: value ?? '' })}
            />
            <TextInput
              label="代理名称"
              required
              maxLength={80}
              value={form.name}
              disabled={busy}
              onChange={(event) => setForm({ ...form, name: event.currentTarget.value })}
            />
            <TextInput
              label="客户端证书 SHA-256 指纹"
              description="在节点上运行 asterlink-nodeagent -print-fingerprint 获得。后端用它固定代理身份（另需 mTLS 证书链校验）。"
              required
              value={form.fingerprint}
              disabled={busy}
              onChange={(event) => setForm({ ...form, fingerprint: event.currentTarget.value })}
              styles={{ input: { fontFamily: 'monospace' } }}
              spellCheck={false}
            />
            <Switch
              label="启用"
              checked={form.enabled}
              disabled={busy}
              onChange={(event) => setForm({ ...form, enabled: event.currentTarget.checked })}
            />
            <Group justify="flex-end">
              <Button variant="default" disabled={busy} onClick={() => setOpened(false)}>
                取消
              </Button>
              <Button type="submit" loading={busy}>
                保存
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
      <Modal
        opened={removeTarget !== null}
        onClose={() => !busy && setRemoveTarget(null)}
        title="移除节点代理"
      >
        <Stack>
          <Text>
            确定移除「{removeTarget?.name}
            」？该线路将无法再发放凭据；已发放的授权在其绝对到期时间（约一个租约）后由节点自行停止。
          </Text>
          <Group justify="flex-end">
            <Button variant="default" disabled={busy} onClick={() => setRemoveTarget(null)}>
              取消
            </Button>
            <Button color="red" loading={busy} onClick={() => void remove()}>
              确认移除
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Paper>
  );
}
