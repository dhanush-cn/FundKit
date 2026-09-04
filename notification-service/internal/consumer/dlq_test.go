// Engineered by Dhanush C N (github.com/dhanush-cn)
package consumer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/dhanush-cn/fundkit/notification-service/internal/domain"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// handlerFunc adapts a function to the Handler interface.
type handlerFunc func(context.Context, domain.OrderEvent) error

func (f handlerFunc) Handle(ctx context.Context, event domain.OrderEvent) error {
	return f(ctx, event)
}

// testConsumer builds a consumer with no reader and no writer. Everything under
// test here is the decision logic — how many times to try, and what to do when
// the tries run out — which is deliberately separable from the broker.
func testConsumer(handler Handler, maxAttempts int) *OrderEventConsumer {
	return &OrderEventConsumer{
		handler:      handler,
		topic:        "order_events",
		group:        "test-group",
		maxAttempts:  maxAttempts,
		retryBackoff: time.Millisecond,
		logger:       discardLogger(),
	}
}

func TestHandleWithRetriesStopsOnSuccess(t *testing.T) {
	t.Parallel()

	calls := 0
	consumer := testConsumer(handlerFunc(func(context.Context, domain.OrderEvent) error {
		calls++
		if calls < 2 {
			return errors.New("smtp timeout")
		}
		return nil
	}), 3)

	attempts, err := consumer.handleWithRetries(context.Background(), domain.OrderEvent{})
	if err != nil {
		t.Fatalf("err = %v, want nil once the handler recovers", err)
	}
	if attempts != 2 || calls != 2 {
		t.Fatalf("attempts = %d, calls = %d, want 2 and 2", attempts, calls)
	}
}

func TestHandleWithRetriesExhaustsThenGivesUp(t *testing.T) {
	t.Parallel()

	calls := 0
	consumer := testConsumer(handlerFunc(func(context.Context, domain.OrderEvent) error {
		calls++
		return errors.New("smtp timeout")
	}), 3)

	attempts, err := consumer.handleWithRetries(context.Background(), domain.OrderEvent{})
	if err == nil {
		t.Fatal("want the last error to surface so the message is dead-lettered")
	}
	if calls != 3 || attempts != 3 {
		t.Fatalf("calls = %d, attempts = %d, want 3 and 3", calls, attempts)
	}
}

// The distinction the retry policy is built on: repetition cannot fix a
// contract violation, so a permanent error must not consume the retry budget
// while the partition waits behind it.
func TestHandleWithRetriesDoesNotRetryPermanentFailures(t *testing.T) {
	t.Parallel()

	calls := 0
	consumer := testConsumer(handlerFunc(func(context.Context, domain.OrderEvent) error {
		calls++
		return fmt.Errorf("%w: version 1", domain.ErrPermanent)
	}), 5)

	attempts, err := consumer.handleWithRetries(context.Background(), domain.OrderEvent{})
	if !errors.Is(err, domain.ErrPermanent) {
		t.Fatalf("err = %v, want it to wrap domain.ErrPermanent", err)
	}
	if calls != 1 || attempts != 1 {
		t.Fatalf("calls = %d, attempts = %d, want exactly one attempt", calls, attempts)
	}
}

// On SIGTERM the consumer should stop waiting out its backoff and let the
// message be redelivered, rather than holding shutdown open.
func TestHandleWithRetriesAbandonsBackoffOnCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	consumer := testConsumer(handlerFunc(func(context.Context, domain.OrderEvent) error {
		calls++
		cancel()
		return errors.New("smtp timeout")
	}), 10)
	consumer.retryBackoff = time.Hour // would hang if cancellation were ignored

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := consumer.handleWithRetries(ctx, domain.OrderEvent{}); err == nil {
			t.Error("want the error to survive cancellation")
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleWithRetries ignored context cancellation and sat in its backoff")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 before cancellation took effect", calls)
	}
}

func TestDeadLetterHeadersCarryTheFailureMetadata(t *testing.T) {
	t.Parallel()

	publisher := &DeadLetterPublisher{group: "fundkit-notification-workers"}
	original := kafka.Message{
		Topic:     "order_events",
		Partition: 3,
		Offset:    4242,
		Key:       []byte("order-1"),
		Value:     []byte(`{"malformed":`),
		Headers: []kafka.Header{
			{Key: "x-request-id", Value: []byte("req-abc")},
			{Key: "event-type", Value: []byte("order.status_changed")},
		},
	}

	headers := publisher.headers(original, Failure{
		Reason:   "unparseable",
		Attempts: 0,
		Err:      errors.New("unexpected end of JSON input"),
	})

	got := map[string]string{}
	for _, header := range headers {
		got[header.Key] = string(header.Value)
	}

	// The originals survive. Without them a parked message cannot be traced back
	// to the request that produced it, which is most of its value.
	if got["x-request-id"] != "req-abc" {
		t.Errorf("x-request-id = %q, want it preserved", got["x-request-id"])
	}
	if got["event-type"] != "order.status_changed" {
		t.Errorf("event-type = %q, want it preserved", got["event-type"])
	}

	// Enough metadata to find the original message on the source topic.
	for key, want := range map[string]string{
		HeaderDLQReason:      "unparseable",
		HeaderDLQAttempts:    "0",
		HeaderDLQOriginTopic: "order_events",
		HeaderDLQOriginPart:  "3",
		HeaderDLQOriginOff:   "4242",
		HeaderDLQConsumer:    "fundkit-notification-workers",
	} {
		if got[key] != want {
			t.Errorf("%s = %q, want %q", key, got[key], want)
		}
	}
	if !strings.Contains(got[HeaderDLQError], "unexpected end of JSON input") {
		t.Errorf("%s = %q, want the decode error recorded", HeaderDLQError, got[HeaderDLQError])
	}
	if got[HeaderDLQFailedAt] == "" {
		t.Error("a parked message must record when it failed")
	}
}

// A provider error chain can run to kilobytes. Failing to park a message because
// the explanation was too long for the broker would be a poor way to lose it.
func TestDeadLetterErrorHeaderIsTruncated(t *testing.T) {
	t.Parallel()

	publisher := &DeadLetterPublisher{group: "g"}
	headers := publisher.headers(kafka.Message{}, Failure{
		Err: errors.New(strings.Repeat("x", 4096)),
	})

	for _, header := range headers {
		if header.Key == HeaderDLQError && len(header.Value) > maxErrorHeaderBytes+32 {
			t.Fatalf("error header is %d bytes, want it truncated near %d", len(header.Value), maxErrorHeaderBytes)
		}
	}
}

// The payload is the one thing that must not be touched: a DLQ whose bodies
// have been reformatted cannot be replayed onto the source topic.
func TestDeadLetterPreservesThePayloadAndKey(t *testing.T) {
	t.Parallel()

	body := []byte(`{"event_id":"evt-1","order":{"amount":500000}}`)
	original := kafka.Message{
		Topic: "order_events",
		Key:   []byte("order-1"),
		Value: body,
	}

	publisher := &DeadLetterPublisher{group: "g"}
	parked := publisher.message(original, Failure{Reason: "retries_exhausted", Attempts: 3})

	if string(parked.Value) != string(body) {
		t.Fatalf("payload = %s, want it byte-identical to the original", parked.Value)
	}
	if string(parked.Key) != "order-1" {
		t.Fatal("the key must be preserved so a replay keeps per-order ordering")
	}
	if len(parked.Headers) == 0 {
		t.Fatal("the parked message must carry failure metadata")
	}
}

func TestPublishWithoutAWriterIsAnErrorNotASilentDrop(t *testing.T) {
	t.Parallel()

	var publisher *DeadLetterPublisher
	if err := publisher.Publish(context.Background(), kafka.Message{}, Failure{}); err == nil {
		t.Fatal("an unconfigured publisher must report failure so the offset is held back")
	}
}
