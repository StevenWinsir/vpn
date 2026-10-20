'use client';
import { useCallback, useEffect, useRef, useState } from 'react';
import { api, message } from './api';
import type { Meta } from './types';

// Public capabilities from GET /meta. While loading or after a failure, callers must treat
// registration as closed: the page never defaults to open (handoff_auth.md 7.3).
export function useMeta() {
  const [meta, setMeta] = useState<Meta | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const revision = useRef(0);
  const load = useCallback(async () => {
    const current = ++revision.current;
    setLoading(true);
    setError(null);
    try {
      const data = await api<Meta>('/meta', {}, false);
      if (current === revision.current) setMeta(data);
    } catch (err) {
      if (current === revision.current) setError(message(err));
    } finally {
      if (current === revision.current) setLoading(false);
    }
  }, []);
  useEffect(() => {
    const revisions = revision;
    // Mount-time fetch; state updates happen after the request resolves.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load();
    return () => {
      revisions.current++;
    };
  }, [load]);
  return { meta, error, loading, reload: load };
}
