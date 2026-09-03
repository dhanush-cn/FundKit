// Engineered by Dhanush C N (github.com/dhanush-cn)
package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaultsTheDeadLetterSettings(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.Kafka.DLQTopic != "order_events_dlq" {
		t.Errorf("DLQTopic = %q, want order_events_dlq", cfg.Kafka.DLQTopic)
	}
	if cfg.Kafka.MaxAttempts != 3 {
		t.Errorf("MaxAttempts = %d, want 3", cfg.Kafka.MaxAttempts)
	}
	if cfg.Kafka.RetryBackoff != 200*time.Millisecond {
		t.Errorf("RetryBackoff = %v, want 200ms", cfg.Kafka.RetryBackoff)
	}
}

// The misconfiguration worth catching at boot rather than at the first failure:
// a dead-letter topic pointed at its own source turns one poison message into
// an infinite republish loop that fills the disk.
func TestLoadRejectsADeadLetterTopicThatFeedsItself(t *testing.T) {
	t.Setenv("FUNDKIT_KAFKA_ORDER_TOPIC", "order_events")
	t.Setenv("FUNDKIT_KAFKA_DLQ_TOPIC", "order_events")

	_, err := Load()
	if err == nil {
		t.Fatal("want an error when the DLQ topic equals the source topic")
	}
	if !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("error = %v, want it to explain the collision", err)
	}
}

func TestLoadRejectsANonPositiveAttemptBudget(t *testing.T) {
	t.Setenv("FUNDKIT_KAFKA_MAX_ATTEMPTS", "0")

	if _, err := Load(); err == nil {
		t.Fatal("want an error: zero attempts would mean every message is dead-lettered unread")
	}
}

func TestLoadReadsTheDeadLetterOverrides(t *testing.T) {
	t.Setenv("FUNDKIT_KAFKA_DLQ_TOPIC", "order_events_parking_lot")
	t.Setenv("FUNDKIT_KAFKA_MAX_ATTEMPTS", "5")
	t.Setenv("FUNDKIT_KAFKA_RETRY_BACKOFF", "1s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.Kafka.DLQTopic != "order_events_parking_lot" {
		t.Errorf("DLQTopic = %q", cfg.Kafka.DLQTopic)
	}
	if cfg.Kafka.MaxAttempts != 5 {
		t.Errorf("MaxAttempts = %d, want 5", cfg.Kafka.MaxAttempts)
	}
	if cfg.Kafka.RetryBackoff != time.Second {
		t.Errorf("RetryBackoff = %v, want 1s", cfg.Kafka.RetryBackoff)
	}
}
