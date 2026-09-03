// Package config loads api-gateway settings from FUNDKIT_-prefixed environment
// variables into typed structs.
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

type Config struct {
	Service   ServiceConfig
	Auth      AuthConfig
	Database  DatabaseConfig
	RateLimit RateLimitConfig
	CORS      CORSConfig
	Upstreams UpstreamConfig
}

type ServiceConfig struct {
	Name            string
	HTTPPort        string
	LogLevel        string
	ShutdownTimeout time.Duration
	ProxyTimeout    time.Duration
}

type AuthConfig struct {
	JWTSecret string
	TokenTTL  time.Duration
	Issuer    string
	// BcryptCost is tunable because the right work factor depends on the
	// hardware: high enough to be expensive for an attacker, low enough that a
	// login does not stall a request thread.
	BcryptCost int
}

// DatabaseConfig points the gateway at the identity store. The gateway owns the
// users table and nothing else in that database.
type DatabaseConfig struct {
	URL             string
	ConnectTimeout  time.Duration
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

type RateLimitConfig struct {
	RequestsPerSecond float64
	Burst             int
	ClientTTL         time.Duration
}

type CORSConfig struct {
	AllowedOrigins []string
}

type UpstreamConfig struct {
	OrderServiceURL string
	HealthTargets   []HealthTarget
	HealthTimeout   time.Duration
}

type HealthTarget struct {
	Name string
	URL  string
}

func Load() (Config, error) {
	cfg := Config{
		Service: ServiceConfig{
			Name:            envString("SERVICE_NAME", "api-gateway", "SERVICE_NAME"),
			HTTPPort:        envString("HTTP_PORT", "8080", "PORT"),
			LogLevel:        envString("LOG_LEVEL", "info", "LOG_LEVEL"),
			ShutdownTimeout: envDuration("SHUTDOWN_TIMEOUT", 15*time.Second),
			ProxyTimeout:    envDuration("PROXY_TIMEOUT", 15*time.Second),
		},
		Auth: AuthConfig{
			JWTSecret: envString("JWT_SECRET", "", "JWT_SECRET"),
			TokenTTL:  envDuration("JWT_TTL", 24*time.Hour),
			Issuer:    envString("JWT_ISSUER", "fundkit-api-gateway", ""),
			// 10 is bcrypt's default: roughly 50-100ms per hash on commodity
			// hardware, which an attacker pays on every single guess.
			BcryptCost: envInt("BCRYPT_COST", 10),
		},
		Database: DatabaseConfig{
			URL:             databaseURL(),
			ConnectTimeout:  envDuration("DB_CONNECT_TIMEOUT", 10*time.Second),
			MaxOpenConns:    envInt("DB_MAX_OPEN_CONNS", 15),
			MaxIdleConns:    envInt("DB_MAX_IDLE_CONNS", 5),
			ConnMaxLifetime: envDuration("DB_CONN_MAX_LIFETIME", time.Hour),
		},
		RateLimit: RateLimitConfig{
			RequestsPerSecond: envFloat("RATE_LIMIT_RPS", 5),
			Burst:             envInt("RATE_LIMIT_BURST", 10),
			ClientTTL:         envDuration("RATE_LIMIT_CLIENT_TTL", 10*time.Minute),
		},
		CORS: CORSConfig{
			AllowedOrigins: envList("CORS_ALLOWED_ORIGINS", []string{"http://localhost:5173"}, ""),
		},
		Upstreams: UpstreamConfig{
			OrderServiceURL: envString("ORDER_SERVICE_URL", "http://localhost:8081", "ORDER_SERVICE_URL"),
			HealthTimeout:   envDuration("HEALTH_TIMEOUT", 2*time.Second),
		},
	}

	cfg.Upstreams.HealthTargets = []HealthTarget{
		{Name: "order-service", URL: envString("ORDER_SERVICE_HEALTH_URL", "http://localhost:8081/readyz", "ORDER_SERVICE_HEALTH_URL")},
		{Name: "portfolio-service", URL: envString("PORTFOLIO_SERVICE_HEALTH_URL", "http://localhost:8082/readyz", "PORTFOLIO_SERVICE_HEALTH_URL")},
		{Name: "notification-service", URL: envString("NOTIFICATION_SERVICE_HEALTH_URL", "http://localhost:8083/readyz", "NOTIFICATION_SERVICE_HEALTH_URL")},
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	// A signing key with a built-in default is a signing key that reaches
	// production. The gateway refuses to start without an explicit secret.
	if strings.TrimSpace(c.Auth.JWTSecret) == "" {
		return fmt.Errorf("config: FUNDKIT_JWT_SECRET must be set")
	}
	if len(c.Auth.JWTSecret) < 16 {
		return fmt.Errorf("config: FUNDKIT_JWT_SECRET must be at least 16 characters")
	}
	if c.Upstreams.OrderServiceURL == "" {
		return fmt.Errorf("config: FUNDKIT_ORDER_SERVICE_URL must be set")
	}
	if c.Database.URL == "" {
		return fmt.Errorf("config: FUNDKIT_DB_URL (or FUNDKIT_DB_HOST/DB_NAME/DB_USER) must be set")
	}
	return nil
}

// databaseURL accepts either a ready-made DSN or the discrete parts that
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

func envFloat(key string, fallback float64) float64 {
	raw := envString(key, "", "")
	if raw == "" {
		return fallback
	}
	var parsed float64
	if _, err := fmt.Sscanf(raw, "%g", &parsed); err != nil {
		return fallback
	}
	return parsed
}
