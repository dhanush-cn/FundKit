// Package domain holds the order aggregate and its state machine. It is
// deliberately free of transport, database and broker concerns so the rules can
// be reasoned about (and unit tested) in isolation.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// OrderStatus enumerates the lifecycle of a mutual fund order.
type OrderStatus string

const (
	StatusPending    OrderStatus = "PENDING"
	StatusProcessing OrderStatus = "PROCESSING"
	StatusExecuted   OrderStatus = "EXECUTED"
	StatusFailed     OrderStatus = "FAILED"
)

// OrderType distinguishes a recurring SIP from a one-off lump sum purchase.
type OrderType string

const (
	TypeSIP     OrderType = "SIP"
	TypeLumpsum OrderType = "LUMPSUM"
)

// Sentinel errors let the handler layer map failures onto HTTP status codes
// without string matching.
var (
	ErrOrderNotFound      = errors.New("order not found")
	ErrDuplicateOrder     = errors.New("duplicate order request")
	ErrInvalidTransition  = errors.New("invalid order status transition")
	ErrOrderNotCancelable = errors.New("only pending orders can be deleted")
)

// Order is the persisted aggregate root.
//
// The customer's contact details are copied onto the order rather than looked
// up when a notification is sent. That is deliberate: an order confirmation
// should be addressed to whoever placed it, using the details that were on file
// at that moment, and notification-service should not need a synchronous call
// back into identity just to find an inbox.
type Order struct {
	ID             string      `gorm:"primaryKey" json:"id"`
	UserID         string      `gorm:"index" json:"user_id"`
	UserName       string      `gorm:"size:128" json:"user_name,omitempty"`
	UserEmail      string      `gorm:"size:255" json:"user_email,omitempty"`
	UserPhone      string      `gorm:"size:32" json:"user_phone,omitempty"`
	FundID         string      `json:"fund_id"`
	Amount         float64     `json:"amount"`
	Type           OrderType   `json:"type"`
	Status         OrderStatus `json:"status"`
	IdempotencyKey string      `gorm:"uniqueIndex" json:"idempotency_key"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

// NewOrder is the command accepted by the service layer when placing an order.
type NewOrder struct {
	UserID         string
	UserName       string
	UserEmail      string
	UserPhone      string
	FundID         string
	Amount         float64
	Type           OrderType
	IdempotencyKey string
}

// CanTransitionStatus encodes the only legal edges of the order state machine.
// Terminal states are absorbing, and a repeated write of the same terminal
// state is tolerated so that at-least-once event replay stays idempotent.
func CanTransitionStatus(current, next OrderStatus) bool {
	switch current {
	case StatusPending:
		return next == StatusProcessing || next == StatusFailed
	case StatusProcessing:
		return next == StatusExecuted || next == StatusFailed
	case StatusExecuted, StatusFailed:
		return next == current
	default:
		return false
	}
}

// Transition validates and applies a status change in memory.
func (o *Order) Transition(next OrderStatus) error {
	if !CanTransitionStatus(o.Status, next) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, o.Status, next)
	}
	o.Status = next
	return nil
}

// IsTerminal reports whether the order has reached an absorbing state.
func (o *Order) IsTerminal() bool {
	return o.Status == StatusExecuted || o.Status == StatusFailed
}

// BeforeCreate assigns a UUID primary key. Client-opaque identifiers keep the
// row count of the table from leaking through the API.
func (o *Order) BeforeCreate(*gorm.DB) error {
	if o.ID == "" {
		o.ID = uuid.New().String()
	}
	return nil
}
