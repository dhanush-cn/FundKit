// Engineered by Dhanush C N (github.com/dhanush-cn)
package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// ReadinessCheck is one named dependency probe.
type ReadinessCheck struct {
	Name  string
	Probe func(ctx context.Context) error
}

// HealthHandler separates liveness from readiness, which is the distinction
// Kubernetes actually acts on: /healthz failing restarts the pod, /readyz
// failing only pulls it out of the load balancer until its dependencies return.
type HealthHandler struct {
	service string
	checks  []ReadinessCheck
}

func NewHealthHandler(service string, checks ...ReadinessCheck) *HealthHandler {
	return &HealthHandler{service: service, checks: checks}
}

// Live answers the liveness probe: the process is running and can serve HTTP.
func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "UP",
		"service": h.service,
		"ts":      time.Now().UTC().Format(time.RFC3339),
	})
}

// Ready answers the readiness probe by fanning out to every dependency.
func (h *HealthHandler) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	results := make(map[string]string, len(h.checks))
	ready := true

	for _, check := range h.checks {
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

	c.JSON(status, gin.H{
		"status":       overall,
		"service":      h.service,
		"dependencies": results,
	})
}
