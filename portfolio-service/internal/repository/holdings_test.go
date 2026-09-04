// Engineered by Dhanush C N (github.com/dhanush-cn)
package repository

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/dhanush-cn/fundkit/portfolio-service/internal/domain"
)

// newTestLedger builds an unseeded repository. Every test here starts empty,
// because a test that leans on the demo positions is really testing the seed
// data and will break the day someone changes it.
func newTestLedger() *HoldingsRepository {
	return NewHoldingsRepository(false)
}

func buy(eventID, user, fund string, units float64, paise domain.Money) Movement {
	return Movement{EventID: eventID, UserID: user, FundID: fund, Side: domain.SideBuy, Units: units, Amount: paise}
}

func sell(eventID, user, fund string, units float64, paise domain.Money) Movement {
	return Movement{EventID: eventID, UserID: user, FundID: fund, Side: domain.SideSell, Units: units, Amount: paise}
}

func TestApplyBuyOpensNewPosition(t *testing.T) {
	ledger := newTestLedger()

	applied, err := ledger.Apply(context.Background(), buy("e1", "user-9", "fund-axi-blue", 10, 125000))
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !applied {
		t.Fatal("Apply() reported no change for a first purchase")
	}

	holdings, err := ledger.ListByUser(context.Background(), "user-9")
	if err != nil {
		t.Fatalf("ListByUser() error = %v", err)
	}
	if len(holdings) != 1 {
		t.Fatalf("len(holdings) = %d, want 1", len(holdings))
	}
	if holdings[0].Units != 10 || holdings[0].InvestedAmount != 125000 {
		t.Errorf("position = %v units / %d paise, want 10 / 125000", holdings[0].Units, holdings[0].InvestedAmount)
	}
	// The catalog supplies the display name; the event never carries one.
	if holdings[0].FundName != "AXI Bluechip" {
		t.Errorf("FundName = %q, want %q", holdings[0].FundName, "AXI Bluechip")
	}
}

func TestApplyBuyUnknownFundFallsBackToID(t *testing.T) {
	ledger := newTestLedger()

	if _, err := ledger.Apply(context.Background(), buy("e1", "user-9", "fund-new-xyz", 4, 40000)); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	holdings, _ := ledger.ListByUser(context.Background(), "user-9")
	// A fund the catalog has not caught up with must still produce a usable
	// row. An empty name would render as a blank line on the dashboard.
	if holdings[0].FundName != "fund-new-xyz" {
		t.Errorf("FundName = %q, want the fund id as fallback", holdings[0].FundName)
	}
}

// TestApplyBuyUpdatesAverageCost is the core of the BUY path: a second purchase
// at a different price must move the average, and it must move it to the exact
// value implied by the totals rather than to the mean of the two prices.
func TestApplyBuyUpdatesAverageCost(t *testing.T) {
	ledger := newTestLedger()
	ctx := context.Background()

	// 10 units at ₹100.00 = ₹1,000.00, then 30 units at ₹200.00 = ₹6,000.00.
	// Total ₹7,000.00 over 40 units, so the average is ₹175.00 — not ₹150.00,
	// which is what averaging the two prices without weighting would give.
	mustApply(t, ledger, buy("e1", "user-9", "fund-axi-blue", 10, 100000))
	mustApply(t, ledger, buy("e2", "user-9", "fund-axi-blue", 30, 600000))

	holdings, _ := ledger.ListByUser(ctx, "user-9")
	if len(holdings) != 1 {
		t.Fatalf("len(holdings) = %d, want the two purchases merged into 1 position", len(holdings))
	}
	if holdings[0].Units != 40 {
		t.Errorf("Units = %v, want 40", holdings[0].Units)
	}
	if holdings[0].InvestedAmount != 700000 {
		t.Errorf("InvestedAmount = %d, want 700000", holdings[0].InvestedAmount)
	}
	if got := holdings[0].AvgCost(); got != 17500 {
		t.Errorf("AvgCost() = %d paise (%s), want 17500 (₹175.00)", got, got)
	}
}

func TestApplySellReducesPositionAndKeepsAverageCost(t *testing.T) {
	ledger := newTestLedger()

	mustApply(t, ledger, buy("e1", "user-9", "fund-axi-blue", 40, 700000))
	mustApply(t, ledger, sell("e2", "user-9", "fund-axi-blue", 10, 200000))

	holdings, _ := ledger.ListByUser(context.Background(), "user-9")
	if holdings[0].Units != 30 {
		t.Errorf("Units = %v, want 30", holdings[0].Units)
	}
	// A quarter of the units left, so a quarter of the basis went with them:
	// ₹7,000.00 - ₹1,750.00 = ₹5,250.00. Note this is NOT the ₹2,000.00 the
	// sale actually realised — a disposal releases cost at the average paid,
	// not at the price received, which is what leaves the average untouched.
	if holdings[0].InvestedAmount != 525000 {
		t.Errorf("InvestedAmount = %d, want 525000", holdings[0].InvestedAmount)
	}
	if got := holdings[0].AvgCost(); got != 17500 {
		t.Errorf("AvgCost() = %d, want it unchanged at 17500", got)
	}
}

// TestApplySellToZeroRemovesPosition covers the requirement that a fully exited
// holding disappears rather than lingering as a ₹0.00 row, and that a user with
// nothing left is indistinguishable from one who never bought.
func TestApplySellToZeroRemovesPosition(t *testing.T) {
	ledger := newTestLedger()
	ctx := context.Background()

	mustApply(t, ledger, buy("e1", "user-9", "fund-axi-blue", 12.5, 150000))
	mustApply(t, ledger, buy("e2", "user-9", "fund-hdfc-top", 20, 320000))
	mustApply(t, ledger, sell("e3", "user-9", "fund-axi-blue", 12.5, 160000))

	holdings, _ := ledger.ListByUser(ctx, "user-9")
	if len(holdings) != 1 {
		t.Fatalf("len(holdings) = %d, want 1 after the full exit", len(holdings))
	}
	if holdings[0].FundID != "fund-hdfc-top" {
		t.Errorf("surviving position = %s, want fund-hdfc-top", holdings[0].FundID)
	}

	mustApply(t, ledger, sell("e4", "user-9", "fund-hdfc-top", 20, 340000))

	holdings, err := ledger.ListByUser(ctx, "user-9")
	if err != nil {
		t.Fatalf("ListByUser() after full exit error = %v", err)
	}
	if holdings == nil {
		t.Fatal("ListByUser() returned nil; the API contract is an empty slice so it serialises as [] not null")
	}
	if len(holdings) != 0 {
		t.Errorf("len(holdings) = %d, want 0", len(holdings))
	}
}

// TestApplySellLeavesNoResidualUnits guards the epsilon. Repeated fractional
// sales that add up to the whole position must close it, not leave 1e-16 units
// and a few paise behind as a row the customer cannot get rid of.
func TestApplySellLeavesNoResidualUnits(t *testing.T) {
	ledger := newTestLedger()
	ctx := context.Background()

	mustApply(t, ledger, buy("e1", "user-9", "fund-axi-blue", 0.3, 3000))
	mustApply(t, ledger, sell("e2", "user-9", "fund-axi-blue", 0.1, 1000))
	mustApply(t, ledger, sell("e3", "user-9", "fund-axi-blue", 0.1, 1000))
	mustApply(t, ledger, sell("e4", "user-9", "fund-axi-blue", 0.1, 1000))

	holdings, _ := ledger.ListByUser(ctx, "user-9")
	if len(holdings) != 0 {
		t.Fatalf("len(holdings) = %d, want 0; residue = %v units / %d paise",
			len(holdings), holdings[0].Units, holdings[0].InvestedAmount)
	}
}

func TestApplySellUnheldFundIsPermanent(t *testing.T) {
	ledger := newTestLedger()

	_, err := ledger.Apply(context.Background(), sell("e1", "user-9", "fund-axi-blue", 1, 10000))
	if err == nil {
		t.Fatal("Apply() accepted a sale of a position the user does not hold")
	}
	// The classification is what routes it: permanent means park it now rather
	// than retry a message that will fail identically three times.
	if !isPermanent(err) {
		t.Errorf("error = %v, want it to wrap domain.ErrPermanent", err)
	}
}

func TestApplySellMoreThanHeldIsPermanent(t *testing.T) {
	ledger := newTestLedger()
	mustApply(t, ledger, buy("e1", "user-9", "fund-axi-blue", 5, 50000))

	_, err := ledger.Apply(context.Background(), sell("e2", "user-9", "fund-axi-blue", 6, 60000))
	if err == nil {
		t.Fatal("Apply() accepted an oversell")
	}
	if !isPermanent(err) {
		t.Errorf("error = %v, want it to wrap domain.ErrPermanent", err)
	}

	// The rejected sale must not have partially applied.
	holdings, _ := ledger.ListByUser(context.Background(), "user-9")
	if holdings[0].Units != 5 || holdings[0].InvestedAmount != 50000 {
		t.Errorf("position = %v / %d after a rejected sale, want it untouched at 5 / 50000",
			holdings[0].Units, holdings[0].InvestedAmount)
	}
}

// TestApplyIsIdempotent is the property that makes at-least-once delivery
// survivable. A redelivered purchase must be recognised and skipped, because
// applying it twice doubles a customer's position.
func TestApplyIsIdempotent(t *testing.T) {
	ledger := newTestLedger()
	ctx := context.Background()

	movement := buy("e1", "user-9", "fund-axi-blue", 10, 125000)
	mustApply(t, ledger, movement)

	applied, err := ledger.Apply(ctx, movement)
	if err != nil {
		t.Fatalf("Apply() on a redelivery error = %v; a duplicate is a success, not a failure", err)
	}
	if applied {
		t.Error("Apply() reported a second change for the same event id")
	}

	holdings, _ := ledger.ListByUser(ctx, "user-9")
	if holdings[0].Units != 10 || holdings[0].InvestedAmount != 125000 {
		t.Errorf("position = %v / %d after a redelivery, want 10 / 125000",
			holdings[0].Units, holdings[0].InvestedAmount)
	}
}

// TestFailedApplyIsNotMarkedApplied covers the DLQ replay path. An event that
// was rejected must stay eligible, so re-driving it from the dead-letter topic
// once the underlying problem is fixed actually takes effect.
func TestFailedApplyIsNotMarkedApplied(t *testing.T) {
	ledger := newTestLedger()
	ctx := context.Background()

	// Arrives before the purchase that would make it valid — the out-of-order
	// case a partition rebalance can genuinely produce.
	if _, err := ledger.Apply(ctx, sell("e1", "user-9", "fund-axi-blue", 5, 60000)); err == nil {
		t.Fatal("Apply() accepted a sale against an empty position")
	}

	mustApply(t, ledger, buy("e2", "user-9", "fund-axi-blue", 10, 100000))

	applied, err := ledger.Apply(ctx, sell("e1", "user-9", "fund-axi-blue", 5, 60000))
	if err != nil {
		t.Fatalf("replayed sale error = %v", err)
	}
	if !applied {
		t.Fatal("the replayed event was treated as a duplicate; a rejected event must not be recorded as applied")
	}
}

func TestSeedingIsOptional(t *testing.T) {
	seeded, _ := NewHoldingsRepository(true).ListByUser(context.Background(), "user-1")
	if len(seeded) == 0 {
		t.Error("NewHoldingsRepository(true) produced no demo positions")
	}

	empty, _ := NewHoldingsRepository(false).ListByUser(context.Background(), "user-1")
	if len(empty) != 0 {
		t.Errorf("NewHoldingsRepository(false) produced %d positions, want 0", len(empty))
	}
}

// TestUnknownUserReturnsEmptyNotError is requirement 3 at the layer that
// decides it. Everything above this — Valuate, the gRPC handler, the gateway's
// JSON — carries the empty slice through untouched, so this is the only place
// the behaviour has to be asserted.
func TestUnknownUserReturnsEmptyNotError(t *testing.T) {
	holdings, err := newTestLedger().ListByUser(context.Background(), "nobody")
	if err != nil {
		t.Fatalf("ListByUser() for an unknown user error = %v; an empty portfolio is not an error", err)
	}
	if holdings == nil {
		t.Fatal("ListByUser() returned nil; the contract is an empty slice so it serialises as []")
	}
	if len(holdings) != 0 {
		t.Errorf("len(holdings) = %d, want 0", len(holdings))
	}
}

// TestConcurrentReadsAndWrites is the reason the RWMutex exists. It proves
// nothing about correctness on its own — it exists to be run under `go test
// -race`, where an unsynchronised map access between the consumer goroutine and
// the HTTP readers is a hard failure rather than an intermittent one.
func TestConcurrentReadsAndWrites(t *testing.T) {
	ledger := newTestLedger()
	ctx := context.Background()

	const writers, readers, perGoroutine = 4, 8, 200

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				// Distinct event ids, so every one of these is a real mutation
				// rather than being short-circuited by the dedup set.
				id := strconv.Itoa(worker) + "-" + strconv.Itoa(i)
				_, _ = ledger.Apply(ctx, buy(id, "user-hot", "fund-axi-blue", 0.5, 5000))
			}
		}(w)
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				holdings, err := ledger.ListByUser(ctx, "user-hot")
				if err != nil {
					t.Errorf("ListByUser() during concurrent writes error = %v", err)
					return
				}
				// Mutating the returned copy must not be visible to anyone
				// else; ListByUser hands out a defensive copy precisely so a
				// reader cannot corrupt the ledger.
				for index := range holdings {
					holdings[index].Units = -1
				}
			}
		}()
	}
	wg.Wait()

	holdings, _ := ledger.ListByUser(ctx, "user-hot")
	if len(holdings) != 1 {
		t.Fatalf("len(holdings) = %d, want 1", len(holdings))
	}
	wantUnits := 0.5 * float64(writers*perGoroutine)
	if holdings[0].Units < wantUnits-1e-6 || holdings[0].Units > wantUnits+1e-6 {
		t.Errorf("Units = %v, want %v — a lost update means a mutation escaped the lock",
			holdings[0].Units, wantUnits)
	}
	if want := domain.Money(5000 * writers * perGoroutine); holdings[0].InvestedAmount != want {
		t.Errorf("InvestedAmount = %d, want %d", holdings[0].InvestedAmount, want)
	}
}

func TestAppliedEventSetIsBounded(t *testing.T) {
	ledger := newTestLedger()
	ctx := context.Background()

	for i := 0; i < maxAppliedEvents+100; i++ {
		_, _ = ledger.Apply(ctx, buy("event-"+strconv.Itoa(i), "user-9", "fund-axi-blue", 0.001, 10))
	}

	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	if len(ledger.appliedEvents) > maxAppliedEvents {
		t.Errorf("appliedEvents holds %d ids, want at most %d", len(ledger.appliedEvents), maxAppliedEvents)
	}
	if len(ledger.appliedOrder) != len(ledger.appliedEvents) {
		t.Errorf("appliedOrder (%d) and appliedEvents (%d) disagree; the eviction bookkeeping has drifted",
			len(ledger.appliedOrder), len(ledger.appliedEvents))
	}
}

func mustApply(t *testing.T, ledger *HoldingsRepository, movement Movement) {
	t.Helper()
	applied, err := ledger.Apply(context.Background(), movement)
	if err != nil {
		t.Fatalf("Apply(%s) error = %v", movement.EventID, err)
	}
	if !applied {
		t.Fatalf("Apply(%s) reported no change", movement.EventID)
	}
}

func isPermanent(err error) bool {
	return errors.Is(err, domain.ErrPermanent)
}
