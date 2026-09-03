// Engineered by Dhanush C N (github.com/dhanush-cn)
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dhanush-cn/fundkit/api-gateway/internal/config"
)

func limitedRouter(limiter *RateLimiter) *gin.Engine {
	router := gin.New()
	router.Use(limiter.Middleware())
	router.GET("/thing", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	return router
}

func call(t *testing.T, router *gin.Engine, ip string) int {
	t.Helper()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/thing", nil)
	request.RemoteAddr = ip + ":54321"
	router.ServeHTTP(recorder, request)
	return recorder.Code
}

func TestRateLimiterAllowsBurstThenRejects(t *testing.T) {
	t.Parallel()

	limiter := NewRateLimiter(config.RateLimitConfig{
		RequestsPerSecond: 1,
		Burst:             3,
		ClientTTL:         time.Minute,
	})
	defer limiter.Close()

	router := limitedRouter(limiter)

	for attempt := 1; attempt <= 3; attempt++ {
		if code := call(t, router, "10.0.0.1"); code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 within the burst", attempt, code)
		}
	}

	if code := call(t, router, "10.0.0.1"); code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 once the burst is spent", code)
	}
}

// The limiter is keyed by client IP, so one noisy caller must not lock everyone
// else out of the platform.
func TestRateLimiterIsolatesClients(t *testing.T) {
	t.Parallel()

	limiter := NewRateLimiter(config.RateLimitConfig{
		RequestsPerSecond: 1,
		Burst:             1,
		ClientTTL:         time.Minute,
	})
	defer limiter.Close()

	router := limitedRouter(limiter)

	if code := call(t, router, "10.0.0.1"); code != http.StatusOK {
		t.Fatalf("first client: status = %d", code)
	}
	if code := call(t, router, "10.0.0.1"); code != http.StatusTooManyRequests {
		t.Fatalf("first client second call: status = %d, want 429", code)
	}
	if code := call(t, router, "10.0.0.2"); code != http.StatusOK {
		t.Fatalf("second client was throttled by the first client's traffic: status = %d", code)
	}
}

func TestRateLimiterCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	limiter := NewRateLimiter(config.RateLimitConfig{RequestsPerSecond: 1, Burst: 1, ClientTTL: time.Minute})
	limiter.Close()
	// A double close would panic on a closed channel if the sync.Once were
	// dropped, and shutdown paths call Close more than once often enough.
	limiter.Close()
}
