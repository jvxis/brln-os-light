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
		"--since", "2026-08-16 13:05:00 UTC",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("journalTailArgs() = %v, want %v", got, want)
	}
}

func TestJournalTailArgsNormalizesBrowserTimesWithoutChangingRelativeFilters(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"2026-09-12T00:00:00.000Z", "2026-09-12 00:00:00 UTC"},
		{"2026-09-12T10:11:49.123Z", "2026-09-12 10:11:49.123 UTC"},
		{"2026-09-12T00:01:00.123456789+03:00", "2026-09-11 21:01:00.123456 UTC"},
		{" 2026-09-12T00:00:00Z ", "2026-09-12 00:00:00 UTC"},
		{"-15 minutes", "-15 minutes"},
		{"2026-09-12 00:00:00 UTC", "2026-09-12 00:00:00 UTC"},
		{"invalid-date", "invalid-date"},
	} {
		for _, service := range []string{"lightningos-tor-upgrade", "lightningos-app-upgrade", "lightningos-lnd-upgrade", "lnd"} {
			t.Run(service+"/"+tc.input, func(t *testing.T) {
				args := journalTailArgs(service, 25, tc.input)
				if args[len(args)-2] != "--since" || args[len(args)-1] != tc.want {
					t.Fatalf("args = %v, want filter %q", args, tc.want)
				}
			})
		}
	}
}

func TestJournalTailArgsUsesBoundedDefault(t *testing.T) {
	got := journalTailArgs("lnd", 0, "")
	want := []string{"-u", "lnd", "-n", "200", "--no-pager", "--output=short-iso"}
	if !slices.Equal(got, want) {
		t.Fatalf("journalTailArgs() = %v, want %v", got, want)
	}
}
