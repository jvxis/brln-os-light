package system

import (
	"context"
	"errors"
	"testing"
)

type fakePrivilegedServiceClient struct {
	mode               string
	calls              int
	unit               string
	noBlock            bool
	dryRun             bool
	err                error
	fileCalls          int
	fileDryRun         bool
	fileErr            error
	appCalls           int
	appID              string
	action             string
	appDryRun          bool
	appErr             error
	inspectCalls       int
	inspectAppID       string
	inspectStatus      string
	inspectCPU         float64
	inspectErr         error
	removeCalls        int
	removeAppID        string
	removeDryRun       bool
	removeErr          error
	dockerCalls        int
	dockerStatusCalls  int
	dockerDryRun       bool
	dockerErr          error
	dockerStatus       string
	dockerStatusValues []string
	prepareCalls       int
	imageAppID         string
	imageVariant       string
	imageDryRun        bool
	prepareStatus      string
	prepareErr         error
	statusCalls        int
	statusValues       []string
	statusErr          error
	probeCalls         int
	probeRunnable      bool
	probeErr           error
}

func (client *fakePrivilegedServiceClient) AppLifecycle(_ context.Context, appID string, action string, dryRun bool) error {
	client.appCalls++
	client.appID = appID
	client.action = action
	client.appDryRun = dryRun
	return client.appErr
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
