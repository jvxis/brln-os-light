package server

import (
	"strings"
	"testing"
)

func TestRobosatsComposeContentsUsesPinnedTorImageAndVolumes(t *testing.T) {
	got := robosatsComposeContents(robosatsPaths{
		DataDir: "/tmp/robosats-data",
	})

	checks := []string{
		"image: osminogin/tor-simple:0.4.9.5",
		"- tor-data:/var/lib/tor",
		"- tor-log:/var/log/tor",
		"- /tmp/robosats-data:/usr/src/robosats/data",
		"volumes:\n  tor-data:\n  tor-log:\n",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Fatalf("compose output missing %q\n%s", want, got)
		}
	}
}
