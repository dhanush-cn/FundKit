// Engineered by Dhanush C N (github.com/dhanush-cn)
import { useCallback, useEffect, useState } from 'react';

import {
  applyTheme,
  otherTheme,
  prefersLightQuery,
  readStoredTheme,
  resolveTheme,
  systemTheme,
  writeStoredTheme,
  type Theme,
} from '../lib/theme';

export interface UseTheme {
  theme: Theme;
  setTheme: (theme: Theme) => void;
  toggleTheme: () => void;
}

/**
 * useTheme owns the light/dark choice for the session.
 *
 * The initial value is read synchronously rather than in an effect, because
 * the boot script in index.html has already stamped the attribute and an
 * effect-driven default would repaint the whole app one frame later.
 *
 * While the user has made no explicit choice we keep following the operating
 * system — if they flip their laptop to dark at sunset, the open tab follows.
 * The moment they press the toggle that subscription stops mattering, because
 * the stored value wins in `resolveTheme`.
 */
export function useTheme(): UseTheme {
  const [theme, setThemeState] = useState<Theme>(() => resolveTheme());

  useEffect(() => {
    applyTheme(theme);
  }, [theme]);

  useEffect(() => {
    if (readStoredTheme() !== null) {
      return;
    }
    const query = prefersLightQuery();
    if (!query) {
      return;
    }
    const onChange = () => {
      if (readStoredTheme() === null) {
        setThemeState(systemTheme());
      }
    };
    query.addEventListener('change', onChange);
    return () => query.removeEventListener('change', onChange);
  }, [theme]);

  const setTheme = useCallback((next: Theme) => {
    writeStoredTheme(next);
    setThemeState(next);
  }, []);

  const toggleTheme = useCallback(() => {
    setThemeState((current) => {
      const next = otherTheme(current);
      writeStoredTheme(next);
      return next;
    });
  }, []);

  return { theme, setTheme, toggleTheme };
}
