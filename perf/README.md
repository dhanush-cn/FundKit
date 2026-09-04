# FundKit performance and idempotency testing

**Engineered by Dhanush C N** ([github.com/dhanush-cn](https://github.com/dhanush-cn))

`load_test.js` is a [k6](https://k6.io) script that stress-tests the order write
path — `POST /orders` through api-gateway — while simultaneously proving that
concurrent replays of an idempotency key cannot create two orders.

Performance and correctness are tested together on purpose. Idempotency bugs are
concurrency bugs: they do not appear at one request per second, they appear when
five hundred virtual users are contending for the same Redis key and the same
unique index. A load test that only measures latency would miss exactly the
class of defect this system is designed to prevent.

---

## What the script does

| Scenario | Executor | Share of traffic | What it proves |
| --- | --- | --- | --- |
| `orders_ramp` | `ramping-vus`, 0 → 500 VUs over 2m, hold 3m, drain 1m | 90% unique keys, 10% replayed | Latency and error budget under sustained realistic load |
| `idempotency_race` | `constant-arrival-rate`, 5 iterations/s | small, steady | Exactly one accept per key when N requests fire simultaneously |

**`orders_ramp`** generates a fresh payload per iteration: a random `user_id`
across 5 000 partitions, a fund drawn from five ids, a rupee amount whose range
depends on the order type (SIPs small and recurring, lump sums large), and a
UUID v4 `idempotency_key`. One iteration in ten instead draws its key from a
shared pool of 200 run-scoped keys, so hundreds of VUs collide on the same keys
at the same instant. That is the double-submit pattern — a customer
double-clicking Buy, or a mobile client retrying a request whose response was
lost. The first VU to reach a pooled key wins it; every later attempt must be
refused. The `idem_pool_accepted` threshold asserts that at most 200 orders were
ever created from those 200 keys.

**`idempotency_race`** is the strict version of the same question. Each
iteration mints one fresh key and fires `RACE_FANOUT` (default 4) identical
requests through `http.batch`, which issues them in parallel from a single VU —
a genuine simultaneous race rather than a probabilistic one. Exactly one must
come back `201`, the rest `409`. It then sleeps half a second and replays the
same key once more, which tests the settled path: the Redis reservation should
still refuse it, and if the reservation has expired the unique index on
`idempotency_key` must refuse it instead.

Every request carries an `x-request-id` of the form `k6-<run_id>-<uuid>`. Both
services adopt an inbound request id rather than minting their own, so any
request in this run can be traced end to end through the structured logs of
api-gateway and order-service by grepping that single value.

---

## Running it

### 1. Start the stack

```bash
make up
```

### 2. Raise the edge rate limit for the duration of the run

This is the step that is easy to skip and invalidates everything. The gateway's
limiter is a per-client-IP token bucket defaulting to **5 rps with a burst of
10** (`FUNDKIT_RATE_LIMIT_RPS` / `FUNDKIT_RATE_LIMIT_BURST`). A load generator is
a single IP, so at 500 VUs the first second is served and the remaining five
minutes are `429`s. You would be benchmarking the rate limiter.

```bash
FUNDKIT_RATE_LIMIT_RPS=100000 FUNDKIT_RATE_LIMIT_BURST=100000 \
  docker compose up -d --force-recreate api-gateway
```

`setup()` sends a 15-request burst at `/healthz` before the run starts and warns
loudly if any of it comes back `429`, so a misconfigured run tells you in the
first second rather than in the summary. Any `429` observed during the run is
counted as an error, never as an expected response, and is also broken out on
its own `rate_limited_429` counter.

### 3. Run

```bash
mkdir -p perf/results
k6 run perf/load_test.js
```

or, from the repository root:

```bash
make load-test
```

### Useful variants

```bash
# Smoke run before committing: 20 VUs, one minute, same assertions.
TARGET_VUS=20 RAMP_UP=20s HOLD=30s RAMP_DOWN=10s k6 run perf/load_test.js

# Bypass the gateway to measure order-service in isolation. In direct mode the
# script forges the x-fundkit-user-id headers the gateway would stamp, so the
# random user_id per request is honoured — through the gateway the verified JWT
# subject always overrides whatever the body claims, which is the control that
# stops one customer placing an order in another's name.
MODE=direct BASE_URL=http://localhost:8081 k6 run perf/load_test.js

# Harder idempotency contention: fewer pooled keys, wider race fan-out.
REPLAY_POOL=20 REPLAY_SHARE=0.25 RACE_FANOUT=16 k6 run perf/load_test.js

# Stream results to Prometheus instead of the terminal summary.
K6_PROMETHEUS_RW_SERVER_URL=http://localhost:9090/api/v1/write \
  k6 run -o experimental-prometheus-rw perf/load_test.js
```

### Environment variables

| Variable | Default | Meaning |
| --- | --- | --- |
| `BASE_URL` | `http://localhost:8080` | Target. Point at `:8081` for direct mode. |
| `MODE` | `gateway` | `gateway` (JWT) or `direct` (forged identity headers). |
| `TARGET_VUS` | `500` | Peak virtual users. |
| `RAMP_UP` / `HOLD` / `RAMP_DOWN` | `2m` / `3m` / `1m` | Ramp profile. |
| `REPLAY_SHARE` | `0.1` | Fraction of ramp iterations that replay a pooled key. |
| `REPLAY_POOL` | `200` | Number of shared keys. Smaller = harder contention. |
| `RACE_RATE` | `5` | Strict races started per second. |
| `RACE_FANOUT` | `4` | Simultaneous requests per race. |
| `P95_BUDGET_MS` | `200` | p95 latency threshold. |
| `ERROR_BUDGET` | `0.01` | Error-rate threshold. |
| `LOAD_USER` / `LOAD_PASSWORD` | `fundkit_loadtest` / `LoadTest#2026` | Account used in gateway mode; registered on first run. |
| `RUN_ID` | timestamp | Prefixes every key so re-runs never collide. |
| `SUMMARY_PATH` | `perf/results/summary-<run_id>.json` | Where the JSON summary is written. |

---

## Thresholds, and why each one is there

```
http_req_duration{endpoint:create_order}  p(95) < 200ms
order_create_latency                      p(95) < 200ms, p(99) < 600ms
order_errors                              rate  < 1%
http_req_failed                           rate  < 1%
idem_double_processed                     count == 0     (aborts the run)
idem_pool_accepted                        count <= REPLAY_POOL
checks                                    rate  > 99%
```

The latency budget is scoped to `endpoint:create_order` so that health probes
and the one-off login in `setup()` cannot flatter the number.

`http_req_failed` needed a correction to be meaningful here. By default k6 counts
every 4xx as a failed request, which would mean a system that correctly rejects
100 000 replayed keys reports a 10% error rate. The script therefore calls
`http.setResponseCallback(http.expectedStatuses(200, 201, 409))`: a conflict is
the *right* answer to a replay, so it is not an error, while a `429` or a `5xx`
still is. `order_errors` carries the same definition as an explicit custom
metric.

`idem_double_processed` is the only threshold with `abortOnFail`. It is not a
performance number — a second accept for one key means a customer was charged
twice, and there is nothing to learn from the remaining four minutes of a run
that has already demonstrated a money bug.

---

## Watching it in Grafana

Open `http://localhost:3000/d/fundkit-overview` while the run is in flight.
Full query reference in [`../monitoring/PROMQL.md`](../monitoring/PROMQL.md).

**Gateway vs service p95** — if the two diverge, the cost is at the edge (JWT
verification, proxy connection pool), not in the order path:

```promql
histogram_quantile(0.95, sum by (le, job) (rate(http_request_duration_seconds_bucket{path="/orders"}[$__rate_interval])))
```

**Status mix on the write path** — the shape to expect is a large `201` band, a
smaller `409` band that grows with `REPLAY_SHARE`, and nothing else. Any `429`
means step 2 was skipped; any `5xx` is a real failure:

```promql
sum by (status_code) (rate(http_requests_total{path="/orders", method="POST"}[$__rate_interval]))
```

**Saturation before latency** — in-flight requests climb before the latency
histogram moves, so this is the earliest signal that the ramp has passed the
knee:

```promql
sum by (job) (http_requests_in_flight)
```

**The asynchronous tail.** The response returns once the order and its outbox
row are committed; Kafka publication happens behind the outbox relay. So the
publish rate should track the accept rate with a short lag, and consumer lag
should return to zero after the ramp drains. If it keeps climbing, the relay or
notification-service is the bottleneck even though `POST /orders` looked healthy
throughout:

```promql
sum by (topic) (rate(kafka_events_published_total{status="success"}[$__rate_interval]))
sum by (topic) (rate(kafka_events_consumed_total{status="success"}[$__rate_interval]))
sum by (topic, group) (kafka_consumer_lag)
```

---

## Reading the result

The custom summary block prints the numbers that matter:

```
  p95 latency          142.7 ms  (budget 200 ms)
  error rate           0.004 %   (budget 1.0 %)
  orders accepted      184203
  idempotent conflicts 20841
  pooled keys accepted 200       (must be <= 200)
  race winners (avg)   1.000     (must be 1.000)
  double processed     0         (must be 0)
  rate limited (429)   0
```

`race winners (avg)` is the headline correctness number. `1.000` means every
single simultaneous race resolved to exactly one accepted order. Anything above
it is double processing; anything below it means races were failing rather than
conflicting, which is a different bug — check for `5xx` and for Redis
saturation.

After the run, `teardown()` reads `/orders?limit=500` back and scans the most
recent page for two orders sharing one `idempotency_key`. This is deliberately
independent of what k6 observed in-flight: thresholds prove what the responses
said, the read-back asks the database what it actually stored. It is
sample-based — the endpoint caps at 500 rows — so a duplicate found there is
conclusive, while finding none is corroboration rather than proof.

## Limitations worth stating before someone else does

- **One load generator is one IP.** The per-IP rate limiter has to be raised for
  the run, which means this test does not exercise the limiter in a realistic
  distributed-client shape. Testing shedding behaviour properly needs
  `k6 run --distributed` or several generators.
- **The in-process limiter is per replica**, so results do not scale linearly
  with gateway replica count.
- **Written orders accumulate.** A full run inserts on the order of a hundred
  thousand rows plus their outbox records. Reset with `docker compose down -v`
  between comparison runs, or the later ones fight a larger index.
- **The p95 budget assumes a warm system.** Postgres connection pool, Redis, and
  the JIT-free Go binaries all settle within the first ramp minute; comparing a
  cold run against a warm one is not a comparison.
