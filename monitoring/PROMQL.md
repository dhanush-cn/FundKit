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
| `kafka_events_consumed_total` | counter | `topic`, `status` | notification-service |
| `kafka_event_processing_duration_seconds` | histogram | `topic` | notification-service |
| `kafka_consumer_lag` | gauge | `topic`, `group` | notification-service |

`job` and `instance` are added by Prometheus from the scrape config, so no
metric carries a redundant service label of its own.

`status` is `success` or `error` on the publish counter, and `success`, `error`
or `dropped` on the consume counter. The third value matters: `error` means the
offset was not committed and the message will be redelivered, `dropped` means an
unparseable message was committed and abandoned. Folding them together would
hide a poison-message leak inside a retry rate.

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
# Production vs consumption. Read them on one panel.
sum by (topic) (rate(kafka_events_published_total{status="success"}[$__rate_interval]))
sum by (topic) (rate(kafka_events_consumed_total{status="success"}[$__rate_interval]))

# Consumer lag: the backlog, not the rate
kafka_consumer_lag

# Is the backlog growing or draining? Positive means falling behind.
  sum by (topic) (rate(kafka_events_published_total{status="success"}[$__rate_interval]))
- sum by (topic) (rate(kafka_events_consumed_total{status="success"}[$__rate_interval]))

# Projected minutes to drain the current backlog at the current consume rate
  max(kafka_consumer_lag)
/ clamp_min(sum(rate(kafka_events_consumed_total{status="success"}[$__rate_interval])), 0.001)
/ 60

# Outbox relay failing to reach the broker
sum by (topic) (rate(kafka_events_published_total{status="error"}[$__rate_interval]))

# Poison messages: committed, discarded, gone
sum by (topic) (rate(kafka_events_consumed_total{status="dropped"}[$__rate_interval]))

# Consumer service time
histogram_quantile(0.95, sum by (le, topic) (rate(kafka_event_processing_duration_seconds_bucket[$__rate_interval])))
```

Throughput alone will not tell you the consumer is failing. A worker handling
500 events/s looks perfectly healthy right up until you notice the producer is
doing 700/s and the backlog is an hour deep. Lag is the metric that catches it;
the drain-time query above is the one to quote when someone asks how long
recovery will take.

## Deploy and restart sanity

```promql
# Processes that restarted in the last 15 minutes
changes(process_start_time_seconds[15m]) > 0

# Uptime
time() - process_start_time_seconds

# Scrape targets that are down
up == 0
```
