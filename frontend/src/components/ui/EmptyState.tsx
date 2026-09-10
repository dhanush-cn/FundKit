// Engineered by Dhanush C N (github.com/dhanush-cn)
import type { ReactNode } from 'react';

interface EmptyStateProps {
  title: string;
  hint?: string;
  icon?: ReactNode;
}

/**
 * An empty region says why it is empty and what fills it. A blank panel is
 * indistinguishable from a failed fetch, and on a dashboard that polls, that
 * ambiguity is the difference between "nothing happened yet" and "the backend
 * is down".
 */
export function EmptyState({ title, hint, icon }: EmptyStateProps) {
  return (
    <div className="empty-state">
      {icon ? <div className="empty-state-icon">{icon}</div> : null}
      <p className="empty-state-title">{title}</p>
      {hint ? <p className="empty-state-hint">{hint}</p> : null}
    </div>
  );
}
