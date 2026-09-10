// FundKit Control Center — overview page.
// Engineered by Dhanush C N (github.com/dhanush-cn)
import {
  ArrowUpRight,
  CheckCircle2,
  CircleDashed,
  Layers3,
  ListOrdered,
  TrendingDown,
  TrendingUp,
  Wallet,
  XCircle,
} from 'lucide-react';

import { EmptyState } from '../components/ui/EmptyState';
import { MetricCard } from '../components/ui/MetricCard';
import { Panel } from '../components/ui/Panel';
import { StatusBadge } from '../components/ui/StatusBadge';
import { formatCurrency, formatDate, formatPaise } from '../lib/format';
import { NavLink } from '../router/NavLink';
import { useDashboard } from '../state/dashboard-context';

const RECENT_LIMIT = 5;

/**
 * A read-only summary. Nothing here mutates anything — every action lives on
 * the page that owns it — so this screen is safe to leave open on a second
 * monitor, which is the only reason a dashboard overview earns its place.
 */
export function Overview() {
  const { orders, portfolio, health } = useDashboard();

  const recent = orders.orders.slice(0, RECENT_LIMIT);
  const active = orders.metrics.pending + orders.metrics.processing;
  const servicesUp = health.services.filter((service) => service.status === 'UP').length;
  const gain = portfolio.pnl?.total_unrealized_gain ?? 0;

  return (
    <div className="page">
      <section className="headline">
        <div className="headline-copy">
          <h2 className="headline-title">Mutual fund orders, backed by real services.</h2>
          <p className="headline-body">
            Every order on this screen travelled the gateway, claimed an idempotency key in Redis,
            landed in Postgres and was published to Kafka before the portfolio and notification
            consumers ever saw it.
          </p>
        </div>

        <div className="headline-figure">
          <p className="headline-figure-label">
            <Wallet size={14} /> Capital routed
          </p>
          <p className="headline-figure-value">{formatPaise(orders.metrics.totalInvested)}</p>
          <p className="headline-figure-foot">
            across {orders.orders.length} {orders.orders.length === 1 ? 'order' : 'orders'}
          </p>
        </div>
      </section>

      <section className="metric-row" aria-label="Order counters">
        <MetricCard
          label="Executed"
          value={orders.metrics.executed}
          tone="gain"
          foot={
            <>
              <CheckCircle2 size={13} /> Reached a terminal success
            </>
          }
        />
        <MetricCard
          label="Active"
          value={active}
          tone="accent"
          foot={
            <>
              <CircleDashed size={13} /> Pending or processing
            </>
          }
        />
        <MetricCard
          label="Failed"
          value={orders.metrics.failed}
          tone={orders.metrics.failed > 0 ? 'loss' : 'neutral'}
          foot={
            <>
              <XCircle size={13} /> Rejected or failed lifecycle
            </>
          }
        />
        <MetricCard
          label="Services"
          value={`${servicesUp}/${health.services.length || 4}`}
          foot={
            <>
              <Layers3 size={13} /> Gateway, order, portfolio, notification
            </>
          }
        />
      </section>

      <div className="split-grid">
        <Panel
          kicker="Order book"
          title="Recent orders"
          action={
            <NavLink to="/orders" className="button button-ghost">
              Open order desk <ArrowUpRight size={14} />
            </NavLink>
          }
        >
          {recent.length === 0 ? (
            <EmptyState
              icon={<ListOrdered size={20} />}
              title="No orders yet"
              hint="Place the first one from the order desk and it will appear here within a poll."
            />
          ) : (
            <ul className="recent-list">
              {recent.map((order) => (
                <li key={order.id} className="recent-item">
                  <div className="recent-main">
                    <p className="recent-fund">{order.fund_id}</p>
                    <p className="recent-meta">
                      {order.type} · {formatDate(order.created_at)}
                    </p>
                  </div>
                  <div className="recent-side">
                    <span className="recent-amount">{formatPaise(order.amount)}</span>
                    <StatusBadge status={order.status} />
                  </div>
                </li>
              ))}
            </ul>
          )}
        </Panel>

        <Panel
          kicker="Portfolio"
          title="Position summary"
          action={
            <NavLink to="/portfolio" className="button button-ghost">
              Open portfolio <ArrowUpRight size={14} />
            </NavLink>
          }
        >
          {portfolio.pnl && portfolio.pnl.holdings.length > 0 ? (
            <div className="summary-stack">
              <div className="summary-row">
                <span className="summary-label">Total value</span>
                <span className="summary-value">{formatCurrency(portfolio.pnl.total_value)}</span>
              </div>
              <div className="summary-row">
                <span className="summary-label">Unrealised gain</span>
                <span className={`summary-value ${gain >= 0 ? 'is-gain' : 'is-loss'}`}>
                  {gain >= 0 ? <TrendingUp size={14} /> : <TrendingDown size={14} />}
                  {formatCurrency(gain)}
                </span>
              </div>
              <div className="summary-row">
                <span className="summary-label">Open positions</span>
                <span className="summary-value">{portfolio.pnl.holdings.length}</span>
              </div>
            </div>
          ) : (
            <EmptyState
              icon={<Wallet size={20} />}
              title="No open positions"
              hint="Holdings appear once an order executes and portfolio-service has consumed the event."
            />
          )}
        </Panel>
      </div>
    </div>
  );
}
