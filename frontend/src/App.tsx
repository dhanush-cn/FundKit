// FundKit Control Center — application root and route table.
// Engineered by Dhanush C N (github.com/dhanush-cn)
import { AppShell } from './components/AppShell';
import { AuthPanel } from './components/AuthPanel';
import { useAuth } from './hooks/useAuth';
import { Orders } from './pages/Orders';
import { Overview } from './pages/Overview';
import { Portfolio } from './pages/Portfolio';
import { System } from './pages/System';
import { useRoute } from './router/route';
import { DashboardProvider } from './state/DashboardProvider';
import './App.css';

/**
 * The route table. Exhaustive over RoutePath, with no default branch — adding
 * a route to the union makes this switch a type error until the page exists,
 * which is the cheapest possible guard against a nav link that goes nowhere.
 */
function RouteView() {
  const route = useRoute();

  switch (route) {
    case '/orders':
      return <Orders />;
    case '/portfolio':
      return <Portfolio />;
    case '/system':
      return <System />;
    case '/':
      return <Overview />;
  }
}

export default function App() {
  const auth = useAuth();

  // The session gate sits above the provider so the polling hooks are never
  // mounted for a signed-out visitor: no interval, no 401 loop, no flash of an
  // empty dashboard behind the login card.
  if (!auth.token) {
    return <AuthPanel auth={auth} />;
  }

  return (
    <DashboardProvider auth={auth}>
      <AppShell>
        <RouteView />
      </AppShell>
    </DashboardProvider>
  );
}
