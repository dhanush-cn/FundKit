// Engineered by Dhanush C N (github.com/dhanush-cn)
import type { OrderStatus } from '../../types';
import { statusTone } from '../../lib/format';

/**
 * The badge carries a dot as well as a colour. Status is the one thing on this
 * screen a user acts on, and colour alone would make PROCESSING and EXECUTED
 * indistinguishable to a red-green colourblind reader — the label does the
 * real work and the tone is reinforcement.
 */
export function StatusBadge({ status }: { status: OrderStatus }) {
  return (
    <span className={`badge badge-${statusTone(status)}`}>
      <span className="badge-dot" aria-hidden="true" />
      {status}
    </span>
  );
}
