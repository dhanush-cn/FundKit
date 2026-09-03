// Engineered by Dhanush C N (github.com/dhanush-cn)
import { useCallback, useState } from 'react';

import { apiRequest, describeError, tokenStore, userStore } from '../lib/api';
import type { LoginPayload, RegisterPayload, SessionResponse, User } from '../types';

export interface UseAuth {
  token: string | null;
  user: User | null;
  loading: boolean;
  error: string | null;
  clearError: () => void;
  login: (payload: LoginPayload) => Promise<boolean>;
  register: (payload: RegisterPayload) => Promise<boolean>;
  logout: () => void;
}

/**
 * useAuth owns the session. Keeping it in one hook means no component touches
 * localStorage directly, and a 401 anywhere can clear the session through a
 * single path.
 *
 * Both credential calls funnel through `startSession` so that the token and the
 * cached profile can never drift apart: either both are written or neither is.
 */
export function useAuth(): UseAuth {
  const [token, setToken] = useState<string | null>(() => tokenStore.read());
  const [user, setUser] = useState<User | null>(() => userStore.read<User>());
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const startSession = useCallback(
    async (path: string, body: LoginPayload | RegisterPayload, fallbackMessage: string) => {
      setLoading(true);
      setError(null);
      try {
        const session = await apiRequest<SessionResponse>(path, { method: 'POST', body });
        tokenStore.write(session.token);
        userStore.write(session.user);
        setToken(session.token);
        setUser(session.user);
        return true;
      } catch (cause) {
        setError(describeError(cause, fallbackMessage));
        return false;
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  const login = useCallback(
    (payload: LoginPayload) => startSession('/auth/login', payload, 'Login failed'),
    [startSession],
  );

  const register = useCallback(
    (payload: RegisterPayload) => startSession('/auth/register', payload, 'Registration failed'),
    [startSession],
  );

  const logout = useCallback(() => {
    tokenStore.clear();
    userStore.clear();
    setToken(null);
    setUser(null);
    setError(null);
  }, []);

  const clearError = useCallback(() => setError(null), []);

  return { token, user, loading, error, clearError, login, register, logout };
}
