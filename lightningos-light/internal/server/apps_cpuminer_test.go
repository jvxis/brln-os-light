package server

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCpuMinerComposeContents(t *testing.T) {
	contents := cpuMinerComposeContents("cniweb/cpuminer-opt:latest")
	checks := []string{
		"image: cniweb/cpuminer-opt:latest",
		"127.0.0.1:4048:4048",
		"stratum+tcp://${STRATUM_HOST}:${STRATUM_PORT}",
		"${MINING_ADDRESS}.${WORKER_NAME}",
		`cpus: "${THREADS}"`,
		"cpu_shares: 128",
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
