package privileged

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestManagerFirewallStatusUsesOnlyFixedConfigAndUFWPath(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "manager-firewall.conf")
	ufwExecutable := filepath.Join(root, "ufw")
	if err := os.WriteFile(configPath, []byte("LAN_CIDR=192.168.68.0/22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ufwExecutable, []byte("fixture"), 0755); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{output: "Status: active\n8443/tcp ALLOW IN 192.168.68.0/22\n"}
	manager := &ManagerFirewallInspector{Runner: runner, ConfigPath: configPath, UFWPath: ufwExecutable}
	state, err := manager.Status(context.Background())
	if err != nil || !state.Installed || !state.Active || !state.ConfigValid || !state.StatusAvailable || !state.LANRulePresent || !state.ManagerAccessBound {
		t.Fatalf("unexpected state/error: %+v/%v", state, err)
	}
	want := []recordedCommand{{path: ufwExecutable, args: []string{"status"}}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("unexpected commands: %#v", runner.commands)
	}
}

func TestManagerFirewallStatusFailsClosedForUnsafeInputs(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "manager-firewall.conf")
	ufwExecutable := filepath.Join(root, "ufw")
	if err := os.WriteFile(configPath, []byte("LAN_CIDR=not-a-cidr\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "outside"), ufwExecutable); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	manager := &ManagerFirewallInspector{Runner: &recordingRunner{}, ConfigPath: configPath, UFWPath: ufwExecutable}
	state, err := manager.Status(context.Background())
	if err == nil || state.ConfigValid || state.Installed {
		t.Fatalf("unsafe UFW path accepted: %+v/%v", state, err)
	}

	if err := os.Remove(ufwExecutable); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ufwExecutable, []byte("fixture"), 0755); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{err: errors.New("permission denied")}
	manager.Runner, manager.UFWPath = runner, ufwExecutable
	state, err = manager.Status(context.Background())
	if err != nil || !state.Installed || state.StatusAvailable || state.ManagerAccessBound {
		t.Fatalf("unavailable UFW status was not closed: %+v/%v", state, err)
	}
}

func TestManagerFirewallStatusReportsMissingConfigWithoutInventingPolicy(t *testing.T) {
	root := t.TempDir()
	ufwExecutable := filepath.Join(root, "ufw")
	if err := os.WriteFile(ufwExecutable, []byte("fixture"), 0755); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{output: "Status: active\n8443/tcp ALLOW IN Anywhere\n"}
	manager := &ManagerFirewallInspector{Runner: runner, ConfigPath: filepath.Join(root, "missing.conf"), UFWPath: ufwExecutable}
	state, err := manager.Status(context.Background())
	if err != nil || state.ConfigValid || !state.Installed || !state.Active || !state.BroadRulePresent || state.ManagerAccessBound {
		t.Fatalf("missing config did not fail closed: %+v/%v", state, err)
	}
}

func TestManagerFirewallProtocolAndBrokerAreReadOnly(t *testing.T) {
	params, err := MarshalParams(struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Version: ProtocolVersion, RequestID: "manager_firewall_status", Operation: OperationManagerFirewallStatus, Params: params}
	if err := ValidateRequest(request); err != nil {
		t.Fatal(err)
	}
	request.DryRun = true
	if err := ValidateRequest(request); err == nil {
		t.Fatal("manager firewall status accepted dry_run")
	}

	manager := &recordingManagerFirewall{state: ManagerFirewallState{Installed: true, Active: true}}
	audit := &recordingAudit{}
	locker := &recordingLocker{}
	broker := &Broker{Runner: &recordingRunner{}, Audit: audit, Locker: locker, ManagerFirewall: manager, Caller: "test"}
	request.DryRun = false
	response := broker.Handle(context.Background(), request)
	if !response.OK || manager.calls != 1 || locker.locks != 0 {
		t.Fatalf("response=%#v calls=%d locks=%d", response, manager.calls, locker.locks)
	}
}

type recordingManagerFirewall struct {
	calls int
	state ManagerFirewallState
	err   error
}

func (manager *recordingManagerFirewall) Status(context.Context) (ManagerFirewallState, error) {
	manager.calls++
	return manager.state, manager.err
}
