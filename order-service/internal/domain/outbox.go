// Package domain — transactional outbox aggregate.
//
// The outbox record is a domain concept rather than a storage detail: it is the
// durable promise that an event *will* be published, made atomically with the
// state change that justified it. Before this existed, order-service committed
// to Postgres and then published to Kafka as a separate step; a crash in the
// gap lost the event silently, with the order already committed.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package domain

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// OutboxStatus is the relay's view of a record.
type OutboxStatus string

const (
	OutboxPending   OutboxStatus = "PENDING"
	OutboxProcessed OutboxStatus = "PROCESSED"
	OutboxFailed    OutboxStatus = "FAILED"
)

// Aggregate and event names shared with notification-service.
const (
	AggregateOrder          = "order"
	EventOrderStatusChanged = "order.status_changed"
	OrderEventVersion       = 1
)

// OutboxMessage is one committed, not-yet-published event.
type OutboxMessage struct {
	ID            int64        `gorm:"primaryKey;autoIncrement" json:"id"`
	AggregateType string       `gorm:"size:64;not null" json:"aggregate_type"`
	AggregateID   string       `gorm:"size:64;not null" json:"aggregate_id"`
	EventType     string       `gorm:"size:128;not null" json:"event_type"`
	Payload       []byte       `gorm:"type:jsonb;not null" json:"payload"`
	RequestID     string       `gorm:"size:64" json:"request_id,omitempty"`
	Status        OutboxStatus `gorm:"size:16;not null;default:PENDING" json:"status"`
	Attempts      int          `gorm:"not null;default:0" json:"attempts"`
	LastError     string       `gorm:"type:text" json:"last_error,omitempty"`
	CreatedAt     time.Time    `gorm:"not null;default:now()" json:"created_at"`
	ProcessedAt   *time.Time   `json:"processed_at,omitempty"`
}

// TableName pins the table name so GORM does not pluralise it to "outboxes".
func (OutboxMessage) TableName() string { return "outbox" }

// OrderEvent is the versioned envelope on the wire. It lives in domain rather
// than in messaging so the service layer can build one inside a database
// transaction without importing a Kafka package.
type OrderEvent struct {
	EventID    string    `json:"event_id"`
	EventType  string    `json:"event_type"`
	Version    int       `json:"version"`
	OccurredAt time.Time `json:"occurred_at"`
	RequestID  string    `json:"request_id,omitempty"`
	Order      Order     `json:"order"`
}

// OutboxBuilder defers event construction until the aggregate's identity is
// final. On an INSERT the order's UUID is assigned by BeforeCreate *during* the
// write, so the outbox row cannot be built before it — only after, and still
// inside the same transaction.
type OutboxBuilder func(Order) (OutboxMessage, error)

// NewOrderStatusChangedOutbox serialises the envelope exactly as it will appear
// on Kafka. Marshalling at commit time rather than at publish time means the
// bytes the relay sends are the bytes that were committed, so a later code
// change can never silently rewrite an event that is already durable.
func NewOrderStatusChangedOutbox(order Order, requestID string) (OutboxMessage, error) {
	event := OrderEvent{
		EventID:    uuid.New().String(),
		EventType:  EventOrderStatusChanged,
		Version:    OrderEventVersion,
		OccurredAt: time.Now().UTC(),
		RequestID:  requestID,
		Order:      order,
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return OutboxMessage{}, fmt.Errorf("marshal order event: %w", err)
	}

	return OutboxMessage{
		AggregateType: AggregateOrder,
		AggregateID:   order.ID,
		EventType:     event.EventType,
		Payload:       payload,
		RequestID:     requestID,
		Status:        OutboxPending,
	}, nil
}

// OutboxBatchResult reports what one relay pass achieved.
type OutboxBatchResult struct {
	Claimed   int
	Published int
	Failed    int
	Err       error // first publish error in the batch, for the relay's backoff
}
