//go:build integration

// Integration tests for the identity store.
//
// The uniqueness of a username and an email address is enforced by database
// indexes rather than by a read-then-write check in Go, precisely because two
// concurrent registrations would both pass such a check. Only a real database
// can prove that guarantee holds.
//
// Run with: go test -tags=integration ./...
// Requires: FUNDKIT_TEST_DB_URL pointing at a throwaway Postgres.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package repository

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/dhanush-cn/fundkit/api-gateway/internal/config"
	"github.com/dhanush-cn/fundkit/api-gateway/internal/domain"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := os.Getenv("FUNDKIT_TEST_DB_URL")
	if dsn == "" {
		t.Skip("FUNDKIT_TEST_DB_URL is not set; skipping the Postgres integration suite")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := OpenPostgres(ctx, config.DatabaseConfig{
		URL:             dsn,
		ConnectTimeout:  15 * time.Second,
		MaxOpenConns:    10,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Minute,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("connect to the test database: %v", err)
	}

	t.Cleanup(func() {
		db.Exec("DELETE FROM users")
		_ = ClosePostgres(db)
	})

	if err := db.Exec("DELETE FROM users").Error; err != nil {
		t.Fatalf("clean the users table: %v", err)
	}
	return db
}

func newUser(username, email string) *domain.User {
	return &domain.User{
		Username:     username,
		Email:        email,
		Phone:        "+919876543210",
		FullName:     "Dhanush C N",
		PasswordHash: "$2a$04$abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTU",
	}
}

func TestIntegrationMigrationCreatesTheUsersTable(t *testing.T) {
	db := testDB(t)

	if !db.Migrator().HasTable(&domain.User{}) {
		t.Fatal("AutoMigrate did not create the users table")
	}
	for _, column := range []string{"username", "email", "phone", "full_name", "password_hash"} {
		if !db.Migrator().HasColumn(&domain.User{}, column) {
			t.Fatalf("users table is missing the %q column", column)
		}
	}
}

func TestIntegrationCreateAndLookup(t *testing.T) {
	repo := NewUserRepository(testDB(t))
	ctx := context.Background()

	user := newUser("dhanush", "dhanush@example.com")
	if err := repo.Create(ctx, user); err != nil {
		t.Fatalf("create: %v", err)
	}
	if user.ID == "" {
		t.Fatal("BeforeCreate did not assign a primary key")
	}

	byName, err := repo.GetByUsername(ctx, "dhanush")
	if err != nil {
		t.Fatalf("get by username: %v", err)
	}
	if byName.ID != user.ID || byName.Email != "dhanush@example.com" {
		t.Fatalf("lookup returned %+v", byName)
	}

	byID, err := repo.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if byID.Username != "dhanush" {
		t.Fatalf("lookup by id returned %+v", byID)
	}
}

func TestIntegrationMissingUserIsReportedAsNotFound(t *testing.T) {
	repo := NewUserRepository(testDB(t))
	ctx := context.Background()

	if _, err := repo.GetByUsername(ctx, "nobody"); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("error = %v, want ErrUserNotFound", err)
	}
	if _, err := repo.GetByID(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("error = %v, want ErrUserNotFound", err)
	}
}

func TestIntegrationUniqueIndexesRejectDuplicates(t *testing.T) {
	repo := NewUserRepository(testDB(t))
	ctx := context.Background()

	if err := repo.Create(ctx, newUser("dhanush", "dhanush@example.com")); err != nil {
		t.Fatalf("first create: %v", err)
	}

	if err := repo.Create(ctx, newUser("dhanush", "other@example.com")); !errors.Is(err, domain.ErrUsernameTaken) {
		t.Fatalf("duplicate username error = %v, want ErrUsernameTaken", err)
	}
	if err := repo.Create(ctx, newUser("someone-else", "dhanush@example.com")); !errors.Is(err, domain.ErrEmailTaken) {
		t.Fatalf("duplicate email error = %v, want ErrEmailTaken", err)
	}
}

// The race a pre-check in Go cannot win: several requests registering the same
// username at the same instant.
func TestIntegrationConcurrentRegistrationsProduceOneAccount(t *testing.T) {
	repo := NewUserRepository(testDB(t))
	ctx := context.Background()

	const racers = 8
	var (
		wait      sync.WaitGroup
		mu        sync.Mutex
		succeeded int
	)

	start := make(chan struct{})
	for racer := 0; racer < racers; racer++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start

			// Same username, different emails: the username index is the one
			// under test.
			user := newUser("contended", "contended-"+string(rune('a'+index))+"@example.com")
			err := repo.Create(ctx, user)

			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				succeeded++
			}
		}(racer)
	}

	close(start)
	wait.Wait()

	if succeeded != 1 {
		t.Fatalf("%d concurrent registrations succeeded, want exactly 1", succeeded)
	}
}
