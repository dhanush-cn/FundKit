// Engineered by Dhanush C N (github.com/dhanush-cn)
package service

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

// fakeRepo is an in-memory order store. It implements the same conditional
// UPDATE semantics as the Postgres repository: a transition only lands if the
// row is still in the state the caller expected.
type fakeRepo struct {
	mu       sync.Mutex
	orders   map[string]*domain.Order
	seq      int
	createFn func(*domain.Order) error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{orders: make(map[string]*domain.Order)}
}

func (f *fakeRepo) Create(_ context.Context, order *domain.Order) error {
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
	stored := *order
	f.orders[order.ID] = &stored
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

func (f *fakeRepo) UpdateStatus(_ context.Context, id string, expected, next domain.OrderStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	order, ok := f.orders[id]
	if !ok {
		return domain.ErrOrderNotFound
	}
	if order.Status != expected {
		return domain.ErrInvalidTransition
	}
	order.Status = next
	order.UpdatedAt = time.Now()
	return nil
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

// recordingPublisher captures the event stream the platform would have emitted.
type recordingPublisher struct {
	mu     sync.Mutex
	events []domain.Order
	err    error
}

func (r *recordingPublisher) PublishOrderStatusChanged(_ context.Context, order domain.Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.err != nil {
		return r.err
	}
	r.events = append(r.events, order)
	return nil
}

func (r *recordingPublisher) statuses() []domain.OrderStatus {
	r.mu.Lock()
	defer r.mu.Unlock()

	statuses := make([]domain.OrderStatus, 0, len(r.events))
	for _, event := range r.events {
		statuses = append(statuses, event.Status)
	}
	return statuses
}

func newTestService(repo Repository, idem IdempotencyStore, events EventPublisher) *OrderService {
	return NewOrderService(repo, idem, events, slog.New(slog.NewTextHandler(io.Discard, nil)))
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

	repo, idem, publisher := newFakeRepo(), newFakeIdem(), &recordingPublisher{}
	orders := newTestService(repo, idem, publisher)

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

	repo, idem, publisher := newFakeRepo(), newFakeIdem(), &recordingPublisher{}
	orders := newTestService(repo, idem, publisher)

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

	repo, idem, publisher := newFakeRepo(), newFakeIdem(), &recordingPublisher{}
	repo.createFn = func(*domain.Order) error { return errors.New("connection reset") }
	orders := newTestService(repo, idem, publisher)

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

	repo, idem, publisher := newFakeRepo(), newFakeIdem(), &recordingPublisher{}
	orders := newTestService(repo, idem, publisher)

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

	statuses := publisher.statuses()
	want := []domain.OrderStatus{domain.StatusPending, domain.StatusProcessing, domain.StatusExecuted}
	if len(statuses) != len(want) {
		t.Fatalf("published %v, want %v", statuses, want)
	}
	for index := range want {
		if statuses[index] != want[index] {
			t.Fatalf("published %v, want %v", statuses, want)
		}
	}
}

func TestPlaceSurvivesAPublisherOutage(t *testing.T) {
	t.Parallel()

	repo, idem := newFakeRepo(), newFakeIdem()
	publisher := &recordingPublisher{err: errors.New("kafka unavailable")}
	orders := newTestService(repo, idem, publisher)

	// Losing an event must never roll back a committed order: the money moved,
	// the notification is the part that can be retried.
	order, err := orders.Place(context.Background(), newOrderInput())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	orders.Drain()

	if got := repo.statusOf(t, order.ID); got != domain.StatusExecuted {
		t.Fatalf("status = %s, want the order to complete regardless of Kafka", got)
	}
}

func TestPlaceFailsAZeroAmountOrder(t *testing.T) {
	t.Parallel()

	repo, idem, publisher := newFakeRepo(), newFakeIdem(), &recordingPublisher{}
	orders := newTestService(repo, idem, publisher)

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

	repo, idem, publisher := newFakeRepo(), newFakeIdem(), &recordingPublisher{}
	orders := newTestService(repo, idem, publisher)

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

	repo, idem, publisher := newFakeRepo(), newFakeIdem(), &recordingPublisher{}
	orders := newTestService(repo, idem, publisher)

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

	repo, publisher := newFakeRepo(), &recordingPublisher{}
	idem := newFakeIdem()
	idem.claimErr = errors.New("redis down")
	orders := newTestService(repo, idem, publisher)

	if _, err := orders.Place(context.Background(), newOrderInput()); !errors.Is(err, idem.claimErr) {
		t.Fatalf("error = %v, want the redis failure", err)
	}
}
