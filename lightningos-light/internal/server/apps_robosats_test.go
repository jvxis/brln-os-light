package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/system"
)

func TestRoboSatsStartAndStopEnforceUseBroker(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "enforce"}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })
	server := &Server{}

	if err := server.startRobosats(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.appCalls != 1 || client.appID != appmanifest.RoboSatsID || client.action != "start" || client.dryRun {
		t.Fatalf("unexpected start broker call: %#v", client)
	}
	if err := server.stopRobosats(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.appCalls != 2 || client.appID != appmanifest.RoboSatsID || client.action != "stop" || client.dryRun {
		t.Fatalf("unexpected stop broker call: %#v", client)
	}
}

func TestRoboSatsLifecycleEnforceFailsClosed(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "enforce", lifecycleErr: errors.New("rejected")}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })
	server := &Server{}

	if err := server.startRobosats(context.Background()); err == nil {
		t.Fatal("expected broker rejection to fail start")
	}
	if err := server.stopRobosats(context.Background()); err == nil {
		t.Fatal("expected broker rejection to fail stop")
	}
	if client.appCalls != 2 {
		t.Fatalf("unexpected broker calls: %#v", client)
	}
}

func TestRobosatsComposeContentsUsesPinnedTorImageAndVolumes(t *testing.T) {
	got := robosatsComposeContents(robosatsPaths{
		DataDir:       "/tmp/robosats-data",
		CaddyfilePath: "/tmp/robosats/Caddyfile",
		TLSDir:        "/tmp/robosats/tls",
	})

	checks := []string{
		// Pinned to the self-hosted-working release, not :latest (v0.8.5 regressed).
		"image: recksato/robosats-client:v0.8.4-alpha",
		"image: osminogin/tor-simple:0.4.9.5",
		"- tor-data:/var/lib/tor",
		"- tor-log:/var/log/tor",
		"- robosats-data:/usr/src/robosats/data",
		// The client is no longer published directly; the TLS/HTTP2 proxy fronts it.
		"image: caddy:2.8-alpine",
		"- /tmp/robosats/Caddyfile:/etc/caddy/Caddyfile:ro",
		"- /tmp/robosats/tls:/etc/caddy/tls:ro",
		"volumes:\n  robosats-data:\n  tor-data:\n  tor-log:\n  caddy-data:\n  caddy-config:\n",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Fatalf("compose output missing %q\n%s", want, got)
		}
	}
	if strings.Count(got, `- "12596:12596"`) != 1 {
		t.Fatalf("expected exactly one published port (the proxy), got:\n%s", got)
	}
}

func TestRobosatsCaddyfileServesTLSAndReverseProxies(t *testing.T) {
	got := robosatsCaddyfileContents()
	checks := []string{
		"https://:12596",
		"tls /etc/caddy/tls/server.crt /etc/caddy/tls/server.key",
		"reverse_proxy robosats:12596",
		// Dead coordinators must fail fast instead of hanging Tor for ~15s.
		"response_header_timeout 8s",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Fatalf("Caddyfile missing %q\n%s", want, got)
		}
	}
}

func TestEnsureRobosatsProxyCertWritesCertAndKey(t *testing.T) {
	dir := t.TempDir()
	paths := robosatsPaths{TLSDir: filepath.Join(dir, "tls")}
	if err := ensureRobosatsProxyCert(paths); err != nil {
		t.Fatalf("ensureRobosatsProxyCert: %v", err)
	}
	crt, err := os.ReadFile(filepath.Join(paths.TLSDir, "server.crt"))
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	if !strings.Contains(string(crt), "BEGIN CERTIFICATE") {
		t.Fatalf("cert file is not PEM:\n%s", crt)
	}
	if _, err := os.Stat(filepath.Join(paths.TLSDir, "server.key")); err != nil {
		t.Fatalf("key not written: %v", err)
	}
	// Second call must be a no-op that keeps the existing cert.
	if err := ensureRobosatsProxyCert(paths); err != nil {
		t.Fatalf("ensureRobosatsProxyCert (second call): %v", err)
	}
	crt2, err := os.ReadFile(filepath.Join(paths.TLSDir, "server.crt"))
	if err != nil {
		t.Fatalf("re-read cert: %v", err)
	}
	if string(crt) != string(crt2) {
		t.Fatalf("cert was regenerated on the second call")
	}
}
