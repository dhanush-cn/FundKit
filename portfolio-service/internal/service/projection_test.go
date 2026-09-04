// Engineered by Dhanush C N (github.com/dhanush-cn)
package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/dhanush-cn/fundkit/portfolio-service/internal/domain"
	"github.com/dhanush-cn/fundkit/portfolio-service/internal/repository"
)

// fakeStore records what the projection asked the ledger to do, so these tests
// assert on the movement rather than on the state it produces — the state is
// the repository's own test's job.
type fakeStore struct {
	applied []repository.Movement
	err     error
	dupe    bool
}

func (f *fakeStore) ListByUser(context.Context, string) ([]domain.Holding, error) { return nil, nil }

func (f *fakeStore) Apply(_ context.Context, movement repository.Movement) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	f.applied = append(f.applied, movement)
	return !f.dupe, nil
}

type fixedNAV struct {
	price float64
	err   error
}

func (f fixedNAV) Fetch(context.Context, string) (float64, error) { return f.price, f.err }

// missCache is a NAVCache that never hits and never stores, so these tests
// exercise the feed path without a Redis.
type missCache struct{}

func (missCache) GetNAV(context.Context, string) (float64, bool, error) { return 0, false, nil }
func (missCache) SetNAV(context.Context, string, float64) error         { return nil }

func newProjection(store *fakeStore, nav float64) *PortfolioService {
	return NewPortfolioService(store, fixedNAV{price: nav}, missCache{},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func executed(orderType string) domain.OrderEvent {
	return domain.OrderEvent{
		EventID:   "e1",
		EventType: domain.EventOrderStatusChanged,
		Version:   domain.SupportedOrderEventVersion,
		Order: domain.Order{
			ID:     "ord-1",
			UserID: "user-1",
			FundID: "fund-axi-blue",
			Amount: 150000, // ₹1,500.00
			Type:   orderType,
			Status: "EXECUTED",
		},
	}
}

func TestApplyOrderEventPricesTheFill(t *testing.T) {
	store := &fakeStore{}

	applied, err := newProjection(store, 125).ApplyOrderEvent(context.Background(), executed("LUMPSUM"))
	if err != nil {
		t.Fatalf("ApplyOrderEvent() error = %v", err)
	}
	if !applied {
		t.Fatal("ApplyOrderEvent() reported no change")
	}
	if len(store.applied) != 1 {
		t.Fatalf("len(applied) = %d, want 1", len(store.applied))
	}

	movement := store.applied[0]
	if movement.Side != domain.SideBuy {
		t.Errorf("Side = %v, want BUY", movement.Side)
	}
	// ₹1,500.00 at a NAV of ₹125.00 is 12 units.
	if movement.Units != 12 {
		t.Errorf("Units = %v, want 12", movement.Units)
	}
	// The amount passes through as paise, untouched by the pricing.
	if movement.Amount != 150000 {
		t.Errorf("Amount = %d, want 150000", movement.Amount)
	}
	if movement.EventID != "e1" {
		t.Errorf("EventID = %q, want the envelope's id so the ledger can deduplicate", movement.EventID)
	}
}

// TestNonExecutedEventsAreSkipped is the filter that keeps most of the topic
// from touching the ledger. A skip is a success — the consumer must commit the
// offset, or the partition stalls on a message nobody wants.
func TestNonExecutedEventsAreSkipped(t *testing.T) {
	for _, status := range []string{"PENDING", "PROCESSING", "FAILED"} {
		store := &fakeStore{}
		event := executed("SIP")
		event.Order.Status = status

		applied, err := newProjection(store, 125).ApplyOrderEvent(context.Background(), event)
		if err != nil {
			t.Errorf("ApplyOrderEvent() for %s error = %v; skipping is not a failure", status, err)
		}
		if applied {
			t.Errorf("ApplyOrderEvent() reported a change for a %s order", status)
		}
		if len(store.applied) != 0 {
			t.Errorf("a %s order reached the ledger", status)
		}
	}
}

// TestUnpricedFillIsRefused is the safety valve. A NAV of zero means the feed
// is down; recording a real payment as zero units is a corruption no later
// event undoes, so the projection fails and lets the consumer retry.
func TestUnpricedFillIsRefused(t *testing.T) {
	store := &fakeStore{}

	_, err := newProjection(store, 0).ApplyOrderEvent(context.Background(), executed("SIP"))
	if err == nil {
		t.Fatal("ApplyOrderEvent() accepted a fill it could not price")
	}
	if len(store.applied) != 0 {
		t.Error("an unpriced fill reached the ledger")
	}
	// Transient by design: the feed coming back fixes it, so this must NOT be
	// permanent or the consumer would park a perfectly good order.
	if errors.Is(err, domain.ErrPermanent) {
		t.Error("a NAV feed outage was classified as permanent; it must be retried, not parked")
	}
}

func TestInvalidEventIsPermanentAndNeverReachesTheLedger(t *testing.T) {
	store := &fakeStore{}
	event := executed("SIP")
	event.Version = 1

	_, err := newProjection(store, 125).ApplyOrderEvent(context.Background(), event)
	if err == nil {
		t.Fatal("ApplyOrderEvent() accepted a v1 envelope")
	}
	if !errors.Is(err, domain.ErrPermanent) {
		t.Errorf("error = %v, want it to wrap domain.ErrPermanent", err)
	}
	if len(store.applied) != 0 {
		t.Error("an invalid event reached the ledger")
	}
}

func TestUnknownOrderTypeNeverReachesTheLedger(t *testing.T) {
	store := &fakeStore{}

	_, err := newProjection(store, 125).ApplyOrderEvent(context.Background(), executed("SWITCH"))
	if !errors.Is(err, domain.ErrPermanent) {
		t.Errorf("error = %v, want a permanent failure for an unmapped order type", err)
	}
	if len(store.applied) != 0 {
		t.Error("an unmapped order type reached the ledger")
	}
}

// TestDuplicateReportsNoChange checks the third outcome: the ledger recognised a
// redelivery and skipped it, which the consumer must treat as a success.
func TestDuplicateReportsNoChange(t *testing.T) {
	store := &fakeStore{dupe: true}

	applied, err := newProjection(store, 125).ApplyOrderEvent(context.Background(), executed("SIP"))
	if err != nil {
		t.Fatalf("ApplyOrderEvent() error = %v", err)
	}
	if applied {
		t.Error("ApplyOrderEvent() reported a change for an event the ledger had already applied")
	}
}

// TestEmptyPortfolioValuesToZero is requirement 3 seen from the read path: a
// user with no holdings produces a zero valuation and a non-nil empty slice, so
// the layers above serialise `"holdings": []` and the UI renders ₹0.00.
func TestEmptyPortfolioValuesToZero(t *testing.T) {
	valuation, err := newProjection(&fakeStore{}, 125).Valuate(context.Background(), "nobody")
	if err != nil {
		t.Fatalf("Valuate() error = %v; an empty portfolio is not an error", err)
	}
	if valuation.TotalValue != 0 || valuation.TotalUnrealizedGain != 0 {
		t.Errorf("totals = %s / %s, want zero", valuation.TotalValue, valuation.TotalUnrealizedGain)
	}
	if valuation.Holdings == nil {
		t.Fatal("Holdings is nil; it must be an empty slice so the JSON is [] rather than null")
	}
	if len(valuation.Holdings) != 0 {
		t.Errorf("len(Holdings) = %d, want 0", len(valuation.Holdings))
	}
}
