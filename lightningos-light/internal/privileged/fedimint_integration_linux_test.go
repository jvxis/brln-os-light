//go:build linux

package privileged

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/appmanifest"
)

// TestFedimintFunctionalGate is an opt-in destructive test for a disposable
// Linux host. It materializes both catalog Compose projects, verifies their
// real Docker security configuration, and executes the pinned upstream
// binaries within the same closed non-root runtime constraints.
func TestFedimintFunctionalGate(t *testing.T) {
	if os.Getenv("LIGHTNINGOS_FEDIMINT_FUNCTIONAL_GATE") != "1" {
		t.Skip("set LIGHTNINGOS_FEDIMINT_FUNCTIONAL_GATE=1 on a disposable Linux host")
	}
	if os.Geteuid() != 0 {
		t.Fatal("Fedimint functional gate requires root on a disposable host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	runner := &ExecCommandRunner{}
	for _, resource := range []string{
		appmanifest.FedimintGuardianID + "-" + appmanifest.FedimintGuardianPrimaryService + "-1",
		appmanifest.FedimintGatewayID + "-" + appmanifest.FedimintGatewayPrimaryService + "-1",
	} {
		if _, err := runner.Run(ctx, dockerPath, "container", "inspect", resource); err == nil {
			t.Fatalf("functional gate resource %s already exists", resource)
		}
	}
	cleanup := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		for _, container := range []string{
			appmanifest.FedimintGuardianID + "-" + appmanifest.FedimintGuardianPrimaryService + "-1",
			appmanifest.FedimintGatewayID + "-" + appmanifest.FedimintGatewayPrimaryService + "-1",
		} {
			_, _ = runner.Run(cleanupCtx, dockerPath, "rm", "-f", container)
		}
		_, _ = runner.Run(cleanupCtx, dockerPath, "network", "rm", appmanifest.FedimintGuardianNetwork, appmanifest.FedimintGatewayNetwork)
		_, _ = runner.Run(cleanupCtx, dockerPath, "image", "rm", appmanifest.FedimintGuardianImage, appmanifest.FedimintGatewayImage)
	}
	t.Cleanup(cleanup)

	stateRoot := t.TempDir()
	appsRoot := filepath.Join(stateRoot, "manager-apps")
	dataRoot := filepath.Join(stateRoot, "manager-data")
	privilegedRoot := filepath.Join(stateRoot, "broker-apps")
	lndRoot := filepath.Join(stateRoot, "lnd")
	if err := os.MkdirAll(filepath.Join(lndRoot, "data", "chain", "bitcoin", "mainnet"), 0700); err != nil {
		t.Fatal(err)
	}
	certificate := testLNDgCertificate(t, "host.docker.internal")
	if err := os.WriteFile(filepath.Join(lndRoot, "tls.cert"), certificate, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lndRoot, "data", "chain", "bitcoin", "mainnet", "admin.macaroon"), []byte("native-admin"), 0600); err != nil {
		t.Fatal(err)
	}
	manager := &ComposeAppManager{
		Runner: runner, AppsRoot: appsRoot, AppsDataRoot: dataRoot,
		PrivilegedAppsRoot: privilegedRoot, LNDDataRoot: lndRoot,
	}

	guardianRuntime := appmanifest.FedimintGuardianRuntime{Bitcoin: appmanifest.FedimintBitcoinRuntime{
		Mode: appmanifest.FedimintBitcoinModeRemote, URL: "http://127.0.0.1:8332", User: "gate-user", Pass: "gate-pass",
	}}
	gatewayRuntime := appmanifest.FedimintGatewayRuntime{
		Bitcoin:             guardianRuntime.Bitcoin,
		GatewayPasswordHash: "$2a$10$abcdefghijklmnopqrstuuuuuuuuuuuuuuuuuuuuuuuuuuuuuu",
	}
	for _, appID := range []string{appmanifest.FedimintGuardianID, appmanifest.FedimintGatewayID} {
		appRoot := filepath.Join(appsRoot, appID)
		if err := os.MkdirAll(appRoot, 0700); err != nil {
			t.Fatal(err)
		}
		var runtimeRaw []byte
		var compose string
		if appID == appmanifest.FedimintGuardianID {
			runtimeRaw, _ = appmanifest.FedimintGuardianRuntimeJSON(guardianRuntime)
			compose, _ = appmanifest.FedimintGuardianCompose(guardianRuntime)
		} else {
			runtimeRaw, _ = appmanifest.FedimintGatewayRuntimeJSON(gatewayRuntime)
			compose, _ = appmanifest.FedimintGatewayCompose(gatewayRuntime)
		}
		composeName := appmanifest.FedimintGuardianComposeFile
		runtimeName := appmanifest.FedimintGuardianRuntimeFile
		if appID == appmanifest.FedimintGatewayID {
			composeName, runtimeName = appmanifest.FedimintGatewayComposeFile, appmanifest.FedimintGatewayRuntimeFile
		}
		if err := os.WriteFile(filepath.Join(appRoot, composeName), []byte(compose), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(appRoot, runtimeName), runtimeRaw, 0600); err != nil {
			t.Fatal(err)
		}
		if appID == appmanifest.FedimintGatewayID {
			if err := os.WriteFile(filepath.Join(appRoot, appmanifest.FedimintGatewayTLSFile), certificate, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(appRoot, appmanifest.FedimintGatewayMacaroonFile), []byte("dedicated-gateway"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := manager.prepareFedimintData(appID); err != nil {
			t.Fatal(err)
		}
		files, err := manager.validatedFedimintFiles(appID)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, _, err := manager.createFedimintSnapshot(appID, files)
		if err != nil {
			t.Fatal(err)
		}
		commandPath, prefix, err := manager.resolveCompose(ctx)
		if err != nil {
			t.Fatal(err)
		}
		manifest, _ := appmanifest.ComposeManifestForApp(appID)
		base := append([]string(nil), prefix...)
		base = append(base, "--env-file", snapshot.envPath, "--project-name", manifest.Project, "--project-directory", snapshot.root, "-f", snapshot.composePath)
		if _, err := runner.Run(ctx, commandPath, append(append([]string(nil), base...), "create")...); err != nil {
			t.Fatalf("%s catalog container creation failed: %v", appID, err)
		}
		container := appID + "-" + manifest.PrimaryService + "-1"
		inspect, err := runner.Run(ctx, dockerPath, "inspect", "--format", `{"user":{{json .Config.User}},"readonly":{{json .HostConfig.ReadonlyRootfs}},"capdrop":{{json .HostConfig.CapDrop}},"security":{{json .HostConfig.SecurityOpt}}}`, container)
		if err != nil {
			t.Fatal(err)
		}
		var security struct {
			User     string   `json:"user"`
			ReadOnly bool     `json:"readonly"`
			CapDrop  []string `json:"capdrop"`
			Security []string `json:"security"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(inspect)), &security); err != nil {
			t.Fatal(err)
		}
		if security.User != "1000:1000" || !security.ReadOnly || !containsString(security.CapDrop, "ALL") || !containsString(security.Security, "no-new-privileges:true") {
			t.Fatalf("%s runtime is not hardened: %#v", appID, security)
		}
		binary := "fedimintd"
		expected := "fedimintd 0.11.1"
		if appID == appmanifest.FedimintGatewayID {
			binary, expected = "gatewayd", "fedimint-gateway-server 0.11.1"
		}
		runArgs := append(append([]string(nil), base...), "run", "--rm", "--no-deps", "--entrypoint", binary, manifest.PrimaryService, "--version")
		version, err := runner.Run(ctx, commandPath, runArgs...)
		if err != nil || strings.TrimSpace(version) != expected {
			t.Fatalf("%s closed runtime probe failed: %q/%v", appID, version, err)
		}
		t.Logf("%s_runtime=passed version=%s readonly=true uid=1000 capdrop=ALL no_new_privileges=true", appID, strings.TrimSpace(version))
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
