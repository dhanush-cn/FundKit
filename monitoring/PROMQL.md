# FundKit PromQL reference

**Engineered by Dhanush C N** ([github.com/dhanush-cn](https://github.com/dhanush-cn))

Every query below is paste-ready into Grafana Explore or a new panel. They are
the same expressions the provisioned dashboard uses, extracted so they can be
copied one at a time.

`$__rate_interval` is a Grafana variable. In raw Prometheus, substitute a window
of at least four scrape intervals — with `scrape_interval: 15s` that means `1m`
or wider.

---

## What each service exports

| Metric | Type | Labels | Emitted by |
| --- | --- | --- | --- |
| `http_requests_total` | counter | `method`, `path`, `status_code` | api-gateway, order-service |
| `http_request_duration_seconds` | histogram | `method`, `path` | api-gateway, order-service |
| `http_requests_in_flight` | gauge | — | api-gateway, order-service |
| `grpc_server_requests_total` | counter | `grpc_service`, `grpc_method`, `grpc_code` | portfolio-service |
| `grpc_server_request_duration_seconds` | histogram | `grpc_service`, `grpc_method` | portfolio-service |
| `grpc_server_requests_in_flight` | gauge | — | portfolio-service |
| `kafka_events_published_total` | counter | `topic`, `status` | order-service |
| `kafka_publish_duration_seconds` | histogram | `topic` | order-service |
| `kafka_events_consumed_total` | counter | `topic`, `status` | portfolio-service, notification-service |
| `kafka_event_processing_duration_seconds` | histogram | `topic` | portfolio-service, notification-service |
| `kafka_event_retries_total` | counter | `topic` | portfolio-service, notification-service |
| `kafka_events_dead_lettered_total` | counter | `topic`, `reason` | portfolio-service, notification-service |
| `kafka_dlq_publish_failures_total` | counter | `topic` | portfolio-service, notification-service |
| `kafka_consumer_lag` | gauge | `topic`, `group` | portfolio-service, notification-service |

`job` and `instance` are added by Prometheus from the scrape config, so no
metric carries a redundant service label of its own. **This now matters more
than it used to**: portfolio-service and notification-service are two separate
consumer groups reading the *same* `order_events` topic, so every query below
that groups `kafka_events_consumed_total` or `kafka_event_processing_duration_seconds`
by `topic` alone is silently summing two different services' numbers into one
figure that describes neither of them. Add `job` to the `by (...)` clause
whenever you actually want one consumer's throughput or latency.

`status` on the consume counter is not the same set of values on both services.
notification-service uses `success`, `error`, `dropped` or `dead_lettered`:
`error` means the offset was not committed and the message will be redelivered,
`dropped` means an unparseable message was committed and abandoned. portfolio-service
has no `dropped` — an unparseable event there goes straight to `dead_lettered`
with `reason="unparseable"` instead — and adds `skipped` for the (very common)
case of an event that never reached `EXECUTED` and so never touched a position,
which notification-service has no equivalent for since it acts on every
terminal transition. Folding any of these together would hide a poison-message
leak inside a retry rate, or hide "the ledger is behind" inside "most of the
topic is just PENDING/PROCESSING noise, as expected."

---

## Throughput

```promql
# Fleet-wide RPS
sum(rate(http_requests_total[$__rate_interval]))

# Per service
sum by (job) (rate(http_requests_total[$__rate_interval]))

# Ten busiest routes, by route template rather than raw path
topk(10, sum by (job, method, path) (rate(http_requests_total[$__rate_interval])))
```

`rate()` before `sum()`, never the other way round. `rate()` knows how to handle
a counter resetting to zero when a pod restarts; summing first destroys that
information and produces a negative spike on every deploy.

## Latency

```promql
# p50 / p95 / p99 per service
histogram_quantile(0.50, sum by (le, job) (rate(http_request_duration_seconds_bucket[$__rate_interval])))
histogram_quantile(0.95, sum by (le, job) (rate(http_request_duration_seconds_bucket[$__rate_interval])))
histogram_quantile(0.99, sum by (le, job) (rate(http_request_duration_seconds_bucket[$__rate_interval])))

# p95 for one route
histogram_quantile(
  0.95,
  sum by (le) (rate(http_request_duration_seconds_bucket{job="order-service", path="/orders"}[$__rate_interval]))
)

# Mean latency, when you want the average rather than a quantile
  sum by (job) (rate(http_request_duration_seconds_sum[$__rate_interval]))
/ sum by (job) (rate(http_request_duration_seconds_count[$__rate_interval]))
```

Keep `le` in the `by` clause. `histogram_quantile` reads the bucket boundary
from that label, and aggregating it away leaves the function nothing to work
with — the query returns empty rather than an error, which is why this is such a
common thing to get wrong.

The quantile is computed from the shared bucket layout every FundKit service
uses, so it is a true fleet-wide percentile. This is exactly what a *summary*
metric could not give you: summaries compute quantiles inside each process and
cannot be averaged, so a p99 across four replicas would be arithmetically
meaningless.

## Error rate

```promql
# 5xx as a share of all traffic, per service
  sum by (job) (rate(http_requests_total{status_code=~"5.."}[$__rate_interval]))
/ clamp_min(sum by (job) (rate(http_requests_total[$__rate_interval])), 0.001)

# Rejections: 429 is the gateway's rate limiter, 401/403 are auth failures
sum by (job, status_code) (rate(http_requests_total{status_code=~"4.."}[$__rate_interval]))

# Availability over the last hour, as an SLO number
1 - (
    sum(increase(http_requests_total{status_code=~"5.."}[1h]))
  / sum(increase(http_requests_total[1h]))
)
```

`clamp_min` on the denominator keeps the panel at zero instead of `NaN` when
there is no traffic at all, which is the difference between an empty graph and a
graph that looks broken.

## Saturation

```promql
sum by (job) (http_requests_in_flight)
sum by (job) (go_goroutines)
sum by (job) (rate(process_cpu_seconds_total[$__rate_interval]))
```

In-flight requests move before latency does. When concurrency climbs while
throughput stays flat, requests are queueing on something downstream — that is
the moment to look, not after the p99 has already gone.

## gRPC (portfolio-service)

```promql
sum by (grpc_method) (rate(grpc_server_requests_total[$__rate_interval]))

sum by (grpc_code) (rate(grpc_server_requests_total[$__rate_interval]))

histogram_quantile(0.99, sum by (le, grpc_method) (rate(grpc_server_request_duration_seconds_bucket[$__rate_interval])))

# Everything that is not OK
sum by (grpc_method, grpc_code) (rate(grpc_server_requests_total{grpc_code!="OK"}[$__rate_interval]))
```

## Kafka

```promql
# Production vs one consumer's consumption. `job` matters here: portfolio-service
# and notification-service are two independent consumer groups on the same
# topic, so dropping `job` from the `by (...)` clause sums their two throughputs
# into one number that answers a question nobody asked.
sum by (topic) (rate(kafka_events_published_total{status="success"}[$__rate_interval]))
sum by (topic, job) (rate(kafka_events_consumed_total{status="success"}[$__rate_interval]))

# Consumer lag: the backlog, not the rate. `group` already disambiguates the
# two consumers, so no extra `by` is needed here.
kafka_consumer_lag

# Is one consumer's backlog growing or draining? Positive means falling behind.
# Pick the job you care about; comparing published-total against one
# consumer's total is meaningless if the other consumer is also draining the
# same topic.
  sum by (topic) (rate(kafka_events_published_total{status="success"}[$__rate_interval]))
- sum by (topic) (rate(kafka_events_consumed_total{status="success", job="portfolio-service"}[$__rate_interval]))

# Projected minutes to drain one consumer's current backlog at its current rate
  max(kafka_consumer_lag{group="fundkit-portfolio-workers"})
/ clamp_min(sum(rate(kafka_events_consumed_total{status="success", job="portfolio-service"}[$__rate_interval])), 0.001)
/ 60

# Outbox relay failing to reach the broker
sum by (topic) (rate(kafka_events_published_total{status="error"}[$__rate_interval]))

# Poison messages, the way that works for BOTH consumers: an unparseable event
# always ends up dead-lettered with this reason on either service, whereas
# `status="dropped"` only exists on notification-service's counter (see the
# table above) and will silently show zero for portfolio-service.
sum by (topic, job) (rate(kafka_events_dead_lettered_total{reason="unparseable"}[$__rate_interval]))

# Every dead-letter, by reason and by which consumer parked it
sum by (topic, job, reason) (rate(kafka_events_dead_lettered_total[$__rate_interval]))

# The counter to page on: the safety net itself failing. Non-zero means a
# consumer cannot park a message and is deliberately holding its offset back —
# always paired with that consumer's lag rising.
sum by (topic, job) (rate(kafka_dlq_publish_failures_total[$__rate_interval]))

# Consumer service time, per consumer
histogram_quantile(0.95, sum by (le, topic, job) (rate(kafka_event_processing_duration_seconds_bucket[$__rate_interval])))
```

Throughput alone will not tell you a consumer is failing. A worker handling
500 events/s looks perfectly healthy right up until you notice the producer is
doing 700/s and the backlog is an hour deep. Lag is the metric that catches it;
the drain-time query above is the one to quote when someone asks how long
recovery will take. On portfolio-service, lag is a product number as much as
an infrastructure one — it is how stale the holdings a customer is currently
looking at might be.

## Deploy and restart sanity

```promql
# Processes that restarted in the last 15 minutes
changes(process_start_time_seconds[15m]) > 0

# Uptime
time() - process_start_time_seconds

# Scrape targets that are down
up == 0
```
