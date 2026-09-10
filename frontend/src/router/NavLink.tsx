// Engineered by Dhanush C N (github.com/dhanush-cn)
import type { ReactNode } from 'react';

import { navigate, useRoute, type RoutePath } from './route';

interface NavLinkProps {
  to: RoutePath;
  icon?: ReactNode;
  children: ReactNode;
  className?: string;
}

/**
 * A real anchor, not a div with an onClick: middle-click, ctrl-click and
 * "copy link address" all keep working, and screen readers announce it as a
 * link. The click handler exists only to skip the default jump when the target
 * is already active, and aria-current carries the selected state so the gold
 * marker is not the only signal.
 */
export function NavLink({ to, icon, children, className }: NavLinkProps) {
  const current = useRoute();
  const active = current === to;

  return (
    <a
      href={`#${to}`}
      className={[className ?? 'nav-link', active ? 'is-active' : ''].filter(Boolean).join(' ')}
      aria-current={active ? 'page' : undefined}
      onClick={(event) => {
        if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) {
          return;
        }
        event.preventDefault();
        navigate(to);
      }}
    >
      {icon ? <span className="nav-link-icon">{icon}</span> : null}
      <span>{children}</span>
    </a>
  );
}
