// Engineered by Dhanush C N (github.com/dhanush-cn)
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func corsRouter() *gin.Engine {
	router := gin.New()
	router.Use(CORS([]string{"http://localhost:5173"}))
	router.GET("/thing", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	return router
}

func TestCORSEchoesOnlyAllowedOrigins(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		origin     string
		wantHeader string
	}{
		{name: "allow-listed origin is echoed", origin: "http://localhost:5173", wantHeader: "http://localhost:5173"},
		{name: "unknown origin gets no allow header", origin: "https://evil.example.com", wantHeader: ""},
		{name: "same-origin request needs no header", origin: "", wantHeader: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/thing", nil)
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}

			corsRouter().ServeHTTP(recorder, request)

			if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != test.wantHeader {
				t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, test.wantHeader)
			}
		})
	}
}

// The browser sends a preflight before any request carrying an Authorization
// header. If it is not answered, login fails before it ever reaches the handler.
func TestCORSAnswersPreflightWithoutCallingTheHandler(t *testing.T) {
	t.Parallel()

	router := gin.New()
	handlerCalled := false
	router.Use(CORS([]string{"http://localhost:5173"}))
	router.POST("/auth/login", func(c *gin.Context) {
		handlerCalled = true
		c.Status(http.StatusOK)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/auth/login", nil)
	request.Header.Set("Origin", "http://localhost:5173")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "authorization, content-type")

	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", recorder.Code)
	}
	if handlerCalled {
		t.Fatal("preflight must be terminated by the middleware, not forwarded to the handler")
	}

	allowedHeaders := recorder.Header().Get("Access-Control-Allow-Headers")
	if allowedHeaders == "" {
		t.Fatal("preflight response did not advertise the allowed headers")
	}
}
