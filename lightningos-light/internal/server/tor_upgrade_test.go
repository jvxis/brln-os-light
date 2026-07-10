package server

import (
	"strings"
	"testing"
)

func TestExtractTorVersion(t *testing.T) {
	tests := map[string]string{
		"runtime output":  "Tor version 0.4.9.11.\n",
		"debian package":  "0.4.9.11-1~noble+1",
		"older package":   "0.4.8.17-1~noble+1",
		"missing version": "",
	}
	for name, input := range tests {
		name := name
		input := input
		t.Run(name, func(t *testing.T) {
			want := ""
			switch name {
			case "runtime output", "debian package":
				want = "0.4.9.11"
			case "older package":
				want = "0.4.8.17"
			}
			if got := extractTorVersion(input); got != want {
				t.Fatalf("extractTorVersion(%q) = %q, want %q", input, got, want)
			}
		})
	}
}

func TestTorVersionNewer(t *testing.T) {
	tests := []struct {
		name      string
		current   string
		candidate string
		want      bool
	}{
		{name: "security patch update", current: "0.4.9.8", candidate: "0.4.9.11", want: true},
		{name: "series update", current: "0.4.8.17", candidate: "0.4.9.11", want: true},
		{name: "same version", current: "0.4.9.11", candidate: "0.4.9.11", want: false},
		{name: "no downgrade", current: "0.4.9.11", candidate: "0.4.8.17", want: false},
		{name: "unknown current", current: "", candidate: "0.4.9.11", want: true},
		{name: "unknown candidate", current: "0.4.9.11", candidate: "", want: false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := torVersionNewer(tc.current, tc.candidate); got != tc.want {
				t.Fatalf("torVersionNewer(%q, %q) = %v, want %v", tc.current, tc.candidate, got, tc.want)
			}
		})
	}
}

func TestAptPolicyCandidate(t *testing.T) {
	policy := `tor:
  Installed: 0.4.9.8-1~noble+1
  Candidate: 0.4.9.11-1~noble+1
  Version table:
     0.4.9.11-1~noble+1 500
`
	if got := aptPolicyCandidate(policy); got != "0.4.9.11-1~noble+1" {
		t.Fatalf("aptPolicyCandidate() = %q", got)
	}
	if got := aptPolicyCandidate("Candidate: (none)\n"); got != "" {
		t.Fatalf("aptPolicyCandidate(none) = %q", got)
	}
}

func TestTorUpgradeScriptForcesStableAptLocale(t *testing.T) {
	for _, declaration := range []string{"export LC_ALL=C", "export LANG=C"} {
		if !strings.Contains(embeddedTorUpgradeScript, declaration) {
			t.Fatalf("Tor upgrade script must contain %q", declaration)
		}
	}
}
