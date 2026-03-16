package server

import (
	"testing"
	"time"
)

func TestSuccessionScheduleFrom(t *testing.T) {
	lastAlive := time.Date(2026, time.March, 16, 9, 10, 43, 0, time.UTC)

	next, deadline := successionScheduleFrom(lastAlive, 1, 5)

	expectedNext := time.Date(2026, time.March, 17, 9, 10, 43, 0, time.UTC)
	expectedDeadline := time.Date(2026, time.March, 22, 9, 10, 43, 0, time.UTC)

	if !next.Equal(expectedNext) {
		t.Fatalf("next_check_at mismatch: got %s want %s", next.Format(time.RFC3339), expectedNext.Format(time.RFC3339))
	}
	if !deadline.Equal(expectedDeadline) {
		t.Fatalf("deadline_at mismatch: got %s want %s", deadline.Format(time.RFC3339), expectedDeadline.Format(time.RFC3339))
	}
}
