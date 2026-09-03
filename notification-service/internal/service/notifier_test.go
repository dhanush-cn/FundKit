// Engineered by Dhanush C N (github.com/dhanush-cn)
package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/dhanush-cn/fundkit/notification-service/internal/domain"
)

// spyChannel records what it was asked to deliver, and can be told to fail.
type spyChannel struct {
	mu       sync.Mutex
	name     string
	sends    []domain.Recipient
	messages []Message
	err      error
}

func (s *spyChannel) Name() string { return s.name }

func (s *spyChannel) Send(_ context.Context, recipient domain.Recipient, message Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err != nil {
		return s.err
	}
	s.sends = append(s.sends, recipient)
	s.messages = append(s.messages, message)
	return nil
}

func (s *spyChannel) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sends)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func executedEvent() domain.OrderEvent {
	return domain.OrderEvent{
		EventID:   "evt-1",
		EventType: domain.EventOrderStatusChanged,
		Version:   1,
		Order: domain.Order{
			ID:        "order-1",
			UserID:    "user-1",
			UserName:  "Dhanush C N",
			UserEmail: "dhanush@example.com",
			UserPhone: "+919876543210",
			FundID:    "quant-small-cap-fund",
			Amount:    5000,
			Type:      "SIP",
			Status:    "EXECUTED",
		},
	}
}

func TestNotifierDeliversToEveryChannelOnATerminalEvent(t *testing.T) {
	t.Parallel()

	email := &spyChannel{name: "email"}
	sms := &spyChannel{name: "sms"}
	notifier := NewNotifier(discardLogger(), email, sms)

	if err := notifier.Handle(context.Background(), executedEvent()); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if email.count() != 1 || sms.count() != 1 {
		t.Fatalf("email=%d sms=%d, want one delivery each", email.count(), sms.count())
	}
	if email.sends[0].Email != "dhanush@example.com" {
		t.Fatalf("recipient email = %q", email.sends[0].Email)
	}
	if email.sends[0].Phone != "+919876543210" {
		t.Fatalf("recipient phone = %q", email.sends[0].Phone)
	}
	// The salutation must use the person's name, not an internal identifier.
	if body := email.messages[0].Body; body == "" || !strings.Contains(body, "Dhanush C N") {
		t.Fatalf("body does not address the customer by name: %q", body)
	}
	if subject := email.messages[0].Subject; !strings.Contains(subject, "order-1") || !strings.Contains(subject, "EXECUTED") {
		t.Fatalf("subject = %q", subject)
	}
}

// Intermediate states are internal plumbing. Waking a customer for PROCESSING
// would be noise, and noise is what makes people mute alerts entirely.
func TestNotifierIgnoresNonTerminalStates(t *testing.T) {
	t.Parallel()

	for _, status := range []string{"PENDING", "PROCESSING"} {
		email := &spyChannel{name: "email"}
		notifier := NewNotifier(discardLogger(), email)

		event := executedEvent()
		event.Order.Status = status

		if err := notifier.Handle(context.Background(), event); err != nil {
			t.Fatalf("handle %s: %v", status, err)
		}
		if email.count() != 0 {
			t.Fatalf("status %s produced %d notifications, want 0", status, email.count())
		}
	}
}

func TestNotifierIgnoresUnrelatedEvents(t *testing.T) {
	t.Parallel()

	email := &spyChannel{name: "email"}
	notifier := NewNotifier(discardLogger(), email)

	event := executedEvent()
	event.EventType = "portfolio.rebalanced"

	if err := notifier.Handle(context.Background(), event); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if email.count() != 0 {
		t.Fatal("an unrelated event type must not produce a notification")
	}
}

// Kafka delivers at least once, so the same event id can arrive twice after a
// rebalance. The customer should still be told exactly once.
func TestNotifierSuppressesDuplicateEventIDs(t *testing.T) {
	t.Parallel()

	email := &spyChannel{name: "email"}
	notifier := NewNotifier(discardLogger(), email)

	for attempt := 0; attempt < 3; attempt++ {
		if err := notifier.Handle(context.Background(), executedEvent()); err != nil {
			t.Fatalf("handle attempt %d: %v", attempt, err)
		}
	}

	if email.count() != 1 {
		t.Fatalf("sent %d notifications for one event, want 1", email.count())
	}
}

func TestNotifierSkipsAChannelWithNoAddress(t *testing.T) {
	t.Parallel()

	logger := discardLogger()
	notifier := NewNotifier(logger, NewEmailChannel(logger), NewSMSChannel(logger))

	event := executedEvent()
	event.Order.UserPhone = ""

	// A customer with no phone on file is not a delivery failure: the email
	// still goes out and the Kafka offset still commits.
	if err := notifier.Handle(context.Background(), event); err != nil {
		t.Fatalf("handle: %v", err)
	}
}

// A real provider failure is different: the offset must stay uncommitted so the
// event is redelivered.
func TestNotifierPropagatesProviderFailures(t *testing.T) {
	t.Parallel()

	failing := &spyChannel{name: "email", err: errors.New("smtp timeout")}
	notifier := NewNotifier(discardLogger(), failing)

	if err := notifier.Handle(context.Background(), executedEvent()); err == nil {
		t.Fatal("expected a provider failure to surface so the event is retried")
	}
}

func TestNotifierIgnoresAnEventWithNoOrder(t *testing.T) {
	t.Parallel()

	email := &spyChannel{name: "email"}
	notifier := NewNotifier(discardLogger(), email)

	event := executedEvent()
	event.Order.ID = ""

	if err := notifier.Handle(context.Background(), event); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if email.count() != 0 {
		t.Fatal("an event with no order payload must be ignored, not delivered")
	}
}

func TestChannelsRouteToTheirOwnAddressField(t *testing.T) {
	t.Parallel()

	logger := discardLogger()
	recipient := domain.Recipient{Name: "Dhanush C N", Email: "dhanush@example.com"}

	if err := NewEmailChannel(logger).Send(context.Background(), recipient, Message{Subject: "s", Body: "b"}); err != nil {
		t.Fatalf("email channel: %v", err)
	}
	if err := NewSMSChannel(logger).Send(context.Background(), recipient, Message{Subject: "s", Body: "b"}); !errors.Is(err, ErrNoAddress) {
		t.Fatalf("sms channel error = %v, want ErrNoAddress when no phone is on file", err)
	}
}
