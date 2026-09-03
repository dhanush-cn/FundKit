// Engineered by Dhanush C N (github.com/dhanush-cn)
package handler

import (
	"context"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dhanush-cn/fundkit/order-service/internal/platform/trace"
)

// RequestID adopts an inbound x-request-id or mints one, stores it on the
// request context and echoes it back so the caller can quote it in a bug report.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, id := trace.EnsureContext(c.Request.Context(), c.GetHeader(trace.HeaderKey))
		c.Request = c.Request.WithContext(ctx)
		c.Writer.Header().Set(trace.HeaderKey, id)
		c.Next()
	}
}

// RequestLogger emits one structured access log per request. It replaces gin's
// human-formatted default, which is unusable for log aggregation.
func RequestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		attrs := []any{
			slog.String("method", c.Request.Method),
			slog.String("path", c.FullPath()),
			slog.Int("status", c.Writer.Status()),
			slog.Duration("latency", time.Since(start)),
			slog.String("client_ip", c.ClientIP()),
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

// Timeout bounds how long any handler may hold a connection open. Without it a
// stalled dependency turns into exhausted server goroutines.
func Timeout(d time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), d)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// Recovery converts a panic into a structured error log plus a 500, keeping the
// process alive and the stack trace out of the response body.
func Recovery(logger *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, recovered any) {
		logger.ErrorContext(c.Request.Context(), "panic recovered", slog.Any("panic", recovered))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal server error"})
	})
}
