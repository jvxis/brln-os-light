package system

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeSystemIntegrationsClient struct {
	*fakePrivilegedServiceClient
	installedAssets  []SystemIntegrationAsset
	installDryRuns   []bool
	terminalChanged  bool
	certificate      bool
	lndPolicy        bool
	applyDryRun      bool
	finalizeDryRun   bool
	serviceStatus    string
	integrationReady bool
	err              error
}

func (client *fakeSystemIntegrationsClient) SystemIntegrationsStatus(context.Context) (bool, error) {
	return client.integrationReady, client.err
}

func (client *fakeSystemIntegrationsClient) InstallSystemIntegrationAsset(_ context.Context, asset string, content string, dryRun bool) (bool, error) {
	client.installedAssets = append(client.installedAssets, SystemIntegrationAsset{Name: asset, Content: content})
	client.installDryRuns = append(client.installDryRuns, dryRun)
	return asset == "terminal" && client.terminalChanged, client.err
}

func (client *fakeSystemIntegrationsClient) ApplySystemIntegrations(_ context.Context, dryRun bool) (bool, bool, error) {
	client.applyDryRun = dryRun
	return client.certificate, client.lndPolicy, client.err
}

func (client *fakeSystemIntegrationsClient) FinalizeSystemIntegrations(_ context.Context, dryRun bool) error {
	client.finalizeDryRun = dryRun
	return client.err
}

func (client *fakeSystemIntegrationsClient) ServiceStatus(context.Context, string) (string, error) {
	return client.serviceStatus, client.err
}

func TestReconcileSystemIntegrationsWithBrokerUsesOrderedTypedAssets(t *testing.T) {
	assets := []SystemIntegrationAsset{{Name: "terminal", Content: "one"}, {Name: "manager_firewall", Content: "two"}}
	client := &fakeSystemIntegrationsClient{
		fakePrivilegedServiceClient: &fakePrivilegedServiceClient{mode: "enforce"},
		terminalChanged:             true,
		certificate:                 true,
		lndPolicy:                   true,
		serviceStatus:               "active",
		integrationReady:            true,
	}
	ConfigurePrivilegedClient(client)
	t.Cleanup(func() { ConfigurePrivilegedClient(nil) })

	handled, terminalChanged, certificateChanged, err := ReconcileSystemIntegrationsWithBroker(context.Background(), assets)
	if !handled || err != nil || !terminalChanged || !certificateChanged || !reflect.DeepEqual(client.installedAssets, assets) || !reflect.DeepEqual(client.installDryRuns, []bool{false, false}) || client.applyDryRun {
		t.Fatalf("unexpected reconciliation: handled=%v terminal=%v certificate=%v err=%v client=%+v", handled, terminalChanged, certificateChanged, err, client)
	}
	if handled, err := FinalizeSystemIntegrationsWithBroker(context.Background()); !handled || err != nil || client.finalizeDryRun {
		t.Fatalf("unexpected finalization: handled=%v err=%v client=%+v", handled, err, client)
	}
	if status, handled, err := ServiceStatusWithBroker(context.Background(), "lightningos-terminal"); !handled || err != nil || status != "active" {
		t.Fatalf("unexpected service status: handled=%v status=%q err=%v", handled, status, err)
	}
	if ready, handled, err := SystemIntegrationsReadyWithBroker(context.Background()); !handled || err != nil || !ready {
		t.Fatalf("unexpected integrations status: handled=%v ready=%v err=%v", handled, ready, err)
	}
}

func TestReconcileSystemIntegrationsShadowOnlyValidates(t *testing.T) {
	client := &fakeSystemIntegrationsClient{
		fakePrivilegedServiceClient: &fakePrivilegedServiceClient{mode: "shadow"},
		err:                         errors.New("shadow rejection is observational"),
	}
	ConfigurePrivilegedClient(client)
	t.Cleanup(func() { ConfigurePrivilegedClient(nil) })

	handled, terminalChanged, certificateChanged, err := ReconcileSystemIntegrationsWithBroker(context.Background(), []SystemIntegrationAsset{{Name: "terminal", Content: "one"}})
	if handled || err != nil || terminalChanged || certificateChanged || !reflect.DeepEqual(client.installDryRuns, []bool{true}) || !client.applyDryRun {
		t.Fatalf("unexpected shadow reconciliation: handled=%v terminal=%v certificate=%v err=%v client=%+v", handled, terminalChanged, certificateChanged, err, client)
	}
	if handled, err := FinalizeSystemIntegrationsWithBroker(context.Background()); handled || err != nil || !client.finalizeDryRun {
		t.Fatalf("unexpected shadow finalization: handled=%v err=%v client=%+v", handled, err, client)
	}
}
