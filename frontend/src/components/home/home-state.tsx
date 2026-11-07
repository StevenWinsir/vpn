'use client';
import { createContext, useContext, useMemo } from 'react';
import { useAuth } from '../providers';
import { useMeta } from '@/lib/use-meta';
import type { Meta } from '@/lib/types';

// What the home page's actions offer: nothing yet (pending), sign-in or registration for a
// visitor, or the way into the user centre. One /meta request serves the header, the hero and the
// closing call to action. Registration counts as closed while /meta loads or fails (handoff_auth
// 7.3), but the visitor's actions wait for it, so "登录" never flashes and turns into "注册".
export type HomeAction =
  { kind: 'pending' } | { kind: 'visitor'; register: boolean } | { kind: 'member' };

type HomeState = { action: HomeAction; meta: Meta | null };
const HomeContext = createContext<HomeState>({ action: { kind: 'pending' }, meta: null });

export function HomeStateProvider({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth();
  const { meta, loading: metaLoading } = useMeta();
  const value = useMemo<HomeState>(() => {
    let action: HomeAction;
    if (loading) action = { kind: 'pending' };
    else if (user) action = { kind: 'member' };
    else if (metaLoading) action = { kind: 'pending' };
    else action = { kind: 'visitor', register: meta?.allow_registration === true };
    return { action, meta };
  }, [user, loading, meta, metaLoading]);
  return <HomeContext.Provider value={value}>{children}</HomeContext.Provider>;
}

export const useHomeState = () => useContext(HomeContext);
