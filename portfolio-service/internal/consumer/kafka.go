// Package consumer owns the Kafka side of portfolio-service.
//
// This is the service's second write path, and the first one that is not driven
// by a request. Holdings used to be a hardcoded map read by the valuation API;
// they are now a projection of the order_events stream, built as fills arrive.
//
// The mechanics are deliberately identical to notification-service's consumer —
// explicit FetchMessage and CommitMessages rather than the auto-committing
// ReadMessage, bounded in-process retries, dead-letter on exhaustion — because
// two consumers on one topic behaving differently under failure is how an
// incident gets misdiagnosed. What differs is the consequence of getting it
// wrong, and that is worth stating plainly:
//
//   - notification-service processing an event twice sends a duplicate email;
//   - portfolio-service processing an event twice doubles a customer's
//     position, and every rupee figure on their dashboard with it.
//
// At-least-once delivery is a property of the transport and cannot be argued
// away here, so the projection is made idempotent instead: every event carries
// an id and the ledger refuses to apply one it has already seen. That check
// lives in the repository, under the same lock as the mutation, not in this
// file — a consumer cannot make a non-idempotent write safe no matter how it
// commits.
//
// The commit ordering is the other half. An offset moves only after the fill is
// in the ledger or durable on the DLQ, so a crash costs a redelivery (absorbed
// by the dedup set) rather than a lost position.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/dhanush-cn/fundkit/portfolio-service/internal/config"
	"github.com/dhanush-cn/fundkit/portfolio-service/internal/domain"
	"github.com/dhanush-cn/fundkit/portfolio-service/internal/platform/metrics"
	"github.com/dhanush-cn/fundkit/portfolio-service/internal/platform/trace"
)

// Projector applies one decoded event to the ledger. It reports whether the
// ledger actually changed, which separates "applied" from the two kinds of
// successful no-op: an event this service does not act on, and a redelivery it
// has already applied.
type Projector interface {
	ApplyOrderEvent(ctx context.Context, event domain.OrderEvent) (bool, error)
}

type OrderEventConsumer struct {
	reader    *kafka.Reader
	projector Projector
	// dlq is where an event goes once this service has established it cannot
	// apply it. Nothing is dropped; see dlq.go.
	dlq *DeadLetterPublisher
	// recorder is the concrete type rather than an interface: *metrics.Kafka
	// tolerates a nil receiver, so a test with no registry passes nil and the
	// hot path below stays free of guard clauses.
	recorder     *metrics.Kafka
	topic        string
	group        string
	maxAttempts  int
	retryBackoff time.Duration
	logger       *slog.Logger
}

// New builds the reader. Pass nil for recorder to run uninstrumented.
func New(cfg config.KafkaConfig, projector Projector, dlq *DeadLetterPublisher, recorder *metrics.Kafka, logger *slog.Logger) *OrderEventConsumer {
	startOffset := kafka.LastOffset
	if cfg.StartOffset == "first" {
		startOffset = kafka.FirstOffset
	}

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     cfg.Brokers,
		Topic:       cfg.OrderTopic,
		GroupID:     cfg.GroupID,
		MinBytes:    cfg.MinBytes,
		MaxBytes:    cfg.MaxBytes,
		MaxWait:     cfg.MaxWait,
		StartOffset: startOffset,
	})

	c := &OrderEventConsumer{
		reader:       reader,
		projector:    projector,
		dlq:          dlq,
		recorder:     recorder,
		topic:        cfg.OrderTopic,
		group:        cfg.GroupID,
		maxAttempts:  cfg.MaxAttempts,
		retryBackoff: cfg.RetryBackoff,
		logger:       logger,
	}

	// Lag is published as a collector rather than a polled gauge, so the value
	// Prometheus receives is read from the reader during the scrape itself.
	recorder.RegisterConsumerLag(c.LagSamples)

	return c
}

// LagSamples reads the reader's own statistics. The number is broker-sourced:
// kafka-go derives it from the high water mark reported on each fetch against
// the offset this member has consumed, so it stays correct through a rebalance.
//
// Reader.Stats() resets the delta counters it returns, which is why nothing else
// in this service may call it — a second caller would silently halve those
// numbers. Lag is an absolute reading and survives the reset. A reader that has
// not completed a fetch reports a negative lag, meaning "unknown"; the reading
// is passed through verbatim and the collector decides what is publishable, so
// there is exactly one place that makes that judgement.
func (c *OrderEventConsumer) LagSamples() []metrics.LagSample {
	if c == nil || c.reader == nil {
		return nil
	}

	return []metrics.LagSample{{
		Topic: c.topic,
		Group: c.group,
		Lag:   float64(c.reader.Stats().Lag),
	}}
}

func (c *OrderEventConsumer) Close() error {
	if c == nil || c.reader == nil {
		return nil
	}
	return c.reader.Close()
}

// Run blocks until the context is cancelled. Cancellation is the shutdown
// signal: the in-flight fetch unblocks, the loop exits, and main closes the
// reader so the group rebalances promptly instead of waiting out the session
// timeout with a partition nobody is consuming.
func (c *OrderEventConsumer) Run(ctx context.Context) error {
	c.logger.InfoContext(ctx, "kafka consumer started",
		slog.String("topic", c.reader.Config().Topic),
		slog.String("group", c.reader.Config().GroupID),
	)

	for {
		message, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
				c.logger.Info("kafka consumer stopping")
				return nil
			}
			c.logger.ErrorContext(ctx, "kafka fetch failed", slog.String("error", err.Error()))
			continue
		}

		c.process(ctx, message)
	}
}

func (c *OrderEventConsumer) process(ctx context.Context, message kafka.Message) {
	// Timing starts at the top of processing rather than at the projection, so
	// the histogram covers decode, every retry and the commit — the parts that
	// get slow first when the NAV feed behind the projection degrades.
	started := time.Now()

	// The correlation id rides on a Kafka header, so the position built here
	// stays joined to the HTTP request that placed the order.
	msgCtx, _ := trace.EnsureContext(ctx, headerValue(message, trace.HeaderKey))

	var event domain.OrderEvent
	if err := json.Unmarshal(message.Value, &event); err != nil {
		// An event that will not parse will never parse, so it gets no retries.
		// It is republished byte-identical to the DLQ with the decode error
		// attached, so someone can look at what was actually on the topic
		// rather than at a paraphrase of it in a log line.
		c.deadLetter(msgCtx, message, started, Failure{
			Reason:   metrics.ReasonUnparseable,
			Attempts: 0,
			Err:      err,
		})
		return
	}

	applied, attempts, err := c.applyWithRetries(msgCtx, event)
	if err == nil {
		c.commit(msgCtx, message)
		// Skipped events are counted separately from applied ones. Most of this
		// topic is PENDING and PROCESSING transitions that belong to
		// notification-service, so folding them into "success" would make the
		// ledger's throughput graph mostly noise and hide the moment fills stop
		// arriving.
		status := metrics.StatusSkipped
		if applied {
			status = metrics.StatusSuccess
		}
		c.recorder.ObserveConsume(c.topic, status, started)
		return
	}

	// A permanent failure gets its own reason so the DLQ can be triaged by
	// cause: "retries_exhausted" points at a dependency — usually the NAV feed —
	// whereas "permanent_failure" points at a contract mismatch or an event that
	// contradicts the ledger, such as a sale of a position the user never held.
	reason := metrics.ReasonExhausted
	if errors.Is(err, domain.ErrPermanent) {
		reason = metrics.ReasonPermanent
	}

	c.deadLetter(msgCtx, message, started, Failure{
		Reason:   reason,
		Attempts: attempts,
		Err:      err,
	})
}

// applyWithRetries runs the projection until it succeeds, hits a permanent
// failure, or runs out of attempts. It returns whether the ledger changed, how
// many attempts were actually made, and the last error.
//
// The retries are in-process and bounded, and the partition is stalled for the
// whole of it. That is the trade: a short stall in exchange for surviving the
// failure that actually dominates — a NAV feed or a cache blipping for a few
// hundred milliseconds. Anything longer belongs on a retry topic with its own
// consumer, so the stall is paid by a different partition than the live one.
func (c *OrderEventConsumer) applyWithRetries(ctx context.Context, event domain.OrderEvent) (bool, int, error) {
	backoff := c.retryBackoff
	var err error

	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		if attempt > 1 {
			c.recorder.ObserveRetry(c.topic)
		}

		var applied bool
		if applied, err = c.projector.ApplyOrderEvent(ctx, event); err == nil {
			if attempt > 1 {
				c.logger.InfoContext(ctx, "event applied after retry",
					slog.String("event_id", event.EventID),
					slog.Int("attempts", attempt),
				)
			}
			return applied, attempt, nil
		}

		// A malformed event, an unreadable version or a sale that contradicts
		// the ledger will fail identically on every attempt; retrying one only
		// delays the park and holds up the partition while it does.
		if errors.Is(err, domain.ErrPermanent) {
			c.logger.ErrorContext(ctx, "permanent projection failure; not retrying",
				slog.String("event_id", event.EventID),
				slog.String("error", err.Error()),
			)
			return false, attempt, err
		}

		if attempt == c.maxAttempts {
			break
		}

		c.logger.WarnContext(ctx, "projection failed; retrying",
			slog.String("event_id", event.EventID),
			slog.Int("attempt", attempt),
			slog.Int("max_attempts", c.maxAttempts),
			slog.Duration("backoff", backoff),
			slog.String("error", err.Error()),
		)

		// The wait is interruptible. On SIGTERM the consumer should stop
		// sleeping and let the event be redelivered after restart rather than
		// holding shutdown open for the remaining backoff.
		select {
		case <-ctx.Done():
			return false, attempt, err
		case <-time.After(backoff):
		}
		backoff *= 2
	}

	c.logger.ErrorContext(ctx, "projection failed after all attempts",
		slog.String("event_id", event.EventID),
		slog.Int("attempts", c.maxAttempts),
		slog.String("error", err.Error()),
	)
	return false, c.maxAttempts, err
}

// deadLetter parks an event and only then commits its offset.
//
// The ordering is the entire safety property. If the park fails the offset is
// left alone, so the event is still on the source topic and will be
// redelivered — this consumer would rather be stuck and visible (lag rising,
// kafka_dlq_publish_failures_total non-zero) than moving and lossy.
func (c *OrderEventConsumer) deadLetter(ctx context.Context, message kafka.Message, started time.Time, failure Failure) {
	if err := c.dlq.Publish(ctx, message, failure); err != nil {
		// Counted as an error, not as a drop: the offset did not move and the
		// event will come back.
		c.recorder.ObserveConsume(c.topic, metrics.StatusError, started)
		return
	}

	c.commit(ctx, message)
	c.recorder.ObserveConsume(c.topic, metrics.StatusDeadLettered, started)
}

func (c *OrderEventConsumer) commit(ctx context.Context, message kafka.Message) {
	if err := c.reader.CommitMessages(context.WithoutCancel(ctx), message); err != nil {
		c.logger.ErrorContext(ctx, "failed to commit offset",
			slog.Int64("offset", message.Offset),
			slog.String("error", err.Error()),
		)
	}
}

func headerValue(message kafka.Message, key string) string {
	for _, header := range message.Headers {
		if header.Key == key {
			return string(header.Value)
		}
	}
	return ""
}
