// Package metrics owns the Prometheus surface of api-gateway.
//
// Three decisions worth calling out, because they are the ones that bite in
// production:
//
//   - The registry is explicit rather than prometheus.DefaultRegisterer. A
//     package-level default is global mutable state: it makes tests order
//     dependent and lets any transitively imported library publish series into
//     our namespace. Constructing one and injecting it keeps ownership visible.
//
//   - The route label is gin's *template* (c.FullPath(), e.g. /orders/:id), not
//     the raw URL path. A raw path turns every order id into its own time
//     series, and unbounded label cardinality is the single most common way a
//     Prometheus install falls over.
//
//   - Latency is a histogram, not a summary. Summaries compute quantiles inside
//     each process and cannot be aggregated, so a p99 over four replicas is
//     meaningless. Histograms ship buckets and let histogram_quantile() do the
//     maths across the whole fleet at query time.
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
// histogram. Identical buckets across services is what makes a single
// histogram_quantile() panel able to compare them.
//
// The spread is deliberate: dense between 5ms and 250ms where a healthy API
// call lives, sparse above 1s where the only question is "how bad".
var LatencyBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// Outcome labels shared by the Kafka and RPC counters.
const (
	StatusSuccess = "success"
	StatusError   = "error"
)

// Registry is this process's metric namespace plus the collectors bound to it.
type Registry struct {
	prom *prometheus.Registry

	// HTTP instruments the public edge.
	HTTP *HTTP
}

// New builds the registry and registers the runtime collectors.
//
// Go and process collectors are not decoration: goroutine count, GC pause and
// open file descriptors are what turn "the API is slow" into an actual
// diagnosis, and they cost nothing until a scrape happens.
func New() *Registry {
	prom := prometheus.NewRegistry()
	prom.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	return &Registry{
		prom: prom,
		HTTP: newHTTP(prom),
	}
}

// Gatherer exposes the underlying registry to the /metrics handler.
func (r *Registry) Gatherer() *prometheus.Registry { return r.prom }

// HTTP holds the RED metrics (Rate, Errors, Duration) for the HTTP surface.
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

// Middleware records one observation per request.
//
// Register it OUTSIDE the recovery middleware (that is, earlier in the chain).
// A panic then unwinds into Recovery, which writes the 500 and returns
// normally, so the deferred block below records the real status code instead of
// the zero value it would see if the panic were still in flight.
func (h *HTTP) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		h.inFlight.Inc()

		defer func() {
			h.inFlight.Dec()

			// An unmatched request has no route template. Folding all of them
			// into one bucket keeps a 404 scan from minting a series per URL.
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
