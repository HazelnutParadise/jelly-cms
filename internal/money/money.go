// Package money is the only place where shop amounts are formatted or converted
// for a payment gateway. Amounts are stored as int64 minor units (分): NT$120 is
// 12000.
package money

import (
	"errors"
	"strconv"
	"strings"
)

// ErrNotImplemented marks the skeleton functions below (ticket 07, E1).
var ErrNotImplemented = errors.New("money: not implemented")

// Format renders minor units for display: "NT$120" for TWD 12000, thousands
// separators ("NT$12,000"), and two decimals only when there is a fractional
// part ("NT$120.50"). Negative amounts get a leading "-". Unknown currencies
// use the ISO code as prefix ("JPY 500"). Format never returns an empty string
// for a valid amount.
func Format(minor int64, currency string) string {
	prefix := currencyPrefix(currency)

	negative := minor < 0
	// Work on the magnitude as uint64 so the minimum int64 does not overflow.
	magnitude := uint64(minor)
	if negative {
		magnitude = uint64(-(minor + 1)) + 1
	}
	whole := magnitude / 100
	fraction := magnitude % 100

	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	b.WriteString(prefix)
	b.WriteString(groupThousands(strconv.FormatUint(whole, 10)))
	if fraction != 0 {
		b.WriteByte('.')
		if fraction < 10 {
			b.WriteByte('0')
		}
		b.WriteString(strconv.FormatUint(fraction, 10))
	}
	return b.String()
}

// currencyPrefix maps a currency code to the text that precedes the amount.
func currencyPrefix(currency string) string {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "", "TWD":
		return "NT$"
	case "USD":
		return "US$"
	default:
		return strings.ToUpper(strings.TrimSpace(currency)) + " "
	}
}

// groupThousands inserts a comma every three digits, counting from the right.
func groupThousands(digits string) string {
	if len(digits) <= 3 {
		return digits
	}
	var b strings.Builder
	first := len(digits) % 3
	if first == 0 {
		first = 3
	}
	b.WriteString(digits[:first])
	for i := first; i < len(digits); i += 3 {
		b.WriteByte(',')
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// FromInput parses a whole-yuan amount typed by an administrator ("120") and
// returns minor units (12000). Decimals, signs and separators are rejected.
//
// Skeleton: not implemented yet.
func FromInput(s string) (int64, error) {
	return 0, ErrNotImplemented
}

// ToGateway converts minor units to the whole-yuan integer that ECPay and
// NewebPay expect. It is the only conversion point; callers must not divide
// by 100 themselves.
//
// Skeleton: not implemented yet.
func ToGateway(minor int64) (int64, error) {
	return 0, errors.New("money: ToGateway not implemented")
}
