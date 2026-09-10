// Engineered by Dhanush C N (github.com/dhanush-cn)
import { formatDate } from '../../lib/format';
import type { OrderStatus } from '../../types';

interface LifecycleTimelineProps {
  status: OrderStatus;
  createdAt: string;
  updatedAt: string;
}

type StepState = 'done' | 'current' | 'todo' | 'failed';

interface Step {
  key: string;
  title: string;
  detail: string;
  state: StepState;
  at?: string;
}

/**
 * The order lifecycle, annotated with the infrastructure step behind each
 * transition.
 *
 * The states themselves are the four the order-service domain defines; the
 * captions name what actually moves — the Redis idempotency claim, the
 * Postgres write, the Kafka publish the portfolio and notification consumers
 * read. That mapping is the thing a reader of this dashboard most often wants
 * and the thing the old flat status column could not show.
 */
function buildSteps(status: OrderStatus, createdAt: string, updatedAt: string): Step[] {
  const reachedProcessing = status !== 'PENDING';
  const terminal = status === 'EXECUTED' || status === 'FAILED';

  return [
    {
      key: 'accepted',
      title: 'Accepted',
      detail:
        'Gateway verified the token, order-service claimed the idempotency key in Redis and wrote the row to Postgres.',
      state: 'done',
      at: createdAt,
    },
    {
      key: 'published',
      title: 'Published',
      detail:
        'order.created was produced to Kafka. portfolio-service and notification-service consume it independently.',
      state: reachedProcessing ? 'done' : 'current',
      at: reachedProcessing ? updatedAt : undefined,
    },
    {
      key: 'processing',
      title: 'Processing',
      detail: 'Units are being allotted against the fund NAV.',
      state: status === 'PROCESSING' ? 'current' : reachedProcessing ? 'done' : 'todo',
    },
    {
      key: 'terminal',
      title: status === 'FAILED' ? 'Failed' : 'Executed',
      detail:
        status === 'FAILED'
          ? 'A terminal failure. The event is on the dead-letter topic for replay rather than dropped.'
          : 'Holdings updated and the confirmation notification dispatched.',
      state: status === 'FAILED' ? 'failed' : terminal ? 'done' : 'todo',
      at: terminal ? updatedAt : undefined,
    },
  ];
}

export function LifecycleTimeline({ status, createdAt, updatedAt }: LifecycleTimelineProps) {
  const steps = buildSteps(status, createdAt, updatedAt);

  return (
    <ol className="timeline">
      {steps.map((step) => (
        <li key={step.key} className={`timeline-step timeline-step-${step.state}`}>
          <span className="timeline-marker" aria-hidden="true" />
          <div className="timeline-content">
            <p className="timeline-title">{step.title}</p>
            <p className="timeline-detail">{step.detail}</p>
            {step.at ? <p className="timeline-at">{formatDate(step.at)}</p> : null}
          </div>
        </li>
      ))}
    </ol>
  );
}
