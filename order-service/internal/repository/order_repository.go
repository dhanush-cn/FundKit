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

// Create persists a new order. A unique-violation on the idempotency key is
// translated into domain.ErrDuplicateOrder: the database is the last line of
// defence behind the Redis reservation, and both must agree on the outcome.
func (r *OrderRepository) Create(ctx context.Context, order *domain.Order) error {
	if err := r.db.WithContext(ctx).Create(order).Error; err != nil {
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

// UpdateStatus performs a compare-and-set on the current status. Guarding the
// UPDATE with the expected state makes the transition safe against the
// concurrent writes produced by the background lifecycle worker and the API.
func (r *OrderRepository) UpdateStatus(ctx context.Context, id string, expected, next domain.OrderStatus) error {
	result := r.db.WithContext(ctx).
		Model(&domain.Order{}).
		Where("id = ? AND status = ?", id, expected).
		Update("status", next)

	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return domain.ErrInvalidTransition
	}
	return nil
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
