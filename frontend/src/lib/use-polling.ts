'use client';

import { useCallback, useEffect, useRef, useState } from 'react';

/**
 * Visible-page polling with the same rules as the node list: refresh every `refreshMs` while
 * the page is visible, immediately on focus, return to the page and network recovery; one
 * request at a time (overlapping triggers are merged); everything is cancelled on unmount.
 */
export function usePolling(load: (signal: AbortSignal) => Promise<void>, refreshMs: number) {
  const [syncing, setSyncing] = useState(true);
  const lifetime = useRef<AbortController | null>(null);
  const inFlight = useRef<AbortController | null>(null);

  const run = useCallback(
    async (controller: AbortController) => {
      if (controller.signal.aborted || inFlight.current === controller) return;
      inFlight.current = controller;
      try {
        await load(controller.signal);
      } finally {
        if (inFlight.current === controller) inFlight.current = null;
        if (!controller.signal.aborted) setSyncing(false);
      }
    },
    [load],
  );
  // A later trigger shows the syncing state; the first load starts with it already set.
  const trigger = useCallback(
    (controller: AbortController) => {
      if (controller.signal.aborted || inFlight.current === controller) return;
      setSyncing(true);
      void run(controller);
    },
    [run],
  );

  useEffect(() => {
    const controller = new AbortController();
    lifetime.current = controller;
    const syncVisible = () => {
      if (document.visibilityState === 'visible') trigger(controller);
    };
    void run(controller);
    const timer = window.setInterval(syncVisible, refreshMs);
    window.addEventListener('focus', syncVisible);
    window.addEventListener('online', syncVisible);
    document.addEventListener('visibilitychange', syncVisible);
    return () => {
      controller.abort();
      window.clearInterval(timer);
      window.removeEventListener('focus', syncVisible);
      window.removeEventListener('online', syncVisible);
      document.removeEventListener('visibilitychange', syncVisible);
    };
  }, [run, trigger, refreshMs]);

  const refresh = useCallback(() => {
    if (lifetime.current) trigger(lifetime.current);
  }, [trigger]);

  return { syncing, refresh };
}
