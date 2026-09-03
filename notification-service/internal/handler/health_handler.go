// Engineered by Dhanush C N (github.com/dhanush-cn)
package handler

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dhanush-cn/fundkit/notification-service/internal/platform/trace"
)

// ConsumerState is flipped by the Kafka consumer as it connects and drains, so
// readiness reflects the worker rather than just the HTTP listener. A worker
// with no broker connection is alive but not ready.
type ConsumerState struct {
	ready atomic.Bool
}

func NewConsumerState() *ConsumerState { return &ConsumerState{} }

func (s *ConsumerState) MarkReady()    { s.ready.Store(true) }
func (s *ConsumerState) MarkNotReady() { s.ready.Store(false) }
func (s *ConsumerState) IsReady() bool { return s.ready.Load() }

// NewRouter builds the probe-only HTTP surface of the worker.
func NewRouter(service string, state *ConsumerState) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	router.Use(gin.Recovery(), requestID())

	live := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "UP",
			"service": service,
			"ts":      time.Now().UTC().Format(time.RFC3339),
		})
	}

	router.GET("/healthz", live)
	// Retained alias so existing gateway aggregation keeps working.
	router.GET("/health", live)

	router.GET("/readyz", func(c *gin.Context) {
		if !state.IsReady() {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status":       "NOT_READY",
				"service":      service,
				"dependencies": gin.H{"kafka-consumer": "DOWN"},
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status":       "READY",
			"service":      service,
			"dependencies": gin.H{"kafka-consumer": "UP"},
		})
	})

	return router
}

func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, id := trace.EnsureContext(c.Request.Context(), c.GetHeader(trace.HeaderKey))
		c.Request = c.Request.WithContext(ctx)
		c.Writer.Header().Set(trace.HeaderKey, id)
		c.Next()
	}
}
