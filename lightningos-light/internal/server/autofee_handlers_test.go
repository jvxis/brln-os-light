package server

import "testing"

func TestAutofeeRefreshAuditTarget(t *testing.T) {
	tests := []struct {
		name         string
		channelPoint string
		channelID    uint64
		wantTarget   string
		wantScope    string
	}{
		{name: "all channels", wantTarget: "all", wantScope: "all"},
		{name: "channel point", channelPoint: "  abc:1  ", channelID: 42, wantTarget: "abc:1", wantScope: "channel"},
		{name: "channel id", channelID: 42, wantTarget: "42", wantScope: "channel"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target, scope := autofeeRefreshAuditTarget(tc.channelPoint, tc.channelID)
			if target != tc.wantTarget || scope != tc.wantScope {
				t.Fatalf("autofeeRefreshAuditTarget() = (%q, %q), want (%q, %q)", target, scope, tc.wantTarget, tc.wantScope)
			}
		})
	}
}

func TestAutofeeRefreshAuditChannelIDPreservesUint64(t *testing.T) {
	const channelID = uint64(18_446_744_073_709_551_615)
	if got := autofeeRefreshAuditChannelID(channelID); got != "18446744073709551615" {
		t.Fatalf("autofeeRefreshAuditChannelID() = %q", got)
	}
	if got := autofeeRefreshAuditChannelID(0); got != "" {
		t.Fatalf("zero channel id = %q, want empty", got)
	}
}
