// Package domain holds the portfolio valuation model. The arithmetic lives here
// rather than in the gRPC handler so it can be unit tested without a server.
//
// Money is carried as integer paise (see money.go). NAV and unit counts stay
// float64, and that split is deliberate rather than half-finished work: a NAV
// is a quoted price and a unit count is genuinely fractional — a SIP buys
// 12.4471 units — so neither is a money amount and neither can be an integer
// without losing real information. Money is the thing that must be exact,
// because it is the thing that gets summed, compared and paid out.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package domain

import "math"

// Holding is one fund position owned by a user.
type Holding struct {
	FundID         string
	FundName       string
	Units          float64
	InvestedAmount Money
}

// HoldingValuation is a holding marked to the latest NAV.
type HoldingValuation struct {
	Holding
	NAV            float64
	CurrentValue   Money
	UnrealizedGain Money
}

// PortfolioValuation is the aggregate result returned to callers.
type PortfolioValuation struct {
	UserID              string
	TotalValue          Money
	TotalUnrealizedGain Money
	Holdings            []HoldingValuation
}

// Value marks a single holding to market.
//
// This is the one place in the service where a float touches money, and it is
// unavoidable: units are fractional, so units × NAV is a real multiplication.
// What the integer type buys is that the result is rounded to a whole paise
// exactly once, here, and is exact from this point onwards. Under the old
// float64 model every subsequent addition carried its own error, so a
// portfolio's total could disagree with the sum of the rows shown beneath it.
//
// The multiplication is done in paise rather than in rupees (nav*100 first, not
// units*nav then *100) to keep the intermediate magnitude large and the
// relative rounding error correspondingly small.
func (h Holding) Value(nav float64) HoldingValuation {
	current := paiseFromProduct(nav, h.Units)
	return HoldingValuation{
		Holding:        h,
		NAV:            nav,
		CurrentValue:   current,
		UnrealizedGain: current - h.InvestedAmount,
	}
}

// Add accumulates a valuation into the portfolio total.
//
// Note what is missing: there is no rounding step. Adding two exact paise
// amounts is exact, so a total can no longer drift away from the rows it is a
// total of. The old code had to call Round2 on the accumulated float64 to hide
// that drift, which only ever moved the discrepancy somewhere less visible.
func (p *PortfolioValuation) Add(valued HoldingValuation) {
	p.TotalValue += valued.CurrentValue
	p.TotalUnrealizedGain += valued.UnrealizedGain
	p.Holdings = append(p.Holdings, valued)
}

// paiseFromProduct multiplies a quoted rupee price by a fractional unit count
// and rounds the result to the nearest paise, half away from zero.
func paiseFromProduct(navRupees, units float64) Money {
	if math.IsNaN(navRupees) || math.IsInf(navRupees, 0) ||
		math.IsNaN(units) || math.IsInf(units, 0) {
		return 0
	}
	return Money(math.Round(navRupees * PaisePerRupee * units))
}

// RoundNAV normalises a quoted price to the two decimal places AMCs publish.
//
// This is the replacement for the old Round2, narrowed to what it is actually
// for. Round2 was named after its implementation and was therefore applied to
// money as well as to prices; this one is named after its subject, so reaching
// for it on a money value now reads as obviously wrong.
func RoundNAV(price float64) float64 {
	return math.Round(price*100) / 100
}
