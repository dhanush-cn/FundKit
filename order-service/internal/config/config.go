// Package config loads order-service settings from the environment into typed
// structs. Every key is namespaced with the FUNDKIT_ prefix so the service can
// share a host or a Kubernetes pod without colliding with unrelated variables.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// Config is the fully resolved configuration for one process.
type Config struct {
	Service   ServiceConfig
	Database  DatabaseConfig
	Redis     RedisConfig
	Kafka     KafkaConfig
	Portfolio PortfolioConfig
	Outbox    OutboxConfig
}

type ServiceConfig struct {
	Name            string
	HTTPPort        string
	LogLevel        string
	ShutdownTimeout time.Duration
	RequestTimeout  time.Duration
}

type DatabaseConfig struct {
	URL             string
	ConnectTimeout  time.Duration
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

type RedisConfig struct {
	Addr           string
	Password       string
	DB             int
	DialTimeout    time.Duration
	IdempotencyTTL time.Duration
}

type KafkaConfig struct {
	Brokers    []string
	OrderTopic string
}

// OutboxConfig tunes the transactional outbox relay worker.
type OutboxConfig struct {
	PollInterval  time.Duration
	BatchSize     int
	MaxAttempts   int
	MaxBackoff    time.Duration
	ShutdownFlush time.Duration
}

type PortfolioConfig struct {
	GRPCAddr    string
	DialTimeout time.Duration
	CallTimeout time.Duration
}

// Load reads and validates the environment. It fails fast: a service that
// cannot describe its own dependencies should never reach the ready state.
func Load() (Config, error) {
	cfg := Config{
		Service: ServiceConfig{
			Name:            envString("SERVICE_NAME", "order-service", "SERVICE_NAME"),
			HTTPPort:        envString("HTTP_PORT", "8081", "PORT"),
			LogLevel:        envString("LOG_LEVEL", "info", "LOG_LEVEL"),
			ShutdownTimeout: envDuration("SHUTDOWN_TIMEOUT", 15*time.Second),
			RequestTimeout:  envDuration("REQUEST_TIMEOUT", 10*time.Second),
		},
		Database: DatabaseConfig{
			URL:             databaseURL(),
			ConnectTimeout:  envDuration("DB_CONNECT_TIMEOUT", 10*time.Second),
			MaxOpenConns:    envInt("DB_MAX_OPEN_CONNS", 25),
			MaxIdleConns:    envInt("DB_MAX_IDLE_CONNS", 5),
			ConnMaxLifetime: envDuration("DB_CONN_MAX_LIFETIME", time.Hour),
		},
		Redis: RedisConfig{
			Addr:           envString("REDIS_URL", "localhost:6379", "REDIS_URL"),
			Password:       envString("REDIS_PASSWORD", "", "REDIS_PASSWORD"),
			DB:             envInt("REDIS_DB", 0),
			DialTimeout:    envDuration("REDIS_DIAL_TIMEOUT", 5*time.Second),
			IdempotencyTTL: envDuration("IDEMPOTENCY_TTL", 24*time.Hour),
		},
		Kafka: KafkaConfig{
			Brokers:    envList("KAFKA_BROKERS", []string{"localhost:9092"}, "KAFKA_BROKERS"),
			OrderTopic: envString("KAFKA_ORDER_TOPIC", "order_events", ""),
		},
		Portfolio: PortfolioConfig{
			GRPCAddr:    envString("PORTFOLIO_GRPC_URL", "localhost:50051", "PORTFOLIO_GRPC_URL"),
			DialTimeout: envDuration("PORTFOLIO_DIAL_TIMEOUT", 5*time.Second),
			CallTimeout: envDuration("PORTFOLIO_CALL_TIMEOUT", 3*time.Second),
		},
		Outbox: OutboxConfig{
			PollInterval:  envDuration("OUTBOX_POLL_INTERVAL", time.Second),
			BatchSize:     envInt("OUTBOX_BATCH_SIZE", 100),
			MaxAttempts:   envInt("OUTBOX_MAX_ATTEMPTS", 10),
			MaxBackoff:    envDuration("OUTBOX_MAX_BACKOFF", 30*time.Second),
			ShutdownFlush: envDuration("OUTBOX_SHUTDOWN_FLUSH", 5*time.Second),
		},
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	if c.Database.URL == "" {
		return fmt.Errorf("config: FUNDKIT_DB_URL (or FUNDKIT_DB_HOST/DB_NAME/DB_USER) must be set")
	}
	if len(c.Kafka.Brokers) == 0 {
		return fmt.Errorf("config: FUNDKIT_KAFKA_BROKERS must contain at least one broker")
	}
	if c.Redis.Addr == "" {
		return fmt.Errorf("config: FUNDKIT_REDIS_URL must be set")
	}
	if c.Outbox.BatchSize <= 0 {
		return fmt.Errorf("config: FUNDKIT_OUTBOX_BATCH_SIZE must be greater than zero")
	}
	if c.Outbox.PollInterval <= 0 {
		return fmt.Errorf("config: FUNDKIT_OUTBOX_POLL_INTERVAL must be greater than zero")
	}
	return nil
}

// databaseURL accepts either a ready-made DSN or discrete parts, which is what
// Kubernetes secrets and managed Postgres offerings tend to hand you.
func databaseURL() string {
	if dsn := envString("DB_URL", "", "DB_URL"); dsn != "" {
		return dsn
	}

	host := envString("DB_HOST", "", "DB_HOST")
	name := envString("DB_NAME", "", "DB_NAME")
	user := envString("DB_USER", "", "DB_USER")
	if host == "" || name == "" || user == "" {
		return ""
	}

	dsn := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, envString("DB_PASSWORD", "", "DB_PASSWORD")),
		Host:   fmt.Sprintf("%s:%s", host, envString("DB_PORT", "5432", "DB_PORT")),
		Path:   "/" + name,
	}
	query := url.Values{}
	query.Set("sslmode", envString("DB_SSLMODE", "disable", "DB_SSLMODE"))
	dsn.RawQuery = query.Encode()
	return dsn.String()
}

// envString resolves FUNDKIT_<key> first and only then the pre-refactor name,
// so an existing deployment keeps working while operators migrate.
func envString(key, fallback string, legacy string) string {
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
