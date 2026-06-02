package server

import (
	"strings"
	"testing"
)

func TestSanitizeLogLineStripsFedimintANSI(t *testing.T) {
	raw := "fedimintd-1  | \x1b[2m2026-06-02T18:01:13.718795Z\x1b[0m \x1b[32m INFO\x1b[0m \x1b[1mrun\x1b[0m\x1b[1m{\x1b[0m\x1b[3mid\x1b[0m\x1b[2m=\x1b[0m0\x1b[1m}\x1b[0m\x1b[2m:\x1b[0m \x1b[2mfm::consensus\x1b[0m\x1b[2m:\x1b[0m Session 22522 completed"

	got := sanitizeLogLine(raw)

	if strings.Contains(got, "\x1b") || strings.Contains(got, "[32m") || strings.Contains(got, "[0m") {
		t.Fatalf("sanitizeLogLine left ANSI escapes in %q", got)
	}
	for _, want := range []string{
		"fedimintd-1",
		"2026-06-02T18:01:13.718795Z",
		"INFO",
		"run{id=0}:",
		"fm::consensus:",
		"Session 22522 completed",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("sanitizeLogLine() missing %q in %q", want, got)
		}
	}
}
