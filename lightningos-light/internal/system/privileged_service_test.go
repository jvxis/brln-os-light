package system

import (
	"context"
	"errors"
	"testing"
)

type fakePrivilegedServiceClient struct {
	mode    string
	calls   int
	unit    string
	noBlock bool
	dryRun  bool
	err     error
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
	ConfigurePrivilegedServiceClient(client)
	t.Cleanup(func() { ConfigurePrivilegedServiceClient(nil) })

	handled, err := restartServiceWithBroker(context.Background(), "lnd", true)
	if !handled || err == nil {
		t.Fatalf("handled/error = %v/%v, want true/non-nil", handled, err)
	}
	if client.calls != 1 || client.unit != "lnd" || !client.noBlock || client.dryRun {
		t.Fatalf("unexpected broker call: %#v", client)
	}
}

func TestRestartServiceWithBrokerShadowFallsBack(t *testing.T) {
	client := &fakePrivilegedServiceClient{mode: "shadow", err: errors.New("rejected")}
	ConfigurePrivilegedServiceClient(client)
	t.Cleanup(func() { ConfigurePrivilegedServiceClient(nil) })

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
	ConfigurePrivilegedServiceClient(client)
	t.Cleanup(func() { ConfigurePrivilegedServiceClient(nil) })

	handled, err := restartServiceWithBroker(context.Background(), "lnd", false)
	if handled || err != nil || client.calls != 0 {
		t.Fatalf("disabled mode result = %v/%v, calls=%d", handled, err, client.calls)
	}
}
