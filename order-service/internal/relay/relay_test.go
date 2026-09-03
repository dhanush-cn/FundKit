// Engineered by Dhanush C N (github.com/dhanush-cn)
package relay

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/dhanush-cn/fundkit/order-service/internal/domain"
)

// fakeStore mimics the repository's batch semantics without a database: it
// claims up to limit pending records, hands each to publish, keeps the
// successes and increments attempts on the failures.
type fakeStore struct {
	mu       sync.Mutex
	pending  []domain.OutboxMessage
	attempts map[int64]int
	failWith error
	calls    int
}

func newFakeStore(n int) *fakeStore {
	store := &fakeStore{attempts: make(map[int64]int)}
	for i := 1; i <= n; i++ {
		store.pending = append(store.pending, domain.OutboxMessage{
			ID:            int64(i),
			AggregateType: domain.AggregateOrder,
			AggregateID:   "order-1",
			EventType:     domain.EventOrderStatusChanged,
			Status:        domain.OutboxPending,
		})
	}
	return store
}

func (f *fakeStore) PublishBatch(
	ctx context.Context,
	limit, maxAttempts int,
	publish func(context.Context, domain.OutboxMessage) error,
) (domain.OutboxBatchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++
	if f.failWith != nil {
		return domain.OutboxBatchResult{}, f.failWith
	}

	size := min(limit, len(f.pending))
	batch := f.pending[:size]

	var result domain.OutboxBatchResult
	result.Claimed = len(batch)

	remaining := make([]domain.OutboxMessage, 0, len(f.pending))
	for _, msg := range batch {
		if err := publish(ctx, msg); err != nil {
			f.attempts[msg.ID]++
			result.Failed++
			if result.Err == nil {
				result.Err = err
			}
			if f.attempts[msg.ID] < maxAttempts {
				remaining = append(remaining, msg)
			}
			continue
		}
		result.Published++
	}
	f.pending = append(remaining, f.pending[size:]...)
	return result, nil
}

func (f *fakeStore) pendingCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pending)
}

// fakePublisher records what reached the broker.
type fakePublisher struct {
	mu   sync.Mutex
	sent []int64
	err  error
}

func (p *fakePublisher) PublishOutbox(_ context.Context, msg domain.OutboxMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.err != nil {
		return p.err
	}
	p.sent = append(p.sent, msg.ID)
	return nil
}

func (p *fakePublisher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.sent)
}

func testRelay(store Store, pub Publisher, cfg Config) *Relay {
	return New(store, pub, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestRelayDrainsTheBacklogAndStopsOnCancellation(t *testing.T) {
	t.Parallel()

	store := newFakeStore(250)
	pub := &fakePublisher{}
	worker := testRelay(store, pub, Config{Interval: time.Millisecond, BatchSize: 100})

	ctx, cancel := context.WithCancel(context.Background())
	go worker.Run(ctx)

	// A backlog larger than one batch must be cleared in a single pass rather
	// than one batch per tick.
	waitFor(t, time.Second, func() bool { return pub.count() == 250 })

	cancel()
	waitForRelay(t, worker, time.Second)

	if store.pendingCount() != 0 {
		t.Fatalf("%d records left pending", store.pendingCount())
	}
}

// The shutdown flush is the difference between "events committed in the last
// second before SIGTERM go out now" and "they wait for the next boot".
func TestRelayFlushesOnShutdown(t *testing.T) {
	t.Parallel()

	store := newFakeStore(5)
	pub := &fakePublisher{}
	worker := testRelay(store, pub, Config{Interval: time.Hour, BatchSize: 100, ShutdownFlush: time.Second})

	// Already cancelled: Run goes straight to its flush, with no ordinary poll
	// in between. Deterministic, no sleeping.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	worker.Run(ctx)

	if pub.count() != 5 {
		t.Fatalf("flush published %d of 5 records", pub.count())
	}
}

func TestRelayRetiresARecordAfterItsAttemptBudget(t *testing.T) {
	t.Parallel()

	store := newFakeStore(1)
	pub := &fakePublisher{err: errors.New("kafka unavailable")}
	worker := testRelay(store, pub, Config{
		Interval:    time.Millisecond,
		BatchSize:   10,
		MaxAttempts: 3,
		MaxBackoff:  5 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	go worker.Run(ctx)

	// A permanently unpublishable record must stop being retried instead of
	// blocking the queue forever.
	waitFor(t, 2*time.Second, func() bool { return store.pendingCount() == 0 })

	cancel()
	waitForRelay(t, worker, time.Second)

	if pub.count() != 0 {
		t.Fatalf("publisher was failing, yet %d records were recorded as sent", pub.count())
	}
}

func TestRelaySurvivesAStoreOutage(t *testing.T) {
	t.Parallel()

	store := newFakeStore(1)
	store.failWith = errors.New("postgres down")
	pub := &fakePublisher{}
	worker := testRelay(store, pub, Config{
		Interval:   time.Millisecond,
		BatchSize:  10,
		MaxBackoff: 10 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	go worker.Run(ctx)

	// It must keep polling rather than exiting, so the relay recovers by itself
	// when the database comes back.
	waitFor(t, time.Second, func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return store.calls >= 3
	})

	cancel()
	waitForRelay(t, worker, time.Second)
}

func TestBackoffIsCappedAtTheConfiguredCeiling(t *testing.T) {
	t.Parallel()

	current := time.Second
	for range 10 {
		current = nextBackoff(current, 30*time.Second)
	}
	if current != 30*time.Second {
		t.Fatalf("backoff = %s, want it pinned at the 30s ceiling", current)
	}
}

func waitFor(t *testing.T, limit time.Duration, done func() bool) {
	t.Helper()

	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met within %s", limit)
}

func waitForRelay(t *testing.T, worker *Relay, limit time.Duration) {
	t.Helper()

	stopped := make(chan struct{})
	go func() {
		worker.Wait()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(limit):
		t.Fatalf("relay did not stop within %s", limit)
	}
}
