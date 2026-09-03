// Package domain mirrors the event contract published by order-service.
// Consumers own a copy of the schema deliberately: it is the shape they can
// tolerate, not whatever the producer happens to be sending today.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package domain

import (
	"errors"
	"fmt"
	"time"
)

const EventOrderStatusChanged = "order.status_changed"

// SupportedOrderEventVersion is the single envelope version this build can read.
//
// v1 carried the order amount as a decimal rupee float; v2 carries integer
// paise. Those two are indistinguishable by inspection — 15000 is a plausible
// value under either reading — so a consumer that guessed would quietly tell a
// customer their ₹15,000 order was for ₹150. The version is therefore checked
// strictly, and anything else is a permanent failure that goes to the
// dead-letter topic for a human to look at.
const SupportedOrderEventVersion = 2

// ErrPermanent marks a failure that retrying cannot fix: a malformed payload, a
// version this build does not understand, a field that violates the contract.
//
// The distinction is the whole basis of the retry policy. A transient failure —
// a provider timeout, a refused connection — deserves several attempts, because
// the next one may well succeed. A permanent failure deserves none, because
// every attempt will fail identically while holding up the partition behind it.
var ErrPermanent = errors.New("permanent event failure")

// Order is the subset of the order aggregate this service actually reads.
//
// The contact details are carried on the event itself. That is what makes this
// service genuinely asynchronous: it can deliver an alert without a synchronous
// call back to the gateway to ask who the customer is, so identity being slow
// or down cannot stall the notification pipeline.
type Order struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	UserName  string `json:"user_name,omitempty"`
	UserEmail string `json:"user_email,omitempty"`
	UserPhone string `json:"user_phone,omitempty"`
	FundID    string `json:"fund_id"`
	// Amount is integer paise, matching the producer's v2 envelope. Decoding it
	// into a Money rather than a float64 means a v1 payload — where this field
	// is a decimal — fails to unmarshal outright instead of being read as an
	// amount 100x too small.
	Amount Money  `json:"amount"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

// Recipient is who an alert is addressed to, independent of the channel that
// will carry it.
type Recipient struct {
	Name  string
	Email string
	Phone string
}

// Recipient derives the delivery target from the order.
//
// The display name falls back to the user id so a message never opens with an
// empty salutation. The email and phone are left empty when the event does not
// carry them: a channel with no address must skip, not invent one. Fabricating
// "<user-id>@example.com" — as an earlier revision of this service did — would
// mean every alert was silently delivered nowhere.
func (o Order) Recipient() Recipient {
	name := o.UserName
	if name == "" {
		name = o.UserID
	}
	return Recipient{Name: name, Email: o.UserEmail, Phone: o.UserPhone}
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

// IsTerminal reports whether this status ends the order lifecycle. Only
// terminal transitions are worth waking a customer up for.
func (o Order) IsTerminal() bool {
	return o.Status == "EXECUTED" || o.Status == "FAILED"
}

// Validate checks the envelope against the contract this build implements.
//
// It runs before any delivery work, so an event that cannot be trusted never
// reaches a channel. Every failure it returns wraps ErrPermanent: none of these
// conditions improve on a second attempt.
func (e OrderEvent) Validate() error {
	if e.Version != SupportedOrderEventVersion {
		return fmt.Errorf("%w: order event version %d, this build reads v%d",
			ErrPermanent, e.Version, SupportedOrderEventVersion)
	}
	if e.Order.ID == "" {
		return fmt.Errorf("%w: event carries no order id", ErrPermanent)
	}
	return nil
}
