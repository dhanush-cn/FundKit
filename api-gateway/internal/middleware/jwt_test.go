// Engineered by Dhanush C N (github.com/dhanush-cn)
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/dhanush-cn/fundkit/api-gateway/internal/token"
)

const testSecret = "test-secret-at-least-16-chars"

func init() { gin.SetMode(gin.TestMode) }

// signedToken mints a token the middleware is expected to accept unless one of
// the knobs is deliberately turned to an invalid value.
func signedToken(t *testing.T, secret string, method jwt.SigningMethod, ttl time.Duration, subject string) string {
	t.Helper()

	now := time.Now()
	claims := token.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
		Username: "dhanush",
		Email:    "dhanush@example.com",
		Phone:    "+919876543210",
		FullName: "Dhanush C N",
	}

	signed, err := jwt.NewWithClaims(method, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

// protectedRouter returns an engine whose single route records the request the
// handler actually saw, so header rewriting can be asserted.
func protectedRouter(seen *http.Request) *gin.Engine {
	router := gin.New()
	router.Use(JWTAuth(testSecret))
	router.GET("/protected", func(c *gin.Context) {
		*seen = *c.Request
		c.JSON(http.StatusOK, gin.H{"user": c.GetString(ContextUserID)})
	})
	return router
}

func TestJWTAuthRejectsBadTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		header string
	}{
		{name: "missing header", header: ""},
		{name: "not a bearer scheme", header: "Basic abc123"},
		{name: "bearer with no token", header: "Bearer"},
		{name: "garbage token", header: "Bearer not.a.jwt"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var seen http.Request
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			if test.header != "" {
				request.Header.Set("Authorization", test.header)
			}

			protectedRouter(&seen).ServeHTTP(recorder, request)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", recorder.Code)
			}
		})
	}
}

func TestJWTAuthRejectsExpiredToken(t *testing.T) {
	t.Parallel()

	var seen http.Request
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+signedToken(t, testSecret, jwt.SigningMethodHS256, -time.Minute, "user-1"))

	protectedRouter(&seen).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an expired token", recorder.Code)
	}
}

func TestJWTAuthRejectsTokenSignedWithAnotherSecret(t *testing.T) {
	t.Parallel()

	var seen http.Request
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+signedToken(t, "a-completely-different-secret", jwt.SigningMethodHS256, time.Hour, "user-1"))

	protectedRouter(&seen).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

// TestJWTAuthRejectsAlgNone is the regression test for the classic JWT attack:
// a token whose header claims alg "none" and which therefore carries no
// signature at all.
func TestJWTAuthRejectsAlgNone(t *testing.T) {
	t.Parallel()

	claims := jwt.RegisteredClaims{
		Subject:   "attacker",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("build unsigned token: %v", err)
	}

	var seen http.Request
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+unsigned)

	protectedRouter(&seen).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an unsigned token", recorder.Code)
	}
}

func TestJWTAuthForwardsVerifiedIdentity(t *testing.T) {
	t.Parallel()

	var seen http.Request
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+signedToken(t, testSecret, jwt.SigningMethodHS256, time.Hour, "user-1"))

	protectedRouter(&seen).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if got := seen.Header.Get(UserHeader); got != "user-1" {
		t.Fatalf("%s = %q, want user-1", UserHeader, got)
	}
	if got := seen.Header.Get(EmailHeader); got != "dhanush@example.com" {
		t.Fatalf("%s = %q", EmailHeader, got)
	}
	if got := seen.Header.Get(PhoneHeader); got != "+919876543210" {
		t.Fatalf("%s = %q", PhoneHeader, got)
	}
	if got := seen.Header.Get(NameHeader); got != "Dhanush C N" {
		t.Fatalf("%s = %q", NameHeader, got)
	}
}

// TestJWTAuthOverwritesClientSuppliedIdentityHeaders is the important one: the
// upstream services trust these headers, so a client must never be able to set
// them itself and impersonate another customer.
func TestJWTAuthOverwritesClientSuppliedIdentityHeaders(t *testing.T) {
	t.Parallel()

	var seen http.Request
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+signedToken(t, testSecret, jwt.SigningMethodHS256, time.Hour, "user-1"))
	request.Header.Set(UserHeader, "victim-user")
	request.Header.Set(EmailHeader, "victim@example.com")

	protectedRouter(&seen).ServeHTTP(recorder, request)

	if got := seen.Header.Get(UserHeader); got != "user-1" {
		t.Fatalf("spoofed %s survived as %q", UserHeader, got)
	}
	if got := seen.Header.Get(EmailHeader); got != "dhanush@example.com" {
		t.Fatalf("spoofed %s survived as %q", EmailHeader, got)
	}
}

func TestJWTAuthRejectsTokenWithoutSubject(t *testing.T) {
	t.Parallel()

	claims := token.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	var seen http.Request
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+signed)

	protectedRouter(&seen).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a subjectless token", recorder.Code)
	}
}
