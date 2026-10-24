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
  Table,
  Text,
  Textarea,
  TextInput,
} from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { api, APIError } from '@/lib/api';
import { describeError, ErrorBox, Pager, useFetched, When } from './console-common';

type Announcement = {
  id: string;
  title: string;
  body: string;
  status: 'draft' | 'published' | 'archived';
  published_at: string | null;
  version: number;
  created_at: string;
  read_count: number;
};
type AnnouncementList = { announcements: Announcement[]; total: number; audience: number };
type Reads = {
  reads: { user_id: string; email: string; acknowledged_at: string }[];
  total: number;
};

const statusLabel = {
  draft: { text: '草稿', color: 'gray' },
  published: { text: '已发布', color: 'teal' },
  archived: { text: '已下线', color: 'orange' },
} as const;

const titleLimit = 100;
const bodyLimit = 5000;
const pageSize = 20;

// The text exactly as a user's dialog shows it: plain, line breaks kept, nothing interpreted.
function Preview({ title, body }: { title: string; body: string }) {
  return (
    <Paper withBorder radius="md" p="md" data-testid="announcement-preview">
      <Text fw={700} mb="xs">
        {title}
      </Text>
      <Text style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{body}</Text>
    </Paper>
  );
}

type Dialog =
  | { kind: 'edit'; item: Announcement | null; seed?: { title: string; body: string } }
  | { kind: 'preview' | 'publish' | 'archive' | 'delete' | 'reads'; item: Announcement }
  | null;

export function ConsoleAnnouncements() {
  const [page, setPage] = useState(1);
  const [nonce, setNonce] = useState(0);
  const [dialog, setDialog] = useState<Dialog>(null);
  const { data, error, loading } = useFetched<AnnouncementList>(
    `/admin/console/announcements?page=${page}`,
    nonce,
  );
  const refresh = () => setNonce((n) => n + 1);
  const close = () => setDialog(null);
  return (
    <Stack gap="lg">
      <Group justify="space-between">
        <Text size="sm" c="dimmed">
          发布后所有用户登录后会弹窗显示，必须点击“知道了”才能关闭；多条公告按发布时间先后依次显示。模拟登录时不显示。
        </Text>
        <Button onClick={() => setDialog({ kind: 'edit', item: null })}>新建公告</Button>
      </Group>
      <ErrorBox text={error} />
      <Paper withBorder radius="lg" p="md">
        {loading && !data ? (
          <Skeleton height={200} />
        ) : (
          <>
            <ScrollArea>
              <Table verticalSpacing="sm" horizontalSpacing="md" miw={900} aria-label="公告列表">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>标题</Table.Th>
                    <Table.Th>状态</Table.Th>
                    <Table.Th>发布时间</Table.Th>
                    <Table.Th>已确认人数</Table.Th>
                    <Table.Th />
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {(data?.announcements ?? []).map((a) => (
                    <Table.Tr key={a.id} data-testid="announcement-row">
                      <Table.Td>
                        <Text size="sm" fw={600}>
                          {a.title}
                        </Text>
                      </Table.Td>
                      <Table.Td>
                        <Badge color={statusLabel[a.status].color} variant="light">
                          {statusLabel[a.status].text}
                        </Badge>
                      </Table.Td>
                      <Table.Td>
                        <When value={a.published_at} />
                      </Table.Td>
                      <Table.Td data-testid="read-count">
                        {a.status === 'draft' ? '—' : `${a.read_count} / ${data?.audience ?? 0}`}
                      </Table.Td>
                      <Table.Td>
                        <Group gap={6} wrap="nowrap">
                          {a.status === 'draft' ? (
                            <>
                              <Button
                                size="xs"
                                variant="light"
                                onClick={() => setDialog({ kind: 'edit', item: a })}
                                aria-label={`编辑 ${a.title}`}
                              >
                                编辑
                              </Button>
                              <Button
                                size="xs"
                                variant="light"
                                onClick={() => setDialog({ kind: 'publish', item: a })}
                                aria-label={`发布 ${a.title}`}
                              >
                                发布
                              </Button>
                              <Button
                                size="xs"
                                variant="light"
                                color="red"
                                onClick={() => setDialog({ kind: 'delete', item: a })}
                                aria-label={`删除 ${a.title}`}
                              >
                                删除
                              </Button>
                            </>
                          ) : (
                            <>
                              {a.status === 'published' ? (
                                <Button
                                  size="xs"
                                  variant="light"
                                  color="orange"
                                  onClick={() => setDialog({ kind: 'archive', item: a })}
                                  aria-label={`下线 ${a.title}`}
                                >
                                  下线
                                </Button>
                              ) : null}
                              <Button
                                size="xs"
                                variant="light"
                                onClick={() =>
                                  setDialog({
                                    kind: 'edit',
                                    item: null,
                                    seed: { title: a.title, body: a.body },
                                  })
                                }
                                aria-label={`复制 ${a.title}`}
                              >
                                复制为新草稿
                              </Button>
                              <Button
                                size="xs"
                                variant="light"
                                onClick={() => setDialog({ kind: 'reads', item: a })}
                                aria-label={`确认名单 ${a.title}`}
                              >
                                确认名单
                              </Button>
                            </>
                          )}
                          <Button
                            size="xs"
                            variant="subtle"
                            onClick={() => setDialog({ kind: 'preview', item: a })}
                            aria-label={`预览 ${a.title}`}
                          >
                            预览
                          </Button>
                        </Group>
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
              {data && data.announcements.length === 0 ? (
                <Text ta="center" c="dimmed" py="xl">
                  还没有公告
                </Text>
              ) : null}
            </ScrollArea>
            <Pager page={page} total={data?.total ?? 0} size={pageSize} onChange={setPage} />
          </>
        )}
      </Paper>
      {dialog?.kind === 'edit' ? (
        <Editor item={dialog.item} seed={dialog.seed} onClose={close} onSaved={refresh} />
      ) : null}
      {dialog?.kind === 'preview' ? (
        <Modal opened onClose={close} title="预览" size="lg">
          <Preview title={dialog.item.title} body={dialog.item.body} />
        </Modal>
      ) : null}
      {dialog?.kind === 'publish' ? (
        <Confirm
          title="发布公告"
          action={`/admin/console/announcements/${dialog.item.id}/publish`}
          confirm="确认发布"
          onClose={close}
          onDone={refresh}
          done="公告已发布"
        >
          <Text size="sm">
            发布后内容将被锁定，所有用户登录后都会看到并必须点击“知道了”。以后只能下线或复制为新草稿。
          </Text>
          <Preview title={dialog.item.title} body={dialog.item.body} />
        </Confirm>
      ) : null}
      {dialog?.kind === 'archive' ? (
        <Confirm
          title="下线公告"
          action={`/admin/console/announcements/${dialog.item.id}/archive`}
          confirm="确认下线"
          color="orange"
          onClose={close}
          onDone={refresh}
          done="公告已下线"
        >
          <Text size="sm">下线后不再向尚未确认的用户推送，已有的确认记录保留。</Text>
        </Confirm>
      ) : null}
      {dialog?.kind === 'delete' ? (
        <Confirm
          title="删除草稿"
          action={`/admin/console/announcements/${dialog.item.id}`}
          method="DELETE"
          confirm="确认删除"
          color="red"
          onClose={close}
          onDone={refresh}
          done="草稿已删除"
        >
          <Text size="sm">只有草稿可以删除。</Text>
        </Confirm>
      ) : null}
      {dialog?.kind === 'reads' ? <ReadsList item={dialog.item} onClose={close} /> : null}
    </Stack>
  );
}

function Editor({
  item,
  seed,
  onClose,
  onSaved,
}: {
  item: Announcement | null;
  seed?: { title: string; body: string };
  onClose: () => void;
  onSaved: () => void;
}) {
  const [title, setTitle] = useState(item?.title ?? seed?.title ?? '');
  const [body, setBody] = useState(item?.body ?? seed?.body ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [conflict, setConflict] = useState(false);
  const titleLength = Array.from(title.trim()).length;
  const bodyLength = Array.from(body.trim()).length;
  const valid =
    titleLength >= 1 && titleLength <= titleLimit && bodyLength >= 1 && bodyLength <= bodyLimit;
  const submit = async () => {
    setBusy(true);
    setError('');
    try {
      await api(item ? `/admin/console/announcements/${item.id}` : '/admin/console/announcements', {
        method: 'POST',
        body: JSON.stringify(item ? { title, body, version: item.version } : { title, body }),
      });
      notifications.show({ color: 'teal', title: '草稿已保存', message: title });
      onSaved();
      onClose();
    } catch (e) {
      setError(describeError(e));
      setConflict(e instanceof APIError && e.code === 'version_conflict');
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      opened
      onClose={onClose}
      title={item ? '编辑草稿' : '新建公告'}
      size="xl"
      closeOnClickOutside={false}
    >
      <Stack>
        <TextInput
          label="标题"
          value={title}
          onChange={(e) => setTitle(e.currentTarget.value)}
          description={`${titleLength} / ${titleLimit}`}
          error={titleLength > titleLimit ? '标题过长' : undefined}
        />
        <Textarea
          label="正文（纯文本，保留换行）"
          autosize
          minRows={6}
          maxRows={16}
          value={body}
          onChange={(e) => setBody(e.currentTarget.value)}
          description={`${bodyLength} / ${bodyLimit}`}
          error={bodyLength > bodyLimit ? '正文过长' : undefined}
        />
        {title.trim() && body.trim() ? (
          <div>
            <Text size="sm" fw={500} mb={4}>
              用户看到的效果
            </Text>
            <Preview title={title.trim()} body={body.trim()} />
          </div>
        ) : null}
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
          <Button onClick={() => void submit()} loading={busy} disabled={!valid}>
            保存草稿
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

function Confirm({
  title,
  action,
  method = 'POST',
  confirm,
  color,
  done,
  onClose,
  onDone,
  children,
}: {
  title: string;
  action: string;
  method?: 'POST' | 'DELETE';
  confirm: string;
  color?: string;
  done: string;
  onClose: () => void;
  onDone: () => void;
  children: React.ReactNode;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const submit = async () => {
    setBusy(true);
    try {
      await api(action, { method, body: '{}' });
      notifications.show({ color: 'teal', title: done, message: '' });
      onDone();
      onClose();
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal opened onClose={onClose} title={title} size="lg">
      <Stack>
        {children}
        <ErrorBox text={error} />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            取消
          </Button>
          <Button color={color} onClick={() => void submit()} loading={busy}>
            {confirm}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

function ReadsList({ item, onClose }: { item: Announcement; onClose: () => void }) {
  const [page, setPage] = useState(1);
  const { data, error } = useFetched<Reads>(
    `/admin/console/announcements/${item.id}/reads?page=${page}`,
  );
  return (
    <Modal opened onClose={onClose} title={`已确认的用户 · ${item.title}`} size="lg">
      <Stack>
        <ErrorBox text={error} />
        {data ? (
          data.reads.length === 0 ? (
            <Alert color="gray">还没有人确认</Alert>
          ) : (
            <Table verticalSpacing={6} aria-label="确认名单">
              <Table.Tbody>
                {data.reads.map((r) => (
                  <Table.Tr key={r.user_id}>
                    <Table.Td>{r.email}</Table.Td>
                    <Table.Td>
                      <When value={r.acknowledged_at} />
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          )
        ) : (
          <Skeleton height={120} />
        )}
        <Pager page={page} total={data?.total ?? 0} size={pageSize} onChange={setPage} />
      </Stack>
    </Modal>
  );
}
