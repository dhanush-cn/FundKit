// Engineered by Dhanush C N (github.com/dhanush-cn)
package middleware

import (
	"github.com/gin-gonic/gin"

	"github.com/dhanush-cn/fundkit/api-gateway/internal/platform/trace"
)

// RequestID is where a FundKit trace is born. The gateway is the only public
// entry point, so every correlation id in the system either arrives here from
// a client or is minted here.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, id := trace.EnsureContext(c.Request.Context(), c.GetHeader(trace.HeaderKey))
		c.Request = c.Request.WithContext(ctx)
		// Set it on the inbound request too, so the reverse proxy forwards it
		// downstream without any extra copying.
		c.Request.Header.Set(trace.HeaderKey, id)
		c.Writer.Header().Set(trace.HeaderKey, id)
		c.Next()
	}
}
