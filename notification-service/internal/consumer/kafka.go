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
	// recorder is the concrete type rather than an interface: *metrics.Kafka
	// tolerates a nil receiver, so a caller with no registry passes nil and the
	// hot path below stays free of guard clauses.
	recorder *metrics.Kafka
	topic    string
	group    string
	logger   *slog.Logger
}

// New builds the reader. Pass nil for recorder to run uninstrumented.
func New(cfg config.KafkaConfig, handler Handler, recorder *metrics.Kafka, logger *slog.Logger) *OrderEventConsumer {
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
		reader:   reader,
		handler:  handler,
		recorder: recorder,
		topic:    cfg.OrderTopic,
		group:    cfg.GroupID,
		logger:   logger,
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
	// histogram covers decode and commit as well. Those are the parts that get
	// slow first when the database behind the notifier degrades.
	started := time.Now()

	// The correlation id rides on a Kafka header, so the asynchronous half of a
	// request stays joined to the HTTP call that produced it.
	msgCtx, _ := trace.EnsureContext(ctx, headerValue(message, trace.HeaderKey))

	var event domain.OrderEvent
	if err := json.Unmarshal(message.Value, &event); err != nil {
		// A message we cannot parse will never parse. Commit it rather than
		// blocking the partition forever; a production system would route it to
		// a dead-letter topic here.
		c.logger.ErrorContext(msgCtx, "discarding unparseable event",
			slog.String("error", err.Error()),
			slog.Int64("offset", message.Offset),
		)
		c.commit(msgCtx, message)
		// Dropped, not errored: the offset moved on and the message is gone.
		// Counting it as an error would hide a poison-message leak inside a
		// retry rate that never clears.
		c.recorder.ObserveConsume(c.topic, metrics.StatusDropped, started)
		return
	}

	if err := c.handler.Handle(msgCtx, event); err != nil {
		c.logger.ErrorContext(msgCtx, "handler failed; offset not committed",
			slog.String("event_id", event.EventID),
			slog.String("error", err.Error()),
		)
		c.recorder.ObserveConsume(c.topic, metrics.StatusError, started)
		return
	}

	c.commit(msgCtx, message)
	c.recorder.ObserveConsume(c.topic, metrics.StatusSuccess, started)
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
