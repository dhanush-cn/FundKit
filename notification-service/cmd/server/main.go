// Command server is the notification-service entry point: a Kafka worker that
// turns order lifecycle events into customer alerts, plus a probe endpoint so
// Kubernetes can supervise it like any other workload.
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
	"sync"
	"syscall"
	"time"

	"github.com/dhanush-cn/fundkit/notification-service/internal/config"
	"github.com/dhanush-cn/fundkit/notification-service/internal/consumer"
	"github.com/dhanush-cn/fundkit/notification-service/internal/handler"
	"github.com/dhanush-cn/fundkit/notification-service/internal/platform/logging"
	"github.com/dhanush-cn/fundkit/notification-service/internal/platform/metrics"
	"github.com/dhanush-cn/fundkit/notification-service/internal/service"
)

func main() {
	if err := run(); err != nil {
		slog.Error("notification-service terminated", slog.String("error", err.Error()))
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

	notifier := service.NewNotifier(logger,
		service.NewEmailChannel(logger),
		service.NewSMSChannel(logger),
	)

	state := handler.NewConsumerState()
	orderEvents := consumer.New(cfg.Kafka, notifier, promRegistry.Kafka, logger)

	httpServer := &http.Server{
		Addr:              ":" + cfg.Service.HTTPPort,
		Handler:           handler.NewRouter(cfg.Service.Name, state),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// The probe listener stays uninstrumented: a liveness check every five
	// seconds would swamp the fleet RPS panel with traffic nobody cares about.
	// For a worker the RED metrics are the Kafka metrics, and they are served
	// from this second listener.
	metricsServer := metrics.NewServer(":"+cfg.Service.MetricsPort, promRegistry)
	go func() {
		logger.InfoContext(ctx, "metrics listener started",
			slog.String("addr", metricsServer.Addr),
			slog.String("path", "/metrics"),
		)
		// Logged, not fatal: losing observability must not stop the worker
		// draining the topic.
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics listener stopped", slog.String("error", err.Error()))
		}
	}()

	var workers sync.WaitGroup
	serverErr := make(chan error, 1)

	workers.Add(1)
	go func() {
		defer workers.Done()
		state.MarkReady()
		defer state.MarkNotReady()

		if err := orderEvents.Run(ctx); err != nil {
			logger.Error("kafka consumer failed", slog.String("error", err.Error()))
		}
	}()

	go func() {
		logger.InfoContext(ctx, "http probe server listening", slog.String("addr", httpServer.Addr))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case err := <-serverErr:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining consumer")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Service.ShutdownTimeout)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("http graceful shutdown failed", slog.String("error", err.Error()))
	}

	// Wait for the in-flight event to finish and its offset to commit before
	// closing the reader, so redelivery after restart stays minimal.
	workers.Wait()
	if err := orderEvents.Close(); err != nil {
		logger.Error("failed to close kafka reader", slog.String("error", err.Error()))
	}

	// The admin listener goes last so a scrape landing mid-drain still sees the
	// final lag reading before the reader closes.
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("metrics listener shutdown failed", slog.String("error", err.Error()))
	}

	logger.Info("notification-service stopped cleanly")
	return nil
}
