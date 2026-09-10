// FundKit Control Center — persistent application shell.
// Engineered by Dhanush C N (github.com/dhanush-cn)
import type { ReactNode } from 'react';
import {
  Activity,
  LayoutDashboard,
  ListOrdered,
  LogOut,
  PieChart,
  RefreshCw,
  ShieldCheck,
} from 'lucide-react';

import { ThemeToggle } from './ui/ThemeToggle';
import { NavLink } from '../router/NavLink';
import { ROUTE_TITLES, useRoute } from '../router/route';
import { useDashboard } from '../state/dashboard-context';
import { API_BASE_URL } from '../lib/api';

const AUTHOR = 'Dhanush C N';
const AUTHOR_URL = 'https://github.com/dhanush-cn';

/**
 * The shell is everything that survives a route change: brand, navigation,
 * stack status and the account controls.
 *
 * The old layout used in-page anchors for the same five destinations, which
 * meant the whole dashboard was mounted at once and the sidebar could never
 * indicate where you were beyond the scroll position. Real routes make each
 * section a page that can own its own controls, and the shell's only job is to
 * stay put around them.
 */
export function AppShell({ children }: { children: ReactNode }) {
  const { auth, health, refreshAll, logout } = useDashboard();
  const route = useRoute();

  const servicesUp = health.services.filter((service) => service.status === 'UP').length;

  return (
    <div className="app-shell">
      <a className="skip-link" href="#main">
        Skip to content
      </a>

      <aside className="sidebar">
        <div className="brand">
          <span className="brand-mark" aria-hidden="true">
            FK
          </span>
          <span className="brand-text">
            <span className="brand-name">FundKit</span>
            <span className="brand-sub">Control Center</span>
          </span>
        </div>

        <nav className="sidebar-nav" aria-label="Sections">
          <NavLink to="/" icon={<LayoutDashboard size={16} />}>
            Overview
          </NavLink>
          <NavLink to="/orders" icon={<ListOrdered size={16} />}>
            Order desk
          </NavLink>
          <NavLink to="/portfolio" icon={<PieChart size={16} />}>
            Portfolio
          </NavLink>
          <NavLink to="/system" icon={<Activity size={16} />}>
            System health
          </NavLink>
        </nav>

        <div className="sidebar-status">
          <p className="sidebar-status-label">Gateway</p>
          <p className="sidebar-status-value">{API_BASE_URL}</p>
          <p
            className={`status-pill ${health.allHealthy ? 'status-pill-up' : 'status-pill-degraded'}`}
          >
            <ShieldCheck size={13} />
            {health.services.length === 0
              ? 'Probing services'
              : health.allHealthy
                ? 'All services healthy'
                : `${servicesUp}/${health.services.length} services up`}
          </p>
        </div>

        <footer className="sidebar-foot">
          Engineered by{' '}
          <a href={AUTHOR_URL} target="_blank" rel="noreferrer">
            {AUTHOR}
          </a>
        </footer>
      </aside>

      <div className="app-main">
        <header className="topbar">
          <div>
            <p className="topbar-kicker">FundKit</p>
            <h1 className="topbar-title">{ROUTE_TITLES[route]}</h1>
          </div>

          <div className="topbar-actions">
            <span className="account-chip">
              <span className="account-avatar" aria-hidden="true">
                {(auth.user?.full_name ?? 'FK').slice(0, 1).toUpperCase()}
              </span>
              <span className="account-text">
                <span className="account-name">{auth.user?.full_name ?? 'Signed in'}</span>
                <span className="account-handle">
                  {auth.user ? `@${auth.user.username}` : ''}
                </span>
              </span>
            </span>

            <ThemeToggle />

            <button type="button" className="button button-ghost" onClick={refreshAll}>
              <RefreshCw size={15} />
              Refresh
            </button>

            <button type="button" className="button button-quiet" onClick={logout}>
              <LogOut size={15} />
              Log out
            </button>
          </div>
        </header>

        <main className="content" id="main">
          {children}
        </main>
      </div>
    </div>
  );
}
