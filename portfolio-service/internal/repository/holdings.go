// Engineered by Dhanush C N (github.com/dhanush-cn)
package repository

import (
	"context"
	"sync"

	"github.com/dhanush-cn/fundkit/portfolio-service/internal/domain"
)

// HoldingsRepository is an in-memory stand-in for the custodian ledger. It is
// deliberately behind an interface-shaped type so swapping it for Postgres is a
// one-file change that the service layer never sees.
//
// Seeded amounts are paise: 150000 is ₹1,500.00.
type HoldingsRepository struct {
	mu     sync.RWMutex
	byUser map[string][]domain.Holding
}

func NewHoldingsRepository() *HoldingsRepository {
	return &HoldingsRepository{
		byUser: map[string][]domain.Holding{
			"user-1": {
				{FundID: "fund-axi-blue", FundName: "AXI Bluechip", Units: 12.5, InvestedAmount: 150000},
				{FundID: "fund-icici-growth", FundName: "ICICI Growth", Units: 8.2, InvestedAmount: 120000},
			},
			"user-2": {
				{FundID: "fund-hdfc-top", FundName: "HDFC Top 100", Units: 20.0, InvestedAmount: 320000},
			},
		},
	}
}

// ListByUser returns a defensive copy so callers cannot mutate stored state.
func (r *HoldingsRepository) ListByUser(_ context.Context, userID string) ([]domain.Holding, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stored := r.byUser[userID]
	holdings := make([]domain.Holding, len(stored))
	copy(holdings, stored)
	return holdings, nil
}
