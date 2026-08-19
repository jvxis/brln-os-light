package privileged

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type recordingLNDObservabilityManager struct {
	calls int
	state LNDChannelDBState
	err   error
}

func (manager *recordingLNDObservabilityManager) ChannelDBSize(context.Context) (LNDChannelDBState, error) {
	manager.calls++
	return manager.state, manager.err
}

func TestNativeLNDObservabilityReadsOnlyRegularChannelDBMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channel.db")
	if err := os.WriteFile(path, make([]byte, 321), 0600); err != nil {
		t.Fatal(err)
	}
	manager := &NativeLNDObservabilityManager{channelDBPath: path}
	state, err := manager.ChannelDBSize(context.Background())
	if err != nil || state.SizeBytes != 321 {
		t.Fatalf("state/error=%#v/%v", state, err)
	}

	directory := filepath.Join(t.TempDir(), "channel.db")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	manager.channelDBPath = directory
	if _, err := manager.ChannelDBSize(context.Background()); err == nil {
		t.Fatal("directory was accepted as channel.db")
	}

	symlink := filepath.Join(t.TempDir(), "channel.db")
	if err := os.Symlink(path, symlink); err == nil {
		manager.channelDBPath = symlink
		if _, err := manager.ChannelDBSize(context.Background()); err == nil {
			t.Fatal("symlink was accepted as channel.db")
		}
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	manager.channelDBPath = path
	if _, err := manager.ChannelDBSize(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context error=%v", err)
	}
}

func TestLNDChannelDBProtocolAndBrokerKeepReadBoundaryClosed(t *testing.T) {
	params, err := MarshalParams(struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		Version: ProtocolVersion, RequestID: "lnd_channel_db_test",
		Operation: OperationLNDChannelDBStatus, Params: params,
	}
	if err := ValidateRequest(request); err != nil {
		t.Fatalf("closed status request rejected: %v", err)
	}
	injected := request
	injected.Params = json.RawMessage(`{"path":"/etc/shadow"}`)
	if err := ValidateRequest(injected); err == nil {
		t.Fatal("status request accepted a caller-selected path")
	}
	dryRun := request
	dryRun.DryRun = true
	if err := ValidateRequest(dryRun); err == nil {
		t.Fatal("read-only status request accepted dry_run")
	}

	manager := &recordingLNDObservabilityManager{state: LNDChannelDBState{SizeBytes: 21_165_629_440}}
	locker := &recordingLocker{}
	broker := &Broker{
		Runner: &recordingRunner{}, Audit: &recordingAudit{}, Locker: locker,
		LNDObservability: manager, Caller: "test",
	}
	response := broker.Handle(context.Background(), request)
	if !response.OK || manager.calls != 1 || locker.locks != 0 || locker.unlocks != 0 {
		t.Fatalf("response/manager/locker=%#v/%#v/%#v", response, manager, locker)
	}
	var state LNDChannelDBState
	if err := decodeStrict(response.Result, &state); err != nil || state.SizeBytes != manager.state.SizeBytes {
		t.Fatalf("state/error=%#v/%v", state, err)
	}

	manager.err = errors.New("sensitive path detail")
	response = broker.Handle(context.Background(), request)
	if response.OK || response.Error == nil || response.Error.Code != "lnd_channel_db_status_failed" || response.Error.Message != "LND channel database status failed" {
		t.Fatalf("broker leaked or misclassified observability failure: %#v", response)
	}
}

func TestClientLNDChannelDBSizeUsesExactReadOnlyResponse(t *testing.T) {
	transport := &fakeTransport{result: LNDChannelDBState{SizeBytes: 1234}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	sizeBytes, err := client.LNDChannelDBSize(context.Background())
	if err != nil || sizeBytes != 1234 || transport.request.Operation != OperationLNDChannelDBStatus || transport.request.DryRun || string(transport.request.Params) != "{}" {
		t.Fatalf("size/error/request=%d/%v/%#v", sizeBytes, err, transport.request)
	}

	transport.result = LNDChannelDBState{SizeBytes: -1}
	if _, err := client.LNDChannelDBSize(context.Background()); err == nil {
		t.Fatal("client accepted a negative channel database size")
	}
}
