// Package consumer — the dead-letter path.
//
// A consumer has exactly three things it can do with a message it cannot
// process: retry it forever, drop it, or park it somewhere else. Retrying
// forever stalls the partition and every message behind it, which turns one bad
// order into a service-wide outage. Dropping it loses a customer's event with
// no record that it ever existed — which is what this service used to do with
// an unparseable payload, with a comment admitting as much.
//
// Parking is the third option and the only defensible one: the message is
// republished, byte for byte, onto a separate topic together with the metadata
// needed to understand why it failed, and only then is the original offset
// committed. Nothing is lost, the partition keeps moving, and the backlog of
// things a human needs to look at is a queue with a depth you can alert on.
//
// The one rule that makes this safe: if the DLQ publish itself fails, the
// offset must NOT be committed. Committing after a failed park is the same
// silent drop with extra steps.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package consumer

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/dhanush-cn/fundkit/notification-service/internal/config"
	"github.com/dhanush-cn/fundkit/notification-service/internal/platform/metrics"
)

// Header keys written onto every dead-lettered message. They are headers rather
// than a JSON wrapper around the payload for one reason: the payload has to stay
// byte-identical to what was published, so it can be replayed onto the source
// topic without being unwrapped first. Anything this service wants to say about
// the failure therefore has to go beside the body, not inside it.
const (
	HeaderDLQReason      = "dlq-reason"
	HeaderDLQError       = "dlq-error"
	HeaderDLQAttempts    = "dlq-attempts"
	HeaderDLQFailedAt    = "dlq-failed-at"
	HeaderDLQOriginTopic = "dlq-origin-topic"
	HeaderDLQOriginPart  = "dlq-origin-partition"
	HeaderDLQOriginOff   = "dlq-origin-offset"
	HeaderDLQConsumer    = "dlq-consumer-group"
)

// maxErrorHeaderBytes caps the recorded error. A wrapped error chain from a
// provider SDK can run to kilobytes, and Kafka enforces a total message size;
// a park that fails because the *explanation* was too long would be a poor way
// to lose a message.
const maxErrorHeaderBytes = 512

// dlqPublishTimeout bounds the park attempt independently of the caller's
// context. See Publish for why that matters.
const dlqPublishTimeout = 10 * time.Second

// Failure is what this service knows about why a message could not be handled.
type Failure struct {
	// Reason is one of the metrics.Reason* constants: a low-cardinality label
	// suitable for a Prometheus series and for a triage query on the DLQ.
	Reason string
	// Attempts is how many times the handler actually ran. Zero for a message
	// that never reached the handler, such as one that would not parse.
	Attempts int
	// Err is the failure itself, recorded verbatim (truncated) for a human.
	Err error
}

// DeadLetterPublisher writes unprocessable messages to the DLQ topic.
type DeadLetterPublisher struct {
	writer *kafka.Writer
	topic  string
	group  string
	// recorder is the concrete type rather than an interface, matching the rest
	// of the service: *metrics.Kafka tolerates a nil receiver, so a test that
	// builds no registry passes nil and the code below needs no guards.
	recorder *metrics.Kafka
	logger   *slog.Logger
}

// NewDeadLetterPublisher builds the DLQ writer.
//
// RequiredAcks is RequireAll and Async is false, which are stronger settings
// than a throughput-oriented producer would choose — deliberately. This writer
// carries the only surviving copy of a message that is about to be committed
// away on the source topic, so "the leader has it" is not good enough; the
// write has to be on every in-sync replica before the caller is allowed to
// treat the park as done.
func NewDeadLetterPublisher(cfg config.KafkaConfig, recorder *metrics.Kafka, logger *slog.Logger) *DeadLetterPublisher {
	writer := &kafka.Writer{
		Addr:         kafka.TCP(cfg.Brokers...),
		Topic:        cfg.DLQTopic,
		Balancer:     &kafka.Hash{},
		RequiredAcks: kafka.RequireAll,
		Async:        false,
		BatchTimeout: 50 * time.Millisecond,
	}

	logger.Info("dead-letter publisher ready",
		slog.String("topic", cfg.DLQTopic),
		slog.Int("max_attempts", cfg.MaxAttempts),
		slog.Duration("retry_backoff", cfg.RetryBackoff),
	)

	return &DeadLetterPublisher{
		writer:   writer,
		topic:    cfg.DLQTopic,
		group:    cfg.GroupID,
		recorder: recorder,
		logger:   logger,
	}
}

// Topic reports the destination, read from the writer so a metric label cannot
// drift from where the messages actually go.
func (p *DeadLetterPublisher) Topic() string {
	if p == nil || p.writer == nil {
		return "unknown"
	}
	return p.writer.Topic
}

func (p *DeadLetterPublisher) Close() error {
	if p == nil || p.writer == nil {
		return nil
	}
	return p.writer.Close()
}

// Publish parks one message on the dead-letter topic.
//
// The returned error is the caller's signal to hold the offset back. There is
// no fallback path and no swallowing: either this returns nil and the message
// is durable somewhere, or it returns an error and the message must be left on
// the source topic to be redelivered.
func (p *DeadLetterPublisher) Publish(ctx context.Context, original kafka.Message, failure Failure) error {
	if p == nil || p.writer == nil {
		return fmt.Errorf("dead-letter publisher not configured")
	}

	// The park is detached from the caller's context and given its own timeout.
	// During shutdown the consumer's context is already cancelled, and a message
	// that has just exhausted its retries is exactly the one that must not be
	// abandoned because the process happened to be stopping. Detaching keeps the
	// values — so the correlation id still rides along — while the timeout keeps
	// shutdown bounded.
	publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dlqPublishTimeout)
	defer cancel()

	if err := p.writer.WriteMessages(publishCtx, p.message(original, failure)); err != nil {
		p.recorder.ObserveDLQPublishFailure(p.Topic())
		p.logger.ErrorContext(ctx, "failed to publish to dead-letter topic; holding offset",
			slog.String("dlq_topic", p.Topic()),
			slog.String("origin_topic", original.Topic),
			slog.Int64("origin_offset", original.Offset),
			slog.String("error", err.Error()),
		)
		return fmt.Errorf("publish to dead-letter topic %s: %w", p.Topic(), err)
	}

	p.recorder.ObserveDeadLetter(p.Topic(), failure.Reason)
	p.logger.WarnContext(ctx, "event dead-lettered",
		slog.String("dlq_topic", p.Topic()),
		slog.String("reason", failure.Reason),
		slog.Int("attempts", failure.Attempts),
		slog.String("origin_topic", original.Topic),
		slog.Int("origin_partition", original.Partition),
		slog.Int64("origin_offset", original.Offset),
		slog.String("error", errorText(failure.Err)),
	)
	return nil
}

// message builds the record that will be published. It is separated from
// Publish so the thing that matters most about it — that the payload and key
// come through untouched — can be asserted without a broker.
func (p *DeadLetterPublisher) message(original kafka.Message, failure Failure) kafka.Message {
	return kafka.Message{
		// Same key as the original, so an order's events stay co-partitioned on
		// the DLQ too and a replay preserves per-order ordering.
		Key: original.Key,
		// The body is copied through untouched. A DLQ whose payloads have been
		// reformatted is a DLQ you cannot replay.
		Value:   original.Value,
		Headers: p.headers(original, failure),
	}
}

// headers builds the DLQ metadata, preserving the original headers underneath
// it. The originals are kept because they are part of the message: event-type,
// outbox-id and the correlation id are what make a parked message traceable
// back to the HTTP request that caused it, months later.
func (p *DeadLetterPublisher) headers(original kafka.Message, failure Failure) []kafka.Header {
	headers := make([]kafka.Header, 0, len(original.Headers)+8)
	headers = append(headers, original.Headers...)

	return append(headers,
		kafka.Header{Key: HeaderDLQReason, Value: []byte(failure.Reason)},
		kafka.Header{Key: HeaderDLQError, Value: []byte(truncate(errorText(failure.Err), maxErrorHeaderBytes))},
		kafka.Header{Key: HeaderDLQAttempts, Value: []byte(strconv.Itoa(failure.Attempts))},
		kafka.Header{Key: HeaderDLQFailedAt, Value: []byte(time.Now().UTC().Format(time.RFC3339Nano))},
		kafka.Header{Key: HeaderDLQOriginTopic, Value: []byte(original.Topic)},
		kafka.Header{Key: HeaderDLQOriginPart, Value: []byte(strconv.Itoa(original.Partition))},
		kafka.Header{Key: HeaderDLQOriginOff, Value: []byte(strconv.FormatInt(original.Offset, 10))},
		kafka.Header{Key: HeaderDLQConsumer, Value: []byte(p.group)},
	)
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…(truncated)"
}
