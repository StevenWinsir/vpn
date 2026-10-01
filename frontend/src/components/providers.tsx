'use client';
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { MantineProvider, createTheme } from '@mantine/core';
import { Notifications } from '@mantine/notifications';
import { api, APIError, message } from '@/lib/api';
import type { User } from '@/lib/types';

type Auth = {
  user: User | null;
  loading: boolean;
  error: string | null;
  setUser: (user: User | null) => void;
  reload: () => Promise<void>;
  logout: () => Promise<void>;
};
const AuthContext = createContext<Auth | null>(null);
const theme = createTheme({
  primaryColor: 'teal',
  defaultRadius: 'md',
  fontFamily:
    'Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif',
  headings: { fontWeight: '650' },
  colors: {
    teal: [
      '#e8faf5',
      '#cdf3e6',
      '#9ee5cd',
      '#69d6b1',
      '#3dc89a',
      '#21b98a',
      '#129773',
      '#0d7b61',
      '#09624f',
      '#074d40',
    ],
  },
  components: { Button: { defaultProps: { fw: 600 } } },
});
export function Providers({ children }: { children: React.ReactNode }) {
  const [user, setStoredUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const generation = useRef(0);
  const setUser = useCallback((next: User | null) => {
    generation.current++;
    setStoredUser(next);
    setError(null);
    setLoading(false);
  }, []);
  const reload = useCallback(async () => {
    const revision = ++generation.current;
    try {
      const data = await api<{ user: User }>('/auth/me');
      if (revision === generation.current) {
        setStoredUser(data.user);
        setError(null);
      }
    } catch (err) {
      if (revision === generation.current) {
        if (err instanceof APIError && err.status === 401) {
          setStoredUser(null);
          setError(null);
        } else {
          setError(message(err));
        }
      }
    } finally {
      if (revision === generation.current) setLoading(false);
    }
  }, []);
  useEffect(() => {
    let active = true;
    const revision = ++generation.current;
    void api<{ user: User }>('/auth/me')
      .then((data) => {
        if (active && revision === generation.current) {
          setStoredUser(data.user);
          setError(null);
        }
      })
      .catch((err) => {
        if (active && revision === generation.current) {
          if (err instanceof APIError && err.status === 401) {
            setStoredUser(null);
            setError(null);
          } else {
            setError(message(err));
          }
        }
      })
      .finally(() => {
        if (active && revision === generation.current) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, []);
  const logout = useCallback(async () => {
    await api('/auth/logout', { method: 'POST', body: '{}' }, false);
    setUser(null);
  }, [setUser]);
  const value = useMemo(
    () => ({ user, loading, error, setUser, reload, logout }),
    [user, loading, error, setUser, reload, logout],
  );
  return (
    <MantineProvider theme={theme} defaultColorScheme="light">
      <Notifications position="top-right" />
      <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
    </MantineProvider>
  );
}
export function useAuth() {
  const value = useContext(AuthContext);
  if (!value) throw new Error('AuthProvider is required');
  return value;
}
