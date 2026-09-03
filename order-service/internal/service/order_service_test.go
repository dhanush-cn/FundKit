// Engineered by Dhanush C N (github.com/dhanush-cn)
package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/dhanush-cn/fundkit/order-service/internal/domain"
	"github.com/dhanush-cn/fundkit/order-service/internal/platform/trace"
)

// fakeRepo is an in-memory order store. It implements the same conditional
// UPDATE semantics as the Postgres repository — a transition only lands if the
// row is still in the state the caller expected — and the same atomicity
// between the order write and its outbox record: if either half fails, neither
// is visible afterwards.
type fakeRepo struct {
	mu       sync.Mutex
	orders   map[string]*domain.Order
	outbox   []domain.OutboxMessage
	seq      int
	createFn func(*domain.Order) error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{orders: make(map[string]*domain.Order)}
}

func (f *fakeRepo) CreateWithOutbox(_ context.Context, order *domain.Order, build domain.OutboxBuilder) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.createFn != nil {
		if err := f.createFn(order); err != nil {
			return err
		}
	}
	f.seq++
	if order.ID == "" {
		order.ID = "order-" + string(rune('a'+f.seq-1))
	}
	order.CreatedAt = time.Now()
	order.UpdatedAt = order.CreatedAt

	// build runs before anything is committed, exactly as it does inside the
	// real transaction, so a marshalling failure rolls the order back with it.
	msg, err := build(*order)
	if err != nil {
		return err
	}

	stored := *order
	f.orders[order.ID] = &stored
	f.outbox = append(f.outbox, msg)
	return nil
}

func (f *fakeRepo) GetByID(_ context.Context, id string) (*domain.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	order, ok := f.orders[id]
	if !ok {
		return nil, domain.ErrOrderNotFound
	}
	copied := *order
	return &copied, nil
}

func (f *fakeRepo) List(_ context.Context, limit int) ([]domain.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	list := make([]domain.Order, 0, len(f.orders))
	for _, order := range f.orders {
		if len(list) == limit {
			break
		}
		list = append(list, *order)
	}
	return list, nil
}

func (f *fakeRepo) UpdateStatusWithOutbox(_ context.Context, id string, expected, next domain.OrderStatus, build domain.OutboxBuilder) (*domain.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	order, ok := f.orders[id]
	if !ok {
		return nil, domain.ErrOrderNotFound
	}
	if order.Status != expected {
		return nil, domain.ErrInvalidTransition
	}

	candidate := *order
	candidate.Status = next
	candidate.UpdatedAt = time.Now()

	msg, err := build(candidate)
	if err != nil {
		return nil, err
	}

	*order = candidate
	f.outbox = append(f.outbox, msg)

	copied := candidate
	return &copied, nil
}

// outboxStatuses replays the committed event stream, decoding each payload the
// way notification-service will.
func (f *fakeRepo) outboxStatuses(t *testing.T) []domain.OrderStatus {
	t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()

	statuses := make([]domain.OrderStatus, 0, len(f.outbox))
	for _, msg := range f.outbox {
		var event domain.OrderEvent
		if err := json.Unmarshal(msg.Payload, &event); err != nil {
			t.Fatalf("outbox payload %d is not a valid envelope: %v", msg.ID, err)
		}
		statuses = append(statuses, event.Order.Status)
	}
	return statuses
}

func (f *fakeRepo) outboxLen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.outbox)
}

func (f *fakeRepo) outboxAt(index int) domain.OutboxMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.outbox[index]
}

func (f *fakeRepo) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.orders[id]; !ok {
		return domain.ErrOrderNotFound
	}
	delete(f.orders, id)
	return nil
}

func (f *fakeRepo) statusOf(t *testing.T, id string) domain.OrderStatus {
	t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()
	order, ok := f.orders[id]
	if !ok {
		t.Fatalf("order %s is gone", id)
	}
	return order.Status
}

// fakeIdem records claims so double submission can be asserted.
type fakeIdem struct {
	mu        sync.Mutex
	claimed   map[string]bool
	confirmed map[string]bool
	claimErr  error
}

func newFakeIdem() *fakeIdem {
	return &fakeIdem{claimed: make(map[string]bool), confirmed: make(map[string]bool)}
}

func (f *fakeIdem) ClaimIdempotency(_ context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.claimErr != nil {
		return false, f.claimErr
	}
	if f.claimed[key] {
		return false, nil
	}
	f.claimed[key] = true
	return true, nil
}

func (f *fakeIdem) ConfirmIdempotency(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirmed[key] = true
	return nil
}

func (f *fakeIdem) ReleaseIdempotency(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.claimed, key)
	return nil
}

func (f *fakeIdem) isClaimed(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.claimed[key]
}

func newTestService(repo Repository, idem IdempotencyStore) *OrderService {
	return NewOrderService(repo, idem, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func newOrderInput() domain.NewOrder {
	return domain.NewOrder{
		UserID:         "user-1",
		UserName:       "Dhanush C N",
		UserEmail:      "dhanush@example.com",
		UserPhone:      "+919876543210",
		FundID:         "quant-small-cap-fund",
		Amount:         5000,
		Type:           domain.TypeSIP,
		IdempotencyKey: "key-1",
	}
}

func TestPlaceStampsContactDetailsOntoTheOrder(t *testing.T) {
	t.Parallel()

	repo, idem := newFakeRepo(), newFakeIdem()
	orders := newTestService(repo, idem)

	order, err := orders.Place(context.Background(), newOrderInput())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	orders.Drain()

	// Without these, notification-service has nowhere to send the confirmation.
	if order.UserEmail != "dhanush@example.com" || order.UserPhone != "+919876543210" {
		t.Fatalf("contact details were not persisted: %+v", order)
	}
	if order.UserName != "Dhanush C N" {
		t.Fatalf("display name = %q", order.UserName)
	}
}

func TestPlaceRejectsAReplayedIdempotencyKey(t *testing.T) {
	t.Parallel()

	repo, idem := newFakeRepo(), newFakeIdem()
	orders := newTestService(repo, idem)

	if _, err := orders.Place(context.Background(), newOrderInput()); err != nil {
		t.Fatalf("first place: %v", err)
	}
	orders.Drain()

	_, err := orders.Place(context.Background(), newOrderInput())
	if !errors.Is(err, domain.ErrDuplicateOrder) {
		t.Fatalf("error = %v, want ErrDuplicateOrder", err)
	}
	orders.Drain()

	list, err := orders.List(context.Background(), 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("stored %d orders, want exactly 1 for a retried request", len(list))
	}
}

// A failed insert must give the key back, or a legitimate retry after a
// transient database error would be rejected forever.
func TestPlaceReleasesTheKeyWhenPersistenceFails(t *testing.T) {
	t.Parallel()

	repo, idem := newFakeRepo(), newFakeIdem()
	repo.createFn = func(*domain.Order) error { return errors.New("connection reset") }
	orders := newTestService(repo, idem)

	if _, err := orders.Place(context.Background(), newOrderInput()); err == nil {
		t.Fatal("expected the insert failure to surface")
	}
	orders.Drain()

	if idem.isClaimed("key-1") {
		t.Fatal("idempotency key stayed claimed after a failed insert")
	}
}

func TestPlaceDrivesTheOrderToATerminalState(t *testing.T) {
	t.Parallel()

	repo, idem := newFakeRepo(), newFakeIdem()
	orders := newTestService(repo, idem)

	order, err := orders.Place(context.Background(), newOrderInput())
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	// Drain is the shutdown path: it must wait for the detached worker rather
	// than leaving the order half-processed.
	orders.Drain()

	if got := repo.statusOf(t, order.ID); got != domain.StatusExecuted {
		t.Fatalf("final status = %s, want EXECUTED", got)
	}

	statuses := repo.outboxStatuses(t)
	want := []domain.OrderStatus{domain.StatusPending, domain.StatusProcessing, domain.StatusExecuted}
	if len(statuses) != len(want) {
		t.Fatalf("outbox recorded %v, want %v", statuses, want)
	}
	for index := range want {
		if statuses[index] != want[index] {
			t.Fatalf("outbox recorded %v, want %v", statuses, want)
		}
	}
}

// This is the property the whole outbox change exists to guarantee: a failed
// write leaves neither an order nor an event. Under the old dual-write design
// the equivalent hole was the other way round — the order committed and the
// event vanished with no record that it ever should have existed.
func TestAFailedWriteCommitsNeitherTheOrderNorItsEvent(t *testing.T) {
	t.Parallel()

	repo, idem := newFakeRepo(), newFakeIdem()
	repo.createFn = func(*domain.Order) error { return errors.New("connection reset") }
	orders := newTestService(repo, idem)

	if _, err := orders.Place(context.Background(), newOrderInput()); err == nil {
		t.Fatal("expected the insert failure to surface")
	}
	orders.Drain()

	if got := repo.outboxLen(); got != 0 {
		t.Fatalf("outbox holds %d records after a failed insert, want 0", got)
	}
	list, err := orders.List(context.Background(), 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("stored %d orders after a failed insert, want 0", len(list))
	}
}

// Order-service is no longer able to reach Kafka at all, so an outage cannot
// affect the write path. What must survive the seam instead is the correlation
// id: it travels in a database column, which is what keeps a trace joined
// across the asynchronous half of the request.
func TestTheOutboxRecordCarriesTheRequestCorrelationID(t *testing.T) {
	t.Parallel()

	repo, idem := newFakeRepo(), newFakeIdem()
	orders := newTestService(repo, idem)

	ctx := trace.WithRequestID(context.Background(), "trace-me-12345")
	order, err := orders.Place(ctx, newOrderInput())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	orders.Drain()

	// One event per transition: PENDING on placement, then PROCESSING and
	// EXECUTED from the detached lifecycle worker.
	if got := repo.outboxLen(); got != 3 {
		t.Fatalf("outbox holds %d records, want 3 (one per transition)", got)
	}

	first := repo.outboxAt(0)
	if first.RequestID != "trace-me-12345" {
		t.Fatalf("outbox request_id = %q, want the inbound correlation id", first.RequestID)
	}
	if first.AggregateID != order.ID {
		t.Fatalf("outbox aggregate_id = %q, want %q", first.AggregateID, order.ID)
	}
	if first.AggregateType != domain.AggregateOrder {
		t.Fatalf("outbox aggregate_type = %q, want %q", first.AggregateType, domain.AggregateOrder)
	}
	if first.Status != domain.OutboxPending {
		t.Fatalf("outbox status = %q, want PENDING so the relay picks it up", first.Status)
	}

	// The detached lifecycle worker keeps the id even though it outlives the
	// request context that carried it in.
	for index, msg := range []domain.OutboxMessage{repo.outboxAt(1), repo.outboxAt(2)} {
		if msg.RequestID != "trace-me-12345" {
			t.Fatalf("lifecycle event %d lost the correlation id: %q", index+1, msg.RequestID)
		}
	}
}

func TestPlaceFailsAZeroAmountOrder(t *testing.T) {
	t.Parallel()

	repo, idem := newFakeRepo(), newFakeIdem()
	orders := newTestService(repo, idem)

	input := newOrderInput()
	input.Amount = 0

	order, err := orders.Place(context.Background(), input)
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	orders.Drain()

	if got := repo.statusOf(t, order.ID); got != domain.StatusFailed {
		t.Fatalf("status = %s, want FAILED", got)
	}
}

func TestUpdateStatusRefusesAnIllegalTransition(t *testing.T) {
	t.Parallel()

	repo, idem := newFakeRepo(), newFakeIdem()
	orders := newTestService(repo, idem)

	order, err := orders.Place(context.Background(), newOrderInput())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	orders.Drain()

	// EXECUTED is absorbing: nothing walks back out of it.
	if _, err := orders.UpdateStatus(context.Background(), order.ID, domain.StatusPending); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("error = %v, want ErrInvalidTransition", err)
	}
}

func TestDeleteOnlyRemovesPendingOrders(t *testing.T) {
	t.Parallel()

	repo, idem := newFakeRepo(), newFakeIdem()
	orders := newTestService(repo, idem)

	order, err := orders.Place(context.Background(), newOrderInput())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	orders.Drain()

	if err := orders.Delete(context.Background(), order.ID); !errors.Is(err, domain.ErrOrderNotCancelable) {
		t.Fatalf("error = %v, want ErrOrderNotCancelable for a completed order", err)
	}

	if err := orders.Delete(context.Background(), "no-such-order"); !errors.Is(err, domain.ErrOrderNotFound) {
		t.Fatalf("error = %v, want ErrOrderNotFound", err)
	}
}

func TestPlacePropagatesIdempotencyStoreFailures(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	idem := newFakeIdem()
	idem.claimErr = errors.New("redis down")
	orders := newTestService(repo, idem)

	if _, err := orders.Place(context.Background(), newOrderInput()); !errors.Is(err, idem.claimErr) {
		t.Fatalf("error = %v, want the redis failure", err)
	}
}
