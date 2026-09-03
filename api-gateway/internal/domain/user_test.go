// Engineered by Dhanush C N (github.com/dhanush-cn)
package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestRegistrationNormalize(t *testing.T) {
	t.Parallel()

	registration := Registration{
		Username: "  DhanushCN  ",
		Email:    "  Dhanush@Example.COM ",
		Phone:    "  +91 98765 43210 ",
		FullName: "  Dhanush C N  ",
		Password: "  keeps-its-spaces  ",
	}
	registration.Normalize()

	if registration.Username != "dhanushcn" {
		t.Fatalf("username = %q, want %q", registration.Username, "dhanushcn")
	}
	if registration.Email != "dhanush@example.com" {
		t.Fatalf("email = %q, want %q", registration.Email, "dhanush@example.com")
	}
	if registration.FullName != "Dhanush C N" {
		t.Fatalf("full name = %q", registration.FullName)
	}
	// A password is a secret, not a display value: trimming it would silently
	// change what the user typed and lock them out later.
	if registration.Password != "  keeps-its-spaces  " {
		t.Fatalf("password was modified: %q", registration.Password)
	}
}

func TestRegistrationValidate(t *testing.T) {
	t.Parallel()

	valid := func() Registration {
		return Registration{
			Username: "dhanush",
			Email:    "dhanush@example.com",
			Phone:    "+919876543210",
			FullName: "Dhanush C N",
			Password: "correct-horse",
		}
	}

	tests := []struct {
		name      string
		mutate    func(*Registration)
		wantField string
	}{
		{name: "accepts a complete registration"},
		{
			name:      "rejects a short username",
			mutate:    func(r *Registration) { r.Username = "dc" },
			wantField: "username",
		},
		{
			name:      "rejects a username with spaces",
			mutate:    func(r *Registration) { r.Username = "dhanush cn" },
			wantField: "username",
		},
		{
			name:      "rejects a malformed email",
			mutate:    func(r *Registration) { r.Email = "dhanush@" },
			wantField: "email",
		},
		{
			name:      "rejects a missing phone number",
			mutate:    func(r *Registration) { r.Phone = "" },
			wantField: "phone",
		},
		{
			name:      "rejects letters in a phone number",
			mutate:    func(r *Registration) { r.Phone = "+91call-me" },
			wantField: "phone",
		},
		{
			name:      "rejects a one character name",
			mutate:    func(r *Registration) { r.FullName = "D" },
			wantField: "full_name",
		},
		{
			name:      "rejects a password below the length floor",
			mutate:    func(r *Registration) { r.Password = strings.Repeat("a", MinPasswordLength-1) },
			wantField: "password",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			registration := valid()
			if test.mutate != nil {
				test.mutate(&registration)
			}

			err := registration.Validate()
			if test.wantField == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			var validation ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error = %v, want a ValidationError", err)
			}
			if validation.Field != test.wantField {
				t.Fatalf("field = %q, want %q", validation.Field, test.wantField)
			}
		})
	}
}

func TestNormalizeUsernameIsIdempotent(t *testing.T) {
	t.Parallel()

	once := NormalizeUsername("  Dhanush.CN ")
	twice := NormalizeUsername(once)
	if once != twice {
		t.Fatalf("normalisation is not idempotent: %q then %q", once, twice)
	}
	if once != "dhanush.cn" {
		t.Fatalf("normalised = %q", once)
	}
}
