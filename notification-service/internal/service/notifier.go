// Package service turns order events into customer notifications.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/dhanush-cn/fundkit/notification-service/internal/domain"
)

// Channel is the delivery port. Email and SMS are simulated here, but the
// interface is what a real provider adapter would satisfy.
//
// The channel receives the whole recipient rather than a pre-resolved address,
// because only the channel knows which field it can actually deliver to.
type Channel interface {
	Name() string
	Send(ctx context.Context, recipient domain.Recipient, message Message) error
}

// Notifier fans one event out to every configured channel.
type Notifier struct {
	channels []Channel
	logger   *slog.Logger

	// Kafka delivers at least once, so the same event id can arrive twice after
	// a rebalance or a redelivery. A bounded seen-set makes handling idempotent
	// without a round trip to an external store.
	mu      sync.Mutex
	seen    map[string]struct{}
	seenCap int
}

func NewNotifier(logger *slog.Logger, channels ...Channel) *Notifier {
	return &Notifier{
		channels: channels,
		logger:   logger,
		seen:     make(map[string]struct{}),
		seenCap:  10000,
	}
}

// Handle implements consumer.Handler.
func (n *Notifier) Handle(ctx context.Context, event domain.OrderEvent) error {
	if event.EventType != "" && event.EventType != domain.EventOrderStatusChanged {
		n.logger.DebugContext(ctx, "ignoring unrelated event", slog.String("event_type", event.EventType))
		return nil
	}
	if event.Order.ID == "" {
		n.logger.WarnContext(ctx, "ignoring event with no order payload")
		return nil
	}
	if n.alreadyHandled(event.EventID) {
		n.logger.DebugContext(ctx, "duplicate event suppressed", slog.String("event_id", event.EventID))
		return nil
	}

	// Intermediate states are internal plumbing; customers are told about
	// outcomes, not about the machine walking its own state graph.
	if !event.Order.IsTerminal() {
		n.logger.InfoContext(ctx, "order progressed",
			slog.String("order_id", event.Order.ID),
			slog.String("status", event.Order.Status),
		)
		return nil
	}

	recipient := event.Order.Recipient()
	message := Message{
		Subject: fmt.Sprintf("FundKit order %s is %s", event.Order.ID, event.Order.Status),
		Body: fmt.Sprintf(
			"Dear %s,\nYour %s order for %s (amount %.2f) is now %s.\nThank you for using FundKit.",
			recipient.Name, event.Order.Type, event.Order.FundID, event.Order.Amount, event.Order.Status,
		),
	}

	delivered := 0
	for _, channel := range n.channels {
		err := channel.Send(ctx, recipient, message)
		switch {
		case err == nil:
			delivered++
		case errors.Is(err, ErrNoAddress):
			// A customer with no phone number is not an error worth blocking the
			// partition over. Log it and let the other channels carry the alert.
			n.logger.WarnContext(ctx, "channel skipped: no address on file",
				slog.String("channel", channel.Name()),
				slog.String("order_id", event.Order.ID),
				slog.String("user_id", event.Order.UserID),
			)
		default:
			// A real provider failure *is* worth retrying, so the error is
			// returned and the Kafka offset stays uncommitted.
			return fmt.Errorf("channel %s: %w", channel.Name(), err)
		}
	}

	if delivered == 0 {
		n.logger.ErrorContext(ctx, "order reached a terminal state but no channel could reach the customer",
			slog.String("order_id", event.Order.ID),
			slog.String("user_id", event.Order.UserID),
		)
	}

	n.logger.InfoContext(ctx, "notifications dispatched",
		slog.String("order_id", event.Order.ID),
		slog.String("status", event.Order.Status),
		slog.Int("channels", delivered),
	)
	return nil
}

func (n *Notifier) alreadyHandled(eventID string) bool {
	if eventID == "" {
		return false
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	if _, ok := n.seen[eventID]; ok {
		return true
	}
	if len(n.seen) >= n.seenCap {
		n.seen = make(map[string]struct{}, n.seenCap)
	}
	n.seen[eventID] = struct{}{}
	return false
}
