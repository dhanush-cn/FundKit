// Command server is the api-gateway entry point: the only public surface of
// FundKit. It terminates client HTTP, authenticates the caller, applies rate
// limiting and forwards to the internal services.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dhanush-cn/fundkit/api-gateway/internal/config"
	"github.com/dhanush-cn/fundkit/api-gateway/internal/handler"
	"github.com/dhanush-cn/fundkit/api-gateway/internal/middleware"
	"github.com/dhanush-cn/fundkit/api-gateway/internal/platform/logging"
	"github.com/dhanush-cn/fundkit/api-gateway/internal/platform/metrics"
	"github.com/dhanush-cn/fundkit/api-gateway/internal/repository"
	"github.com/dhanush-cn/fundkit/api-gateway/internal/service"
)

func main() {
	if err := run(); err != nil {
		slog.Error("api-gateway terminated", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := logging.New(cfg.Service.Name, cfg.Service.LogLevel)

	// One registry per process, constructed here and injected downward. Nothing
	// in FundKit reaches for prometheus.DefaultRegisterer, so what this service
	// exports is exactly what this function wired up.
	promRegistry := metrics.New()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The gateway owns the identity store: it is the only component that ever
	// reads a password hash.
	db, err := repository.OpenPostgres(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer closeQuietly(logger, "postgres", func() error { return repository.ClosePostgres(db) })

	authService := service.NewAuthService(repository.NewUserRepository(db), cfg.Auth, logger)

	orderProxy, err := handler.NewProxyHandler(cfg.Upstreams.OrderServiceURL, cfg.Service.ProxyTimeout, logger)
	if err != nil {
		return err
	}

	rateLimiter := middleware.NewRateLimiter(cfg.RateLimit)
	defer rateLimiter.Close()

	router := handler.NewRouter(handler.RouterDeps{
		Config:      cfg,
		Logger:      logger,
		RateLimiter: rateLimiter,
		Metrics:     promRegistry.HTTP,
		Auth:        handler.NewAuthHandler(authService),
		Health: handler.NewHealthHandler(cfg.Service.Name,
			service.NewHealthAggregator(cfg.Upstreams),
			handler.ReadinessCheck{Name: "postgres", Probe: func(ctx context.Context) error {
				return repository.PingPostgres(ctx, db)
			}},
		),
		OrderProxy: orderProxy,
	})

	server := &http.Server{
		Addr:              ":" + cfg.Service.HTTPPort,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      40 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	// The admin listener is deliberately a second server on its own port: a
	// scrape must not pass through JWT auth, CORS or the rate limiter, and
	// /metrics must not be reachable from the internet through the ingress.
	metricsServer := metrics.NewServer(":"+cfg.Service.MetricsPort, promRegistry)
	go func() {
		logger.InfoContext(ctx, "metrics listener started",
			slog.String("addr", metricsServer.Addr),
			slog.String("path", "/metrics"),
		)
		// A failure here is logged, not fatal. Losing observability is bad;
		// refusing to serve customer traffic because of it is worse.
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics listener stopped", slog.String("error", err.Error()))
		}
	}()

	serverErr := make(chan error, 1)
	go func() {
		logger.InfoContext(ctx, "api gateway listening",
			slog.String("addr", server.Addr),
			slog.String("order_upstream", cfg.Upstreams.OrderServiceURL),
		)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining connections")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Service.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", slog.String("error", err.Error()))
		return err
	}

	// The admin listener goes last so a scrape landing mid-drain still sees the
	// in-flight gauge fall to zero rather than a refused connection.
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("metrics listener shutdown failed", slog.String("error", err.Error()))
	}

	logger.Info("api-gateway stopped cleanly")
	return nil
}

func closeQuietly(logger *slog.Logger, name string, closer func() error) {
	if err := closer(); err != nil {
		logger.Error("failed to close dependency",
			slog.String("dependency", name),
			slog.String("error", err.Error()))
	}
}
