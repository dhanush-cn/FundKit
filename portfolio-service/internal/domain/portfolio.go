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

import (
	"fmt"
	"math"
)

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

// ---------------------------------------------------------------------------
// Position arithmetic — how an executed order moves a holding.
//
// This lives next to the valuation maths rather than in the repository for the
// same reason the rest of this file does: it is the part that can be wrong in a
// way a customer notices, so it belongs where it can be unit tested without a
// broker, a mutex or a map.
// ---------------------------------------------------------------------------

// UnitEpsilon is the tolerance below which a unit count is treated as zero.
//
// Units are float64 — deliberately, see the package comment — so a position
// sold in full will not generally reach exactly 0. Selling 4.1 units of a
// 12.4 unit holding three times leaves something like 1e-15 units behind, and
// without a tolerance that residue survives as a permanent ₹0.00 row on the
// customer's dashboard that they cannot get rid of. The threshold is set far
// above accumulated float error and far below any real fractional allotment: an
// AMC allots to three or four decimal places, so a real position is never
// smaller than 1e-4 units.
const UnitEpsilon = 1e-9

// AvgCost returns the average price paid per unit, in paise.
//
// The average is derived rather than stored, and that is the whole design of
// this struct. Storing a running average would mean recomputing and re-rounding
// it on every purchase, so a position built by 36 monthly SIP instalments would
// carry 36 roundings of accumulated error in the number the P&L is measured
// against. Keeping the two exact facts instead — total paise invested, total
// units held — means the average is exact at every point and the unrealized
// gain is a subtraction of two amounts that were never approximated.
func (h Holding) AvgCost() Money {
	if h.Units <= UnitEpsilon {
		return 0
	}
	return Money(math.Round(float64(h.InvestedAmount) / h.Units))
}

// ApplyBuy adds units to a position at the price actually paid.
//
// Both fields are accumulated, so the average cost this implies updates on its
// own. There is no rounding step here at all: paise are added to paise and
// units to units, which is what makes a position built over years agree with
// the sum of the orders that built it.
func (h *Holding) ApplyBuy(units float64, amount Money) {
	h.Units += units
	h.InvestedAmount += amount
}

// ApplySell removes units from a position and releases the cost basis they
// carried, returning the amount released.
//
// The cost released is proportional to the units sold, valued at the position's
// average cost — the standard treatment, and the one that leaves the remaining
// position with the same average cost it had before the sale. It is computed
// from the stored integer total rather than from AvgCost() so the rounding
// happens once, on the amount actually released, instead of twice.
//
// The final unit of a position is a special case: whatever paise remain are
// released in full rather than proportionally, so closing a position always
// leaves an invested amount of exactly zero. Without that, a sequence of
// partial sales could leave a few paise of basis attached to no units at all.
func (h *Holding) ApplySell(units float64) (Money, error) {
	if units <= 0 {
		return 0, fmt.Errorf("%w: sell of %v units is not a positive quantity", ErrPermanent, units)
	}
	// The epsilon is applied to the comparison, not to the stored value: a sale
	// of exactly the held quantity must succeed even when the two numbers
	// differ in their last bit, which is the normal case for a full exit.
	if units > h.Units+UnitEpsilon {
		return 0, fmt.Errorf("%w: cannot sell %v units of %s, position holds %v",
			ErrPermanent, units, h.FundID, h.Units)
	}

	remaining := h.Units - units
	if remaining <= UnitEpsilon {
		released := h.InvestedAmount
		h.Units = 0
		h.InvestedAmount = 0
		return released, nil
	}

	released := Money(math.Round(float64(h.InvestedAmount) * units / h.Units))
	h.Units = remaining
	h.InvestedAmount -= released
	return released, nil
}

// IsClosed reports whether the position has been fully exited and should be
// removed from the ledger rather than kept as an empty row.
func (h Holding) IsClosed() bool {
	return h.Units <= UnitEpsilon
}

// UnitsFor converts a rupee-denominated order amount into the units it buys at
// a given NAV.
//
// This is the one conversion in the ledger path that a float has to make, and
// it is the mirror image of Holding.Value: that one turns units into money,
// this one turns money into units. Both are real divisions of real quantities,
// so neither can be made exact — what can be controlled is that each happens
// exactly once, in a named function, where it can be pointed at.
//
// A non-positive NAV yields zero units rather than an infinity. That case is
// reachable: resolveNAV returns 0 when the upstream feed is down, and a
// position of +Inf units would corrupt the customer's portfolio permanently,
// where zero units is merely wrong in a way the caller can detect and refuse.
func UnitsFor(amount Money, navRupees float64) float64 {
	if navRupees <= 0 || math.IsNaN(navRupees) || math.IsInf(navRupees, 0) {
		return 0
	}
	return amount.Rupees() / navRupees
}
