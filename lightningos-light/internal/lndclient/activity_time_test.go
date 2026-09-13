package lndclient

import (
	"testing"
	"time"
)

func TestOnchainActivityTimestamp(t *testing.T) {
	now := time.Date(2026, 9, 11, 22, 23, 0, 0, time.UTC)
	start := now.Add(-7 * 24 * time.Hour)
	for _, tc := range []struct {
		name       string
		stamp, end time.Time
		want       bool
	}{
		{"known transaction ahead of clock", now.Add(89 * time.Second), now, true},
		{"current", now, now, true},
		{"old", start.Add(-time.Second), now, false},
		{"extreme future", now.Add(3 * time.Hour), now, false},
		{"historical end", now.Add(-time.Hour), now.Add(-2 * time.Hour), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := onchainActivityTimeInRange(tc.stamp, start, tc.end, now); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
