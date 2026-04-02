package server

import "testing"

func TestNormalizeGraphExplorerCloseType(t *testing.T) {
	tests := map[string]string{
		"":                   "unknown",
		"COOPERATIVE_CLOSE":  "mutual_close",
		"mutual":             "mutual_close",
		"LOCAL_FORCE_CLOSE":  "force_close",
		"REMOTE_FORCE_CLOSE": "force_close",
		"BREACH_CLOSE":       "breach_close",
		"FUNDING_CANCELED":   "funding_canceled",
		"ABANDONED":          "abandoned",
		"something_custom":   "something_custom",
	}

	for input, expected := range tests {
		if got := normalizeGraphExplorerCloseType(input); got != expected {
			t.Fatalf("normalizeGraphExplorerCloseType(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestGraphExplorerShortChannelID(t *testing.T) {
	const channelID uint64 = 1036267719070122000
	if got, want := graphExplorerShortChannelID(channelID), "942480x1889x16"; got != want {
		t.Fatalf("graphExplorerShortChannelID(%d) = %q, want %q", channelID, got, want)
	}
}
