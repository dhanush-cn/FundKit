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
	"github.com/dhanush-cn/fundkit/order-service/internal/portfolio"
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

	publisher := messaging.NewPublisher(cfg.Kafka, logger)
	defer closeQuietly(logger, "kafka", publisher.Close)

	portfolioClient, err := portfolio.New(cfg.Portfolio, logger)
	if err != nil {
		return err
	}
	defer closeQuietly(logger, "portfolio-grpc", portfolioClient.Close)

	orderService := service.NewOrderService(
		repository.NewOrderRepository(db),
		redisClient,
		publisher,
		logger,
	)

	router := handler.NewRouter(handler.RouterDeps{
		Logger:         logger,
		RequestTimeout: cfg.Service.RequestTimeout,
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

	// Stop accepting new work, let in-flight requests finish, then wait for the
	// detached order lifecycle workers before releasing the dependencies.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Service.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", slog.String("error", err.Error()))
	}
	orderService.Drain()
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
