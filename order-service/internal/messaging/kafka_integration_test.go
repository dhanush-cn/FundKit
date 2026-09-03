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
	"net"
	"os"
	"strconv"
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

// createTopic provisions the fixture explicitly instead of leaning on the
// broker's auto-creation.
//
// The compose stack sets KAFKA_AUTO_CREATE_TOPICS_ENABLE=true, which is why
// relying on it looks reasonable — but kafka-go's Writer sends its metadata
// requests with AllowAutoTopicCreation unset, so the broker never receives a
// request that would trigger creation. Every publish then fails with
// UNKNOWN_TOPIC_OR_PARTITION until the retry budget runs out, which is a
// twenty-second wait to arrive at a misleading error.
//
// Creating it here is better than flipping AllowAutoTopicCreation on the
// production Writer, which would let a typo in FUNDKIT_KAFKA_ORDER_TOPIC
// silently create a topic nobody consumes. It also puts the partition count
// under the test's control, and this test's ordering claim depends on it.
func createTopic(t *testing.T, brokers []string, topic string) {
	t.Helper()

	conn, err := kafka.Dial("tcp", brokers[0])
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Topic administration has to go to the cluster controller, which on a
	// single-broker stack is this same broker — but asking is what makes the
	// test work against a real multi-broker cluster too.
	controller, err := conn.Controller()
	if err != nil {
		t.Fatalf("locate controller: %v", err)
	}

	controllerConn, err := kafka.Dial("tcp", net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port)))
	if err != nil {
		t.Fatalf("dial controller: %v", err)
	}
	defer func() { _ = controllerConn.Close() }()

	if err := controllerConn.CreateTopics(kafka.TopicConfig{
		Topic:             topic,
		NumPartitions:     1,
		ReplicationFactor: 1,
	}); err != nil {
		t.Fatalf("create topic %s: %v", topic, err)
	}

	// Each run mints a timestamped topic, so without this the broker
	// accumulates one dead topic per CI run forever.
	t.Cleanup(func() { _ = controllerConn.DeleteTopics(topic) })
}

func TestIntegrationPublishedEventIsReadableByAConsumer(t *testing.T) {
	brokers := testBrokers(t)
	topic := "order_events_it_" + time.Now().UTC().Format("150405.000000000")
	createTopic(t, brokers, topic)

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
		Amount:    500000, // paise: ₹5,000.00
		Type:      domain.TypeSIP,
		Status:    domain.StatusExecuted,
	}

	ctx, cancel := context.WithTimeout(trace.WithRequestID(context.Background(), "req-integration-1"), 60*time.Second)
	defer cancel()

	// The topic exists by now, but the writer still has to see it: metadata
	// propagates asynchronously and kafka-go caches what it last fetched. This
	// is a short retry for that race, not a substitute for the topic existing.
	var publishErr error
	for attempt := 0; attempt < 10; attempt++ {
		if publishErr = publisher.PublishOrderStatusChanged(ctx, order); publishErr == nil {
			break
		}
		time.Sleep(time.Second)
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
	// Asserted against the constant rather than a literal. This test hardcoded
	// `!= 1` and broke when the envelope went to v2 for the paise change —
	// which is the failure mode a literal guarantees: the test has to be edited
	// every time the contract moves, and editing it is how a real version
	// regression gets waved through.
	if event.EventType != EventOrderStatusChanged || event.Version != domain.OrderEventVersion {
		t.Fatalf("envelope = %+v", event)
	}
	// The amount is the payload field the v2 bump exists for, so the round trip
	// has to prove the exact integer survives — not merely that something
	// numeric arrived.
	if event.Order.Amount != order.Amount {
		t.Fatalf("amount did not survive the round trip: got %d paise, want %d",
			event.Order.Amount.Paise(), order.Amount.Paise())
	}
	if event.Order.UserEmail != order.UserEmail || event.Order.UserPhone != order.UserPhone {
		t.Fatalf("contact details did not survive the round trip: %+v", event.Order)
	}
}
