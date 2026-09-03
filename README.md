# FundKit — Distributed Mutual Fund & SIP Order Processing Platform

[![CI](https://github.com/dhanush-cn/fundkit/actions/workflows/ci.yml/badge.svg)](https://github.com/dhanush-cn/fundkit/actions/workflows/ci.yml)

**Engineered and Developed by Dhanush C N** · [github.com/dhanush-cn](https://github.com/dhanush-cn)

FundKit is an event-driven trading back office for mutual fund and SIP orders, written in Go.
Four independently deployable services move an order from an authenticated HTTP request to a
durable state transition, a real-time valuation and an asynchronous customer notification —
with the correlation id, idempotency guarantees and shutdown semantics that a money-moving
system actually needs.

Module path: `github.com/dhanush-cn/fundkit`

---

## Architecture

```
                    ┌──────────────────────────┐
                    │   React + TypeScript UI  │
                    │  FundKit Control Center  │
                    └────────────┬─────────────┘
                                 │ HTTPS / JSON
                                 │ x-request-id minted here
                                 ▼
                    ┌──────────────────────────┐
                    │       API GATEWAY        │   :8080
                    │  JWT auth · rate limit   │
                    │  CORS · reverse proxy    │
                    └────────────┬─────────────┘
                                 │ HTTP (keep-alive pool)
                                 │ x-request-id forwarded
                                 ▼
        ┌────────────────────────────────────────────────┐
        │                 ORDER SERVICE                  │   :8081
        │  order state machine · idempotency · outbound  │
        │  PENDING → PROCESSING → EXECUTED / FAILED      │
        └───┬───────────────┬──────────────┬─────────────┘
            │               │              │
   gRPC     │        SQL    │       Redis  │        Kafka produce
   (unary,  │    (Postgres) │   (SETNX     │     (keyed by order id,
   deadline)│               │  reservation)│      x-request-id header)
            ▼               ▼              ▼              │
  ┌───────────────────┐  ┌──────────┐  ┌─────────┐        │
  │ PORTFOLIO SERVICE │  │ PostgreSQL│ │  Redis  │        │
  │  :50051 gRPC      │  │  orders   │ │ idem +  │        │
  │  :8082 probes     │  └──────────┘  │ NAV TTL │        │
  │  NAV cache-aside  │◄───────────────┴─────────┘        │
  │  P&L valuation    │                                    │
  └───────────────────┘                                    ▼
                                              ┌────────────────────────┐
                                              │  APACHE KAFKA          │
                                              │  topic: order_events   │
                                              │  partitioned by order  │
                                              └───────────┬────────────┘
                                                          │ consumer group
                                                          │ manual offset commit
                                                          ▼
                                              ┌────────────────────────┐
                                              │ NOTIFICATION SERVICE   │  :8083
                                              │ email + SMS fan-out    │
                                              │ dedupe on event_id     │
                                              └────────────────────────┘
```

**Request path**: `Client → API Gateway (HTTP/JWT) → Order Service (HTTP) → Portfolio Service (gRPC)`
**Event path**: `Order Service → Kafka order_events → Notification Worker`

The synchronous path and the asynchronous path are deliberately separate. A read that a user is
waiting on takes the gRPC hop; a state change that other parts of the system need to know about
becomes an event. Nothing in the write path blocks on a notification being delivered.

---

## Why this stack

**Go.** The workload is thousands of concurrent, mostly-IO-bound requests fanning out to a
database, a cache, a gRPC peer and a broker. Goroutines make that fan-out cheap to express and
cheap to run, and `context.Context` gives cancellation and deadlines a first-class place in every
signature. Static binaries with no runtime dependency also make the container images small
(~15 MB) and the Kubernetes rollout boring, which is the correct thing for a rollout to be.

**gRPC for internal RPC.** `order-service → portfolio-service` is a synchronous read on a hot
path. Protobuf gives a versioned, compile-time-checked contract and generated clients, HTTP/2
multiplexes calls over one connection, and deadlines propagate through the wire protocol rather
than being reinvented per client. REST is kept at the edge, where the client is a browser and
human debuggability matters more than encoding efficiency.

**Apache Kafka for events.** Order status changes have more than one interested party and the
list will grow — notifications today, ledger and analytics tomorrow. A durable, replayable log
decouples the producer from that list entirely: order-service does not know who consumes
`order_events`, and a consumer that was down for an hour catches up from its committed offset
instead of losing an hour of history. Keying by order id keeps all events for one order on one
partition, so per-order ordering survives horizontal scaling.

**PostgreSQL per service.** The order table is the system of record for money movements, so it
gets ACID transactions and a unique index on the idempotency key. Services do not read each
other's tables; they ask over gRPC or subscribe to events.

**Redis.** Two jobs, both a natural fit: an atomic `SETNX` reservation that stops a retried
submission from becoming a second real order, and a short-TTL cache in front of the NAV feed,
which is read on every valuation but changes once a day.

---

## Design tradeoffs

| Decision | Chosen | Rejected alternative | Reasoning |
|---|---|---|---|
| Internal transport | gRPC (unary, deadline-bound) | JSON over REST | Schema enforcement, generated clients and native deadline propagation on a hot read path. REST stays at the browser edge. |
| Status propagation | Kafka events | Synchronous callbacks | Adding a consumer must not mean editing the producer, and a consumer being down must not fail an order. |
| Duplicate protection | Redis `SETNX` **and** a unique DB index | Redis alone | Redis is the fast guard; the unique constraint is the authority. A cache eviction cannot become a double purchase. |
| Event ordering | Partition key = order id | Round-robin partitioning | Guarantees per-order ordering while still allowing the consumer group to scale out. |
| Consumer offsets | Manual commit after handling | Auto-commit | At-least-once with real redelivery. Handlers deduplicate on `event_id` to make reprocessing safe. |
| Data ownership | A database per service | One shared schema | Independent deploys and schema evolution; no service is coupled to another's table layout. |
| Rate limiting | In-process token bucket per pod | Redis-backed global quota | Zero added latency for coarse abuse protection. The tradeoff is accepted and documented: N replicas allow N× the configured rate. |
| Gateway readiness | Ready when the identity store is reachable, regardless of upstream health | Readiness follows every dependency | One restarting downstream service must not take the entire edge out of rotation — the gateway can still authenticate callers and return honest 502s. Postgres is the exception: without it nobody can sign in at all. |
| Identity | Owned by the gateway, verified once at the edge | Each service validates the token | Only one component ever sees a password or a signing key. Downstream services trust the `x-fundkit-user-*` headers, which the gateway strips from the inbound request before setting them. |
| Password storage | bcrypt with a configurable cost | SHA-256, or a salted digest of my own | The work factor is the mechanism: it makes each guess expensive. Rolling your own is how password databases get cracked in an afternoon. |
| Customer contact details | Copied onto the order and carried on the event | Looked up from identity when an alert is sent | Notification delivery stays fully asynchronous: identity being slow or down cannot stall the alert pipeline. The cost is a snapshot that goes stale if the customer edits their profile, which is acceptable for addressing a message rather than authorising one. |
| Order lifecycle worker | Goroutine on a detached context | Request-scoped goroutine | `context.WithoutCancel` keeps the correlation id while dropping the caller's cancellation, so an order is not abandoned mid-transition when the client hangs up. |
| Money representation | `float64` for display valuations | Integer minor units everywhere | Honest scoping: NAV valuations are presentational. A production ledger would use integer paise end to end. |

---

## Distributed tracing without a tracing backend

Every request carries one correlation id (`x-request-id`) across every transport:

1. **API gateway** adopts the inbound header or mints a 128-bit id.
2. It travels to order-service as an **HTTP header**.
3. It travels to portfolio-service as **gRPC metadata** (client interceptor out, server interceptor in).
4. It travels to notification-service as a **Kafka message header**, so the async half of a request stays joined to the sync half.
5. The asynchronous lifecycle worker inherits it through `context.WithoutCancel`.

A custom `slog.Handler` reads the id off the context and stamps it onto every JSON log record, so
no call site has to remember to attach it:

```json
{"time":"2026-09-02T10:14:22Z","level":"INFO","msg":"order placed","service":"order-service",
 "x-request-id":"9f2c1e5a4b7d8c3e1f0a6b2d5c8e7f31","order_id":"6b7e…","amount":5000}
```

`grep` one id across all four services and you have the whole request.

---

## Project layout

Each service follows the same layered Go layout, so moving between them requires no re-orientation:

```
<service>/
├── cmd/server/main.go          # composition root: config → deps → server → graceful shutdown
├── internal/
│   ├── config/                 # typed env parsing, FUNDKIT_ prefixed, fail-fast validation
│   ├── handler/                # transport adapters (HTTP routes, gRPC servers, middleware)
│   ├── service/                # use cases; depends only on interfaces declared here
│   ├── repository/             # persistence adapters (Postgres, in-memory stores)
│   ├── cache/  messaging/      # Redis and Kafka adapters
│   ├── domain/                 # entities and rules; no framework imports
│   └── platform/logging|trace/ # structured logging and correlation-id plumbing
└── pb/                         # generated protobuf stubs
```

Dependencies point inward: `handler → service → domain`. The service layer declares the interfaces
it needs (`Repository`, `EventPublisher`, `IdempotencyStore`) and the adapters satisfy them, so the
business rules can be tested with fakes and never import gorm, redis or kafka.

```
├── api-gateway/            edge: JWT, rate limiting, CORS, reverse proxy, health aggregation
├── order-service/          order state machine, idempotency, Kafka producer, gRPC client
├── portfolio-service/      gRPC valuation API, NAV cache-aside, P&L arithmetic
├── notification-service/   Kafka consumer group, alert fan-out
├── frontend/               React + TypeScript dashboard
├── proto/fundkit.proto     internal service contract
├── k8s/                    deployments with liveness/readiness probes and resource limits
└── docker-compose.yml      full local stack
```

---

## Production practices baked in

- **Graceful shutdown everywhere.** `SIGINT`/`SIGTERM` → `signal.NotifyContext` → stop accepting
  work → drain in-flight requests → wait for background workers → close dependencies. The gRPC
  server falls back to a forced stop if a call outlives the grace period.
- **Liveness vs readiness.** `/healthz` says the process is alive; `/readyz` actually probes
  Postgres, Redis or the Kafka consumer loop. Conflating them turns a slow dependency into a
  restart loop.
- **Structured JSON logging** via `log/slog` with a context-aware handler — no `log.Println`
  anywhere in the codebase.
- **Bounded everything.** Server read/write/idle timeouts, per-request context deadlines, gRPC
  call deadlines, connection pool limits, and a rate-limiter janitor so the per-IP map cannot grow
  without bound.
- **Fail-fast configuration.** The gateway refuses to start without an explicit `FUNDKIT_JWT_SECRET`;
  a default signing key is a default that reaches production.
- **Hardened containers.** Multi-stage builds, non-root user, read-only root filesystem in
  Kubernetes, cached dependency layer.

---

## Configuration

Every variable is namespaced `FUNDKIT_`. The pre-refactor names (`PORT`, `DB_URL`, …) are still
read as a fallback so a running deployment can migrate without a flag day. See
[`.env.example`](.env.example) for the full reference.

| Variable | Service | Default |
|---|---|---|
| `FUNDKIT_HTTP_PORT` | all | 8080 / 8081 / 8082 / 8083 |
| `FUNDKIT_LOG_LEVEL` | all | `info` |
| `FUNDKIT_SHUTDOWN_TIMEOUT` | all | `15s` |
| `FUNDKIT_JWT_SECRET` | api-gateway | **required**, ≥16 chars |
| `FUNDKIT_JWT_TTL` | api-gateway | `24h` |
| `FUNDKIT_BCRYPT_COST` | api-gateway | `10` |
| `FUNDKIT_ORDER_SERVICE_URL` | api-gateway | `http://localhost:8081` |
| `FUNDKIT_RATE_LIMIT_RPS` / `_BURST` | api-gateway | `5` / `10` |
| `FUNDKIT_DB_URL` *(or `FUNDKIT_DB_HOST`, `_PORT`, `_USER`, `_PASSWORD`, `_NAME`)* | api-gateway, order-service | **required** |
| `FUNDKIT_REDIS_URL` | order, portfolio | `localhost:6379` |
| `FUNDKIT_KAFKA_BROKERS` | order, notification | `localhost:9092` |
| `FUNDKIT_IDEMPOTENCY_TTL` | order-service | `24h` |
| `FUNDKIT_PORTFOLIO_GRPC_URL` | order-service | `localhost:50051` |
| `FUNDKIT_NAV_CACHE_TTL` | portfolio-service | `30s` |
| `FUNDKIT_KAFKA_CONSUMER_GROUP` | notification-service | `fundkit-notification-workers` |

---

## Running the stack

Prerequisite: Docker and Docker Compose. Nothing else needs to be installed.

```bash
git clone https://github.com/dhanush-cn/fundkit
cd fundkit
docker compose up -d --build
```

Then open **http://localhost:5173**.

| Endpoint | URL |
|---|---|
| Dashboard | http://localhost:5173 |
| API gateway | http://localhost:8080 |
| Gateway liveness / readiness | `/healthz`, `/readyz` |
| Aggregated stack health | http://localhost:8080/services/health |
| Portfolio gRPC | `localhost:50051` |
| Prometheus / Grafana | http://localhost:9090 · http://localhost:3000 |

### Running a single service locally

```bash
cd order-service
FUNDKIT_DB_URL='postgres://fundkit:password@localhost:5433/fundkit_db?sslmode=disable' \
FUNDKIT_REDIS_URL=localhost:6380 \
FUNDKIT_KAFKA_BROKERS=localhost:29092 \
go run ./cmd/server
```

### Tests

Unit tests need nothing running — every external dependency sits behind an interface with an
in-memory fake:

```bash
make test          # go test ./... across all four modules
make test-frontend # tsc, eslint and vitest for the dashboard
```

Integration tests are guarded by a build tag, so they never slow down the unit run. They exercise
the guarantees a fake cannot prove: the unique indexes that backstop idempotency and account
uniqueness, the `SETNX` reservation under real concurrency, and a Kafka round trip.

```bash
make test-integration   # starts postgres, redis and kafka, then runs the tagged suites
```

Or by hand, against a stack you already have running:

```bash
export FUNDKIT_TEST_DB_URL='postgres://fundkit:password@localhost:5433/fundkit_db?sslmode=disable'
export FUNDKIT_TEST_REDIS_URL=localhost:6380
export FUNDKIT_TEST_KAFKA_BROKERS=localhost:29092
cd order-service && go test -tags=integration ./...
```

Each integration suite skips itself when its environment variable is unset, so `go test ./...`
stays green on a laptop with nothing running.

### Continuous integration

[`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs on every push and pull request:

| Job | What it proves |
|---|---|
| **Go** (matrix over all four services) | `gofmt` clean, `go vet` clean, builds, and `go test -race` passes with a coverage report per module |
| **Frontend** | TypeScript type check, ESLint, Vitest with V8 coverage, and a production Vite build |
| **Integration** | Brings up Postgres, Redis and Kafka with the project's own compose file, waits for health, then runs the `integration`-tagged suites |
| **Images** | `docker compose build` for every service, so a Dockerfile can never rot unnoticed |

The Go job is a matrix rather than a loop on purpose: a failure names the service that broke
instead of stopping at the first one.

### Regenerating the protobuf stubs

```bash
protoc --proto_path=proto \
       --go_out=order-service/pb      --go_opt=paths=source_relative \
       --go-grpc_out=order-service/pb --go-grpc_opt=paths=source_relative \
       proto/fundkit.proto
```

---

## Walking through the system

1. **Create an account.** The dashboard posts a username, password, full name, email and phone to
   `POST /auth/register`. The gateway validates the input, hashes the password with bcrypt, stores
   the account in Postgres and returns a session — registering signs you in, so there is no second
   round trip. `POST /auth/login` verifies a returning user against the stored hash and issues an
   HS256 JWT carrying the subject *and* the contact details.
2. **Place an order.** The gateway verifies the token, applies the rate limit, strips any
   identity headers the client tried to set, stamps its own, and proxies to order-service. The
   verified subject — not the request body — decides whose order it is. order-service claims the
   idempotency key in Redis, inserts the row with the customer's contact details attached, and
   publishes `order.status_changed`. Submitting the same idempotency key twice returns `409`.
3. **Watch the lifecycle.** A background worker advances `PENDING → PROCESSING → EXECUTED`,
   publishing an event at each hop. Every write is a conditional `UPDATE … WHERE status = ?`, so
   the worker and a concurrent operator action cannot corrupt each other.
4. **See the notifications.** `docker compose logs -f notification-service` — the email channel
   addresses the email you registered with and the SMS channel your phone number, and each alert
   carries the same `x-request-id` as the HTTP request that created the order. A channel with no
   address on file is skipped rather than failing the event.
5. **Fetch P&L.** Enter a user id (`user-1` or `user-2`) and fetch. order-service calls
   portfolio-service over gRPC; portfolio-service serves NAV from Redis, falling back to the feed
   on a miss.
6. **Trace it.** Take the `x-request-id` from any response header and grep it across
   `docker compose logs`.

```bash
docker compose down          # stop
docker compose down -v       # stop and drop the Postgres volume
```

---

## Known limits

Stated plainly, because knowing where a system stops is part of designing it:

- **No transactional outbox.** The database commit and the Kafka publish are separate operations;
  a crash between them loses an event. The fix is an outbox table drained by a relay, which is the
  natural next iteration.
- **Holdings are in-memory** in portfolio-service. The valuation logic and the cache-aside path
  are real; the ledger behind them is a stub with a repository seam ready for Postgres.
- **Rate limiting is per replica**, not global — see the tradeoff table.
- **Auth stops short of a full identity product.** Accounts are real — bcrypt-hashed passwords in
  Postgres, algorithm-pinned expiring tokens, uniqueness enforced by database indexes — but there
  is no refresh flow, no password reset, no email or phone verification, and no roles. A token is
  valid until it expires; there is no revocation list.
- **Notification channels are simulated.** `LogChannel` emits a structured record instead of
  calling SES or Twilio. The `Channel` interface is the seam: a real provider is an adapter, not a
  change to the notifier.
- **Metrics are not wired.** Prometheus and Grafana are in the compose file; `/metrics` endpoints
  are the next thing to add.

---

Engineered and developed by **Dhanush C N** — [github.com/dhanush-cn](https://github.com/dhanush-cn)
