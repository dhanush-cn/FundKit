// Package service implements portfolio valuation as a cache-aside read path.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/dhanush-cn/fundkit/portfolio-service/internal/domain"
	"github.com/dhanush-cn/fundkit/portfolio-service/internal/repository"
)

// HoldingsStore is the ledger port. It is read on the valuation path and
// written on the event path, and both halves are declared here so a Postgres
// implementation has one interface to satisfy.
type HoldingsStore interface {
	ListByUser(ctx context.Context, userID string) ([]domain.Holding, error)
	Apply(ctx context.Context, movement repository.Movement) (bool, error)
}

// NAVProvider is the upstream price feed port.
type NAVProvider interface {
	Fetch(ctx context.Context, fundID string) (float64, error)
}

// NAVCache is the Redis port. Both methods are best-effort: the service must
// stay correct, only slower, when the cache is unavailable.
type NAVCache interface {
	GetNAV(ctx context.Context, fundID string) (float64, bool, error)
	SetNAV(ctx context.Context, fundID string, price float64) error
}

type PortfolioService struct {
	holdings HoldingsStore
	nav      NAVProvider
	cache    NAVCache
	logger   *slog.Logger
}

func NewPortfolioService(holdings HoldingsStore, nav NAVProvider, cache NAVCache, logger *slog.Logger) *PortfolioService {
	return &PortfolioService{holdings: holdings, nav: nav, cache: cache, logger: logger}
}

// Valuate marks a user's whole portfolio to market.
//
// NAVs are resolved once per distinct fund per call, so a user holding the same
// fund twice does not pay for two lookups.
func (s *PortfolioService) Valuate(ctx context.Context, userID string) (domain.PortfolioValuation, error) {
	holdings, err := s.holdings.ListByUser(ctx, userID)
	if err != nil {
		return domain.PortfolioValuation{}, err
	}

	valuation := domain.PortfolioValuation{
		UserID:   userID,
		Holdings: make([]domain.HoldingValuation, 0, len(holdings)),
	}

	prices := make(map[string]float64, len(holdings))
	for _, holding := range holdings {
		price, seen := prices[holding.FundID]
		if !seen {
			price = s.resolveNAV(ctx, holding.FundID)
			prices[holding.FundID] = price
		}

		valuation.Add(holding.Value(price))
	}

	// No rounding pass on the totals any more. They are sums of exact paise, so
	// they are already exact; the Round2 calls that used to live here existed
	// only to paper over float64 accumulation error.
	return valuation, nil
}

// resolveNAV is the cache-aside read: try Redis, fall back to the feed, then
// populate the cache. Cache errors degrade to a feed read instead of failing
// the request.
func (s *PortfolioService) resolveNAV(ctx context.Context, fundID string) float64 {
	if price, hit, err := s.cache.GetNAV(ctx, fundID); err == nil && hit {
		return price
	} else if err != nil {
		s.logger.WarnContext(ctx, "nav cache read failed",
			slog.String("fund_id", fundID), slog.String("error", err.Error()))
	}

	price, err := s.nav.Fetch(ctx, fundID)
	if err != nil {
		s.logger.ErrorContext(ctx, "nav feed lookup failed",
			slog.String("fund_id", fundID), slog.String("error", err.Error()))
		return 0
	}

	if err := s.cache.SetNAV(ctx, fundID, price); err != nil {
		s.logger.WarnContext(ctx, "nav cache write failed",
			slog.String("fund_id", fundID), slog.String("error", err.Error()))
	}
	return price
}

// ApplyOrderEvent projects one executed order onto the ledger.
//
// This is the write half of the service, and it is deliberately the only path
// by which the Kafka consumer can reach the repository. The consumer knows
// about offsets, retries and dead letters; it knows nothing about NAVs, units
// or what an order type means. Keeping the projection here is what lets the
// rules below be tested with a fake store and no broker at all.
//
// It returns whether the ledger actually changed: false for an event this
// service does not act on, and false for one it has already applied. Both are
// successes — the consumer must commit the offset in either case, or the
// partition stalls forever on a message nobody wants.
func (s *PortfolioService) ApplyOrderEvent(ctx context.Context, event domain.OrderEvent) (bool, error) {
	if err := event.Validate(); err != nil {
		return false, err
	}

	// Every other status on this topic belongs to notification-service. Skipping
	// them here rather than filtering in the consumer keeps the decision about
	// what moves a position in the layer that owns positions.
	if !event.IsExecuted() {
		return false, nil
	}

	side, err := event.Order.Side()
	if err != nil {
		return false, err
	}

	units, err := s.unitsFor(ctx, event.Order)
	if err != nil {
		return false, err
	}

	applied, err := s.holdings.Apply(ctx, repository.Movement{
		EventID: event.EventID,
		UserID:  event.Order.UserID,
		FundID:  event.Order.FundID,
		Side:    side,
		Units:   units,
		Amount:  event.Order.Amount,
	})
	if err != nil {
		return false, err
	}

	if !applied {
		s.logger.InfoContext(ctx, "duplicate order event ignored",
			slog.String("event_id", event.EventID),
			slog.String("order_id", event.Order.ID),
		)
		return false, nil
	}

	s.logger.InfoContext(ctx, "holding updated from executed order",
		slog.String("event_id", event.EventID),
		slog.String("order_id", event.Order.ID),
		slog.String("user_id", event.Order.UserID),
		slog.String("fund_id", event.Order.FundID),
		slog.String("side", string(side)),
		slog.Float64("units", units),
		slog.String("amount", event.Order.Amount.String()),
	)
	return true, nil
}

// unitsFor converts an executed order's rupee amount into a unit count.
//
// This is the weakest point in the whole pipeline and it is worth being precise
// about why. The v2 envelope carries what the customer paid but not what they
// got: there is no allotted unit count and no execution NAV on the wire. So the
// unit count has to be reconstructed here, from the NAV that is current when
// the event is *consumed* rather than the one the order actually filled at. For
// a fill consumed a second later on a fund priced once a day those are the same
// number. For an event redelivered after a two-hour outage, on a fund whose NAV
// has since moved, they are not — and the ledger would record a position the
// custodian disagrees with.
//
// The correct fix is on the producer: order-service already knows the execution
// NAV, and adding `units` and `execution_nav` to a v3 envelope would make this
// function a field read. That is a cross-service schema change with its own
// rollout, so what this build does instead is refuse to guess when it cannot
// price the fill at all — a NAV of zero means the upstream feed is down, and a
// position of zero units for a real payment is a corruption that no later event
// undoes. Failing here is transient by default, so the consumer retries and
// then parks; the customer's money is never silently converted into nothing.
func (s *PortfolioService) unitsFor(ctx context.Context, order domain.Order) (float64, error) {
	nav := s.resolveNAV(ctx, order.FundID)

	units := domain.UnitsFor(order.Amount, nav)
	if units <= 0 {
		return 0, fmt.Errorf("cannot price fill for fund %s: nav feed returned %v", order.FundID, nav)
	}
	return units, nil
}
