// Package consumer owns the Kafka side of notification-service.
//
// The reader uses explicit FetchMessage/CommitMessages rather than the
// auto-committing ReadMessage: an offset is only advanced once the alert has
// actually been handled, which is what makes redelivery-on-crash meaningful.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"

	"github.com/segmentio/kafka-go"

	"github.com/dhanush-cn/fundkit/notification-service/internal/config"
	"github.com/dhanush-cn/fundkit/notification-service/internal/domain"
	"github.com/dhanush-cn/fundkit/notification-service/internal/platform/trace"
)

// Handler processes one decoded event.
type Handler interface {
	Handle(ctx context.Context, event domain.OrderEvent) error
}

type OrderEventConsumer struct {
	reader  *kafka.Reader
	handler Handler
	logger  *slog.Logger
}

func New(cfg config.KafkaConfig, handler Handler, logger *slog.Logger) *OrderEventConsumer {
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

	return &OrderEventConsumer{reader: reader, handler: handler, logger: logger}
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
		return
	}

	if err := c.handler.Handle(msgCtx, event); err != nil {
		c.logger.ErrorContext(msgCtx, "handler failed; offset not committed",
			slog.String("event_id", event.EventID),
			slog.String("error", err.Error()),
		)
		return
	}

	c.commit(msgCtx, message)
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
