// FundKit Control Center — order control plane dashboard.
// Engineered by Dhanush C N (github.com/dhanush-cn)
import { useCallback, useState } from 'react';
import type { FormEvent } from 'react';
import { motion } from 'framer-motion';
import {
  Activity,
  ArrowRight,
  CheckCircle2,
  CircleDashed,
  Clock3,
  Cloud,
  Layers3,
  RefreshCw,
  Send,
  ShieldCheck,
  Sparkles,
} from 'lucide-react';

import { AuthPanel } from './components/AuthPanel';
import { ErrorBoundary } from './components/ErrorBoundary';
import { useAuth } from './hooks/useAuth';
import { useOrders } from './hooks/useOrders';
import { usePortfolio } from './hooks/usePortfolio';
import { useServiceHealth } from './hooks/useServiceHealth';
import { API_BASE_URL } from './lib/api';
import { formatCurrency, formatDate, formatPaise, rupeesToPaise, statusTone } from './lib/format';
import type { OrderType } from './types';
import './App.css';

const AUTHOR = 'Dhanush C N';
const AUTHOR_URL = 'https://github.com/dhanush-cn';

interface OrderFormState {
  fundId: string;
  amount: string;
  type: OrderType;
  idempotencyKey: string;
}

function newIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID();
  }
  return `fundkit-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

const initialForm = (): OrderFormState => ({
  fundId: 'quant-small-cap-fund',
  amount: '5000',
  type: 'SIP',
  idempotencyKey: newIdempotencyKey(),
});

export default function App() {
  const auth = useAuth();
  const isAuthenticated = Boolean(auth.token);

  const orders = useOrders({ enabled: isAuthenticated, onUnauthorized: auth.logout });
  const health = useServiceHealth(isAuthenticated);

  const [form, setForm] = useState<OrderFormState>(initialForm);

  // The signed-in account is the subject of everything on this screen. The
  // gateway overrides an order's user id with the verified token subject
  // regardless, so anything else shown here would be a lie. Deriving the
  // defaults beats copying them into state with an effect.
  const signedInUserId = auth.user?.id ?? '';

  // The P&L widget has no way to ask for anyone else's portfolio: it is
  // always the signed-in user's own id, polled the same way the order book
  // is. Even if this were tampered with, the backend independently pins the
  // response to the gateway-verified identity -- this is defense in depth,
  // not the only guard.
  const portfolio = usePortfolio({
    enabled: isAuthenticated,
    userId: isAuthenticated ? signedInUserId : null,
    onUnauthorized: auth.logout,
  });

  const handleSubmit = useCallback(
    async (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault();
      // The field holds rupees because that is what a person types; the API
      // takes paise. Converting here, once, at the boundary, is the same rule
      // the Go handlers follow — and the conversion is exact, so ₹100.50
      // becomes 10050 rather than 10049.999999999998.
      const amountPaise = rupeesToPaise(form.amount);
      if (!Number.isFinite(amountPaise) || amountPaise <= 0) {
        return;
      }

      const placed = await orders.placeOrder({
        user_id: signedInUserId,
        fund_id: form.fundId,
        amount: amountPaise,
        type: form.type,
        idempotency_key: form.idempotencyKey,
      });

      if (placed) {
        // A new key per submission: reusing one would be rejected by the
        // order-service idempotency guard, which is exactly what it is for.
        setForm((current) => ({ ...current, idempotencyKey: newIdempotencyKey() }));
      }
    },
    [form, orders, signedInUserId],
  );

  const handleRefresh = useCallback(() => {
    void orders.refresh();
    void health.refresh();
  }, [health, orders]);

  const handleLogout = useCallback(() => {
    auth.logout();
    portfolio.reset();
  }, [auth, portfolio]);

  if (!isAuthenticated) {
    return <AuthPanel auth={auth} />;
  }

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand-mark">
          <div className="brand-orb">
            <Sparkles size={18} />
          </div>
          <div>
            <div className="brand-name">FundKit Control Center</div>
            <div className="brand-subtitle">
              {auth.user ? `Signed in as ${auth.user.full_name}` : `Engineered by ${AUTHOR}`}
            </div>
          </div>
        </div>

        <div className="sidebar-card">
          <div className="sidebar-card-label">Gateway</div>
          <div className="sidebar-card-value">{API_BASE_URL}</div>
          <div className={`status-pill ${health.allHealthy ? 'status-pill-up' : 'status-pill-degraded'}`}>
            <ShieldCheck size={14} />
            {health.allHealthy ? 'All services healthy' : 'Some services degraded'}
          </div>
        </div>

        <nav className="sidebar-links">
          <a href="#overview">Overview</a>
          <a href="#order-form">Place order</a>
          <a href="#service-health">Service health</a>
          <a href="#pnl">Portfolio P&amp;L</a>
          <a href="#orders">Orders</a>
        </nav>

        <div style={{ marginTop: 'auto', display: 'flex', flexDirection: 'column', gap: 8 }}>
          <button className="refresh-button" type="button" onClick={handleRefresh}>
            <RefreshCw size={16} />
            Refresh stack
          </button>
          <button
            className="refresh-button"
            type="button"
            onClick={handleLogout}
            style={{
              backgroundColor: 'transparent',
              color: 'var(--danger-text)',
              border: '1px solid var(--danger-border)',
            }}
          >
            Logout
          </button>
        </div>
      </aside>

      <main className="content">
        <section className="hero" id="overview">
          <div>
            <div className="eyebrow">
              <Activity size={14} />
              Connected to API gateway
            </div>
            <h1>Mutual fund orders, backed by real services.</h1>
            <p>
              Create SIP and lump-sum orders, watch the lifecycle move through PENDING, PROCESSING
              and terminal states, and verify every backend service from one dashboard.
            </p>
          </div>

          <div className="hero-card">
            <div className="hero-card-top">
              <span>System status</span>
              <span className={`system-chip ${health.allHealthy ? 'system-chip-up' : 'system-chip-degraded'}`}>
                {health.allHealthy ? 'Stable' : 'Degraded'}
              </span>
            </div>
            <div className="hero-card-metric">{formatPaise(orders.metrics.totalInvested)}</div>
            <div className="hero-card-caption">Capital routed through the order service</div>
            <div className="hero-card-row">
              <span><CheckCircle2 size={14} /> {orders.metrics.executed} executed</span>
              <span><Clock3 size={14} /> {orders.metrics.pending + orders.metrics.processing} active</span>
            </div>
          </div>
        </section>

        <section className="metrics-grid">
          <motion.article className="metric-card" initial={{ opacity: 0, y: 18 }} animate={{ opacity: 1, y: 0 }}>
            <div className="metric-label">Executed</div>
            <div className="metric-value">{orders.metrics.executed}</div>
            <div className="metric-foot"><CheckCircle2 size={14} /> Successful orders</div>
          </motion.article>
          <motion.article className="metric-card" initial={{ opacity: 0, y: 18 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: 0.05 }}>
            <div className="metric-label">Active</div>
            <div className="metric-value">{orders.metrics.pending + orders.metrics.processing}</div>
            <div className="metric-foot"><CircleDashed size={14} /> Pending or processing</div>
          </motion.article>
          <motion.article className="metric-card" initial={{ opacity: 0, y: 18 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: 0.1 }}>
            <div className="metric-label">Failed</div>
            <div className="metric-value">{orders.metrics.failed}</div>
            <div className="metric-foot"><ArrowRight size={14} /> Rejected or failed lifecycle</div>
          </motion.article>
          <motion.article className="metric-card" initial={{ opacity: 0, y: 18 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: 0.15 }}>
            <div className="metric-label">Services</div>
            <div className="metric-value">
              {health.services.filter((service) => service.status === 'UP').length}/{health.services.length}
            </div>
            <div className="metric-foot"><Layers3 size={14} /> Gateway, order, portfolio, notification</div>
          </motion.article>
        </section>

        <section className="dashboard-grid">
          <motion.section className="panel" id="order-form" initial={{ opacity: 0, y: 20 }} animate={{ opacity: 1, y: 0 }}>
            <div className="panel-header">
              <div>
                <div className="panel-kicker">Order desk</div>
                <h2>Place an order through the gateway</h2>
              </div>
              <Send size={18} />
            </div>

            <form className="order-form" onSubmit={(event) => void handleSubmit(event)}>
              <label>
                Placing as
                <input
                  value={auth.user ? `${auth.user.full_name} (@${auth.user.username})` : signedInUserId}
                  readOnly
                />
              </label>
              <label>
                Fund ID
                <input value={form.fundId} onChange={(event) => setForm({ ...form, fundId: event.target.value })} />
              </label>
              {/* step is 0.01 so the field can express paise. The old step of 1
                  could not represent ₹100.50 at all, which is part of why the
                  rounding question stayed invisible for so long. */}
              <label>
                Amount (₹)
                <input
                  type="number"
                  min="0.01"
                  step="0.01"
                  value={form.amount}
                  onChange={(event) => setForm({ ...form, amount: event.target.value })}
                />
              </label>
              <label>
                Order type
                <select
                  value={form.type}
                  onChange={(event) => setForm({ ...form, type: event.target.value as OrderType })}
                >
                  <option value="SIP">SIP</option>
                  <option value="LUMPSUM">LUMPSUM</option>
                </select>
              </label>
              <label className="full-width">
                Idempotency key
                <input
                  value={form.idempotencyKey}
                  onChange={(event) => setForm({ ...form, idempotencyKey: event.target.value })}
                />
              </label>

              <button className="submit-button" type="submit" disabled={orders.submitting}>
                {orders.submitting ? 'Submitting…' : 'Submit order'}
              </button>
            </form>

            <div className="helper-text">
              The gateway forwards this to order-service, which claims the Redis idempotency key,
              persists the order to Postgres and publishes the event to Kafka.
            </div>
          </motion.section>

          <motion.section className="panel" id="service-health" initial={{ opacity: 0, y: 20 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: 0.05 }}>
            <div className="panel-header">
              <div>
                <div className="panel-kicker">Stack health</div>
                <h2>Connected services</h2>
              </div>
              <Cloud size={18} />
            </div>

            <div className="service-list">
              {health.services.map((service) => (
                <div key={service.service} className="service-item">
                  <div>
                    <div className="service-name">{service.service}</div>
                    <div className="service-url">{service.url}</div>
                  </div>
                  <span className={`status-pill ${service.status === 'UP' ? 'status-pill-up' : 'status-pill-down'}`}>
                    {service.status}
                    {service.latency ? ` · ${service.latency}` : ''}
                  </span>
                </div>
              ))}
            </div>

            {health.error && <div className="error-banner">{health.error}</div>}

            <div className="helper-text">
              The gateway probes every service&apos;s <code>/readyz</code> endpoint concurrently, so
              this panel reflects dependency health rather than just a live process.
            </div>
          </motion.section>
        </section>

        <motion.section className="panel pnl-panel" id="pnl" initial={{ opacity: 0, y: 20 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: 0.08 }}>
          <div className="panel-header">
            <div>
              <div className="panel-kicker">Portfolio intelligence</div>
              <h2>Real-time P&amp;L</h2>
            </div>
            <Sparkles size={18} />
          </div>

          <div className="pnl-controls">
            <label>
              Account
              <input
                readOnly
                value={auth.user ? `${auth.user.full_name} (@${auth.user.username})` : signedInUserId}
              />
            </label>
            <button
              className="ghost-button"
              type="button"
              onClick={() => void portfolio.refresh()}
              disabled={portfolio.loading}
            >
              <RefreshCw size={16} />
              {portfolio.loading ? 'Refreshing…' : 'Refresh'}
            </button>
          </div>

          {portfolio.error && <div className="error-banner">{portfolio.error}</div>}

          {portfolio.loading && !portfolio.pnl && (
            <div className="empty-state">Loading your P&amp;L…</div>
          )}

          {portfolio.pnl && (
            <div className="pnl-grid">
              <div className="pnl-metric">
                <div className="metric-label">Total value</div>
                <div className="metric-value">{formatCurrency(portfolio.pnl.total_value)}</div>
              </div>
              <div className="pnl-metric">
                <div className="metric-label">Unrealized gain</div>
                <div className={`metric-value ${portfolio.pnl.total_unrealized_gain >= 0 ? 'pnl-positive' : 'pnl-negative'}`}>
                  {formatCurrency(portfolio.pnl.total_unrealized_gain)}
                </div>
              </div>
              {portfolio.pnl.holdings.length === 0 ? (
                <div className="empty-state">
                  No open positions yet. Place an order above and it will show up here once it
                  executes.
                </div>
              ) : (
                <div className="pnl-holdings">
                  {portfolio.pnl.holdings.map((holding) => (
                    <div key={holding.fund_id} className="pnl-holding">
                      <div>
                        <div className="fund-name">{holding.fund_name}</div>
                        <div className="fund-meta">
                          Units {holding.units.toFixed(2)} · NAV {holding.nav.toFixed(2)}
                        </div>
                      </div>
                      <div className="pnl-values">
                        <div>{formatCurrency(holding.current_value)}</div>
                        <div className={holding.unrealized_gain >= 0 ? 'pnl-positive' : 'pnl-negative'}>
                          {formatCurrency(holding.unrealized_gain)}
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}

          <div className="helper-text">
            order-service resolves this over gRPC; portfolio-service serves it from a short-TTL
            Redis cache in front of the NAV feed.
          </div>
        </motion.section>

        <motion.section className="panel" id="orders" initial={{ opacity: 0, y: 20 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: 0.1 }}>
          <div className="panel-header">
            <div>
              <div className="panel-kicker">Order book</div>
              <h2>Latest orders</h2>
            </div>
            <button className="ghost-button" type="button" onClick={() => void orders.refresh()}>
              <RefreshCw size={16} />
              Reload
            </button>
          </div>

          <ErrorBoundary>
            <div className="table-shell">
              <table>
                <thead>
                  <tr>
                    <th>Fund</th>
                    <th>Amount</th>
                    <th>Type</th>
                    <th>Status</th>
                    <th>Created</th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {orders.orders.map((order) => (
                    <tr key={order.id}>
                      <td>
                        <div className="fund-cell">
                          <div className="fund-dot" />
                          <div>
                            <div className="fund-name">{order.fund_id}</div>
                            <div className="fund-meta">{order.user_id}</div>
                          </div>
                        </div>
                      </td>
                      <td>{formatPaise(order.amount)}</td>
                      <td>{order.type}</td>
                      <td>
                        <span className={`order-badge order-badge-${statusTone(order.status)}`}>
                          {order.status}
                        </span>
                      </td>
                      <td>{formatDate(order.created_at)}</td>
                      <td>
                        <div className="action-row">
                          {order.status === 'PENDING' && (
                            <button type="button" onClick={() => void orders.updateStatus(order.id, 'PROCESSING')}>
                              Process
                            </button>
                          )}
                          {order.status === 'PROCESSING' && (
                            <>
                              <button type="button" onClick={() => void orders.updateStatus(order.id, 'EXECUTED')}>
                                Execute
                              </button>
                              <button type="button" onClick={() => void orders.updateStatus(order.id, 'FAILED')}>
                                Fail
                              </button>
                            </>
                          )}
                          {(order.status === 'EXECUTED' || order.status === 'FAILED') && (
                            <span className="action-static">Terminal</span>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))}
                  {!orders.loading && orders.orders.length === 0 && (
                    <tr>
                      <td colSpan={6} className="empty-state">
                        No orders yet. Create the first one above.
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </ErrorBoundary>
        </motion.section>

        {orders.error && (
          <div className="error-banner" onClick={orders.clearError} role="alert">
            {orders.error}
          </div>
        )}

        <footer className="app-footer">
          <span>FundKit Control Center</span>
          <span className="author-credit">
            Engineered by{' '}
            <a href={AUTHOR_URL} target="_blank" rel="noreferrer">
              {AUTHOR}
            </a>
          </span>
        </footer>
      </main>
    </div>
  );
}
