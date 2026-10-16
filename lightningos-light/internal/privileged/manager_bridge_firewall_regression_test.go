package privileged

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// These tests model the persisted firewall rules, rather than only asserting
// the implementation's command order. No real firewall is modified.
type bridgeMigrationRunner struct {
	broad       map[string]bool
	restricted  map[string]bool
	failAdd     bool
	failDelete  bool
	lostAccess  bool
	cancelOnAdd context.CancelFunc
	commands    []recordedCommand
}

func newBridgeMigrationRunner() *bridgeMigrationRunner {
	return &bridgeMigrationRunner{
		broad:      map[string]bool{"br-0123456789ab": true, "br-aaaaaaaaaaaa": true, "br-bbbbbbbbbbbb": true},
		restricted: map[string]bool{},
	}
}

func (r *bridgeMigrationRunner) Run(ctx context.Context, path string, args ...string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	r.commands = append(r.commands, recordedCommand{path: path, args: append([]string(nil), args...)})
	if path == dockerPath && len(args) == 5 && args[0] == "network" && args[1] == "inspect" {
		if args[4] == "{{.Id}}" {
			if args[2] == "bark-wallet_default" {
				return strings.Repeat("a", 64), nil
			}
			return testNetworkID, nil
		}
		if args[4] == "{{range .IPAM.Config}}{{.Subnet}} {{end}}" {
			if args[2] == "bark-wallet_default" {
				return "172.21.0.0/16", nil
			}
			return "172.20.0.0/16", nil
		}
	}
	if path == ufwPath && len(args) == 12 && args[0] == "allow" {
		if r.failAdd {
			return "", errors.New("injected allow failure")
		}
		r.restricted[args[3]] = true
		if r.cancelOnAdd != nil {
			r.cancelOnAdd()
		}
		return "Rule added", nil
	}
	if path == ufwPath && len(args) == 12 && args[0] == "--force" {
		if r.failDelete {
			return "", errors.New("injected delete failure")
		}
		bridge := args[5]
		delete(r.broad, bridge)
		if !r.restricted[bridge] {
			r.lostAccess = true
		}
		return "Rule deleted", nil
	}
	return "", errors.New("unexpected fixture command")
}

func (r *bridgeMigrationRunner) status() string {
	status := "Status: active\n8443/tcp on tailscale0 ALLOW IN Anywhere\n"
	for bridge := range r.broad {
		status += "8443/tcp on " + bridge + " ALLOW IN Anywhere\n"
	}
	return status
}

func TestManagerBridgeMigrationAddFailurePreservesExistingAccess(t *testing.T) {
	r := newBridgeMigrationRunner()
	r.failAdd = true
	before := r.status()
	if err := allowManagerFromAppBridge(context.Background(), r, "brln-community", before); err == nil {
		t.Fatal("expected allow failure")
	}
	if !r.broad["br-0123456789ab"] || len(r.broad) != 3 || r.lostAccess {
		t.Fatalf("failed replacement removed existing access: %+v", r)
	}
}

func TestManagerBridgeMigrationPreservesUnownedRulesAndContinuity(t *testing.T) {
	r := newBridgeMigrationRunner()
	if err := allowManagerFromAppBridge(context.Background(), r, "brln-community", r.status()); err != nil {
		t.Fatal(err)
	}
	if r.broad["br-0123456789ab"] || !r.restricted["br-0123456789ab"] || r.lostAccess {
		t.Fatalf("own migration interrupted access: %+v", r)
	}
	if !reflect.DeepEqual(r.broad, map[string]bool{"br-aaaaaaaaaaaa": true, "br-bbbbbbbbbbbb": true}) {
		t.Fatalf("migration removed unowned rules (including absent interfaces): %+v", r.broad)
	}
}

func TestManagerBridgeMigrationDeleteFailureCanRetryWithoutOutage(t *testing.T) {
	r := newBridgeMigrationRunner()
	r.failDelete = true
	if err := allowManagerFromAppBridge(context.Background(), r, "brln-community", r.status()); err == nil {
		t.Fatal("expected delete failure")
	}
	if !r.broad["br-0123456789ab"] || !r.restricted["br-0123456789ab"] || r.lostAccess {
		t.Fatalf("replacement not installed before failed removal: %+v", r)
	}
	r.failDelete = false
	if err := allowManagerFromAppBridge(context.Background(), r, "brln-community", r.status()); err != nil {
		t.Fatal(err)
	}
	if r.broad["br-0123456789ab"] || r.lostAccess {
		t.Fatal("retry failed to preserve access and remove the old rule")
	}
}

func TestManagerBridgeMigrationEachAppOwnsOnlyItsRule(t *testing.T) {
	r := newBridgeMigrationRunner()
	for _, project := range []string{"brln-community", "bark-wallet", "brln-community", "bark-wallet"} {
		if err := allowManagerFromAppBridge(context.Background(), r, project, r.status()); err != nil {
			t.Fatal(err)
		}
	}
	if r.lostAccess || !reflect.DeepEqual(r.broad, map[string]bool{"br-bbbbbbbbbbbb": true}) || len(r.restricted) != 2 {
		t.Fatalf("repeated two-app migration affected another app or access: %+v", r)
	}
}

func TestManagerBridgeMigrationCancelledBeforeMutation(t *testing.T) {
	r := newBridgeMigrationRunner()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := allowManagerFromAppBridge(ctx, r, "brln-community", r.status()); err == nil || len(r.commands) != 0 {
		t.Fatalf("cancelled operation mutated firewall: %v, %+v", err, r.commands)
	}
}

func TestManagerBridgeMigrationInterruptedAfterAddKeepsAccess(t *testing.T) {
	r := newBridgeMigrationRunner()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.cancelOnAdd = cancel
	if err := allowManagerFromAppBridge(ctx, r, "brln-community", r.status()); err == nil {
		t.Fatal("expected cancellation")
	}
	if !r.broad["br-0123456789ab"] || !r.restricted["br-0123456789ab"] || r.lostAccess {
		t.Fatalf("interruption lost access: %+v", r)
	}
	r.cancelOnAdd = nil
	if err := allowManagerFromAppBridge(context.Background(), r, "brln-community", r.status()); err != nil {
		t.Fatal(err)
	}
	if r.broad["br-0123456789ab"] || r.lostAccess {
		t.Fatal("retry failed")
	}
}

func TestManagerBridgeMigrationPreservesOtherPortsAndAccessRules(t *testing.T) {
	r := newBridgeMigrationRunner()
	status := "Status: active\n8443/tcp on tailscale0 ALLOW IN Anywhere\n8443/tcp ALLOW IN 192.168.0.0/24\n"
	for _, port := range []string{"22", "8332", "8333", "28332", "28333", "10009", "8080", "50001", "7041", "1984", "42069", "5000", "8889", "23000", "12596", "8999", "8173", "8174", "8175", "8176", "8177", "3333", "3334", "8081", "10029", "8089", "11010", "18081", "4448", "4004"} {
		status += port + "/tcp on br-bbbbbbbbbbbb ALLOW IN Anywhere\n"
	}
	if err := allowManagerFromAppBridge(context.Background(), r, "brln-community", status); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range r.commands {
		if cmd.path == ufwPath && cmd.args[0] != "allow" {
			t.Fatalf("unrelated rule changed: %+v", cmd)
		}
	}
}
