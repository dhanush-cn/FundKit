// Package config loads portfolio-service settings from FUNDKIT_-prefixed
// environment variables into typed structs.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Service ServiceConfig
	Redis   RedisConfig
	NAV     NAVConfig
	Kafka   KafkaConfig
	Ledger  LedgerConfig
}

type ServiceConfig struct {
	Name            string
	GRPCPort        string
	HTTPPort        string
	MetricsPort     string // admin listener for /metrics, deliberately not HTTPPort
	LogLevel        string
	ShutdownTimeout time.Duration
	RequestTimeout  time.Duration
}

type RedisConfig struct {
	Addr        string
	Password    string
	DB          int
	DialTimeout time.Duration
}

type NAVConfig struct {
	CacheTTL time.Duration
}

// KafkaConfig mirrors notification-service's, field for field and default for
// default, because both services consume the same topic and an operator should
// not have to remember which one spells a setting differently. Only the
// consumer group differs: two groups on one topic means each service gets its
// own copy of every event and its own independent offsets, which is the entire
// reason a notification outage cannot stall the ledger.
type KafkaConfig struct {
	Brokers     []string
	OrderTopic  string
	GroupID     string
	MinBytes    int
	MaxBytes    int
	MaxWait     time.Duration
	StartOffset string

	// DLQTopic receives events this service could not apply. Its own topic
	// rather than a retry in place: a poison event blocks every event behind it
	// on the same partition, and on a ledger that means one customer's bad
	// order freezes everyone else's positions too.
	DLQTopic string

	// MaxAttempts is how many times one event is handed to the projection
	// before it is parked, counting the first try.
	MaxAttempts int

	// RetryBackoff is the base delay between attempts; the consumer doubles it
	// each time. Short and bounded, because the partition is stalled throughout.
	RetryBackoff time.Duration
}

// LedgerConfig covers how the in-memory holdings store starts up.
type LedgerConfig struct {
	// SeedHoldings loads the demo positions for user-1 and user-2 at boot.
	//
	// It defaults to true so `docker compose up` gives a dashboard with
	// something on it, and k8s/config.yaml sets it false: seeded positions are
	// not derived from any event, so a cluster replaying the topic would stack
	// real holdings on top of invented ones and total the two together.
	SeedHoldings bool
}

func Load() (Config, error) {
	cfg := Config{
		Service: ServiceConfig{
			Name:            envString("SERVICE_NAME", "portfolio-service", "SERVICE_NAME"),
			GRPCPort:        envString("GRPC_PORT", "50051", "GRPC_PORT"),
			HTTPPort:        envString("HTTP_PORT", "8082", "PORT"),
			MetricsPort:     envString("METRICS_PORT", "9100", ""),
			LogLevel:        envString("LOG_LEVEL", "info", "LOG_LEVEL"),
			ShutdownTimeout: envDuration("SHUTDOWN_TIMEOUT", 15*time.Second),
			RequestTimeout:  envDuration("REQUEST_TIMEOUT", 5*time.Second),
		},
		Redis: RedisConfig{
			Addr:        envString("REDIS_URL", "localhost:6379", "REDIS_URL"),
			Password:    envString("REDIS_PASSWORD", "", "REDIS_PASSWORD"),
			DB:          envInt("REDIS_DB", 0),
			DialTimeout: envDuration("REDIS_DIAL_TIMEOUT", 5*time.Second),
		},
		NAV: NAVConfig{
			CacheTTL: envDuration("NAV_CACHE_TTL", 30*time.Second),
		},
		Kafka: KafkaConfig{
			Brokers:      envList("KAFKA_BROKERS", []string{"localhost:29092"}, "KAFKA_BROKERS"),
			OrderTopic:   envString("KAFKA_ORDER_TOPIC", "order_events", ""),
			GroupID:      envString("KAFKA_CONSUMER_GROUP", "fundkit-portfolio-workers", ""),
			MinBytes:     envInt("KAFKA_MIN_BYTES", 1),
			MaxBytes:     envInt("KAFKA_MAX_BYTES", 10e6),
			MaxWait:      envDuration("KAFKA_MAX_WAIT", 500*time.Millisecond),
			StartOffset:  envString("KAFKA_START_OFFSET", "last", ""),
			DLQTopic:     envString("KAFKA_DLQ_TOPIC", "order_events_portfolio_dlq", ""),
			MaxAttempts:  envInt("KAFKA_MAX_ATTEMPTS", 3),
			RetryBackoff: envDuration("KAFKA_RETRY_BACKOFF", 200*time.Millisecond),
		},
		Ledger: LedgerConfig{
			SeedHoldings: envBool("SEED_HOLDINGS", true),
		},
	}

	if cfg.Redis.Addr == "" {
		return Config{}, fmt.Errorf("config: FUNDKIT_REDIS_URL must be set")
	}
	if len(cfg.Kafka.Brokers) == 0 {
		return Config{}, fmt.Errorf("config: FUNDKIT_KAFKA_BROKERS must contain at least one broker")
	}
	if cfg.Kafka.OrderTopic == "" {
		return Config{}, fmt.Errorf("config: FUNDKIT_KAFKA_ORDER_TOPIC must not be empty")
	}
	if cfg.Kafka.DLQTopic == "" {
		return Config{}, fmt.Errorf("config: FUNDKIT_KAFKA_DLQ_TOPIC must not be empty")
	}
	if cfg.Kafka.DLQTopic == cfg.Kafka.OrderTopic {
		// The same boot-time check notification-service makes, for the same
		// reason: a DLQ pointed at its own source topic turns one poison event
		// into an infinite republish loop that fills the disk.
		return Config{}, fmt.Errorf("config: FUNDKIT_KAFKA_DLQ_TOPIC must differ from FUNDKIT_KAFKA_ORDER_TOPIC (both are %q)", cfg.Kafka.DLQTopic)
	}
	if cfg.Kafka.GroupID == "" {
		return Config{}, fmt.Errorf("config: FUNDKIT_KAFKA_CONSUMER_GROUP must not be empty")
	}
	if cfg.Kafka.MaxAttempts < 1 {
		return Config{}, fmt.Errorf("config: FUNDKIT_KAFKA_MAX_ATTEMPTS must be at least 1, got %d", cfg.Kafka.MaxAttempts)
	}
	return cfg, nil
}

// envList reads a comma-separated setting into a slice, dropping empty entries
// so a trailing comma is not read as a broker named "".
func envList(key string, fallback []string, legacy string) []string {
	raw := envString(key, "", legacy)
	if raw == "" {
		return fallback
	}

	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	if len(values) == 0 {
		return fallback
	}
	return values
}

// envBool reads a boolean setting. An unparseable value falls back to the
// default rather than failing the boot, matching envInt and envDuration above.
func envBool(key string, fallback bool) bool {
	raw := envString(key, "", "")
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

// envString resolves FUNDKIT_<key> first, then the pre-refactor variable name,
// so a running deployment can migrate without a flag day.
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
