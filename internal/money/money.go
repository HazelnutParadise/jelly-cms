// Package money is the only place where shop amounts are formatted or converted
// for a payment gateway. Amounts are stored as int64 minor units (分): NT$120 is
// 12000.
package money

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

// Format renders minor units for display: "NT$120" for TWD 12000, thousands
// separators ("NT$12,000"), and two decimals only when there is a fractional
// part ("NT$120.50"). Negative amounts get a leading "-". Unknown currencies
// use the ISO code as prefix ("JPY 500"). Format never returns an empty string
// for a valid amount.
func Format(minor int64, currency string) string {
	prefix := currencyPrefix(currency)

	negative := minor < 0
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

// FromInput parses a non-negative amount with optional correctly grouped
// thousands separators and up to two fractional digits into minor units.
func FromInput(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("money: invalid amount")
	}
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		return 0, errors.New("money: invalid amount")
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 {
		return 0, errors.New("money: invalid amount")
	}
	whole := parts[0]
	if whole == "" || (len(parts) == 2 && parts[1] == "") {
		return 0, errors.New("money: invalid amount")
	}
	integer := whole
	if strings.Contains(whole, ",") {
		groups := strings.Split(whole, ",")
		if len(groups[0]) < 1 || len(groups[0]) > 3 {
			return 0, errors.New("money: invalid amount")
		}
		for i := 1; i < len(groups); i++ {
			if len(groups[i]) != 3 {
				return 0, errors.New("money: invalid amount")
			}
		}
		integer = strings.Join(groups, "")
	}
	for i := 0; i < len(integer); i++ {
		if integer[i] < '0' || integer[i] > '9' {
			return 0, errors.New("money: invalid amount")
		}
	}
	var fraction int64
	if len(parts) == 2 {
		frac := parts[1]
		if len(frac) > 2 {
			return 0, errors.New("money: invalid amount")
		}
		for i := 0; i < len(frac); i++ {
			if frac[i] < '0' || frac[i] > '9' {
				return 0, errors.New("money: invalid amount")
			}
			fraction = fraction*10 + int64(frac[i]-'0')
		}
		if len(frac) == 1 {
			fraction *= 10
		}
	}
	wholeValue, err := strconv.ParseInt(integer, 10, 64)
	if err != nil || wholeValue > math.MaxInt64/100 {
		return 0, errors.New("money: amount overflows minor units")
	}
	minor := wholeValue * 100
	if minor > math.MaxInt64-fraction {
		return 0, errors.New("money: amount overflows minor units")
	}
	return minor + fraction, nil
}

// ToGateway converts minor units to the whole-yuan integer that ECPay and
// NewebPay expect. It is the only conversion point; callers must not divide
// by 100 themselves.
//
// It refuses negative amounts and fractional NT$ values.
func ToGateway(minor int64) (int64, error) {
	if minor < 0 {
		return 0, errors.New("money: negative amount not allowed for gateway")
	}
	if minor%100 != 0 {
		return 0, errors.New("money: fractional amount not allowed for gateway")
	}
	return minor / 100, nil
}

// RoundToWhole rounds non-negative minor units to the nearest whole unit,
// with exact halves rounded up. It avoids adding a rounding bias before the
// division so MaxInt64 remains safe.
func RoundToWhole(minor int64) (int64, error) {
	if minor < 0 {
		return 0, errors.New("money: negative amount cannot be rounded")
	}
	whole, remainder := minor/100, minor%100
	if remainder >= 50 {
		whole++
	}
	if whole > math.MaxInt64/100 {
		return 0, errors.New("money: rounded amount overflows minor units")
	}
	return whole * 100, nil
}
