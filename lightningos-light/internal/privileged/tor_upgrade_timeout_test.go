package privileged

import (
	"context"
	"testing"
	"time"
)

type delayedTorTransport struct {
	fakeTransport
	delay time.Duration
}

func (t *delayedTorTransport) Do(ctx context.Context, request Request) (Response, error) {
	select {
	case <-time.After(t.delay):
		return t.fakeTransport.Do(ctx, request)
	case <-ctx.Done():
		return Response{}, ctx.Err()
	}
}

func TestTorStartSurvivesShortDefaultTransportTimeout(t *testing.T) {
	transport := &delayedTorTransport{delay: 40 * time.Millisecond}
	transport.result = TorUpgradeState{Status: "started", Unit: torUpgradeUnit}
	client := NewClientWithTransport(ModeEnforce, 10*time.Millisecond, transport, nil)
	status, _, err := client.StartTorUpgrade(context.Background(), "trusted", false, false)
	if err != nil || status != "started" {
		t.Fatalf("slow successful start reported as failure: %q %v", status, err)
	}
}

func TestTorStartTimeoutKeepsDryRunAndCallerBounds(t *testing.T) {
	if got := operationTimeout(3*time.Second, OperationTorUpgradeStart, false); got < 30*time.Second || got > time.Minute {
		t.Fatalf("Tor preparation budget = %v, want bounded 30-60s", got)
	}
	if got := operationTimeout(3*time.Second, OperationTorUpgradeStart, true); got != 3*time.Second {
		t.Fatalf("dry-run budget changed: %v", got)
	}
	if got := operationTimeout(time.Minute, OperationTorUpgradeStart, false); got != time.Minute {
		t.Fatalf("larger configured budget shortened: %v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	transport := &delayedTorTransport{delay: time.Second}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	if _, _, err := client.StartTorUpgrade(ctx, "trusted", false, false); err == nil {
		t.Fatal("cancelled caller was ignored")
	}
}

type deadlineTorManager struct {
	recordingTorUpgradeManager
	remaining time.Duration
}

func (m *deadlineTorManager) Start(ctx context.Context, _ TorUpgradeStartParams, _ bool) (TorUpgradeState, error) {
	deadline, _ := ctx.Deadline()
	m.remaining = time.Until(deadline)
	return TorUpgradeState{Status: "started", Unit: torUpgradeUnit}, nil
}

func TestTorStartBrokerAlsoAllowsPreparationBudget(t *testing.T) {
	manager := &deadlineTorManager{}
	broker := &Broker{Runner: &recordingRunner{}, Audit: &recordingAudit{}, Locker: &recordingLocker{}, TorUpgrade: manager, Timeout: 3 * time.Second}
	params, err := MarshalParams(TorUpgradeStartParams{HelperContent: "trusted"})
	if err != nil {
		t.Fatal(err)
	}
	result := broker.Handle(context.Background(), Request{Version: ProtocolVersion, RequestID: "tor_start_budget", Operation: OperationTorUpgradeStart, Params: params})
	if !result.OK || manager.remaining < 29*time.Second || manager.remaining > 30*time.Second {
		t.Fatalf("broker preparation still has a short deadline: result=%+v remaining=%v", result, manager.remaining)
	}
}
