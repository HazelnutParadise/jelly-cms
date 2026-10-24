package money

import "testing"

// Money contract (ticket 07, E1). Amounts are stored in minor units (分, int64). Format renders NT$ with
// thousands separators, FromInput reads whole or decimal NT$ typed by an admin, and ToGateway hands whole
// dollars to a payment gateway. These tests are written before internal/money is implemented and must
// fail until it is. Do not edit them to match an implementation.

func TestFormatTWD(t *testing.T) {
	cases := []struct {
		name  string
		minor int64
		want  string
	}{
		{"whole dollars", 12000, "NT$120"},
		{"thousands separator and cents", 123456789, "NT$1,234,567.89"},
		{"zero", 0, "NT$0"},
		{"negative", -500, "-NT$5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Format(tc.minor, "TWD"); got != tc.want {
				t.Fatalf("Format(%d, TWD) = %q, want %q", tc.minor, got, tc.want)
			}
		})
	}
}

func TestFromInput(t *testing.T) {
	valid := []struct {
		input string
		want  int64
	}{
		{"120", 12000},
		{"1,200", 120000},
		{"12.5", 1250},
	}
	for _, tc := range valid {
		got, err := FromInput(tc.input)
		if err != nil {
			t.Errorf("FromInput(%q) returned error %v, want %d", tc.input, err, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("FromInput(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
	for _, input := range []string{"abc", "-1"} {
		if got, err := FromInput(input); err == nil {
			t.Errorf("FromInput(%q) = %d with no error, want an error", input, got)
		}
	}
}

func TestToGateway(t *testing.T) {
	got, err := ToGateway(12000)
	if err != nil || got != 120 {
		t.Fatalf("ToGateway(12000) = %d, %v; want 120, nil", got, err)
	}
	if _, err := ToGateway(12050); err == nil {
		t.Fatal("ToGateway(12050) returned no error; a fractional NT$ amount must be refused, not rounded")
	}
	if got, err := ToGateway(0); err != nil || got != 0 {
		t.Fatalf("ToGateway(0) = %d, %v; want 0, nil", got, err)
	}
}

// panicsOn reports whether f panics.
func panicsOn(f func()) (panicked bool) {
	defer func() { panicked = recover() != nil }()
	f()
	return false
}
