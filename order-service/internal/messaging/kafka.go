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
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/dhanush-cn/fundkit/order-service/internal/config"
	"github.com/dhanush-cn/fundkit/order-service/internal/domain"
	"github.com/dhanush-cn/fundkit/order-service/internal/platform/trace"
)

// EventType names the contract shared with notification-service.
const EventOrderStatusChanged = "order.status_changed"

// OrderEvent is a versioned envelope. Wrapping the aggregate rather than
// publishing it raw means new metadata can be added without breaking consumers.
type OrderEvent struct {
	EventID    string       `json:"event_id"`
	EventType  string       `json:"event_type"`
	Version    int          `json:"version"`
	OccurredAt time.Time    `json:"occurred_at"`
	RequestID  string       `json:"request_id,omitempty"`
	Order      domain.Order `json:"order"`
}

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
