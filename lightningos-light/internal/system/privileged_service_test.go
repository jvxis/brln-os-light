package system

import (
	"context"
	"errors"
	"testing"
)

type fakePrivilegedServiceClient struct {
	mode       string
	calls      int
	unit       string
	noBlock    bool
	dryRun     bool
	err        error
	fileCalls  int
	fileDryRun bool
	fileErr    error
}

func (client *fakePrivilegedServiceClient) EnableLogin(_ context.Context, dryRun bool) error {
	client.fileCalls++
	client.fileDryRun = dryRun
	return client.fileErr
}

func (client *fakePrivilegedServiceClient) Mode() string { return client.mode }

func (client *fakePrivilegedServiceClient) RestartService(_ context.Context, unit string, noBlock bool, dryRun bool) error {
	client.calls++
	client.unit = unit
	client.noBlock = noBlock
	client.dryRun = dryRun
	return client.err
}

func TestRestartServiceWithBrokerEnforce(t *testing.T) {
	client := &fakePrivilegedServiceClient{mode: "enforce", err: errors.New("rejected")}
	ConfigurePrivilegedClient(client)
	t.Cleanup(func() { ConfigurePrivilegedClient(nil) })

	handled, err := restartServiceWithBroker(context.Background(), "lnd", true)
	if !handled || err == nil {
		t.Fatalf("handled/error = %v/%v, want true/non-nil", handled, err)
	}
	if client.calls != 1 || client.unit != "lnd" || !client.noBlock || client.dryRun {
		t.Fatalf("unexpected broker call: %#v", client)
	}
}

func TestSystemctlRestartManagerUsesBrokerNoBlock(t *testing.T) {
	client := &fakePrivilegedServiceClient{mode: "enforce"}
	ConfigurePrivilegedClient(client)
	t.Cleanup(func() { ConfigurePrivilegedClient(nil) })

	if err := SystemctlRestart(context.Background(), "lightningos-manager"); err != nil {
		t.Fatalf("restart manager: %v", err)
	}
	if client.calls != 1 || client.unit != "lightningos-manager" || !client.noBlock || client.dryRun {
		t.Fatalf("manager restart must use one real non-blocking broker call: %#v", client)
	}
}

func TestRestartServiceWithBrokerShadowFallsBack(t *testing.T) {
	client := &fakePrivilegedServiceClient{mode: "shadow", err: errors.New("rejected")}
	ConfigurePrivilegedClient(client)
	t.Cleanup(func() { ConfigurePrivilegedClient(nil) })

	handled, err := restartServiceWithBroker(context.Background(), "lnd", false)
	if handled || err != nil {
		t.Fatalf("handled/error = %v/%v, want false/nil", handled, err)
	}
	if client.calls != 1 || !client.dryRun {
		t.Fatalf("shadow mode did not issue exactly one dry-run: %#v", client)
	}
}

func TestRestartServiceWithBrokerDisabledUsesLegacyPath(t *testing.T) {
	client := &fakePrivilegedServiceClient{mode: "disabled"}
	ConfigurePrivilegedClient(client)
	t.Cleanup(func() { ConfigurePrivilegedClient(nil) })

	handled, err := restartServiceWithBroker(context.Background(), "lnd", false)
	if handled || err != nil || client.calls != 0 {
		t.Fatalf("disabled mode result = %v/%v, calls=%d", handled, err, client.calls)
	}
}

func TestEnableLoginConfigWithBrokerModes(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		path        string
		fileErr     error
		wantHandled bool
		wantError   bool
		wantCalls   int
		wantDryRun  bool
	}{
		{name: "disabled", mode: "disabled", path: "/etc/lightningos/config.yaml"},
		{name: "shadow", mode: "shadow", path: "/etc/lightningos/config.yaml", fileErr: errors.New("rejected"), wantCalls: 1, wantDryRun: true},
		{name: "enforce", mode: "enforce", path: "/etc/lightningos/config.yaml", wantHandled: true, wantCalls: 1},
		{name: "enforce error", mode: "enforce", path: "/etc/lightningos/config.yaml", fileErr: errors.New("failed"), wantHandled: true, wantError: true, wantCalls: 1},
		{name: "custom shadow fallback", mode: "shadow", path: "/tmp/config.yaml"},
		{name: "custom enforce rejected", mode: "enforce", path: "/tmp/config.yaml", wantHandled: true, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakePrivilegedServiceClient{mode: test.mode, fileErr: test.fileErr}
			ConfigurePrivilegedClient(client)
			t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
			handled, err := EnableLoginConfigWithBroker(context.Background(), test.path)
			if handled != test.wantHandled || (err != nil) != test.wantError {
				t.Fatalf("handled/error = %v/%v", handled, err)
			}
			if client.fileCalls != test.wantCalls || client.fileDryRun != test.wantDryRun {
				t.Fatalf("unexpected file call: %#v", client)
			}
		})
	}
}
