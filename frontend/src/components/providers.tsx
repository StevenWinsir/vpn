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
import {
  MantineProvider,
  createTheme,
  defaultVariantColorsResolver,
  localStorageColorSchemeManager,
  type CSSVariablesResolver,
  type VariantColorsResolver,
} from '@mantine/core';
import { Notifications } from '@mantine/notifications';
import { api, APIError, message, onSessionEnded } from '@/lib/api';
import { colorSchemeKey } from '@/lib/theme';
import type { Impersonation, User } from '@/lib/types';

type Auth = {
  user: User | null;
  // Non-null while an administrator is impersonating this user (read-only session).
  impersonation: Impersonation | null;
  loading: boolean;
  error: string | null;
  setUser: (user: User | null) => void;
  reload: () => Promise<void>;
  logout: () => Promise<void>;
};
const AuthContext = createContext<Auth | null>(null);
// Light and dark (handoff_ui.md 6): the scheme follows the system until the user picks one, which
// is then kept under colorSchemeKey, the key the head script in the root layout reads too.
const colorSchemeManager = localStorageColorSchemeManager({ key: colorSchemeKey });
// Mantine picks the text colour of filled buttons from the light shade only; the primary
// contrast variable follows the scheme (white on teal 7 in light, dark on teal 5 in dark).
const variantColorResolver: VariantColorsResolver = (input) => {
  const colors = defaultVariantColorsResolver(input);
  const primary = !input.color || input.color === input.theme.primaryColor;
  return input.variant === 'filled' && primary
    ? { ...colors, color: 'var(--mantine-primary-color-contrast)' }
    : colors;
};
const theme = createTheme({
  primaryColor: 'teal',
  // Filled teal: shade 7 with white text in light (5.2:1); shade 5 with dark text in dark (5.8:1).
  primaryShade: { light: 7, dark: 5 },
  autoContrast: true,
  variantColorResolver,
  black: '#172d2b',
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
    // Mantine's dark scale, cool and slightly brand-tinted: 0 text, 2 dimmed, 4 borders,
    // 6 inputs, 7 cards and body, 8 the page behind them (globals.css --al-bg).
    dark: [
      '#dbe5e2',
      '#b4c3bf',
      '#93a5a0',
      '#6d807b',
      '#3d4d49',
      '#2e3c39',
      '#24302d',
      '#19221f',
      '#121a18',
      '#0c1211',
    ],
  },
  components: { Button: { defaultProps: { fw: 600 } } },
});
// Mantine's light "dimmed" grey is 3.3:1 on white; this one reaches 4.5:1 on the page background.
const cssVariablesResolver: CSSVariablesResolver = () => ({
  variables: {},
  light: { '--mantine-color-dimmed': '#66756f' },
  dark: {},
});
export function Providers({ children }: { children: React.ReactNode }) {
  const [user, setStoredUser] = useState<User | null>(null);
  const [impersonation, setImpersonation] = useState<Impersonation | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const generation = useRef(0);
  const setUser = useCallback((next: User | null) => {
    generation.current++;
    setStoredUser(next);
    setImpersonation(null);
    setError(null);
    setLoading(false);
  }, []);
  const reload = useCallback(async () => {
    const revision = ++generation.current;
    try {
      const data = await api<{ user: User; impersonation?: Impersonation }>('/auth/me');
      if (revision === generation.current) {
        setStoredUser(data.user);
        setImpersonation(data.impersonation ?? null);
        setError(null);
      }
    } catch (err) {
      if (revision === generation.current) {
        if (err instanceof APIError && err.status === 401) {
          setStoredUser(null);
          setImpersonation(null);
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
    void api<{ user: User; impersonation?: Impersonation }>('/auth/me')
      .then((data) => {
        if (active && revision === generation.current) {
          setStoredUser(data.user);
          setImpersonation(data.impersonation ?? null);
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
  // A final 401 anywhere ends the signed-in state at once. setUser bumps the generation, so a
  // late /auth/me or business response cannot bring the old account back; RequireAuth then
  // unmounts the protected views (and their dialogs and editors) and redirects to login.
  useEffect(() => onSessionEnded(() => setUser(null)), [setUser]);
  const logout = useCallback(async () => {
    await api('/auth/logout', { method: 'POST', body: '{}' }, false);
    setUser(null);
  }, [setUser]);
  const value = useMemo(
    () => ({ user, impersonation, loading, error, setUser, reload, logout }),
    [user, impersonation, loading, error, setUser, reload, logout],
  );
  return (
    <MantineProvider
      theme={theme}
      defaultColorScheme="auto"
      colorSchemeManager={colorSchemeManager}
      cssVariablesResolver={cssVariablesResolver}
    >
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
