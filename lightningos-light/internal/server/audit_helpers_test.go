package server

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuditSessionIDFromAuthContext(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/onchain/utxos/lock", nil)
	req = req.WithContext(context.WithValue(req.Context(), authSessionContextKey, authSessionSnapshot{ID: "sess_123"}))

	if got := auditSessionID(req); got != "sess_123" {
		t.Fatalf("expected session id, got %q", got)
	}
}

func TestAuditClientIPPrefersForwardedFor(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/onchain/utxos/lock", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.2")

	if got := auditClientIP(req); got != "203.0.113.7" {
		t.Fatalf("expected forwarded ip, got %q", got)
	}
}

func TestAuditTargetForOutpoints(t *testing.T) {
	if got := auditTargetForOutpoints([]string{" ABC:1 ", "abc:1"}); got != "abc:1" {
		t.Fatalf("expected single normalized outpoint, got %q", got)
	}
	if got := auditTargetForOutpoints([]string{"abc:1", "def:2"}); got != "batch:2" {
		t.Fatalf("expected batch target, got %q", got)
	}
}

func TestAuditStatusFromResult(t *testing.T) {
	if got := auditStatusFromResult(2, 0); got != "success" {
		t.Fatalf("expected success, got %q", got)
	}
	if got := auditStatusFromResult(2, 1); got != "partial_error" {
		t.Fatalf("expected partial_error, got %q", got)
	}
	if got := auditStatusFromResult(2, 2); got != "error" {
		t.Fatalf("expected error, got %q", got)
	}
}

func TestParseAuditEventsLimit(t *testing.T) {
	tests := []struct {
		raw     string
		want    int
		wantErr bool
	}{
		{raw: "", want: auditEventsDefaultLimit},
		{raw: "25", want: 25},
		{raw: "0", want: auditEventsDefaultLimit},
		{raw: "999", want: auditEventsMaxLimit},
		{raw: "-1", wantErr: true},
		{raw: "bad", wantErr: true},
	}

	for _, tc := range tests {
		got, err := parseAuditEventsLimit(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("expected parseAuditEventsLimit(%q) to fail", tc.raw)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("parseAuditEventsLimit(%q) = (%d, %v), want (%d, nil)", tc.raw, got, err, tc.want)
		}
	}
}

func TestParseAuditEventsRetentionDays(t *testing.T) {
	tests := []struct {
		raw  string
		want int
	}{
		{raw: "", want: auditEventsDefaultRetentionDays},
		{raw: "365", want: 365},
		{raw: "0", want: 0},
		{raw: "forever", want: 0},
		{raw: "keep_forever", want: 0},
		{raw: "keep-forever", want: 0},
		{raw: "-1", want: auditEventsDefaultRetentionDays},
		{raw: "bad", want: auditEventsDefaultRetentionDays},
	}

	for _, tc := range tests {
		if got := parseAuditEventsRetentionDays(tc.raw); got != tc.want {
			t.Fatalf("parseAuditEventsRetentionDays(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestAuditEventsRetentionCutoff(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 30, 0, 0, time.FixedZone("BRT", -3*60*60))
	cutoff, ok := auditEventsRetentionCutoff(now, 365)
	if !ok {
		t.Fatalf("expected retention cutoff")
	}
	want := now.UTC().AddDate(0, 0, -365)
	if !cutoff.Equal(want) {
		t.Fatalf("cutoff = %s, want %s", cutoff, want)
	}

	if _, ok := auditEventsRetentionCutoff(now, 0); ok {
		t.Fatalf("expected disabled retention for zero days")
	}
}

func TestUtxoLockRequiresReauthEnv(t *testing.T) {
	t.Setenv(utxoLockRequiresReauthEnv, "")
	if utxoLockRequiresReauth() {
		t.Fatalf("expected empty env to keep reauth disabled")
	}

	t.Setenv(utxoLockRequiresReauthEnv, "true")
	if !utxoLockRequiresReauth() {
		t.Fatalf("expected true env to enable reauth")
	}

	t.Setenv(utxoLockRequiresReauthEnv, "0")
	if utxoLockRequiresReauth() {
		t.Fatalf("expected false-like env to disable reauth")
	}
}
