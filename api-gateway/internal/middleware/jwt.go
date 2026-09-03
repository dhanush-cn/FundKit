// Engineered by Dhanush C N (github.com/dhanush-cn)
package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/dhanush-cn/fundkit/api-gateway/internal/token"
)

// Context keys holding the authenticated identity for downstream handlers.
const (
	ContextUserID   = "userID"
	ContextUsername = "username"
	ContextEmail    = "email"
	ContextPhone    = "phone"
	ContextFullName = "fullName"
)

// Headers that carry the verified identity to upstream services. Those services
// trust the gateway rather than re-validating the token on every hop, which is
// why the gateway must overwrite these headers unconditionally: a client that
// sets them itself would otherwise be spoofing another customer.
const (
	UserHeader     = "x-fundkit-user-id"
	UsernameHeader = "x-fundkit-username"
	EmailHeader    = "x-fundkit-user-email"
	PhoneHeader    = "x-fundkit-user-phone"
	NameHeader     = "x-fundkit-user-name"
)

// JWTAuth validates the bearer token and pins the expected algorithm.
//
// The algorithm check is not decoration: without it a token signed with "none"
// (or an RS256 public key replayed as an HMAC secret) would be accepted.
func JWTAuth(secret string) gin.HandlerFunc {
	keyFunc := func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	}

	return func(c *gin.Context) {
		// Strip any inbound identity headers before anything else: whatever the
		// client sent is not evidence of who they are.
		clearIdentityHeaders(c)

		header := c.GetHeader("Authorization")
		if header == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authorization header required"})
			return
		}

		parts := strings.Fields(header)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization header format"})
			return
		}

		claims := &token.Claims{}
		parsed, err := jwt.ParseWithClaims(parts[1], claims, keyFunc,
			jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
			jwt.WithExpirationRequired(),
		)
		if err != nil || !parsed.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		subject, err := claims.GetSubject()
		if err != nil || subject == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token is missing a subject claim"})
			return
		}

		c.Set(ContextUserID, subject)
		c.Set(ContextUsername, claims.Username)
		c.Set(ContextEmail, claims.Email)
		c.Set(ContextPhone, claims.Phone)
		c.Set(ContextFullName, claims.FullName)

		// The contact details travel with the request so order-service can stamp
		// them onto the order, and notification-service can reach a real inbox
		// instead of inventing one from the user id.
		c.Request.Header.Set(UserHeader, subject)
		c.Request.Header.Set(UsernameHeader, claims.Username)
		c.Request.Header.Set(EmailHeader, claims.Email)
		c.Request.Header.Set(PhoneHeader, claims.Phone)
		c.Request.Header.Set(NameHeader, claims.FullName)

		c.Next()
	}
}

func clearIdentityHeaders(c *gin.Context) {
	for _, key := range []string{UserHeader, UsernameHeader, EmailHeader, PhoneHeader, NameHeader} {
		c.Request.Header.Del(key)
	}
}
