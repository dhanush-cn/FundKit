// Engineered by Dhanush C N (github.com/dhanush-cn)
package repository

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"github.com/dhanush-cn/fundkit/order-service/internal/domain"
)

// OrderRepository is the only component in the service that knows SQL exists.
type OrderRepository struct {
	db *gorm.DB
}

func NewOrderRepository(db *gorm.DB) *OrderRepository {
	return &OrderRepository{db: db}
}

// CreateWithOutbox persists the order and its creation event in ONE
// transaction. This is the fix for the dual-write problem: the insert used to
// commit and the Kafka publish happened afterwards, so a crash in between lost
// the event with the order already durable. Now either both rows land or
// neither does, and the relay guarantees the event eventually reaches Kafka.
//
// build is invoked after the INSERT because BeforeCreate assigns the order's
// UUID during it, and the outbox row needs that final id as its aggregate_id.
//
// A unique-violation on the idempotency key is still translated into
// domain.ErrDuplicateOrder: the database is the last line of defence behind the
// Redis reservation, and both must agree on the outcome.
func (r *OrderRepository) CreateWithOutbox(ctx context.Context, order *domain.Order, build domain.OutboxBuilder) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(order).Error; err != nil {
			return err
		}

		msg, err := build(*order)
		if err != nil {
			return err
		}
		return tx.Create(&msg).Error
	})

	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrDuplicateOrder
		}
		return err
	}
	return nil
}

func (r *OrderRepository) GetByID(ctx context.Context, id string) (*domain.Order, error) {
	var order domain.Order
	if err := r.db.WithContext(ctx).First(&order, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrOrderNotFound
		}
		return nil, err
	}
	return &order, nil
}

func (r *OrderRepository) List(ctx context.Context, limit int) ([]domain.Order, error) {
	var orders []domain.Order
	query := r.db.WithContext(ctx).Order("created_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Find(&orders).Error; err != nil {
		return nil, err
	}
	return orders, nil
}

// UpdateStatusWithOutbox performs the compare-and-set transition and enqueues
// the resulting event atomically. The CAS guard is unchanged: guarding the
// UPDATE with the expected state makes the transition safe against the
// concurrent writes produced by the background lifecycle worker and the API.
//
// The reloaded row is returned because the caller needs the database's
// updated_at, and because the outbox payload must carry the post-transition
// aggregate rather than the caller's in-memory guess at it.
func (r *OrderRepository) UpdateStatusWithOutbox(
	ctx context.Context,
	id string,
	expected, next domain.OrderStatus,
	build domain.OutboxBuilder,
) (*domain.Order, error) {
	var updated domain.Order

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&domain.Order{}).
			Where("id = ? AND status = ?", id, expected).
			Update("status", next)

		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return domain.ErrInvalidTransition
		}

		if err := tx.First(&updated, "id = ?", id).Error; err != nil {
			return err
		}

		msg, err := build(updated)
		if err != nil {
			return err
		}
		return tx.Create(&msg).Error
	})

	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func (r *OrderRepository) Delete(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Delete(&domain.Order{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return domain.ErrOrderNotFound
	}
	return nil
}

func isUniqueViolation(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate key") ||
		strings.Contains(message, "unique constraint") ||
		strings.Contains(message, "sqlstate 23505")
}
