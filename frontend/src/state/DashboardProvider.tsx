// Engineered by Dhanush C N (github.com/dhanush-cn)
import { useCallback, useMemo } from 'react';
import type { ReactNode } from 'react';

import { useOrders } from '../hooks/useOrders';
import { usePortfolio } from '../hooks/usePortfolio';
import { useServiceHealth } from '../hooks/useServiceHealth';
import type { UseAuth } from '../hooks/useAuth';
import { DashboardContext, type DashboardValue } from './dashboard-context';

interface DashboardProviderProps {
  auth: UseAuth;
  children: ReactNode;
}

export function DashboardProvider({ auth, children }: DashboardProviderProps) {
  const isAuthenticated = Boolean(auth.token);

  const orders = useOrders({ enabled: isAuthenticated, onUnauthorized: auth.logout });
  const health = useServiceHealth(isAuthenticated);

  // The signed-in account is the subject of every screen here. The gateway
  // pins an order's user id to the verified token subject regardless, so
  // passing anything else would be a lie the backend would then correct.
  const signedInUserId = auth.user?.id ?? '';

  // There is deliberately no way to ask for someone else's portfolio: the id
  // is always the session's own. The backend enforces the same rule
  // independently, so this is defence in depth rather than the only guard.
  const portfolio = usePortfolio({
    enabled: isAuthenticated,
    userId: isAuthenticated ? signedInUserId : null,
    onUnauthorized: auth.logout,
  });

  const refreshAll = useCallback(() => {
    void orders.refresh();
    void health.refresh();
    void portfolio.refresh();
  }, [health, orders, portfolio]);

  const logout = useCallback(() => {
    auth.logout();
    portfolio.reset();
  }, [auth, portfolio]);

  const value = useMemo<DashboardValue>(
    () => ({ auth, orders, portfolio, health, refreshAll, logout }),
    [auth, orders, portfolio, health, refreshAll, logout],
  );

  return <DashboardContext.Provider value={value}>{children}</DashboardContext.Provider>;
}
