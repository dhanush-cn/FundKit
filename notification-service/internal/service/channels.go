// Engineered by Dhanush C N (github.com/dhanush-cn)
package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/dhanush-cn/fundkit/notification-service/internal/domain"
)

// ErrNoAddress reports that this recipient cannot be reached on this channel.
// It is a routing outcome, not a delivery failure: the notifier logs it and
// moves on rather than blocking the partition retrying forever.
var ErrNoAddress = errors.New("recipient has no address for this channel")

// Message is the rendered alert, independent of how it is carried.
type Message struct {
	Subject string
	Body    string
}

// LogChannel simulates a delivery provider by emitting a structured record.
// Swapping in SES or Twilio means implementing Channel, not touching Notifier.
type LogChannel struct {
	name    string
	address func(domain.Recipient) string
	logger  *slog.Logger
}

// NewEmailChannel routes to the customer's registered email address.
func NewEmailChannel(logger *slog.Logger) *LogChannel {
	return &LogChannel{
		name:    "email",
		address: func(r domain.Recipient) string { return r.Email },
		logger:  logger,
	}
}

// NewSMSChannel routes to the customer's registered phone number.
func NewSMSChannel(logger *slog.Logger) *LogChannel {
	return &LogChannel{
		name:    "sms",
		address: func(r domain.Recipient) string { return r.Phone },
		logger:  logger,
	}
}

func (c *LogChannel) Name() string { return c.name }

// Send resolves the address for this channel and "delivers" the message.
func (c *LogChannel) Send(ctx context.Context, recipient domain.Recipient, message Message) error {
	address := c.address(recipient)
	if address == "" {
		return ErrNoAddress
	}

	c.logger.InfoContext(ctx, "notification sent",
		slog.String("channel", c.name),
		slog.String("recipient", address),
		slog.String("recipient_name", recipient.Name),
		slog.String("subject", message.Subject),
		slog.String("body", message.Body),
	)
	return nil
}
