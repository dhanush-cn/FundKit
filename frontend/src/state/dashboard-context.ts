// FundKit Control Center — shared dashboard state.
// Engineered by Dhanush C N (github.com/dhanush-cn)
//
// The four data hooks each own a polling interval. Instantiating them inside a
// page component would mean every navigation tore down the interval and issued
// a fresh burst of requests, and the order count on the overview would
// disagree with the order book for a beat after switching. Hoisting them above
// the router and reading them through context keeps one poll per resource for
// the life of the session, whatever page is on screen.

import { createContext, useContext } from 'react';

import type { UseAuth } from '../hooks/useAuth';
import type { UseOrders } from '../hooks/useOrders';
import type { UsePortfolio } from '../hooks/usePortfolio';
import type { UseServiceHealth } from '../hooks/useServiceHealth';

export interface DashboardValue {
  auth: UseAuth;
  orders: UseOrders;
  portfolio: UsePortfolio;
  health: UseServiceHealth;
  /** Forces every resource to refetch now, ignoring where each interval sits. */
  refreshAll: () => void;
  logout: () => void;
}

export const DashboardContext = createContext<DashboardValue | null>(null);

export function useDashboard(): DashboardValue {
  const value = useContext(DashboardContext);
  if (!value) {
    throw new Error('useDashboard must be used inside <DashboardProvider>');
  }
  return value;
}
