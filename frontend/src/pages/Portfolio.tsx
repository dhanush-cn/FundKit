// FundKit Control Center — portfolio and P&L.
// Engineered by Dhanush C N (github.com/dhanush-cn)
import { useMemo, useState } from 'react';
import { ArrowDown, ArrowUp, RefreshCw, TrendingDown, TrendingUp, Wallet } from 'lucide-react';

import { Donut } from '../components/ui/Donut';
import { EmptyState } from '../components/ui/EmptyState';
import { MetricCard } from '../components/ui/MetricCard';
import { Panel } from '../components/ui/Panel';
import { formatCurrency } from '../lib/format';
import { allocationColor } from '../lib/palette';
import { useDashboard } from '../state/dashboard-context';
import type { PnLHolding } from '../types';

type HoldingSortKey = 'fund_name' | 'current_value' | 'unrealized_gain' | 'units';
type SortDirection = 'asc' | 'desc';

export function Portfolio() {
  const { auth, portfolio } = useDashboard();
  const [sortKey, setSortKey] = useState<HoldingSortKey>('current_value');
  const [sortDirection, setSortDirection] = useState<SortDirection>('desc');

  // Memoised because `?? []` mints a fresh array on every render, which
  // would make both memos below recompute each time and defeat the point
  // of having them — the poll already re-renders this page every 15s.
  const holdings = useMemo(() => portfolio.pnl?.holdings ?? [], [portfolio.pnl]);
  const totalValue = portfolio.pnl?.total_value ?? 0;
  const totalGain = portfolio.pnl?.total_unrealized_gain ?? 0;

  // Invested is derived rather than read from the response: the P&L contract
  // reports value and gain, and cost basis is the difference. Deriving it here
  // keeps the two figures on screen arithmetically consistent with each other
  // even mid-poll, which a separately-fetched number would not be.
  const invested = totalValue - totalGain;
  const returnPct = invested > 0 ? (totalGain / invested) * 100 : 0;

  const sorted = useMemo(() => {
    const direction = sortDirection === 'asc' ? 1 : -1;
    return [...holdings].sort((left, right) => {
      switch (sortKey) {
        case 'fund_name':
          return left.fund_name.localeCompare(right.fund_name) * direction;
        case 'units':
          return (left.units - right.units) * direction;
        case 'unrealized_gain':
          return (left.unrealized_gain - right.unrealized_gain) * direction;
        case 'current_value':
        default:
          return (left.current_value - right.current_value) * direction;
      }
    });
  }, [holdings, sortDirection, sortKey]);

  // The donut and its legend share one ordering — largest position first — so
  // the colour a reader picks out of the ring maps to the same row every time,
  // regardless of how the table beside it happens to be sorted.
  const allocation = useMemo(
    () =>
      [...holdings]
        .sort((left, right) => right.current_value - left.current_value)
        .map((holding) => ({
          key: holding.fund_id,
          label: holding.fund_name,
          value: holding.current_value,
        })),
    [holdings],
  );

  const toggleSort = (key: HoldingSortKey) => {
    if (key === sortKey) {
      setSortDirection((current) => (current === 'asc' ? 'desc' : 'asc'));
      return;
    }
    setSortKey(key);
    setSortDirection(key === 'fund_name' ? 'asc' : 'desc');
  };

  const indicator = (key: HoldingSortKey) =>
    key === sortKey ? (
      sortDirection === 'asc' ? (
        <ArrowUp size={12} />
      ) : (
        <ArrowDown size={12} />
      )
    ) : null;

  return (
    <div className="page">
      <section className="metric-row" aria-label="Portfolio totals">
        <MetricCard
          label="Total value"
          value={formatCurrency(totalValue)}
          foot={
            auth.user ? `${auth.user.full_name} (@${auth.user.username})` : 'Signed-in account'
          }
        />
        <MetricCard label="Invested" value={formatCurrency(invested)} foot="Cost basis" />
        <MetricCard
          label="Unrealised gain"
          value={formatCurrency(totalGain)}
          tone={totalGain >= 0 ? 'gain' : 'loss'}
          foot={
            <>
              {totalGain >= 0 ? <TrendingUp size={13} /> : <TrendingDown size={13} />}
              {returnPct >= 0 ? '+' : ''}
              {returnPct.toFixed(2)}% on cost
            </>
          }
        />
        <MetricCard
          label="Open positions"
          value={holdings.length}
          foot={<>{holdings.length === 1 ? 'fund' : 'funds'} with units allotted</>}
        />
      </section>

      {portfolio.error && (
        <div className="error-banner" role="alert">
          {portfolio.error}
        </div>
      )}

      <div className="portfolio-grid">
        <Panel kicker="Allocation" title="By current value">
          {allocation.length === 0 ? (
            <EmptyState
              icon={<Wallet size={20} />}
              title="Nothing allotted yet"
              hint="An executed order becomes units once portfolio-service consumes the event."
            />
          ) : (
            <div className="allocation">
              <Donut
                slices={allocation}
                centerValue={formatCurrency(totalValue)}
                centerLabel="Total value"
              />
              <ul className="legend">
                {allocation.map((slice, index) => {
                  const share = totalValue > 0 ? (slice.value / totalValue) * 100 : 0;
                  return (
                    <li key={slice.key} className="legend-item">
                      <span
                        className="legend-swatch"
                        style={{ backgroundColor: allocationColor(index) }}
                        aria-hidden="true"
                      />
                      <span className="legend-label">{slice.label}</span>
                      <span className="legend-value">{share.toFixed(1)}%</span>
                    </li>
                  );
                })}
              </ul>
            </div>
          )}
        </Panel>

        <Panel
          kicker="Holdings"
          title="Positions and P&L"
          action={
            <button
              type="button"
              className="button button-ghost"
              onClick={() => void portfolio.refresh()}
              disabled={portfolio.loading}
            >
              <RefreshCw size={14} />
              {portfolio.loading ? 'Refreshing…' : 'Refresh'}
            </button>
          }
        >
          {sorted.length === 0 ? (
            <EmptyState
              icon={<Wallet size={20} />}
              title="No open positions"
              hint="Place an order on the order desk; holdings appear here once it executes."
            />
          ) : (
            <div className="table-shell">
              <table>
                <thead>
                  <tr>
                    <th>
                      <button
                        type="button"
                        className="th-sort"
                        onClick={() => toggleSort('fund_name')}
                      >
                        Fund {indicator('fund_name')}
                      </button>
                    </th>
                    <th className="numeric">
                      <button type="button" className="th-sort" onClick={() => toggleSort('units')}>
                        Units {indicator('units')}
                      </button>
                    </th>
                    <th className="numeric">NAV</th>
                    <th className="numeric">
                      <button
                        type="button"
                        className="th-sort"
                        onClick={() => toggleSort('current_value')}
                      >
                        Value {indicator('current_value')}
                      </button>
                    </th>
                    <th className="numeric">
                      <button
                        type="button"
                        className="th-sort"
                        onClick={() => toggleSort('unrealized_gain')}
                      >
                        Gain {indicator('unrealized_gain')}
                      </button>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {sorted.map((holding) => (
                    <HoldingRow key={holding.fund_id} holding={holding} />
                  ))}
                </tbody>
              </table>
            </div>
          )}

          <p className="helper-text">
            order-service resolves this over gRPC; portfolio-service serves it from a short-TTL
            Redis cache in front of the NAV feed. Figures arrive as rupee decimals — the order
            ledger is integer paise, which is why the two halves of this app use different
            formatters.
          </p>
        </Panel>
      </div>
    </div>
  );
}

function HoldingRow({ holding }: { holding: PnLHolding }) {
  const positive = holding.unrealized_gain >= 0;
  const costBasis = holding.invested_amount || holding.current_value - holding.unrealized_gain;
  const pct = costBasis > 0 ? (holding.unrealized_gain / costBasis) * 100 : 0;

  return (
    <tr>
      <td>
        <span className="fund-name">{holding.fund_name}</span>
        <span className="fund-meta">{holding.fund_id}</span>
      </td>
      <td className="numeric">{holding.units.toFixed(3)}</td>
      <td className="numeric muted-cell">{holding.nav.toFixed(2)}</td>
      <td className="numeric">{formatCurrency(holding.current_value)}</td>
      <td className={`numeric ${positive ? 'is-gain' : 'is-loss'}`}>
        {formatCurrency(holding.unrealized_gain)}
        <span className="cell-note">
          {positive ? '+' : ''}
          {pct.toFixed(2)}%
        </span>
      </td>
    </tr>
  );
}
