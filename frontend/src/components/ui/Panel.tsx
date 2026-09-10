// Engineered by Dhanush C N (github.com/dhanush-cn)
import type { ReactNode } from 'react';

interface PanelProps {
  id?: string;
  kicker?: string;
  title: string;
  action?: ReactNode;
  children: ReactNode;
  className?: string;
}

/**
 * Every boxed region on the dashboard is this component. Keeping one header
 * shape means the kicker/title/action rhythm cannot drift panel to panel,
 * which was the main reason the old single-page layout read as noisy.
 */
export function Panel({ id, kicker, title, action, children, className }: PanelProps) {
  return (
    <section id={id} className={['panel', className].filter(Boolean).join(' ')}>
      <header className="panel-header">
        <div>
          {kicker ? <p className="panel-kicker">{kicker}</p> : null}
          <h2 className="panel-title">{title}</h2>
        </div>
        {action ? <div className="panel-action">{action}</div> : null}
      </header>
      {children}
    </section>
  );
}
