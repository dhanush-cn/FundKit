// Package portfolio is the outbound gRPC adapter to portfolio-service.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package portfolio

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/dhanush-cn/fundkit/order-service/internal/config"
	"github.com/dhanush-cn/fundkit/order-service/internal/platform/trace"
	"github.com/dhanush-cn/fundkit/order-service/pb"
)

// ErrUnavailable is returned when the dependency is not reachable, letting the
// handler degrade to 503 instead of pretending the data is missing.
var ErrUnavailable = errors.New("portfolio service unavailable")

type Client struct {
	conn        *grpc.ClientConn
	rpc         pb.PortfolioServiceClient
	callTimeout config.PortfolioConfig
	logger      *slog.Logger
}

// New creates a lazily-connecting client. grpc.NewClient does not block on
// startup, so a cold portfolio-service delays a single request rather than
// preventing order-service from booting at all.
func New(cfg config.PortfolioConfig, logger *slog.Logger) (*Client, error) {
	conn, err := grpc.NewClient(
		cfg.GRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(requestIDInterceptor),
	)
	if err != nil {
		return nil, err
	}

	logger.Info("portfolio gRPC client ready", slog.String("target", cfg.GRPCAddr))
	return &Client{conn: conn, rpc: pb.NewPortfolioServiceClient(conn), callTimeout: cfg, logger: logger}, nil
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// FetchUserPnL calls portfolio-service with a bounded deadline so a slow
// dependency cannot pin an inbound HTTP request open indefinitely.
func (c *Client) FetchUserPnL(ctx context.Context, userID string) (*pb.PnLResponse, error) {
	if c == nil || c.rpc == nil {
		return nil, ErrUnavailable
	}

	callCtx, cancel := context.WithTimeout(ctx, c.callTimeout.CallTimeout)
	defer cancel()

	response, err := c.rpc.GetUserPnL(callCtx, &pb.PnLRequest{UserId: userID})
	if err != nil {
		c.logger.WarnContext(ctx, "portfolio rpc failed", slog.String("error", err.Error()))
		return nil, errors.Join(ErrUnavailable, err)
	}
	return response, nil
}

// requestIDInterceptor copies the correlation id from the context onto outgoing
// gRPC metadata, extending the trace across the process boundary.
func requestIDInterceptor(
	ctx context.Context,
	method string,
	req, reply any,
	cc *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	opts ...grpc.CallOption,
) error {
	if id := trace.FromContext(ctx); id != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, trace.HeaderKey, id)
	}
	return invoker(ctx, method, req, reply, cc, opts...)
}
