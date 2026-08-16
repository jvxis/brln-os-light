package system

import (
	"slices"
	"testing"
)

func TestJournalTailArgsUsesStableTimestampFormat(t *testing.T) {
	got := journalTailArgs("lightningos-app-upgrade", 25, "2026-08-16T10:05:00-03:00")
	want := []string{
		"-u", "lightningos-app-upgrade",
		"-n", "25",
		"--no-pager",
		"--output=short-iso",
		"--since", "2026-08-16T10:05:00-03:00",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("journalTailArgs() = %v, want %v", got, want)
	}
}

func TestJournalTailArgsUsesBoundedDefault(t *testing.T) {
	got := journalTailArgs("lnd", 0, "")
	want := []string{"-u", "lnd", "-n", "200", "--no-pager", "--output=short-iso"}
	if !slices.Equal(got, want) {
		t.Fatalf("journalTailArgs() = %v, want %v", got, want)
	}
}
