'use client';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { Alert, Group, Pagination, PasswordInput, Select, Stack, Text } from '@mantine/core';
import { api, APIError, message } from '@/lib/api';
import { date } from '@/lib/types';
import { utc } from '@/lib/console';

export type Values = Record<string, string>;

// One GET whose result is keyed by what was asked for, so a stale answer is never shown for a
// newer question and no state is set synchronously inside the effect.
export function useFetched<T>(url: string | null, nonce = 0) {
  const key = url === null ? null : `${url}#${nonce}`;
  const [state, setState] = useState<{ key: string | null; data: T | null; error: string }>({
    key: null,
    data: null,
    error: '',
  });
  useEffect(() => {
    if (key === null || url === null) return;
    const controller = new AbortController();
    api<T>(url, { signal: controller.signal })
      .then((data) => setState({ key, data, error: '' }))
      .catch((e) => {
        if (!controller.signal.aborted)
          setState((s) => ({ key, data: s.data, error: describeError(e) }));
      });
    return () => controller.abort();
  }, [key, url]);
  const current = state.key === key;
  return { data: state.data, error: current ? state.error : '', loading: key !== null && !current };
}

// A filtered, paged console list whose filters live in the URL (shareable, survives a reload).
// toApi turns the URL values into the API's query; it and keys must be module-level.
export function useConsoleList<T>(
  path: string,
  keys: readonly string[],
  toApi: (values: Values) => Values,
) {
  const params = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();
  const [nonce, setNonce] = useState(0);
  const serialized = params.toString();
  const values = useMemo<Values>(() => {
    const current = new URLSearchParams(serialized);
    return Object.fromEntries(keys.map((key) => [key, current.get(key) ?? '']));
  }, [serialized, keys]);
  const page = Math.max(1, Number(values.page) || 1);
  const query = useMemo(() => {
    const out = new URLSearchParams();
    for (const [key, value] of Object.entries(toApi(values))) if (value) out.set(key, value);
    out.set('page', String(page));
    return out.toString();
  }, [values, page, toApi]);
  const { data, error, loading } = useFetched<T>(`${path}?${query}`, nonce);
  const apply = useCallback(
    (patch: Values) => {
      const next = new URLSearchParams(serialized);
      for (const [key, value] of Object.entries(patch)) {
        if (value) next.set(key, value);
        else next.delete(key);
      }
      if (!('page' in patch)) next.delete('page');
      const text = next.toString();
      router.replace(text ? `${pathname}?${text}` : pathname);
    },
    [serialized, router, pathname],
  );
  const reload = useCallback(() => setNonce((n) => n + 1), []);
  return { values, page, data, error, loading, apply, reload };
}

// A moment in the browser's zone; hovering shows the UTC instant.
export function When({ value }: { value: string | null | undefined }) {
  if (!value)
    return (
      <Text span c="dimmed">
        —
      </Text>
    );
  return (
    <span title={utc(value)} data-utc={utc(value)}>
      {date(value)}
    </span>
  );
}

export function Pager({
  page,
  total,
  size,
  onChange,
}: {
  page: number;
  total: number;
  size: number;
  onChange: (page: number) => void;
}) {
  const pages = Math.max(1, Math.ceil(total / size));
  return (
    <Group justify="space-between" mt="md">
      <Text size="sm" c="dimmed">
        共 {total} 条
      </Text>
      {pages > 1 ? <Pagination value={page} total={pages} onChange={onChange} size="sm" /> : null}
    </Group>
  );
}

export function PageSizeSelect({
  value,
  onChange,
}: {
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <Select
      aria-label="每页条数"
      w={110}
      allowDeselect={false}
      data={[
        { value: '20', label: '每页 20' },
        { value: '50', label: '每页 50' },
      ]}
      value={value || '20'}
      onChange={(next) => onChange(next === '50' ? '50' : '')}
    />
  );
}

// The second factor of dangerous actions: the administrator's own password.
export function ReauthField({
  value,
  onChange,
  autoFocus,
}: {
  value: string;
  onChange: (value: string) => void;
  autoFocus?: boolean;
}) {
  return (
    <PasswordInput
      label="管理员密码"
      description="危险操作需要再次输入你自己的登录密码"
      autoComplete="current-password"
      data-autofocus={autoFocus ? true : undefined}
      value={value}
      onChange={(e) => onChange(e.currentTarget.value)}
    />
  );
}

export function describeError(e: unknown) {
  if (e instanceof APIError && e.status === 429 && e.retryAfter)
    return `${e.message}（约 ${Math.ceil(e.retryAfter / 60)} 分钟后可重试）`;
  return message(e);
}

export function ErrorBox({ text }: { text: string }) {
  if (!text) return null;
  return (
    <Alert color="red" role="alert" data-testid="console-error">
      <Stack gap={4}>{text}</Stack>
    </Alert>
  );
}
