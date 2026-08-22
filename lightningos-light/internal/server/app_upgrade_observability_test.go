package server

import (
	"slices"
	"testing"
)

func TestTransientSystemdUnitListArgsOnlyEnumeratesLoadedUnits(t *testing.T) {
	got := transientSystemdUnitListArgs(appUpgradeUnitName)
	want := []string{
		"list-units",
		"--type=service",
		"--state=active,activating,reloading,deactivating",
		"--no-legend",
		"--no-pager",
		"--plain",
		"lightningos-app-upgrade.service",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("transientSystemdUnitListArgs() = %v, want %v", got, want)
	}
	for _, arg := range got {
		if arg == "is-active" {
			t.Fatal("transient status lookup must not try to load a collected unit")
		}
	}
}

func TestTransientSystemdUnitListed(t *testing.T) {
	active := "lightningos-app-upgrade.service loaded active running LightningOS upgrade\n"
	if !transientSystemdUnitListed(active, appUpgradeUnitName) {
		t.Fatal("loaded app upgrade unit was not detected")
	}
	if transientSystemdUnitListed("", appUpgradeUnitName) {
		t.Fatal("missing transient unit must be treated as idle")
	}
	other := "lightningos-app-verify.service loaded active running LightningOS verification\n"
	if transientSystemdUnitListed(other, appUpgradeUnitName) {
		t.Fatal("a different transient unit was treated as the app upgrade")
	}
}

func TestFilterExpectedAppUpgradeJournalLines(t *testing.T) {
	lines := []string{
		"2026-08-21T11:02:53-0300 host systemd[1]: lightningos-app-upgrade.service: Failed to open /run/systemd/transient/lightningos-app-upgrade.service: No such file or directory",
		"2026-08-21T11:03:01-0300 host lightningos-upgrade-app[42]: [OK] Manager built",
		"2026-08-21T11:03:02-0300 host lightningos-upgrade-app[42]: [ERROR] UI build failed",
		"2026-08-21T11:03:03-0300 host systemd[1]: lightningos-app-upgrade.service: Failed with result 'exit-code'.",
	}
	got := filterExpectedAppUpgradeJournalLines(lines)
	want := lines[1:]
	if !slices.Equal(got, want) {
		t.Fatalf("filterExpectedAppUpgradeJournalLines() = %v, want %v", got, want)
	}
}
