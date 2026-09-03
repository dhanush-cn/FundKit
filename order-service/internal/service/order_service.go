// Package service holds the order use cases. It depends on interfaces declared
// here rather than on concrete adapters, which keeps the business rules
// testable with fakes and free of gorm, redis and kafka types.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/dhanush-cn/fundkit/order-service/internal/domain"
)

// Repository is the persistence port required by the order use cases.
type Repository interface {
	Create(ctx context.Context, order *domain.Order) error
	GetByID(ctx context.Context, id string) (*domain.Order, error)
	List(ctx context.Context, limit int) ([]domain.Order, error)
	UpdateStatus(ctx context.Context, id string, expected, next domain.OrderStatus) error
	Delete(ctx context.Context, id string) error
}

// IdempotencyStore is the reservation port backed by Redis in production.
type IdempotencyStore interface {
	ClaimIdempotency(ctx context.Context, key string) (bool, error)
	ConfirmIdempotency(ctx context.Context, key string) error
	ReleaseIdempotency(ctx context.Context, key string) error
}

// EventPublisher is the outbound event port backed by Kafka in production.
type EventPublisher interface {
	PublishOrderStatusChanged(ctx context.Context, order domain.Order) error
}

// OrderService orchestrates persistence, idempotency and event publication.
type OrderService struct {
	repo      Repository
	idem      IdempotencyStore
	events    EventPublisher
	logger    *slog.Logger
	workerTTL time.Duration

	// inFlight tracks the detached lifecycle goroutines so that shutdown can
	// drain them instead of killing an order mid-transition.
	inFlight sync.WaitGroup
}

func NewOrderService(repo Repository, idem IdempotencyStore, events EventPublisher, logger *slog.Logger) *OrderService {
	return &OrderService{
		repo:      repo,
		idem:      idem,
		events:    events,
		logger:    logger,
		workerTTL: 30 * time.Second,
	}
}

// Place reserves the idempotency key, persists the order, emits the creation
// event and hands the order to the asynchronous lifecycle worker.
//
// The reservation is taken *before* the insert: Redis is the cheap, fast guard
// and the unique index on the key is the authoritative one. If the insert
// fails, the reservation is released so a legitimate retry still succeeds.
func (s *OrderService) Place(ctx context.Context, input domain.NewOrder) (*domain.Order, error) {
	claimed, err := s.idem.ClaimIdempotency(ctx, input.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return nil, domain.ErrDuplicateOrder
	}

	order := &domain.Order{
		UserID:         input.UserID,
		UserName:       input.UserName,
		UserEmail:      input.UserEmail,
		UserPhone:      input.UserPhone,
		FundID:         input.FundID,
		Amount:         input.Amount,
		Type:           input.Type,
		Status:         domain.StatusPending,
		IdempotencyKey: input.IdempotencyKey,
	}

	if err := s.repo.Create(ctx, order); err != nil {
		if releaseErr := s.idem.ReleaseIdempotency(ctx, input.IdempotencyKey); releaseErr != nil {
			s.logger.ErrorContext(ctx, "failed to release idempotency reservation",
				slog.String("error", releaseErr.Error()))
		}
		return nil, err
	}

	s.publish(ctx, *order)
	s.startLifecycle(ctx, order.ID)

	s.logger.InfoContext(ctx, "order placed",
		slog.String("order_id", order.ID),
		slog.String("user_id", order.UserID),
		slog.Float64("amount", order.Amount),
	)
	return order, nil
}

func (s *OrderService) List(ctx context.Context, limit int) ([]domain.Order, error) {
	return s.repo.List(ctx, limit)
}

func (s *OrderService) Get(ctx context.Context, id string) (*domain.Order, error) {
	return s.repo.GetByID(ctx, id)
}

// UpdateStatus applies an operator-driven transition, validating it against the
// domain state machine before the conditional UPDATE reaches the database.
func (s *OrderService) UpdateStatus(ctx context.Context, id string, next domain.OrderStatus) (*domain.Order, error) {
	order, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	current := order.Status
	if err := order.Transition(next); err != nil {
		return nil, err
	}

	if err := s.repo.UpdateStatus(ctx, id, current, next); err != nil {
		return nil, err
	}

	s.publish(ctx, *order)
	s.logger.InfoContext(ctx, "order status updated",
		slog.String("order_id", id),
		slog.String("from", string(current)),
		slog.String("to", string(next)),
	)
	return order, nil
}

// Delete removes an order that has not yet started processing.
func (s *OrderService) Delete(ctx context.Context, id string) error {
	order, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if order.Status != domain.StatusPending {
		return domain.ErrOrderNotCancelable
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	if err := s.idem.ReleaseIdempotency(ctx, order.IdempotencyKey); err != nil {
		s.logger.WarnContext(ctx, "failed to release idempotency key on delete",
			slog.String("error", err.Error()))
	}

	s.logger.InfoContext(ctx, "order deleted", slog.String("order_id", id))
	return nil
}

// Drain blocks until every detached lifecycle worker has finished. Called from
// the shutdown path so in-flight orders reach a consistent state.
func (s *OrderService) Drain() {
	s.inFlight.Wait()
}

// startLifecycle detaches the worker from the HTTP request's cancellation while
// deliberately keeping its values, so the correlation id survives into the
// asynchronous half of the trace.
func (s *OrderService) startLifecycle(ctx context.Context, orderID string) {
	workerCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.workerTTL)

	s.inFlight.Add(1)
	go func() {
		defer s.inFlight.Done()
		defer cancel()
		s.processLifecycle(workerCtx, orderID)
	}()
}

func (s *OrderService) processLifecycle(ctx context.Context, orderID string) {
	order, err := s.repo.GetByID(ctx, orderID)
	if err != nil {
		s.logger.ErrorContext(ctx, "lifecycle worker could not load order",
			slog.String("order_id", orderID), slog.String("error", err.Error()))
		return
	}
	if order.Status != domain.StatusPending {
		return
	}

	if err := s.repo.UpdateStatus(ctx, orderID, domain.StatusPending, domain.StatusProcessing); err != nil {
		// A losing race here means an operator moved the order first, which is
		// a legitimate outcome rather than an error to shout about.
		if errors.Is(err, domain.ErrInvalidTransition) {
			s.logger.InfoContext(ctx, "lifecycle worker yielded to concurrent transition",
				slog.String("order_id", orderID))
			return
		}
		s.logger.ErrorContext(ctx, "failed to mark order processing",
			slog.String("order_id", orderID), slog.String("error", err.Error()))
		return
	}
	order.Status = domain.StatusProcessing
	s.publish(ctx, *order)

	final := domain.StatusExecuted
	if order.Amount <= 0 {
		final = domain.StatusFailed
	}

	if err := s.repo.UpdateStatus(ctx, orderID, domain.StatusProcessing, final); err != nil {
		s.logger.ErrorContext(ctx, "failed to finalize order",
			slog.String("order_id", orderID), slog.String("error", err.Error()))
		return
	}
	order.Status = final
	s.publish(ctx, *order)

	if err := s.idem.ConfirmIdempotency(ctx, order.IdempotencyKey); err != nil {
		s.logger.WarnContext(ctx, "failed to confirm idempotency key",
			slog.String("order_id", orderID), slog.String("error", err.Error()))
	}
}

// publish never fails the caller: losing an event must not roll back a
// committed order. The failure is logged loudly for the outbox/retry work that
// a production deployment would layer on top.
func (s *OrderService) publish(ctx context.Context, order domain.Order) {
	if err := s.events.PublishOrderStatusChanged(ctx, order); err != nil {
		s.logger.ErrorContext(ctx, "failed to publish order event",
			slog.String("order_id", order.ID),
			slog.String("status", string(order.Status)),
			slog.String("error", err.Error()),
		)
	}
}
