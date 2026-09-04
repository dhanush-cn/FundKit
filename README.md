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
                                                  ┌────────┴────────┐
                                     consumer group:         consumer group:
                                fundkit-portfolio-       fundkit-notification-
                                       workers                   workers
                                          │                         │
                                          ▼                         ▼
                              ┌────────────────────────┐  ┌────────────────────────┐
                              │ PORTFOLIO SERVICE       │  │ NOTIFICATION SERVICE   │  :8083
                              │ (same process as above) │  │ email + SMS fan-out    │
                              │ builds holdings from    │  │ dedupe on event_id     │
                              │ EXECUTED fills, dedupe  │  └────────────────────────┘
                              │ on event_id, DLQ on     │
                              │ permanent/exhausted     │
                              │ failure                 │
                              └────────────────────────┘
```

**Request path**: `Client → API Gateway (HTTP/JWT) → Order Service (HTTP) → Portfolio Service (gRPC)`
**Event path**: `Order Service → Kafka order_events → {Portfolio Service ledger, Notification Worker}` —
two independent consumer groups on the same topic, so a notification outage can never stall the
ledger, and a NAV feed outage stalling the ledger can never delay a notification.

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

**Apache Kafka for events.** Order status changes have more than one interested party — customer
notifications and the portfolio ledger both need to know today, and analytics will want the same
feed tomorrow. A durable, replayable log decouples the producer from that list entirely:
order-service does not know who consumes `order_events`, and a consumer that was down for an hour
catches up from its committed offset instead of losing an hour of history. Each consumer runs its
own consumer group and its own dead-letter topic, so notification-service and portfolio-service
read every event independently — one falling behind or failing cannot stall the other. Keying by
order id keeps all events for one order on one partition, so per-order ordering survives horizontal
scaling.

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
| Ledger writes (portfolio-service) | Idempotent apply, under the same lock as the mutation, keyed on `event_id` | Trusting commit ordering alone | At-least-once delivery means a crash between applying a fill and committing its offset redelivers that event. Applying a `BUY` twice would double a customer's position, so the safety property lives in the ledger, not the transport — a duplicate is detected and skipped before it touches state. |
| Data ownership | A database per service | One shared schema | Independent deploys and schema evolution; no service is coupled to another's table layout. |
| Rate limiting | In-process token bucket per pod | Redis-backed global quota | Zero added latency for coarse abuse protection. The tradeoff is accepted and documented: N replicas allow N× the configured rate. |
| Gateway readiness | Ready when the identity store is reachable, regardless of upstream health | Readiness follows every dependency | One restarting downstream service must not take the entire edge out of rotation — the gateway can still authenticate callers and return honest 502s. Postgres is the exception: without it nobody can sign in at all. |
| Identity | Owned by the gateway, verified once at the edge | Each service validates the token | Only one component ever sees a password or a signing key. Downstream services trust the `x-fundkit-user-*` headers, which the gateway strips from the inbound request before setting them. |
| Password storage | bcrypt with a configurable cost | SHA-256, or a salted digest of my own | The work factor is the mechanism: it makes each guess expensive. Rolling your own is how password databases get cracked in an afternoon. |
| Customer contact details | Copied onto the order and carried on the event | Looked up from identity when an alert is sent | Notification delivery stays fully asynchronous: identity being slow or down cannot stall the alert pipeline. The cost is a snapshot that goes stale if the customer edits their profile, which is acceptable for addressing a message rather than authorising one. |
| Order lifecycle worker | Goroutine on a detached context | Request-scoped goroutine | `context.WithoutCancel` keeps the correlation id while dropping the caller's cancellation, so an order is not abandoned mid-transition when the client hangs up. |
| Money representation | **Chosen:** integer paise (`int64`) in the domain, the database and on the wire | **Rejected:** `float64` rupees | `0.1 + 0.2 != 0.3` in IEEE-754, so a float ledger drifts by a few paise per reconciliation and nobody can reproduce the complaint. The column is `BIGINT`, the JSON field is an integer, and a client sending the decimal `100.50` gets a 400 rather than a silent truncation. Units and NAV are the one deliberate exception and stay `float64` — a unit count is genuinely fractional and a NAV is a quoted price, so neither one is money. |
| Money on the gRPC contract | Converted to `double` at the portfolio boundary | Changing the `.proto` to `int64` in the same commit | The internals are exact either way. The `.proto` is a published contract, so changing its units is a breaking release of its own rather than a side effect of an internal refactor. The conversion is confined to two functions in `grpc_server.go`. |
| Unprocessable events | Retry three times, then park on a per-consumer DLQ topic (`order_events_dlq` for notifications, `order_events_portfolio_dlq` for the ledger) | Retry forever, commit and drop, or one shared DLQ | Retrying forever stalls the partition and every message behind it; dropping loses a customer's event with only a log line as evidence. A shared DLQ would mean replaying a parked event hands it to both consumers again, so fixing the ledger could re-send a customer's email. Parking keeps the partition moving and turns "things needing a human" into a queue with a depth you can alert on. The offset is committed only after the park succeeds — if the DLQ write fails, the consumer would rather be stuck and visible than moving and lossy. |
| Retry classification | By error type (`domain.ErrPermanent`), not by attempt count | A flat retry budget for every failure | A provider timeout deserves several attempts; an unreadable schema version deserves none, because every attempt fails identically while the partition waits. |
| Schema management | Versioned `.sql` files applied by golang-migrate before boot | GORM `AutoMigrate` on startup | AutoMigrate is invisible to review, unordered, raced by replicas, never drops or narrows a column, cannot express a `CHECK` constraint or a partial index, and has no inverse. order-service now *verifies* the schema version on boot and refuses to start if it is behind — a missing migration is a container that will not start, not a 500 on the first request that touches a missing column. |

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
 "x-request-id":"9f2c1e5a4b7d8c3e1f0a6b2d5c8e7f31","order_id":"6b7e…","amount_paise":500000,"amount":"₹5,000.00"}
```

`grep` one id across all four services and you have the whole request.

---

## Metrics, dashboards and alerts

Tracing answers "what happened to *this* request". Metrics answer "is the system healthy right
now" — and they are the half you can alert on, because a metric is a number over time rather than a
line of text you have to go looking for.

Every service exposes Prometheus metrics on a **dedicated admin listener** (`:9100`, `/metrics`),
separate from the port that serves customer traffic. That separation is deliberate:

- a scrape must not traverse JWT auth, CORS or the per-client rate limiter, and adding exceptions
  to a security chain to let a scraper through is how those chains rot;
- `/metrics` stays off the internet, because the admin port is never published through the ingress;
- a slow scrape cannot consume a connection slot on the public server, so observability can never
  be the thing that takes the API down.

### What is instrumented

| Metric | Type | Labels | Where |
|---|---|---|---|
| `http_requests_total` | counter | `method`, `path`, `status_code` | api-gateway, order-service |
| `http_request_duration_seconds` | histogram | `method`, `path` | api-gateway, order-service |
| `http_requests_in_flight` | gauge | — | api-gateway, order-service |
| `grpc_server_requests_total` | counter | `grpc_service`, `grpc_method`, `grpc_code` | portfolio-service |
| `grpc_server_request_duration_seconds` | histogram | `grpc_service`, `grpc_method` | portfolio-service |
| `grpc_server_requests_in_flight` | gauge | — | portfolio-service |
| `kafka_events_published_total` | counter | `topic`, `status` | order-service |
| `kafka_events_consumed_total` | counter | `topic`, `status` | portfolio-service, notification-service |
| `kafka_event_processing_duration_seconds` | histogram | `topic` | portfolio-service, notification-service |
| `kafka_event_retries_total` | counter | `topic` | portfolio-service, notification-service |
| `kafka_events_dead_lettered_total` | counter | `topic`, `reason` | portfolio-service, notification-service |
| `kafka_dlq_publish_failures_total` | counter | `topic` | portfolio-service, notification-service |
| `kafka_consumer_lag` | gauge | `topic`, `group` | portfolio-service, notification-service |

`status` on `kafka_events_consumed_total` is not identical across the two consumers, and a query
that sums it needs `job` (or `group`) in its `by (...)` clause now that two services read the same
topic — see [`monitoring/PROMQL.md`](monitoring/PROMQL.md) for the corrected queries. notification-service
uses `success` / `error` / `dropped` / `dead_lettered`; portfolio-service uses `success` / `skipped` /
`error` / `dead_lettered` — `skipped` is the (very common) case of an event on the topic that never
reached `EXECUTED` and so never touched a position, which notification-service has no equivalent for
since it acts on every terminal transition.

Plus the standard Go runtime and process collectors on every service: goroutines, heap, GC pause,
open file descriptors, CPU seconds.

### Four decisions worth defending in an interview

**The route label is the template, never the raw path.** `/orders/:id`, not
`/orders/9f3c1e5a…`. A raw path turns every order id into its own time series, and unbounded label
cardinality is the single most common way a Prometheus install falls over. Unmatched requests
collapse into one `unmatched` bucket so a 404 scan cannot mint a series per URL either.

**Latency is a histogram, not a summary.** Summaries compute quantiles inside each process and
cannot be aggregated, so a p99 across four replicas would be arithmetically meaningless. Histograms
ship buckets and let `histogram_quantile()` do the maths across the whole fleet at query time. All
four services share one bucket layout, which is what lets a single panel compare an HTTP hop against
a gRPC hop.

**The metrics middleware sits outside the recovery handler.** A panic unwinds through every
middleware registered *below* the recovery handler before `recover()` runs, so an inner observer
would record the status code as it stood before the 500 was written — and panics would quietly
count as successes. Placed outside, `c.Next()` returns with the 500 already set. The same argument
puts the gRPC interceptor outside the server-side timeout, so an RPC killed by its own deadline is
counted with code `DeadlineExceeded` rather than not counted at all.

**Consumer lag is a collector, not a polled gauge.** The value is read from the kafka-go reader
during the scrape itself. A polled gauge is stale by up to one poll interval and keeps cheerfully
reporting its last value after the reader dies; a collector that reads on demand simply stops
reporting, which is the honest answer.

And the reason lag is instrumented at all: throughput hides backlog. A worker handling 500 events/s
looks perfectly healthy right until you notice the producer is doing 700/s and the backlog is an
hour deep. On portfolio-service specifically, lag is not just an infrastructure number — it is how
stale the holdings a customer is currently looking at might be, since the dashboard reflects
whatever the ledger has consumed so far.

### Running it

```bash
docker compose up -d --build
make metrics      # curl every service's /metrics
make monitoring   # print the Prometheus and Grafana URLs
```

| | URL |
|---|---|
| Grafana dashboard | http://localhost:3000/d/fundkit-overview *(admin / admin)* |
| Prometheus targets | http://localhost:9090/targets |
| Alert rules | http://localhost:9090/alerts |
| Raw metrics | `:9101` gateway · `:9102` order · `:9103` portfolio · `:9104` notification |

The Grafana datasource **and** dashboard are provisioned from files in
[`monitoring/`](monitoring/), so a fresh `docker compose up` produces a working dashboard with no
clicking. The dashboard JSON lives in git and is reviewed like code rather than existing only in
somebody's browser.

Alert rules in [`monitoring/prometheus/rules/`](monitoring/prometheus/rules/) alert on symptoms a
customer could describe — error ratio, p99 regression, publish failures, growing consumer lag,
poison messages — never on causes like CPU. Every rule carries a `for:` clause so one bad scrape or
a deploy blip cannot page anyone.

Query recipes, including how to project backlog drain time, are in
[`monitoring/PROMQL.md`](monitoring/PROMQL.md).

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
│   └── platform/                # cross-cutting concerns
│       ├── logging|trace/       # structured logging and correlation-id plumbing
│       └── metrics/             # Prometheus registry, middleware, admin listener
└── pb/                         # generated protobuf stubs
```

Dependencies point inward: `handler → service → domain`. The service layer declares the interfaces
it needs (`Repository`, `EventPublisher`, `IdempotencyStore`) and the adapters satisfy them, so the
business rules can be tested with fakes and never import gorm, redis or kafka.

```
├── api-gateway/            edge: JWT, rate limiting, CORS, reverse proxy, health aggregation
├── order-service/          order state machine, idempotency, Kafka producer, gRPC client
├── portfolio-service/      gRPC valuation API + Kafka consumer building a real-time ledger, NAV cache-aside
├── notification-service/   Kafka consumer group, alert fan-out
├── frontend/               React + TypeScript dashboard
├── proto/fundkit.proto     internal service contract
├── monitoring/             Prometheus scrape config, alert rules, provisioned Grafana dashboard
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
- **Prometheus metrics on a separate admin port**, with route-template labels, shared histogram
  buckets across services, and alert rules that fire on symptoms rather than causes.
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
| `FUNDKIT_METRICS_PORT` | all | `9100` |
| `FUNDKIT_LOG_LEVEL` | all | `info` |
| `FUNDKIT_SHUTDOWN_TIMEOUT` | all | `15s` |
| `FUNDKIT_JWT_SECRET` | api-gateway | **required**, ≥16 chars |
| `FUNDKIT_JWT_TTL` | api-gateway | `24h` |
| `FUNDKIT_BCRYPT_COST` | api-gateway | `10` |
| `FUNDKIT_ORDER_SERVICE_URL` | api-gateway | `http://localhost:8081` |
| `FUNDKIT_RATE_LIMIT_RPS` / `_BURST` | api-gateway | `5` / `10` |
| `FUNDKIT_DB_URL` *(or `FUNDKIT_DB_HOST`, `_PORT`, `_USER`, `_PASSWORD`, `_NAME`)* | api-gateway, order-service | **required** |
| `FUNDKIT_REDIS_URL` | order, portfolio | `localhost:6379` |
| `FUNDKIT_KAFKA_BROKERS` | order, portfolio, notification | `localhost:9092` |
| `FUNDKIT_KAFKA_ORDER_TOPIC` | order, portfolio, notification | `order_events` |
| `FUNDKIT_KAFKA_CONSUMER_GROUP` | portfolio, notification | `fundkit-portfolio-workers` / `fundkit-notification-workers` — **must differ per service**, or Kafka splits the partitions between them and each sees only half the events, both looking perfectly healthy |
| `FUNDKIT_KAFKA_DLQ_TOPIC` | portfolio, notification | `order_events_portfolio_dlq` / `order_events_dlq` — must differ from the order topic and from each other, or a replay on one hands the event to both |
| `FUNDKIT_KAFKA_MAX_ATTEMPTS` | portfolio, notification | `3` |
| `FUNDKIT_KAFKA_RETRY_BACKOFF` | portfolio, notification | `200ms` |
| `FUNDKIT_IDEMPOTENCY_TTL` | order-service | `24h` |
| `FUNDKIT_PORTFOLIO_GRPC_URL` | order-service | `localhost:50051` |
| `FUNDKIT_NAV_CACHE_TTL` | portfolio-service | `30s` |
| `FUNDKIT_SEED_HOLDINGS` | portfolio-service | `true` (`false` in `k8s/config.yaml`) — demo positions for `user-1`/`user-2`; off in a cluster because they are not derived from any event and would double-count on a topic replay |

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
| Service metrics | `:9101` · `:9102` · `:9103` · `:9104` (`/metrics`) |
| Prometheus | http://localhost:9090/targets |
| Grafana dashboard | http://localhost:3000/d/fundkit-overview |

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
5. **Watch the ledger update.** portfolio-service runs its own Kafka consumer on `order_events`,
   in its own consumer group (`fundkit-portfolio-workers`), independent of notification-service's.
   When step 3's worker marks an order `EXECUTED`, that consumer resolves the fill's NAV, converts
   the rupee amount into units, and applies a `BUY` or `SELL` to the customer's position — updating
   the average cost, or closing the position entirely at zero units. Every event is deduplicated by
   `event_id` under the same lock as the mutation, so an at-least-once redelivery can never double a
   position.
6. **Fetch P&L.** Enter a user id (`user-1` or `user-2`) and fetch. order-service calls
   portfolio-service over gRPC; portfolio-service serves NAV from Redis (falling back to the feed on
   a miss) and holdings from the ledger step 5 just built.
7. **Trace it.** Take the `x-request-id` from any response header and grep it across
   `docker compose logs`.

```bash
docker compose down          # stop
docker compose down -v       # stop and drop the Postgres volume
```

---

## Known limits

Stated plainly, because knowing where a system stops is part of designing it:

- **Holdings are in-memory and single-replica.** portfolio-service builds a customer's positions by
  consuming `order_events` itself — average cost, unit accounting and idempotent replay are all
  real, not a stub — but the ledger lives in process memory, not Postgres. That is why
  `k8s/portfolio-service-deployment.yaml` is pinned to `replicas: 1`: two replicas in one consumer
  group would each be assigned different partitions and so hold only some customers' positions, and
  a read landing on the wrong pod would come back empty for a customer who owns funds. The fix is
  the same seam every other repository in this codebase already uses — move `HoldingsRepository`
  behind Postgres, with the position write and the processed-event insert in one transaction —
  at which point the replicas become interchangeable and the constraint lifts.
- **Rate limiting is per replica**, not global — see the tradeoff table.
- **Auth stops short of a full identity product.** Accounts are real — bcrypt-hashed passwords in
  Postgres, algorithm-pinned expiring tokens, uniqueness enforced by database indexes — but there
  is no refresh flow, no password reset, no email or phone verification, and no roles. A token is
  valid until it expires; there is no revocation list.
- **Notification channels are simulated.** `LogChannel` emits a structured record instead of
  calling SES or Twilio. The `Channel` interface is the seam: a real provider is an adapter, not a
  change to the notifier.

---

Engineered and developed by **Dhanush C N** — [github.com/dhanush-cn](https://github.com/dhanush-cn)
