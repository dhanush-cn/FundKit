// Engineered by Dhanush C N (github.com/dhanush-cn)
package handler

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dhanush-cn/fundkit/order-service/internal/platform/metrics"
)

// RouterDeps is explicit constructor injection: every dependency the HTTP layer
// needs is visible in one struct rather than reached for through globals.
type RouterDeps struct {
	Logger         *slog.Logger
	RequestTimeout time.Duration
	Metrics        *metrics.HTTP
	Orders         *OrderHandler
	Portfolio      *PortfolioHandler
	Health         *HealthHandler
}

// NewRouter wires the middleware chain and route table.
//
// The metrics middleware is registered outside Recovery on purpose: a panic
// unwinds through everything below the recovery handler before recover() runs,
// so an inner observer would record the status code as it stood before the 500
// was written. Outside it, c.Next() returns with the 500 already set and a
// panic is counted as the error it is.
func NewRouter(deps RouterDeps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	router.Use(
		RequestID(),
		deps.Metrics.Middleware(),
		Recovery(deps.Logger),
		RequestLogger(deps.Logger),
		Timeout(deps.RequestTimeout),
	)

	// Kubernetes probes. /health is retained as an alias so the existing
	// gateway aggregation and dashboards keep working.
	router.GET("/healthz", deps.Health.Live)
	router.GET("/readyz", deps.Health.Ready)
	router.GET("/health", deps.Health.Live)

	orders := router.Group("/orders")
	{
		orders.POST("", deps.Orders.Create)
		orders.GET("", deps.Orders.List)
		orders.GET("/:id", deps.Orders.Get)
		orders.PATCH("/:id", deps.Orders.UpdateStatus)
		orders.DELETE("/:id", deps.Orders.Delete)
	}

	router.GET("/portfolio/:userId/pnl", deps.Portfolio.GetUserPnL)

	return router
}
