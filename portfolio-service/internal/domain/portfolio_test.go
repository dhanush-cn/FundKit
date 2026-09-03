// Engineered by Dhanush C N (github.com/dhanush-cn)
package domain

import "testing"

func TestHoldingValue(t *testing.T) {
	// ₹1,500.00 invested, 12.5 units.
	holding := Holding{FundID: "fund-axi-blue", FundName: "AXI Bluechip", Units: 12.5, InvestedAmount: 150000}

	valued := holding.Value(125.45)

	// 125.45 × 12.5 = 1568.125 exactly, which rounds to ₹1,568.13 = 156813p.
	if got, want := valued.CurrentValue, Money(156813); got != want {
		t.Fatalf("CurrentValue = %d, want %d", got, want)
	}
	if got, want := valued.UnrealizedGain, Money(6813); got != want {
		t.Fatalf("UnrealizedGain = %d, want %d", got, want)
	}
}

func TestHoldingValueReportsLoss(t *testing.T) {
	holding := Holding{Units: 10, InvestedAmount: 200000}

	valued := holding.Value(150)

	if valued.UnrealizedGain >= 0 {
		t.Fatalf("expected a loss, got %s", valued.UnrealizedGain)
	}
	if got, want := valued.UnrealizedGain, Money(-50000); got != want {
		t.Fatalf("UnrealizedGain = %d, want %d", got, want)
	}
}

// The regression this whole change exists to prevent.
//
// Under float64 these three valuations summed to 0.30000000000000004, so the
// portfolio total disagreed with the sum of the rows displayed beneath it and
// the code had to round the total to hide it. With integer paise the sum is
// exact and no rounding step is involved.
func TestTotalsAreExactAcrossManyHoldings(t *testing.T) {
	valuation := PortfolioValuation{UserID: "user-1"}

	// 0.1 + 0.2 + 0.3, the canonical IEEE-754 demonstration, in paise.
	for _, paise := range []Money{10, 20, 30} {
		valuation.Add(HoldingValuation{CurrentValue: paise, UnrealizedGain: paise})
	}

	if got, want := valuation.TotalValue, Money(60); got != want {
		t.Fatalf("TotalValue = %d, want exactly %d", got, want)
	}
	if got := valuation.TotalValue.String(); got != "₹0.60" {
		t.Fatalf("TotalValue renders as %q, want ₹0.60", got)
	}
}

// A thousand small SIP instalments is where float error becomes visible money.
func TestTotalsDoNotDriftOverManyAdditions(t *testing.T) {
	valuation := PortfolioValuation{UserID: "user-1"}

	for i := 0; i < 1000; i++ {
		valuation.Add(HoldingValuation{CurrentValue: 1}) // one paise each
	}

	if got, want := valuation.TotalValue, Money(1000); got != want {
		t.Fatalf("TotalValue = %d, want %d — the total drifted", got, want)
	}
}

func TestValueRoundsHalfAwayFromZero(t *testing.T) {
	// 2.675 × 100 is exactly 267.5 in float64, so this is a real half-way case
	// and it must land on 268 rather than 267.
	//
	// Worth knowing why 1.005 is NOT used here: it looks like the same test,
	// but 1.005 is not representable in binary and its nearest float64 is
	// slightly below, so 1.005 × 100 is 100.49999999999999 and rounds *down* to
	// 100. That is not a bug in this function — it is the reason the function
	// exists. The float is already wrong before any rounding happens, which is
	// precisely the argument for storing money as an integer and only ever
	// letting a float near it at this one multiplication.
	holding := Holding{Units: 1}
	if got, want := holding.Value(2.675).CurrentValue, Money(268); got != want {
		t.Fatalf("CurrentValue = %d, want %d", got, want)
	}
}

func TestValueToleratesAMissingNAV(t *testing.T) {
	// resolveNAV returns 0 when the feed is down. That must produce a zero
	// valuation, not a panic or a NaN written into a total.
	holding := Holding{Units: 12.5, InvestedAmount: 150000}

	valued := holding.Value(0)

	if valued.CurrentValue != 0 {
		t.Fatalf("CurrentValue = %d, want 0 when no NAV is available", valued.CurrentValue)
	}
	if got, want := valued.UnrealizedGain, Money(-150000); got != want {
		t.Fatalf("UnrealizedGain = %d, want %d", got, want)
	}
}

func TestRoundNAV(t *testing.T) {
	if got := RoundNAV(125.4550001); got != 125.46 {
		t.Fatalf("RoundNAV = %v, want 125.46", got)
	}
}
