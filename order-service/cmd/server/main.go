// Command server is the order-service entry point: the transactional core of
// FundKit. It owns the order state machine, the idempotency guard and the
// publication of lifecycle events onto Kafka.
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

	"github.com/dhanush-cn/fundkit/order-service/internal/cache"
	"github.com/dhanush-cn/fundkit/order-service/internal/config"
	"github.com/dhanush-cn/fundkit/order-service/internal/handler"
	"github.com/dhanush-cn/fundkit/order-service/internal/messaging"
	"github.com/dhanush-cn/fundkit/order-service/internal/platform/logging"
	"github.com/dhanush-cn/fundkit/order-service/internal/platform/metrics"
	"github.com/dhanush-cn/fundkit/order-service/internal/portfolio"
	"github.com/dhanush-cn/fundkit/order-service/internal/relay"
	"github.com/dhanush-cn/fundkit/order-service/internal/repository"
	"github.com/dhanush-cn/fundkit/order-service/internal/service"
)

func main() {
	if err := run(); err != nil {
		slog.Error("order-service terminated", slog.String("error", err.Error()))
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

	// signal.NotifyContext turns SIGINT/SIGTERM into context cancellation, so
	// the whole dependency graph shuts down through one mechanism.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := repository.OpenPostgres(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer closeQuietly(logger, "postgres", func() error { return repository.ClosePostgres(db) })

	redisClient, err := cache.New(ctx, cfg.Redis, logger)
	if err != nil {
		return err
	}
	defer closeQuietly(logger, "redis", redisClient.Close)

	publisher := messaging.NewPublisher(cfg.Kafka, promRegistry.Kafka, logger)
	defer closeQuietly(logger, "kafka", publisher.Close)

	portfolioClient, err := portfolio.New(cfg.Portfolio, logger)
	if err != nil {
		return err
	}
	defer closeQuietly(logger, "portfolio-grpc", portfolioClient.Close)

	outboxRepo := repository.NewOutboxRepository(db)

	// The service no longer receives the publisher. It writes events to the
	// outbox inside the same transaction as the order, and the relay below is
	// the only thing in this process that talks to Kafka.
	orderService := service.NewOrderService(
		repository.NewOrderRepository(db),
		redisClient,
		logger,
	)

	outboxRelay := relay.New(outboxRepo, publisher, relay.Config{
		Interval:      cfg.Outbox.PollInterval,
		BatchSize:     cfg.Outbox.BatchSize,
		MaxAttempts:   cfg.Outbox.MaxAttempts,
		MaxBackoff:    cfg.Outbox.MaxBackoff,
		ShutdownFlush: cfg.Outbox.ShutdownFlush,
	}, logger)

	// The relay gets its own cancellation rather than sharing the signal
	// context, so shutdown can stop it *after* the HTTP server and the
	// lifecycle workers have finished writing their last outbox rows.
	relayCtx, stopRelay := context.WithCancel(context.Background())
	defer stopRelay()
	go outboxRelay.Run(relayCtx)

	router := handler.NewRouter(handler.RouterDeps{
		Logger:         logger,
		RequestTimeout: cfg.Service.RequestTimeout,
		Metrics:        promRegistry.HTTP,
		Orders:         handler.NewOrderHandler(orderService),
		Portfolio:      handler.NewPortfolioHandler(portfolioClient),
		Health: handler.NewHealthHandler(cfg.Service.Name,
			handler.ReadinessCheck{Name: "postgres", Probe: func(ctx context.Context) error {
				return repository.PingPostgres(ctx, db)
			}},
			handler.ReadinessCheck{Name: "redis", Probe: redisClient.Ping},
		),
	})

	server := &http.Server{
		Addr:              ":" + cfg.Service.HTTPPort,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// The admin listener is a second server on its own port so a scrape never
	// traverses the request timeout, tracing and access-log chain that exists
	// for customer traffic, and never appears in the RED metrics it is reading.
	metricsServer := metrics.NewServer(":"+cfg.Service.MetricsPort, promRegistry)
	go func() {
		logger.InfoContext(ctx, "metrics listener started",
			slog.String("addr", metricsServer.Addr),
			slog.String("path", "/metrics"),
		)
		// Logged, not fatal: losing observability is bad, refusing to accept
		// orders because of it is worse.
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics listener stopped", slog.String("error", err.Error()))
		}
	}()

	serverErr := make(chan error, 1)
	go func() {
		logger.InfoContext(ctx, "http server listening", slog.String("addr", server.Addr))
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

	// Shutdown order matters, and this is the whole story:
	//   1. stop accepting new HTTP work,
	//   2. let the detached lifecycle workers commit their last outbox rows,
	//   3. only then stop the relay, whose final flush publishes everything
	//      steps 1 and 2 just committed,
	//   4. and last, the deferred closers release Kafka, Redis and Postgres.
	// Closing the publisher before the relay drains would make the flush
	// silently publish nothing.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Service.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", slog.String("error", err.Error()))
	}
	orderService.Drain()

	stopRelay()
	outboxRelay.Wait()

	// The admin listener goes last, after the relay's final flush, so a scrape
	// landing mid-drain still records the publishes that flush produced.
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("metrics listener shutdown failed", slog.String("error", err.Error()))
	}

	logger.Info("order-service stopped cleanly")
	return nil
}

func closeQuietly(logger *slog.Logger, name string, closer func() error) {
	if err := closer(); err != nil {
		logger.Error("failed to close dependency",
			slog.String("dependency", name),
			slog.String("error", err.Error()))
	}
}
