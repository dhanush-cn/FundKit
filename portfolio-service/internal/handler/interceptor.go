// Engineered by Dhanush C N (github.com/dhanush-cn)
package handler

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/dhanush-cn/fundkit/portfolio-service/internal/platform/trace"
)

// UnaryRequestID lifts the correlation id off inbound gRPC metadata into the
// context, so a trace started at the API gateway continues through this hop.
func UnaryRequestID() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		inbound := ""
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if values := md.Get(trace.HeaderKey); len(values) > 0 {
				inbound = values[0]
			}
		}

		ctx, id := trace.EnsureContext(ctx, inbound)
		// Echo the id back so the caller can correlate its own client-side log.
		_ = grpc.SetHeader(ctx, metadata.Pairs(trace.HeaderKey, id))
		return handler(ctx, req)
	}
}

// UnaryLogger emits one structured record per RPC, including the gRPC status
// code, which is the field on-call actually alerts on.
func UnaryLogger(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		response, err := handler(ctx, req)

		attrs := []any{
			slog.String("rpc", info.FullMethod),
			slog.String("code", status.Code(err).String()),
			slog.Duration("latency", time.Since(start)),
		}
		if err != nil {
			attrs = append(attrs, slog.String("error", err.Error()))
			logger.ErrorContext(ctx, "grpc request", attrs...)
			return response, err
		}

		logger.InfoContext(ctx, "grpc request", attrs...)
		return response, nil
	}
}

// UnaryTimeout applies a server-side deadline when a client did not set one,
// so a forgetful caller cannot hold a server stream open indefinitely.
func UnaryTimeout(d time.Duration) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if _, hasDeadline := ctx.Deadline(); hasDeadline {
			return handler(ctx, req)
		}
		ctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		return handler(ctx, req)
	}
}
