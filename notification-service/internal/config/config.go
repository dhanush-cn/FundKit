// Package config loads notification-service settings from FUNDKIT_-prefixed
// environment variables into typed structs.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	Service ServiceConfig
	Kafka   KafkaConfig
}

type ServiceConfig struct {
	Name            string
	HTTPPort        string
	MetricsPort     string // admin listener for /metrics, deliberately not HTTPPort
	LogLevel        string
	ShutdownTimeout time.Duration
}

type KafkaConfig struct {
	Brokers      []string
	OrderTopic   string
	GroupID      string
	MinBytes     int
	MaxBytes     int
	MaxWait      time.Duration
	StartOffset  string
	CommitPolicy string

	// DLQTopic receives messages this service could not process. Kept as its
	// own topic rather than a status field on the original: a poison message
	// must leave the partition entirely, or it blocks every message behind it.
	DLQTopic string

	// MaxAttempts is how many times one message is handed to the handler before
	// it is dead-lettered, counting the first try. 3 is the default because the
	// failures worth retrying are transient provider errors, and a failure that
	// survives three attempts a few hundred milliseconds apart is not one of
	// them — it is a bad message or a dependency that is properly down, and in
	// both cases further retries only deepen the backlog.
	MaxAttempts int

	// RetryBackoff is the base delay between attempts; the consumer doubles it
	// each time. Bounded and short by design: this is in-process retry, so the
	// partition is stalled for the whole of it.
	RetryBackoff time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		Service: ServiceConfig{
			Name:            envString("SERVICE_NAME", "notification-service", "SERVICE_NAME"),
			HTTPPort:        envString("HTTP_PORT", "8083", "PORT"),
			MetricsPort:     envString("METRICS_PORT", "9100", ""),
			LogLevel:        envString("LOG_LEVEL", "info", "LOG_LEVEL"),
			ShutdownTimeout: envDuration("SHUTDOWN_TIMEOUT", 15*time.Second),
		},
		Kafka: KafkaConfig{
			Brokers:      envList("KAFKA_BROKERS", []string{"localhost:29092"}, "KAFKA_BROKERS"),
			OrderTopic:   envString("KAFKA_ORDER_TOPIC", "order_events", ""),
			GroupID:      envString("KAFKA_CONSUMER_GROUP", "fundkit-notification-workers", ""),
			MinBytes:     envInt("KAFKA_MIN_BYTES", 1),
			MaxBytes:     envInt("KAFKA_MAX_BYTES", 10e6),
			MaxWait:      envDuration("KAFKA_MAX_WAIT", 500*time.Millisecond),
			StartOffset:  envString("KAFKA_START_OFFSET", "last", ""),
			DLQTopic:     envString("KAFKA_DLQ_TOPIC", "order_events_dlq", ""),
			MaxAttempts:  envInt("KAFKA_MAX_ATTEMPTS", 3),
			RetryBackoff: envDuration("KAFKA_RETRY_BACKOFF", 200*time.Millisecond),
		},
	}

	if len(cfg.Kafka.Brokers) == 0 {
		return Config{}, fmt.Errorf("config: FUNDKIT_KAFKA_BROKERS must contain at least one broker")
	}
	if cfg.Kafka.DLQTopic == "" {
		return Config{}, fmt.Errorf("config: FUNDKIT_KAFKA_DLQ_TOPIC must not be empty")
	}
	if cfg.Kafka.DLQTopic == cfg.Kafka.OrderTopic {
		// Catching this at boot rather than at the first failure: a DLQ pointed
		// at its own source topic turns one poison message into an infinite
		// republish loop that fills the disk.
		return Config{}, fmt.Errorf("config: FUNDKIT_KAFKA_DLQ_TOPIC must differ from FUNDKIT_KAFKA_ORDER_TOPIC (both are %q)", cfg.Kafka.DLQTopic)
	}
	if cfg.Kafka.MaxAttempts < 1 {
		return Config{}, fmt.Errorf("config: FUNDKIT_KAFKA_MAX_ATTEMPTS must be at least 1, got %d", cfg.Kafka.MaxAttempts)
	}
	return cfg, nil
}

func envString(key, fallback, legacy string) string {
	if value := strings.TrimSpace(os.Getenv("FUNDKIT_" + key)); value != "" {
		return value
	}
	if legacy != "" {
		if value := strings.TrimSpace(os.Getenv(legacy)); value != "" {
			return value
		}
	}
	return fallback
}

func envList(key string, fallback []string, legacy string) []string {
	raw := envString(key, "", legacy)
	if raw == "" {
		return fallback
	}

	items := make([]string, 0, 4)
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	if len(items) == 0 {
		return fallback
	}
	return items
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw := envString(key, "", "")
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	raw := envString(key, "", "")
	if raw == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(raw, "%d", &parsed); err != nil {
		return fallback
	}
	return parsed
}
