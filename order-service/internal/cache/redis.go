// Package cache wraps Redis. Its single responsibility in order-service is the
// idempotency reservation that keeps a retried HTTP request from becoming two
// real money movements.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package cache

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dhanush-cn/fundkit/order-service/internal/config"
)

type Client struct {
	rdb *redis.Client
	ttl time.Duration
}

// New dials Redis and verifies the connection before the service is allowed to
// report itself ready.
func New(ctx context.Context, cfg config.RedisConfig, logger *slog.Logger) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:        cfg.Addr,
		Password:    cfg.Password,
		DB:          cfg.DB,
		DialTimeout: cfg.DialTimeout,
	})

	pingCtx, cancel := context.WithTimeout(ctx, cfg.DialTimeout)
	defer cancel()

	if err := rdb.Ping(pingCtx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis at %s: %w", cfg.Addr, err)
	}

	logger.InfoContext(ctx, "redis connected", slog.String("addr", cfg.Addr))
	return &Client{rdb: rdb, ttl: cfg.IdempotencyTTL}, nil
}

func (c *Client) Close() error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Close()
}

// Ping backs the /readyz probe.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.rdb == nil {
		return fmt.Errorf("redis not initialised")
	}
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return c.rdb.Ping(pingCtx).Err()
}

// ClaimIdempotency atomically reserves a request key with SETNX. Using a single
// round trip closes the check-then-write race that an EXISTS followed by a SET
// would leave open between two concurrent replicas.
func (c *Client) ClaimIdempotency(ctx context.Context, key string) (bool, error) {
	return c.rdb.SetNX(ctx, idempotencyKey(key), "processing", c.ttl).Result()
}

// ConfirmIdempotency marks a reservation as durably processed.
func (c *Client) ConfirmIdempotency(ctx context.Context, key string) error {
	return c.rdb.Set(ctx, idempotencyKey(key), "processed", c.ttl).Err()
}

// ReleaseIdempotency rolls a reservation back when the write it guarded failed,
// so an honest retry is not punished as a duplicate.
func (c *Client) ReleaseIdempotency(ctx context.Context, key string) error {
	return c.rdb.Del(ctx, idempotencyKey(key)).Err()
}

func idempotencyKey(key string) string {
	return fmt.Sprintf("fundkit:idempotency:%s", key)
}
