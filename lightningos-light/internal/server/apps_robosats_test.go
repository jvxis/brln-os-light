package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	if client.appCalls != 1 || client.appID != appmanifest.RoboSatsID || client.action != "start" || client.dryRun || client.firewallCalls != 1 || client.firewallAppID != appmanifest.RoboSatsID || client.firewallDryRun {
		t.Fatalf("unexpected start broker call: %#v", client)
	}
	if err := server.stopRobosats(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.appCalls != 2 || client.appID != appmanifest.RoboSatsID || client.action != "stop" || client.dryRun {
		t.Fatalf("unexpected stop broker call: %#v", client)
	}
}

func TestEnsureRoboSatsUFWAccessEnforceUsesClosedBrokerApp(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "enforce", firewallStatus: "active"}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := ensureRobosatsUfwAccess(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.firewallCalls != 1 || client.firewallAppID != appmanifest.RoboSatsID || client.firewallDryRun {
		t.Fatalf("unexpected firewall broker call: %#v", client)
	}
}

func TestEnsureRoboSatsUFWAccessEnforceFailsClosed(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "enforce", firewallErr: errors.New("ufw failed")}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := ensureRobosatsUfwAccess(context.Background()); err == nil {
		t.Fatal("expected firewall broker failure")
	}
	if client.firewallCalls != 1 {
		t.Fatalf("unexpected firewall broker calls: %#v", client)
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

func TestRemoveRoboSatsAppEnforceUsesBrokerBeforeDeletingFiles(t *testing.T) {
	root := t.TempDir()
	paths := robosatsPaths{Root: root, ComposePath: filepath.Join(root, appmanifest.RoboSatsComposeFile)}
	if err := os.WriteFile(paths.ComposePath, []byte("manager-owned"), 0600); err != nil {
		t.Fatal(err)
	}
	client := &cpuMinerPrivilegedClient{mode: "enforce"}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := removeRoboSatsApp(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	if client.removeCalls != 1 || client.removeAppID != appmanifest.RoboSatsID || client.removeDryRun {
		t.Fatalf("unexpected broker call: %#v", client)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("app files still exist or stat failed unexpectedly: %v", err)
	}
}

func TestRemoveRoboSatsAppEnforceFailsClosedAndPreservesFiles(t *testing.T) {
	root := t.TempDir()
	paths := robosatsPaths{Root: root, ComposePath: filepath.Join(root, appmanifest.RoboSatsComposeFile)}
	if err := os.WriteFile(paths.ComposePath, []byte("manager-owned"), 0600); err != nil {
		t.Fatal(err)
	}
	client := &cpuMinerPrivilegedClient{mode: "enforce", removeErr: errors.New("rejected")}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := removeRoboSatsApp(context.Background(), paths); err == nil {
		t.Fatal("expected broker rejection")
	}
	if client.removeCalls != 1 {
		t.Fatalf("unexpected broker calls: %#v", client)
	}
	if _, err := os.Stat(paths.ComposePath); err != nil {
		t.Fatalf("manager files were removed after broker failure: %v", err)
	}
}

func TestEnsureRoboSatsImagesEnforceUsesClosedBrokerVariants(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "enforce", imageStatus: "ready"}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := ensureRobosatsImages(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"client", "tor", "proxy"}
	if client.prepareCalls != 3 || !reflect.DeepEqual(client.preparedVariants, want) || client.appID != appmanifest.RoboSatsID || client.imageDryRun {
		t.Fatalf("unexpected image broker calls: %#v", client)
	}
}

func TestEnsureRoboSatsImagesEnforceFailsClosed(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "enforce", imageErr: errors.New("pull failed")}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := ensureRobosatsImages(context.Background()); err == nil {
		t.Fatal("expected image broker failure")
	}
	if client.prepareCalls != 1 || client.preparedVariants[0] != "client" {
		t.Fatalf("unexpected fail-closed sequence: %#v", client)
	}
}

func TestEnsureDockerForCatalogAppEnforceUsesPackageAndRuntimeBroker(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "enforce", dockerStatus: "ready"}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := ensureDockerForCatalogApp(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.packageCalls != 2 || client.packageFeature != "docker_runtime" || client.packageDryRun || client.dockerCalls != 1 || client.dockerDryRun {
		t.Fatalf("unexpected Docker preparation broker calls: %#v", client)
	}
}

func TestEnsureDockerForCatalogAppEnforceFailsClosed(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "enforce", packageErr: errors.New("package failed")}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := ensureDockerForCatalogApp(context.Background()); err == nil {
		t.Fatal("expected package broker failure")
	}
	if client.packageCalls != 1 || client.dockerCalls != 0 {
		t.Fatalf("package failure did not stop before runtime: %#v", client)
	}
}

func TestEnsureDockerForCatalogAppStrictRejectsLegacyFallback(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "disabled"}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := ensureDockerForCatalogAppEnforce(context.Background()); err == nil || !strings.Contains(err.Error(), "requires privileged broker enforce mode") {
		t.Fatalf("strict Docker preparation did not fail closed: %v", err)
	}
	if client.packageCalls != 0 || client.dockerCalls != 0 {
		t.Fatalf("disabled broker unexpectedly executed Docker preparation: %#v", client)
	}
}

func TestRobosatsComposeContentsUsesPinnedTorImageAndVolumes(t *testing.T) {
	got := robosatsComposeContents(robosatsPaths{
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
