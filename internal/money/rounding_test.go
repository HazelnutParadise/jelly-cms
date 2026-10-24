package money

import (
	"math"
	"testing"
)

func TestRoundToWhole(t *testing.T) {
	tests := []struct {
		minor int64
		want  int64
	}{
		{0, 0},
		{1, 0},
		{49, 0},
		{50, 100},
		{51, 100},
		{9449, 9400},
		{9450, 9500},
		{9451, 9500},
		{9500, 9500},
		{math.MaxInt64, 9223372036854775800},
		{math.MaxInt64 - 57, 9223372036854775800},
		{math.MaxInt64 - 58, 9223372036854775700},
	}
	for _, tt := range tests {
		got, err := RoundToWhole(tt.minor)
		if err != nil {
			t.Errorf("RoundToWhole(%d) error = %v", tt.minor, err)
			continue
		}
		if got != tt.want {
			t.Errorf("RoundToWhole(%d) = %d, want %d", tt.minor, got, tt.want)
		}
	}
}

func TestRoundToWholeRejectsNegativeAmounts(t *testing.T) {
	for _, minor := range []int64{-1, math.MinInt64} {
		if got, err := RoundToWhole(minor); err == nil {
			t.Errorf("RoundToWhole(%d) = %d, want an error", minor, got)
		}
	}
}

func TestFromInputKeepsFractionalPrecision(t *testing.T) {
	got, err := FromInput("1.49")
	if err != nil {
		t.Fatal(err)
	}
	if got != 149 {
		t.Fatalf("FromInput(1.49) = %d, want 149 minor units", got)
	}
}
