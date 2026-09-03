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

// Registry is this process's metric namespace plus the collectors bound to it.
type Registry struct {
	prom *prometheus.Registry

	GRPC *GRPC
}

// New builds the registry and registers the runtime collectors.
func New() *Registry {
	prom := prometheus.NewRegistry()
	prom.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	return &Registry{
		prom: prom,
		GRPC: newGRPC(prom),
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
