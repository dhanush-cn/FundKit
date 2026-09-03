// Package domain holds the portfolio valuation model. The arithmetic lives here
// rather than in the gRPC handler so it can be unit tested without a server.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package domain

import "math"

// Holding is one fund position owned by a user.
type Holding struct {
	FundID         string
	FundName       string
	Units          float64
	InvestedAmount float64
}

// HoldingValuation is a holding marked to the latest NAV.
type HoldingValuation struct {
	Holding
	NAV            float64
	CurrentValue   float64
	UnrealizedGain float64
}

// PortfolioValuation is the aggregate result returned to callers.
type PortfolioValuation struct {
	UserID              string
	TotalValue          float64
	TotalUnrealizedGain float64
	Holdings            []HoldingValuation
}

// Value marks a single holding to market.
func (h Holding) Value(nav float64) HoldingValuation {
	current := nav * h.Units
	return HoldingValuation{
		Holding:        h,
		NAV:            nav,
		CurrentValue:   Round2(current),
		UnrealizedGain: Round2(current - h.InvestedAmount),
	}
}

// Round2 normalises money to two decimal places at the boundary. Float64 is
// acceptable here because these are display valuations; the ledger of record
// would use integer minor units.
func Round2(value float64) float64 {
	return math.Round(value*100) / 100
}
