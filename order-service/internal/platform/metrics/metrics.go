// Package metrics owns the Prometheus surface of order-service.
//
// order-service is the only process in FundKit that both serves HTTP and writes
// to Kafka, so it carries two families of instrument:
//
//   - RED metrics for the HTTP API (Rate, Errors, Duration), and
//   - publish counters for the outbox relay, keyed by topic and outcome.
//
// The publish counters exist because the outbox pattern moves the failure mode
// rather than removing it: an order is durable the moment its transaction
// commits, but the *event* is only real once the relay drains it. Without
// kafka_events_published_total the gap between "order accepted" and "downstream
// told" is invisible, which is exactly the gap that pages you at 3am.
//
// The registry is explicit rather than prometheus.DefaultRegisterer, and route
// labels use gin's template (/orders/:id) rather than the raw path, so an order
// id can never become a time series of its own.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package metrics

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// LatencyBuckets is the shared bucket layout for every FundKit latency
// histogram. Identical buckets across services is what lets one
// histogram_quantile() panel compare them.
var LatencyBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// Outcome labels shared by the HTTP and Kafka instruments.
const (
	StatusSuccess = "success"
	StatusError   = "error"
)

// Registry is this process's metric namespace plus the collectors bound to it.
type Registry struct {
	prom *prometheus.Registry

	HTTP  *HTTP
	Kafka *Kafka
}

// New builds the registry and registers the runtime collectors. Goroutine
// count and GC pause are what turn "orders are slow" into a diagnosis.
func New() *Registry {
	prom := prometheus.NewRegistry()
	prom.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	return &Registry{
		prom:  prom,
		HTTP:  newHTTP(prom),
		Kafka: newKafka(prom),
	}
}

// Gatherer exposes the underlying registry to the /metrics handler.
func (r *Registry) Gatherer() *prometheus.Registry { return r.prom }

// HTTP holds the RED metrics for the order API.
type HTTP struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inFlight prometheus.Gauge
}

func newHTTP(reg prometheus.Registerer) *HTTP {
	h := &HTTP{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests handled, by method, route template and response status code.",
		}, []string{"method", "path", "status_code"}),

		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency in seconds. p50/p95/p99 are derived at query time with histogram_quantile().",
			Buckets: LatencyBuckets,
		}, []string{"method", "path"}),

		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "HTTP requests currently being served. Saturation signal: it climbs before latency does.",
		}),
	}

	reg.MustRegister(h.requests, h.duration, h.inFlight)
	return h
}

// Middleware records one observation per request. Register it outside the
// recovery middleware so a recovered panic is counted as the 500 it became.
func (h *HTTP) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		h.inFlight.Inc()

		defer func() {
			h.inFlight.Dec()

			route := c.FullPath()
			if route == "" {
				route = "unmatched"
			}

			h.duration.
				WithLabelValues(c.Request.Method, route).
				Observe(time.Since(start).Seconds())
			h.requests.
				WithLabelValues(c.Request.Method, route, strconv.Itoa(c.Writer.Status())).
				Inc()
		}()

		c.Next()
	}
}

// Kafka instruments the producer side of the outbox relay.
type Kafka struct {
	published    *prometheus.CounterVec
	publishDelay *prometheus.HistogramVec
}

func newKafka(reg prometheus.Registerer) *Kafka {
	k := &Kafka{
		published: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kafka_events_published_total",
			Help: "Order lifecycle events written to Kafka, by topic and outcome.",
		}, []string{"topic", "status"}),

		publishDelay: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "kafka_publish_duration_seconds",
			Help:    "Time spent inside a single Kafka write, including the RequireAll acknowledgement wait.",
			Buckets: LatencyBuckets,
		}, []string{"topic"}),
	}

	reg.MustRegister(k.published, k.publishDelay)
	return k
}

// ObservePublish records the outcome of one publish attempt.
//
// The nil receiver check is deliberate: the messaging tests construct a
// Publisher without a registry, and instrumentation should never be the reason
// a test binary panics.
func (k *Kafka) ObservePublish(topic string, started time.Time, err error) {
	if k == nil {
		return
	}

	status := StatusSuccess
	if err != nil {
		status = StatusError
	}

	k.published.WithLabelValues(topic, status).Inc()
	k.publishDelay.WithLabelValues(topic).Observe(time.Since(started).Seconds())
}
