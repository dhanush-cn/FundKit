// Engineered by Dhanush C N (github.com/dhanush-cn)
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  THEME_ATTRIBUTE,
  THEME_STORAGE_KEY,
  applyTheme,
  isTheme,
  otherTheme,
  readStoredTheme,
  resolveTheme,
  systemTheme,
  writeStoredTheme,
} from './theme';

function stubPrefersLight(matches: boolean) {
  vi.stubGlobal(
    'matchMedia',
    vi.fn((query: string) => ({
      matches: query.includes('light') ? matches : !matches,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })),
  );
}

beforeEach(() => {
  window.localStorage.clear();
  document.documentElement.removeAttribute(THEME_ATTRIBUTE);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('isTheme', () => {
  it('accepts only the two themes that exist', () => {
    expect(isTheme('light')).toBe(true);
    expect(isTheme('dark')).toBe(true);
    expect(isTheme('solarized')).toBe(false);
    expect(isTheme(null)).toBe(false);
  });
});

describe('readStoredTheme', () => {
  it('returns null when nothing has been stored', () => {
    expect(readStoredTheme()).toBeNull();
  });

  // A stale or hand-edited value must not reach the attribute, or the app
  // would resolve to a token set that does not exist and render unstyled.
  it('discards a stored value that is not a theme', () => {
    window.localStorage.setItem(THEME_STORAGE_KEY, 'solarized');
    expect(readStoredTheme()).toBeNull();
  });

  it('round-trips a written theme', () => {
    writeStoredTheme('light');
    expect(readStoredTheme()).toBe('light');
  });
});

describe('systemTheme', () => {
  it('follows the operating system when it asks for light', () => {
    stubPrefersLight(true);
    expect(systemTheme()).toBe('light');
  });

  it('defaults to dark when the operating system has no preference', () => {
    stubPrefersLight(false);
    expect(systemTheme()).toBe('dark');
  });
});

describe('resolveTheme', () => {
  // The whole point of the precedence rule: pressing the toggle has to survive
  // the operating system disagreeing with it.
  it('prefers an explicit choice over the operating system', () => {
    stubPrefersLight(true);
    writeStoredTheme('dark');
    expect(resolveTheme()).toBe('dark');
  });

  it('falls back to the operating system when no choice has been made', () => {
    stubPrefersLight(true);
    expect(resolveTheme()).toBe('light');
  });
});

describe('applyTheme', () => {
  it('writes the theme onto the document element', () => {
    applyTheme('light');
    expect(document.documentElement.getAttribute(THEME_ATTRIBUTE)).toBe('light');
    applyTheme('dark');
    expect(document.documentElement.getAttribute(THEME_ATTRIBUTE)).toBe('dark');
  });
});

describe('otherTheme', () => {
  it('is its own inverse', () => {
    expect(otherTheme('dark')).toBe('light');
    expect(otherTheme(otherTheme('dark'))).toBe('dark');
  });
});

// The inline boot script in index.html duplicates this module's fallback order
// so the page can pick a theme before the bundle loads. If either name drifts,
// the script silently stops finding the stored choice and every reload flashes.
describe('the boot script contract', () => {
  it('pins the storage key and attribute the inline script hardcodes', () => {
    expect(THEME_STORAGE_KEY).toBe('fundkit_theme');
    expect(THEME_ATTRIBUTE).toBe('data-theme');
  });
});
