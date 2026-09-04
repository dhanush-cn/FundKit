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

// ---------------------------------------------------------------------------
// Position arithmetic.
// ---------------------------------------------------------------------------

func TestAvgCostIsDerivedNotAccumulated(t *testing.T) {
	// 36 monthly SIP instalments of ₹5,000.00, each buying units at a slightly
	// different NAV. The point is not the final number but that it is computed
	// from two exact running totals: a stored average would carry 36 roundings
	// by the time the customer sees it.
	holding := Holding{FundID: "fund-axi-blue"}
	nav := 100.0
	for month := 0; month < 36; month++ {
		amount := Money(500000)
		holding.ApplyBuy(UnitsFor(amount, nav), amount)
		nav += 1.37
	}

	if holding.InvestedAmount != 36*500000 {
		t.Errorf("InvestedAmount = %d, want %d — accumulating paise must be exact",
			holding.InvestedAmount, 36*500000)
	}
	// avg = total paise / total units, and it must agree with the totals to the
	// paise rather than to "about right".
	want := Money(float64(holding.InvestedAmount)/holding.Units + 0.5)
	if got := holding.AvgCost(); got != want && got != want-1 {
		t.Errorf("AvgCost() = %d, want %d derived from the totals", got, want)
	}
}

func TestApplyBuyWeightsTheAverage(t *testing.T) {
	holding := Holding{FundID: "fund-axi-blue"}
	holding.ApplyBuy(10, 100000) // 10 units at ₹100.00
	holding.ApplyBuy(30, 600000) // 30 units at ₹200.00

	// The weighted average is ₹175.00, not the ₹150.00 an unweighted mean of
	// the two prices would give.
	if got := holding.AvgCost(); got != 17500 {
		t.Errorf("AvgCost() = %d (%s), want 17500 (₹175.00)", got, got)
	}
}

func TestApplySellReleasesCostAtAverage(t *testing.T) {
	holding := Holding{FundID: "fund-axi-blue", Units: 40, InvestedAmount: 700000}

	released, err := holding.ApplySell(10)
	if err != nil {
		t.Fatalf("ApplySell() error = %v", err)
	}
	// A quarter of the units carries a quarter of the basis: ₹1,750.00.
	if released != 175000 {
		t.Errorf("released = %d, want 175000", released)
	}
	if holding.Units != 30 || holding.InvestedAmount != 525000 {
		t.Errorf("position = %v / %d, want 30 / 525000", holding.Units, holding.InvestedAmount)
	}
	if got := holding.AvgCost(); got != 17500 {
		t.Errorf("AvgCost() = %d after a partial sale, want it unchanged at 17500", got)
	}
}

// TestApplySellClosesCleanly is the special case that keeps the ledger honest:
// the last sale releases whatever paise remain in full, so a closed position
// never leaves a basis attached to no units.
func TestApplySellClosesCleanly(t *testing.T) {
	holding := Holding{FundID: "fund-axi-blue", Units: 3, InvestedAmount: 100000}

	// Three sales of one unit each. 100000/3 does not divide evenly, so a purely
	// proportional release would strand a paise or two on the last one.
	for i := 0; i < 3; i++ {
		if _, err := holding.ApplySell(1); err != nil {
			t.Fatalf("ApplySell() %d error = %v", i+1, err)
		}
	}

	if !holding.IsClosed() {
		t.Errorf("IsClosed() = false with %v units remaining", holding.Units)
	}
	if holding.InvestedAmount != 0 {
		t.Errorf("InvestedAmount = %d after a full exit, want 0", holding.InvestedAmount)
	}
}

func TestApplySellRejectsOversell(t *testing.T) {
	holding := Holding{FundID: "fund-axi-blue", Units: 5, InvestedAmount: 50000}

	if _, err := holding.ApplySell(5.001); err == nil {
		t.Fatal("ApplySell() accepted a sale larger than the position")
	}
	if holding.Units != 5 || holding.InvestedAmount != 50000 {
		t.Errorf("position = %v / %d after a rejected sale, want it untouched",
			holding.Units, holding.InvestedAmount)
	}

	if _, err := holding.ApplySell(0); err == nil {
		t.Error("ApplySell() accepted a zero-unit sale")
	}
}

// TestApplySellToleratesFloatDrift covers the epsilon: a full exit must succeed
// even when the two unit counts differ in their last bit, which is the normal
// case rather than an edge one.
func TestApplySellToleratesFloatDrift(t *testing.T) {
	holding := Holding{FundID: "fund-axi-blue"}
	holding.ApplyBuy(0.1, 1000)
	holding.ApplyBuy(0.2, 2000)
	// 0.1 + 0.2 is 0.30000000000000004, so selling a literal 0.3 is selling
	// very slightly less than the position holds — and must still close it.
	if _, err := holding.ApplySell(0.3); err != nil {
		t.Fatalf("ApplySell(0.3) error = %v", err)
	}
	if !holding.IsClosed() {
		t.Errorf("IsClosed() = false, %v units of float residue survived", holding.Units)
	}
}

func TestUnitsForRefusesUnpricedFills(t *testing.T) {
	// A NAV of zero is what resolveNAV returns when the upstream feed is down.
	// Returning +Inf units here would corrupt a customer's position permanently.
	for _, nav := range []float64{0, -1} {
		if got := UnitsFor(150000, nav); got != 0 {
			t.Errorf("UnitsFor(_, %v) = %v, want 0", nav, got)
		}
	}

	// ₹1,500.00 at a NAV of ₹125.00 buys 12 units.
	if got := UnitsFor(150000, 125); got != 12 {
		t.Errorf("UnitsFor(150000, 125) = %v, want 12", got)
	}
}

// TestBuyThenValueRoundTrips ties the two halves together: units bought at a
// NAV, then valued at the same NAV, must be worth what was paid. Any systematic
// bias in either conversion shows up here as a difference.
func TestBuyThenValueRoundTrips(t *testing.T) {
	amount := Money(150000)
	nav := 125.45

	holding := Holding{FundID: "fund-axi-blue"}
	holding.ApplyBuy(UnitsFor(amount, nav), amount)

	valued := holding.Value(nav)
	if diff := valued.UnrealizedGain; diff > 1 || diff < -1 {
		t.Errorf("UnrealizedGain = %d paise immediately after purchase, want within 1 paise of 0", diff)
	}
}
