// FundKit Control Center — public demo entry point.
// Engineered by Dhanush C N (github.com/dhanush-cn)
//
// This builds the hosted, backend-free build of the dashboard: the real
// components and the real state machine, with the gateway replaced by an
// in-browser stand-in. It is NOT part of the application — `src/main.tsx` is
// the entry the deployed app uses, and nothing under src/ imports this file.
//
// Mutations resolve locally so a visitor can actually operate the thing:
// placing, advancing and cancelling an order all work, they just never leave
// the tab. The idempotency guard is reproduced too, because a replayed key
// returning the original order is the single most interesting thing the write
// path does.
import React from 'react';
import ReactDOM from 'react-dom/client';

import App from '../App';
import '../index.css';

const DEMO_USER = {
  id: 'user-1',
  username: 'dhanush',
  email: 'dhanush@example.com',
  phone: '+919876543210',
  full_name: 'Dhanush C N',
};

function seed() {
  try {
    localStorage.setItem('fundkit_jwt', 'demo-session-token');
    localStorage.setItem('fundkit_user', JSON.stringify(DEMO_USER));
  } catch {
    /* private browsing — the demo still renders, it just starts signed out */
  }
}
seed();

const now = Date.now();
const iso = (minsAgo: number) => new Date(now - minsAgo * 60000).toISOString();

const orders = [
  { id: '9f2c1a84-7d33-4b21-9c10-aa5512bb7701', user_id: 'user-1', user_name: 'Dhanush C N', fund_id: 'quant-small-cap-fund', amount: 1500000, type: 'SIP', status: 'EXECUTED', idempotency_key: 'b41f7a2c-55de-4c9a-8f21-9ac3e1d20b44', created_at: iso(180), updated_at: iso(176) },
  { id: '2c7b9e10-1f45-4a88-b3d2-11ee44aa9902', user_id: 'user-1', user_name: 'Dhanush C N', fund_id: 'parag-parikh-flexi-cap', amount: 5000000, type: 'LUMPSUM', status: 'EXECUTED', idempotency_key: 'de91c034-2b77-41a5-9d18-70bb2c4f8813', created_at: iso(140), updated_at: iso(138) },
  { id: '55aa0311-9c62-4d70-8e19-3fb7712c0d55', user_id: 'user-1', user_name: 'Dhanush C N', fund_id: 'hdfc-mid-cap-opportunities', amount: 2500000, type: 'SIP', status: 'PROCESSING', idempotency_key: '7c2e5580-4a19-4f63-b0aa-6d1195e37722', created_at: iso(42), updated_at: iso(9) },
  { id: '81de44a0-3b17-4f92-a5c8-00cc19ee4413', user_id: 'user-1', user_name: 'Dhanush C N', fund_id: 'icici-bluechip-fund', amount: 750050, type: 'SIP', status: 'PENDING', idempotency_key: 'a0f31d9e-8c44-4b12-9e77-2f6633aa5591', created_at: iso(11), updated_at: iso(11) },
  { id: '6b3f2205-77aa-4e18-bc31-9911ddaa6624', user_id: 'user-1', user_name: 'Dhanush C N', fund_id: 'axis-small-cap-fund', amount: 1000000, type: 'LUMPSUM', status: 'FAILED', idempotency_key: 'ff21b7c5-9d03-4a6e-81b2-5c4477de1190', created_at: iso(320), updated_at: iso(318) },
  { id: '4a9c7731-6e20-4c55-90fd-88bb22cc3345', user_id: 'user-1', user_name: 'Dhanush C N', fund_id: 'quant-small-cap-fund', amount: 1500000, type: 'SIP', status: 'EXECUTED', idempotency_key: '1b8d44f7-0a95-4e31-b6c2-33aa9911ee08', created_at: iso(1580), updated_at: iso(1576) },
];

const pnl = {
  user_id: 'user-1',
  total_value: 104960.15,
  total_unrealized_gain: 9960.15,
  holdings: [
    { fund_id: 'parag-parikh-flexi-cap', fund_name: 'Parag Parikh Flexi Cap', units: 612.443, invested_amount: 50000, nav: 81.66, current_value: 50012.09, unrealized_gain: 12.09 },
    { fund_id: 'quant-small-cap-fund', fund_name: 'Quant Small Cap Fund', units: 128.902, invested_amount: 30000, nav: 291.4, current_value: 37561.04, unrealized_gain: 7561.04 },
    { fund_id: 'hdfc-mid-cap-opportunities', fund_name: 'HDFC Mid-Cap Opportunities', units: 91.204, invested_amount: 15000, nav: 190.65, current_value: 17388.02, unrealized_gain: 2388.02 },
  ],
};

const health = {
  status: 'DEGRADED',
  services: [
    { service: 'api-gateway', url: 'http://localhost:8080/readyz', status: 'UP', latency: '3ms' },
    { service: 'order-service', url: 'http://order-service:8081/readyz', status: 'UP', latency: '11ms' },
    { service: 'portfolio-service', url: 'http://portfolio-service:8082/readyz', status: 'UP', latency: '7ms' },
    { service: 'notification-service', url: 'http://notification-service:8083/readyz', status: 'DOWN', error: 'kafka consumer group rebalancing' },
  ],
};

const reply = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: {
      'content-type': 'application/json',
      'x-request-id': '7f3a11c9-4e02-4b8d-9a01-5cc7de210b93',
    },
  });

window.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(
    typeof input === 'string' ? input : input instanceof URL ? input.href : input.url,
  );
  const method = (init?.method ?? 'GET').toUpperCase();
  // A little latency, so the loading and disabled states are visible rather
  // than resolving before the first paint.
  await new Promise((resolve) => setTimeout(resolve, 180));

  if (url.includes('/services/health')) return reply(health);
  if (url.includes('/pnl')) return reply(pnl);
  if (url.includes('/auth/login') || url.includes('/auth/register')) {
    seed();
    return reply({
      token: 'demo-session-token',
      user_id: DEMO_USER.id,
      expires_at: new Date(now + 864e5).toISOString(),
      user: DEMO_USER,
    });
  }

  const orderMatch = /\/orders\/([^/?]+)/.exec(url);
  if (orderMatch) {
    const id = decodeURIComponent(orderMatch[1]);
    const index = orders.findIndex((order) => order.id === id);
    if (index === -1) return reply({ error: 'order not found' }, 404);
    if (method === 'DELETE') {
      orders.splice(index, 1);
      return reply(null);
    }
    if (method === 'PATCH') {
      const body = init?.body ? JSON.parse(String(init.body)) : {};
      orders[index] = { ...orders[index], status: body.status, updated_at: new Date().toISOString() };
      return reply(orders[index]);
    }
    return reply(orders[index]);
  }

  if (url.includes('/orders')) {
    if (method === 'POST') {
      const body = JSON.parse(String(init?.body ?? '{}'));
      const existing = orders.find((order) => order.idempotency_key === body.idempotency_key);
      if (existing) {
        // The guard order-service enforces in Redis: a replayed key returns
        // the original order rather than creating a second one.
        return reply(existing);
      }
      const created = {
        ...body,
        id: crypto.randomUUID(),
        user_name: DEMO_USER.full_name,
        status: 'PENDING',
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      };
      orders.unshift(created);
      return reply(created);
    }
    return reply(orders);
  }

  return reply({});
}) as typeof fetch;

// Demo-only styling, kept out of App.css so the application's stylesheet
// carries nothing that only exists for the hosted build.
const style = document.createElement('style');
style.textContent = `
.demo-flag {
  position: fixed;
  right: 16px;
  bottom: 16px;
  z-index: 40;
  max-width: min(340px, calc(100vw - 32px));
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 11px 14px;
  border: 1px solid var(--gold);
  border-radius: var(--radius);
  background-color: var(--surface-sunken);
  box-shadow: var(--shadow);
}
.demo-flag-tag {
  flex: none;
  margin-top: 1px;
  padding: 2px 7px;
  border-radius: 4px;
  background-color: var(--gold);
  color: var(--accent-ink);
  font-size: 10px;
  font-weight: 800;
  letter-spacing: 0.08em;
}
.demo-flag-copy {
  margin: 0;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.5;
}
@media (max-width: 560px) {
  .demo-flag { left: 16px; right: 16px; max-width: none; }
}
`;
document.head.appendChild(style);

function DemoFlag() {
  return (
    <aside className="demo-flag">
      <span className="demo-flag-tag">DEMO</span>
      <p className="demo-flag-copy">
        Sample data, served from the browser — no gateway or Kafka behind it. Every control works;
        nothing is a real order or holding.
      </p>
    </aside>
  );
}

const container = document.getElementById('root');
if (!container) {
  throw new Error('FundKit demo: #root element is missing from demo.html');
}

ReactDOM.createRoot(container).render(
  <React.StrictMode>
    <App />
    <DemoFlag />
  </React.StrictMode>,
);
