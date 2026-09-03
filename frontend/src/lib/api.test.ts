// Engineered by Dhanush C N (github.com/dhanush-cn)
import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError, API_BASE_URL, apiRequest, tokenStore, userStore } from './api';

/** jsonResponse builds the kind of Response the gateway actually returns. */
function jsonResponse(body: unknown, init: { status?: number; requestId?: string } = {}): Response {
  const headers = new Headers({ 'Content-Type': 'application/json' });
  if (init.requestId) {
    headers.set('x-request-id', init.requestId);
  }
  return new Response(body === undefined ? '' : JSON.stringify(body), {
    status: init.status ?? 200,
    headers,
  });
}

function stubFetch(implementation: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>) {
  const spy = vi.fn(implementation);
  vi.stubGlobal('fetch', spy);
  return spy;
}

afterEach(() => {
  tokenStore.clear();
  userStore.clear();
});

describe('apiRequest', () => {
  it('sends JSON to the configured gateway and returns the parsed body', async () => {
    const fetchSpy = stubFetch(async () => jsonResponse({ id: 'order-1' }));

    const result = await apiRequest<{ id: string }>('/orders', {
      method: 'POST',
      body: { fund_id: 'quant-small-cap-fund' },
    });

    expect(result).toEqual({ id: 'order-1' });

    const [url, init] = fetchSpy.mock.calls[0];
    expect(url).toBe(`${API_BASE_URL}/orders`);
    expect((init?.headers as Record<string, string>)['Content-Type']).toBe('application/json');
    expect(init?.body).toBe(JSON.stringify({ fund_id: 'quant-small-cap-fund' }));
  });

  it('attaches the stored bearer token when there is a session', async () => {
    tokenStore.write('a-signed-token');
    const fetchSpy = stubFetch(async () => jsonResponse([]));

    await apiRequest('/orders');

    const [, init] = fetchSpy.mock.calls[0];
    expect((init?.headers as Record<string, string>).Authorization).toBe('Bearer a-signed-token');
  });

  it('omits the Authorization header when signed out', async () => {
    const fetchSpy = stubFetch(async () => jsonResponse([]));

    await apiRequest('/orders');

    const [, init] = fetchSpy.mock.calls[0];
    expect((init?.headers as Record<string, string>).Authorization).toBeUndefined();
  });

  it('turns the gateway error envelope into an ApiError carrying the trace id', async () => {
    stubFetch(async () =>
      jsonResponse({ error: 'invalid username or password' }, { status: 401, requestId: 'req-9' }),
    );

    await expect(apiRequest('/auth/login', { method: 'POST', body: {} })).rejects.toMatchObject({
      name: 'ApiError',
      status: 401,
      requestId: 'req-9',
      message: 'invalid username or password',
    });
  });

  it('flags a 401 so the session can be cleared through one path', async () => {
    stubFetch(async () => jsonResponse({ error: 'invalid or expired token' }, { status: 401 }));

    const error = await apiRequest('/orders').catch((cause: unknown) => cause);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).isUnauthorized).toBe(true);
  });

  it('reports an unreachable gateway rather than throwing a raw network error', async () => {
    stubFetch(async () => {
      throw new TypeError('Failed to fetch');
    });

    const error = await apiRequest('/orders').catch((cause: unknown) => cause);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(0);
    expect((error as ApiError).message).toMatch(/cannot reach/i);
  });

  it('re-throws an abort so a superseded request is not reported as a failure', async () => {
    stubFetch(async () => {
      throw new DOMException('The operation was aborted.', 'AbortError');
    });

    await expect(apiRequest('/orders')).rejects.toBeInstanceOf(DOMException);
  });

  it('survives a non-JSON error body', async () => {
    stubFetch(
      async () =>
        new Response('<html>502 Bad Gateway</html>', {
          status: 502,
          headers: { 'Content-Type': 'text/html' },
        }),
    );

    const error = await apiRequest('/orders').catch((cause: unknown) => cause);
    expect((error as ApiError).status).toBe(502);
    expect((error as ApiError).message).toMatch(/502/);
  });

  it('treats an empty 200 body as null instead of crashing', async () => {
    stubFetch(async () => new Response('', { status: 200 }));

    await expect(apiRequest('/orders')).resolves.toBeNull();
  });
});

describe('tokenStore and userStore', () => {
  it('round-trips a session and clears it completely', () => {
    tokenStore.write('token-1');
    userStore.write({ id: 'user-1', username: 'dhanush' });

    expect(tokenStore.read()).toBe('token-1');
    expect(userStore.read<{ username: string }>()?.username).toBe('dhanush');

    tokenStore.clear();
    userStore.clear();

    expect(tokenStore.read()).toBeNull();
    expect(userStore.read()).toBeNull();
  });

  it('degrades gracefully when storage is unavailable', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('storage disabled in private browsing');
    });

    // Private browsing must not take the whole dashboard down.
    expect(() => tokenStore.read()).not.toThrow();
    expect(tokenStore.read()).toBeNull();
  });

  it('returns null for a corrupted cached profile', () => {
    localStorage.setItem('fundkit_user', 'not-json');
    expect(userStore.read()).toBeNull();
  });
});
