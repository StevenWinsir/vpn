'use client';
import { useEffect, useState } from 'react';
import { usePathname } from 'next/navigation';
import { Alert, Button, Group, Modal, Stack, Text } from '@mantine/core';
import { api, message } from '@/lib/api';
import { date } from '@/lib/types';
import { useFetched } from './console/console-common';
import { useAuth } from './providers';

type Announcement = { id: string; title: string; body: string; published_at: string | null };

// New announcements are looked for on every page change and every five minutes.
const recheckMs = 5 * 60 * 1000;

// Unacknowledged announcements, one dialog at a time, oldest first (handoff_payment_account.md
// 9.6). There is no close button; Escape and a click outside do nothing; the only way past is
// "知道了", which the server records. A reload shows the dialog again until it was recorded. An
// impersonation session gets none (the server returns an empty list and refuses the click).
export function AnnouncementGate() {
  const { user, impersonation } = useAuth();
  const path = usePathname();
  const [tick, setTick] = useState(0);
  const [done, setDone] = useState<string[]>([]);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const watching = !!user && !impersonation;
  useEffect(() => {
    if (!watching) return;
    const timer = setInterval(() => {
      if (document.visibilityState === 'visible') setTick((t) => t + 1);
    }, recheckMs);
    return () => clearInterval(timer);
  }, [watching]);
  // A failed check keeps what is already shown; the next page change or timer tries again.
  const { data } = useFetched<{ announcements: Announcement[] }>(
    watching ? '/me/announcements' : null,
    `${path}#${tick}`,
  );
  const queue = (data?.announcements ?? []).filter((a) => !done.includes(a.id));
  const current = watching ? queue[0] : undefined;
  const acknowledge = async () => {
    if (!current) return;
    setBusy(true);
    setError('');
    try {
      await api(`/me/announcements/${current.id}/ack`, { method: 'POST', body: '{}' });
      setDone((ids) => [...ids, current.id]);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      opened={!!current}
      onClose={() => undefined}
      withCloseButton={false}
      closeOnEscape={false}
      closeOnClickOutside={false}
      trapFocus
      centered
      size="lg"
      title={current?.title}
      styles={{ title: { fontWeight: 700, fontSize: 18 } }}
    >
      {current ? (
        <Stack data-testid="announcement" gap="md">
          {current.published_at ? (
            <Text size="xs" c="dimmed">
              {date(current.published_at)}
              {queue.length > 1 ? ` · 还有 ${queue.length - 1} 条待阅读` : ''}
            </Text>
          ) : null}
          {/* Plain text only: React escapes it, and line breaks are kept by CSS. */}
          <Text
            data-testid="announcement-body"
            style={{
              whiteSpace: 'pre-wrap',
              overflowWrap: 'anywhere',
              maxHeight: '55vh',
              overflowY: 'auto',
            }}
          >
            {current.body}
          </Text>
          {error ? (
            <Alert color="red" role="alert">
              {error}
            </Alert>
          ) : null}
          <Group justify="flex-end">
            <Button onClick={() => void acknowledge()} loading={busy}>
              知道了
            </Button>
          </Group>
        </Stack>
      ) : null}
    </Modal>
  );
}
