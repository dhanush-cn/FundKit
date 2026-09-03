// Engineered by Dhanush C N (github.com/dhanush-cn)
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewServer builds the admin listener that exposes /metrics.
//
// It is a *separate* http.Server on its own port rather than a route on the
// public router, and that is the point:
//
//   - the gateway's public router carries JWT auth, CORS and a per-client rate
//     limiter; a scrape is neither authenticated nor rate limited, and adding
//     exceptions to a security chain to let a scraper through is how those
//     chains rot;
//   - it keeps /metrics off the internet by binding a port that is never
//     published through the ingress, so the only thing that can reach it is
//     something already inside the network namespace;
//   - a scrape that hangs cannot then consume a connection slot on the public
//     server, so observability can never be the thing that takes the API down.
//
// The same pattern and the same default port are used by all four FundKit
// services, so one Prometheus scrape config describes the whole fleet.
func NewServer(addr string, reg *Registry) *http.Server {
	mux := http.NewServeMux()

	mux.Handle("/metrics", promhttp.HandlerFor(reg.Gatherer(), promhttp.HandlerOpts{
		// OpenMetrics is what carries exemplars, which is the hook a tracing
		// backend uses to jump from a latency bucket to an actual slow request.
		EnableOpenMetrics: true,
		// A stuck scrape must not pile up: Prometheus retries on its own
		// schedule, so shedding is strictly better than queueing.
		MaxRequestsInFlight: 4,
		Timeout:             10 * time.Second,
	}))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("FundKit admin listener. Metrics are at /metrics.\n"))
	})

	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}
