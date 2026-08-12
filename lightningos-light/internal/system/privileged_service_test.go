package system

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakePrivilegedServiceClient struct {
	mode                string
	calls               int
	unit                string
	noBlock             bool
	dryRun              bool
	err                 error
	fileCalls           int
	fileDryRun          bool
	fileErr             error
	appCalls            int
	appID               string
	action              string
	appDryRun           bool
	appErr              error
	snapshotCalls       int
	snapshotAppID       string
	snapshotDryRun      bool
	snapshotErr         error
	inspectCalls        int
	inspectAppID        string
	inspectStatus       string
	inspectCPU          float64
	inspectErr          error
	removeCalls         int
	removeAppID         string
	removeDryRun        bool
	removeErr           error
	adminResetCalls     int
	adminResetAppID     string
	adminResetDryRun    bool
	adminResetErr       error
	dockerCalls         int
	dockerStatusCalls   int
	dockerDryRun        bool
	dockerErr           error
	dockerStatus        string
	dockerStatusValues  []string
	packageCalls        int
	packageStatusCalls  int
	packageFeature      string
	packageDryRun       bool
	packageStatus       string
	packageEnsureValues []string
	packageValues       []string
	packageErr          error
	prepareCalls        int
	imageAppID          string
	imageVariant        string
	imageDryRun         bool
	prepareStatus       string
	prepareErr          error
	statusCalls         int
	statusValues        []string
	statusErr           error
	probeCalls          int
	probeRunnable       bool
	probeErr            error
	firewallCalls       int
	firewallAppID       string
	firewallDryRun      bool
	firewallStatus      string
	firewallErr         error
	storageCalls        int
	storageDataDir      string
	storageDryRun       bool
	storageStatus       string
	storageErr          error
	networkCalls        int
	networkDryRun       bool
	networkStatus       string
	networkErr          error
	configCalls         int
	configOperation     string
	configDataDir       string
	configContent       string
	configDryRun        bool
	configStatus        string
	configErr           error
	bitcoinStatusCalls  int
	bitcoinStatusJSON   string
	bitcoinStatusErr    error
}

func (client *fakePrivilegedServiceClient) EnsureBitcoinConsumerNetwork(_ context.Context, dryRun bool) (string, error) {
	client.networkCalls++
	client.networkDryRun = dryRun
	status := client.networkStatus
	if status == "" {
		if dryRun {
			status = "validated"
		} else {
			status = "ready"
		}
	}
	return status, client.networkErr
}

func (client *fakePrivilegedServiceClient) AppLifecycle(_ context.Context, appID string, action string, dryRun bool) error {
	client.appCalls++
	client.appID = appID
	client.action = action
	client.appDryRun = dryRun
	return client.appErr
}

func (client *fakePrivilegedServiceClient) SnapshotApp(_ context.Context, appID string, dryRun bool) error {
	client.snapshotCalls++
	client.snapshotAppID = appID
	client.snapshotDryRun = dryRun
	return client.snapshotErr
}

func (client *fakePrivilegedServiceClient) InspectApp(_ context.Context, appID string) (string, float64, error) {
	client.inspectCalls++
	client.inspectAppID = appID
	return client.inspectStatus, client.inspectCPU, client.inspectErr
}

func (client *fakePrivilegedServiceClient) RemoveApp(_ context.Context, appID string, dryRun bool) error {
	client.removeCalls++
	client.removeAppID = appID
	client.removeDryRun = dryRun
	return client.removeErr
}

func (client *fakePrivilegedServiceClient) ResetAppAdmin(_ context.Context, appID string, dryRun bool) error {
	client.adminResetCalls++
	client.adminResetAppID = appID
	client.adminResetDryRun = dryRun
	return client.adminResetErr
}

func (client *fakePrivilegedServiceClient) EnsureDockerRuntime(_ context.Context, dryRun bool) (string, error) {
	client.dockerCalls++
	client.dockerDryRun = dryRun
	return client.dockerStatus, client.dockerErr
}

func (client *fakePrivilegedServiceClient) DockerRuntimeStatus(_ context.Context) (string, error) {
	client.dockerStatusCalls++
	if client.dockerErr != nil {
		return "", client.dockerErr
	}
	if len(client.dockerStatusValues) == 0 {
		return client.dockerStatus, nil
	}
	status := client.dockerStatusValues[0]
	client.dockerStatusValues = client.dockerStatusValues[1:]
	return status, nil
}

func (client *fakePrivilegedServiceClient) EnsurePackageFeature(_ context.Context, feature string, dryRun bool) (string, error) {
	client.packageCalls++
	client.packageFeature = feature
	client.packageDryRun = dryRun
	if client.packageErr != nil {
		return "", client.packageErr
	}
	if len(client.packageEnsureValues) == 0 {
		return client.packageStatus, nil
	}
	status := client.packageEnsureValues[0]
	client.packageEnsureValues = client.packageEnsureValues[1:]
	return status, nil
}

func (client *fakePrivilegedServiceClient) PackageFeatureStatus(_ context.Context, feature string) (string, error) {
	client.packageStatusCalls++
	client.packageFeature = feature
	if client.packageErr != nil {
		return "", client.packageErr
	}
	if len(client.packageValues) == 0 {
		return client.packageStatus, nil
	}
	status := client.packageValues[0]
	client.packageValues = client.packageValues[1:]
	return status, nil
}

func (client *fakePrivilegedServiceClient) PrepareAppImage(_ context.Context, appID string, variant string, dryRun bool) (string, error) {
	client.prepareCalls++
	client.imageAppID = appID
	client.imageVariant = variant
	client.imageDryRun = dryRun
	return client.prepareStatus, client.prepareErr
}

func (client *fakePrivilegedServiceClient) AppImageStatus(_ context.Context, appID string, variant string) (string, error) {
	client.statusCalls++
	client.imageAppID = appID
	client.imageVariant = variant
	if client.statusErr != nil {
		return "", client.statusErr
	}
	if len(client.statusValues) == 0 {
		return "absent", nil
	}
	status := client.statusValues[0]
	client.statusValues = client.statusValues[1:]
	return status, nil
}

func (client *fakePrivilegedServiceClient) ProbeAppImage(_ context.Context, appID string, variant string, dryRun bool) (bool, error) {
	client.probeCalls++
	client.imageAppID = appID
	client.imageVariant = variant
	client.imageDryRun = dryRun
	return client.probeRunnable, client.probeErr
}

func (client *fakePrivilegedServiceClient) EnsureAppFirewall(_ context.Context, appID string, dryRun bool) (string, error) {
	client.firewallCalls++
	client.firewallAppID = appID
	client.firewallDryRun = dryRun
	return client.firewallStatus, client.firewallErr
}

func (client *fakePrivilegedServiceClient) EnableLogin(_ context.Context, dryRun bool) error {
	client.fileCalls++
	client.fileDryRun = dryRun
	return client.fileErr
}

func (client *fakePrivilegedServiceClient) Mode() string { return client.mode }

func (client *fakePrivilegedServiceClient) EnsureBitcoinCoreStorage(_ context.Context, dataDir string, dryRun bool) (string, error) {
	client.storageCalls++
	client.storageDataDir = dataDir
	client.storageDryRun = dryRun
	status := client.storageStatus
	if status == "" {
		if dryRun {
			status = "validated"
		} else {
			status = "ready"
		}
	}
	return status, client.storageErr
}

func (client *fakePrivilegedServiceClient) EnsureBitcoinCoreConfig(_ context.Context, dataDir string, content string, _ bool, dryRun bool) (string, error) {
	client.recordBitcoinCoreConfig("ensure", dataDir, content, dryRun)
	return client.bitcoinCoreConfigStatus(dryRun), client.configErr
}

func (client *fakePrivilegedServiceClient) ReadBitcoinCoreCredentials(context.Context, string) (string, string, error) {
	return "lightningos", strings.Repeat("a", 64), nil
}

func (client *fakePrivilegedServiceClient) ReadBitcoinCoreConfig(_ context.Context, dataDir string) (string, error) {
	content := client.configContent
	client.recordBitcoinCoreConfig("read", dataDir, "", false)
	return content, client.configErr
}

func (client *fakePrivilegedServiceClient) BitcoinCoreStatus(_ context.Context) (string, error) {
	client.bitcoinStatusCalls++
	return client.bitcoinStatusJSON, client.bitcoinStatusErr
}

func (client *fakePrivilegedServiceClient) WriteBitcoinCoreConfig(_ context.Context, dataDir string, content string, dryRun bool) (string, error) {
	client.recordBitcoinCoreConfig("write", dataDir, content, dryRun)
	return client.bitcoinCoreConfigStatus(dryRun), client.configErr
}

func (client *fakePrivilegedServiceClient) recordBitcoinCoreConfig(operation string, dataDir string, content string, dryRun bool) {
	client.configCalls++
	client.configOperation = operation
	client.configDataDir = dataDir
	client.configContent = content
	client.configDryRun = dryRun
}

func (client *fakePrivilegedServiceClient) bitcoinCoreConfigStatus(dryRun bool) string {
	if client.configStatus != "" {
		return client.configStatus
	}
	if dryRun {
		return "validated"
	}
	return "ready"
}

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

func TestAppLifecycleWithBrokerModes(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		appErr      error
		wantHandled bool
		wantError   bool
		wantCalls   int
		wantDryRun  bool
	}{
		{name: "disabled", mode: "disabled"},
		{name: "shadow", mode: "shadow", appErr: errors.New("rejected"), wantCalls: 1, wantDryRun: true},
		{name: "enforce", mode: "enforce", wantHandled: true, wantCalls: 1},
		{name: "enforce error", mode: "enforce", appErr: errors.New("failed"), wantHandled: true, wantError: true, wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakePrivilegedServiceClient{mode: test.mode, appErr: test.appErr}
			ConfigurePrivilegedClient(client)
			t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
			handled, err := AppLifecycleWithBroker(context.Background(), "cpuminer", "start")
			if handled != test.wantHandled || (err != nil) != test.wantError {
				t.Fatalf("handled/error = %v/%v", handled, err)
			}
			if client.appCalls != test.wantCalls || client.appDryRun != test.wantDryRun {
				t.Fatalf("unexpected app call: %#v", client)
			}
			if test.wantCalls == 1 && (client.appID != "cpuminer" || client.action != "start") {
				t.Fatalf("unexpected typed app params: %#v", client)
			}
		})
	}
}

func TestSnapshotAppWithBrokerModes(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		snapshotErr error
		wantHandled bool
		wantError   bool
		wantCalls   int
		wantDryRun  bool
	}{
		{name: "disabled", mode: "disabled"},
		{name: "shadow", mode: "shadow", snapshotErr: errors.New("rejected"), wantCalls: 1, wantDryRun: true},
		{name: "enforce", mode: "enforce", wantHandled: true, wantCalls: 1},
		{name: "enforce error", mode: "enforce", snapshotErr: errors.New("failed"), wantHandled: true, wantError: true, wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakePrivilegedServiceClient{mode: test.mode, snapshotErr: test.snapshotErr}
			ConfigurePrivilegedClient(client)
			t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
			handled, err := SnapshotAppWithBroker(context.Background(), "btcpay")
			if handled != test.wantHandled || (err != nil) != test.wantError {
				t.Fatalf("handled/error = %v/%v", handled, err)
			}
			if client.snapshotCalls != test.wantCalls || client.snapshotDryRun != test.wantDryRun {
				t.Fatalf("unexpected snapshot call: %#v", client)
			}
			if test.wantCalls == 1 && client.snapshotAppID != "btcpay" {
				t.Fatalf("unexpected typed snapshot params: %#v", client)
			}
		})
	}
}

func TestEnsureAppFirewallWithBrokerModes(t *testing.T) {
	for _, test := range []struct {
		name        string
		mode        string
		firewallErr error
		wantHandled bool
		wantError   bool
		wantCalls   int
		wantDryRun  bool
	}{
		{name: "disabled", mode: "disabled"},
		{name: "shadow", mode: "shadow", firewallErr: errors.New("rejected"), wantCalls: 1, wantDryRun: true},
		{name: "enforce", mode: "enforce", wantHandled: true, wantCalls: 1},
		{name: "enforce error", mode: "enforce", firewallErr: errors.New("failed"), wantHandled: true, wantError: true, wantCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakePrivilegedServiceClient{mode: test.mode, firewallStatus: "active", firewallErr: test.firewallErr}
			ConfigurePrivilegedClient(client)
			t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
			handled, status, err := EnsureAppFirewallWithBroker(context.Background(), "robosats")
			if handled != test.wantHandled || (err != nil) != test.wantError {
				t.Fatalf("handled/status/error = %v/%q/%v", handled, status, err)
			}
			if client.firewallCalls != test.wantCalls || client.firewallDryRun != test.wantDryRun {
				t.Fatalf("unexpected firewall call: %#v", client)
			}
			if test.wantCalls == 1 && client.firewallAppID != "robosats" {
				t.Fatalf("unexpected typed app params: %#v", client)
			}
		})
	}
}

func TestInspectAppWithBrokerModes(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		inspectErr  error
		wantHandled bool
		wantError   bool
		wantCalls   int
	}{
		{name: "disabled", mode: "disabled"},
		{name: "shadow", mode: "shadow", inspectErr: errors.New("rejected"), wantCalls: 1},
		{name: "enforce", mode: "enforce", wantHandled: true, wantCalls: 1},
		{name: "enforce error", mode: "enforce", inspectErr: errors.New("failed"), wantHandled: true, wantError: true, wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakePrivilegedServiceClient{mode: test.mode, inspectStatus: "running", inspectCPU: 99.5, inspectErr: test.inspectErr}
			ConfigurePrivilegedClient(client)
			t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
			handled, status, cpu, err := InspectAppWithBroker(context.Background(), "cpuminer")
			if handled != test.wantHandled || (err != nil) != test.wantError {
				t.Fatalf("handled/error = %v/%v", handled, err)
			}
			if client.inspectCalls != test.wantCalls {
				t.Fatalf("unexpected inspect calls: %#v", client)
			}
			if test.wantHandled && !test.wantError && (status != "running" || cpu != 99.5) {
				t.Fatalf("unexpected inspection = %q/%v", status, cpu)
			}
		})
	}
}

func TestRemoveAppWithBrokerModes(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		removeErr   error
		wantHandled bool
		wantError   bool
		wantCalls   int
		wantDryRun  bool
	}{
		{name: "disabled", mode: "disabled"},
		{name: "shadow", mode: "shadow", removeErr: errors.New("rejected"), wantCalls: 1, wantDryRun: true},
		{name: "enforce", mode: "enforce", wantHandled: true, wantCalls: 1},
		{name: "enforce error", mode: "enforce", removeErr: errors.New("failed"), wantHandled: true, wantError: true, wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakePrivilegedServiceClient{mode: test.mode, removeErr: test.removeErr}
			ConfigurePrivilegedClient(client)
			t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
			handled, err := RemoveAppWithBroker(context.Background(), "cpuminer")
			if handled != test.wantHandled || (err != nil) != test.wantError {
				t.Fatalf("handled/error = %v/%v", handled, err)
			}
			if client.removeCalls != test.wantCalls || client.removeDryRun != test.wantDryRun {
				t.Fatalf("unexpected remove call: %#v", client)
			}
			if test.wantCalls == 1 && client.removeAppID != "cpuminer" {
				t.Fatalf("unexpected typed app params: %#v", client)
			}
		})
	}
}

func TestResetAppAdminWithBrokerModes(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		resetErr    error
		wantHandled bool
		wantError   bool
		wantCalls   int
		wantDryRun  bool
	}{
		{name: "disabled", mode: "disabled"},
		{name: "shadow", mode: "shadow", resetErr: errors.New("rejected"), wantCalls: 1, wantDryRun: true},
		{name: "enforce", mode: "enforce", wantHandled: true, wantCalls: 1},
		{name: "enforce error", mode: "enforce", resetErr: errors.New("failed"), wantHandled: true, wantError: true, wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakePrivilegedServiceClient{mode: test.mode, adminResetErr: test.resetErr}
			ConfigurePrivilegedClient(client)
			t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
			handled, err := ResetAppAdminWithBroker(context.Background(), "lndg")
			if handled != test.wantHandled || (err != nil) != test.wantError {
				t.Fatalf("handled/error = %v/%v", handled, err)
			}
			if client.adminResetCalls != test.wantCalls || client.adminResetDryRun != test.wantDryRun {
				t.Fatalf("unexpected reset call: %#v", client)
			}
			if test.wantCalls == 1 && client.adminResetAppID != "lndg" {
				t.Fatalf("unexpected typed app params: %#v", client)
			}
		})
	}
}

func TestEnsureDockerRuntimeWithBrokerModes(t *testing.T) {
	for _, test := range []struct {
		name         string
		mode         string
		dockerErr    error
		dockerStatus string
		wantHandled  bool
		wantError    bool
		wantCalls    int
		wantDryRun   bool
	}{
		{name: "disabled", mode: "disabled"},
		{name: "shadow", mode: "shadow", dockerErr: errors.New("rejected"), wantCalls: 1, wantDryRun: true},
		{name: "enforce", mode: "enforce", dockerStatus: "ready", wantHandled: true, wantCalls: 1},
		{name: "enforce error", mode: "enforce", dockerErr: errors.New("missing"), wantHandled: true, wantError: true, wantCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakePrivilegedServiceClient{mode: test.mode, dockerErr: test.dockerErr, dockerStatus: test.dockerStatus}
			ConfigurePrivilegedClient(client)
			t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
			handled, err := EnsureDockerRuntimeWithBroker(context.Background())
			if handled != test.wantHandled || (err != nil) != test.wantError || client.dockerCalls != test.wantCalls || client.dockerDryRun != test.wantDryRun {
				t.Fatalf("handled/error/client=%v/%v/%#v", handled, err, client)
			}
		})
	}
}

func TestEnsurePackageFeatureWithBrokerStagesAndModes(t *testing.T) {
	client := &fakePrivilegedServiceClient{
		mode:                "enforce",
		packageEnsureValues: []string{"indexing", "installing", "ready"},
		packageValues:       []string{"indexed", "ready"},
	}
	ConfigurePrivilegedClient(client)
	t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
	handled, err := EnsurePackageFeatureWithBroker(context.Background(), "docker_runtime")
	if !handled || err != nil || client.packageCalls != 3 || client.packageStatusCalls != 2 || client.packageFeature != "docker_runtime" {
		t.Fatalf("handled/error/client=%v/%v/%#v", handled, err, client)
	}

	shadow := &fakePrivilegedServiceClient{mode: "shadow", packageErr: errors.New("rejected")}
	ConfigurePrivilegedClient(shadow)
	handled, err = EnsurePackageFeatureWithBroker(context.Background(), "docker_runtime")
	if handled || err != nil || shadow.packageCalls != 1 || !shadow.packageDryRun {
		t.Fatalf("shadow handled/error/client=%v/%v/%#v", handled, err, shadow)
	}
}

func TestPrepareAppImageWithBrokerWaitsForReady(t *testing.T) {
	client := &fakePrivilegedServiceClient{mode: "enforce", prepareStatus: "preparing", statusValues: []string{"preparing", "ready"}}
	ConfigurePrivilegedClient(client)
	t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
	handled, err := PrepareAppImageWithBroker(context.Background(), "cpuminer", "baseline")
	if !handled || err != nil || client.prepareCalls != 1 || client.statusCalls != 2 || client.imageAppID != "cpuminer" || client.imageVariant != "baseline" {
		t.Fatalf("handled/error/client=%v/%v/%#v", handled, err, client)
	}
}

func TestEnsureDockerRuntimeWithBrokerWaitsForReady(t *testing.T) {
	client := &fakePrivilegedServiceClient{mode: "enforce", dockerStatus: "starting", dockerStatusValues: []string{"starting", "ready"}}
	ConfigurePrivilegedClient(client)
	t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
	handled, err := EnsureDockerRuntimeWithBroker(context.Background())
	if !handled || err != nil || client.dockerCalls != 1 || client.dockerStatusCalls != 2 {
		t.Fatalf("handled/error/client=%v/%v/%#v", handled, err, client)
	}
}

func TestPrepareAppImageWithBrokerModesAndFailure(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		prepare     string
		status      []string
		wantHandled bool
		wantError   bool
		wantDryRun  bool
	}{
		{name: "disabled", mode: "disabled"},
		{name: "shadow", mode: "shadow", prepare: "validated", wantDryRun: true},
		{name: "ready", mode: "enforce", prepare: "ready", wantHandled: true},
		{name: "failed", mode: "enforce", prepare: "preparing", status: []string{"failed"}, wantHandled: true, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakePrivilegedServiceClient{mode: test.mode, prepareStatus: test.prepare, statusValues: test.status}
			ConfigurePrivilegedClient(client)
			t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
			handled, err := PrepareAppImageWithBroker(context.Background(), "cpuminer", "baseline")
			if handled != test.wantHandled || (err != nil) != test.wantError || client.imageDryRun != test.wantDryRun {
				t.Fatalf("handled/error/client=%v/%v/%#v", handled, err, client)
			}
		})
	}
}

func TestProbeAppImageWithBrokerModes(t *testing.T) {
	for _, test := range []struct {
		name         string
		mode         string
		runnable     bool
		wantHandled  bool
		wantRunnable bool
		wantCalls    int
		wantDryRun   bool
	}{
		{name: "disabled", mode: "disabled"},
		{name: "shadow", mode: "shadow", wantCalls: 1, wantDryRun: true},
		{name: "enforce", mode: "enforce", runnable: true, wantHandled: true, wantRunnable: true, wantCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakePrivilegedServiceClient{mode: test.mode, probeRunnable: test.runnable}
			ConfigurePrivilegedClient(client)
			t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
			handled, runnable, err := ProbeAppImageWithBroker(context.Background(), "cpuminer", "fast_pinned")
			if err != nil || handled != test.wantHandled || runnable != test.wantRunnable || client.probeCalls != test.wantCalls || client.imageDryRun != test.wantDryRun {
				t.Fatalf("handled/runnable/error/client=%v/%v/%v/%#v", handled, runnable, err, client)
			}
		})
	}
}

func TestEnsureBitcoinCoreStorageWithBrokerModes(t *testing.T) {
	for _, test := range []struct {
		name        string
		mode        string
		status      string
		wantHandled bool
		wantCalls   int
		wantDryRun  bool
		wantError   bool
	}{
		{name: "disabled", mode: "disabled"},
		{name: "shadow", mode: "shadow", wantCalls: 1, wantDryRun: true},
		{name: "enforce", mode: "enforce", status: "ready", wantHandled: true, wantCalls: 1},
		{name: "invalid state", mode: "enforce", status: "validated", wantHandled: true, wantCalls: 1, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakePrivilegedServiceClient{mode: test.mode, storageStatus: test.status}
			ConfigurePrivilegedClient(client)
			t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
			handled, err := EnsureBitcoinCoreStorageWithBroker(context.Background(), "/mnt/bitcoin-ssd/bitcoin")
			if handled != test.wantHandled || (err != nil) != test.wantError || client.storageCalls != test.wantCalls || client.storageDryRun != test.wantDryRun || (client.storageCalls > 0 && client.storageDataDir != "/mnt/bitcoin-ssd/bitcoin") {
				t.Fatalf("handled/error/client=%v/%v/%#v", handled, err, client)
			}
		})
	}
}

func TestEnsureBitcoinConsumerNetworkWithBrokerModes(t *testing.T) {
	for _, test := range []struct {
		name        string
		mode        string
		status      string
		brokerErr   error
		wantHandled bool
		wantErr     bool
		wantCalls   int
		wantDryRun  bool
	}{
		{name: "enforce", mode: "enforce", status: "ready", wantHandled: true, wantCalls: 1},
		{name: "enforce failure", mode: "enforce", brokerErr: errors.New("rejected"), wantHandled: true, wantErr: true, wantCalls: 1},
		{name: "enforce invalid state", mode: "enforce", status: "invalid", wantHandled: true, wantErr: true, wantCalls: 1},
		{name: "shadow", mode: "shadow", status: "validated", wantCalls: 1, wantDryRun: true},
		{name: "disabled", mode: "disabled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakePrivilegedServiceClient{mode: test.mode, networkStatus: test.status, networkErr: test.brokerErr}
			ConfigurePrivilegedClient(client)
			t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
			handled, err := EnsureBitcoinConsumerNetworkWithBroker(context.Background())
			if handled != test.wantHandled || (err != nil) != test.wantErr || client.networkCalls != test.wantCalls || client.networkDryRun != test.wantDryRun {
				t.Fatalf("handled/error/client=%v/%v/%#v", handled, err, client)
			}
		})
	}
}

func TestBitcoinCoreConfigBrokerHelpersAreEnforceOnly(t *testing.T) {
	const dataDir = "/mnt/bitcoin-ssd/bitcoin"
	const content = "server=1\nrpcpassword=secret\n"

	t.Run("shadow validates ensure without handling", func(t *testing.T) {
		client := &fakePrivilegedServiceClient{mode: "shadow"}
		ConfigurePrivilegedClient(client)
		t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
		handled, err := EnsureBitcoinCoreConfigWithBroker(context.Background(), dataDir, content, true)
		if err != nil || handled || client.configCalls != 1 || client.configOperation != "ensure" || !client.configDryRun || client.configContent != content {
			t.Fatalf("handled/error/client=%v/%v/%#v", handled, err, client)
		}
	})

	t.Run("enforce reads and writes", func(t *testing.T) {
		client := &fakePrivilegedServiceClient{mode: "enforce", configContent: content}
		ConfigurePrivilegedClient(client)
		t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
		read, handled, err := ReadBitcoinCoreConfigWithBroker(context.Background(), dataDir)
		if err != nil || !handled || read != content || client.configOperation != "read" {
			t.Fatalf("read/handled/error/client=%q/%v/%v/%#v", read, handled, err, client)
		}
		handled, err = WriteBitcoinCoreConfigWithBroker(context.Background(), dataDir, content)
		if err != nil || !handled || client.configOperation != "write" || client.configDryRun || client.configContent != content {
			t.Fatalf("handled/error/client=%v/%v/%#v", handled, err, client)
		}
	})
}

func TestBitcoinCoreStatusBrokerHelperUsesReadOnlyCapability(t *testing.T) {
	const statusJSON = `{"chain":"main","blocks":100}`
	for _, test := range []struct {
		name        string
		mode        string
		brokerErr   error
		wantHandled bool
		wantErr     bool
		wantCalls   int
	}{
		{name: "enforce", mode: "enforce", wantHandled: true, wantCalls: 1},
		{name: "enforce failure", mode: "enforce", brokerErr: errors.New("unavailable"), wantHandled: true, wantErr: true, wantCalls: 1},
		{name: "shadow preserves compatibility status", mode: "shadow"},
		{name: "disabled", mode: "disabled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakePrivilegedServiceClient{mode: test.mode, bitcoinStatusJSON: statusJSON, bitcoinStatusErr: test.brokerErr}
			ConfigurePrivilegedClient(client)
			t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
			raw, handled, err := ReadBitcoinCoreStatusWithBroker(context.Background())
			if handled != test.wantHandled || (err != nil) != test.wantErr || client.bitcoinStatusCalls != test.wantCalls {
				t.Fatalf("raw/handled/error/client=%q/%v/%v/%#v", raw, handled, err, client)
			}
			if test.wantHandled && !test.wantErr && raw != statusJSON {
				t.Fatalf("status=%q want=%q", raw, statusJSON)
			}
		})
	}
}
