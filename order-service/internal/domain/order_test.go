// Engineered by Dhanush C N (github.com/dhanush-cn)
package domain

import "testing"

// The state machine is the one piece of order-service that must never regress,
// so it is covered directly and without any infrastructure in the way.
func TestCanTransitionStatus(t *testing.T) {
	cases := []struct {
		name    string
		current OrderStatus
		next    OrderStatus
		allowed bool
	}{
		{"pending to processing", StatusPending, StatusProcessing, true},
		{"pending to failed", StatusPending, StatusFailed, true},
		{"pending cannot skip to executed", StatusPending, StatusExecuted, false},
		{"processing to executed", StatusProcessing, StatusExecuted, true},
		{"processing to failed", StatusProcessing, StatusFailed, true},
		{"processing cannot regress", StatusProcessing, StatusPending, false},
		{"executed is absorbing", StatusExecuted, StatusFailed, false},
		{"executed replay is tolerated", StatusExecuted, StatusExecuted, true},
		{"failed replay is tolerated", StatusFailed, StatusFailed, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanTransitionStatus(tc.current, tc.next); got != tc.allowed {
				t.Fatalf("CanTransitionStatus(%s, %s) = %v, want %v", tc.current, tc.next, got, tc.allowed)
			}
		})
	}
}

func TestTransitionRejectsIllegalEdge(t *testing.T) {
	order := &Order{Status: StatusPending}
	if err := order.Transition(StatusExecuted); err == nil {
		t.Fatal("expected PENDING -> EXECUTED to be rejected")
	}
	if order.Status != StatusPending {
		t.Fatalf("status mutated on a rejected transition: %s", order.Status)
	}
}

func TestIsTerminal(t *testing.T) {
	if (&Order{Status: StatusProcessing}).IsTerminal() {
		t.Fatal("PROCESSING must not be terminal")
	}
	if !(&Order{Status: StatusExecuted}).IsTerminal() {
		t.Fatal("EXECUTED must be terminal")
	}
}
