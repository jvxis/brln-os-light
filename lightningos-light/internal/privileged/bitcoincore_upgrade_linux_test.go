//go:build linux

package privileged

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func TestBitcoinCorePreviousManagedReleaseStaysObservableAndRequiresStop(t *testing.T) {
	for _, running := range []bool{true, false} {
		manager, runner, _, dataDir := newTestBitcoinCoreLifecycleManager(t)
		id := strings.Repeat("b", 64)
		baseHook := runner.hook
		runner.hook = func(path string, args []string) (string, error, bool) {
			if path == dockerPath && len(args) > 1 && args[0] == "ps" && args[1] == "-a" {
				return id, nil, true
			}
			if path == dockerPath && reflect.DeepEqual(args, []string{"inspect", id}) {
				raw := strings.ReplaceAll(managedBitcoinInspectJSON(id, lifecycleTestBitcoinCoreImageID, dataDir), appmanifest.BitcoinCoreImage, "lightningos/bitcoin-core:31.1")
				if !running {
					raw = strings.ReplaceAll(raw, `"Running":true`, `"Running":false`)
				}
				return raw, nil, true
			}
			return baseHook(path, args)
		}
		inspection, err := manager.Inspect(context.Background(), appmanifest.BitcoinCoreID)
		want := "stopped"
		if running {
			want = "running"
		}
		if err != nil || inspection.Status != want || inspection.LegacyMigrationRequired {
			t.Fatalf("31.1 observation requires no legacy migration: %+v/%v", inspection, err)
		}
		runner.commands = nil
		migration, err := manager.prepareLegacyBitcoinMigration(context.Background())
		if running && (err == nil || !strings.Contains(err.Error(), "stop Bitcoin Core")) {
			t.Fatalf("running previous Core must be stopped before recreation: %v", err)
		}
		if !running && (err != nil || migration != nil) {
			t.Fatalf("normal stopped upgrade entered legacy migration: %v", err)
		}
		for _, command := range runner.commands {
			for _, arg := range command.args {
				if arg == "up" || arg == "down" || arg == "commit" || arg == "rm" {
					t.Fatalf("preflight mutated runtime: %v", command)
				}
			}
		}
	}
}

func TestBitcoinCoreStartupHealthFailureIsNotReportedAsSuccess(t *testing.T) {
	manager, _, _, _ := newTestBitcoinCoreLifecycleManager(t)
	failed := errors.New("RPC readiness failed")
	manager.BitcoinMigrationProbe = func(context.Context, string) error { return failed }
	if err := manager.Lifecycle(context.Background(), appmanifest.BitcoinCoreID, AppLifecycleStart, false); !errors.Is(err, failed) {
		t.Fatalf("startup returned success without RPC readiness: %v", err)
	}
}

func TestBitcoinCoreStopSucceedsWithoutRunningContainerOrStartupProbe(t *testing.T) {
	manager, runner, _, _ := newTestBitcoinCoreLifecycleManager(t)
	runner.containerID = ""
	manager.BitcoinMigrationProbe = func(context.Context, string) error {
		t.Fatal("Stop must not run startup health checks")
		return nil
	}
	if err := manager.Lifecycle(context.Background(), appmanifest.BitcoinCoreID, AppLifecycleStop, false); err != nil {
		t.Fatalf("successful shutdown treated as failed startup: %v", err)
	}
}

func TestBitcoinCoreStartupRejectsWrongChainOrVersion(t *testing.T) {
	for _, chain := range []string{"main", "regtest"} {
		manager := &ComposeAppManager{Runner: &composeRecordingRunner{hook: func(_ string, args []string) (string, error, bool) {
			if args[len(args)-1] == "getblockchaininfo" {
				return `{"chain":"` + chain + `"}`, nil, true
			}
			if args[len(args)-1] == "getnetworkinfo" {
				return `{"version":310100}`, nil, true
			}
			return "", nil, false
		}}}
		if err := manager.probeMigratedBitcoinCore(context.Background(), strings.Repeat("a", 64)); err == nil {
			t.Fatalf("accepted chain=%s with old version", chain)
		}
	}
}
