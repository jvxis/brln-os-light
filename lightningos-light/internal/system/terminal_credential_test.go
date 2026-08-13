package system

import (
	"context"
	"errors"
	"testing"
)

type fakeTerminalCredentialClient struct {
	*fakePrivilegedServiceClient
	operator string
	password string
	dryRun   bool
	result   string
	err      error
}

func (client *fakeTerminalCredentialClient) RotateTerminalCredential(_ context.Context, operatorUser string, password string, dryRun bool) (string, error) {
	client.operator, client.password, client.dryRun = operatorUser, password, dryRun
	return client.result, client.err
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
