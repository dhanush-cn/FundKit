// Package relay drains the transactional outbox onto Kafka.
//
// It is the second half of the outbox pattern: the write path makes events
// durable, and this worker makes them visible. It runs in-process rather than
// as a separate deployment because it shares the service's database
// credentials and lifecycle, and because FOR UPDATE SKIP LOCKED makes one copy
// per replica safe — each claims a disjoint batch.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package relay

import (
	"context"
	"log/slog"
	"time"

	"github.com/dhanush-cn/fundkit/order-service/internal/domain"
)

// Store is the persistence port. It exposes no gorm types, so the worker is
// testable against a plain fake with no database.
type Store interface {
	PublishBatch(
		ctx context.Context,
		limit, maxAttempts int,
		publish func(context.Context, domain.OutboxMessage) error,
	) (domain.OutboxBatchResult, error)
}

// Publisher is the broker port.
type Publisher interface {
	PublishOutbox(ctx context.Context, msg domain.OutboxMessage) error
}

// Config tunes the polling loop.
type Config struct {
	Interval      time.Duration // idle poll period
	BatchSize     int           // rows claimed per transaction
	MaxAttempts   int           // publish failures before a record is retired
	MaxBackoff    time.Duration // ceiling when the broker is unhealthy
	ShutdownFlush time.Duration // final drain budget on SIGTERM
}

func (c Config) withDefaults() Config {
	if c.Interval <= 0 {
		c.Interval = time.Second
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 100
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 10
	}
	if c.MaxBackoff < c.Interval {
		c.MaxBackoff = 30 * time.Second
	}
	if c.ShutdownFlush <= 0 {
		c.ShutdownFlush = 5 * time.Second
	}
	return c
}

// Relay polls the outbox and publishes what it finds.
type Relay struct {
	store  Store
	pub    Publisher
	cfg    Config
	logger *slog.Logger
	done   chan struct{}
}

func New(store Store, pub Publisher, cfg Config, logger *slog.Logger) *Relay {
	return &Relay{
		store:  store,
		pub:    pub,
		cfg:    cfg.withDefaults(),
		logger: logger,
		done:   make(chan struct{}),
	}
}

// Run blocks until ctx is cancelled. Cancellation triggers one final drain on a
// detached context, so events committed moments before SIGTERM still reach
// Kafka inside the shutdown window instead of waiting for the next boot.
func (r *Relay) Run(ctx context.Context) {
	defer close(r.done)

	r.logger.Info("outbox relay started",
		slog.Duration("interval", r.cfg.Interval),
		slog.Int("batch_size", r.cfg.BatchSize),
		slog.Int("max_attempts", r.cfg.MaxAttempts),
	)

	backoff := r.cfg.Interval
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			r.flush()
			r.logger.Info("outbox relay stopped")
			return
		case <-timer.C:
		}

		if err := r.drain(ctx); err != nil {
			backoff = nextBackoff(backoff, r.cfg.MaxBackoff)
			r.logger.Error("outbox drain failed",
				slog.String("error", err.Error()),
				slog.Duration("retry_in", backoff),
			)
		} else {
			backoff = r.cfg.Interval
		}

		timer.Reset(backoff)
	}
}

// Wait blocks until Run has returned, including its final flush. main uses it
// to hold the process open until the backlog is clear.
func (r *Relay) Wait() { <-r.done }

// drain publishes batches until the backlog is empty, so a burst of orders is
// cleared in one pass rather than one batch per tick.
func (r *Relay) drain(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}

		res, err := r.store.PublishBatch(ctx, r.cfg.BatchSize, r.cfg.MaxAttempts, r.pub.PublishOutbox)
		if err != nil {
			return err
		}
		if res.Claimed == 0 {
			return nil
		}

		r.logger.Info("outbox batch relayed",
			slog.Int("claimed", res.Claimed),
			slog.Int("published", res.Published),
			slog.Int("failed", res.Failed),
		)

		// Nothing got through: treat it as broker trouble and back off rather
		// than spinning on a full batch of failures.
		if res.Published == 0 {
			return res.Err
		}
		if res.Claimed < r.cfg.BatchSize {
			return nil
		}
	}
}

// flush is the shutdown drain. It runs on a fresh context because the one Run
// was given has already been cancelled — the point is to publish what is
// already committed, on a bounded budget, before the process exits.
func (r *Relay) flush() {
	flushCtx, cancel := context.WithTimeout(context.Background(), r.cfg.ShutdownFlush)
	defer cancel()

	if err := r.drain(flushCtx); err != nil {
		r.logger.Warn("outbox flush incomplete on shutdown",
			slog.String("error", err.Error()))
		return
	}
	r.logger.Info("outbox flushed on shutdown")
}

func nextBackoff(current, max time.Duration) time.Duration {
	next := current * 2
	if next > max {
		return max
	}
	return next
}
