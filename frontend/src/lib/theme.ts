// FundKit Control Center — theme resolution and persistence.
// Developed by Dhanush C N (github.com/dhanush-cn)

export type Theme = 'light' | 'dark';

export const THEME_STORAGE_KEY = 'fundkit_theme';
export const THEME_ATTRIBUTE = 'data-theme';

/**
 * The theme is a single attribute on <html>; every colour in the app resolves
 * from the custom properties that attribute selects. Keeping the mechanism in
 * one module means the boot script in index.html, the React hook and the tests
 * all agree on the storage key, the attribute name and the fallback order.
 *
 * The fallback order is deliberate: an explicit choice the user made outranks
 * the operating system, and the operating system outranks our own default. A
 * control plane for money movement is more often read in a dark room than a
 * bright one, so dark is what we default to when nothing else has an opinion.
 */
export function isTheme(value: unknown): value is Theme {
  return value === 'light' || value === 'dark';
}

/** The stored choice, or null when the user has never made one. */
export function readStoredTheme(): Theme | null {
  try {
    const stored = window.localStorage.getItem(THEME_STORAGE_KEY);
    return isTheme(stored) ? stored : null;
  } catch {
    // Safari in private mode throws on localStorage rather than returning null.
    return null;
  }
}

export function writeStoredTheme(theme: Theme): void {
  try {
    window.localStorage.setItem(THEME_STORAGE_KEY, theme);
  } catch {
    // A theme we cannot persist is still a theme we can apply for this tab.
  }
}

/**
 * The live query for the OS preference, or null where there is nothing to ask.
 *
 * `matchMedia` is universal in browsers but absent in jsdom and in a few
 * embedded webviews, and a control plane that throws on boot because it could
 * not determine a colour preference would be an absurd way to lose a session.
 * Every caller treats null as "no opinion".
 */
export function prefersLightQuery(): MediaQueryList | null {
  try {
    return typeof window.matchMedia === 'function'
      ? window.matchMedia('(prefers-color-scheme: light)')
      : null;
  } catch {
    return null;
  }
}

/** What the operating system asks for, defaulting to dark. */
export function systemTheme(): Theme {
  return prefersLightQuery()?.matches ? 'light' : 'dark';
}

export function resolveTheme(): Theme {
  return readStoredTheme() ?? systemTheme();
}

/**
 * Applying the theme is one attribute write. It is idempotent, so the boot
 * script and the first React render can both call it without a flash between
 * them.
 */
export function applyTheme(theme: Theme): void {
  document.documentElement.setAttribute(THEME_ATTRIBUTE, theme);
}

export function otherTheme(theme: Theme): Theme {
  return theme === 'dark' ? 'light' : 'dark';
}
