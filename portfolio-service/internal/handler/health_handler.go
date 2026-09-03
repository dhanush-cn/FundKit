// Engineered by Dhanush C N (github.com/dhanush-cn)
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/dhanush-cn/fundkit/portfolio-service/internal/platform/trace"
)

// ReadinessCheck is one named dependency probe.
type ReadinessCheck struct {
	Name  string
	Probe func(ctx context.Context) error
}

// NewHealthMux exposes the probe endpoints over plain HTTP. portfolio-service
// speaks gRPC to its peers, but Kubernetes and the dashboard aggregator both
// speak HTTP, so the process serves a small side-car mux of its own.
func NewHealthMux(service string, checks ...ReadinessCheck) *http.ServeMux {
	mux := http.NewServeMux()

	live := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, r, http.StatusOK, map[string]any{
			"status":  "UP",
			"service": service,
			"ts":      time.Now().UTC().Format(time.RFC3339),
		})
	}

	mux.HandleFunc("/healthz", live)
	// Retained alias so existing gateway aggregation keeps working.
	mux.HandleFunc("/health", live)

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		results := make(map[string]string, len(checks))
		ready := true
		for _, check := range checks {
			if err := check.Probe(ctx); err != nil {
				results[check.Name] = "DOWN: " + err.Error()
				ready = false
				continue
			}
			results[check.Name] = "UP"
		}

		status := http.StatusOK
		overall := "READY"
		if !ready {
			status = http.StatusServiceUnavailable
			overall = "NOT_READY"
		}

		writeJSON(w, r, status, map[string]any{
			"status":       overall,
			"service":      service,
			"dependencies": results,
		})
	})

	return mux
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, body map[string]any) {
	if id := r.Header.Get(trace.HeaderKey); id != "" {
		w.Header().Set(trace.HeaderKey, id)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
