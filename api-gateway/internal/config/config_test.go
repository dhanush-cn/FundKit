// Engineered by Dhanush C N (github.com/dhanush-cn)
package config

import (
	"strings"
	"testing"
	"time"
)

// setValidEnv fills in the minimum a gateway process needs to start.
func setValidEnv(t *testing.T) {
	t.Helper()

	t.Setenv("FUNDKIT_JWT_SECRET", "a-secret-that-is-long-enough")
	t.Setenv("FUNDKIT_DB_URL", "postgres://fundkit:password@postgres:5432/fundkit_db?sslmode=disable")
}

func TestLoadUsesDefaultsForOptionalSettings(t *testing.T) {
	setValidEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.Service.HTTPPort != "8080" {
		t.Fatalf("http port = %q, want the 8080 default", cfg.Service.HTTPPort)
	}
	if cfg.Auth.TokenTTL != 24*time.Hour {
		t.Fatalf("token ttl = %s", cfg.Auth.TokenTTL)
	}
	if len(cfg.CORS.AllowedOrigins) != 1 || cfg.CORS.AllowedOrigins[0] != "http://localhost:5173" {
		t.Fatalf("cors origins = %v", cfg.CORS.AllowedOrigins)
	}
	if len(cfg.Upstreams.HealthTargets) != 3 {
		t.Fatalf("health targets = %d, want one per downstream service", len(cfg.Upstreams.HealthTargets))
	}
}

func TestLoadPrefersTheFundkitPrefixOverTheLegacyName(t *testing.T) {
	setValidEnv(t)
	t.Setenv("PORT", "9999")
	t.Setenv("FUNDKIT_HTTP_PORT", "8085")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Service.HTTPPort != "8085" {
		t.Fatalf("http port = %q, want the prefixed value to win", cfg.Service.HTTPPort)
	}
}

func TestLoadFallsBackToLegacyNames(t *testing.T) {
	setValidEnv(t)
	t.Setenv("PORT", "9999")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Service.HTTPPort != "9999" {
		t.Fatalf("http port = %q, want the legacy value during migration", cfg.Service.HTTPPort)
	}
}

func TestLoadRejectsAWeakOrMissingSecret(t *testing.T) {
	tests := []struct {
		name   string
		secret string
	}{
		{name: "missing", secret: ""},
		{name: "blank", secret: "   "},
		{name: "too short", secret: "short"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("FUNDKIT_DB_URL", "postgres://localhost:5432/db")
			t.Setenv("FUNDKIT_JWT_SECRET", test.secret)

			// A signing key with a usable default is a signing key that reaches
			// production, so the process must refuse to start instead.
			if _, err := Load(); err == nil {
				t.Fatal("expected Load to fail without a strong signing secret")
			}
		})
	}
}

func TestLoadRequiresAnIdentityDatabase(t *testing.T) {
	t.Setenv("FUNDKIT_JWT_SECRET", "a-secret-that-is-long-enough")
	t.Setenv("FUNDKIT_DB_URL", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected Load to fail without a database")
	}
	if !strings.Contains(err.Error(), "DB_URL") {
		t.Fatalf("error = %v, want it to name the missing variable", err)
	}
}

func TestLoadAssemblesADSNFromDiscreteParts(t *testing.T) {
	t.Setenv("FUNDKIT_JWT_SECRET", "a-secret-that-is-long-enough")
	t.Setenv("FUNDKIT_DB_HOST", "db.internal")
	t.Setenv("FUNDKIT_DB_NAME", "fundkit_db")
	t.Setenv("FUNDKIT_DB_USER", "fundkit")
	t.Setenv("FUNDKIT_DB_PASSWORD", "s3cret")
	t.Setenv("FUNDKIT_DB_PORT", "6543")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Managed Postgres and Kubernetes secrets hand you the parts, not a DSN.
	for _, fragment := range []string{"db.internal:6543", "fundkit", "sslmode=disable"} {
		if !strings.Contains(cfg.Database.URL, fragment) {
			t.Fatalf("dsn %q is missing %q", cfg.Database.URL, fragment)
		}
	}
}

func TestLoadParsesListsAndDurations(t *testing.T) {
	setValidEnv(t)
	t.Setenv("FUNDKIT_CORS_ALLOWED_ORIGINS", "http://a.test, http://b.test ,")
	t.Setenv("FUNDKIT_JWT_TTL", "45m")
	t.Setenv("FUNDKIT_RATE_LIMIT_RPS", "12.5")
	t.Setenv("FUNDKIT_RATE_LIMIT_BURST", "40")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(cfg.CORS.AllowedOrigins) != 2 {
		t.Fatalf("origins = %v, want blanks discarded", cfg.CORS.AllowedOrigins)
	}
	if cfg.Auth.TokenTTL != 45*time.Minute {
		t.Fatalf("ttl = %s", cfg.Auth.TokenTTL)
	}
	if cfg.RateLimit.RequestsPerSecond != 12.5 || cfg.RateLimit.Burst != 40 {
		t.Fatalf("rate limit = %+v", cfg.RateLimit)
	}
}

func TestLoadIgnoresAnUnparseableDuration(t *testing.T) {
	setValidEnv(t)
	t.Setenv("FUNDKIT_JWT_TTL", "half an hour")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Falling back to the default beats refusing to boot over a typo in an
	// optional tuning knob.
	if cfg.Auth.TokenTTL != 24*time.Hour {
		t.Fatalf("ttl = %s, want the default", cfg.Auth.TokenTTL)
	}
}
