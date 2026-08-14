package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"lightningos-light/internal/system"
)

type cpuMinerPrivilegedClient struct {
	mode              string
	appCalls          int
	appID             string
	action            string
	dryRun            bool
	lifecycleErr      error
	removeCalls       int
	removeAppID       string
	removeDryRun      bool
	removeErr         error
	dockerCalls       int
	dockerStatusCalls int
	dockerDryRun      bool
	dockerErr         error
	dockerStatus      string
	packageCalls      int
	packageFeature    string
	packageDryRun     bool
	packageStatus     string
	packageErr        error
	prepareCalls      int
	preparedVariants  []string
	imageVariant      string
	imageDryRun       bool
	imageStatus       string
	imageErr          error
	statusCalls       int
	probeCalls        int
	probeResult       bool
	probeErr          error
	firewallCalls     int
	firewallAppID     string
	firewallDryRun    bool
	firewallStatus    string
	firewallErr       error
	inspectCalls      int
	inspectAppID      string
	inspectStatus     string
	inspectErr        error
	storageCalls      int
	storageDataDir    string
	storageDryRun     bool
	storageErr        error
	networkCalls      int
	networkDryRun     bool
	networkStatus     string
	networkErr        error
}

func (client *cpuMinerPrivilegedClient) EnsureBitcoinConsumerNetwork(_ context.Context, dryRun bool) (string, error) {
	client.networkCalls++
	client.networkDryRun = dryRun
	if client.networkErr != nil {
		return "", client.networkErr
	}
	if client.networkStatus != "" {
		return client.networkStatus, nil
	}
	if dryRun {
		return "validated", nil
	}
	return "ready", nil
}

func (client *cpuMinerPrivilegedClient) Mode() string { return client.mode }

func (client *cpuMinerPrivilegedClient) EnsureBitcoinCoreStorage(_ context.Context, dataDir string, dryRun bool) (string, error) {
	client.storageCalls++
	client.storageDataDir = dataDir
	client.storageDryRun = dryRun
	if client.storageErr != nil {
		return "", client.storageErr
	}
	if dryRun {
		return "validated", nil
	}
	return "ready", nil
}
func (client *cpuMinerPrivilegedClient) EnsureAppStorage(_ context.Context, dryRun bool) (string, bool, error) {
	if dryRun {
		return "validated", false, nil
	}
	return "ready", false, nil
}
func (client *cpuMinerPrivilegedClient) RestartService(context.Context, string, bool, bool) error {
	return nil
}
func (client *cpuMinerPrivilegedClient) EnableLogin(context.Context, bool) error { return nil }
func (client *cpuMinerPrivilegedClient) InspectApp(_ context.Context, appID string) (string, float64, error) {
	client.inspectCalls++
	client.inspectAppID = appID
	status := client.inspectStatus
	if status == "" {
		status = "stopped"
	}
	return status, 0, client.inspectErr
}
func (client *cpuMinerPrivilegedClient) AppLifecycle(_ context.Context, appID, action string, dryRun bool) error {
	client.appCalls++
	client.appID = appID
	client.action = action
	client.dryRun = dryRun
	return client.lifecycleErr
}

func (client *cpuMinerPrivilegedClient) RemoveApp(_ context.Context, appID string, dryRun bool) error {
	client.removeCalls++
	client.removeAppID = appID
	client.removeDryRun = dryRun
	return client.removeErr
}

func (client *cpuMinerPrivilegedClient) EnsureDockerRuntime(_ context.Context, dryRun bool) (string, error) {
	client.dockerCalls++
	client.dockerDryRun = dryRun
	return client.dockerStatus, client.dockerErr
}

func (client *cpuMinerPrivilegedClient) DockerRuntimeStatus(_ context.Context) (string, error) {
	client.dockerStatusCalls++
	return client.dockerStatus, client.dockerErr
}

func (client *cpuMinerPrivilegedClient) EnsurePackageFeature(_ context.Context, feature string, dryRun bool) (string, error) {
	client.packageCalls++
	client.packageFeature = feature
	client.packageDryRun = dryRun
	status := client.packageStatus
	if status == "" {
		status = "ready"
	}
	return status, client.packageErr
}

func (client *cpuMinerPrivilegedClient) PackageFeatureStatus(context.Context, string) (string, error) {
	status := client.packageStatus
	if status == "" {
		status = "ready"
	}
	return status, client.packageErr
}

func (client *cpuMinerPrivilegedClient) PrepareAppImage(_ context.Context, appID string, variant string, dryRun bool) (string, error) {
	client.prepareCalls++
	client.appID = appID
	client.imageVariant = variant
	client.preparedVariants = append(client.preparedVariants, variant)
	client.imageDryRun = dryRun
	return client.imageStatus, client.imageErr
}

func (client *cpuMinerPrivilegedClient) AppImageStatus(_ context.Context, appID string, variant string) (string, error) {
	client.statusCalls++
	client.appID = appID
	client.imageVariant = variant
	return client.imageStatus, client.imageErr
}

func (client *cpuMinerPrivilegedClient) ProbeAppImage(_ context.Context, appID string, variant string, dryRun bool) (bool, error) {
	client.probeCalls++
	client.appID = appID
	client.imageVariant = variant
	client.imageDryRun = dryRun
	return client.probeResult, client.probeErr
}

func (client *cpuMinerPrivilegedClient) EnsureAppFirewall(_ context.Context, appID string, dryRun bool) (string, error) {
	client.firewallCalls++
	client.firewallAppID = appID
	client.firewallDryRun = dryRun
	return client.firewallStatus, client.firewallErr
}

func TestApplyCpuMinerComposeEnforceUsesBroker(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "enforce"}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	paths := cpuMinerPaths{Root: t.TempDir(), ComposePath: filepath.Join(t.TempDir(), "untrusted.yaml")}
	if err := applyCpuMinerCompose(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	if client.appCalls != 1 || client.appID != cpuMinerAppID || client.action != "start" || client.dryRun {
		t.Fatalf("unexpected broker call: %#v", client)
	}
}

func TestApplyCpuMinerComposeEnforceFailsClosed(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "enforce", lifecycleErr: errors.New("rejected")}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := applyCpuMinerCompose(context.Background(), cpuMinerPaths{}); err == nil {
		t.Fatal("expected broker rejection to fail closed")
	}
	if client.appCalls != 1 {
		t.Fatalf("unexpected broker calls: %#v", client)
	}
}

func TestStartCpuMinerMigratesLegacyComposeAndPreservesEnv(t *testing.T) {
	root := t.TempDir()
	paths := cpuMinerPaths{
		Root:        root,
		ComposePath: filepath.Join(root, "docker-compose.yaml"),
		EnvPath:     filepath.Join(root, ".env"),
	}
	legacyCompose := "services:\n  cpuminer:\n    image: ${CPUMINER_IMAGE}\n"
	legacyEnv := "CPUMINER_IMAGE=jvx1971/cpu-lottery-miner:v1\nPOOL_MODE=brln\nMINING_ADDRESS=bc1qlegacy\nTHREADS=1\n"
	if err := os.WriteFile(paths.ComposePath, []byte(legacyCompose), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.EnvPath, []byte(legacyEnv), 0600); err != nil {
		t.Fatal(err)
	}

	client := &cpuMinerPrivilegedClient{mode: "enforce"}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := startCpuMinerAtPaths(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	compose, err := os.ReadFile(paths.ComposePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(compose), "stop_grace_period: 2s") {
		t.Fatalf("legacy compose was not migrated:\n%s", compose)
	}
	env, err := os.ReadFile(paths.EnvPath)
	if err != nil {
		t.Fatal(err)
	}
	wantEnv := strings.Replace(legacyEnv, "jvx1971/cpu-lottery-miner:v1", cpuMinerBaselineImage, 1)
	if string(env) != wantEnv {
		t.Fatalf("legacy migration changed settings other than the image:\ngot:  %q\nwant: %q", env, wantEnv)
	}
	if client.appCalls != 1 || client.appID != cpuMinerAppID || client.action != "start" || client.dryRun {
		t.Fatalf("unexpected broker call: %#v", client)
	}
}

func TestRemoveCpuMinerAppEnforceUsesBrokerBeforeDeletingFiles(t *testing.T) {
	root := t.TempDir()
	paths := cpuMinerPaths{Root: root, ComposePath: filepath.Join(root, "docker-compose.yaml"), EnvPath: filepath.Join(root, ".env")}
	if err := os.WriteFile(paths.ComposePath, []byte("manager-owned"), 0600); err != nil {
		t.Fatal(err)
	}
	client := &cpuMinerPrivilegedClient{mode: "enforce"}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := removeCpuMinerApp(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	if client.removeCalls != 1 || client.removeAppID != cpuMinerAppID || client.removeDryRun {
		t.Fatalf("unexpected broker call: %#v", client)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("app files still exist or stat failed unexpectedly: %v", err)
	}
}

func TestRemoveCpuMinerAppEnforceFailsClosedAndPreservesFiles(t *testing.T) {
	root := t.TempDir()
	paths := cpuMinerPaths{Root: root, ComposePath: filepath.Join(root, "docker-compose.yaml"), EnvPath: filepath.Join(root, ".env")}
	if err := os.WriteFile(paths.ComposePath, []byte("manager-owned"), 0600); err != nil {
		t.Fatal(err)
	}
	client := &cpuMinerPrivilegedClient{mode: "enforce", removeErr: errors.New("rejected")}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := removeCpuMinerApp(context.Background(), paths); err == nil {
		t.Fatal("expected broker rejection to fail closed")
	}
	if client.removeCalls != 1 {
		t.Fatalf("unexpected broker calls: %#v", client)
	}
	if _, err := os.Stat(paths.ComposePath); err != nil {
		t.Fatalf("app files were removed after broker failure: %v", err)
	}
}

func TestPrepareCpuMinerImageEnforceUsesBroker(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "enforce", imageStatus: "ready"}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })
	if err := prepareCpuMinerImage(context.Background(), cpuMinerBaselineImage); err != nil {
		t.Fatal(err)
	}
	if client.prepareCalls != 1 || client.appID != cpuMinerAppID || client.imageVariant != "baseline" || client.imageDryRun {
		t.Fatalf("unexpected broker call: %#v", client)
	}
}

func TestPrepareCpuMinerImageEnforceFailsClosed(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "enforce", imageErr: errors.New("pull failed")}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })
	if err := prepareCpuMinerImage(context.Background(), cpuMinerBaselineImage); err == nil {
		t.Fatal("expected broker failure")
	}
	if client.prepareCalls != 1 {
		t.Fatalf("unexpected broker calls: %#v", client)
	}
}

func TestProbeCpuMinerImageEnforceUsesBroker(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "enforce", probeResult: true}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })
	server := &Server{}
	if !server.probeCpuMinerImage(context.Background(), cpuMinerFastImages[0]) {
		t.Fatal("expected typed probe to report runnable")
	}
	if client.probeCalls != 1 || client.imageVariant != "fast_pinned" || client.imageDryRun {
		t.Fatalf("unexpected broker call: %#v", client)
	}
}

func TestCpuMinerImageBrokerHelpersRejectUnknownImage(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "enforce", imageStatus: "ready", probeResult: true}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })
	if err := prepareCpuMinerImage(context.Background(), "evil/root:latest"); err == nil {
		t.Fatal("expected unknown image to fail")
	}
	server := &Server{}
	if server.probeCpuMinerImage(context.Background(), "evil/root:latest") {
		t.Fatal("expected unknown image probe to fail")
	}
	if client.prepareCalls != 0 || client.probeCalls != 0 {
		t.Fatalf("unknown image reached broker: %#v", client)
	}
}

func TestCpuMinerComposeContents(t *testing.T) {
	contents := cpuMinerComposeContents()
	checks := []string{
		"image: ${CPUMINER_IMAGE}",
		"stop_grace_period: 2s",
		"127.0.0.1:4048:4048",
		"stratum+tcp://${STRATUM_HOST}:${STRATUM_PORT}",
		"${MINING_ADDRESS}.${WORKER_NAME}",
		`cpus: "${THREADS}"`,
		"cpu_shares: 128",
		`user: "65534:65534"`,
		"read_only: true",
		"cap_drop:",
		"security_opt:",
		"no-new-privileges:true",
		"/tmp:rw,noexec,nosuid,nodev,size=16m,mode=1777",
		"- \"cpuminer\"",
		"--algo",
		"sha256d",
		"--api-bind",
	}
	for _, want := range checks {
		if !strings.Contains(contents, want) {
			t.Fatalf("compose contents missing %q:\n%s", want, contents)
		}
	}
}

func TestWriteCpuMinerEnv(t *testing.T) {
	dir := t.TempDir()
	paths := cpuMinerPaths{
		Root:        dir,
		ComposePath: filepath.Join(dir, "docker-compose.yaml"),
		EnvPath:     filepath.Join(dir, ".env"),
	}

	// BR-LN pool, clamped threads, sanitized worker.
	if err := writeCpuMinerEnv(paths, cpuMinerConfig{
		Address: "bc1qexampleaddr",
		Worker:  "my rig.01",
		Pool:    cpuMinerPoolPreset(cpuMinerPoolBRLN),
		Threads: 9999,
	}); err != nil {
		t.Fatalf("writeCpuMinerEnv create: %v", err)
	}
	if got := readEnvValue(paths.EnvPath, "POOL_MODE"); got != "brln" {
		t.Fatalf("POOL_MODE = %q, want brln", got)
	}
	if got := readEnvValue(paths.EnvPath, "STRATUM_HOST"); got != cpuMinerBRLNStratumHost {
		t.Fatalf("STRATUM_HOST = %q, want %q", got, cpuMinerBRLNStratumHost)
	}
	if got := readEnvValue(paths.EnvPath, "STRATUM_PORT"); got != "3332" {
		t.Fatalf("STRATUM_PORT = %q, want 3332", got)
	}
	if got := readEnvValue(paths.EnvPath, "WORKER_NAME"); got != "myrig01" {
		t.Fatalf("WORKER_NAME = %q, want myrig01 (sanitized)", got)
	}
	if got := readEnvValue(paths.EnvPath, "THREADS"); got != strconv.Itoa(cpuMinerMaxThreads()) {
		t.Fatalf("THREADS = %q, want clamp to max %d", got, cpuMinerMaxThreads())
	}

	// Overwrite to local pool.
	if err := writeCpuMinerEnv(paths, cpuMinerConfig{
		Address: "bc1qexampleaddr",
		Worker:  "cpu-lottery",
		Pool:    cpuMinerPoolPreset(cpuMinerPoolLocal),
		Threads: 1,
	}); err != nil {
		t.Fatalf("writeCpuMinerEnv update: %v", err)
	}
	if got := readEnvValue(paths.EnvPath, "POOL_MODE"); got != "local" {
		t.Fatalf("POOL_MODE = %q, want local", got)
	}
	if got := readEnvValue(paths.EnvPath, "STRATUM_HOST"); got != cpuMinerLocalStratumHost {
		t.Fatalf("STRATUM_HOST = %q, want %q", got, cpuMinerLocalStratumHost)
	}
}

func TestValidateCpuMinerAddress(t *testing.T) {
	valid := []string{"bc1qexampleaddr0000", "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2", "3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy"}
	for _, a := range valid {
		if err := validateCpuMinerAddress(a); err != nil {
			t.Fatalf("validateCpuMinerAddress(%q) = %v, want nil", a, err)
		}
	}
	invalid := []string{"", "short", "xyz1qnotbitcoin000000", "bc1q bad space 0000"}
	for _, a := range invalid {
		if err := validateCpuMinerAddress(a); err == nil {
			t.Fatalf("validateCpuMinerAddress(%q) = nil, want error", a)
		}
	}
}

func TestSanitizeCpuMinerWorker(t *testing.T) {
	cases := map[string]string{
		"cpu-lottery": "cpu-lottery",
		"my rig.01":   "myrig01",
		"":            cpuMinerWorkerTag,
		"@@@":         cpuMinerWorkerTag,
	}
	for in, want := range cases {
		if got := sanitizeCpuMinerWorker(in); got != want {
			t.Fatalf("sanitizeCpuMinerWorker(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseCpuMinerSummary(t *testing.T) {
	raw := "NAME=cpuminer-opt;VER=3.21;API=1.0;ALGO=sha256d;CPUS=1;KHS=12.50;ACC=7;REJ=1;DIFF=1024|"
	fields := parseCpuMinerSummary(raw)
	if fields["KHS"] != "12.50" {
		t.Fatalf("KHS = %q, want 12.50", fields["KHS"])
	}
	if fields["ACC"] != "7" {
		t.Fatalf("ACC = %q, want 7", fields["ACC"])
	}
	if fields["REJ"] != "1" {
		t.Fatalf("REJ = %q, want 1", fields["REJ"])
	}
}

func TestParseDockerCPUPercent(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want float64
		ok   bool
	}{
		{name: "plain", out: "198.42%\n", want: 198.42, ok: true},
		{name: "decimal comma", out: "12,50%\n", want: 12.5, ok: true},
		{name: "compose warning", out: "warning: legacy compose format\n87.25%\n", want: 87.25, ok: true},
		{name: "missing", out: "--", ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseDockerCPUPercent(tc.out)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("parseDockerCPUPercent(%q) = (%v, %v), want (%v, %v)", tc.out, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestNormalizeHostCPUPercent(t *testing.T) {
	tests := []struct {
		name    string
		perCore float64
		cpus    int
		want    float64
	}{
		{name: "two threads on sixteen CPUs", perCore: 200, cpus: 16, want: 12.5},
		{name: "one full node", perCore: 1600, cpus: 16, want: 100},
		{name: "clamp scheduler overhead", perCore: 1700, cpus: 16, want: 100},
		{name: "invalid CPU count", perCore: 100, cpus: 0, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeHostCPUPercent(tc.perCore, tc.cpus); got != tc.want {
				t.Fatalf("normalizeHostCPUPercent(%v, %d) = %v, want %v", tc.perCore, tc.cpus, got, tc.want)
			}
		})
	}
}

func TestParseComposeContainerIDIgnoresWarnings(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	out := "time=2026-08-01T12:00:00Z level=warning msg=legacy\n" + id + "\n"
	if got := parseComposeContainerID(out); got != id {
		t.Fatalf("parseComposeContainerID() = %q, want %q", got, id)
	}
	if got := parseComposeContainerID("warning only\n"); got != "" {
		t.Fatalf("parseComposeContainerID(warning) = %q, want empty", got)
	}
}

func TestParseContainerCPUCounter(t *testing.T) {
	v2 := containerCPUCounter{path: "/sys/fs/cgroup/cpu.stat"}
	if got, ok := parseContainerCPUCounter("usage_usec 123456\nuser_usec 120000\nsystem_usec 3456\n", v2); !ok || got != 123456 {
		t.Fatalf("cgroup v2 counter = (%d, %v), want (123456, true)", got, ok)
	}
	v1 := containerCPUCounter{path: "/sys/fs/cgroup/cpuacct/cpuacct.usage"}
	if got, ok := parseContainerCPUCounter("987654321\n", v1); !ok || got != 987654321 {
		t.Fatalf("cgroup v1 counter = (%d, %v), want (987654321, true)", got, ok)
	}
}

func TestCpuFlagsLineHas(t *testing.T) {
	// No AVX (constrained VM like LOS-TEST).
	noAVX := "processor\t: 0\nflags\t\t: fpu vme sse2 ssse3 sse4_1 sse4_2 aes sha_ni\n"
	if cpuFlagsLineHas(noAVX, "avx") {
		t.Fatalf("avx should not be detected on no-AVX cpuinfo")
	}
	if !cpuFlagsLineHas(noAVX, "sha_ni") {
		t.Fatalf("sha_ni should be detected")
	}
	// Modern CPU with AVX2 but no AVX512: 'avx' must match, not as a substring of avx512.
	avx2 := "flags : fpu sse2 avx avx2 fma bmi1 bmi2\n"
	if !cpuFlagsLineHas(avx2, "avx") {
		t.Fatalf("avx should be detected")
	}
	if !cpuFlagsLineHas(avx2, "avx2") {
		t.Fatalf("avx2 should be detected")
	}
	if cpuFlagsLineHas(avx2, "avx512f") {
		t.Fatalf("avx512f should not be detected as a substring")
	}
}

func TestCpuMinerRegisteredInStore(t *testing.T) {
	s := &Server{}
	apps, err := s.appRegistry()
	if err != nil {
		t.Fatalf("appRegistry: %v", err)
	}
	found := false
	for _, app := range apps {
		if app.Definition().ID == cpuMinerAppID {
			found = true
			if app.Definition().Port != 0 {
				t.Fatalf("cpuminer port = %d, want 0", app.Definition().Port)
			}
		}
	}
	if !found {
		t.Fatalf("cpuminer app not found in registry")
	}
}
