//go:build integration

// Integration tests for the Redis idempotency guard.
//
// SETNX either wins or it does not; that atomicity is the whole mechanism, and
// it cannot be verified against a fake that simply implements the same check in
// Go. Run with: go test -tags=integration ./...
// Requires: FUNDKIT_TEST_REDIS_URL (host:port).
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package cache

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/dhanush-cn/fundkit/order-service/internal/config"
)

func testClient(t *testing.T) *Client {
	t.Helper()

	addr := os.Getenv("FUNDKIT_TEST_REDIS_URL")
	if addr == "" {
		t.Skip("FUNDKIT_TEST_REDIS_URL is not set; skipping the Redis integration suite")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client, err := New(ctx, config.RedisConfig{
		Addr:           addr,
		DialTimeout:    5 * time.Second,
		IdempotencyTTL: time.Minute,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("connect to the test redis: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })
	return client
}

func uniqueKey(t *testing.T) string {
	t.Helper()
	return t.Name() + "-" + time.Now().UTC().Format("150405.000000000")
}

func TestIntegrationClaimIsExclusive(t *testing.T) {
	client := testClient(t)
	ctx := context.Background()
	key := uniqueKey(t)

	claimed, err := client.ClaimIdempotency(ctx, key)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !claimed {
		t.Fatal("the first claim on a fresh key must succeed")
	}

	claimedAgain, err := client.ClaimIdempotency(ctx, key)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if claimedAgain {
		t.Fatal("a second claim on the same key must be refused")
	}
}

// This is the property that matters under load: N replicas racing on the same
// retried request, and exactly one of them allowed to place the order.
func TestIntegrationConcurrentClaimsProduceExactlyOneWinner(t *testing.T) {
	client := testClient(t)
	ctx := context.Background()
	key := uniqueKey(t)

	const racers = 20
	var (
		wait    sync.WaitGroup
		mu      sync.Mutex
		winners int
	)

	start := make(chan struct{})
	for racer := 0; racer < racers; racer++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start

			claimed, err := client.ClaimIdempotency(ctx, key)
			mu.Lock()
			defer mu.Unlock()
			if err == nil && claimed {
				winners++
			}
		}()
	}

	close(start)
	wait.Wait()

	if winners != 1 {
		t.Fatalf("%d concurrent claims succeeded, want exactly 1", winners)
	}
}

// A released key must be usable again, otherwise a transient database failure
// would permanently block an honest retry.
func TestIntegrationReleaseAllowsAnHonestRetry(t *testing.T) {
	client := testClient(t)
	ctx := context.Background()
	key := uniqueKey(t)

	if _, err := client.ClaimIdempotency(ctx, key); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := client.ReleaseIdempotency(ctx, key); err != nil {
		t.Fatalf("release: %v", err)
	}

	claimed, err := client.ClaimIdempotency(ctx, key)
	if err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	if !claimed {
		t.Fatal("a released key must be claimable again")
	}
}

// Confirm marks the reservation as durably processed; it must not reopen it.
func TestIntegrationConfirmKeepsTheKeyReserved(t *testing.T) {
	client := testClient(t)
	ctx := context.Background()
	key := uniqueKey(t)

	if _, err := client.ClaimIdempotency(ctx, key); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := client.ConfirmIdempotency(ctx, key); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	claimed, err := client.ClaimIdempotency(ctx, key)
	if err != nil {
		t.Fatalf("claim after confirm: %v", err)
	}
	if claimed {
		t.Fatal("a confirmed key must stay reserved")
	}
}

func TestIntegrationPingReportsAHealthyConnection(t *testing.T) {
	if err := testClient(t).Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
}
