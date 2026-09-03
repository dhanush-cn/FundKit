// Package messaging publishes order lifecycle events onto Kafka.
//
// Two decisions worth calling out:
//   - every message is keyed by order id, so all events for one order land on
//     one partition and are therefore consumed in the order they happened;
//   - the correlation id travels as a Kafka header rather than inside the
//     payload, so tracing works even for consumers that never parse the body.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/dhanush-cn/fundkit/order-service/internal/config"
	"github.com/dhanush-cn/fundkit/order-service/internal/domain"
	"github.com/dhanush-cn/fundkit/order-service/internal/platform/trace"
)

// EventOrderStatusChanged and OrderEvent moved into the domain package so the
// service layer can build an envelope inside a database transaction without
// importing Kafka. These aliases keep every existing caller compiling and the
// wire format byte-identical.
const EventOrderStatusChanged = domain.EventOrderStatusChanged

// OrderEvent is a versioned envelope. Wrapping the aggregate rather than
// publishing it raw means new metadata can be added without breaking consumers.
type OrderEvent = domain.OrderEvent

type Publisher struct {
	writer *kafka.Writer
	logger *slog.Logger
}

func NewPublisher(cfg config.KafkaConfig, logger *slog.Logger) *Publisher {
	writer := &kafka.Writer{
		Addr:         kafka.TCP(cfg.Brokers...),
		Topic:        cfg.OrderTopic,
		Balancer:     &kafka.Hash{},
		RequiredAcks: kafka.RequireAll,
		Async:        false,
		BatchTimeout: 50 * time.Millisecond,
	}

	logger.Info("kafka publisher ready",
		slog.Any("brokers", cfg.Brokers),
		slog.String("topic", cfg.OrderTopic),
	)
	return &Publisher{writer: writer, logger: logger}
}

func (p *Publisher) Close() error {
	if p == nil || p.writer == nil {
		return nil
	}
	return p.writer.Close()
}

// PublishOrderStatusChanged emits one lifecycle event.
func (p *Publisher) PublishOrderStatusChanged(ctx context.Context, order domain.Order) error {
	if p == nil || p.writer == nil {
		return nil
	}

	requestID := trace.FromContext(ctx)
	event := OrderEvent{
		EventID:    trace.NewID(),
		EventType:  EventOrderStatusChanged,
		Version:    1,
		OccurredAt: time.Now().UTC(),
		RequestID:  requestID,
		Order:      order,
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal order event: %w", err)
	}

	message := kafka.Message{
		Key:   []byte(order.ID),
		Value: payload,
		Headers: []kafka.Header{
			{Key: trace.HeaderKey, Value: []byte(requestID)},
			{Key: "event-type", Value: []byte(event.EventType)},
		},
	}

	if err := p.writer.WriteMessages(ctx, message); err != nil {
		return fmt.Errorf("write order event: %w", err)
	}

	p.logger.DebugContext(ctx, "order event published",
		slog.String("order_id", order.ID),
		slog.String("status", string(order.Status)),
	)
	return nil
}

// PublishOutbox writes an already-serialised outbox record to Kafka.
//
// It deliberately does no envelope construction: the bytes on the wire are
// exactly the bytes that were committed alongside the order, so an event can
// never drift from the state that produced it.
//
// Routing metadata travels in headers rather than payload fields, so consumers
// that never parse the body can still trace and deduplicate. outbox-id in
// particular is a stable, monotonic dedup key — the relay is at-least-once, so
// notification-service must be able to recognise a replay.
func (p *Publisher) PublishOutbox(ctx context.Context, msg domain.OutboxMessage) error {
	if p == nil || p.writer == nil {
		return nil
	}

	message := kafka.Message{
		// Keying by aggregate id keeps every event for one order on one
		// partition, which is what makes per-order ordering observable.
		Key:   []byte(msg.AggregateID),
		Value: msg.Payload,
		Headers: []kafka.Header{
			{Key: trace.HeaderKey, Value: []byte(msg.RequestID)},
			{Key: "event-type", Value: []byte(msg.EventType)},
			{Key: "aggregate-type", Value: []byte(msg.AggregateType)},
			{Key: "outbox-id", Value: []byte(strconv.FormatInt(msg.ID, 10))},
		},
	}

	if err := p.writer.WriteMessages(ctx, message); err != nil {
		return fmt.Errorf("write outbox message %d: %w", msg.ID, err)
	}

	p.logger.DebugContext(ctx, "outbox message published",
		slog.Int64("outbox_id", msg.ID),
		slog.String("aggregate_id", msg.AggregateID),
		slog.String("event_type", msg.EventType),
		slog.String(trace.HeaderKey, msg.RequestID),
	)
	return nil
}
