// Package domain mirrors the event contract published by order-service.
// Consumers own a copy of the schema deliberately: it is the shape they can
// tolerate, not whatever the producer happens to be sending today.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package domain

import "time"

const EventOrderStatusChanged = "order.status_changed"

// Order is the subset of the order aggregate this service actually reads.
//
// The contact details are carried on the event itself. That is what makes this
// service genuinely asynchronous: it can deliver an alert without a synchronous
// call back to the gateway to ask who the customer is, so identity being slow
// or down cannot stall the notification pipeline.
type Order struct {
	ID        string  `json:"id"`
	UserID    string  `json:"user_id"`
	UserName  string  `json:"user_name,omitempty"`
	UserEmail string  `json:"user_email,omitempty"`
	UserPhone string  `json:"user_phone,omitempty"`
	FundID    string  `json:"fund_id"`
	Amount    float64 `json:"amount"`
	Type      string  `json:"type"`
	Status    string  `json:"status"`
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
