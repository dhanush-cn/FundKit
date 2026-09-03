// Typed fetch wrapper for the FundKit API gateway.
//
// Every network call in the dashboard goes through this module so that
// authentication, correlation ids and error shape are handled once instead of
// being re-implemented at each call site.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)

export const API_BASE_URL = (
  import.meta.env.VITE_API_BASE_URL ?? 'http://localhost:8080'
).replace(/\/$/, '');

const TOKEN_STORAGE_KEY = 'fundkit_jwt';
const USER_STORAGE_KEY = 'fundkit_user';
const REQUEST_ID_HEADER = 'x-request-id';

/**
 * ApiError preserves the HTTP status and the gateway's correlation id, so a
 * failure in the UI can be traced straight to the matching backend log line.
 */
export class ApiError extends Error {
  readonly status: number;
  readonly requestId: string | null;

  constructor(message: string, status: number, requestId: string | null) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.requestId = requestId;
  }

  /** True when the session is missing or expired and the user must log in again. */
  get isUnauthorized(): boolean {
    return this.status === 401;
  }
}

export const tokenStore = {
  read(): string | null {
    try {
      return localStorage.getItem(TOKEN_STORAGE_KEY);
    } catch {
      return null;
    }
  },
  write(token: string): void {
    try {
      localStorage.setItem(TOKEN_STORAGE_KEY, token);
    } catch {
      /* storage can be unavailable in private browsing; the session still works */
    }
  },
  clear(): void {
    try {
      localStorage.removeItem(TOKEN_STORAGE_KEY);
    } catch {
      /* nothing to clean up */
    }
  },
};

/**
 * userStore caches the profile alongside the token so a page reload restores the
 * signed-in name without a round trip. It is a convenience cache only — the
 * token remains the sole thing the server trusts.
 */
export const userStore = {
  read<T>(): T | null {
    try {
      const raw = localStorage.getItem(USER_STORAGE_KEY);
      return raw ? (JSON.parse(raw) as T) : null;
    } catch {
      return null;
    }
  },
  write(user: unknown): void {
    try {
      localStorage.setItem(USER_STORAGE_KEY, JSON.stringify(user));
    } catch {
      /* storage can be unavailable in private browsing; the session still works */
    }
  },
  clear(): void {
    try {
      localStorage.removeItem(USER_STORAGE_KEY);
    } catch {
      /* nothing to clean up */
    }
  },
};

interface RequestOptions extends Omit<RequestInit, 'body'> {
  body?: unknown;
  signal?: AbortSignal;
}

/**
 * apiRequest performs one authenticated call and normalises every failure mode
 * — network error, non-JSON body, error envelope — into an ApiError.
 */
export async function apiRequest<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { body, headers, ...rest } = options;

  const requestHeaders: Record<string, string> = {
    Accept: 'application/json',
    ...(headers as Record<string, string> | undefined),
  };

  if (body !== undefined) {
    requestHeaders['Content-Type'] = 'application/json';
  }

  const token = tokenStore.read();
  if (token) {
    requestHeaders.Authorization = `Bearer ${token}`;
  }

  let response: Response;
  try {
    response = await fetch(`${API_BASE_URL}${path}`, {
      ...rest,
      headers: requestHeaders,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch (cause) {
    if (cause instanceof DOMException && cause.name === 'AbortError') {
      throw cause;
    }
    throw new ApiError('Cannot reach the FundKit API gateway', 0, null);
  }

  const requestId = response.headers.get(REQUEST_ID_HEADER);
  const payload = await readJson(response);

  if (!response.ok) {
    const message =
      (payload && typeof payload === 'object' && 'error' in payload
        ? String((payload as { error: unknown }).error)
        : null) ?? `Request failed with status ${response.status}`;
    throw new ApiError(message, response.status, requestId);
  }

  return payload as T;
}

async function readJson(response: Response): Promise<unknown> {
  const text = await response.text();
  if (!text) {
    return null;
  }
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

/** Turns any thrown value into a message safe to render. */
export function describeError(error: unknown, fallback: string): string {
  if (error instanceof ApiError) {
    return error.requestId ? `${error.message} (trace ${error.requestId})` : error.message;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return fallback;
}
