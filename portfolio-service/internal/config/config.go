// Package config loads portfolio-service settings from FUNDKIT_-prefixed
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
	Redis   RedisConfig
	NAV     NAVConfig
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
	}

	if cfg.Redis.Addr == "" {
		return Config{}, fmt.Errorf("config: FUNDKIT_REDIS_URL must be set")
	}
	return cfg, nil
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
