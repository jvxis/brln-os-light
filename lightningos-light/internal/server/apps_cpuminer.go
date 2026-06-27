package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	cpuMinerAppID          = "cpuminer"
	cpuMinerAPIPort        = 4048
	cpuMinerWorkerTag      = "cpu-lottery"
	cpuMinerDefaultThreads = 1
	cpuMinerMaxThreads     = 2
)

// cpuMinerImageCandidates lists amd64 cpuminer-opt images tried in order at
// install time. The image has an empty ENTRYPOINT with CMD ["cpuminer", ...],
// so cpuMinerComposeContents passes "cpuminer" as the first command argument.
// Pinned to a validated digest for supply-chain safety, with the tag as fallback.
var cpuMinerImageCandidates = []string{
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
	image, err := ensureFirstAvailableDockerImage(ctx, cpuMinerImageCandidates)
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
      - "--cpu-priority"
      - "0"
      - "--api-bind"
      - "0.0.0.0:%d"
`, image, cpuMinerAPIPort, cpuMinerAPIPort, publicPoolStratumPort, cpuMinerWorkerTag, cpuMinerAPIPort)
}

func ensureCpuMinerEnv(paths cpuMinerPaths, address string, threads int) error {
	if threads < 1 {
		threads = cpuMinerDefaultThreads
	}
	if threads > cpuMinerMaxThreads {
		threads = cpuMinerMaxThreads
	}
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
