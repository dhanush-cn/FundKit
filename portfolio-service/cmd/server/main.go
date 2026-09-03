// Command server is the portfolio-service entry point. It serves the internal
// gRPC valuation API plus a small HTTP mux carrying the Kubernetes probes.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"github.com/dhanush-cn/fundkit/portfolio-service/internal/cache"
	"github.com/dhanush-cn/fundkit/portfolio-service/internal/config"
	"github.com/dhanush-cn/fundkit/portfolio-service/internal/handler"
	"github.com/dhanush-cn/fundkit/portfolio-service/internal/platform/logging"
	"github.com/dhanush-cn/fundkit/portfolio-service/internal/platform/metrics"
	"github.com/dhanush-cn/fundkit/portfolio-service/internal/repository"
	"github.com/dhanush-cn/fundkit/portfolio-service/internal/service"
	"github.com/dhanush-cn/fundkit/portfolio-service/pb"
)

func main() {
	if err := run(); err != nil {
		slog.Error("portfolio-service terminated", slog.String("error", err.Error()))
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

	redisClient, err := cache.New(ctx, cfg.Redis, cfg.NAV.CacheTTL, logger)
	if err != nil {
		return err
	}
	defer func() {
		if err := redisClient.Close(); err != nil {
			logger.Error("failed to close redis", slog.String("error", err.Error()))
		}
	}()

	portfolioService := service.NewPortfolioService(
		repository.NewHoldingsRepository(),
		repository.NewNAVSource(),
		redisClient,
		logger,
	)

	// Interceptor order is the same argument as the HTTP middleware order in
	// the other services: the recorder sits outside the timeout, so an RPC the
	// server kills on its own deadline is still counted — with code
	// DeadlineExceeded, which is the distinction on-call actually needs.
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			handler.UnaryRequestID(),
			promRegistry.GRPC.UnaryInterceptor(),
			handler.UnaryLogger(logger),
			handler.UnaryTimeout(cfg.Service.RequestTimeout),
		),
	)
	pb.RegisterPortfolioServiceServer(grpcServer, handler.NewPortfolioServer(portfolioService, logger))

	// The standard gRPC health service lets grpc_health_probe and service
	// meshes check this process without a bespoke RPC.
	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("fundkit.PortfolioService", healthpb.HealthCheckResponse_SERVING)

	// Reflection makes the API explorable with grpcurl during debugging.
	reflection.Register(grpcServer)

	listener, err := net.Listen("tcp", ":"+cfg.Service.GRPCPort)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr: ":" + cfg.Service.HTTPPort,
		Handler: handler.NewHealthMux(cfg.Service.Name,
			handler.ReadinessCheck{Name: "redis", Probe: redisClient.Ping},
		),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// portfolio-service already runs an HTTP mux for the kubelet probes, but
	// metrics get their own listener anyway: a slow scrape must never be able
	// to delay a readiness probe and get the pod cycled out of rotation.
	metricsServer := metrics.NewServer(":"+cfg.Service.MetricsPort, promRegistry)

	serverErr := make(chan error, 2)

	go func() {
		logger.InfoContext(ctx, "metrics listener started",
			slog.String("addr", metricsServer.Addr),
			slog.String("path", "/metrics"),
		)
		// Logged, not fatal. Losing observability must not take the valuation
		// API down with it.
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics listener stopped", slog.String("error", err.Error()))
		}
	}()

	go func() {
		logger.InfoContext(ctx, "grpc server listening", slog.String("addr", listener.Addr().String()))
		serverErr <- grpcServer.Serve(listener)
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
		logger.Info("shutdown signal received, draining connections")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Service.ShutdownTimeout)
	defer cancel()

	// Report NOT_SERVING first so load balancers stop routing before the
	// in-flight RPCs are drained.
	healthServer.SetServingStatus("fundkit.PortfolioService", healthpb.HealthCheckResponse_NOT_SERVING)
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("http graceful shutdown failed", slog.String("error", err.Error()))
	}

	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
		logger.Info("portfolio-service stopped cleanly")
	case <-shutdownCtx.Done():
		// A stuck RPC must not hold the pod past its termination grace period.
		grpcServer.Stop()
		logger.Warn("grpc graceful stop timed out; forced shutdown")
	}

	// The admin listener goes last so a scrape landing mid-drain still sees the
	// in-flight gauge fall to zero rather than a refused connection. It is shut
	// down on a fresh context because shutdownCtx may already have expired in
	// the branch above.
	metricsCtx, cancelMetrics := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelMetrics()
	if err := metricsServer.Shutdown(metricsCtx); err != nil {
		logger.Error("metrics listener shutdown failed", slog.String("error", err.Error()))
	}

	return nil
}
