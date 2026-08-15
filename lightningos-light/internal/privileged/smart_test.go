package privileged

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type smartRecordingRunner struct {
	commands []recordedCommand
	listing  string
	output   string
	smartErr error
}

func (runner *smartRecordingRunner) Run(_ context.Context, path string, args ...string) (string, error) {
	runner.commands = append(runner.commands, recordedCommand{path: path, args: append([]string(nil), args...)})
	if len(args) == 3 && reflect.DeepEqual(args, []string{"-dn", "-o", "PATH,TYPE"}) {
		return runner.listing, nil
	}
	return runner.output, runner.smartErr
}

func TestNativeSMARTManagerAllowsOnlyEnumeratedDiskAndFixedArguments(t *testing.T) {
	root := t.TempDir()
	lsblk := filepath.Join(root, "lsblk")
	smartctl := filepath.Join(root, "smartctl")
	for _, path := range []string{lsblk, smartctl} {
		if err := os.WriteFile(path, []byte("fixed"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runner := &smartRecordingRunner{listing: "/dev/sda disk\n/dev/loop0 loop\n", output: "SMART payload"}
	manager := &NativeSMARTManager{Runner: runner, LSBLKPath: lsblk, SMARTCTLPaths: []string{smartctl}}
	state, err := manager.Read(context.Background(), "/dev/sda")
	if err != nil || state.Device != "/dev/sda" || state.Output != "SMART payload" || !state.Available {
		t.Fatalf("unexpected SMART state/error: %#v/%v", state, err)
	}
	want := []recordedCommand{
		{path: lsblk, args: []string{"-dn", "-o", "PATH,TYPE"}},
		{path: smartctl, args: []string{"-a", "/dev/sda"}},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("unexpected SMART commands: %#v", runner.commands)
	}
	if _, err := manager.Read(context.Background(), "/dev/loop0"); err == nil {
		t.Fatal("non-disk SMART target was accepted")
	}
}

func TestNativeSMARTManagerRejectsSymlinkedExecutableAndEmptyFailure(t *testing.T) {
	root := t.TempDir()
	lsblk := filepath.Join(root, "lsblk")
	target := filepath.Join(root, "smartctl-target")
	smartctl := filepath.Join(root, "smartctl")
	for _, path := range []string{lsblk, target} {
		if err := os.WriteFile(path, []byte("fixed"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runner := &smartRecordingRunner{listing: "/dev/sda disk\n"}
	manager := &NativeSMARTManager{Runner: runner, LSBLKPath: lsblk, SMARTCTLPaths: []string{target}}
	if err := os.Symlink(target, smartctl); err == nil {
		manager.SMARTCTLPaths = []string{smartctl}
		if _, err := manager.Read(context.Background(), "/dev/sda"); err == nil {
			t.Fatal("symlinked smartctl executable was accepted")
		}
	}
	manager.SMARTCTLPaths = []string{target}
	runner.smartErr = errors.New("smartctl failed")
	if _, err := manager.Read(context.Background(), "/dev/sda"); err == nil {
		t.Fatal("empty failed SMART read was accepted")
	}
}

type recordingSMARTManager struct {
	device string
	state  SMARTReadState
}

func (manager *recordingSMARTManager) Read(_ context.Context, device string) (SMARTReadState, error) {
	manager.device = device
	return manager.state, nil
}

func TestSMARTProtocolBrokerAndClientAreStrict(t *testing.T) {
	params, err := MarshalParams(SMARTReadParams{Device: "/dev/nvme0n1"})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Version: ProtocolVersion, RequestID: "smart_read_test", Operation: OperationSMARTRead, Params: params}
	if err := ValidateRequest(request); err != nil {
		t.Fatalf("valid SMART request rejected: %v", err)
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"device":"/etc/passwd"}`),
		json.RawMessage(`{"device":"/dev/sda","args":["--set"]}`),
		json.RawMessage(`{"device":"/dev/sda/../sdb"}`),
	} {
		request.Params = raw
		if err := ValidateRequest(request); err == nil {
			t.Fatalf("unsafe SMART request accepted: %s", raw)
		}
	}
	request.Params = params
	manager := &recordingSMARTManager{state: SMARTReadState{Device: "/dev/nvme0n1", Output: "SMART payload", Available: true}}
	broker := &Broker{Runner: &recordingRunner{}, Audit: &recordingAudit{}, SMART: manager, Caller: "test"}
	response := broker.Handle(context.Background(), request)
	if !response.OK || manager.device != "/dev/nvme0n1" {
		t.Fatalf("unexpected SMART broker response/manager: %#v/%#v", response, manager)
	}

	transport := &fakeTransport{result: manager.state}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	output, available, err := client.ReadSMART(context.Background(), "/dev/nvme0n1")
	if err != nil || output != "SMART payload" || !available || transport.request.Operation != OperationSMARTRead {
		t.Fatalf("unexpected SMART client result/request: %q/%v/%v/%#v", output, available, err, transport.request)
	}
	transport.result = SMARTReadState{Device: "/dev/nvme0n1", Output: strings.Repeat("x", maxCommandOutputBytes+1), Available: true}
	if _, _, err := client.ReadSMART(context.Background(), "/dev/nvme0n1"); err == nil {
		t.Fatal("client accepted oversized SMART output")
	}
}
