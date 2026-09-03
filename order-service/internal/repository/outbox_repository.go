// Engineered by Dhanush C N (github.com/dhanush-cn)
package repository

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/dhanush-cn/fundkit/order-service/internal/domain"
)

// OutboxRepository is the relay's persistence adapter. It owns the transaction,
// the row locking and the status bookkeeping, so the relay worker itself never
// touches a *gorm.DB and stays testable without a database.
type OutboxRepository struct {
	db *gorm.DB
}

func NewOutboxRepository(db *gorm.DB) *OutboxRepository {
	return &OutboxRepository{db: db}
}

// Enqueue inserts an outbox record onto an *existing* transaction handle. This
// is the method that makes the pattern work: the caller is already inside the
// transaction that wrote the aggregate, so the event and the state change
// commit or roll back together. There is deliberately no variant that opens its
// own transaction — that would reintroduce the dual write.
func (r *OutboxRepository) Enqueue(ctx context.Context, tx *gorm.DB, msg *domain.OutboxMessage) error {
	if tx == nil {
		return fmt.Errorf("outbox enqueue requires an open transaction")
	}
	msg.Status = domain.OutboxPending
	if err := tx.WithContext(ctx).Create(msg).Error; err != nil {
		return fmt.Errorf("enqueue outbox message: %w", err)
	}
	return nil
}

// PublishBatch claims a batch of pending records, hands each to publish, and
// marks the successes PROCESSED — all inside one transaction.
//
// FOR UPDATE SKIP LOCKED is what makes the relay horizontally scalable: run
// three replicas of order-service and each claims a disjoint batch, instead of
// three of them fighting over the same rows and publishing triplicates.
//
// The transaction stays open across the Kafka round trip. That is a deliberate
// trade: it costs a held connection for the duration of the batch and buys
// "rollback == retry" semantics, with no intermediate CLAIMED state that a
// crash could strand. With a bounded batch size and a write deadline the hold
// is short; to decouple them you would add a CLAIMED status plus a reaper for
// records stuck in it.
func (r *OutboxRepository) PublishBatch(
	ctx context.Context,
	limit, maxAttempts int,
	publish func(context.Context, domain.OutboxMessage) error,
) (domain.OutboxBatchResult, error) {
	var result domain.OutboxBatchResult

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var batch []domain.OutboxMessage
		if err := tx.
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ?", domain.OutboxPending).
			Order("id ASC").
			Limit(limit).
			Find(&batch).Error; err != nil {
			return fmt.Errorf("claim outbox batch: %w", err)
		}

		result.Claimed = len(batch)
		if result.Claimed == 0 {
			return nil
		}

		// blocked holds aggregates whose head-of-line message failed. Skipping
		// the rest of that aggregate's events preserves per-order ordering,
		// while letting unrelated orders flow past a poison message instead of
		// queueing behind it.
		blocked := make(map[string]struct{})
		published := make([]int64, 0, len(batch))

		for _, msg := range batch {
			if _, stalled := blocked[msg.AggregateID]; stalled {
				continue
			}

			if err := publish(ctx, msg); err != nil {
				blocked[msg.AggregateID] = struct{}{}
				result.Failed++
				if result.Err == nil {
					result.Err = err
				}
				if markErr := r.recordFailure(ctx, tx, msg, maxAttempts, err); markErr != nil {
					return markErr
				}
				continue
			}
			published = append(published, msg.ID)
		}

		result.Published = len(published)
		if result.Published == 0 {
			return nil
		}

		now := time.Now().UTC()
		if err := tx.WithContext(ctx).
			Model(&domain.OutboxMessage{}).
			Where("id IN ?", published).
			Updates(map[string]any{
				"status":       domain.OutboxProcessed,
				"processed_at": now,
			}).Error; err != nil {
			return fmt.Errorf("mark outbox processed: %w", err)
		}
		return nil
	})

	return result, err
}

// recordFailure increments the attempt counter and retires a record that has
// exhausted its budget, so one permanently unpublishable message cannot stall
// its aggregate forever.
//
// Caveat worth knowing: a total broker outage burns one attempt on every
// pending record per poll. The relay's exponential backoff stretches that over
// minutes, and RequeueFailed recovers anything that is retired in error. A
// stricter implementation would separate retryable broker errors from
// permanent serialisation errors and only count the latter.
func (r *OutboxRepository) recordFailure(
	ctx context.Context,
	tx *gorm.DB,
	msg domain.OutboxMessage,
	maxAttempts int,
	cause error,
) error {
	attempts := msg.Attempts + 1
	updates := map[string]any{
		"attempts":   attempts,
		"last_error": truncateError(cause.Error(), 500),
	}
	if maxAttempts > 0 && attempts >= maxAttempts {
		updates["status"] = domain.OutboxFailed
	}

	if err := tx.WithContext(ctx).
		Model(&domain.OutboxMessage{}).
		Where("id = ?", msg.ID).
		Updates(updates).Error; err != nil {
		return fmt.Errorf("record outbox failure: %w", err)
	}
	return nil
}

// PendingOlderThan powers the backlog metric: pending records older than a few
// seconds mean the relay is behind or the broker is unhealthy. This is the one
// number worth alerting on.
func (r *OutboxRepository) PendingOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	var count int64
	cutoff := time.Now().UTC().Add(-age)
	err := r.db.WithContext(ctx).
		Model(&domain.OutboxMessage{}).
		Where("status = ? AND created_at < ?", domain.OutboxPending, cutoff).
		Count(&count).Error
	return count, err
}

// RequeueFailed returns retired records to the queue after an operator has
// fixed whatever was rejecting them.
func (r *OutboxRepository) RequeueFailed(ctx context.Context) (int64, error) {
	result := r.db.WithContext(ctx).
		Model(&domain.OutboxMessage{}).
		Where("status = ?", domain.OutboxFailed).
		Updates(map[string]any{
			"status":     domain.OutboxPending,
			"attempts":   0,
			"last_error": "",
		})
	return result.RowsAffected, result.Error
}

func truncateError(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
