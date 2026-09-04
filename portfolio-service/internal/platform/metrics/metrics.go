// Package metrics owns the Prometheus surface of portfolio-service.
//
// This service speaks gRPC rather than HTTP, so the RED metrics are recorded
// from a unary interceptor and labelled the way the gRPC ecosystem expects:
// grpc_service, grpc_method and grpc_code. The code label matters more than the
// HTTP equivalent does — a gRPC call that fails with NotFound and one that fails
// with DeadlineExceeded look identical at the transport layer and mean
// completely different things to whoever is on call.
//
// FullMethod arrives as "/fundkit.PortfolioService/GetUserPnL" and is split
// rather than used whole, so a dashboard can group by service and still break
// down by method.
//
// Latency is a histogram rather than a summary: summaries compute quantiles
// per process and cannot be aggregated, so a p99 across replicas would be
// arithmetically meaningless. Buckets match the other three services exactly,
// which is what lets one panel compare an HTTP hop against a gRPC hop.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package metrics

import (
	"context"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// LatencyBuckets is the shared bucket layout for every FundKit latency
// histogram.
var LatencyBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// Outcomes recorded on kafka_events_consumed_total. The names match
// notification-service exactly so one Grafana panel can graph both consumers of
// order_events side by side.
//
// "skipped" is this service's addition and it matters here in a way it does not
// there. Most of the topic is PENDING and PROCESSING transitions that move no
// units, so the majority of what this consumer reads is a deliberate no-op.
// Counting those as successes would make the graph mostly noise and hide the
// only thing worth watching on it: the moment fills stop arriving.
//
// "error" and "dead_lettered" stay distinct for the same reason they do in
// notification-service. An error left the offset alone and the event will come
// back; a dead letter moved the offset but the event is durable elsewhere. One
// means "stuck", the other means "parked", and an alert wants to say which.
const (
	StatusSuccess      = "success"
	StatusSkipped      = "skipped"
	StatusError        = "error"
	StatusDeadLettered = "dead_lettered"
)

// Reasons recorded on kafka_events_dead_lettered_total.
const (
	ReasonUnparseable = "unparseable"
	ReasonPermanent   = "permanent_failure"
	ReasonExhausted   = "retries_exhausted"
)

// Registry is this process's metric namespace plus the collectors bound to it.
//
// portfolio-service is the only service in FundKit that is both an API and a
// worker, so it carries both metric families: gRPC RED metrics for the
// valuation reads and Kafka metrics for the ledger writes. They are separate
// namespaces on purpose — a spike in read latency and a spike in consumer lag
// are different incidents with different first questions, and a single
// "requests" counter covering both would answer neither.
type Registry struct {
	prom *prometheus.Registry

	GRPC  *GRPC
	Kafka *Kafka
}

// New builds the registry and registers the runtime collectors.
func New() *Registry {
	prom := prometheus.NewRegistry()
	prom.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	return &Registry{
		prom:  prom,
		GRPC:  newGRPC(prom),
		Kafka: newKafka(prom),
	}
}

// Gatherer exposes the underlying registry to the /metrics handler.
func (r *Registry) Gatherer() *prometheus.Registry { return r.prom }

// GRPC holds the RED metrics for the valuation API.
type GRPC struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inFlight prometheus.Gauge
}

func newGRPC(reg prometheus.Registerer) *GRPC {
	g := &GRPC{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "grpc_server_requests_total",
			Help: "Total unary RPCs handled, by service, method and gRPC status code.",
		}, []string{"grpc_service", "grpc_method", "grpc_code"}),

		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "grpc_server_request_duration_seconds",
			Help:    "Unary RPC latency in seconds. p50/p95/p99 are derived at query time with histogram_quantile().",
			Buckets: LatencyBuckets,
		}, []string{"grpc_service", "grpc_method"}),

		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "grpc_server_requests_in_flight",
			Help: "Unary RPCs currently being served. Saturation signal: it climbs before latency does.",
		}),
	}

	reg.MustRegister(g.requests, g.duration, g.inFlight)
	return g
}

// UnaryInterceptor records one observation per RPC.
//
// Chain it after UnaryRequestID (so a recorded call already carries its
// correlation id) but before UnaryTimeout, so a call killed by the server-side
// deadline is still counted — with code DeadlineExceeded, which is the whole
// reason to look at the code label.
func (g *GRPC) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		service, method := splitFullMethod(info.FullMethod)

		g.inFlight.Inc()
		defer g.inFlight.Dec()

		response, err := handler(ctx, req)

		// status.Code maps a nil error to OK and an unwrapped Go error to
		// Unknown, which is the same mapping the client sees on the wire.
		g.duration.WithLabelValues(service, method).Observe(time.Since(start).Seconds())
		g.requests.WithLabelValues(service, method, status.Code(err).String()).Inc()

		return response, err
	}
}

// splitFullMethod turns "/fundkit.PortfolioService/GetUserPnL" into its two
// halves. Anything that does not match that shape is labelled "unknown" rather
// than being passed through, so a malformed method name cannot inflate
// cardinality.
func splitFullMethod(fullMethod string) (service, method string) {
	trimmed := strings.TrimPrefix(fullMethod, "/")
	separator := strings.Index(trimmed, "/")
	if separator < 0 {
		return "unknown", "unknown"
	}
	return trimmed[:separator], trimmed[separator+1:]
}

// ---------------------------------------------------------------------------
// Kafka — the ledger write path.
//
// Lag is the metric that actually matters here, and it means something sharper
// in this service than in notification-service. There, lag is delayed email.
// Here, lag is a customer looking at a dashboard that does not yet include the
// order they just placed — the holdings are not wrong, they are behind, and the
// two are indistinguishable from the customer's side. So the alert threshold on
// kafka_consumer_lag for this group is a product decision about staleness, not
// an infrastructure one about throughput.
// ---------------------------------------------------------------------------

// Kafka instruments the consumer side of the order event stream.
type Kafka struct {
	reg prometheus.Registerer

	consumed     *prometheus.CounterVec
	processing   *prometheus.HistogramVec
	retries      *prometheus.CounterVec
	deadLettered *prometheus.CounterVec
	dlqFailures  *prometheus.CounterVec
}

func newKafka(reg prometheus.Registerer) *Kafka {
	k := &Kafka{
		reg: reg,

		consumed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kafka_events_consumed_total",
			Help: "Order lifecycle events read from Kafka, by topic and outcome (success, skipped, error, dead_lettered).",
		}, []string{"topic", "status"}),

		processing: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "kafka_event_processing_duration_seconds",
			Help:    "Time spent handling one event, from fetch to offset commit. p95 here is the consumer's real service time.",
			Buckets: LatencyBuckets,
		}, []string{"topic"}),

		retries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kafka_event_retries_total",
			Help: "Projection attempts beyond the first. A rising rate with a flat dead-letter rate is the NAV feed wobbling but recovering.",
		}, []string{"topic"}),

		deadLettered: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kafka_events_dead_lettered_total",
			Help: "Events published to the dead-letter topic, by reason (unparseable, permanent_failure, retries_exhausted).",
		}, []string{"topic", "reason"}),

		// This is the counter to page on. Every other failure mode here is
		// contained: the event is either retried or safely parked. A DLQ publish
		// failure means the safety net itself is down, and the consumer responds
		// by refusing to commit — so this rising is always accompanied by lag
		// rising, and the pair together says "stuck, on purpose, needs a human".
		dlqFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kafka_dlq_publish_failures_total",
			Help: "Failed attempts to publish to the dead-letter topic. Non-zero means events cannot be parked and the offset is deliberately held back.",
		}, []string{"topic"}),
	}

	reg.MustRegister(k.consumed, k.processing, k.retries, k.deadLettered, k.dlqFailures)
	return k
}

// ObserveConsume records the outcome of handling one event. The nil receiver
// check keeps the consumer usable in tests that never build a registry.
func (k *Kafka) ObserveConsume(topic, status string, started time.Time) {
	if k == nil {
		return
	}
	k.consumed.WithLabelValues(topic, status).Inc()
	k.processing.WithLabelValues(topic).Observe(time.Since(started).Seconds())
}

// ObserveRetry counts one re-attempt of an event that already failed.
func (k *Kafka) ObserveRetry(topic string) {
	if k == nil {
		return
	}
	k.retries.WithLabelValues(topic).Inc()
}

// ObserveDeadLetter counts one event parked on the dead-letter topic.
func (k *Kafka) ObserveDeadLetter(topic, reason string) {
	if k == nil {
		return
	}
	k.deadLettered.WithLabelValues(topic, reason).Inc()
}

// ObserveDLQPublishFailure counts a failure to park an event.
func (k *Kafka) ObserveDLQPublishFailure(topic string) {
	if k == nil {
		return
	}
	k.dlqFailures.WithLabelValues(topic).Inc()
}

// LagSample is one reading of consumer lag, reported at scrape time.
//
// A negative Lag means "not known yet" — a reader that has not completed a
// fetch has no high water mark to measure against. Reporting the reading
// verbatim and letting the collector decide what is publishable keeps the
// source honest: the consumer says what it saw, and one place decides what
// leaves the process.
type LagSample struct {
	Topic string
	Group string
	Lag   float64
}

// RegisterConsumerLag publishes kafka_consumer_lag, sourced from snapshot on
// every scrape. Passing a function rather than a value is what keeps the gauge
// honest: nothing is exported unless the reader can actually answer.
func (k *Kafka) RegisterConsumerLag(snapshot func() []LagSample) {
	if k == nil || snapshot == nil {
		return
	}
	k.reg.MustRegister(lagCollector{
		desc: prometheus.NewDesc(
			"kafka_consumer_lag",
			"Messages this consumer group is behind the head of the topic. On this service it is how stale the holdings a customer is looking at may be.",
			[]string{"topic", "group"},
			nil,
		),
		snapshot: snapshot,
	})
}

// lagCollector is a minimal prometheus.Collector: it holds no state and reads
// the reader's own statistics when Prometheus asks.
type lagCollector struct {
	desc     *prometheus.Desc
	snapshot func() []LagSample
}

func (c lagCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c lagCollector) Collect(ch chan<- prometheus.Metric) {
	for _, sample := range c.snapshot() {
		// Suppress rather than export. A negative reading means the lag is not
		// known yet, and "unknown" is not the same as "zero" — publishing zero
		// would tell a dashboard the consumer is fully caught up at exactly the
		// moment nobody can say whether it is.
		if sample.Lag < 0 {
			continue
		}

		ch <- prometheus.MustNewConstMetric(
			c.desc,
			prometheus.GaugeValue,
			sample.Lag,
			sample.Topic,
			sample.Group,
		)
	}
}
