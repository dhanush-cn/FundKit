# FundKit Control Center — Frontend

Engineered by **Dhanush C N** ([github.com/dhanush-cn](https://github.com/dhanush-cn))

React 19 + TypeScript + Vite dashboard for the FundKit order control plane.

## Structure

```
src/
├── lib/api.ts        typed fetch wrapper: auth header, ApiError, correlation id capture
├── lib/format.ts     currency, date and status formatting
├── types.ts          the API contract, mirrored from the Go handlers
├── hooks/
│   ├── useAuth.ts          session token lifecycle
│   ├── useOrders.ts        order book: polling, mutation, derived metrics, abort on unmount
│   ├── usePortfolio.ts     P&L reads, with a distinct message for a 503 from the gRPC chain
│   └── useServiceHealth.ts aggregated stack status
├── components/ErrorBoundary.tsx
└── App.tsx           presentation only — no fetch calls
```

Every network call goes through `lib/api.ts`, so authentication, error shape and trace ids are
handled once. `ApiError` carries the HTTP status and the gateway's `x-request-id`, so a failure
shown in the UI can be matched to the exact backend log line.

## Commands

```bash
npm install
npm run dev      # http://localhost:5173
npm run build
npm run lint
```

`VITE_API_BASE_URL` points the dashboard at the gateway (default `http://localhost:8080`). Vite
inlines it at build time, so the Docker image takes it as a build argument.
