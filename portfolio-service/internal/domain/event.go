// Package domain — the event contract portfolio-service reads.
//
// This is a copy of the envelope order-service publishes, not an import of it.
// That is the same choice notification-service already documents: a consumer
// owns the shape it can tolerate, so a producer adding a field cannot break a
// service that never reads it, and a producer changing the meaning of a field
// is caught by the version check rather than absorbed silently.
//
// What this service takes from the stream is narrower than what
// notification-service takes. A notification cares about every terminal
// transition; a ledger cares about exactly one — an order that EXECUTED, which
// is the only status that moves units. Everything else on the topic is skipped
// as a successful no-op, because "not for me" is not a failure and must not
// look like one on a dashboard.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package domain

import (
	"errors"
	"fmt"
	"time"
)

// EventOrderStatusChanged is the only event type this service acts on.
const EventOrderStatusChanged = "order.status_changed"

// SupportedOrderEventVersion is the single envelope version this build reads.
//
// The strictness is inherited from the producer's own reasoning, and it matters
// more here than it does for a notification. v1 carried the order amount as a
// decimal rupee float and v2 carries integer paise; the two are
// indistinguishable by inspection, because 15000 is a plausible value under
// either reading. A notification service that guessed wrong sends a customer a
// wrong number in an email. A ledger that guesses wrong writes a position 100x
// too small and then values it, sums it and shows it as fact. So an unknown
// version is a permanent failure that goes to the DLQ for a human, never a
// best-effort decode.
const SupportedOrderEventVersion = 2

// ErrPermanent marks a failure no amount of retrying can fix: a malformed
// payload, an unreadable version, an event that contradicts the ledger. The
// consumer parks these immediately instead of stalling the partition behind a
// message that will fail identically three times in a row.
var ErrPermanent = errors.New("permanent event failure")

// Side is the direction a movement pushes a position.
type Side string

const (
	SideBuy  Side = "BUY"
	SideSell Side = "SELL"
)

// Order is the subset of the producer's aggregate this service reads.
//
// Note what is absent: the customer's name, email and phone are on the wire and
// are deliberately not decoded here. A ledger has no business holding contact
// details, and a struct that does not have the field cannot leak it into a log
// line.
type Order struct {
	ID     string `json:"id"`
	UserID string `json:"user_id"`
	FundID string `json:"fund_id"`
	// Amount is integer paise, matching the v2 envelope. Decoding it into Money
	// rather than float64 means a v1 payload fails to unmarshal outright
	// instead of being read as an amount a hundred times too small.
	Amount Money  `json:"amount"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

// OrderEvent is the versioned envelope carried on the order_events topic.
type OrderEvent struct {
	EventID    string    `json:"event_id"`
	EventType  string    `json:"event_type"`
	Version    int       `json:"version"`
	OccurredAt time.Time `json:"occurred_at"`
	RequestID  string    `json:"request_id,omitempty"`
	Order      Order     `json:"order"`
}

// IsExecuted reports whether this event represents a fill.
//
// EXECUTED is the only status that moves units. PENDING and PROCESSING are
// promises, and FAILED is the absence of one; applying any of them to a ledger
// would show a customer units they do not own.
func (e OrderEvent) IsExecuted() bool {
	return e.EventType == EventOrderStatusChanged && e.Order.Status == "EXECUTED"
}

// Side maps the producer's order type onto a ledger direction.
//
// order-service currently emits only SIP and LUMPSUM, both of which are
// purchases, so SELL is unreachable from today's producer. It is implemented
// and tested anyway, and the mapping is written as an explicit table rather
// than "anything that is not a redemption is a buy". The difference shows up
// the day someone adds a SWITCH: an unknown type becomes a permanent failure
// that lands on the DLQ where a human sees it, instead of being silently
// treated as a purchase and credited to the customer.
func (o Order) Side() (Side, error) {
	switch o.Type {
	case "SIP", "LUMPSUM", "BUY":
		return SideBuy, nil
	case "REDEEM", "SELL", "SWITCH_OUT":
		return SideSell, nil
	default:
		return "", fmt.Errorf("%w: unknown order type %q", ErrPermanent, o.Type)
	}
}

// Validate checks the envelope against the contract this build implements.
//
// It runs before anything touches the ledger, so an event that cannot be
// trusted never becomes a position. Every failure wraps ErrPermanent: none of
// these conditions improve on a second attempt.
func (e OrderEvent) Validate() error {
	if e.Version != SupportedOrderEventVersion {
		return fmt.Errorf("%w: order event version %d, this build reads v%d",
			ErrPermanent, e.Version, SupportedOrderEventVersion)
	}
	if e.EventID == "" {
		// The event id is the deduplication key. Without one, an at-least-once
		// redelivery cannot be recognised, and a replayed BUY would be applied
		// twice. An event that cannot be deduplicated is worse than one that is
		// parked, so it is parked.
		return fmt.Errorf("%w: event carries no event_id to deduplicate on", ErrPermanent)
	}
	if e.Order.ID == "" {
		return fmt.Errorf("%w: event carries no order id", ErrPermanent)
	}
	if e.Order.UserID == "" {
		return fmt.Errorf("%w: order %s carries no user_id", ErrPermanent, e.Order.ID)
	}
	if e.Order.FundID == "" {
		return fmt.Errorf("%w: order %s carries no fund_id", ErrPermanent, e.Order.ID)
	}
	if !e.Order.Amount.IsPositive() {
		return fmt.Errorf("%w: order %s has non-positive amount %d paise",
			ErrPermanent, e.Order.ID, e.Order.Amount.Paise())
	}
	return nil
}
