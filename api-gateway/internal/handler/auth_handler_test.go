// Engineered by Dhanush C N (github.com/dhanush-cn)
package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/dhanush-cn/fundkit/api-gateway/internal/config"
	"github.com/dhanush-cn/fundkit/api-gateway/internal/domain"
	"github.com/dhanush-cn/fundkit/api-gateway/internal/middleware"
	"github.com/dhanush-cn/fundkit/api-gateway/internal/service"
)

func init() { gin.SetMode(gin.TestMode) }

// memoryUsers is an in-memory account store, so these tests exercise the real
// handler, the real service and real bcrypt hashing without a database.
type memoryUsers struct {
	mu     sync.Mutex
	users  map[string]*domain.User
	emails map[string]struct{}
	seq    int
}

func newMemoryUsers() *memoryUsers {
	return &memoryUsers{users: make(map[string]*domain.User), emails: make(map[string]struct{})}
}

func (m *memoryUsers) Create(_ context.Context, user *domain.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.users[user.Username]; exists {
		return domain.ErrUsernameTaken
	}
	if _, exists := m.emails[user.Email]; exists {
		return domain.ErrEmailTaken
	}
	m.seq++
	user.ID = "user-" + time.Now().UTC().Format("150405.000000") + "-" + itoa(m.seq)
	m.users[user.Username] = user
	m.emails[user.Email] = struct{}{}
	return nil
}

func (m *memoryUsers) GetByUsername(_ context.Context, username string) (*domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if user, ok := m.users[username]; ok {
		return user, nil
	}
	return nil, domain.ErrUserNotFound
}

func (m *memoryUsers) GetByID(_ context.Context, id string) (*domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, user := range m.users {
		if user.ID == id {
			return user, nil
		}
	}
	return nil, domain.ErrUserNotFound
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

func authTestRouter() (*gin.Engine, *memoryUsers) {
	users := newMemoryUsers()
	cfg := config.AuthConfig{
		JWTSecret:  "test-secret-at-least-16-chars",
		TokenTTL:   time.Hour,
		Issuer:     "fundkit-test",
		BcryptCost: bcrypt.MinCost,
	}
	authService := service.NewAuthService(users, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	handler := NewAuthHandler(authService)

	router := gin.New()
	router.POST("/auth/register", handler.Register)
	router.POST("/auth/login", handler.Login)

	protected := router.Group("/")
	protected.Use(middleware.JWTAuth(cfg.JWTSecret))
	protected.GET("/auth/me", handler.Me)

	return router, users
}

func postJSON(t *testing.T, router *gin.Engine, path string, body any) *httptest.ResponseRecorder {
	t.Helper()

	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder
}

func validRegistrationBody() map[string]string {
	return map[string]string{
		"username":  "dhanush",
		"email":     "dhanush@example.com",
		"phone":     "+919876543210",
		"full_name": "Dhanush C N",
		"password":  "correct-horse-battery",
	}
}

func decodeSession(t *testing.T, recorder *httptest.ResponseRecorder) sessionResponse {
	t.Helper()

	var session sessionResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode session: %v (body %s)", err, recorder.Body.String())
	}
	return session
}

func TestRegisterReturnsASessionAndNeverThePasswordHash(t *testing.T) {
	t.Parallel()

	router, _ := authTestRouter()
	recorder := postJSON(t, router, "/auth/register", validRegistrationBody())

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}

	session := decodeSession(t, recorder)
	if session.Token == "" || session.UserID == "" {
		t.Fatalf("incomplete session: %+v", session)
	}
	if session.User.Email != "dhanush@example.com" || session.User.Phone != "+919876543210" {
		t.Fatalf("contact details missing from the response: %+v", session.User)
	}
	// Registering signs you in, so no second round trip is needed.
	if session.ExpiresAt == "" {
		t.Fatal("session did not report an expiry")
	}
	// The account model carries a bcrypt digest; the response must not.
	for _, leak := range []string{"password", "PasswordHash", "$2a$", "$2b$"} {
		if bytes.Contains(recorder.Body.Bytes(), []byte(leak)) {
			t.Fatalf("response body leaks %q: %s", leak, recorder.Body.String())
		}
	}
}

func TestRegisterRejectsInvalidPayloads(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mutate     func(map[string]string)
		wantStatus int
	}{
		{
			name:       "missing phone",
			mutate:     func(body map[string]string) { delete(body, "phone") },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "malformed email",
			mutate:     func(body map[string]string) { body["email"] = "not-an-email" },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "short password",
			mutate:     func(body map[string]string) { body["password"] = "short" },
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			router, _ := authTestRouter()
			body := validRegistrationBody()
			test.mutate(body)

			recorder := postJSON(t, router, "/auth/register", body)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestRegisterReportsADuplicateAsAConflict(t *testing.T) {
	t.Parallel()

	router, _ := authTestRouter()
	if recorder := postJSON(t, router, "/auth/register", validRegistrationBody()); recorder.Code != http.StatusCreated {
		t.Fatalf("first registration: %d", recorder.Code)
	}

	recorder := postJSON(t, router, "/auth/register", validRegistrationBody())
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 for a duplicate account", recorder.Code)
	}
}

func TestLoginIssuesAWorkingToken(t *testing.T) {
	t.Parallel()

	router, _ := authTestRouter()
	if recorder := postJSON(t, router, "/auth/register", validRegistrationBody()); recorder.Code != http.StatusCreated {
		t.Fatalf("register: %d", recorder.Code)
	}

	recorder := postJSON(t, router, "/auth/login", map[string]string{
		"username": "dhanush",
		"password": "correct-horse-battery",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}

	session := decodeSession(t, recorder)

	// The token has to actually open the door it was issued for.
	meRecorder := httptest.NewRecorder()
	meRequest := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	meRequest.Header.Set("Authorization", "Bearer "+session.Token)
	router.ServeHTTP(meRecorder, meRequest)

	if meRecorder.Code != http.StatusOK {
		t.Fatalf("/auth/me status = %d: %s", meRecorder.Code, meRecorder.Body.String())
	}

	var profile userView
	if err := json.Unmarshal(meRecorder.Body.Bytes(), &profile); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if profile.Username != "dhanush" || profile.ID != session.UserID {
		t.Fatalf("profile = %+v, want the signed-in account", profile)
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	t.Parallel()

	router, _ := authTestRouter()
	if recorder := postJSON(t, router, "/auth/register", validRegistrationBody()); recorder.Code != http.StatusCreated {
		t.Fatalf("register: %d", recorder.Code)
	}

	tests := []struct {
		name string
		body map[string]string
	}{
		{name: "wrong password", body: map[string]string{"username": "dhanush", "password": "nope-nope-nope"}},
		{name: "unknown user", body: map[string]string{"username": "nobody", "password": "nope-nope-nope"}},
	}

	bodies := make([]string, 0, len(tests))
	for _, test := range tests {
		recorder := postJSON(t, router, "/auth/login", test.body)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, want 401", test.name, recorder.Code)
		}
		bodies = append(bodies, recorder.Body.String())
	}

	// Identical responses: the API must not reveal which usernames exist.
	if bodies[0] != bodies[1] {
		t.Fatalf("responses differ and leak account existence:\n%s\n%s", bodies[0], bodies[1])
	}
}

func TestLoginRequiresBothFields(t *testing.T) {
	t.Parallel()

	router, _ := authTestRouter()
	recorder := postJSON(t, router, "/auth/login", map[string]string{"username": "dhanush"})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestMeRequiresAuthentication(t *testing.T) {
	t.Parallel()

	router, _ := authTestRouter()
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/auth/me", nil))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}
