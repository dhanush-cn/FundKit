package middleware

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// IP-based token bucket rate limiter
type rateLimiter struct {
	ips   map[string]*rate.Limiter
	mutex sync.RWMutex
	rate  rate.Limit
	burst int
}

var limiter = &rateLimiter{
	ips:   make(map[string]*rate.Limiter),
	rate:  5,  // 5 requests per second
	burst: 10, // burst of up to 10
}

func (r *rateLimiter) getLimiter(ip string) *rate.Limiter {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	lim, exists := r.ips[ip]
	if !exists {
		lim = rate.NewLimiter(r.rate, r.burst)
		r.ips[ip] = lim
	}
	return lim
}

func RateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		lim := limiter.getLimiter(ip)

		if !lim.Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "Too many requests. Please slow down."})
			return
		}
		c.Next()
	}
}
