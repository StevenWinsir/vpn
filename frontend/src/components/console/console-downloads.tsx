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
  Select,
  Skeleton,
  Stack,
  Switch,
  Table,
  Text,
  TextInput,
  Title,
} from '@mantine/core';
import { toast } from '@/lib/toast';
import { api, APIError } from '@/lib/api';
import { megabytes } from '@/lib/downloads';
import {
  archLabels,
  firstFree,
  formatLabels,
  isStoreFormat,
  label,
  parseSize,
  platformLabels,
  platformOf,
  takenVariants,
  toInput,
  validateDownload,
  type Catalog,
  type ClientDownload,
  type DownloadField,
  type DownloadInput,
} from '@/lib/download-catalog';
import { describeError, ErrorBox, useFetched, When } from './console-common';

// The console's "客户端下载" tab (handoff_download.md 7): one table per platform in the order the
// download page uses, an editor whose choices come from the server's catalogue, a publish switch
// and a confirmed delete. The real address shows here only; the public page never has it (D4).
type List = { downloads: ClientDownload[]; catalog: Catalog };
type Dialog =
  | { kind: 'edit'; platform: string; id: string | null }
  | { kind: 'delete'; row: ClientDownload }
  | null;

const variantName = (row: { platform: string; arch: string; format: string }) =>
  `${label(platformLabels, row.platform)} · ${label(archLabels, row.arch)} · ${label(formatLabels, row.format)}`;

export function ConsoleDownloads() {
  const [nonce, setNonce] = useState(0);
  const [dialog, setDialog] = useState<Dialog>(null);
  const [switching, setSwitching] = useState('');
  const { data, error, loading } = useFetched<List>('/admin/console/downloads', nonce);
  const refresh = () => setNonce((n) => n + 1);
  const close = () => setDialog(null);
  const rows = data?.downloads ?? [];

  const togglePublished = async (row: ClientDownload) => {
    setSwitching(row.id);
    try {
      await api(`/admin/console/downloads/${row.id}`, {
        method: 'POST',
        body: JSON.stringify({ ...toInput(row), published: !row.published }),
      });
      toast.success(row.published ? '已下架' : '已上架', variantName(row));
    } catch (e) {
      toast.error('操作失败', describeError(e));
    } finally {
      setSwitching('');
      refresh();
    }
  };

  // The editor follows the list: after a refresh it opens again on the newest copy of the row.
  const editing =
    dialog?.kind === 'edit' && dialog.id ? rows.find((r) => r.id === dialog.id) : undefined;

  return (
    <Stack gap="lg">
      <Alert color="blue" variant="light" data-testid="downloads-notice">
        保存后约 30
        秒内在下载页生效。填写前请先确认地址可以正常下载。新增的条目默认不上架，打开“上架”后才会在下载页显示。
      </Alert>
      <ErrorBox text={error} />
      {loading && !data ? (
        <Skeleton height={320} />
      ) : data ? (
        <>
          {data.catalog.platforms.map(({ platform }) => {
            const group = rows.filter((r) => r.platform === platform);
            const full = firstFree(data.catalog, rows, platform) === null;
            const name = label(platformLabels, platform);
            return (
              <Paper
                key={platform}
                withBorder
                radius="lg"
                p="md"
                data-testid={`download-group-${platform}`}
              >
                <Group justify="space-between" mb="sm">
                  <Title order={2} size={18}>
                    {name}
                  </Title>
                  <Button
                    size="xs"
                    onClick={() => setDialog({ kind: 'edit', platform, id: null })}
                    disabled={full || rows.length >= data.catalog.limit}
                    title={full ? '该平台的架构与格式组合已全部添加' : undefined}
                    aria-label={`添加 ${name}`}
                  >
                    添加
                  </Button>
                </Group>
                {group.length === 0 ? (
                  <Text c="dimmed" size="sm" py="md" ta="center" data-testid="download-empty">
                    尚未添加
                  </Text>
                ) : (
                  <ScrollArea>
                    <Table
                      verticalSpacing="sm"
                      horizontalSpacing="md"
                      miw={980}
                      aria-label={`${name} 下载条目`}
                    >
                      <Table.Thead>
                        <Table.Tr>
                          <Table.Th>架构</Table.Th>
                          <Table.Th>格式</Table.Th>
                          <Table.Th>版本</Table.Th>
                          <Table.Th>大小</Table.Th>
                          <Table.Th>SHA-256</Table.Th>
                          <Table.Th>状态</Table.Th>
                          <Table.Th>更新时间</Table.Th>
                          <Table.Th />
                        </Table.Tr>
                      </Table.Thead>
                      <Table.Tbody>
                        {group.map((row) => (
                          <Table.Tr
                            key={row.id}
                            data-testid="download-row"
                            data-variant={`${row.platform}/${row.arch}/${row.format}`}
                          >
                            <Table.Td>{label(archLabels, row.arch)}</Table.Td>
                            <Table.Td>{label(formatLabels, row.format)}</Table.Td>
                            <Table.Td>
                              <Text size="sm" ff="monospace">
                                {row.app_version}
                              </Text>
                            </Table.Td>
                            <Table.Td>
                              {row.size_bytes === null ? (
                                <Text span c="dimmed">
                                  —
                                </Text>
                              ) : (
                                <span title={`${row.size_bytes} 字节`}>
                                  {megabytes(row.size_bytes)}
                                </span>
                              )}
                            </Table.Td>
                            <Table.Td>
                              {row.sha256 ? (
                                <Text
                                  span
                                  size="sm"
                                  ff="monospace"
                                  title={row.sha256}
                                  data-testid="download-sha"
                                >
                                  {row.sha256.slice(0, 8)}…
                                </Text>
                              ) : (
                                <Text span c="dimmed">
                                  —
                                </Text>
                              )}
                            </Table.Td>
                            <Table.Td>
                              <Switch
                                checked={row.published}
                                disabled={switching === row.id}
                                onChange={() => void togglePublished(row)}
                                label={row.published ? '已上架' : '未上架'}
                                aria-label={`上架 ${variantName(row)}`}
                              />
                            </Table.Td>
                            <Table.Td>
                              <When value={row.updated_at} />
                            </Table.Td>
                            <Table.Td>
                              <Group gap={6} wrap="nowrap">
                                <Button
                                  size="xs"
                                  variant="light"
                                  onClick={() => setDialog({ kind: 'edit', platform, id: row.id })}
                                  aria-label={`编辑 ${variantName(row)}`}
                                >
                                  编辑
                                </Button>
                                <Button
                                  component="a"
                                  size="xs"
                                  variant="subtle"
                                  href={row.url}
                                  target="_blank"
                                  rel="noopener noreferrer"
                                  aria-label={`在新标签打开 ${variantName(row)} 的地址`}
                                >
                                  打开地址
                                </Button>
                                <Button
                                  size="xs"
                                  variant="light"
                                  color="red"
                                  onClick={() => setDialog({ kind: 'delete', row })}
                                  aria-label={`删除 ${variantName(row)}`}
                                >
                                  删除
                                </Button>
                              </Group>
                            </Table.Td>
                          </Table.Tr>
                        ))}
                      </Table.Tbody>
                    </Table>
                  </ScrollArea>
                )}
              </Paper>
            );
          })}
          <Text size="sm" c="dimmed" data-testid="download-count">
            共 {rows.length} 条，最多 {data.catalog.limit} 条
          </Text>
        </>
      ) : null}
      {data && dialog?.kind === 'edit' && (dialog.id === null || editing) ? (
        <Editor
          key={editing ? `${editing.id}:${editing.version}` : `new:${dialog.platform}`}
          catalog={data.catalog}
          rows={rows}
          platform={dialog.platform}
          item={editing ?? null}
          onClose={close}
          onRefresh={refresh}
        />
      ) : null}
      {dialog?.kind === 'delete' ? (
        <DeleteDialog row={dialog.row} onClose={close} onDone={refresh} />
      ) : null}
    </Stack>
  );
}

function Editor({
  catalog,
  rows,
  platform,
  item,
  onClose,
  onRefresh,
}: {
  catalog: Catalog;
  rows: ClientDownload[];
  platform: string;
  item: ClientDownload | null;
  onClose: () => void;
  onRefresh: () => void;
}) {
  const start = item ? toInput(item) : null;
  const free = firstFree(catalog, rows, platform);
  const [arch, setArch] = useState(start?.arch ?? free?.arch ?? '');
  const [format, setFormat] = useState(start?.format ?? free?.format ?? '');
  const [version, setVersion] = useState(start?.app_version ?? '');
  const [url, setURL] = useState(start?.url ?? '');
  const [sha, setSha] = useState(start?.sha256 ?? '');
  const [size, setSize] = useState(start?.size_bytes == null ? '' : String(start.size_bytes));
  const [cert, setCert] = useState(start?.signing_cert_sha256 ?? '');
  const [bundle, setBundle] = useState(start?.bundle_id ?? '');
  const [published, setPublished] = useState(start?.published ?? false);
  const [attempted, setAttempted] = useState(false);
  const [server, setServer] = useState<{ field: string; message: string } | null>(null);
  const [error, setError] = useState('');
  const [conflict, setConflict] = useState(false);
  const [busy, setBusy] = useState(false);

  const entry = platformOf(catalog, platform);
  const taken = takenVariants(rows, platform, item?.id);
  const store = isStoreFormat(catalog, format);
  const showCert = platform === 'android';
  const showBundle = format === 'ipa';
  const parsedSize = parseSize(size);

  const input: DownloadInput = {
    platform,
    arch,
    format,
    app_version: version.trim(),
    url: url.trim(),
    sha256: store ? '' : sha,
    size_bytes: store || parsedSize === 'invalid' ? null : parsedSize,
    signing_cert_sha256: showCert ? cert : '',
    bundle_id: showBundle ? bundle.trim() : '',
    published,
    ...(item ? { version: item.version } : {}),
  };
  const local =
    parsedSize === 'invalid' && !store
      ? { field: 'size_bytes' as DownloadField, message: '文件大小须为 1 字节到 8 GiB 的整数' }
      : validateDownload(catalog, input);
  const clash = taken.has(`${arch}|${format}`);
  const fieldError = (field: DownloadField) => {
    if (server?.field === field) return server.message;
    if (attempted && local?.field === field) return local.message;
    return undefined;
  };
  // Editing a field clears what the server said about it.
  const edit =
    (field: DownloadField, set: (value: string) => void) =>
    (e: React.ChangeEvent<HTMLInputElement>) => {
      set(e.currentTarget.value);
      if (server?.field === field) setServer(null);
    };

  const pickArch = (next: string | null) => {
    if (!next) return;
    setArch(next);
    if (server?.field === 'arch') setServer(null);
    if (taken.has(`${next}|${format}`)) {
      const open = entry?.formats.find((f) => !taken.has(`${next}|${f}`));
      if (open) setFormat(open);
    }
  };

  const submit = async () => {
    setAttempted(true);
    setError('');
    setConflict(false);
    if (local || clash) return;
    setBusy(true);
    try {
      await api(item ? `/admin/console/downloads/${item.id}` : '/admin/console/downloads', {
        method: 'POST',
        body: JSON.stringify(input),
      });
      toast.success(item ? '已保存' : '已添加', variantName(input));
      onRefresh();
      onClose();
    } catch (e) {
      const field = e instanceof APIError ? e.details.field : undefined;
      if (e instanceof APIError && e.code === 'invalid_download' && typeof field === 'string') {
        setServer({ field, message: e.message });
      } else if (e instanceof APIError && e.code === 'version_conflict') {
        setConflict(true);
      } else {
        setError(describeError(e));
      }
    } finally {
      setBusy(false);
    }
  };

  const sizeHint =
    typeof parsedSize === 'number' ? `约 ${megabytes(parsedSize)}` : '以字节为单位，可不填';
  return (
    <Modal
      opened
      onClose={onClose}
      title={item ? `编辑 ${variantName(item)}` : `添加 ${label(platformLabels, platform)} 下载`}
      size="lg"
      closeOnClickOutside={false}
    >
      <Stack data-testid="download-editor">
        <TextInput label="平台" value={label(platformLabels, platform)} disabled />
        <Group grow align="flex-start">
          <Select
            label="架构"
            allowDeselect={false}
            data={(entry?.archs ?? []).map((a) => ({
              value: a,
              label: label(archLabels, a),
              disabled: (entry?.formats ?? []).every((f) => taken.has(`${a}|${f}`)),
            }))}
            value={arch}
            onChange={pickArch}
            error={fieldError('arch')}
          />
          <Select
            label="格式"
            allowDeselect={false}
            data={(entry?.formats ?? []).map((f) => ({
              value: f,
              label: taken.has(`${arch}|${f}`)
                ? `${label(formatLabels, f)}（已添加）`
                : label(formatLabels, f),
              disabled: taken.has(`${arch}|${f}`),
            }))}
            value={format}
            onChange={(next) => {
              if (!next) return;
              setFormat(next);
              if (server?.field === 'format') setServer(null);
            }}
            error={fieldError('format') ?? (clash ? '该组合已存在，请直接编辑那一条' : undefined)}
          />
        </Group>
        <TextInput
          label="版本号"
          placeholder="1.2.3"
          value={version}
          onChange={edit('app_version', setVersion)}
          error={fieldError('app_version')}
        />
        <TextInput
          label="下载地址"
          placeholder={
            store
              ? `https://${catalog.store_hosts[format]}/…`
              : `https://…/AsterLink-1.2.3${catalog.suffixes[format] ?? ''}`
          }
          description={
            store
              ? `只能填写 https://${catalog.store_hosts[format]} 的页面地址`
              : `https 地址，文件名以 ${catalog.suffixes[format] ?? ''} 结尾；不会显示在下载页上`
          }
          value={url}
          onChange={edit('url', setURL)}
          error={fieldError('url')}
        />
        {store ? null : (
          <>
            <TextInput
              label="SHA-256"
              description="64 位十六进制，大写、冒号和空格会自动去掉"
              value={sha}
              onChange={edit('sha256', setSha)}
              error={fieldError('sha256')}
              ff="monospace"
            />
            <TextInput
              label="文件大小（字节）"
              description={sizeHint}
              inputMode="numeric"
              value={size}
              onChange={edit('size_bytes', setSize)}
              error={fieldError('size_bytes')}
              data-testid="download-size"
            />
          </>
        )}
        {showCert ? (
          <TextInput
            label="签名证书 SHA-256"
            description="可不填；填写后在下载页显示，供用户核对"
            value={cert}
            onChange={edit('signing_cert_sha256', setCert)}
            error={fieldError('signing_cert_sha256')}
            ff="monospace"
          />
        ) : null}
        {showBundle ? (
          <TextInput
            label="Bundle ID"
            placeholder="com.example.app"
            value={bundle}
            onChange={edit('bundle_id', setBundle)}
            error={fieldError('bundle_id')}
          />
        ) : null}
        <Switch
          checked={published}
          onChange={(e) => setPublished(e.currentTarget.checked)}
          label="上架（在下载页显示）"
        />
        <ErrorBox text={error} />
        {conflict ? (
          <Alert color="orange" role="alert" data-testid="download-conflict">
            <Stack gap="xs">
              <Text size="sm">已被其他管理员修改，请刷新后重试。</Text>
              <Group>
                <Button size="xs" variant="light" onClick={onRefresh}>
                  刷新
                </Button>
              </Group>
            </Stack>
          </Alert>
        ) : null}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            取消
          </Button>
          <Button
            onClick={() => void submit()}
            loading={busy}
            disabled={attempted && (local !== null || clash)}
          >
            保存
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

function DeleteDialog({
  row,
  onClose,
  onDone,
}: {
  row: ClientDownload;
  onClose: () => void;
  onDone: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const submit = async () => {
    setBusy(true);
    try {
      await api(`/admin/console/downloads/${row.id}`, { method: 'DELETE', body: '{}' });
      toast.success('已删除', variantName(row));
      onDone();
      onClose();
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal opened onClose={onClose} title="删除下载条目" size="md">
      <Stack>
        <Group gap="xs">
          <Text fw={600}>{variantName(row)}</Text>
          <Badge variant="light" color="gray">
            {row.app_version}
          </Badge>
        </Group>
        <Text size="sm">
          删除后下载页不再提供这个文件，已发出的下载链接会回到下载页。操作记录保留在审计中。
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
