// Engineered by Dhanush C N (github.com/dhanush-cn)
import type { ReactNode } from 'react';

interface MetricCardProps {
  label: string;
  value: ReactNode;
  foot?: ReactNode;
  /** Applies the gain/loss pair. Left unset, the figure stays white. */
  tone?: 'neutral' | 'gain' | 'loss' | 'accent';
}

export function MetricCard({ label, value, foot, tone = 'neutral' }: MetricCardProps) {
  return (
    <article className="metric-card">
      <p className="metric-label">{label}</p>
      <p className={`metric-value metric-value-${tone}`}>{value}</p>
      {foot ? <p className="metric-foot">{foot}</p> : null}
    </article>
  );
}
