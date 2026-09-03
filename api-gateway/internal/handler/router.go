// Engineered by Dhanush C N (github.com/dhanush-cn)
package handler

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/dhanush-cn/fundkit/api-gateway/internal/config"
	"github.com/dhanush-cn/fundkit/api-gateway/internal/middleware"
	"github.com/dhanush-cn/fundkit/api-gateway/internal/platform/metrics"
)

// RouterDeps lists everything the edge needs, injected explicitly.
type RouterDeps struct {
	Config      config.Config
	Logger      *slog.Logger
	RateLimiter *middleware.RateLimiter
	Metrics     *metrics.HTTP
	Auth        *AuthHandler
	Health      *HealthHandler
	OrderProxy  *ProxyHandler
}

// NewRouter wires the gateway's middleware chain and route table.
//
// Ordering matters: correlation id first so every later log line carries it,
// metrics next so they wrap the recovery handler, recovery after that so a
// panic anywhere below is still logged with that id, then CORS and rate
// limiting before any work is done on behalf of the caller.
//
// Metrics sit *outside* Recovery rather than inside it for one specific reason:
// a panic unwinds through every middleware registered below the recovery
// handler before the recover() runs, so an inner metrics middleware would
// observe the status code as it stood before Recovery wrote the 500. Placed
// outside, c.Next() returns normally with the 500 already written, and the
// error rate panel counts panics as the failures they are.
//
// Rate-limited requests are counted too: the limiter aborts with 429 below
// this point, so the counter carries them under status_code="429". That is the
// signal that tells you whether a traffic drop is the client backing off or
// the gateway shedding.
func NewRouter(deps RouterDeps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	router.Use(
		middleware.RequestID(),
		deps.Metrics.Middleware(),
		middleware.Recovery(deps.Logger),
		middleware.RequestLogger(deps.Logger),
		middleware.CORS(deps.Config.CORS.AllowedOrigins),
		deps.RateLimiter.Middleware(),
	)

	// Probes stay unauthenticated: kubelet does not carry a bearer token.
	router.GET("/healthz", deps.Health.Live)
	router.GET("/readyz", deps.Health.Ready)
	router.GET("/health", deps.Health.Live)
	router.GET("/services/health", deps.Health.Stack)

	// Credential endpoints are public by necessity: you cannot present a token
	// before you have one.
	auth := router.Group("/auth")
	{
		auth.POST("/register", deps.Auth.Register)
		auth.POST("/login", deps.Auth.Login)
	}
	// Retained alias so older clients and scripts keep working.
	router.POST("/login", deps.Auth.Login)

	protected := router.Group("/")
	protected.Use(middleware.JWTAuth(deps.Config.Auth.JWTSecret))
	{
		protected.GET("/auth/me", deps.Auth.Me)
		protected.Any("/orders", deps.OrderProxy.Handle)
		protected.Any("/orders/*path", deps.OrderProxy.Handle)
		protected.Any("/portfolio", deps.OrderProxy.Handle)
		protected.Any("/portfolio/*path", deps.OrderProxy.Handle)
	}

	return router
}
