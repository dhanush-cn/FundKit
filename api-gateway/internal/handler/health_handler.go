// Engineered by Dhanush C N (github.com/dhanush-cn)
package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dhanush-cn/fundkit/api-gateway/internal/service"
)

// ReadinessCheck is one dependency the gateway cannot serve without.
type ReadinessCheck struct {
	Name  string
	Probe func(ctx context.Context) error
}

type HealthHandler struct {
	service    string
	aggregator *service.HealthAggregator
	checks     []ReadinessCheck
}

func NewHealthHandler(name string, aggregator *service.HealthAggregator, checks ...ReadinessCheck) *HealthHandler {
	return &HealthHandler{service: name, aggregator: aggregator, checks: checks}
}

// Live answers the Kubernetes liveness probe. It reports only that the process
// is running: restarting the pod cannot fix a dependency outage, so dependencies
// are deliberately not consulted here.
func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "UP",
		"service": h.service,
		"ts":      time.Now().UTC().Format(time.RFC3339),
	})
}

// Ready answers the readiness probe.
//
// The gateway stays ready when an *upstream* service is down: it can still
// authenticate callers and return honest 502s, and tying edge readiness to
// downstream health would take the whole platform offline whenever one service
// restarts. The identity database is different — without it no one can log in
// at all — so it is the one dependency that gates readiness.
func (h *HealthHandler) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	details := make(map[string]string, len(h.checks))
	ready := true
	for _, check := range h.checks {
		if err := check.Probe(ctx); err != nil {
			details[check.Name] = "DOWN"
			ready = false
			continue
		}
		details[check.Name] = "UP"
	}

	if !ready {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":       "NOT_READY",
			"service":      h.service,
			"dependencies": details,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":       "READY",
		"service":      h.service,
		"dependencies": details,
	})
}

// Stack aggregates upstream health for the dashboard.
func (h *HealthHandler) Stack(c *gin.Context) {
	status := h.aggregator.Collect(c.Request.Context())
	if status.Status != "UP" {
		c.JSON(http.StatusPartialContent, status)
		return
	}
	c.JSON(http.StatusOK, status)
}
