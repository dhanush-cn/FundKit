// Engineered by Dhanush C N (github.com/dhanush-cn)
package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// RequestLogger writes one JSON access log per request.
func RequestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		attrs := []any{
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.Int("status", c.Writer.Status()),
			slog.Duration("latency", time.Since(start)),
			slog.String("client_ip", c.ClientIP()),
		}
		if userID, ok := c.Get(ContextUserID); ok {
			attrs = append(attrs, slog.Any("user_id", userID))
		}
		if err := c.Errors.Last(); err != nil {
			attrs = append(attrs, slog.String("error", err.Error()))
		}

		switch {
		case c.Writer.Status() >= 500:
			logger.ErrorContext(c.Request.Context(), "http request", attrs...)
		case c.Writer.Status() >= 400:
			logger.WarnContext(c.Request.Context(), "http request", attrs...)
		default:
			logger.InfoContext(c.Request.Context(), "http request", attrs...)
		}
	}
}

// Recovery turns a panic into a structured log line and a 500.
func Recovery(logger *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, recovered any) {
		logger.ErrorContext(c.Request.Context(), "panic recovered", slog.Any("panic", recovered))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal server error"})
	})
}
