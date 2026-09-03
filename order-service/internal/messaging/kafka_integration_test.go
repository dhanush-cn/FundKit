//go:build integration

// Integration test for the Kafka publisher.
//
// It publishes a real lifecycle event and reads it back off the broker, which
// is the only way to verify the two things consumers actually depend on: that
// the payload matches the schema notification-service decodes, and that the
// correlation id rides on a header rather than inside the body.
//
// Run with: go test -tags=integration ./...
// Requires: FUNDKIT_TEST_KAFKA_BROKERS (comma-separated host:port).
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package messaging

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/dhanush-cn/fundkit/order-service/internal/config"
	"github.com/dhanush-cn/fundkit/order-service/internal/domain"
	"github.com/dhanush-cn/fundkit/order-service/internal/platform/trace"
)

func testBrokers(t *testing.T) []string {
	t.Helper()

	raw := os.Getenv("FUNDKIT_TEST_KAFKA_BROKERS")
	if raw == "" {
		t.Skip("FUNDKIT_TEST_KAFKA_BROKERS is not set; skipping the Kafka integration suite")
	}

	brokers := make([]string, 0, 2)
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			brokers = append(brokers, trimmed)
		}
	}
	return brokers
}

func TestIntegrationPublishedEventIsReadableByAConsumer(t *testing.T) {
	brokers := testBrokers(t)
	topic := "order_events_it_" + time.Now().UTC().Format("150405.000000000")

	publisher := NewPublisher(config.KafkaConfig{
		Brokers:    brokers,
		OrderTopic: topic,
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = publisher.Close() })

	order := domain.Order{
		ID:        "order-it-1",
		UserID:    "user-1",
		UserName:  "Dhanush C N",
		UserEmail: "dhanush@example.com",
		UserPhone: "+919876543210",
		FundID:    "quant-small-cap-fund",
		Amount:    5000,
		Type:      domain.TypeSIP,
		Status:    domain.StatusExecuted,
	}

	ctx, cancel := context.WithTimeout(trace.WithRequestID(context.Background(), "req-integration-1"), 60*time.Second)
	defer cancel()

	// Auto-topic-creation can need a moment on a cold broker, so the first
	// write is retried rather than failing the suite on a start-up race.
	var publishErr error
	for attempt := 0; attempt < 10; attempt++ {
		if publishErr = publisher.PublishOrderStatusChanged(ctx, order); publishErr == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if publishErr != nil {
		t.Fatalf("publish: %v", publishErr)
	}

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     brokers,
		Topic:       topic,
		Partition:   0,
		MinBytes:    1,
		MaxBytes:    10e6,
		StartOffset: kafka.FirstOffset,
	})
	t.Cleanup(func() { _ = reader.Close() })

	readCtx, readCancel := context.WithTimeout(ctx, 45*time.Second)
	defer readCancel()

	message, err := reader.ReadMessage(readCtx)
	if err != nil {
		t.Fatalf("read published message: %v", err)
	}

	// Keyed by order id, so every event for one order lands on one partition
	// and is therefore consumed in the order it happened.
	if string(message.Key) != order.ID {
		t.Fatalf("message key = %q, want the order id", string(message.Key))
	}

	var header string
	for _, item := range message.Headers {
		if item.Key == trace.HeaderKey {
			header = string(item.Value)
		}
	}
	if header != "req-integration-1" {
		t.Fatalf("%s header = %q, want the correlation id to survive the hop", trace.HeaderKey, header)
	}

	var event OrderEvent
	if err := json.Unmarshal(message.Value, &event); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if event.EventType != EventOrderStatusChanged || event.Version != 1 {
		t.Fatalf("envelope = %+v", event)
	}
	if event.Order.UserEmail != order.UserEmail || event.Order.UserPhone != order.UserPhone {
		t.Fatalf("contact details did not survive the round trip: %+v", event.Order)
	}
}
