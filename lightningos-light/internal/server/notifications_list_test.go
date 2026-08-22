package server

import (
	"net/url"
	"reflect"
	"testing"
	"time"
)

func TestParseNotificationListOptionsDefaults(t *testing.T) {
	opts, err := parseNotificationListOptions(url.Values{}, time.Now())
	if err != nil {
		t.Fatalf("parse defaults: %v", err)
	}
	if opts.Limit != 200 || opts.Range != "all" || opts.Type != "all" || opts.Outcome != "all" {
		t.Fatalf("unexpected defaults: %+v", opts)
	}
	if !opts.Start.IsZero() || opts.Cursor != nil || opts.HideFailedOutgoing {
		t.Fatalf("unexpected optional defaults: %+v", opts)
	}
}

func TestParseNotificationListOptionsFilters(t *testing.T) {
	now := time.Date(2026, time.August, 22, 12, 30, 0, 0, time.UTC)
	cursorTime := now.Add(-2 * time.Hour)
	values := url.Values{
		"limit":                {"75"},
		"range":                {"3m"},
		"type":                 {"lightning"},
		"outcome":              {"failed"},
		"hide_failed_outgoing": {"true"},
		"cursor":               {encodeNotificationListCursor(cursorTime, 42)},
	}
	opts, err := parseNotificationListOptions(values, now)
	if err != nil {
		t.Fatalf("parse filters: %v", err)
	}
	if opts.Limit != 75 || opts.Range != "3m" || opts.Type != "lightning" || opts.Outcome != "failed" || !opts.HideFailedOutgoing {
		t.Fatalf("unexpected filters: %+v", opts)
	}
	if want := now.AddDate(0, -3, 0); !opts.Start.Equal(want) {
		t.Fatalf("start = %s, want %s", opts.Start, want)
	}
	if opts.Cursor == nil || opts.Cursor.ID != 42 || !opts.Cursor.OccurredAt.Equal(cursorTime) {
		t.Fatalf("unexpected cursor: %+v", opts.Cursor)
	}
}

func TestParseNotificationListOptionsAcceptsPortugueseYearAlias(t *testing.T) {
	now := time.Date(2026, time.August, 22, 12, 30, 0, 0, time.UTC)
	opts, err := parseNotificationListOptions(url.Values{"range": {"1a"}}, now)
	if err != nil {
		t.Fatalf("parse 1a alias: %v", err)
	}
	if opts.Range != "1y" || !opts.Start.Equal(now.AddDate(-1, 0, 0)) {
		t.Fatalf("unexpected one-year range: %+v", opts)
	}
}

func TestParseNotificationListOptionsRejectsInvalidValues(t *testing.T) {
	tests := []url.Values{
		{"limit": {"0"}},
		{"limit": {"1001"}},
		{"limit": {"many"}},
		{"range": {"2m"}},
		{"type": {"wallet"}},
		{"outcome": {"blocked"}},
		{"hide_failed_outgoing": {"sometimes"}},
		{"cursor": {"invalid"}},
		{"cursor": {"0:1"}},
		{"cursor": {"1:0"}},
	}
	for _, values := range tests {
		if _, err := parseNotificationListOptions(values, time.Now()); err == nil {
			t.Fatalf("expected error for %v", values)
		}
	}
}

func TestNotificationOutcomeStatuses(t *testing.T) {
	if got := notificationOutcomeStatuses("failed"); !reflect.DeepEqual(got, []string{"FAILED", "ERROR"}) {
		t.Fatalf("failed statuses = %v", got)
	}
	if got := notificationOutcomeStatuses("pending"); len(got) == 0 {
		t.Fatal("pending statuses must not be empty")
	}
	if got := notificationOutcomeStatuses("all"); got != nil {
		t.Fatalf("all statuses = %v, want nil", got)
	}
}
