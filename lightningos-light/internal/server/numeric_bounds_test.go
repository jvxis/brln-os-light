package server

import "testing"

func TestParseNonNegativeInt32EnforcesBounds(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want int32
		ok   bool
	}{
		{raw: "0", want: 0, ok: true},
		{raw: "2147483647", want: 2147483647, ok: true},
		{raw: "-1", ok: false},
		{raw: "2147483648", ok: false},
		{raw: "4294967295", ok: false},
		{raw: "not-a-number", ok: false},
	} {
		got, ok := parseNonNegativeInt32(test.raw)
		if ok != test.ok || got != test.want {
			t.Fatalf("parseNonNegativeInt32(%q) = (%d, %v), want (%d, %v)", test.raw, got, ok, test.want, test.ok)
		}
	}
}
