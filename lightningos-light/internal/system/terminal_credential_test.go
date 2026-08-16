package system

import (
	"context"
	"errors"
	"testing"
)

type fakeTerminalCredentialClient struct {
	*fakePrivilegedServiceClient
	operator          string
	password          string
	dryRun            bool
	result            string
	err               error
	controlRequested  bool
	controlEnabled    bool
	controlAllowWrite bool
	controlDryRun     bool
	controlResult     bool
	controlErr        error
}

func (client *fakeTerminalCredentialClient) SetTerminalEnabled(_ context.Context, enabled bool, allowWrite bool, dryRun bool) (bool, bool, error) {
	client.controlRequested, client.controlEnabled, client.controlAllowWrite, client.controlDryRun = true, enabled, allowWrite, dryRun
	return client.controlResult, allowWrite, client.controlErr
}

func (client *fakeTerminalCredentialClient) RotateTerminalCredential(_ context.Context, operatorUser string, password string, dryRun bool) (string, error) {
	client.operator, client.password, client.dryRun = operatorUser, password, dryRun
	return client.result, client.err
}

func TestSetTerminalEnabledWithBrokerModes(t *testing.T) {
	for _, test := range []struct {
		name        string
		mode        string
		result      bool
		clientErr   error
		wantHandled bool
		wantDryRun  bool
		wantError   bool
	}{
		{name: "enforce", mode: "enforce", result: true, wantHandled: true},
		{name: "enforce mismatch", mode: "enforce", wantHandled: true, wantError: true},
		{name: "enforce error", mode: "enforce", clientErr: errors.New("rejected"), wantHandled: true, wantError: true},
		{name: "shadow", mode: "shadow", clientErr: errors.New("observational"), wantDryRun: true},
		{name: "disabled", mode: "disabled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeTerminalCredentialClient{
				fakePrivilegedServiceClient: &fakePrivilegedServiceClient{mode: test.mode},
				controlResult:               test.result,
				controlErr:                  test.clientErr,
			}
			ConfigurePrivilegedClient(client)
			t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
			handled, err := SetTerminalEnabledWithBroker(context.Background(), true, true)
			if handled != test.wantHandled || (err != nil) != test.wantError || client.controlDryRun != test.wantDryRun {
				t.Fatalf("handled/error/client=%v/%v/%+v", handled, err, client)
			}
			if test.mode != "disabled" && (!client.controlRequested || !client.controlEnabled || !client.controlAllowWrite) {
				t.Fatalf("typed terminal control request was not preserved: %+v", client)
			}
		})
	}
}

func TestRotateTerminalCredentialWithBrokerModes(t *testing.T) {
	const password = "AbCdEfGhIjKlMnOpQrStUvWxYz012345"
	for _, test := range []struct {
		name        string
		mode        string
		result      string
		clientErr   error
		wantHandled bool
		wantDryRun  bool
		wantError   bool
	}{
		{name: "enforce", mode: "enforce", result: "losop", wantHandled: true},
		{name: "enforce mismatch", mode: "enforce", result: "other", wantHandled: true, wantError: true},
		{name: "enforce error", mode: "enforce", clientErr: errors.New("rejected"), wantHandled: true, wantError: true},
		{name: "shadow", mode: "shadow", clientErr: errors.New("observational"), wantDryRun: true},
		{name: "disabled", mode: "disabled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeTerminalCredentialClient{
				fakePrivilegedServiceClient: &fakePrivilegedServiceClient{mode: test.mode},
				result:                      test.result,
				err:                         test.clientErr,
			}
			ConfigurePrivilegedClient(client)
			t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
			handled, err := RotateTerminalCredentialWithBroker(context.Background(), "losop", password)
			if handled != test.wantHandled || (err != nil) != test.wantError || client.dryRun != test.wantDryRun {
				t.Fatalf("handled/error/client=%v/%v/%+v", handled, err, client)
			}
			if test.mode != "disabled" && (client.operator != "losop" || client.password != password) {
				t.Fatalf("typed terminal credential request was not preserved: operator=%q password_len=%d", client.operator, len(client.password))
			}
		})
	}
}
