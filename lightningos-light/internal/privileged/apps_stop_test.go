package privileged

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

// Emulates Docker-owned labels, not the potentially stale installed Compose.
func catalogStopRunner(t *testing.T, appID string, services ...string) *composeRecordingRunner {
	t.Helper()
	m, err := appmanifest.ComposeManifestForApp(appID)
	if err != nil {
		t.Fatal(err)
	}
	active := map[string]bool{}
	metadata := map[string]catalogStopContainer{}
	var ids []string
	for i, service := range services {
		id := fmt.Sprintf("%064x", i+1)
		ids = append(ids, id)
		active[id] = true
		metadata[id] = catalogStopContainer{ID: id, Labels: map[string]string{
			"com.docker.compose.project": m.Project,
			"com.docker.compose.service": service,
			"com.docker.compose.oneoff":  "False",
		}}
	}
	return &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path != dockerPath || len(args) == 0 {
			return "", nil, false
		}
		switch args[0] {
		case "ps":
			if !reflect.DeepEqual(args, []string{"ps", "--no-trunc", "--filter", "label=com.docker.compose.project=" + m.Project, "--filter", "label=com.docker.compose.oneoff=False", "--format", "{{.ID}}"}) {
				return "", nil, false
			}
			var result []string
			for _, id := range ids {
				if active[id] {
					result = append(result, id)
				}
			}
			return strings.Join(result, "\n"), nil, true
		case "inspect":
			if len(args) != 6 || args[4] != catalogStopInspectFormat {
				return "", nil, false
			}
			c, ok := metadata[args[5]]
			if !ok {
				t.Fatalf("inspection escaped app inventory: %v", args)
			}
			out, _ := json.Marshal(c)
			return string(out), nil, true
		case "stop":
			if len(args) < 4 || args[1] != "--time" || args[2] != fmt.Sprint(m.StopTimeoutSeconds) {
				t.Fatalf("unsafe stop: %v", args)
			}
			for _, id := range args[3:] {
				if !active[id] {
					t.Fatalf("stop escaped active inventory: %v", args)
				}
				delete(active, id)
			}
			return "", nil, true
		}
		return "", nil, false
	}}
}

func assertCatalogStopOnly(t *testing.T, runner *composeRecordingRunner, count int) {
	t.Helper()
	stopped := 0
	for _, command := range runner.commands {
		if command.path != dockerPath || len(command.args) == 0 {
			t.Fatalf("unexpected command: %#v", command)
		}
		switch command.args[0] {
		case "ps", "inspect":
		case "stop":
			stopped += len(command.args) - 3
		default:
			t.Fatalf("stop executed configuration/image/database command: %#v", command)
		}
	}
	if stopped != count {
		t.Fatalf("stopped %d containers, expected %d", stopped, count)
	}
}

func TestCatalogStopAllComposeAppsIndependentOfInstalledVersion(t *testing.T) {
	for _, id := range []string{appmanifest.CPUMinerID, appmanifest.RoboSatsID, appmanifest.BTCPayID,
		appmanifest.LNDgID, appmanifest.LNbitsID, appmanifest.ElectrsID, appmanifest.MempoolID,
		appmanifest.FedimintGuardianID, appmanifest.FedimintGatewayID, appmanifest.TapdID,
		appmanifest.PublicPoolID, appmanifest.BarkWalletID, appmanifest.BRLNCommunityID} {
		t.Run(id, func(t *testing.T) {
			runner := catalogStopRunner(t, id, "old-service", "old-service", "removed-sidecar")
			// No declaration, image, credentials or version fixture is necessary.
			manager := &ComposeAppManager{Runner: runner, AppsRoot: t.TempDir()}
			if err := manager.Lifecycle(context.Background(), id, AppLifecycleStop, false); err != nil {
				t.Fatal(err)
			}
			assertCatalogStopOnly(t, runner, 3)
			runner.commands = nil
			if err := manager.Lifecycle(context.Background(), id, AppLifecycleStop, false); err != nil {
				t.Fatal(err)
			}
			assertCatalogStopOnly(t, runner, 0)
		})
	}
}

func TestCatalogStopDependenciesAndReplicas(t *testing.T) {
	for _, labels := range []bool{false, true} {
		t.Run(fmt.Sprint(labels), func(t *testing.T) {
			runner := catalogStopRunner(t, appmanifest.MempoolID, "mempool-db", "mempool-api", "mempool-web", "mempool-api")
			original := runner.hook
			if labels {
				runner.hook = func(path string, args []string) (string, error, bool) {
					out, err, handled := original(path, args)
					if args[0] == "inspect" {
						var c catalogStopContainer
						_ = json.Unmarshal([]byte(out), &c)
						deps := map[string]string{"mempool-api": "mempool-db:service_healthy:false", "mempool-web": "mempool-api:service_started:false"}
						c.Labels["com.docker.compose.depends_on"] = deps[c.Labels["com.docker.compose.service"]]
						b, _ := json.Marshal(c)
						out = string(b)
					}
					return out, err, handled
				}
			}
			if err := stopCatalogApp(context.Background(), runner, appmanifest.MempoolID, false); err != nil {
				t.Fatal(err)
			}
			assertCatalogStopOnly(t, runner, 4)
			var stops [][]string
			for i, c := range runner.commands {
				if c.args[0] == "stop" {
					if i < 5 {
						t.Fatal("stopped before verifying entire inventory")
					}
					stops = append(stops, c.args[3:])
				}
			}
			want := [][]string{{fmt.Sprintf("%064x", 3)}, {fmt.Sprintf("%064x", 2), fmt.Sprintf("%064x", 4)}, {fmt.Sprintf("%064x", 1)}}
			if !reflect.DeepEqual(stops, want) {
				t.Fatalf("unsafe order: %v", stops)
			}
		})
	}
}

func TestCatalogStopRejectsUnverifiedInventoryBeforeMutation(t *testing.T) {
	for _, kind := range []string{"lookup failure", "short id", "duplicate", "too many", "bad json", "wrong id", "wrong project", "oneoff", "invalid service", "inspect failure", "cycle", "dependency injection"} {
		t.Run(kind, func(t *testing.T) {
			runner := catalogStopRunner(t, appmanifest.CPUMinerID, "cpuminer", "sidecar")
			original := runner.hook
			runner.hook = func(path string, args []string) (string, error, bool) {
				out, err, handled := original(path, args)
				if args[0] == "ps" {
					switch kind {
					case "lookup failure":
						return "secret", errors.New("secret"), true
					case "short id":
						return "abc;reboot", nil, true
					case "duplicate":
						return out + "\n" + out, nil, true
					case "too many":
						return strings.Repeat(fmt.Sprintf("%064x\n", 1), 65), nil, true
					}
				}
				// Break the last entry to prove the first was not stopped early.
				if args[0] == "inspect" && strings.HasSuffix(args[5], "2") {
					if kind == "inspect failure" {
						return "secret", errors.New("secret"), true
					}
					if kind == "bad json" {
						return "secret", nil, true
					}
					var c catalogStopContainer
					_ = json.Unmarshal([]byte(out), &c)
					switch kind {
					case "wrong id":
						c.ID = strings.Repeat("a", 64)
					case "wrong project":
						c.Labels["com.docker.compose.project"] = "other-node"
					case "oneoff":
						c.Labels["com.docker.compose.oneoff"] = "True"
					case "invalid service":
						c.Labels["com.docker.compose.service"] = "--all"
					case "cycle":
						c.Labels["com.docker.compose.depends_on"] = "sidecar:service_started:false"
					case "dependency injection":
						c.Labels["com.docker.compose.depends_on"] = "db;reboot"
					}
					b, _ := json.Marshal(c)
					out = string(b)
				}
				return out, err, handled
			}
			err := stopCatalogApp(context.Background(), runner, appmanifest.CPUMinerID, false)
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe error: %v", err)
			}
			assertCatalogStopOnly(t, runner, 0)
		})
	}
}

func TestCatalogStopFailureAndPostcondition(t *testing.T) {
	for _, phase := range []string{"stop", "still running", "post lookup"} {
		t.Run(phase, func(t *testing.T) {
			runner := catalogStopRunner(t, appmanifest.LNbitsID, "lnbits")
			original := runner.hook
			lookups := 0
			runner.hook = func(path string, args []string) (string, error, bool) {
				if args[0] == "stop" && phase == "stop" {
					return "secret", errors.New("secret"), true
				}
				if args[0] == "ps" {
					lookups++
					if lookups == 2 {
						if phase == "post lookup" {
							return "", errors.New("secret"), true
						}
						return fmt.Sprintf("%064x", 1), nil, true
					}
				}
				return original(path, args)
			}
			if err := stopCatalogApp(context.Background(), runner, appmanifest.LNbitsID, false); err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe success/error: %v", err)
			}
		})
	}
}

func TestCatalogStopDryRunAndUnknownApp(t *testing.T) {
	runner := catalogStopRunner(t, appmanifest.LNbitsID, "lnbits")
	if err := stopCatalogApp(context.Background(), runner, appmanifest.LNbitsID, true); err != nil {
		t.Fatal(err)
	}
	if err := stopCatalogApp(context.Background(), runner, "untrusted-project", false); err == nil {
		t.Fatal("unknown app accepted")
	}
	if len(runner.commands) != 0 {
		t.Fatal("unexpected command")
	}
}

func TestComposeAppStartStillSupportsStandaloneCompose(t *testing.T) {
	appsRoot, _ := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{standalone: true}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	if err := manager.Lifecycle(context.Background(), appmanifest.CPUMinerID, AppLifecycleStart, false); err != nil {
		t.Fatal(err)
	}
	last := runner.commands[len(runner.commands)-1]
	if last.path != dockerComposePath || !hasArgsSuffix(last.args, "up", "-d") {
		t.Fatalf("standalone start regressed: %#v", runner.commands)
	}
}

func TestNativeCatalogManagersStopWithoutCurrentSnapshot(t *testing.T) {
	t.Run("bark", func(t *testing.T) {
		runner := catalogStopRunner(t, appmanifest.BarkWalletID, "proxy", "web", "api", "barkd")
		manager := testBarkWalletManager(t, runner)
		if _, err := manager.Lifecycle(context.Background(), AppLifecycleStop, false); err != nil {
			t.Fatal(err)
		}
		assertCatalogStopOnly(t, runner, 4)
	})
	t.Run("publicpool", func(t *testing.T) {
		runner := catalogStopRunner(t, appmanifest.PublicPoolID, "public-pool-ui", "public-pool")
		manager := testPublicPoolManager(t, runner)
		if _, err := manager.Lifecycle(context.Background(), AppLifecycleStop, false); err != nil {
			t.Fatal(err)
		}
		assertCatalogStopOnly(t, runner, 2)
	})
}

func TestCatalogStatusDoesNotRequireCurrentImageDeclaration(t *testing.T) {
	for _, id := range []string{appmanifest.CPUMinerID, appmanifest.RoboSatsID, appmanifest.BTCPayID, appmanifest.LNDgID, appmanifest.LNbitsID, appmanifest.ElectrsID, appmanifest.MempoolID, appmanifest.FedimintGuardianID, appmanifest.FedimintGatewayID} {
		t.Run(id, func(t *testing.T) {
			m, err := appmanifest.ComposeManifestForApp(id)
			if err != nil {
				t.Fatal(err)
			}
			runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
				if path == dockerPath && len(args) > 0 && args[0] == "ps" {
					if !reflect.DeepEqual(args, []string{"ps", "--no-trunc", "--filter", "label=com.docker.compose.project=" + m.Project, "--filter", "label=com.docker.compose.service=" + m.PrimaryService, "--format", "{{.ID}}"}) {
						t.Fatalf("unsafe lookup: %v", args)
					}
					return strings.Repeat("a", 64), nil, true
				}
				if path == dockerPath && len(args) > 0 && args[0] == "stats" {
					return "1.2%", nil, true
				}
				t.Fatalf("unexpected command: %s %v", path, args)
				return "", nil, true
			}}
			manager := &ComposeAppManager{Runner: runner, AppsRoot: t.TempDir()}
			state, err := manager.Inspect(context.Background(), id)
			if err != nil || state.Status != "running" {
				t.Fatalf("runtime hidden by declaration drift: %#v %v", state, err)
			}
		})
	}
}
