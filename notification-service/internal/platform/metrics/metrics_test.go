// Engineered by Dhanush C N (github.com/dhanush-cn)
package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// TestObserveConsumeKeepsDroppedSeparateFromError is the distinction the whole
// consumer design rests on.
//
//   - error   → the offset was NOT committed; the message comes back.
//   - dropped → the offset WAS committed; the message is gone forever.
//
// Folding them into one bucket would bury a poison-message leak inside a retry
// rate that never clears, and FundKitPoisonMessages would never fire.
func TestObserveConsumeKeepsDroppedSeparateFromError(t *testing.T) {
	registry := New()

	registry.Kafka.ObserveConsume("order_events", StatusSuccess, time.Now())
	registry.Kafka.ObserveConsume("order_events", StatusSuccess, time.Now())
	registry.Kafka.ObserveConsume("order_events", StatusError, time.Now())
	registry.Kafka.ObserveConsume("order_events", StatusDropped, time.Now())

	expected := `
# HELP kafka_events_consumed_total Order lifecycle events read from Kafka, by topic and outcome (success, error, dropped).
# TYPE kafka_events_consumed_total counter
kafka_events_consumed_total{status="dropped",topic="order_events"} 1
kafka_events_consumed_total{status="error",topic="order_events"} 1
kafka_events_consumed_total{status="success",topic="order_events"} 2
`
	if err := testutil.GatherAndCompare(
		registry.Gatherer(), strings.NewReader(expected), "kafka_events_consumed_total",
	); err != nil {
		t.Fatalf("consume outcomes: %v", err)
	}

	if observations := histogramObservations(t, registry.Kafka.processing); observations != 4 {
		t.Fatalf("kafka_event_processing_duration_seconds observations = %d, want 4", observations)
	}
}

// TestConsumerLagIsReadAtScrapeTime is what separates a collector from a polled
// gauge. The snapshot function must be called on every gather, so a value that
// changes between scrapes is actually reflected rather than frozen at whatever
// it was when the process started.
func TestConsumerLagIsReadAtScrapeTime(t *testing.T) {
	registry := New()

	lag := 42.0
	// Atomic because a registry gathers its collectors concurrently, and this
	// suite runs under -race in CI.
	var calls atomic.Int64
	registry.Kafka.RegisterConsumerLag(func() []LagSample {
		calls.Add(1)
		return []LagSample{{Topic: "order_events", Group: "fundkit-notification-workers", Lag: lag}}
	})

	expected := `
# HELP kafka_consumer_lag Messages this consumer group is behind the head of the topic. The backlog, not the rate.
# TYPE kafka_consumer_lag gauge
kafka_consumer_lag{group="fundkit-notification-workers",topic="order_events"} 42
`
	if err := testutil.GatherAndCompare(
		registry.Gatherer(), strings.NewReader(expected), "kafka_consumer_lag",
	); err != nil {
		t.Fatalf("first scrape: %v", err)
	}

	lag = 7
	expected = strings.Replace(expected, "} 42", "} 7", 1)
	if err := testutil.GatherAndCompare(
		registry.Gatherer(), strings.NewReader(expected), "kafka_consumer_lag",
	); err != nil {
		t.Fatalf("second scrape did not re-read the source: %v", err)
	}

	if got := calls.Load(); got < 2 {
		t.Fatalf("snapshot called %d times across two scrapes; the value is being cached", got)
	}
}

// TestNegativeLagIsSuppressed: kafka-go reports a negative lag before the
// reader has completed its first fetch. That means "not known yet", and
// exporting it as zero would tell the dashboard the consumer is fully caught up
// at precisely the moment nobody can say whether it is.
func TestNegativeLagIsSuppressed(t *testing.T) {
	registry := New()
	registry.Kafka.RegisterConsumerLag(func() []LagSample {
		return []LagSample{{Topic: "order_events", Group: "fundkit-notification-workers", Lag: -1}}
	})

	if err := testutil.GatherAndCompare(
		registry.Gatherer(), strings.NewReader(""), "kafka_consumer_lag",
	); err != nil {
		t.Fatalf("an unknown lag was exported anyway: %v", err)
	}
}

// TestLagCollectorHandlesAnAbsentReader: a snapshot returning nothing must
// export nothing rather than a zero. A gauge that disappears is a truthful
// signal — Prometheus shows a gap, and `absent()` can alert on it.
func TestLagCollectorHandlesAnAbsentReader(t *testing.T) {
	registry := New()
	registry.Kafka.RegisterConsumerLag(func() []LagSample { return nil })

	if err := testutil.GatherAndCompare(
		registry.Gatherer(), strings.NewReader(""), "kafka_consumer_lag",
	); err != nil {
		t.Fatalf("expected no series at all: %v", err)
	}
}

// TestNilRecorderIsSafe pins the contract consumer.New relies on: it accepts a
// nil recorder so the consumer can run uninstrumented, and the hot path is
// written free of guard clauses on the strength of this.
func TestNilRecorderIsSafe(t *testing.T) {
	var recorder *Kafka

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("nil recorder panicked: %v", recovered)
		}
	}()

	recorder.ObserveConsume("order_events", StatusSuccess, time.Now())
	recorder.RegisterConsumerLag(func() []LagSample { return nil })
}

// TestRegisterConsumerLagIgnoresANilSnapshot: registering a collector whose
// source is nil would panic on the first scrape, turning /metrics into a 500
// and blinding every panel at once.
func TestRegisterConsumerLagIgnoresANilSnapshot(t *testing.T) {
	registry := New()
	registry.Kafka.RegisterConsumerLag(nil)

	recorder := httptest.NewRecorder()
	NewServer(":0", registry).Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", recorder.Code)
	}
}

func TestMetricsServerExposesPrometheusExposition(t *testing.T) {
	registry := New()
	registry.Kafka.ObserveConsume("order_events", StatusSuccess, time.Now())

	recorder := httptest.NewRecorder()
	NewServer(":0", registry).Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", recorder.Code)
	}
	for _, want := range []string{
		`kafka_events_consumed_total{status="success",topic="order_events"} 1`,
		"go_goroutines",
	} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Errorf("/metrics body is missing %q", want)
		}
	}
}

// --------------------------------------------------------------------- helpers

// histogramObservations totals the sample count across every series a histogram
// collector holds. testutil has no helper for this because a histogram's _sum
// is timing-dependent and therefore cannot be compared against fixed text.
func histogramObservations(t *testing.T, collector prometheus.Collector) uint64 {
	t.Helper()

	samples := make(chan prometheus.Metric)
	go func() {
		collector.Collect(samples)
		close(samples)
	}()

	var total uint64
	for sample := range samples {
		var snapshot dto.Metric
		if err := sample.Write(&snapshot); err != nil {
			t.Fatalf("write metric: %v", err)
		}
		total += snapshot.GetHistogram().GetSampleCount()
	}
	return total
}
