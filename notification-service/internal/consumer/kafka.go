// Package consumer owns the Kafka side of notification-service.
//
// The reader uses explicit FetchMessage/CommitMessages rather than the
// auto-committing ReadMessage: an offset is only advanced once the alert has
// actually been handled, which is what makes redelivery-on-crash meaningful.
//
// That same design is what makes the metrics here worth reading. Because the
// offset only moves on success, a handler failure shows up twice: once as
// kafka_events_consumed_total{status="error"} and again as a kafka_consumer_lag
// that stops falling. A metric that only counted deliveries would show the
// retries as healthy throughput.
//
// Failure handling has three tiers, and which one a message gets is decided by
// the error it produced, not by how many times it has been seen:
//
//  1. transient failure — retried in process, bounded, with exponential
//     backoff, because most of these clear within a second;
//  2. permanent failure (domain.ErrPermanent) — no retries at all, parked
//     immediately, because repetition cannot fix a contract mismatch;
//  3. exhausted or unparseable — parked on the dead-letter topic with the
//     failure metadata attached.
//
// In every one of those the offset moves only after the message is durable
// somewhere. Nothing in this file drops a message.
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

	"github.com/dhanush-cn/fundkit/notification-service/internal/config"
	"github.com/dhanush-cn/fundkit/notification-service/internal/domain"
	"github.com/dhanush-cn/fundkit/notification-service/internal/platform/metrics"
	"github.com/dhanush-cn/fundkit/notification-service/internal/platform/trace"
)

// Handler processes one decoded event.
type Handler interface {
	Handle(ctx context.Context, event domain.OrderEvent) error
}

type OrderEventConsumer struct {
	reader  *kafka.Reader
	handler Handler
	// dlq is where a message goes once this service has established it cannot
	// process it. Nothing is dropped any more; see dlq.go.
	dlq *DeadLetterPublisher
	// recorder is the concrete type rather than an interface: *metrics.Kafka
	// tolerates a nil receiver, so a caller with no registry passes nil and the
	// hot path below stays free of guard clauses.
	recorder     *metrics.Kafka
	topic        string
	group        string
	maxAttempts  int
	retryBackoff time.Duration
	logger       *slog.Logger
}

// New builds the reader. Pass nil for recorder to run uninstrumented.
func New(cfg config.KafkaConfig, handler Handler, dlq *DeadLetterPublisher, recorder *metrics.Kafka, logger *slog.Logger) *OrderEventConsumer {
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
		handler:      handler,
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
// kafka-go derives it from the high water mark the broker reports on each fetch
// against the offset this member has consumed, across the partitions the group
// has assigned to it. It is not inferred from local counters, so it stays
// correct through a rebalance.
//
// Two details this depends on. Reader.Stats() resets the *delta* counters it
// returns (fetches, messages, bytes), which is why nothing else in the service
// is allowed to call it — a second caller would silently halve those numbers.
// Lag and Offset are absolute readings and survive the reset untouched, and Lag
// is the only field read here. Second, a reader that has not completed a fetch
// yet reports a negative lag; that means "unknown", not "ahead of the topic".
// The reading is passed through verbatim and the collector suppresses it, so
// there is exactly one place in the service that decides what is publishable.
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
// reader so the consumer group rebalances promptly instead of waiting for the
// session timeout to expire.
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
	// Timing starts at the top of processing rather than at the handler, so the
	// histogram covers decode, every retry and the commit. Those are the parts
	// that get slow first when the database behind the notifier degrades.
	started := time.Now()

	// The correlation id rides on a Kafka header, so the asynchronous half of a
	// request stays joined to the HTTP call that produced it.
	msgCtx, _ := trace.EnsureContext(ctx, headerValue(message, trace.HeaderKey))

	var event domain.OrderEvent
	if err := json.Unmarshal(message.Value, &event); err != nil {
		// A message we cannot parse will never parse, so it gets no retries. It
		// used to be logged and committed — dropped, with only a log line as
		// evidence. Now it is republished to the DLQ with the decode error
		// attached and the body byte-identical, so someone can look at what was
		// actually on the topic rather than at a paraphrase of it.
		c.deadLetter(msgCtx, message, started, Failure{
			Reason:   metrics.ReasonUnparseable,
			Attempts: 0,
			Err:      err,
		})
		return
	}

	attempts, err := c.handleWithRetries(msgCtx, event)
	if err == nil {
		c.commit(msgCtx, message)
		c.recorder.ObserveConsume(c.topic, metrics.StatusSuccess, started)
		return
	}

	// A permanent failure is recorded under its own reason so the DLQ can be
	// triaged by cause: "retries_exhausted" points at a dependency, whereas
	// "permanent_failure" points at a contract mismatch — usually a producer
	// that has been deployed ahead of its consumers.
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

// handleWithRetries runs the handler until it succeeds, hits a permanent
// failure, or runs out of attempts. It returns the number of attempts actually
// made along with the last error.
//
// The retries are in-process and bounded, and the partition is stalled for the
// whole of it. That is the trade being made: a short stall in exchange for
// surviving the failure mode that actually dominates in production — a provider
// or database blipping for a few hundred milliseconds. Anything longer than
// that does not belong here; it belongs in a retry topic with its own consumer,
// so the stall is paid by a different partition than the live one.
func (c *OrderEventConsumer) handleWithRetries(ctx context.Context, event domain.OrderEvent) (int, error) {
	backoff := c.retryBackoff
	var err error

	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		if attempt > 1 {
			c.recorder.ObserveRetry(c.topic)
		}

		if err = c.handler.Handle(ctx, event); err == nil {
			if attempt > 1 {
				c.logger.InfoContext(ctx, "event handled after retry",
					slog.String("event_id", event.EventID),
					slog.Int("attempts", attempt),
				)
			}
			return attempt, nil
		}

		// No amount of repetition fixes a malformed event or an unreadable
		// schema version, and retrying one only delays the park.
		if errors.Is(err, domain.ErrPermanent) {
			c.logger.ErrorContext(ctx, "permanent handler failure; not retrying",
				slog.String("event_id", event.EventID),
				slog.String("error", err.Error()),
			)
			return attempt, err
		}

		if attempt == c.maxAttempts {
			break
		}

		c.logger.WarnContext(ctx, "handler failed; retrying",
			slog.String("event_id", event.EventID),
			slog.Int("attempt", attempt),
			slog.Int("max_attempts", c.maxAttempts),
			slog.Duration("backoff", backoff),
			slog.String("error", err.Error()),
		)

		// The wait is interruptible. On SIGTERM the consumer should stop
		// sleeping and let the message be redelivered after restart, rather than
		// holding shutdown open for the remaining backoff.
		select {
		case <-ctx.Done():
			return attempt, err
		case <-time.After(backoff):
		}
		backoff *= 2
	}

	c.logger.ErrorContext(ctx, "handler failed after all attempts",
		slog.String("event_id", event.EventID),
		slog.Int("attempts", c.maxAttempts),
		slog.String("error", err.Error()),
	)
	return c.maxAttempts, err
}

// deadLetter parks a message and only then commits its offset.
//
// The ordering is the entire safety property. If the park fails the offset is
// left alone, so the message is still on the source topic and will be
// redelivered — the consumer would rather be stuck and visible (lag rising,
// kafka_dlq_publish_failures_total non-zero) than moving and lossy.
func (c *OrderEventConsumer) deadLetter(ctx context.Context, message kafka.Message, started time.Time, failure Failure) {
	if err := c.dlq.Publish(ctx, message, failure); err != nil {
		// Counted as an error, not as a drop: the offset did not move and the
		// message will come back.
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
