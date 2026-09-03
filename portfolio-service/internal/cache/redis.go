// Package cache fronts the NAV feed with Redis. NAV moves once a day but is
// read on every valuation, which is exactly the read-heavy, low-churn shape a
// short-TTL cache is built for.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package cache

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dhanush-cn/fundkit/portfolio-service/internal/config"
)

type Client struct {
	rdb *redis.Client
	ttl time.Duration
}

func New(ctx context.Context, cfg config.RedisConfig, ttl time.Duration, logger *slog.Logger) (*Client, error) {
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

	logger.InfoContext(ctx, "redis connected",
		slog.String("addr", cfg.Addr),
		slog.Duration("nav_ttl", ttl))
	return &Client{rdb: rdb, ttl: ttl}, nil
}

func (c *Client) Close() error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Close()
}

func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.rdb == nil {
		return fmt.Errorf("redis not initialised")
	}
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return c.rdb.Ping(pingCtx).Err()
}

// GetNAV reports (price, hit, error). A cache miss is not an error: it is the
// normal path that triggers a refresh.
func (c *Client) GetNAV(ctx context.Context, fundID string) (float64, bool, error) {
	value, err := c.rdb.Get(ctx, navKey(fundID)).Result()
	if errors.Is(err, redis.Nil) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}

	price, parseErr := strconv.ParseFloat(value, 64)
	if parseErr != nil {
		return 0, false, parseErr
	}
	return price, true, nil
}

// SetNAV stores a price under the configured TTL.
func (c *Client) SetNAV(ctx context.Context, fundID string, price float64) error {
	return c.rdb.Set(ctx, navKey(fundID), strconv.FormatFloat(price, 'f', 4, 64), c.ttl).Err()
}

func navKey(fundID string) string {
	return "fundkit:nav:" + fundID
}
