// Engineered by Dhanush C N (github.com/dhanush-cn)
package handler

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// RouterDeps is explicit constructor injection: every dependency the HTTP layer
// needs is visible in one struct rather than reached for through globals.
type RouterDeps struct {
	Logger         *slog.Logger
	RequestTimeout time.Duration
	Orders         *OrderHandler
	Portfolio      *PortfolioHandler
	Health         *HealthHandler
}

// NewRouter wires the middleware chain and route table.
func NewRouter(deps RouterDeps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	router.Use(
		RequestID(),
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
