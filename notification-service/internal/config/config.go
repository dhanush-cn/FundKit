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
			Brokers:     envList("KAFKA_BROKERS", []string{"localhost:29092"}, "KAFKA_BROKERS"),
			OrderTopic:  envString("KAFKA_ORDER_TOPIC", "order_events", ""),
			GroupID:     envString("KAFKA_CONSUMER_GROUP", "fundkit-notification-workers", ""),
			MinBytes:    envInt("KAFKA_MIN_BYTES", 1),
			MaxBytes:    envInt("KAFKA_MAX_BYTES", 10e6),
			MaxWait:     envDuration("KAFKA_MAX_WAIT", 500*time.Millisecond),
			StartOffset: envString("KAFKA_START_OFFSET", "last", ""),
		},
	}

	if len(cfg.Kafka.Brokers) == 0 {
		return Config{}, fmt.Errorf("config: FUNDKIT_KAFKA_BROKERS must contain at least one broker")
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
