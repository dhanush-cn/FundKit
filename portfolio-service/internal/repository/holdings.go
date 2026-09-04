// Engineered by Dhanush C N (github.com/dhanush-cn)
package repository

import (
	"context"
	"fmt"
	"sync"

	"github.com/dhanush-cn/fundkit/portfolio-service/internal/domain"
)

// HoldingsRepository is an in-memory stand-in for the custodian ledger. It is
// deliberately behind an interface-shaped type so swapping it for Postgres is a
// one-file change that the service layer never sees.
//
// It is now written from two directions at once: HTTP and gRPC readers on the
// valuation path, and the Kafka consumer applying executed orders. That is what
// the RWMutex is for, and it is a genuine read-write lock rather than a plain
// Mutex because the read side is both far more frequent and concurrent with
// itself — every dashboard poll takes RLock, and they do not need to queue
// behind each other.
//
// The lock is held for the whole of a mutation, including the duplicate check.
// Splitting those into "has this been applied?" then "apply it" would leave a
// window in which two deliveries of the same event both pass the check, which
// is exactly the bug the check exists to prevent.
//
// What this type does NOT do is survive a restart. Everything below is process
// memory, so a pod that restarts rebuilds its holdings only from whatever the
// consumer group has yet to consume. That is acceptable for a demo ledger and
// is not acceptable for a real one; the fix is not a bigger map but a Postgres
// table where the position update and the processed-event insert land in one
// transaction. See the note on appliedEvents.
//
// Seeded amounts are paise: 150000 is ₹1,500.00.
type HoldingsRepository struct {
	mu     sync.RWMutex
	byUser map[string][]domain.Holding

	// appliedEvents is the deduplication set, and it is the reason this
	// repository can sit behind an at-least-once stream at all.
	//
	// The outbox relay in order-service is at-least-once by design, and a
	// consumer that crashes between applying a purchase and committing its
	// offset will be handed the same event again on restart. Delivering a
	// notification twice is an annoyance; applying a BUY twice doubles a
	// customer's position and every rupee figure derived from it. So the
	// ledger, not the transport, is where idempotency has to live.
	//
	// In memory this is a map guarded by the same lock as the positions, so
	// check-and-apply is atomic. In Postgres it would be a UNIQUE constraint on
	// the event id inside the same transaction as the position write, which is
	// the same property enforced by the database instead of by this comment.
	appliedEvents map[string]struct{}
	// appliedOrder is the insertion order of appliedEvents, used to evict the
	// oldest ids once the set reaches maxAppliedEvents. An unbounded dedup set
	// is a memory leak with a long fuse: it grows with total lifetime
	// throughput, not with the number of customers, so it looks fine for weeks.
	appliedOrder []string
}

// maxAppliedEvents bounds the deduplication set.
//
// Eviction is by age, which is safe for the failure mode that actually happens:
// a redelivery follows its original within seconds — a consumer restart, a
// rebalance, a relay retry — so the id is still in the set. It is not safe
// against a replay of the whole topic from the beginning days later, and it is
// not meant to be; that operation needs the ledger rebuilt from empty, not
// deduplicated against a window.
const maxAppliedEvents = 100_000

// fundNames is the catalog this ledger uses to name a newly opened position.
//
// The event stream carries a fund id and no display name, and that is the right
// split: a name is reference data that changes when an AMC rebrands, and
// copying it onto every event would freeze whatever string was current the day
// the order was placed. An unknown id falls back to the id itself rather than
// to an empty string, so a fund added upstream before it is added here shows up
// as "fund-new-xyz" on the dashboard — obviously unfinished, but never blank
// and never a reason to reject a customer's holding.
var fundNames = map[string]string{
	"fund-axi-blue":     "AXI Bluechip",
	"fund-icici-growth": "ICICI Growth",
	"fund-hdfc-top":     "HDFC Top 100",
}

// Movement is one applied fill: the ledger-facing projection of an executed
// order event.
//
// It carries units and amount already resolved, so the repository does no
// pricing of its own. Deciding what a fill is worth is the service layer's job
// (it owns the NAV cache); recording it is this one's.
type Movement struct {
	// EventID is the deduplication key, taken from the event envelope.
	EventID string
	UserID  string
	FundID  string
	Side    domain.Side
	// Units is the quantity moved, always positive regardless of side.
	Units float64
	// Amount is the consideration in paise, always positive regardless of side.
	Amount domain.Money
}

// NewHoldingsRepository builds the ledger.
//
// seed controls whether the demo positions below are loaded. It is a parameter
// rather than a build tag because the two deployments genuinely differ: the
// docker-compose stack wants a dashboard that shows something before anyone has
// placed an order, while a cluster wants holdings that came only from the event
// stream. Seeding both would be actively wrong — the seeds are not derived from
// any event, so a consumer replaying the topic from the beginning would add
// real positions on top of invented ones and report the sum as fact.
func NewHoldingsRepository(seed bool) *HoldingsRepository {
	r := &HoldingsRepository{
		byUser:        make(map[string][]domain.Holding),
		appliedEvents: make(map[string]struct{}),
	}

	if seed {
		r.byUser["user-1"] = []domain.Holding{
			{FundID: "fund-axi-blue", FundName: "AXI Bluechip", Units: 12.5, InvestedAmount: 150000},
			{FundID: "fund-icici-growth", FundName: "ICICI Growth", Units: 8.2, InvestedAmount: 120000},
		}
		r.byUser["user-2"] = []domain.Holding{
			{FundID: "fund-hdfc-top", FundName: "HDFC Top 100", Units: 20.0, InvestedAmount: 320000},
		}
	}

	return r
}

// ListByUser returns a defensive copy so callers cannot mutate stored state.
//
// A user with no positions returns an empty, non-nil slice and no error. That
// is the contract the whole "₹0.00 renders cleanly" path rests on: an unknown
// user is not a 404 and not an error, it is a customer who has not bought
// anything yet, and every layer above this one — Valuate, the gRPC handler, the
// gateway's JSON — carries that empty slice through to `"holdings": []` without
// a single special case.
func (r *HoldingsRepository) ListByUser(_ context.Context, userID string) ([]domain.Holding, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stored := r.byUser[userID]
	holdings := make([]domain.Holding, len(stored))
	copy(holdings, stored)
	return holdings, nil
}

// Apply records one executed order against a user's positions.
//
// It reports whether the movement was actually applied: false means the event
// had already been seen and was skipped, which is a success, not a failure. The
// caller distinguishes the two so a redelivery storm is visible as duplicates
// rather than as inflated throughput.
//
// Errors from here wrap domain.ErrPermanent, because everything that can go
// wrong at this layer is a contradiction between the event and the ledger —
// selling a fund the user does not hold, selling more units than exist. None of
// those improve on a retry, and the consumer parks them.
func (r *HoldingsRepository) Apply(_ context.Context, movement Movement) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, seen := r.appliedEvents[movement.EventID]; seen {
		return false, nil
	}

	var err error
	switch movement.Side {
	case domain.SideBuy:
		err = r.applyBuy(movement)
	case domain.SideSell:
		err = r.applySell(movement)
	default:
		err = fmt.Errorf("%w: unknown movement side %q", domain.ErrPermanent, movement.Side)
	}
	if err != nil {
		// The event is deliberately NOT marked applied on failure. It is about
		// to be dead-lettered, and if it is ever replayed from the DLQ after
		// the underlying problem is fixed, it must be allowed to take effect.
		return false, err
	}

	r.markApplied(movement.EventID)
	return true, nil
}

// applyBuy adds to an existing position or opens a new one. The caller holds
// the write lock.
func (r *HoldingsRepository) applyBuy(movement Movement) error {
	holdings := r.byUser[movement.UserID]

	if index := indexOfFund(holdings, movement.FundID); index >= 0 {
		// Indexing rather than ranging by value: `for _, h := range` hands out
		// a copy, and mutating that copy would silently do nothing.
		holdings[index].ApplyBuy(movement.Units, movement.Amount)
		r.byUser[movement.UserID] = holdings
		return nil
	}

	r.byUser[movement.UserID] = append(holdings, domain.Holding{
		FundID:         movement.FundID,
		FundName:       fundName(movement.FundID),
		Units:          movement.Units,
		InvestedAmount: movement.Amount,
	})
	return nil
}

// applySell reduces a position, removing it entirely once it is fully exited.
// The caller holds the write lock.
func (r *HoldingsRepository) applySell(movement Movement) error {
	holdings := r.byUser[movement.UserID]

	index := indexOfFund(holdings, movement.FundID)
	if index < 0 {
		return fmt.Errorf("%w: user %s holds no position in %s to sell",
			domain.ErrPermanent, movement.UserID, movement.FundID)
	}

	if _, err := holdings[index].ApplySell(movement.Units); err != nil {
		return err
	}

	if holdings[index].IsClosed() {
		// A closed position is removed rather than left at zero. Keeping it
		// would show the customer a ₹0.00 row for a fund they no longer own,
		// and — worse — a fund they have fully exited would keep costing a NAV
		// lookup on every valuation for the life of the process.
		holdings = append(holdings[:index], holdings[index+1:]...)
	}

	if len(holdings) == 0 {
		// The map entry goes too, so a user who has exited everything is
		// indistinguishable from one who never bought: both return the empty
		// slice from ListByUser and both render as a clean ₹0.00.
		delete(r.byUser, movement.UserID)
		return nil
	}

	r.byUser[movement.UserID] = holdings
	return nil
}

// markApplied records an event id and evicts the oldest once the set is full.
// The caller holds the write lock.
func (r *HoldingsRepository) markApplied(eventID string) {
	r.appliedEvents[eventID] = struct{}{}
	r.appliedOrder = append(r.appliedOrder, eventID)

	if len(r.appliedOrder) <= maxAppliedEvents {
		return
	}

	evict := len(r.appliedOrder) - maxAppliedEvents
	for _, id := range r.appliedOrder[:evict] {
		delete(r.appliedEvents, id)
	}
	// Re-slicing alone would keep the evicted strings reachable through the
	// backing array, so the copy is what actually releases them.
	r.appliedOrder = append([]string(nil), r.appliedOrder[evict:]...)
}

func indexOfFund(holdings []domain.Holding, fundID string) int {
	for index := range holdings {
		if holdings[index].FundID == fundID {
			return index
		}
	}
	return -1
}

func fundName(fundID string) string {
	if name, ok := fundNames[fundID]; ok {
		return name
	}
	return fundID
}
