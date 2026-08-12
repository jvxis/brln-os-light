package server

import (
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func TestLndgBuildUsesCompatiblePinnedUpstream(t *testing.T) {
	dockerfile := appmanifest.LNDgDockerfile()
	if !strings.Contains(dockerfile, "FROM python:3.12-slim") {
		t.Fatal("LNDg Django 6 build must use Python 3.12")
	}
	if len(appmanifest.LNDgSourceCommit) != 40 {
		t.Fatalf("LNDg source commit must be pinned, got %q", appmanifest.LNDgSourceCommit)
	}
	if strings.Contains(lndgComposeContents(lndgAppPaths()), "build:") {
		t.Fatal("LNDg runtime declaration must not retain a manager-owned build path")
	}
}

func TestLNDgPostgresBackedLNDUsesPrivateChannelDBPlaceholder(t *testing.T) {
	paths := lndgPaths{ChannelDBPath: "/var/lib/lightningos/apps-data/lndg/lnd/channel.db"}
	if got := lndgChannelDBSource(paths); got != paths.ChannelDBPath {
		t.Fatalf("channel DB source=%q want=%q", got, paths.ChannelDBPath)
	}
}
