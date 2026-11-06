package server

import "testing"

// The UI percent-encodes the insight id ("r5:<pubkey>" → "r5%3A<pubkey>");
// chi then routes on RawPath and returns the segment still encoded. The
// handler must decode it or no stored decision is ever matched.
func TestInsightIDFromPathDecodesEncodedColon(t *testing.T) {
	cases := map[string]string{
		"r5%3A03a93b87":    "r5:03a93b87",
		"r1%3Aabc%3A2":     "r1:abc:2",
		"r5:03a93b87":      "r5:03a93b87",
		"  r7:deadbeef:1 ": "r7:deadbeef:1",
		"":                 "",
		"r5%ZZbroken":      "r5%ZZbroken", // invalid escape: kept as-is
	}
	for in, want := range cases {
		if got := insightIDFromPath(in); got != want {
			t.Fatalf("insightIDFromPath(%q) = %q, want %q", in, got, want)
		}
	}
}
