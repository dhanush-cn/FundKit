// Engineered by Dhanush C N (github.com/dhanush-cn)
import { Moon, Sun } from 'lucide-react';

import { useTheme } from '../../hooks/useTheme';

/**
 * One button, not a two-option segmented control: there are exactly two
 * themes, so a control that shows the state you are in and switches on click
 * costs half the width and reads faster in a crowded topbar.
 *
 * The icon shows the theme you would get, which is the convention every OS
 * settled on, and the accessible name says so out loud because the icon alone
 * is ambiguous the first time you meet it.
 */
export function ThemeToggle({ className = 'icon-button' }: { className?: string }) {
  const { theme, toggleTheme } = useTheme();
  const next = theme === 'dark' ? 'light' : 'dark';

  return (
    <button
      type="button"
      className={className}
      onClick={toggleTheme}
      aria-label={`Switch to ${next} mode`}
      title={`Switch to ${next} mode`}
    >
      {theme === 'dark' ? <Sun size={16} /> : <Moon size={16} />}
    </button>
  );
}
