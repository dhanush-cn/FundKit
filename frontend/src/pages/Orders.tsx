// FundKit Control Center — order desk.
// Engineered by Dhanush C N (github.com/dhanush-cn)
import { useCallback, useMemo, useState } from 'react';
import type { FormEvent } from 'react';
import { ArrowDown, ArrowUp, KeyRound, ListOrdered, Search, Send, Trash2 } from 'lucide-react';

import { Drawer } from '../components/ui/Drawer';
import { EmptyState } from '../components/ui/EmptyState';
import { LifecycleTimeline } from '../components/ui/LifecycleTimeline';
import { Panel } from '../components/ui/Panel';
import { StatusBadge } from '../components/ui/StatusBadge';
import { ErrorBoundary } from '../components/ErrorBoundary';
import { formatDate, formatPaise, rupeesToPaise } from '../lib/format';
import { useDashboard } from '../state/dashboard-context';
import type { Order, OrderStatus, OrderType } from '../types';

type StatusFilter = OrderStatus | 'ALL';
type TypeFilter = OrderType | 'ALL';
type SortKey = 'created_at' | 'amount' | 'fund_id' | 'status';
type SortDirection = 'asc' | 'desc';

const STATUS_FILTERS: StatusFilter[] = ['ALL', 'PENDING', 'PROCESSING', 'EXECUTED', 'FAILED'];

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

export function Orders() {
  const { auth, orders } = useDashboard();
  const signedInUserId = auth.user?.id ?? '';

  const [form, setForm] = useState<OrderFormState>(initialForm);
  const [query, setQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('ALL');
  const [typeFilter, setTypeFilter] = useState<TypeFilter>('ALL');
  const [sortKey, setSortKey] = useState<SortKey>('created_at');
  const [sortDirection, setSortDirection] = useState<SortDirection>('desc');
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [confirmingCancel, setConfirmingCancel] = useState(false);

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

  // Fund suggestions come from the order book rather than a hardcoded list, so
  // the datalist reflects what this deployment has actually traded instead of
  // inventing instrument names that may not resolve.
  const knownFunds = useMemo(
    () => Array.from(new Set(orders.orders.map((order) => order.fund_id))).sort(),
    [orders.orders],
  );

  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase();

    const filtered = orders.orders.filter((order) => {
      if (statusFilter !== 'ALL' && order.status !== statusFilter) return false;
      if (typeFilter !== 'ALL' && order.type !== typeFilter) return false;
      if (!needle) return true;
      return (
        order.fund_id.toLowerCase().includes(needle) ||
        order.id.toLowerCase().includes(needle) ||
        order.idempotency_key.toLowerCase().includes(needle)
      );
    });

    const direction = sortDirection === 'asc' ? 1 : -1;
    return [...filtered].sort((left, right) => {
      switch (sortKey) {
        case 'amount':
          // Integer paise on both sides, so this comparison is exact.
          return (left.amount - right.amount) * direction;
        case 'fund_id':
          return left.fund_id.localeCompare(right.fund_id) * direction;
        case 'status':
          return left.status.localeCompare(right.status) * direction;
        case 'created_at':
        default:
          return left.created_at.localeCompare(right.created_at) * direction;
      }
    });
  }, [orders.orders, query, sortDirection, sortKey, statusFilter, typeFilter]);

  const selected = useMemo(
    () => orders.orders.find((order) => order.id === selectedId) ?? null,
    [orders.orders, selectedId],
  );

  const toggleSort = useCallback(
    (key: SortKey) => {
      if (key === sortKey) {
        setSortDirection((current) => (current === 'asc' ? 'desc' : 'asc'));
        return;
      }
      setSortKey(key);
      // A new column starts newest/largest first, which is what someone
      // clicking a column header on a ledger almost always wants.
      setSortDirection(key === 'fund_id' || key === 'status' ? 'asc' : 'desc');
    },
    [sortKey],
  );

  const closeDrawer = useCallback(() => {
    setSelectedId(null);
    setConfirmingCancel(false);
  }, []);

  const sortIndicator = (key: SortKey) =>
    key === sortKey ? (
      sortDirection === 'asc' ? (
        <ArrowUp size={12} />
      ) : (
        <ArrowDown size={12} />
      )
    ) : null;

  const filtersActive = statusFilter !== 'ALL' || typeFilter !== 'ALL' || query.trim() !== '';

  return (
    <div className="page">
      <div className="desk-grid">
        <Panel kicker="Order desk" title="Place an order" className="order-form-panel">
          <form className="order-form" onSubmit={(event) => void handleSubmit(event)}>
            <label className="field">
              <span className="field-label">Placing as</span>
              <input
                readOnly
                value={
                  auth.user ? `${auth.user.full_name} (@${auth.user.username})` : signedInUserId
                }
              />
            </label>

            <label className="field">
              <span className="field-label">Fund ID</span>
              <input
                list="fundkit-known-funds"
                value={form.fundId}
                onChange={(event) => setForm({ ...form, fundId: event.target.value })}
                required
              />
              <datalist id="fundkit-known-funds">
                {knownFunds.map((fund) => (
                  <option key={fund} value={fund} />
                ))}
              </datalist>
            </label>

            <div className="field-row">
              {/* step is 0.01 so the field can express paise. A step of 1 could
                  not represent ₹100.50 at all, which is part of why the
                  rounding question stayed invisible for so long. */}
              <label className="field">
                <span className="field-label">Amount (₹)</span>
                <input
                  type="number"
                  min="0.01"
                  step="0.01"
                  value={form.amount}
                  onChange={(event) => setForm({ ...form, amount: event.target.value })}
                  required
                />
              </label>

              <label className="field">
                <span className="field-label">Type</span>
                <select
                  value={form.type}
                  onChange={(event) =>
                    setForm({ ...form, type: event.target.value as OrderType })
                  }
                >
                  <option value="SIP">SIP</option>
                  <option value="LUMPSUM">LUMPSUM</option>
                </select>
              </label>
            </div>

            <label className="field">
              <span className="field-label">
                <KeyRound size={12} /> Idempotency key
              </span>
              <input
                value={form.idempotencyKey}
                onChange={(event) => setForm({ ...form, idempotencyKey: event.target.value })}
                required
              />
            </label>

            <button className="button button-primary" type="submit" disabled={orders.submitting}>
              <Send size={15} />
              {orders.submitting ? 'Submitting…' : 'Submit order'}
            </button>
          </form>

          <p className="helper-text">
            The gateway forwards this to order-service, which claims the Redis idempotency key,
            persists the order to Postgres and publishes the event to Kafka. Resubmitting the same
            key returns the original order rather than creating a second one.
          </p>
        </Panel>

        <Panel
          kicker="Order book"
          title={`${visible.length} of ${orders.orders.length} orders`}
          className="order-book-panel"
        >
          <div className="filter-bar">
            <label className="search-field">
              <Search size={14} aria-hidden="true" />
              <input
                type="search"
                placeholder="Search fund, order id or idempotency key"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                aria-label="Search orders"
              />
            </label>

            <div className="chip-group" role="group" aria-label="Filter by status">
              {STATUS_FILTERS.map((status) => (
                <button
                  key={status}
                  type="button"
                  className={`chip ${statusFilter === status ? 'is-active' : ''}`}
                  onClick={() => setStatusFilter(status)}
                  aria-pressed={statusFilter === status}
                >
                  {status === 'ALL' ? 'All' : status}
                </button>
              ))}
            </div>

            <select
              className="select-compact"
              value={typeFilter}
              onChange={(event) => setTypeFilter(event.target.value as TypeFilter)}
              aria-label="Filter by order type"
            >
              <option value="ALL">All types</option>
              <option value="SIP">SIP</option>
              <option value="LUMPSUM">LUMPSUM</option>
            </select>
          </div>

          <ErrorBoundary>
            <div className="table-shell">
              <table>
                <thead>
                  <tr>
                    <th>
                      <button type="button" className="th-sort" onClick={() => toggleSort('fund_id')}>
                        Fund {sortIndicator('fund_id')}
                      </button>
                    </th>
                    <th className="numeric">
                      <button type="button" className="th-sort" onClick={() => toggleSort('amount')}>
                        Amount {sortIndicator('amount')}
                      </button>
                    </th>
                    <th>Type</th>
                    <th>
                      <button type="button" className="th-sort" onClick={() => toggleSort('status')}>
                        Status {sortIndicator('status')}
                      </button>
                    </th>
                    <th>
                      <button
                        type="button"
                        className="th-sort"
                        onClick={() => toggleSort('created_at')}
                      >
                        Created {sortIndicator('created_at')}
                      </button>
                    </th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {visible.map((order) => (
                    <tr
                      key={order.id}
                      className={selectedId === order.id ? 'is-selected' : undefined}
                    >
                      <td>
                        <button
                          type="button"
                          className="fund-cell"
                          onClick={() => {
                            setSelectedId(order.id);
                            setConfirmingCancel(false);
                          }}
                        >
                          <span className="fund-name">{order.fund_id}</span>
                          <span className="fund-meta">{order.id.slice(0, 8)}…</span>
                        </button>
                      </td>
                      <td className="numeric">{formatPaise(order.amount)}</td>
                      <td>
                        <span className="type-tag">{order.type}</span>
                      </td>
                      <td>
                        <StatusBadge status={order.status} />
                      </td>
                      <td className="muted-cell">{formatDate(order.created_at)}</td>
                      <td>
                        <div className="action-row">
                          {order.status === 'PENDING' && (
                            <button
                              type="button"
                              className="button button-mini"
                              onClick={() => void orders.updateStatus(order.id, 'PROCESSING')}
                            >
                              Process
                            </button>
                          )}
                          {order.status === 'PROCESSING' && (
                            <>
                              <button
                                type="button"
                                className="button button-mini"
                                onClick={() => void orders.updateStatus(order.id, 'EXECUTED')}
                              >
                                Execute
                              </button>
                              <button
                                type="button"
                                className="button button-mini button-danger"
                                onClick={() => void orders.updateStatus(order.id, 'FAILED')}
                              >
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

                  {visible.length === 0 && (
                    <tr>
                      <td colSpan={6}>
                        <EmptyState
                          icon={<ListOrdered size={20} />}
                          title={filtersActive ? 'No orders match these filters' : 'No orders yet'}
                          hint={
                            filtersActive
                              ? 'Clear the search or pick a different status to widen the view.'
                              : 'Place the first one from the form on the left.'
                          }
                        />
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </ErrorBoundary>
        </Panel>
      </div>

      {orders.error && (
        <div className="error-banner" role="alert">
          <span>{orders.error}</span>
          <button type="button" className="button button-mini" onClick={orders.clearError}>
            Dismiss
          </button>
        </div>
      )}

      <OrderDetail
        order={selected}
        confirmingCancel={confirmingCancel}
        onConfirmCancel={() => setConfirmingCancel(true)}
        onAbortCancel={() => setConfirmingCancel(false)}
        onCancel={async (orderId) => {
          const done = await orders.cancelOrder(orderId);
          if (done) {
            closeDrawer();
          }
        }}
        onClose={closeDrawer}
      />
    </div>
  );
}

interface OrderDetailProps {
  order: Order | null;
  confirmingCancel: boolean;
  onConfirmCancel: () => void;
  onAbortCancel: () => void;
  onCancel: (orderId: string) => Promise<void>;
  onClose: () => void;
}

function OrderDetail({
  order,
  confirmingCancel,
  onConfirmCancel,
  onAbortCancel,
  onCancel,
  onClose,
}: OrderDetailProps) {
  const terminal = order ? order.status === 'EXECUTED' || order.status === 'FAILED' : true;

  return (
    <Drawer
      open={Boolean(order)}
      title={order?.fund_id ?? 'Order'}
      subtitle={order ? `${order.type} · ${formatPaise(order.amount)}` : undefined}
      onClose={onClose}
    >
      {order && (
        <>
          <dl className="detail-list">
            <div>
              <dt>Status</dt>
              <dd>
                <StatusBadge status={order.status} />
              </dd>
            </div>
            <div>
              <dt>Order id</dt>
              <dd>
                <code>{order.id}</code>
              </dd>
            </div>
            <div>
              <dt>Idempotency key</dt>
              <dd>
                <code>{order.idempotency_key}</code>
              </dd>
            </div>
            <div>
              <dt>Account</dt>
              <dd>{order.user_name ?? order.user_id}</dd>
            </div>
            <div>
              <dt>Amount</dt>
              <dd>
                {formatPaise(order.amount)}{' '}
                <span className="detail-note">({order.amount} paise on the wire)</span>
              </dd>
            </div>
            <div>
              <dt>Created</dt>
              <dd>{formatDate(order.created_at)}</dd>
            </div>
            <div>
              <dt>Updated</dt>
              <dd>{formatDate(order.updated_at)}</dd>
            </div>
          </dl>

          <h3 className="detail-heading">Lifecycle</h3>
          <LifecycleTimeline
            status={order.status}
            createdAt={order.created_at}
            updatedAt={order.updated_at}
          />

          {!terminal && (
            <div className="drawer-danger">
              {confirmingCancel ? (
                <>
                  <p className="drawer-danger-copy">
                    Cancel this order? The row is retained for the audit trail; what it gives up is
                    its place in the lifecycle.
                  </p>
                  <div className="action-row">
                    <button
                      type="button"
                      className="button button-danger"
                      onClick={() => void onCancel(order.id)}
                    >
                      <Trash2 size={14} /> Yes, cancel it
                    </button>
                    <button type="button" className="button button-quiet" onClick={onAbortCancel}>
                      Keep it
                    </button>
                  </div>
                </>
              ) : (
                <button type="button" className="button button-quiet" onClick={onConfirmCancel}>
                  <Trash2 size={14} /> Cancel order
                </button>
              )}
            </div>
          )}
        </>
      )}
    </Drawer>
  );
}
