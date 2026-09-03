// Engineered by Dhanush C N (github.com/dhanush-cn)
package domain

import (
	"encoding/json"
	"testing"
)

func TestRecipientPrefersTheRegisteredName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		order     Order
		wantName  string
		wantEmail string
		wantPhone string
	}{
		{
			name: "full contact details",
			order: Order{
				UserID:    "user-1",
				UserName:  "Dhanush C N",
				UserEmail: "dhanush@example.com",
				UserPhone: "+919876543210",
			},
			wantName:  "Dhanush C N",
			wantEmail: "dhanush@example.com",
			wantPhone: "+919876543210",
		},
		{
			name:     "falls back to the user id for the salutation",
			order:    Order{UserID: "user-1", UserEmail: "dhanush@example.com"},
			wantName: "user-1",
			// An event with no phone leaves the field empty rather than
			// inventing one; the SMS channel will skip.
			wantEmail: "dhanush@example.com",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			recipient := test.order.Recipient()
			if recipient.Name != test.wantName {
				t.Fatalf("name = %q, want %q", recipient.Name, test.wantName)
			}
			if recipient.Email != test.wantEmail {
				t.Fatalf("email = %q, want %q", recipient.Email, test.wantEmail)
			}
			if recipient.Phone != test.wantPhone {
				t.Fatalf("phone = %q, want %q", recipient.Phone, test.wantPhone)
			}
		})
	}
}

// This service owns its own copy of the schema, so the decode has to be pinned
// against the JSON order-service actually publishes.
func TestOrderEventDecodesTheProducerContract(t *testing.T) {
	t.Parallel()

	payload := `{
		"event_id": "evt-1",
		"event_type": "order.status_changed",
		"version": 1,
		"occurred_at": "2026-09-02T10:00:00Z",
		"request_id": "req-1",
		"order": {
			"id": "order-1",
			"user_id": "user-1",
			"user_name": "Dhanush C N",
			"user_email": "dhanush@example.com",
			"user_phone": "+919876543210",
			"fund_id": "quant-small-cap-fund",
			"amount": 5000,
			"type": "SIP",
			"status": "EXECUTED"
		}
	}`

	var event OrderEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if event.EventType != EventOrderStatusChanged || event.RequestID != "req-1" {
		t.Fatalf("envelope = %+v", event)
	}
	if event.Order.UserEmail != "dhanush@example.com" || event.Order.UserPhone != "+919876543210" {
		t.Fatalf("contact details did not decode: %+v", event.Order)
	}
	if !event.Order.IsTerminal() {
		t.Fatal("EXECUTED should be terminal")
	}
}

func TestIsTerminalOnlyForOutcomes(t *testing.T) {
	t.Parallel()

	for status, want := range map[string]bool{
		"PENDING":    false,
		"PROCESSING": false,
		"EXECUTED":   true,
		"FAILED":     true,
	} {
		if got := (Order{Status: status}).IsTerminal(); got != want {
			t.Fatalf("IsTerminal(%s) = %v, want %v", status, got, want)
		}
	}
}
