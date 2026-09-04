// Package domain — the money type.
//
// Money is an integer count of paise, never a float64. The reason is not
// pedantry: float64 cannot represent 0.1 exactly, so a ledger built on it
// accumulates error that shows up as a few paise of drift on a reconciliation
// run and, eventually, as a customer complaint nobody can reproduce. The
// classic demonstration is that 0.1 + 0.2 != 0.3 in IEEE-754. An order book
// must be exact, so the amount is stored, transported and compared as an
// integer, and only rendered as rupees at the very edge.
//
// The unit is the paise (1/100 of a rupee), which is the smallest unit the
// Indian payment rails settle in. int64 paise covers roughly ±92,000 trillion
// rupees, so overflow is not a practical concern for a retail fund platform.
//
// Each service keeps its own copy of this type rather than sharing a module.
// That is the same deliberate choice notification-service already makes for the
// event schema: a service owns the shape it can tolerate.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package domain

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Money is an amount in paise. The zero value is a valid ₹0.00.
type Money int64

// PaisePerRupee is the scaling factor between the storage unit and the
// presentation unit.
const PaisePerRupee = 100

// ErrInvalidAmount is returned when text cannot be read as a rupee amount.
var ErrInvalidAmount = errors.New("invalid rupee amount")

// NewMoneyFromRupees converts a rupee figure to paise, rounding half away from
// zero.
//
// This exists for boundaries that hand over a float — a legacy payload, a
// spreadsheet import — and nothing else should call it. Every such call is a
// place where precision was already lost upstream; keeping them to one named
// function makes them greppable.
func NewMoneyFromRupees(rupees float64) Money {
	if math.IsNaN(rupees) || math.IsInf(rupees, 0) {
		return 0
	}
	return Money(math.Round(rupees * PaisePerRupee))
}

// Paise returns the raw integer amount. Named rather than relying on a
// conversion so call sites read as a unit assertion.
func (m Money) Paise() int64 { return int64(m) }

// Rupees returns the amount as a float64 for presentation and for wire formats
// that only speak double (the portfolio gRPC contract, chart libraries).
//
// The result is lossy by definition and must never be fed back into a
// calculation whose result is stored.
func (m Money) Rupees() float64 { return float64(m) / PaisePerRupee }

// IsPositive reports whether the amount is strictly greater than zero, which is
// the validity rule for an order.
func (m Money) IsPositive() bool { return m > 0 }

// String renders the amount with the rupee sign and Indian digit grouping, so
// ₹1234567.89 prints as ₹12,34,567.89 rather than the western ₹1,234,567.89.
// It satisfies fmt.Stringer, which means slog and %v render money correctly
// with no call site having to remember to format it.
func (m Money) String() string { return m.Format("₹") }

// Rupee renders the amount with grouping but no currency symbol, for contexts
// where the unit is already stated in a column header or a label.
func (m Money) Rupee() string { return m.Format("") }

// Format renders the amount with an arbitrary prefix. A negative amount puts
// the sign before the symbol (-₹68.13), which is how Indian statements read.
func (m Money) Format(symbol string) string {
	sign := ""
	value := int64(m)
	if value < 0 {
		sign = "-"
		// Negating math.MinInt64 overflows, so the magnitude is taken on the
		// unsigned side. This branch is unreachable with real money, but a
		// formatter that panics on a sentinel value is a bad formatter.
		if value == math.MinInt64 {
			return sign + symbol + groupIndian("92233720368547758") + ".08"
		}
		value = -value
	}

	whole := value / PaisePerRupee
	fraction := value % PaisePerRupee

	return fmt.Sprintf("%s%s%s.%02d", sign, symbol, groupIndian(strconv.FormatInt(whole, 10)), fraction)
}

// ParseRupees reads a human rupee string ("1500", "1,500.50", "₹1,500.50")
// into exact paise.
//
// It works on the decimal text rather than going via strconv.ParseFloat, so the
// value never passes through a float64 and cannot pick up representation error
// on the way in. More than two decimal places is rejected instead of silently
// rounded: a caller sending ₹10.005 has a bug, and swallowing it would put the
// difference somewhere nobody is looking.
func ParseRupees(text string) (Money, error) {
	cleaned := strings.NewReplacer(",", "", "₹", "", " ", "").Replace(strings.TrimSpace(text))
	if cleaned == "" {
		return 0, fmt.Errorf("%w: empty", ErrInvalidAmount)
	}

	negative := false
	switch cleaned[0] {
	case '-':
		negative = true
		cleaned = cleaned[1:]
	case '+':
		cleaned = cleaned[1:]
	}

	whole, fraction, hasFraction := strings.Cut(cleaned, ".")
	if whole == "" {
		whole = "0"
	}
	if hasFraction {
		if len(fraction) > 2 {
			return 0, fmt.Errorf("%w: %q has more precision than one paise", ErrInvalidAmount, text)
		}
		fraction += strings.Repeat("0", 2-len(fraction))
	} else {
		fraction = "00"
	}

	wholeValue, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, text)
	}
	fractionValue, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, text)
	}

	total := wholeValue*PaisePerRupee + fractionValue
	if negative {
		total = -total
	}
	return Money(total), nil
}

// groupIndian inserts separators into a digit string using the lakh/crore
// convention: the last three digits form one group, everything above them is
// grouped in twos. 1234567 becomes 12,34,567.
func groupIndian(digits string) string {
	if len(digits) <= 3 {
		return digits
	}

	head, tail := digits[:len(digits)-3], digits[len(digits)-3:]

	var builder strings.Builder
	for index, char := range head {
		// A separator goes before every character that starts a new pair,
		// counting pairs from the right-hand end of the head.
		if index > 0 && (len(head)-index)%2 == 0 {
			builder.WriteByte(',')
		}
		builder.WriteRune(char)
	}
	builder.WriteByte(',')
	builder.WriteString(tail)
	return builder.String()
}
