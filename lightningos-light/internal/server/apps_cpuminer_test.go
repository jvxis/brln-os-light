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
		"stratum+tcp://host.docker.internal:3333",
		"${MINING_ADDRESS}.cpu-lottery",
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

func TestEnsureCpuMinerEnvCreatesAndClamps(t *testing.T) {
	dir := t.TempDir()
	paths := cpuMinerPaths{
		Root:        dir,
		ComposePath: filepath.Join(dir, "docker-compose.yaml"),
		EnvPath:     filepath.Join(dir, ".env"),
	}

	wantMax := strconv.Itoa(cpuMinerMaxThreads())
	if err := ensureCpuMinerEnv(paths, "bc1qexampleaddr", 9999); err != nil {
		t.Fatalf("ensureCpuMinerEnv create: %v", err)
	}
	if got := readEnvValue(paths.EnvPath, "MINING_ADDRESS"); got != "bc1qexampleaddr" {
		t.Fatalf("MINING_ADDRESS = %q, want bc1qexampleaddr", got)
	}
	if got := readEnvValue(paths.EnvPath, "THREADS"); got != wantMax {
		t.Fatalf("THREADS clamp = %q, want %q (max)", got, wantMax)
	}

	// Re-run keeps the address sticky and updates THREADS to the new value.
	if err := ensureCpuMinerEnv(paths, "bc1qDIFFERENT", 1); err != nil {
		t.Fatalf("ensureCpuMinerEnv update: %v", err)
	}
	if got := readEnvValue(paths.EnvPath, "MINING_ADDRESS"); got != "bc1qexampleaddr" {
		t.Fatalf("MINING_ADDRESS changed to %q, expected sticky bc1qexampleaddr", got)
	}
	if got := readEnvValue(paths.EnvPath, "THREADS"); got != "1" {
		t.Fatalf("THREADS update = %q, want 1", got)
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
