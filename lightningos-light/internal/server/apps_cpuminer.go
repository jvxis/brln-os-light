package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/system"
)

const (
	cpuMinerAppID          = "cpuminer"
	cpuMinerAPIPort        = 4048
	cpuMinerWorkerTag      = "cpu-lottery"
	cpuMinerDefaultThreads = 1

	// cpuMinerBaselineImage is our own image, built for a baseline x86-64 target
	// (see docker/cpu-lottery-miner/Dockerfile). It runs on ANY amd64 CPU,
	// including constrained VMs without AVX, and is the universal fallback.
	// Published as a public image on Docker Hub so installs pull it automatically.
	cpuMinerBaselineImage = "jvx1971/cpu-lottery-miner:v1"
)

// cpuMinerFastImages are off-the-shelf cpuminer-opt builds that are much faster
// but require modern instructions (AVX2/AVX512) and crash with SIGILL on CPUs
// without them. selectCpuMinerImage only adopts one after probeCpuMinerImage
// confirms it actually runs on this host. These and the baseline image all have
// an empty ENTRYPOINT with the binary as "cpuminer" in PATH, so
// cpuMinerComposeContents passes "cpuminer" as the first command argument.
var cpuMinerFastImages = []string{
	"cniweb/cpuminer-opt@sha256:8aba97834d6a6e1946b2a61c8939eee8907b7be97d8e77c1174f66579d5bd90b",
	"cniweb/cpuminer-opt:latest",
}

type cpuMinerPaths struct {
	Root        string
	ComposePath string
	EnvPath     string
}

type cpuMinerApp struct {
	server *Server
}

func newCpuMinerApp(s *Server) appHandler {
	return cpuMinerApp{server: s}
}

func cpuMinerDefinition() appDefinition {
	return appDefinition{
		ID:          cpuMinerAppID,
		Name:        "CPU Lottery Miner",
		Description: "Solo-mine Bitcoin with spare CPU against your local Public Pool. Pure lottery, rewards go straight to your LND wallet.",
		Port:        0,
	}
}

func (a cpuMinerApp) Definition() appDefinition {
	return cpuMinerDefinition()
}

func (a cpuMinerApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)

	available, reason, message := a.server.cpuMinerAvailability(ctx)
	info.Available = available
	info.UnavailableReason = reason
	info.UnavailableMessage = message

	paths := cpuMinerAppPaths()
	if !fileExists(paths.ComposePath) {
		return info, nil
	}
	info.Installed = true
	status, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, cpuMinerAppID)
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (a cpuMinerApp) Install(ctx context.Context) error {
	return a.server.installCpuMiner(ctx)
}

func (a cpuMinerApp) Uninstall(ctx context.Context) error {
	return a.server.uninstallCpuMiner(ctx)
}

func (a cpuMinerApp) Start(ctx context.Context) error {
	return a.server.startCpuMiner(ctx)
}

func (a cpuMinerApp) Stop(ctx context.Context) error {
	return a.server.stopCpuMiner(ctx)
}

func cpuMinerAppPaths() cpuMinerPaths {
	root := filepath.Join(appsRoot, cpuMinerAppID)
	return cpuMinerPaths{
		Root:        root,
		ComposePath: filepath.Join(root, "docker-compose.yaml"),
		EnvPath:     filepath.Join(root, ".env"),
	}
}

// cpuMinerAvailability gates the app on a running local Public Pool, which is
// the stratum target the miner connects to.
func (s *Server) cpuMinerAvailability(ctx context.Context) (bool, string, string) {
	poolPaths := publicPoolAppPaths()
	if !fileExists(poolPaths.ComposePath) {
		return false, "requires_public_pool", "Install and start Public Pool from the App Store before installing CPU Lottery Miner."
	}
	status, err := getComposeStatus(ctx, poolPaths.Root, poolPaths.ComposePath, "public-pool")
	if err != nil || status != "running" {
		return false, "requires_public_pool_running", "Start Public Pool before using CPU Lottery Miner."
	}
	return true, "", ""
}

func (s *Server) installCpuMiner(ctx context.Context) error {
	if available, _, message := s.cpuMinerAvailability(ctx); !available {
		return errors.New(message)
	}
	if err := ensureDocker(ctx); err != nil {
		return err
	}
	paths := cpuMinerAppPaths()
	if err := ensureCpuMinerPaths(paths); err != nil {
		return err
	}
	image, err := s.selectCpuMinerImage(ctx)
	if err != nil {
		return err
	}
	address, err := s.ensureCpuMinerAddress(ctx, paths)
	if err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, cpuMinerComposeContents(image)); err != nil {
		return err
	}
	if err := ensureCpuMinerEnv(paths, address, cpuMinerDefaultThreads); err != nil {
		return err
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d")
}

func (s *Server) startCpuMiner(ctx context.Context) error {
	paths := cpuMinerAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("CPU Lottery Miner is not installed")
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d")
}

func (s *Server) stopCpuMiner(ctx context.Context) error {
	paths := cpuMinerAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("CPU Lottery Miner is not installed")
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "stop")
}

func (s *Server) uninstallCpuMiner(ctx context.Context) error {
	paths := cpuMinerAppPaths()
	if fileExists(paths.ComposePath) {
		_ = runCompose(ctx, paths.Root, paths.ComposePath, "down", "--remove-orphans")
	}
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("failed to remove app files: %w", err)
	}
	return nil
}

func ensureCpuMinerPaths(paths cpuMinerPaths) error {
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	return nil
}

// ensureCpuMinerAddress reuses a previously generated payout address or mints a
// fresh one from the LND wallet (a receive-only operation that needs no
// password). The reward of any found block lands directly in the node wallet.
func (s *Server) ensureCpuMinerAddress(ctx context.Context, paths cpuMinerPaths) (string, error) {
	if fileExists(paths.EnvPath) {
		if existing := strings.TrimSpace(readEnvValue(paths.EnvPath, "MINING_ADDRESS")); existing != "" {
			return existing, nil
		}
	}
	if s.lnd == nil {
		return "", errors.New("LND is unavailable to generate a mining address")
	}
	address, err := s.lnd.NewAddress(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to generate mining address: %w", err)
	}
	if strings.TrimSpace(address) == "" {
		return "", errors.New("LND returned an empty mining address")
	}
	return address, nil
}

// selectCpuMinerImage picks the fastest cpuminer image that actually runs on
// this CPU. It only tries the fast (AVX) images when the CPU advertises AVX,
// and verifies each with a short benchmark (probeCpuMinerImage) so that an
// AVX2-only CPU never adopts an AVX512 build that would crash. It falls back to
// our baseline image, which runs on any amd64.
func (s *Server) selectCpuMinerImage(ctx context.Context) (string, error) {
	if cpuinfoHasFlag("avx") {
		for _, image := range cpuMinerFastImages {
			if err := ensureDockerImage(ctx, image); err != nil {
				continue
			}
			if s.probeCpuMinerImage(ctx, image) {
				return image, nil
			}
			if s.logger != nil {
				s.logger.Printf("cpuminer: %s not runnable on this CPU, falling back", image)
			}
		}
	}
	if err := ensureDockerImage(ctx, cpuMinerBaselineImage); err != nil {
		return "", fmt.Errorf("cpuminer baseline image %s unavailable: %w", cpuMinerBaselineImage, err)
	}
	return cpuMinerBaselineImage, nil
}

// probeCpuMinerImage runs a brief benchmark to confirm the binary executes on
// this CPU. A SIGILL from a too-modern build makes docker run exit non-zero.
func (s *Server) probeCpuMinerImage(ctx context.Context, image string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	_, err := system.RunCommandWithSudo(probeCtx, "docker", "run", "--rm", image,
		"cpuminer", "--algo", "sha256d", "--benchmark", "--time-limit", "2")
	return err == nil
}

// cpuinfoHasFlag reports whether /proc/cpuinfo advertises the given CPU flag.
func cpuinfoHasFlag(flag string) bool {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return false
	}
	return cpuFlagsLineHas(string(data), flag)
}

func cpuFlagsLineHas(cpuinfo string, flag string) bool {
	for _, line := range strings.Split(cpuinfo, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		if !strings.HasPrefix(lower, "flags") && !strings.HasPrefix(lower, "features") {
			continue
		}
		idx := strings.Index(lower, ":")
		if idx < 0 {
			continue
		}
		for _, f := range strings.Fields(lower[idx+1:]) {
			if f == flag {
				return true
			}
		}
	}
	return false
}

func cpuMinerComposeContents(image string) string {
	return fmt.Sprintf(`services:
  cpuminer:
    image: %s
    restart: unless-stopped
    extra_hosts:
      - "host.docker.internal:host-gateway"
    ports:
      - "127.0.0.1:%d:%d"
    cpus: "${THREADS}"
    cpu_shares: 128
    command:
      - "cpuminer"
      - "--algo"
      - "sha256d"
      - "--url"
      - "stratum+tcp://host.docker.internal:%d"
      - "--user"
      - "${MINING_ADDRESS}.%s"
      - "--pass"
      - "x"
      - "--threads"
      - "${THREADS}"
      - "--api-bind"
      - "0.0.0.0:%d"
`, image, cpuMinerAPIPort, cpuMinerAPIPort, publicPoolStratumPort, cpuMinerWorkerTag, cpuMinerAPIPort)
}

// cpuMinerMaxThreads caps mining threads at the host core count minus one, so
// at least one core is always left for LND and the rest of the node.
func cpuMinerMaxThreads() int {
	if n := runtime.NumCPU() - 1; n >= 1 {
		return n
	}
	return 1
}

func clampCpuMinerThreads(threads int) int {
	if threads < 1 {
		return cpuMinerDefaultThreads
	}
	if max := cpuMinerMaxThreads(); threads > max {
		return max
	}
	return threads
}

// setCpuMinerThreads updates the thread count and recreates the container so the
// new value (and matching CPU cap) takes effect.
func (s *Server) setCpuMinerThreads(ctx context.Context, threads int) error {
	paths := cpuMinerAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("CPU Lottery Miner is not installed")
	}
	if err := setEnvValue(paths.EnvPath, "THREADS", strconv.Itoa(clampCpuMinerThreads(threads))); err != nil {
		return err
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d")
}

func ensureCpuMinerEnv(paths cpuMinerPaths, address string, threads int) error {
	threads = clampCpuMinerThreads(threads)
	required := [][2]string{
		{"MINING_ADDRESS", address},
		{"THREADS", strconv.Itoa(threads)},
	}
	if !fileExists(paths.EnvPath) {
		lines := make([]string, 0, len(required)+1)
		for _, kv := range required {
			lines = append(lines, kv[0]+"="+kv[1])
		}
		lines = append(lines, "")
		return writeFile(paths.EnvPath, strings.Join(lines, "\n"), 0600)
	}
	for _, kv := range required {
		exists, value, err := envValueState(paths.EnvPath, kv[0])
		if err != nil {
			return err
		}
		if !exists {
			if err := appendEnvLine(paths.EnvPath, kv[0], kv[1]); err != nil {
				return err
			}
			continue
		}
		// MINING_ADDRESS is sticky once set; only refresh THREADS if it drifts.
		if kv[0] == "THREADS" && strings.TrimSpace(value) != kv[1] {
			if err := setEnvValue(paths.EnvPath, kv[0], kv[1]); err != nil {
				return err
			}
		}
	}
	return nil
}
