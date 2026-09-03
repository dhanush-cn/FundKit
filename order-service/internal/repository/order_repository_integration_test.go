//go:build integration

// Integration tests for the Postgres adapter.
//
// These run against a real database because the behaviour under test *is* the
// database's: the unique index that backstops idempotency, and the conditional
// UPDATE that makes a status transition safe under concurrency. A mock would
// only assert that the code calls the methods the code calls.
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

	"github.com/dhanush-cn/fundkit/order-service/internal/config"
	"github.com/dhanush-cn/fundkit/order-service/internal/domain"
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
		// Leave the schema behind but never the rows: the next test must not
		// inherit this one's state.
		db.Exec("DELETE FROM orders")
		_ = ClosePostgres(db)
	})

	if err := db.Exec("DELETE FROM orders").Error; err != nil {
		t.Fatalf("clean the orders table: %v", err)
	}
	return db
}

func newOrder(key string) *domain.Order {
	return &domain.Order{
		UserID:         "user-1",
		UserName:       "Dhanush C N",
		UserEmail:      "dhanush@example.com",
		UserPhone:      "+919876543210",
		FundID:         "quant-small-cap-fund",
		Amount:         5000,
		Type:           domain.TypeSIP,
		Status:         domain.StatusPending,
		IdempotencyKey: key,
	}
}

func TestIntegrationCreateAssignsAnIDAndPersistsContactDetails(t *testing.T) {
	repo := NewOrderRepository(testDB(t))
	ctx := context.Background()

	order := newOrder("integration-key-1")
	if err := repo.Create(ctx, order); err != nil {
		t.Fatalf("create: %v", err)
	}
	if order.ID == "" {
		t.Fatal("BeforeCreate did not assign a primary key")
	}

	found, err := repo.GetByID(ctx, order.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if found.UserEmail != "dhanush@example.com" || found.UserPhone != "+919876543210" {
		t.Fatalf("contact columns did not round-trip: %+v", found)
	}
}

// The Redis reservation is the fast guard; this unique index is the
// authoritative one. If the migration ever loses it, two identical requests
// become two real orders.
func TestIntegrationDuplicateIdempotencyKeyIsRejectedByTheDatabase(t *testing.T) {
	repo := NewOrderRepository(testDB(t))
	ctx := context.Background()

	if err := repo.Create(ctx, newOrder("integration-key-dup")); err != nil {
		t.Fatalf("first create: %v", err)
	}

	err := repo.Create(ctx, newOrder("integration-key-dup"))
	if !errors.Is(err, domain.ErrDuplicateOrder) {
		t.Fatalf("error = %v, want ErrDuplicateOrder from the unique index", err)
	}
}

// Only one of two racing writers may move the order out of PENDING. This is the
// exact race between the API and the background lifecycle worker.
func TestIntegrationConditionalUpdateSerialisesConcurrentTransitions(t *testing.T) {
	repo := NewOrderRepository(testDB(t))
	ctx := context.Background()

	order := newOrder("integration-key-race")
	if err := repo.Create(ctx, order); err != nil {
		t.Fatalf("create: %v", err)
	}

	const racers = 8
	var (
		wait      sync.WaitGroup
		mu        sync.Mutex
		succeeded int
	)

	start := make(chan struct{})
	for racer := 0; racer < racers; racer++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start

			err := repo.UpdateStatus(ctx, order.ID, domain.StatusPending, domain.StatusProcessing)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				succeeded++
			}
		}()
	}

	close(start)
	wait.Wait()

	if succeeded != 1 {
		t.Fatalf("%d writers moved the order out of PENDING, want exactly 1", succeeded)
	}

	found, err := repo.GetByID(ctx, order.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if found.Status != domain.StatusProcessing {
		t.Fatalf("status = %s, want PROCESSING", found.Status)
	}
}

func TestIntegrationUpdateFromTheWrongStateIsRefused(t *testing.T) {
	repo := NewOrderRepository(testDB(t))
	ctx := context.Background()

	order := newOrder("integration-key-state")
	if err := repo.Create(ctx, order); err != nil {
		t.Fatalf("create: %v", err)
	}

	err := repo.UpdateStatus(ctx, order.ID, domain.StatusProcessing, domain.StatusExecuted)
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("error = %v, want ErrInvalidTransition", err)
	}
}

func TestIntegrationListIsNewestFirst(t *testing.T) {
	repo := NewOrderRepository(testDB(t))
	ctx := context.Background()

	for index, key := range []string{"integration-list-1", "integration-list-2", "integration-list-3"} {
		order := newOrder(key)
		if err := repo.Create(ctx, order); err != nil {
			t.Fatalf("create %d: %v", index, err)
		}
		// Postgres timestamps have microsecond resolution; a pause keeps the
		// ordering deterministic rather than dependent on clock granularity.
		time.Sleep(5 * time.Millisecond)
	}

	orders, err := repo.List(ctx, 2)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(orders) != 2 {
		t.Fatalf("returned %d orders, want the limit of 2", len(orders))
	}
	if orders[0].CreatedAt.Before(orders[1].CreatedAt) {
		t.Fatal("list is not newest-first")
	}
}

func TestIntegrationDeleteAndMissingRows(t *testing.T) {
	repo := NewOrderRepository(testDB(t))
	ctx := context.Background()

	order := newOrder("integration-key-delete")
	if err := repo.Create(ctx, order); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Delete(ctx, order.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := repo.GetByID(ctx, order.ID); !errors.Is(err, domain.ErrOrderNotFound) {
		t.Fatalf("error = %v, want ErrOrderNotFound", err)
	}
	if err := repo.Delete(ctx, order.ID); !errors.Is(err, domain.ErrOrderNotFound) {
		t.Fatalf("second delete error = %v, want ErrOrderNotFound", err)
	}
}
