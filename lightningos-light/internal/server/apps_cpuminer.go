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

	"lightningos-light/internal/appmanifest"
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

	// Pool targets the miner can point its stratum connection at.
	cpuMinerPoolLocal = "local" // the Public Pool app running on this machine
	cpuMinerPoolBRLN  = "brln"  // the BR-LN hosted pool (btcpool.br-ln.com)

	cpuMinerBRLNStratumHost = "btcpool.br-ln.com"
	cpuMinerBRLNStratumPort = 3332
	cpuMinerBRLNStatsBase   = "https://btcpool.br-ln.com"

	cpuMinerLocalStratumHost = "host.docker.internal"
)

// cpuMinerPool describes a resolved stratum target and where to read pool-side
// stats (best difficulty / pool hashrate). StatsBase is empty when no pool API
// is reachable.
type cpuMinerPool struct {
	Mode        string
	StratumHost string
	StratumPort int
	StatsBase   string
}

func cpuMinerPoolLabel(mode string) string {
	if mode == cpuMinerPoolBRLN {
		return "BR-LN (" + cpuMinerBRLNStratumHost + ")"
	}
	return "Local Public Pool"
}

func cpuMinerPoolPreset(mode string) cpuMinerPool {
	if mode == cpuMinerPoolBRLN {
		return cpuMinerPool{
			Mode:        cpuMinerPoolBRLN,
			StratumHost: cpuMinerBRLNStratumHost,
			StratumPort: cpuMinerBRLNStratumPort,
			StatsBase:   cpuMinerBRLNStatsBase,
		}
	}
	return cpuMinerPool{
		Mode:        cpuMinerPoolLocal,
		StratumHost: cpuMinerLocalStratumHost,
		StratumPort: publicPoolStratumPort,
		StatsBase:   "http://127.0.0.1:" + strconv.Itoa(publicPoolAPIPort),
	}
}

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
		Description: "Solo-mine Bitcoin with spare CPU against the BR-LN pool or your local Public Pool. Pure lottery, rewards go straight to your wallet.",
		Port:        0,
	}
}

func (a cpuMinerApp) Definition() appDefinition {
	return cpuMinerDefinition()
}

func (a cpuMinerApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)

	// Always available: it can mine on the BR-LN pool without any local
	// dependency; the local Public Pool is just one of the selectable targets.
	paths := cpuMinerAppPaths()
	if !fileExists(paths.ComposePath) {
		return info, nil
	}
	info.Installed = true
	status := "unknown"
	handled, brokerStatus, _, err := system.InspectAppWithBroker(ctx, cpuMinerAppID)
	if handled {
		status = brokerStatus
	} else {
		status, err = getComposeStatus(ctx, paths.Root, paths.ComposePath, cpuMinerAppID)
	}
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

// publicPoolRunning reports whether the local Public Pool app is up, so the
// miner can default to (and validate) the local stratum target.
func (s *Server) publicPoolRunning(ctx context.Context) bool {
	poolPaths := publicPoolAppPaths()
	if !fileExists(poolPaths.ComposePath) {
		return false
	}
	status, err := getComposeStatus(ctx, poolPaths.Root, poolPaths.ComposePath, "public-pool")
	return err == nil && status == "running"
}

func (s *Server) installCpuMiner(ctx context.Context) error {
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
	if err := ensureCpuMinerCompose(paths); err != nil {
		return err
	}
	cfg := s.loadCpuMinerConfig(ctx, paths, address)
	cfg.Image = image
	if err := writeCpuMinerEnv(paths, cfg); err != nil {
		return err
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d")
}

func (s *Server) startCpuMiner(ctx context.Context) error {
	paths := cpuMinerAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("CPU Lottery Miner is not installed")
	}
	return applyCpuMinerCompose(ctx, paths)
}

func applyCpuMinerCompose(ctx context.Context, paths cpuMinerPaths) error {
	if handled, err := system.AppLifecycleWithBroker(ctx, cpuMinerAppID, "start"); handled {
		return err
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d")
}

func (s *Server) stopCpuMiner(ctx context.Context) error {
	paths := cpuMinerAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("CPU Lottery Miner is not installed")
	}
	if handled, err := system.AppLifecycleWithBroker(ctx, cpuMinerAppID, "stop"); handled {
		return err
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

func cpuMinerComposeContents() string {
	return appmanifest.CPUMinerCompose()
}

// ensureCpuMinerCompose (re)writes the compose file from the current template.
// Called on every install/config/threads change so an install created by an
// older build (with a stale, hardcoded compose) self-heals to the latest
// template on the next apply — no reinstall needed.
func ensureCpuMinerCompose(paths cpuMinerPaths) error {
	_, err := ensureFileWithChange(paths.ComposePath, cpuMinerComposeContents())
	return err
}

// cpuMinerResolveImage returns the image to run: the value stored in .env, or
// (migrating an older install) the image hardcoded in the existing compose
// file, falling back to the baseline image.
func cpuMinerResolveImage(paths cpuMinerPaths) string {
	if v := strings.TrimSpace(readEnvValue(paths.EnvPath, "CPUMINER_IMAGE")); v != "" {
		return v
	}
	if data, err := os.ReadFile(paths.ComposePath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "image:") {
				img := strings.TrimSpace(strings.TrimPrefix(trimmed, "image:"))
				if img != "" && !strings.Contains(img, "${") {
					return img
				}
			}
		}
	}
	return cpuMinerBaselineImage
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
	if err := setEnvValue(paths.EnvPath, "CPUMINER_IMAGE", cpuMinerResolveImage(paths)); err != nil {
		return err
	}
	if err := ensureCpuMinerCompose(paths); err != nil {
		return err
	}
	return applyCpuMinerCompose(ctx, paths)
}

type cpuMinerConfig struct {
	Image   string
	Address string
	Worker  string
	Pool    cpuMinerPool
	Threads int
}

func writeCpuMinerEnv(paths cpuMinerPaths, cfg cpuMinerConfig) error {
	kv := [][2]string{
		{"CPUMINER_IMAGE", cfg.Image},
		{"POOL_MODE", cfg.Pool.Mode},
		{"STRATUM_HOST", cfg.Pool.StratumHost},
		{"STRATUM_PORT", strconv.Itoa(cfg.Pool.StratumPort)},
		{"MINING_ADDRESS", cfg.Address},
		{"WORKER_NAME", sanitizeCpuMinerWorker(cfg.Worker)},
		{"THREADS", strconv.Itoa(clampCpuMinerThreads(cfg.Threads))},
	}
	if !fileExists(paths.EnvPath) {
		lines := make([]string, 0, len(kv)+1)
		for _, p := range kv {
			lines = append(lines, p[0]+"="+p[1])
		}
		lines = append(lines, "")
		return writeFile(paths.EnvPath, strings.Join(lines, "\n"), 0600)
	}
	for _, p := range kv {
		if err := setEnvValue(paths.EnvPath, p[0], p[1]); err != nil {
			return err
		}
	}
	return nil
}

// loadCpuMinerConfig reads the current config from .env, filling missing fields
// with defaults: local pool when Public Pool is running (else the BR-LN pool),
// the default worker tag, and a single thread.
func (s *Server) loadCpuMinerConfig(ctx context.Context, paths cpuMinerPaths, address string) cpuMinerConfig {
	cfg := cpuMinerConfig{Address: address, Worker: cpuMinerWorkerTag, Threads: cpuMinerDefaultThreads}

	switch strings.TrimSpace(readEnvValue(paths.EnvPath, "POOL_MODE")) {
	case cpuMinerPoolBRLN:
		cfg.Pool = cpuMinerPoolPreset(cpuMinerPoolBRLN)
	case cpuMinerPoolLocal:
		cfg.Pool = cpuMinerPoolPreset(cpuMinerPoolLocal)
	default:
		if s.publicPoolRunning(ctx) {
			cfg.Pool = cpuMinerPoolPreset(cpuMinerPoolLocal)
		} else {
			cfg.Pool = cpuMinerPoolPreset(cpuMinerPoolBRLN)
		}
	}
	if w := strings.TrimSpace(readEnvValue(paths.EnvPath, "WORKER_NAME")); w != "" {
		cfg.Worker = w
	}
	if t, err := strconv.Atoi(strings.TrimSpace(readEnvValue(paths.EnvPath, "THREADS"))); err == nil && t > 0 {
		cfg.Threads = t
	}
	return cfg
}

// setCpuMinerConfig applies a pool/address/worker change and recreates the
// container. Threads are preserved (managed by setCpuMinerThreads).
func (s *Server) setCpuMinerConfig(ctx context.Context, poolMode, address, worker string, useNodeAddress bool) error {
	paths := cpuMinerAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("CPU Lottery Miner is not installed")
	}

	pool := cpuMinerPoolPreset(poolMode)
	if pool.Mode == cpuMinerPoolLocal && !s.publicPoolRunning(ctx) {
		return errors.New("Start the Public Pool app to mine on the local pool, or pick the BR-LN pool.")
	}

	resolvedAddress := strings.TrimSpace(address)
	if useNodeAddress {
		if s.lnd == nil {
			return errors.New("LND is unavailable to generate a mining address")
		}
		generated, err := s.lnd.NewAddress(ctx)
		if err != nil {
			return fmt.Errorf("failed to generate mining address: %w", err)
		}
		resolvedAddress = strings.TrimSpace(generated)
	}
	if resolvedAddress == "" {
		resolvedAddress = strings.TrimSpace(readEnvValue(paths.EnvPath, "MINING_ADDRESS"))
	}
	if err := validateCpuMinerAddress(resolvedAddress); err != nil {
		return err
	}

	threads := cpuMinerDefaultThreads
	if t, err := strconv.Atoi(strings.TrimSpace(readEnvValue(paths.EnvPath, "THREADS"))); err == nil && t > 0 {
		threads = t
	}

	cfg := cpuMinerConfig{
		Image:   cpuMinerResolveImage(paths),
		Address: resolvedAddress,
		Worker:  worker,
		Pool:    pool,
		Threads: threads,
	}
	if err := ensureCpuMinerCompose(paths); err != nil {
		return err
	}
	if err := writeCpuMinerEnv(paths, cfg); err != nil {
		return err
	}
	return applyCpuMinerCompose(ctx, paths)
}

// sanitizeCpuMinerWorker keeps the worker name to characters a stratum user
// string tolerates (no dots — those separate address and worker — no spaces).
func sanitizeCpuMinerWorker(worker string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(worker) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		return cpuMinerWorkerTag
	}
	if len(out) > 32 {
		return out[:32]
	}
	return out
}

// validateCpuMinerAddress does a light sanity check on a mainnet Bitcoin
// address; the pool is the ultimate authority, but this catches obvious typos.
func validateCpuMinerAddress(address string) error {
	address = strings.TrimSpace(address)
	if address == "" {
		return errors.New("mining address is empty")
	}
	if len(address) < 14 || len(address) > 100 {
		return errors.New("mining address has an invalid length")
	}
	lower := strings.ToLower(address)
	if !(strings.HasPrefix(lower, "bc1") || strings.HasPrefix(address, "1") || strings.HasPrefix(address, "3")) {
		return errors.New("mining address must be a mainnet Bitcoin address (bc1…, 1… or 3…)")
	}
	for _, r := range address {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return errors.New("mining address contains invalid characters")
		}
	}
	return nil
}
