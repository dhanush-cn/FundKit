// Package service implements portfolio valuation as a cache-aside read path.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package service

import (
	"context"
	"log/slog"

	"github.com/dhanush-cn/fundkit/portfolio-service/internal/domain"
)

// HoldingsStore is the ledger port.
type HoldingsStore interface {
	ListByUser(ctx context.Context, userID string) ([]domain.Holding, error)
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
