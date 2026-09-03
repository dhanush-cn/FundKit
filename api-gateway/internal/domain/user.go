// Package domain holds the api-gateway's identity aggregate.
//
// Identity lives at the edge on purpose: the gateway is the only component that
// ever sees a password, and every service behind it trusts the verified subject
// the gateway forwards rather than re-validating credentials on each hop.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package domain

import (
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Sentinel errors let the handler layer map failures onto HTTP status codes
// without matching on error strings.
var (
	ErrUserNotFound       = errors.New("user not found")
	ErrUsernameTaken      = errors.New("username is already registered")
	ErrEmailTaken         = errors.New("email is already registered")
	ErrInvalidCredentials = errors.New("invalid username or password")
)

// ValidationError reports a field the caller can fix, so the API can answer
// 400 with something actionable instead of a generic rejection.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// User is the persisted account. The password hash is never serialised: the
// json:"-" tag is the last line of defence if a handler ever returns the model
// directly.
type User struct {
	ID           string    `gorm:"primaryKey;size:36" json:"id"`
	Username     string    `gorm:"uniqueIndex;size:64;not null" json:"username"`
	Email        string    `gorm:"uniqueIndex;size:255;not null" json:"email"`
	Phone        string    `gorm:"size:32" json:"phone"`
	FullName     string    `gorm:"size:128" json:"full_name"`
	PasswordHash string    `gorm:"size:255;not null" json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TableName pins the table so a later rename of the Go type cannot silently
// orphan production data.
func (User) TableName() string { return "users" }

// BeforeCreate assigns an opaque primary key. A UUID keeps the number of
// registered users from leaking through the API the way a serial id would.
func (u *User) BeforeCreate(*gorm.DB) error {
	if u.ID == "" {
		u.ID = uuid.New().String()
	}
	return nil
}

// Registration is the command accepted by the auth service.
type Registration struct {
	Username string
	Email    string
	Phone    string
	FullName string
	Password string
}

var (
	usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{3,64}$`)
	phonePattern    = regexp.MustCompile(`^\+?[0-9][0-9 ()-]{6,19}$`)
)

// MinPasswordLength is deliberately a length floor rather than a character-class
// rule: length is what actually resists offline cracking, and composition rules
// mostly push people towards predictable substitutions.
const MinPasswordLength = 8

// NormalizeUsername applies the same folding to a login attempt that
// registration applied, so "Alice" and "alice" resolve to one account.
func NormalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// Normalize trims and case-folds the fields that must compare equal regardless
// of how the user typed them. Doing this before validation means "  Alice@X.COM"
// and "alice@x.com" cannot both be registered.
func (r *Registration) Normalize() {
	r.Username = NormalizeUsername(r.Username)
	r.Email = strings.ToLower(strings.TrimSpace(r.Email))
	r.Phone = strings.TrimSpace(r.Phone)
	r.FullName = strings.TrimSpace(r.FullName)
}

// Validate enforces the account rules. It returns the first problem found so the
// caller gets one clear message per attempt.
func (r Registration) Validate() error {
	if !usernamePattern.MatchString(r.Username) {
		return ValidationError{
			Field:   "username",
			Message: "must be 3-64 characters using letters, digits, dot, dash or underscore",
		}
	}
	if _, err := mail.ParseAddress(r.Email); err != nil {
		return ValidationError{Field: "email", Message: "must be a valid email address"}
	}
	// Notifications are the whole point of collecting contact details, so a
	// phone number is required rather than optional.
	if !phonePattern.MatchString(r.Phone) {
		return ValidationError{Field: "phone", Message: "must be a valid phone number, e.g. +919876543210"}
	}
	if len([]rune(r.FullName)) < 2 {
		return ValidationError{Field: "full_name", Message: "must be at least 2 characters"}
	}
	if len(r.Password) < MinPasswordLength {
		return ValidationError{
			Field:   "password",
			Message: fmt.Sprintf("must be at least %d characters", MinPasswordLength),
		}
	}
	return nil
}
