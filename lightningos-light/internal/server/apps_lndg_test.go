package server

import (
	"net"
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

func TestLNDgHostIPsUseLocalInterfacesWithoutCommandExecution(t *testing.T) {
	interfaces := []lndgHostInterface{
		{name: "lo", flags: net.FlagUp | net.FlagLoopback, addrs: []string{"127.0.0.1/8"}},
		{name: "enp1s0", flags: net.FlagUp, addrs: []string{"192.168.68.92/24", "fe80::1/64"}},
		{name: "tailscale0", flags: net.FlagUp, addrs: []string{"100.101.102.103/32"}},
		{name: "docker0", flags: net.FlagUp, addrs: []string{"172.17.0.1/16"}},
		{name: "br-app", flags: net.FlagUp, addrs: []string{"172.20.0.1/16"}},
		{name: "wlan0", flags: 0, addrs: []string{"10.0.0.2/24"}},
	}
	got := strings.Join(lndgHostIPsFromInterfaces(interfaces), ",")
	if got != "100.101.102.103,192.168.68.92" {
		t.Fatalf("unexpected LNDg host IPs: %q", got)
	}
	hosts, origins := lndgHosts(lndgHostIPsFromInterfaces(interfaces))
	if strings.Contains(strings.Join(hosts, ","), "*") {
		t.Fatal("LNDg allowed hosts must remain closed")
	}
	for _, required := range []string{"192.168.68.92", "100.101.102.103", "http://192.168.68.92:8889", "https://100.101.102.103:8889"} {
		if !strings.Contains(strings.Join(append(hosts, origins...), ","), required) {
			t.Fatalf("LNDg access list is missing %q", required)
		}
	}
}
