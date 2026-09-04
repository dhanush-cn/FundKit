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
	"github.com/dhanush-cn/fundkit/order-service/internal/platform/trace"
)

// Repository is the persistence port required by the order use cases.
//
// Both mutating methods take an OutboxBuilder: writing the event is not an
// optional follow-up step a caller might forget, it is part of the method's
// contract and of the same transaction.
type Repository interface {
	CreateWithOutbox(ctx context.Context, order *domain.Order, build domain.OutboxBuilder) error
	GetByID(ctx context.Context, id string) (*domain.Order, error)
	List(ctx context.Context, limit int) ([]domain.Order, error)
	UpdateStatusWithOutbox(ctx context.Context, id string, expected, next domain.OrderStatus, build domain.OutboxBuilder) (*domain.Order, error)
	Delete(ctx context.Context, id string) error
}

// IdempotencyStore is the reservation port backed by Redis in production.
type IdempotencyStore interface {
	ClaimIdempotency(ctx context.Context, key string) (bool, error)
	ConfirmIdempotency(ctx context.Context, key string) error
	ReleaseIdempotency(ctx context.Context, key string) error
}

// There is deliberately no EventPublisher port here any more. Publication is
// the relay's job, and removing the port removes the possibility: after this
// change there is no code path from an HTTP request to the broker.

// OrderService orchestrates persistence, idempotency and event recording.
type OrderService struct {
	repo      Repository
	idem      IdempotencyStore
	logger    *slog.Logger
	workerTTL time.Duration

	// inFlight tracks the detached lifecycle goroutines so that shutdown can
	// drain them instead of killing an order mid-transition.
	inFlight sync.WaitGroup
}

func NewOrderService(repo Repository, idem IdempotencyStore, logger *slog.Logger) *OrderService {
	return &OrderService{
		repo:      repo,
		idem:      idem,
		logger:    logger,
		workerTTL: 30 * time.Second,
	}
}

// outboxFor captures the correlation id from the request context so the event
// written inside the transaction carries the same x-request-id as the HTTP call
// that caused it. This is what keeps a trace intact across the asynchronous
// seam: the id survives in a database column, not in a goroutine.
func (s *OrderService) outboxFor(ctx context.Context) domain.OutboxBuilder {
	requestID := trace.FromContext(ctx)
	return func(order domain.Order) (domain.OutboxMessage, error) {
		return domain.NewOrderStatusChangedOutbox(order, requestID)
	}
}

// Place reserves the idempotency key, persists the order together with its
// creation event, and hands the order to the asynchronous lifecycle worker.
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

	// The order row and its outbox row commit together. If this returns an
	// error, neither exists — there is no window in which the order is durable
	// but its event is not.
	if err := s.repo.CreateWithOutbox(ctx, order, s.outboxFor(ctx)); err != nil {
		if releaseErr := s.idem.ReleaseIdempotency(ctx, input.IdempotencyKey); releaseErr != nil {
			s.logger.ErrorContext(ctx, "failed to release idempotency reservation",
				slog.String("error", releaseErr.Error()))
		}
		return nil, err
	}

	// No publish call here any more: the event is already durable and the relay
	// will have it on Kafka within one poll interval.
	s.startLifecycle(ctx, order.ID)

	// The amount is logged twice on purpose: amount_paise is the exact value a
	// query or an alert threshold should match on, and amount is the rendered
	// form a human reading the log actually wants to see.
	s.logger.InfoContext(ctx, "order placed",
		slog.String("order_id", order.ID),
		slog.String("user_id", order.UserID),
		slog.Int64("amount_paise", order.Amount.Paise()),
		slog.String("amount", order.DisplayAmount()),
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

	updated, err := s.repo.UpdateStatusWithOutbox(ctx, id, current, next, s.outboxFor(ctx))
	if err != nil {
		return nil, err
	}

	s.logger.InfoContext(ctx, "order status updated",
		slog.String("order_id", id),
		slog.String("from", string(current)),
		slog.String("to", string(next)),
	)
	return updated, nil
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

	build := s.outboxFor(ctx)

	if _, err := s.repo.UpdateStatusWithOutbox(ctx, orderID, domain.StatusPending, domain.StatusProcessing, build); err != nil {
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

	final := domain.StatusExecuted
	if !order.Amount.IsPositive() {
		final = domain.StatusFailed
	}

	if _, err := s.repo.UpdateStatusWithOutbox(ctx, orderID, domain.StatusProcessing, final, build); err != nil {
		s.logger.ErrorContext(ctx, "failed to finalize order",
			slog.String("order_id", orderID), slog.String("error", err.Error()))
		return
	}

	if err := s.idem.ConfirmIdempotency(ctx, order.IdempotencyKey); err != nil {
		s.logger.WarnContext(ctx, "failed to confirm idempotency key",
			slog.String("order_id", orderID), slog.String("error", err.Error()))
	}
}
