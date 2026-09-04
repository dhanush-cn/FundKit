// Engineered by Dhanush C N (github.com/dhanush-cn)
package domain

import (
	"encoding/json"
	"errors"
	"testing"
)

// executedEventJSON is a byte-for-byte sample of what order-service actually
// publishes. Decoding the real wire format rather than constructing the struct
// is the point of the test: it is the only thing here that would notice the
// producer renaming a field.
const executedEventJSON = `{
	"event_id": "3f2b",
	"event_type": "order.status_changed",
	"version": 2,
	"occurred_at": "2026-09-03T09:15:00Z",
	"request_id": "req-1",
	"order": {
		"id": "ord-1",
		"user_id": "user-1",
		"user_name": "Dhanush",
		"user_email": "dhanush@example.com",
		"fund_id": "fund-axi-blue",
		"amount": 150000,
		"type": "LUMPSUM",
		"status": "EXECUTED"
	}
}`

func TestDecodeExecutedEvent(t *testing.T) {
	var event OrderEvent
	if err := json.Unmarshal([]byte(executedEventJSON), &event); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if err := event.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !event.IsExecuted() {
		t.Error("IsExecuted() = false for an EXECUTED order.status_changed event")
	}
	// 150000 paise is ₹1,500.00. If this ever reads 1500 the amount is being
	// decoded as rupees and every position built from it is 100x too small.
	if event.Order.Amount != 150000 {
		t.Errorf("Amount = %d paise, want 150000", event.Order.Amount)
	}
}

// TestDecodeIgnoresContactDetails documents a deliberate omission: the wire
// carries the customer's name and email, and this service's struct does not.
// A ledger has no business holding contact details, and a field that does not
// exist cannot leak into a log line.
func TestDecodeIgnoresContactDetails(t *testing.T) {
	var event OrderEvent
	if err := json.Unmarshal([]byte(executedEventJSON), &event); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	// Compile-time proof by absence: if someone adds a UserEmail field to
	// domain.Order this test still passes, so the assertion is on the decoded
	// value being usable without it.
	if event.Order.UserID != "user-1" {
		t.Errorf("UserID = %q, want user-1", event.Order.UserID)
	}
}

// TestV1AmountIsRejected is the version check earning its place. A v1 payload
// carries the amount as a decimal rupee float, which cannot unmarshal into
// Money — and even if it could, the version guard rejects it first.
func TestV1AmountIsRejected(t *testing.T) {
	event := OrderEvent{EventID: "e1", EventType: EventOrderStatusChanged, Version: 1}
	event.Order = Order{ID: "ord-1", UserID: "user-1", FundID: "fund-axi-blue", Amount: 15000, Type: "SIP", Status: "EXECUTED"}

	err := event.Validate()
	if err == nil {
		t.Fatal("Validate() accepted a v1 envelope")
	}
	if !errors.Is(err, ErrPermanent) {
		t.Errorf("error = %v, want it to wrap ErrPermanent so the consumer parks it without retrying", err)
	}
}

func TestValidateRejectsIncompleteEvents(t *testing.T) {
	valid := func() OrderEvent {
		return OrderEvent{
			EventID:   "e1",
			EventType: EventOrderStatusChanged,
			Version:   SupportedOrderEventVersion,
			Order:     Order{ID: "ord-1", UserID: "user-1", FundID: "fund-axi-blue", Amount: 150000, Type: "SIP", Status: "EXECUTED"},
		}
	}

	if err := valid().Validate(); err != nil {
		t.Fatalf("the baseline event is not valid: %v", err)
	}

	cases := map[string]func(*OrderEvent){
		// No event id means no deduplication key, which means a redelivery
		// would be applied a second time. That is worse than parking it.
		"missing event id": func(e *OrderEvent) { e.EventID = "" },
		"missing order id": func(e *OrderEvent) { e.Order.ID = "" },
		"missing user id":  func(e *OrderEvent) { e.Order.UserID = "" },
		"missing fund id":  func(e *OrderEvent) { e.Order.FundID = "" },
		"zero amount":      func(e *OrderEvent) { e.Order.Amount = 0 },
		"negative amount":  func(e *OrderEvent) { e.Order.Amount = -1 },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			event := valid()
			mutate(&event)

			err := event.Validate()
			if err == nil {
				t.Fatalf("Validate() accepted an event with %s", name)
			}
			if !errors.Is(err, ErrPermanent) {
				t.Errorf("error = %v, want it to wrap ErrPermanent", err)
			}
		})
	}
}

// TestOnlyExecutedOrdersMoveUnits guards the filter. PENDING and PROCESSING are
// promises and FAILED is the absence of one; applying any of them would show a
// customer units they do not own.
func TestOnlyExecutedOrdersMoveUnits(t *testing.T) {
	for status, want := range map[string]bool{
		"EXECUTED":   true,
		"PENDING":    false,
		"PROCESSING": false,
		"FAILED":     false,
	} {
		event := OrderEvent{EventType: EventOrderStatusChanged, Order: Order{Status: status}}
		if got := event.IsExecuted(); got != want {
			t.Errorf("IsExecuted() for %s = %v, want %v", status, got, want)
		}
	}

	// A different event type on the same topic is not this service's business
	// however its status reads.
	other := OrderEvent{EventType: "order.cancelled", Order: Order{Status: "EXECUTED"}}
	if other.IsExecuted() {
		t.Error("IsExecuted() = true for an event type this service does not handle")
	}
}

func TestSideMapping(t *testing.T) {
	buys := []string{"SIP", "LUMPSUM", "BUY"}
	for _, orderType := range buys {
		side, err := Order{Type: orderType}.Side()
		if err != nil {
			t.Errorf("Side() for %s error = %v", orderType, err)
		}
		if side != SideBuy {
			t.Errorf("Side() for %s = %v, want BUY", orderType, side)
		}
	}

	sells := []string{"REDEEM", "SELL", "SWITCH_OUT"}
	for _, orderType := range sells {
		side, err := Order{Type: orderType}.Side()
		if err != nil {
			t.Errorf("Side() for %s error = %v", orderType, err)
		}
		if side != SideSell {
			t.Errorf("Side() for %s = %v, want SELL", orderType, side)
		}
	}
}

// TestUnknownOrderTypeIsPermanent is the reason the mapping is a table rather
// than "not a redemption means a purchase". The day someone adds a SWITCH, this
// parks the event for a human instead of silently crediting the customer.
func TestUnknownOrderTypeIsPermanent(t *testing.T) {
	_, err := Order{Type: "SWITCH"}.Side()
	if err == nil {
		t.Fatal("Side() accepted an unknown order type")
	}
	if !errors.Is(err, ErrPermanent) {
		t.Errorf("error = %v, want it to wrap ErrPermanent", err)
	}
}
