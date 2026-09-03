// Engineered by Dhanush C N (github.com/dhanush-cn)
package repository

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"github.com/dhanush-cn/fundkit/api-gateway/internal/domain"
)

// UserRepository is the gorm-backed implementation of the port the auth service
// declares. Translating driver errors into domain errors happens here so the
// service layer never imports gorm.
type UserRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

// Create inserts a new account.
//
// The uniqueness of username and email is enforced by database indexes rather
// than by a read-then-write check in Go: two concurrent registrations for the
// same name would both pass a pre-check and only the index would catch it.
func (r *UserRepository) Create(ctx context.Context, user *domain.User) error {
	if err := r.db.WithContext(ctx).Create(user).Error; err != nil {
		return classifyUniqueViolation(err)
	}
	return nil
}

// GetByUsername loads the account a login attempt refers to.
func (r *UserRepository) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	var user domain.User
	if err := r.db.WithContext(ctx).Where("username = ?", username).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

// GetByID loads the account referenced by a validated token subject.
func (r *UserRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	var user domain.User
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

// classifyUniqueViolation turns a constraint failure into the specific domain
// error, so the API can say which field collided instead of returning a 500.
func classifyUniqueViolation(err error) error {
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrDuplicatedKey) && !strings.Contains(strings.ToLower(err.Error()), "duplicate") &&
		!strings.Contains(strings.ToLower(err.Error()), "unique") {
		return err
	}

	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "email"):
		return domain.ErrEmailTaken
	case strings.Contains(message, "username"):
		return domain.ErrUsernameTaken
	default:
		return domain.ErrUsernameTaken
	}
}
