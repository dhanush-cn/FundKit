// FundKit Control Center — hash routing.
// Engineered by Dhanush C N (github.com/dhanush-cn)
//
// The dashboard is served as a static bundle behind the API gateway, and the
// gateway has no catch-all rewrite: a hard refresh on /orders would reach Go
// and 404 rather than reaching index.html. Hash routing sidesteps that without
// a server change, which is the right trade for a control plane whose URLs are
// never bookmarked externally.
//
// It is built on useSyncExternalStore rather than an effect that calls
// setState on hashchange. The browser's hash is external mutable state that
// other tabs, the back button and window.location can all change; subscribing
// to it is exactly what that hook exists for, and it keeps the value correct
// during concurrent renders instead of one commit behind.

import { useSyncExternalStore } from 'react';

export const ROUTES = ['/', '/orders', '/portfolio', '/system'] as const;

export type RoutePath = (typeof ROUTES)[number];

export const ROUTE_TITLES: Record<RoutePath, string> = {
  '/': 'Overview',
  '/orders': 'Order desk',
  '/portfolio': 'Portfolio',
  '/system': 'System health',
};

function isRoutePath(value: string): value is RoutePath {
  return (ROUTES as readonly string[]).includes(value);
}

/** Reads the current route, falling back to the overview for anything unknown. */
function readRoute(): RoutePath {
  if (typeof window === 'undefined') {
    return '/';
  }
  const raw = window.location.hash.replace(/^#/, '');
  const path = raw === '' ? '/' : raw;
  return isRoutePath(path) ? path : '/';
}

function subscribe(onChange: () => void): () => void {
  window.addEventListener('hashchange', onChange);
  return () => window.removeEventListener('hashchange', onChange);
}

/** The server snapshot is only reached if this ever renders outside a browser. */
function readServerRoute(): RoutePath {
  return '/';
}

export function useRoute(): RoutePath {
  return useSyncExternalStore(subscribe, readRoute, readServerRoute);
}

/**
 * Navigates by writing the hash, which is what makes the browser back button
 * work for free — there is no history stack of our own to keep in sync.
 */
export function navigate(path: RoutePath): void {
  if (readRoute() === path) {
    return;
  }
  window.location.hash = path;
}
