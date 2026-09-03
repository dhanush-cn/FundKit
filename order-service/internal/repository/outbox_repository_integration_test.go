//go:build integration

// Integration tests for the transactional outbox.
//
// These have to run against a real Postgres because the behaviour under test
// *is* the database's: the atomicity of the order write and its event, and the
// FOR UPDATE SKIP LOCKED claim that lets several relay replicas share one
// table without publishing duplicates. A mock would assert nothing.
//
// Run with: go test -tags=integration ./...
// Requires: FUNDKIT_TEST_DB_URL pointing at a throwaway Postgres.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/dhanush-cn/fundkit/order-service/internal/domain"
)

func pendingOutbox(t *testing.T, db *gorm.DB) []domain.OutboxMessage {
	t.Helper()

	var rows []domain.OutboxMessage
	if err := db.Where("status = ?", domain.OutboxPending).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	return rows
}

func allOutbox(t *testing.T, db *gorm.DB) []domain.OutboxMessage {
	t.Helper()

	var rows []domain.OutboxMessage
	if err := db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	return rows
}

// This is the dual-write fix, stated as a test: the order row and its event
// become visible together or not at all.
func TestIntegrationOrderAndItsEventCommitTogether(t *testing.T) {
	db := testDB(t)
	repo := NewOrderRepository(db)
	ctx := context.Background()

	order := newOrder("outbox-key-atomic")
	if err := repo.CreateWithOutbox(ctx, order, testOutbox); err != nil {
		t.Fatalf("create: %v", err)
	}

	rows := pendingOutbox(t, db)
	if len(rows) != 1 {
		t.Fatalf("outbox holds %d records, want exactly 1", len(rows))
	}

	record := rows[0]
	if record.AggregateID != order.ID {
		t.Fatalf("aggregate_id = %q, want the order's id %q", record.AggregateID, order.ID)
	}
	if record.AggregateType != domain.AggregateOrder {
		t.Fatalf("aggregate_type = %q", record.AggregateType)
	}
	if record.EventType != domain.EventOrderStatusChanged {
		t.Fatalf("event_type = %q", record.EventType)
	}
	if record.RequestID != "integration-trace" {
		t.Fatalf("request_id = %q, want the correlation id to be persisted", record.RequestID)
	}
	if record.Attempts != 0 || record.ProcessedAt != nil {
		t.Fatalf("a fresh record should be untouched: %+v", record)
	}

	// The payload must be the exact envelope notification-service will decode.
	var event domain.OrderEvent
	if err := json.Unmarshal(record.Payload, &event); err != nil {
		t.Fatalf("payload is not a valid envelope: %v", err)
	}
	if event.Order.ID != order.ID || event.Order.Status != domain.StatusPending {
		t.Fatalf("payload carries the wrong aggregate: %+v", event.Order)
	}
	if event.Version != domain.OrderEventVersion {
		t.Fatalf("envelope version = %d", event.Version)
	}
}

// The other half of atomicity: if building the event fails, the order must not
// survive on its own. Under the old design this was the silent failure mode —
// order committed, event gone.
func TestIntegrationAFailedEventRollsBackTheOrder(t *testing.T) {
	db := testDB(t)
	repo := NewOrderRepository(db)
	ctx := context.Background()

	boom := errors.New("event serialisation failed")
	order := newOrder("outbox-key-rollback")
	failing := func(domain.Order) (domain.OutboxMessage, error) {
		return domain.OutboxMessage{}, boom
	}

	if err := repo.CreateWithOutbox(ctx, order, failing); !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the builder failure to surface", err)
	}

	var orders int64
	if err := db.Model(&domain.Order{}).Count(&orders).Error; err != nil {
		t.Fatalf("count orders: %v", err)
	}
	if orders != 0 {
		t.Fatalf("%d orders survived a failed event write, want 0", orders)
	}
	if rows := allOutbox(t, db); len(rows) != 0 {
		t.Fatalf("%d outbox records survived, want 0", len(rows))
	}
}

func TestIntegrationEveryTransitionEnqueuesAnEvent(t *testing.T) {
	db := testDB(t)
	repo := NewOrderRepository(db)
	ctx := context.Background()

	order := newOrder("outbox-key-transitions")
	if err := repo.CreateWithOutbox(ctx, order, testOutbox); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.UpdateStatusWithOutbox(ctx, order.ID, domain.StatusPending, domain.StatusProcessing, testOutbox); err != nil {
		t.Fatalf("to processing: %v", err)
	}
	if _, err := repo.UpdateStatusWithOutbox(ctx, order.ID, domain.StatusProcessing, domain.StatusExecuted, testOutbox); err != nil {
		t.Fatalf("to executed: %v", err)
	}

	rows := allOutbox(t, db)
	if len(rows) != 3 {
		t.Fatalf("outbox holds %d records, want 3 (one per transition)", len(rows))
	}

	want := []domain.OrderStatus{domain.StatusPending, domain.StatusProcessing, domain.StatusExecuted}
	for index, row := range rows {
		var event domain.OrderEvent
		if err := json.Unmarshal(row.Payload, &event); err != nil {
			t.Fatalf("payload %d: %v", index, err)
		}
		if event.Order.Status != want[index] {
			t.Fatalf("event %d carries %s, want %s", index, event.Order.Status, want[index])
		}
	}

	// A refused transition must not leave a phantom event behind.
	if _, err := repo.UpdateStatusWithOutbox(ctx, order.ID, domain.StatusPending, domain.StatusProcessing, testOutbox); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("error = %v, want ErrInvalidTransition", err)
	}
	if rows := allOutbox(t, db); len(rows) != 3 {
		t.Fatalf("a refused transition wrote an event: outbox holds %d", len(rows))
	}
}

func TestIntegrationPublishBatchMarksRecordsProcessed(t *testing.T) {
	db := testDB(t)
	orders := NewOrderRepository(db)
	outbox := NewOutboxRepository(db)
	ctx := context.Background()

	for _, key := range []string{"outbox-batch-1", "outbox-batch-2", "outbox-batch-3"} {
		if err := orders.CreateWithOutbox(ctx, newOrder(key), testOutbox); err != nil {
			t.Fatalf("create %s: %v", key, err)
		}
	}

	var sent []int64
	result, err := outbox.PublishBatch(ctx, 10, 5, func(_ context.Context, msg domain.OutboxMessage) error {
		sent = append(sent, msg.ID)
		return nil
	})
	if err != nil {
		t.Fatalf("publish batch: %v", err)
	}
	if result.Claimed != 3 || result.Published != 3 || result.Failed != 0 {
		t.Fatalf("result = %+v, want 3 claimed and 3 published", result)
	}
	if len(pendingOutbox(t, db)) != 0 {
		t.Fatal("records are still PENDING after a successful publish")
	}

	for _, row := range allOutbox(t, db) {
		if row.Status != domain.OutboxProcessed {
			t.Fatalf("record %d is %s, want PROCESSED", row.ID, row.Status)
		}
		if row.ProcessedAt == nil {
			t.Fatalf("record %d has no processed_at", row.ID)
		}
	}

	// A second pass must find nothing: this is what stops a restart from
	// republishing history.
	again, err := outbox.PublishBatch(ctx, 10, 5, func(context.Context, domain.OutboxMessage) error {
		t.Fatal("a processed record was claimed a second time")
		return nil
	})
	if err != nil {
		t.Fatalf("second batch: %v", err)
	}
	if again.Claimed != 0 {
		t.Fatalf("second pass claimed %d records, want 0", again.Claimed)
	}
}

// FOR UPDATE SKIP LOCKED is what lets every replica run its own relay. Without
// it, two relays claim the same rows and every consumer sees each event twice.
func TestIntegrationConcurrentRelaysClaimDisjointBatches(t *testing.T) {
	db := testDB(t)
	orders := NewOrderRepository(db)
	ctx := context.Background()

	const records = 10
	for index := range records {
		key := "outbox-skip-locked-" + string(rune('a'+index))
		if err := orders.CreateWithOutbox(ctx, newOrder(key), testOutbox); err != nil {
			t.Fatalf("create %s: %v", key, err)
		}
	}

	var (
		mu     sync.Mutex
		seen   = make(map[int64]int)
		wait   sync.WaitGroup
		start  = make(chan struct{})
		relays = 2
	)

	for range relays {
		wait.Add(1)
		go func() {
			defer wait.Done()

			// Each relay needs its own repository handle, exactly as separate
			// replicas would have.
			outbox := NewOutboxRepository(db)
			<-start

			_, err := outbox.PublishBatch(ctx, records/relays, 5, func(_ context.Context, msg domain.OutboxMessage) error {
				mu.Lock()
				seen[msg.ID]++
				mu.Unlock()
				// Hold the claim long enough that the two transactions overlap.
				time.Sleep(20 * time.Millisecond)
				return nil
			})
			if err != nil {
				t.Errorf("relay batch: %v", err)
			}
		}()
	}

	close(start)
	wait.Wait()

	mu.Lock()
	defer mu.Unlock()
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("record %d was published %d times; SKIP LOCKED is not holding", id, count)
		}
	}
	if len(seen) != records {
		t.Fatalf("%d distinct records published, want %d", len(seen), records)
	}
}

func TestIntegrationAFailedPublishStaysPendingAndCountsAnAttempt(t *testing.T) {
	db := testDB(t)
	orders := NewOrderRepository(db)
	outbox := NewOutboxRepository(db)
	ctx := context.Background()

	if err := orders.CreateWithOutbox(ctx, newOrder("outbox-retry"), testOutbox); err != nil {
		t.Fatalf("create: %v", err)
	}

	broker := errors.New("kafka unavailable")
	result, err := outbox.PublishBatch(ctx, 10, 3, func(context.Context, domain.OutboxMessage) error {
		return broker
	})
	if err != nil {
		t.Fatalf("publish batch: %v", err)
	}
	if result.Published != 0 || result.Failed != 1 {
		t.Fatalf("result = %+v, want 0 published and 1 failed", result)
	}

	rows := pendingOutbox(t, db)
	if len(rows) != 1 {
		t.Fatalf("record did not stay PENDING for retry: %d pending", len(rows))
	}
	if rows[0].Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", rows[0].Attempts)
	}
	if rows[0].LastError == "" {
		t.Fatal("last_error was not recorded")
	}

	// Exhaust the budget: the record retires instead of blocking its aggregate
	// forever.
	for range 2 {
		if _, err := outbox.PublishBatch(ctx, 10, 3, func(context.Context, domain.OutboxMessage) error {
			return broker
		}); err != nil {
			t.Fatalf("retry batch: %v", err)
		}
	}

	if len(pendingOutbox(t, db)) != 0 {
		t.Fatal("record is still PENDING after exhausting its attempt budget")
	}
	retired := allOutbox(t, db)
	if len(retired) != 1 || retired[0].Status != domain.OutboxFailed {
		t.Fatalf("record status = %+v, want FAILED", retired)
	}

	// And an operator can put it back once the broker is healthy.
	requeued, err := outbox.RequeueFailed(ctx)
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued %d records, want 1", requeued)
	}
	if rows := pendingOutbox(t, db); len(rows) != 1 || rows[0].Attempts != 0 {
		t.Fatalf("requeue did not reset the record: %+v", rows)
	}
}
