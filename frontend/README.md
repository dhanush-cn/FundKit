# FundKit Control Center — Frontend

Engineered by **Dhanush C N** ([github.com/dhanush-cn](https://github.com/dhanush-cn))

React 19 + TypeScript + Vite dashboard for the FundKit order control plane.

## Structure

```
src/
├── lib/api.ts        typed fetch wrapper: auth header, ApiError, correlation id capture
├── lib/format.ts     currency, date and status formatting
├── lib/palette.ts    allocation-chart ramp, drawn from the three brand colours
├── types.ts          the API contract, mirrored from the Go handlers
├── hooks/
│   ├── useAuth.ts          session token lifecycle
│   ├── useOrders.ts        order book: polling, mutation, cancel, derived metrics
│   ├── usePortfolio.ts     P&L reads, with a distinct message for a 503 from the gRPC chain
│   └── useServiceHealth.ts aggregated stack status
├── router/           ~60-line hash router on useSyncExternalStore — no dependency
├── state/            the four polling hooks, instantiated once above the router
├── pages/
│   ├── Overview.tsx   read-only summary
│   ├── Orders.tsx     order desk: place, filter, sort, inspect, advance, cancel
│   ├── Portfolio.tsx  holdings, allocation ring, P&L
│   └── System.tsx     readiness probes, topology, request-id propagation
├── components/ui/    Panel, StatusBadge, Drawer, Donut, LifecycleTimeline, …
├── components/AppShell.tsx   everything that survives a route change
└── App.tsx           route table only — exhaustive over RoutePath, no default branch
```

Routing is by hash rather than history because the bundle is served behind the Go gateway, which
has no catch-all rewrite: a hard refresh on `/orders` would reach the router in Go and 404.

The polling hooks live above the router on purpose. Each owns a 15s interval, so mounting them
per page would restart every poll on navigation and let the overview's counters disagree with the
order book for a beat after each switch.

Every network call goes through `lib/api.ts`, so authentication, error shape and trace ids are
handled once. `ApiError` carries the HTTP status and the gateway's `x-request-id`, so a failure
shown in the UI can be matched to the exact backend log line.

## Commands

```bash
npm install
npm run dev        # http://localhost:5173, against a running gateway
npm run build
npm run lint
npm run build:demo # backend-free build → dist-demo/
```

`VITE_API_BASE_URL` points the dashboard at the gateway (default `http://localhost:8080`). Vite
inlines it at build time, so the Docker image takes it as a build argument.

## Design system

Three brand colours and nothing else: navy `#110E7A`, white `#FFFFFF`, gold `#FFBD00`, defined as
tokens in `src/index.css`.

- **Flat by rule.** Depth comes from four solid navy planes — sidebar, panel, page, hover — never a
  gradient or a translucent overlay. `grep -E '(linear|radial|conic)-gradient' src/*.css` returns
  nothing; a hit is a regression.
- **Gold is reserved.** It marks the primary action, the active nav item, or the focus ring. Nothing
  else earns it.
- **Two exceptions, on numbers only.** `--gain` and `--loss` are the sole non-brand hues, and they
  never touch a surface, a border or a control. A portfolio screen has to say which way a figure
  moved, and gold cannot carry that meaning while it is also the call to action.

## Hosted demo

`npm run build:demo` produces a self-contained build in `dist-demo/` that runs the real components
and the real state machine with `src/demo/main.tsx` standing in for the gateway. Order placement,
status transitions and cancellation all work against in-memory data — including the idempotency
guard, where a replayed key returns the original order instead of creating a second one.

It exists so the UI can be shown without provisioning Postgres, Redis and Kafka. `base: './'` in
`vite.demo.config.ts` keeps the asset URLs relative, so the output drops onto GitHub Pages, Netlify
or any static host under a project subpath without a rebuild. Nothing under `src/` imports it, and
`npm run build` still produces exactly the application bundle.
