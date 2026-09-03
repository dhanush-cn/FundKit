// Engineered by Dhanush C N (github.com/dhanush-cn)
package domain

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

// The reason the type exists, stated as a test.
//
// Under float64 this assertion fails: 0.1 + 0.2 is 0.30000000000000004, so a
// ledger built on floats disagrees with itself over three instalments. In paise
// it is integer addition and it is exact.
func TestMoneyAdditionIsExact(t *testing.T) {
	t.Parallel()

	sum := Money(10) + Money(20) // ₹0.10 + ₹0.20
	if sum != Money(30) {
		t.Fatalf("sum = %d, want exactly 30 paise", sum)
	}

	// The float equivalent, for contrast — and a Go subtlety worth knowing.
	//
	// Writing `0.1 + 0.2 == 0.3` directly does NOT demonstrate anything: Go's
	// untyped constants are arbitrary-precision rationals evaluated at compile
	// time, so the compiler folds that to exactly 0.3 and the comparison is
	// true. The imprecision belongs to float64, not to the literals, so the
	// values have to pass through float64 variables before the hardware's
	// actual behaviour shows up.
	a, b, c := 0.1, 0.2, 0.3
	if a+b == c {
		t.Fatal("float64 addition is suddenly exact; the premise of this refactor needs review")
	}
}

func TestMoneyAccumulatesWithoutDrift(t *testing.T) {
	t.Parallel()

	var total Money
	for i := 0; i < 100000; i++ {
		total += 1 // one paise
	}
	if total != Money(100000) {
		t.Fatalf("total = %d, want 100000 paise (₹1,000.00)", total)
	}
}

func TestMoneyStringUsesIndianGrouping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		paise Money
		want  string
	}{
		{0, "₹0.00"},
		{1, "₹0.01"},
		{50, "₹0.50"},
		{10050, "₹100.50"},
		{100000, "₹1,000.00"},
		{500000, "₹5,000.00"},
		// The lakh boundary: Indian grouping, not the western ₹1,00,000 vs
		// ₹100,000 distinction that a default formatter would produce.
		{10000000, "₹1,00,000.00"},
		{123456789, "₹12,34,567.89"},
		{-6813, "-₹68.13"},
	}

	for _, test := range tests {
		if got := test.paise.String(); got != test.want {
			t.Errorf("Money(%d).String() = %q, want %q", test.paise, got, test.want)
		}
	}
}

func TestMoneyFormatWithoutSymbol(t *testing.T) {
	t.Parallel()

	if got, want := Money(123456789).Rupee(), "12,34,567.89"; got != want {
		t.Fatalf("Rupee() = %q, want %q", got, want)
	}
}

// A formatter that panics on a sentinel value is a formatter you cannot trust
// in a log line, and a log line is exactly where the strange value shows up.
func TestMoneyFormatSurvivesMinInt64(t *testing.T) {
	t.Parallel()

	if got := Money(math.MinInt64).String(); got == "" {
		t.Fatal("MinInt64 must format rather than panic or return empty")
	}
}

func TestParseRupeesIsExact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text string
		want Money
	}{
		{"0", 0},
		{"1500", 150000},
		{"100.50", 10050},
		{"0.01", 1},
		{"10.5", 1050}, // a single decimal place is padded, not truncated
		{".5", 50},     // and a leading zero is optional
		{"1,500.50", 150050},
		{"₹1,500.50", 150050},
		{" 1500 ", 150000},
		{"-68.13", -6813},
		{"+68.13", 6813},
		// The case that motivates parsing the text rather than the float:
		// 8.11 * 100 is 810.9999999999999 in IEEE-754 and truncates to 810.
		{"8.11", 811},
		{"70.07", 7007},
	}

	for _, test := range tests {
		got, err := ParseRupees(test.text)
		if err != nil {
			t.Errorf("ParseRupees(%q): unexpected error %v", test.text, err)
			continue
		}
		if got != test.want {
			t.Errorf("ParseRupees(%q) = %d, want %d", test.text, got, test.want)
		}
	}
}

// Silently rounding ₹10.005 would put half a paise somewhere nobody is looking.
// The caller has a bug and should hear about it.
func TestParseRupeesRejectsExcessPrecision(t *testing.T) {
	t.Parallel()

	for _, text := range []string{"10.005", "1.999", "", "abc", "1.2.3", "₹"} {
		if _, err := ParseRupees(text); !errors.Is(err, ErrInvalidAmount) {
			t.Errorf("ParseRupees(%q) error = %v, want ErrInvalidAmount", text, err)
		}
	}
}

func TestParseRupeesRoundTripsWithString(t *testing.T) {
	t.Parallel()

	for _, text := range []string{"0.01", "100.50", "12,34,567.89"} {
		parsed, err := ParseRupees(text)
		if err != nil {
			t.Fatalf("ParseRupees(%q): %v", text, err)
		}
		if got := parsed.Rupee(); got != text {
			t.Errorf("round trip of %q produced %q", text, got)
		}
	}
}

// The wire contract. `amount` is a JSON integer of paise, and a decimal must be
// refused rather than truncated — that refusal is what turns a client's unit
// mistake into a 400 instead of an order for a hundredth of the intended value.
func TestMoneyJSONIsIntegerPaise(t *testing.T) {
	t.Parallel()

	type payload struct {
		Amount Money `json:"amount"`
	}

	encoded, err := json.Marshal(payload{Amount: 10050})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got, want := string(encoded), `{"amount":10050}`; got != want {
		t.Fatalf("encoded = %s, want %s", got, want)
	}

	var decoded payload
	if err := json.Unmarshal([]byte(`{"amount":10050}`), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Amount != Money(10050) {
		t.Fatalf("decoded = %d, want 10050", decoded.Amount)
	}

	if err := json.Unmarshal([]byte(`{"amount":100.50}`), &decoded); err == nil {
		t.Fatal("a decimal amount must be rejected, not truncated")
	}
}

func TestNewMoneyFromRupees(t *testing.T) {
	t.Parallel()

	if got, want := NewMoneyFromRupees(100.50), Money(10050); got != want {
		t.Fatalf("NewMoneyFromRupees(100.50) = %d, want %d", got, want)
	}
	// A NaN from an upstream division must not become a nonsense amount.
	if got := NewMoneyFromRupees(math.NaN()); got != 0 {
		t.Fatalf("NaN produced %d, want 0", got)
	}
	if got := NewMoneyFromRupees(math.Inf(1)); got != 0 {
		t.Fatalf("+Inf produced %d, want 0", got)
	}
}

func TestMoneyAccessors(t *testing.T) {
	t.Parallel()

	amount := Money(10050)
	if amount.Paise() != 10050 {
		t.Fatalf("Paise() = %d", amount.Paise())
	}
	if amount.Rupees() != 100.50 {
		t.Fatalf("Rupees() = %v", amount.Rupees())
	}
	if !amount.IsPositive() {
		t.Fatal("10050 paise must be positive")
	}
	if Money(0).IsPositive() || Money(-1).IsPositive() {
		t.Fatal("zero and negative amounts must not be positive")
	}
}
