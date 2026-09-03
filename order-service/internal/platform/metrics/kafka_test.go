// Engineered by Dhanush C N (github.com/dhanush-cn)
package metrics

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestObservePublishSplitsOutcomes is the alert's contract.
//
// FundKitOutboxPublishFailures fires on
// rate(kafka_events_published_total{status="error"}) > 0, so if a failed write
// were ever recorded as a success the alert would go permanently silent while
// the outbox table filled up behind it. Orders would keep being accepted and
// nothing downstream would ever hear about them.
func TestObservePublishSplitsOutcomes(t *testing.T) {
	registry := New()

	registry.Kafka.ObservePublish("order_events", time.Now(), nil)
	registry.Kafka.ObservePublish("order_events", time.Now(), nil)
	registry.Kafka.ObservePublish("order_events", time.Now(), errors.New("broker unavailable"))

	expected := `
# HELP kafka_events_published_total Order lifecycle events written to Kafka, by topic and outcome.
# TYPE kafka_events_published_total counter
kafka_events_published_total{status="error",topic="order_events"} 1
kafka_events_published_total{status="success",topic="order_events"} 2
`
	if err := testutil.GatherAndCompare(
		registry.Gatherer(), strings.NewReader(expected), "kafka_events_published_total",
	); err != nil {
		t.Fatalf("publish outcomes: %v", err)
	}
}

// TestObservePublishTimesEveryAttempt: a failed write is still a write that
// took time, and with RequiredAcks=RequireAll the slow ones are exactly the
// interesting ones. Timing only the successes would hide a broker that has
// started rejecting after a long wait.
func TestObservePublishTimesEveryAttempt(t *testing.T) {
	registry := New()

	registry.Kafka.ObservePublish("order_events", time.Now(), nil)
	registry.Kafka.ObservePublish("order_events", time.Now(), errors.New("timeout"))

	if observations := histogramObservations(t, registry.Kafka.publishDelay); observations != 2 {
		t.Fatalf("kafka_publish_duration_seconds observations = %d, want 2", observations)
	}
}

func TestObservePublishSeparatesTopics(t *testing.T) {
	registry := New()

	registry.Kafka.ObservePublish("order_events", time.Now(), nil)
	registry.Kafka.ObservePublish("audit_events", time.Now(), nil)

	if count := testutil.CollectAndCount(registry.Kafka.published, "kafka_events_published_total"); count != 2 {
		t.Fatalf("series count = %d, want one per topic", count)
	}
}

// TestNilRecorderIsSafe pins the contract the messaging package relies on:
// NewPublisher accepts a nil recorder so the integration tests can run without
// a registry, and every call site is written free of guard clauses on the
// strength of this. Instrumentation must never be the reason a binary panics.
func TestNilRecorderIsSafe(t *testing.T) {
	var recorder *Kafka

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("nil recorder panicked: %v", recovered)
		}
	}()

	recorder.ObservePublish("order_events", time.Now(), nil)
	recorder.ObservePublish("order_events", time.Now(), errors.New("boom"))
}
