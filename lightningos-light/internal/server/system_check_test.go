package server

import (
	"strings"
	"testing"
)

func TestWorstSystemCheckTone(t *testing.T) {
	tests := []struct {
		name     string
		statuses []systemCheckTone
		want     systemCheckTone
	}{
		{name: "empty is muted", want: systemCheckMuted},
		{name: "ok beats muted", statuses: []systemCheckTone{systemCheckMuted, systemCheckOK}, want: systemCheckOK},
		{name: "warn beats ok", statuses: []systemCheckTone{systemCheckOK, systemCheckWarn}, want: systemCheckWarn},
		{name: "danger beats warn", statuses: []systemCheckTone{systemCheckWarn, systemCheckDanger, systemCheckOK}, want: systemCheckDanger},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := worstSystemCheckTone(tt.statuses...); got != tt.want {
				t.Fatalf("worstSystemCheckTone() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSystemCheckOverallStatus(t *testing.T) {
	tests := []struct {
		name   string
		groups []systemCheckGroup
		want   string
	}{
		{name: "all ok", groups: []systemCheckGroup{{Status: systemCheckOK}, {Status: systemCheckMuted}}, want: "OK"},
		{name: "warn", groups: []systemCheckGroup{{Status: systemCheckOK}, {Status: systemCheckWarn}}, want: "WARN"},
		{name: "danger", groups: []systemCheckGroup{{Status: systemCheckWarn}, {Status: systemCheckDanger}}, want: "ERR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := systemCheckOverallStatus(tt.groups); got != tt.want {
				t.Fatalf("systemCheckOverallStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSystemCheckSummary(t *testing.T) {
	tests := []struct {
		name  string
		items []systemCheckItem
		want  string
	}{
		{
			name:  "danger summary",
			items: []systemCheckItem{{Status: systemCheckDanger}, {Status: systemCheckWarn}},
			want:  "1 failing check(s)",
		},
		{
			name:  "warn summary",
			items: []systemCheckItem{{Status: systemCheckOK}, {Status: systemCheckWarn}},
			want:  "1 warning check(s)",
		},
		{
			name:  "ok summary",
			items: []systemCheckItem{{Status: systemCheckOK}, {Status: systemCheckMuted}},
			want:  "All checks passed",
		},
		{
			name:  "muted summary",
			items: []systemCheckItem{{Status: systemCheckMuted}},
			want:  "No active checks",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := systemCheckSummary(tt.items); got != tt.want {
				t.Fatalf("systemCheckSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStableID(t *testing.T) {
	got := stableID("LightningOS", "main db")
	if got != "lightningos_main_db" {
		t.Fatalf("stableID() = %q, want lightningos_main_db", got)
	}
}

func TestRedactSystemCheckLogLines(t *testing.T) {
	lines := redactSystemCheckLogLines([]string{
		"postgres://los:supersecret@127.0.0.1:5432/lightningos",
		"BITCOIN_RPC_PASS=abc123 token: xyz",
		"Authorization: Basic dXNlcjpwYXNz",
	})

	joined := strings.Join(lines, "\n")
	for _, leaked := range []string{"supersecret", "abc123", "xyz", "dXNlcjpwYXNz"} {
		if strings.Contains(joined, leaked) {
			t.Fatalf("redacted logs leaked %q in %q", leaked, joined)
		}
	}
	if !strings.Contains(joined, "[redacted]") {
		t.Fatalf("expected redaction marker in %q", joined)
	}
}
