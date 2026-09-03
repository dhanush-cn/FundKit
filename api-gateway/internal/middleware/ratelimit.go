// Engineered by Dhanush C N (github.com/dhanush-cn)
package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"github.com/dhanush-cn/fundkit/api-gateway/internal/config"
)

// clientLimiter is a token bucket plus the last time it was touched.
//
// Tracking lastSeen matters: a per-IP map that only ever grows is an unbounded
// allocation driven by unauthenticated traffic. The janitor below evicts idle
// buckets so the gateway's memory is a function of active clients, not of
// every address that has ever connected.
type clientLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiter is an in-process token-bucket limiter keyed by client IP.
//
// Tradeoff: this is per-replica, so N gateway pods allow N times the configured
// rate. That is acceptable for coarse abuse protection; a hard global quota
// would need a shared counter in Redis, paying a network round trip on every
// request.
type RateLimiter struct {
	mu       sync.Mutex
	clients  map[string]*clientLimiter
	rate     rate.Limit
	burst    int
	ttl      time.Duration
	stopOnce sync.Once
	stop     chan struct{}
}

func NewRateLimiter(cfg config.RateLimitConfig) *RateLimiter {
	limiter := &RateLimiter{
		clients: make(map[string]*clientLimiter),
		rate:    rate.Limit(cfg.RequestsPerSecond),
		burst:   cfg.Burst,
		ttl:     cfg.ClientTTL,
		stop:    make(chan struct{}),
	}
	go limiter.reapIdleClients()
	return limiter
}

// Close stops the background janitor.
func (r *RateLimiter) Close() {
	r.stopOnce.Do(func() { close(r.stop) })
}

// Middleware enforces the bucket and advertises the limit to clients.
func (r *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !r.allow(c.ClientIP()) {
			c.Header("Retry-After", "1")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "too many requests, please slow down",
			})
			return
		}
		c.Next()
	}
}

func (r *RateLimiter) allow(ip string) bool {
	r.mu.Lock()
	client, exists := r.clients[ip]
	if !exists {
		client = &clientLimiter{limiter: rate.NewLimiter(r.rate, r.burst)}
		r.clients[ip] = client
	}
	client.lastSeen = time.Now()
	r.mu.Unlock()

	return client.limiter.Allow()
}

func (r *RateLimiter) reapIdleClients() {
	ticker := time.NewTicker(r.ttl)
	defer ticker.Stop()

	for {
		select {
		case <-r.stop:
			return
		case now := <-ticker.C:
			r.mu.Lock()
			for ip, client := range r.clients {
				if now.Sub(client.lastSeen) > r.ttl {
					delete(r.clients, ip)
				}
			}
			r.mu.Unlock()
		}
	}
}
