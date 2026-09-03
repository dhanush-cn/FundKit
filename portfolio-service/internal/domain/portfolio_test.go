// Engineered by Dhanush C N (github.com/dhanush-cn)
package domain

import "testing"

func TestHoldingValue(t *testing.T) {
	holding := Holding{FundID: "fund-axi-blue", FundName: "AXI Bluechip", Units: 12.5, InvestedAmount: 1500}

	valued := holding.Value(125.45)

	if got, want := valued.CurrentValue, 1568.13; got != want {
		t.Fatalf("CurrentValue = %.2f, want %.2f", got, want)
	}
	if got, want := valued.UnrealizedGain, 68.13; got != want {
		t.Fatalf("UnrealizedGain = %.2f, want %.2f", got, want)
	}
}

func TestHoldingValueReportsLoss(t *testing.T) {
	holding := Holding{Units: 10, InvestedAmount: 2000}

	valued := holding.Value(150)

	if valued.UnrealizedGain >= 0 {
		t.Fatalf("expected a loss, got %.2f", valued.UnrealizedGain)
	}
}

func TestRound2(t *testing.T) {
	if got := Round2(1568.1250001); got != 1568.13 {
		t.Fatalf("Round2 = %v, want 1568.13", got)
	}
}
