// Package metrics owns the Prometheus surface of notification-service.
//
// This process is a worker, not an API. Its HTTP listener exists only so a
// kubelet can probe it, and instrumenting a liveness probe that fires every
// five seconds would drown the fleet-wide RPS panel in traffic nobody cares
// about. So the RED metrics here are Kafka metrics:
//
//   - kafka_events_consumed_total is the rate and the error signal,
//   - kafka_event_processing_duration_seconds is the duration signal,
//   - kafka_consumer_lag is the one that actually matters.
//
// Lag is the difference between the newest offset on a partition and the offset
// this group has committed. It is the only metric that answers "are we keeping
// up", and it is the metric a throughput graph hides: a consumer processing
// 500 events/s looks healthy right up until you notice the producer is doing
// 700/s and the backlog is an hour deep.
//
// Lag is published through a *collector* rather than a polling goroutine, so
// the value is read from the kafka-go reader at scrape time. A polled gauge is
// stale by up to one poll interval and keeps reporting its last value after the
// reader dies; a collector that reads on demand simply stops reporting, which
// is the honest answer.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// LatencyBuckets is the shared bucket layout for every FundKit latency
// histogram.
var LatencyBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// Outcomes recorded on kafka_events_consumed_total.
//
// "dropped" is separate from "error" on purpose. An unparseable message is
// committed and abandoned, while a handler failure leaves the offset alone and
// will be redelivered. Folding them together would hide a poison-message leak
// behind a retry rate.
const (
	StatusSuccess = "success"
	StatusError   = "error"
	StatusDropped = "dropped"
)

// Registry is this process's metric namespace plus the collectors bound to it.
type Registry struct {
	prom *prometheus.Registry

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
		Kafka: newKafka(prom),
	}
}

// Gatherer exposes the underlying registry to the /metrics handler.
func (r *Registry) Gatherer() *prometheus.Registry { return r.prom }

// Kafka instruments the consumer side of the order event stream.
type Kafka struct {
	reg prometheus.Registerer

	consumed   *prometheus.CounterVec
	processing *prometheus.HistogramVec
}

func newKafka(reg prometheus.Registerer) *Kafka {
	k := &Kafka{
		reg: reg,

		consumed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kafka_events_consumed_total",
			Help: "Order lifecycle events read from Kafka, by topic and outcome (success, error, dropped).",
		}, []string{"topic", "status"}),

		processing: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "kafka_event_processing_duration_seconds",
			Help:    "Time spent handling one event, from fetch to offset commit. p95 here is the consumer's real service time.",
			Buckets: LatencyBuckets,
		}, []string{"topic"}),
	}

	reg.MustRegister(k.consumed, k.processing)
	return k
}

// ObserveConsume records the outcome of handling one message. The nil receiver
// check keeps the consumer usable in tests that never build a registry.
func (k *Kafka) ObserveConsume(topic, status string, started time.Time) {
	if k == nil {
		return
	}
	k.consumed.WithLabelValues(topic, status).Inc()
	k.processing.WithLabelValues(topic).Observe(time.Since(started).Seconds())
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
			"Messages this consumer group is behind the head of the topic. The backlog, not the rate.",
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
