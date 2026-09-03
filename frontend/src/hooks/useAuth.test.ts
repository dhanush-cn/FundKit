// Engineered by Dhanush C N (github.com/dhanush-cn)
import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { tokenStore, userStore } from '../lib/api';
import type { User } from '../types';
import { useAuth } from './useAuth';

const user: User = {
  id: 'user-1',
  username: 'dhanush',
  email: 'dhanush@example.com',
  phone: '+919876543210',
  full_name: 'Dhanush C N',
};

/** stubFetch replaces window.fetch with a spy typed like fetch itself, so the
 *  recorded call arguments stay inspectable. */
function stubFetch(response: () => Promise<Response>) {
  const spy = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(() =>
    response(),
  );
  vi.stubGlobal('fetch', spy);
  return spy;
}

function sessionResponse(status = 200): Response {
  return new Response(
    JSON.stringify({
      token: 'signed-token',
      user_id: user.id,
      expires_at: '2026-09-03T10:00:00Z',
      user,
    }),
    { status, headers: { 'Content-Type': 'application/json' } },
  );
}

describe('useAuth', () => {
  it('starts signed out when nothing is stored', () => {
    const { result } = renderHook(() => useAuth());

    expect(result.current.token).toBeNull();
    expect(result.current.user).toBeNull();
  });

  it('restores a session from storage so a reload does not sign the user out', () => {
    tokenStore.write('stored-token');
    userStore.write(user);

    const { result } = renderHook(() => useAuth());

    expect(result.current.token).toBe('stored-token');
    expect(result.current.user?.username).toBe('dhanush');
  });

  it('logs in with a username and password and persists the session', async () => {
    const fetchSpy = stubFetch(async () => sessionResponse());
    const { result } = renderHook(() => useAuth());

    await act(async () => {
      await result.current.login({ username: 'dhanush', password: 'correct-horse-battery' });
    });

    const [url, init] = fetchSpy.mock.calls[0];
    expect(String(url)).toMatch(/\/auth\/login$/);
    expect(init?.method).toBe('POST');
    expect(init?.body).toBe(
      JSON.stringify({ username: 'dhanush', password: 'correct-horse-battery' }),
    );

    expect(result.current.token).toBe('signed-token');
    expect(result.current.user?.email).toBe('dhanush@example.com');
    // Both halves of the session are written, or neither is.
    expect(tokenStore.read()).toBe('signed-token');
    expect(userStore.read<User>()?.id).toBe('user-1');
  });

  it('registers and signs the new user in without a second round trip', async () => {
    const fetchSpy = stubFetch(async () => sessionResponse(201));
    const { result } = renderHook(() => useAuth());

    await act(async () => {
      await result.current.register({
        username: 'dhanush',
        email: 'dhanush@example.com',
        phone: '+919876543210',
        full_name: 'Dhanush C N',
        password: 'correct-horse-battery',
      });
    });

    expect(String(fetchSpy.mock.calls[0][0])).toMatch(/\/auth\/register$/);
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect(result.current.token).toBe('signed-token');
  });

  it('surfaces the gateway message on a failed login and stores nothing', async () => {
    stubFetch(
      async () =>
        new Response(JSON.stringify({ error: 'invalid username or password' }), {
          status: 401,
          headers: { 'Content-Type': 'application/json' },
        }),
    );

    const { result } = renderHook(() => useAuth());

    await act(async () => {
      await result.current.login({ username: 'dhanush', password: 'wrong' });
    });

    await waitFor(() => expect(result.current.error).toBe('invalid username or password'));
    expect(result.current.token).toBeNull();
    expect(tokenStore.read()).toBeNull();
    expect(userStore.read()).toBeNull();
  });

  it('reports a failed login through the return value as well as the error state', async () => {
    stubFetch(async () => new Response(JSON.stringify({ error: 'nope' }), { status: 401 }));

    const { result } = renderHook(() => useAuth());

    let outcome = true;
    await act(async () => {
      outcome = await result.current.login({ username: 'dhanush', password: 'wrong' });
    });

    expect(outcome).toBe(false);
  });

  it('clears the error when asked, so switching forms does not carry it over', async () => {
    stubFetch(async () => new Response(JSON.stringify({ error: 'nope' }), { status: 401 }));
    const { result } = renderHook(() => useAuth());

    await act(async () => {
      await result.current.login({ username: 'dhanush', password: 'wrong' });
    });
    expect(result.current.error).not.toBeNull();

    act(() => result.current.clearError());
    expect(result.current.error).toBeNull();
  });

  it('logout removes every trace of the session', async () => {
    stubFetch(async () => sessionResponse());
    const { result } = renderHook(() => useAuth());

    await act(async () => {
      await result.current.login({ username: 'dhanush', password: 'correct-horse-battery' });
    });

    act(() => result.current.logout());

    expect(result.current.token).toBeNull();
    expect(result.current.user).toBeNull();
    expect(tokenStore.read()).toBeNull();
    expect(userStore.read()).toBeNull();
  });
});
