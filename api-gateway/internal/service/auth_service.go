// Engineered by Dhanush C N (github.com/dhanush-cn)
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/dhanush-cn/fundkit/api-gateway/internal/config"
	"github.com/dhanush-cn/fundkit/api-gateway/internal/domain"
	"github.com/dhanush-cn/fundkit/api-gateway/internal/token"
)

// UserRepository is the persistence port the auth use cases need. Declaring it
// here (rather than importing a concrete repository) is what lets the service be
// tested against an in-memory fake.
type UserRepository interface {
	Create(ctx context.Context, user *domain.User) error
	GetByUsername(ctx context.Context, username string) (*domain.User, error)
	GetByID(ctx context.Context, id string) (*domain.User, error)
}

// decoyHash is a valid bcrypt digest of a value nobody knows. When a login
// names an account that does not exist we still run a comparison against it, so
// the response time does not tell an attacker which usernames are registered.
const decoyHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

// AuthService owns registration, credential verification and token issuance.
type AuthService struct {
	users  UserRepository
	cfg    config.AuthConfig
	logger *slog.Logger
	cost   int
}

func NewAuthService(users UserRepository, cfg config.AuthConfig, logger *slog.Logger) *AuthService {
	cost := cfg.BcryptCost
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		cost = bcrypt.DefaultCost
	}
	return &AuthService{users: users, cfg: cfg, logger: logger, cost: cost}
}

// Session is what a successful login or registration hands back.
type Session struct {
	Token     string
	ExpiresAt time.Time
	User      *domain.User
}

// Register validates the input, hashes the password and stores the account.
func (s *AuthService) Register(ctx context.Context, input domain.Registration) (*domain.User, error) {
	input.Normalize()
	if err := input.Validate(); err != nil {
		return nil, err
	}

	// bcrypt's work factor is the whole point: it makes each guess expensive.
	// The password is never logged, stored or echoed anywhere but here.
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), s.cost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	user := &domain.User{
		Username:     input.Username,
		Email:        input.Email,
		Phone:        input.Phone,
		FullName:     input.FullName,
		PasswordHash: string(hash),
	}

	if err := s.users.Create(ctx, user); err != nil {
		return nil, err
	}

	s.logger.InfoContext(ctx, "user registered",
		slog.String("user_id", user.ID),
		slog.String("username", user.Username),
	)
	return user, nil
}

// Authenticate verifies a username/password pair.
//
// Every failure returns the same ErrInvalidCredentials: telling the caller
// "no such user" versus "wrong password" hands an attacker a free account
// enumeration oracle.
func (s *AuthService) Authenticate(ctx context.Context, username, password string) (*domain.User, error) {
	user, err := s.users.GetByUsername(ctx, domain.NormalizeUsername(username))
	if err != nil && !errors.Is(err, domain.ErrUserNotFound) {
		return nil, err
	}

	hash := decoyHash
	if user != nil {
		hash = user.PasswordHash
	}

	if compareErr := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); compareErr != nil || user == nil {
		return nil, domain.ErrInvalidCredentials
	}

	return user, nil
}

// IssueToken mints a signed, expiring token for an authenticated user.
func (s *AuthService) IssueToken(user *domain.User) (Session, error) {
	now := time.Now()
	expiresAt := now.Add(s.cfg.TokenTTL)

	claims := token.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID,
			Issuer:    s.cfg.Issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
		Username: user.Username,
		Email:    user.Email,
		Phone:    user.Phone,
		FullName: user.FullName,
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.cfg.JWTSecret))
	if err != nil {
		return Session{}, fmt.Errorf("sign token: %w", err)
	}

	return Session{Token: signed, ExpiresAt: expiresAt.UTC(), User: user}, nil
}

// Profile loads the account behind a verified token subject.
func (s *AuthService) Profile(ctx context.Context, userID string) (*domain.User, error) {
	return s.users.GetByID(ctx, userID)
}
