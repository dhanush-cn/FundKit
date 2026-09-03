// Engineered by Dhanush C N (github.com/dhanush-cn)
package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/dhanush-cn/fundkit/api-gateway/internal/config"
	"github.com/dhanush-cn/fundkit/api-gateway/internal/domain"
	"github.com/dhanush-cn/fundkit/api-gateway/internal/token"
)

// fakeUserRepository is an in-memory stand-in for Postgres. The service only
// ever sees the UserRepository interface, which is what makes this possible
// without a database.
type fakeUserRepository struct {
	mu      sync.Mutex
	byName  map[string]*domain.User
	byID    map[string]*domain.User
	byEmail map[string]struct{}
	failOn  error
	nextID  int
}

func newFakeRepo() *fakeUserRepository {
	return &fakeUserRepository{
		byName:  make(map[string]*domain.User),
		byID:    make(map[string]*domain.User),
		byEmail: make(map[string]struct{}),
	}
}

func (f *fakeUserRepository) Create(_ context.Context, user *domain.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failOn != nil {
		return f.failOn
	}
	if _, exists := f.byName[user.Username]; exists {
		return domain.ErrUsernameTaken
	}
	if _, exists := f.byEmail[user.Email]; exists {
		return domain.ErrEmailTaken
	}

	f.nextID++
	user.ID = "user-" + string(rune('a'+f.nextID-1))
	f.byName[user.Username] = user
	f.byID[user.ID] = user
	f.byEmail[user.Email] = struct{}{}
	return nil
}

func (f *fakeUserRepository) GetByUsername(_ context.Context, username string) (*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	user, ok := f.byName[username]
	if !ok {
		return nil, domain.ErrUserNotFound
	}
	return user, nil
}

func (f *fakeUserRepository) GetByID(_ context.Context, id string) (*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	user, ok := f.byID[id]
	if !ok {
		return nil, domain.ErrUserNotFound
	}
	return user, nil
}

func testConfig() config.AuthConfig {
	return config.AuthConfig{
		JWTSecret: "test-secret-at-least-16-chars",
		TokenTTL:  time.Hour,
		Issuer:    "fundkit-test",
		// bcrypt.MinCost keeps the suite fast; production cost comes from config.
		BcryptCost: bcrypt.MinCost,
	}
}

func newTestService(repo UserRepository) *AuthService {
	return NewAuthService(repo, testConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func validRegistration() domain.Registration {
	return domain.Registration{
		Username: "dhanush",
		Email:    "dhanush@example.com",
		Phone:    "+919876543210",
		FullName: "Dhanush C N",
		Password: "correct-horse-battery",
	}
}

func TestRegisterHashesPasswordAndNormalisesInput(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	service := newTestService(repo)

	input := validRegistration()
	input.Username = "  Dhanush  "
	input.Email = "Dhanush@Example.com"

	user, err := service.Register(context.Background(), input)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if user.Username != "dhanush" || user.Email != "dhanush@example.com" {
		t.Fatalf("input was not normalised: %+v", user)
	}
	if user.PasswordHash == "" || strings.Contains(user.PasswordHash, input.Password) {
		t.Fatal("the password must be stored as a hash, never in recoverable form")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password)); err != nil {
		t.Fatalf("stored hash does not verify the original password: %v", err)
	}
}

func TestRegisterRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	service := newTestService(newFakeRepo())
	input := validRegistration()
	input.Email = "not-an-email"

	if _, err := service.Register(context.Background(), input); err == nil {
		t.Fatal("expected validation to reject a malformed email")
	}
}

func TestRegisterSurfacesUniquenessConflicts(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	service := newTestService(repo)

	if _, err := service.Register(context.Background(), validRegistration()); err != nil {
		t.Fatalf("first registration: %v", err)
	}

	second := validRegistration()
	second.Email = "someone.else@example.com"
	if _, err := service.Register(context.Background(), second); !errors.Is(err, domain.ErrUsernameTaken) {
		t.Fatalf("error = %v, want ErrUsernameTaken", err)
	}
}

func TestAuthenticateAcceptsCorrectCredentials(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	service := newTestService(repo)
	registered, err := service.Register(context.Background(), validRegistration())
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// Case folding must apply to the login attempt too, or an account becomes
	// unreachable the moment someone capitalises their own name.
	user, err := service.Authenticate(context.Background(), "  DHANUSH ", "correct-horse-battery")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if user.ID != registered.ID {
		t.Fatalf("authenticated the wrong account: %s", user.ID)
	}
}

func TestAuthenticateIsIndistinguishableForUnknownUserAndWrongPassword(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	service := newTestService(repo)
	if _, err := service.Register(context.Background(), validRegistration()); err != nil {
		t.Fatalf("register: %v", err)
	}

	_, wrongPassword := service.Authenticate(context.Background(), "dhanush", "not-the-password")
	_, unknownUser := service.Authenticate(context.Background(), "nobody", "not-the-password")

	if !errors.Is(wrongPassword, domain.ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v", wrongPassword)
	}
	// Returning a different error for an unknown account would hand an attacker
	// a free account-enumeration oracle.
	if !errors.Is(unknownUser, domain.ErrInvalidCredentials) {
		t.Fatalf("unknown user error = %v, want the same ErrInvalidCredentials", unknownUser)
	}
}

func TestAuthenticatePropagatesRepositoryFailures(t *testing.T) {
	t.Parallel()

	// A database outage must not be reported to the caller as "bad password":
	// that would turn an incident into a wave of confused password resets.
	boom := errors.New("connection refused")
	service := newTestService(&erroringRepo{err: boom})

	if _, err := service.Authenticate(context.Background(), "dhanush", "whatever"); !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the underlying repository failure", err)
	}
}

type erroringRepo struct{ err error }

func (e *erroringRepo) Create(context.Context, *domain.User) error { return e.err }
func (e *erroringRepo) GetByUsername(context.Context, string) (*domain.User, error) {
	return nil, e.err
}
func (e *erroringRepo) GetByID(context.Context, string) (*domain.User, error) { return nil, e.err }

func TestIssueTokenCarriesContactDetails(t *testing.T) {
	t.Parallel()

	service := newTestService(newFakeRepo())
	user, err := service.Register(context.Background(), validRegistration())
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	session, err := service.IssueToken(user)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	if session.ExpiresAt.Before(time.Now()) {
		t.Fatal("token expired before it was issued")
	}

	claims := &token.Claims{}
	parsed, err := jwt.ParseWithClaims(session.Token, claims, func(*jwt.Token) (any, error) {
		return []byte(testConfig().JWTSecret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !parsed.Valid {
		t.Fatalf("token did not verify: %v", err)
	}

	if claims.Subject != user.ID {
		t.Fatalf("subject = %q, want %q", claims.Subject, user.ID)
	}
	// These three are what let notification-service reach a real customer.
	if claims.Email != user.Email || claims.Phone != user.Phone || claims.FullName != user.FullName {
		t.Fatalf("contact claims missing from token: %+v", claims)
	}
	if claims.Issuer != "fundkit-test" {
		t.Fatalf("issuer = %q", claims.Issuer)
	}
}

func TestProfileReadsThroughToTheRepository(t *testing.T) {
	t.Parallel()

	service := newTestService(newFakeRepo())
	user, err := service.Register(context.Background(), validRegistration())
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	found, err := service.Profile(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if found.Username != user.Username {
		t.Fatalf("profile returned %q", found.Username)
	}

	if _, err := service.Profile(context.Background(), "does-not-exist"); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("error = %v, want ErrUserNotFound", err)
	}
}
