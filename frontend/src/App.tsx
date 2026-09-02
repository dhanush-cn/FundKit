import { useEffect, useMemo, useState } from 'react';
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
import './App.css';

type OrderStatus = 'PENDING' | 'PROCESSING' | 'EXECUTED' | 'FAILED';
type OrderType = 'SIP' | 'LUMPSUM';
type ServiceStatus = 'UP' | 'DOWN';

interface Order {
  id: string;
  user_id: string;
  fund_id: string;
  amount: number;
  type: OrderType;
  status: OrderStatus;
  idempotency_key: string;
  created_at: string;
  updated_at: string;
}

interface ServiceHealthItem {
  service: string;
  url: string;
  status: ServiceStatus;
}

interface ServiceHealthResponse {
  status: 'UP' | 'DEGRADED';
  services: ServiceHealthItem[];
}

interface OrderFormState {
  userId: string;
  fundId: string;
  amount: string;
  type: OrderType;
  idempotencyKey: string;
}

interface PnLHolding {
  fund_id: string;
  fund_name: string;
  units: number;
  invested_amount: number;
  nav: number;
  current_value: number;
  unrealized_gain: number;
}

interface PnLResponse {
  user_id: string;
  total_value: number;
  total_unrealized_gain: number;
  holdings: PnLHolding[];
}

const apiBase = (import.meta.env.VITE_API_BASE_URL ?? 'http://localhost:8080').replace(/\/$/, '');
let globalToken = localStorage.getItem('fundkit_jwt');

const initialForm = (): OrderFormState => ({
  userId: 'user-001',
  fundId: 'quant-small-cap-fund',
  amount: '5000',
  type: 'SIP',
  idempotencyKey: crypto.randomUUID(),
});

async function apiRequest<T>(path: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  };
  
  if (globalToken) {
    headers['Authorization'] = `Bearer ${globalToken}`;
  }

  const response = await fetch(`${apiBase}${path}`, {
    headers: {
      ...headers,
      ...(init?.headers ?? {}),
    },
    ...init,
  });

  let payload: any = null;
  try {
    payload = await response.json();
  } catch {
    payload = null;
  }

  if (!response.ok) {
    throw new Error(payload?.error ?? `Request failed with ${response.status}`);
  }

  return payload as T;
}

function formatCurrency(amount: number) {
  return new Intl.NumberFormat('en-IN', {
    style: 'currency',
    currency: 'INR',
    maximumFractionDigits: 0,
  }).format(amount);
}

function formatDate(value: string) {
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString('en-IN', { dateStyle: 'medium', timeStyle: 'short' });
}

function statusTone(status: OrderStatus) {
  switch (status) {
    case 'PENDING':
      return 'pending';
    case 'PROCESSING':
      return 'processing';
    case 'EXECUTED':
      return 'executed';
    case 'FAILED':
      return 'failed';
  }
}

export default function App() {
  const [token, setToken] = useState<string | null>(globalToken);
  const [orders, setOrders] = useState<Order[]>([]);
  const [services, setServices] = useState<ServiceHealthItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [form, setForm] = useState<OrderFormState>(() => initialForm());
  const [pnlUserId, setPnlUserId] = useState('user-1');
  const [pnl, setPnl] = useState<PnLResponse | null>(null);
  const [pnlLoading, setPnlLoading] = useState(false);
  const [pnlError, setPnlError] = useState<string | null>(null);
  const [loginLoading, setLoginLoading] = useState(false);

  const orderMetrics = useMemo(() => {
    const totalInvested = orders.reduce((sum, order) => sum + order.amount, 0);
    const pending = orders.filter((order) => order.status === 'PENDING').length;
    const processing = orders.filter((order) => order.status === 'PROCESSING').length;
    const executed = orders.filter((order) => order.status === 'EXECUTED').length;
    const failed = orders.filter((order) => order.status === 'FAILED').length;
    return { totalInvested, pending, processing, executed, failed };
  }, [orders]);

  const allServicesHealthy = services.length > 0 && services.every((service) => service.status === 'UP');

  useEffect(() => {
    if (!token) return;
    void refreshAll();
    const timer = window.setInterval(() => {
      void refreshAll();
    }, 15000);
    return () => window.clearInterval(timer);
  }, [token]);

  async function refreshAll() {
    try {
      setError(null);
      setLoading(true);
      const [health, orderList] = await Promise.all([
        apiRequest<ServiceHealthResponse>('/services/health'),
        apiRequest<Order[]>('/orders'),
      ]);
      setServices(health.services);
      setOrders(orderList.sort((left, right) => right.created_at.localeCompare(left.created_at)));
    } catch (refreshError) {
      setError(refreshError instanceof Error ? refreshError.message : 'Unable to load FundKit data');
    } finally {
      setLoading(false);
    }
  }

  async function handleLogin() {
    setLoginLoading(true);
    setError(null);
    try {
      const data = await apiRequest<{token: string}>('/login', { method: 'POST' });
      localStorage.setItem('fundkit_jwt', data.token);
      globalToken = data.token;
      setToken(data.token);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Login failed');
    } finally {
      setLoginLoading(false);
    }
  }

  function handleLogout() {
    localStorage.removeItem('fundkit_jwt');
    globalToken = null;
    setToken(null);
    setOrders([]);
    setPnl(null);
  }

  async function handleSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSubmitting(true);
    setError(null);

    try {
      await apiRequest<Order>('/orders', {
        method: 'POST',
        body: JSON.stringify({
          user_id: form.userId,
          fund_id: form.fundId,
          amount: Number(form.amount),
          type: form.type,
          idempotency_key: form.idempotencyKey,
        }),
      });
      setForm((current) => ({ ...current, amount: '5000', idempotencyKey: crypto.randomUUID() }));
      await refreshAll();
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : 'Unable to place order');
    } finally {
      setSubmitting(false);
    }
  }

  async function updateStatus(orderId: string, status: OrderStatus) {
    setError(null);
    try {
      await apiRequest<Order>(`/orders/${orderId}`, {
        method: 'PATCH',
        body: JSON.stringify({ status }),
      });
      await refreshAll();
    } catch (updateError) {
      setError(updateError instanceof Error ? updateError.message : 'Unable to update order');
    }
  }

  async function handlePnLFetch() {
    if (!pnlUserId.trim()) {
      setPnlError('Enter a user id to fetch P&L');
      return;
    }

    setPnlError(null);
    setPnlLoading(true);
    try {
      const response = await apiRequest<PnLResponse>(`/portfolio/${encodeURIComponent(pnlUserId)}/pnl`);
      setPnl(response);
    } catch (pnlFetchError) {
      setPnlError(pnlFetchError instanceof Error ? pnlFetchError.message : 'Unable to load portfolio P&L');
    } finally {
      setPnlLoading(false);
    }
  }

  if (!token) {
    return (
      <div className="app-shell" style={{ justifyContent: 'center', alignItems: 'center' }}>
        <motion.div className="panel" initial={{ opacity: 0, scale: 0.95 }} animate={{ opacity: 1, scale: 1 }} style={{ maxWidth: 400, width: '100%', margin: '0 auto' }}>
          <div className="brand-mark" style={{ justifyContent: 'center', marginBottom: 24 }}>
            <div className="brand-orb">
              <Sparkles size={24} />
            </div>
            <div>
              <div className="brand-name" style={{ fontSize: 24 }}>FundKit</div>
            </div>
          </div>
          <h2 style={{ textAlign: 'center', marginBottom: 8 }}>Authentication Required</h2>
          <p style={{ textAlign: 'center', marginBottom: 24, color: 'var(--text-secondary)' }}>
            The API Gateway is secured. You must authenticate to access the order control plane.
          </p>
          <button className="submit-button" onClick={() => void handleLogin()} disabled={loginLoading} style={{ width: '100%' }}>
            {loginLoading ? 'Authenticating...' : 'Login with Dummy Token'}
          </button>
          {error && <div className="error-banner" style={{ marginTop: 16 }}>{error}</div>}
        </motion.div>
      </div>
    );
  }

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand-mark">
          <div className="brand-orb">
            <Sparkles size={18} />
          </div>
          <div>
            <div className="brand-name">FundKit</div>
            <div className="brand-subtitle">Order control plane</div>
          </div>
        </div>

        <div className="sidebar-card">
          <div className="sidebar-card-label">Gateway</div>
          <div className="sidebar-card-value">{apiBase}</div>
          <div className={`status-pill ${allServicesHealthy ? 'status-pill-up' : 'status-pill-degraded'}`}>
            <ShieldCheck size={14} />
            {allServicesHealthy ? 'All services healthy' : 'Some services degraded'}
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
          <button className="refresh-button" type="button" onClick={() => void refreshAll()}>
            <RefreshCw size={16} />
            Refresh stack
          </button>
          <button className="refresh-button" type="button" onClick={handleLogout} style={{ backgroundColor: 'transparent', color: 'var(--danger-text)', border: '1px solid var(--danger-border)' }}>
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
              Create SIP and lump-sum orders, watch the lifecycle move through PENDING, PROCESSING, and terminal states,
              and verify the backend services from one dashboard.
            </p>
          </div>

          <div className="hero-card">
            <div className="hero-card-top">
              <span>System status</span>
              <span className={`system-chip ${allServicesHealthy ? 'system-chip-up' : 'system-chip-degraded'}`}>
                {allServicesHealthy ? 'Stable' : 'Degraded'}
              </span>
            </div>
            <div className="hero-card-metric">{formatCurrency(orderMetrics.totalInvested)}</div>
            <div className="hero-card-caption">Capital routed through the order service</div>
            <div className="hero-card-row">
              <span><CheckCircle2 size={14} /> {orderMetrics.executed} executed</span>
              <span><Clock3 size={14} /> {orderMetrics.pending + orderMetrics.processing} active</span>
            </div>
          </div>
        </section>

        <section className="metrics-grid">
          <motion.article className="metric-card" initial={{ opacity: 0, y: 18 }} animate={{ opacity: 1, y: 0 }}>
            <div className="metric-label">Executed</div>
            <div className="metric-value">{orderMetrics.executed}</div>
            <div className="metric-foot"><CheckCircle2 size={14} /> Successful orders</div>
          </motion.article>
          <motion.article className="metric-card" initial={{ opacity: 0, y: 18 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: 0.05 }}>
            <div className="metric-label">Active</div>
            <div className="metric-value">{orderMetrics.pending + orderMetrics.processing}</div>
            <div className="metric-foot"><CircleDashed size={14} /> Pending or processing</div>
          </motion.article>
          <motion.article className="metric-card" initial={{ opacity: 0, y: 18 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: 0.1 }}>
            <div className="metric-label">Failed</div>
            <div className="metric-value">{orderMetrics.failed}</div>
            <div className="metric-foot"><ArrowRight size={14} /> Rejected or failed lifecycle</div>
          </motion.article>
          <motion.article className="metric-card" initial={{ opacity: 0, y: 18 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: 0.15 }}>
            <div className="metric-label">Services</div>
            <div className="metric-value">{services.filter((service) => service.status === 'UP').length}/{services.length}</div>
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

            <form className="order-form" onSubmit={handleSubmit}>
              <label>
                User ID
                <input value={form.userId} onChange={(event) => setForm({ ...form, userId: event.target.value })} />
              </label>
              <label>
                Fund ID
                <input value={form.fundId} onChange={(event) => setForm({ ...form, fundId: event.target.value })} />
              </label>
              <label>
                Amount
                <input type="number" min="1" value={form.amount} onChange={(event) => setForm({ ...form, amount: event.target.value })} />
              </label>
              <label>
                Order type
                <select value={form.type} onChange={(event) => setForm({ ...form, type: event.target.value as OrderType })}>
                  <option value="SIP">SIP</option>
                  <option value="LUMPSUM">LUMPSUM</option>
                </select>
              </label>
              <label className="full-width">
                Idempotency key
                <input value={form.idempotencyKey} onChange={(event) => setForm({ ...form, idempotencyKey: event.target.value })} />
              </label>

              <button className="submit-button" type="submit" disabled={submitting}>
                {submitting ? 'Submitting...' : 'Submit order'}
              </button>
            </form>

            <div className="helper-text">
              The gateway forwards this to order-service, which persists the order, claims Redis idempotency, and publishes
              the event to Kafka.
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
              {services.map((service) => (
                <div key={service.service} className="service-item">
                  <div>
                    <div className="service-name">{service.service}</div>
                    <div className="service-url">{service.url}</div>
                  </div>
                  <span className={`status-pill ${service.status === 'UP' ? 'status-pill-up' : 'status-pill-down'}`}>
                    {service.status}
                  </span>
                </div>
              ))}
            </div>

            <div className="helper-text">
              Gateway health is aggregated from order-service, portfolio-service, and notification-service endpoints.
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
              User ID
              <input value={pnlUserId} onChange={(event) => setPnlUserId(event.target.value)} />
            </label>
            <button className="ghost-button" type="button" onClick={() => void handlePnLFetch()} disabled={pnlLoading}>
              {pnlLoading ? 'Loading...' : 'Fetch P&L'}
            </button>
          </div>

          {pnlError && <div className="error-banner">{pnlError}</div>}

          {pnl && (
            <div className="pnl-grid">
              <div className="pnl-metric">
                <div className="metric-label">Total value</div>
                <div className="metric-value">{formatCurrency(pnl.total_value)}</div>
              </div>
              <div className="pnl-metric">
                <div className="metric-label">Unrealized gain</div>
                <div className={`metric-value ${pnl.total_unrealized_gain >= 0 ? 'pnl-positive' : 'pnl-negative'}`}>
                  {formatCurrency(pnl.total_unrealized_gain)}
                </div>
              </div>
              <div className="pnl-holdings">
                {pnl.holdings.map((holding) => (
                  <div key={holding.fund_id} className="pnl-holding">
                    <div>
                      <div className="fund-name">{holding.fund_name}</div>
                      <div className="fund-meta">Units {holding.units.toFixed(2)} · NAV {holding.nav.toFixed(2)}</div>
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
            </div>
          )}

          <div className="helper-text">
            The portfolio service caches NAV prices in Redis and recalculates unrealized gains on demand.
          </div>
        </motion.section>

        <motion.section className="panel" id="orders" initial={{ opacity: 0, y: 20 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: 0.1 }}>
          <div className="panel-header">
            <div>
              <div className="panel-kicker">Order book</div>
              <h2>Latest orders</h2>
            </div>
            <button className="ghost-button" type="button" onClick={() => void refreshAll()}>
              <RefreshCw size={16} />
              Reload
            </button>
          </div>

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
                {orders.map((order) => (
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
                    <td>{formatCurrency(order.amount)}</td>
                    <td>{order.type}</td>
                    <td>
                      <span className={`order-badge order-badge-${statusTone(order.status).toLowerCase()}`}>{order.status}</span>
                    </td>
                    <td>{formatDate(order.created_at)}</td>
                    <td>
                      <div className="action-row">
                        {order.status === 'PENDING' && (
                          <button type="button" onClick={() => void updateStatus(order.id, 'PROCESSING')}>Process</button>
                        )}
                        {order.status === 'PROCESSING' && (
                          <>
                            <button type="button" onClick={() => void updateStatus(order.id, 'EXECUTED')}>Execute</button>
                            <button type="button" onClick={() => void updateStatus(order.id, 'FAILED')}>Fail</button>
                          </>
                        )}
                        {(order.status === 'EXECUTED' || order.status === 'FAILED') && <span className="action-static">Terminal</span>}
                      </div>
                    </td>
                  </tr>
                ))}
                {!loading && orders.length === 0 && (
                  <tr>
                    <td colSpan={6} className="empty-state">
                      No orders yet. Create the first one above.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </motion.section>

        {error && <div className="error-banner">{error}</div>}
      </main>
    </div>
  );
}