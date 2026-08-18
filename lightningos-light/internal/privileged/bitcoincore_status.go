package privileged

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"
)

const (
	maxBitcoinCoreStatusBytes       = 1024 * 1024
	bitcoinCoreCadenceWindowSec     = int64(600)
	bitcoinCoreCadenceBucketCount   = 12
	bitcoinCoreCadenceMaxScanBlocks = 144
)

var bitcoinCoreBlockHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type bitcoinCoreChainStatus struct {
	Chain                string  `json:"chain"`
	Blocks               int64   `json:"blocks"`
	Headers              int64   `json:"headers"`
	VerificationProgress float64 `json:"verificationprogress"`
	InitialBlockDownload bool    `json:"initialblockdownload"`
	BestBlockHash        string  `json:"bestblockhash"`
	Pruned               bool    `json:"pruned"`
	PruneHeight          int64   `json:"pruneheight"`
	PruneTargetSize      int64   `json:"prune_target_size"`
	SizeOnDisk           int64   `json:"size_on_disk"`
}

type bitcoinCoreNetworkStatus struct {
	Version     int    `json:"version"`
	Subversion  string `json:"subversion"`
	Connections int    `json:"connections"`
}

type bitcoinCoreHeaderStatus struct {
	Time              int64  `json:"time"`
	PreviousBlockHash string `json:"previousblockhash"`
}

// BitcoinCoreStatus obtains bounded, read-only telemetry through bitcoin-cli
// inside the catalog container. bitcoin-cli authenticates with Core's runtime
// cookie, so legacy rpcauth-only nodes remain observable without recovering or
// rotating a password and without restarting bitcoind.
func (manager *ComposeAppManager) BitcoinCoreStatus(ctx context.Context) (BitcoinCoreStatusState, error) {
	var state BitcoinCoreStatusState
	if manager == nil || manager.Runner == nil {
		return state, errors.New("compose app manager is unavailable")
	}
	containerID := ""
	snapshot, cleanup, err := manager.createBitcoinCoreInspectionSnapshot(ctx)
	if err == nil {
		defer cleanup()
		commandPath, prefix, resolveErr := manager.resolveCompose(ctx)
		if resolveErr != nil {
			return state, resolveErr
		}
		manifest, _ := appmanifest.ComposeManifestForApp(appmanifest.BitcoinCoreID)
		baseArgs := append([]string(nil), prefix...)
		baseArgs = append(baseArgs,
			"--project-name", manifest.Project,
			"--project-directory", snapshot.root,
			"-f", snapshot.composePath,
		)
		running, runErr := manager.Runner.Run(ctx, commandPath, append(append([]string(nil), baseArgs...), "ps", "--services", "--filter", "status=running")...)
		if runErr != nil || !containsExactLine(running, appmanifest.BitcoinCorePrimaryService) {
			return state, errors.New("bitcoin core container is not running")
		}
		containerOutput, lookupErr := manager.Runner.Run(ctx, commandPath, append(append([]string(nil), baseArgs...), "ps", "-q", appmanifest.BitcoinCorePrimaryService)...)
		if lookupErr != nil {
			return state, errors.New("bitcoin core container lookup failed")
		}
		containerID = parseDockerContainerID(containerOutput)
		if containerID == "" {
			return state, errors.New("bitcoin core container lookup returned an invalid ID")
		}
	} else {
		// Upgraded nodes can have a healthy App Store Bitcoin runtime created by
		// an older release but no broker enrollment yet. Keep observability
		// read-only: identify the closed catalog container directly and execute
		// only the fixed cookie-backed bitcoin-cli commands below. Lifecycle
		// writes still require full broker enrollment and attestation.
		runtimeState, found, inspectErr := manager.inspectBitcoinCoreCatalogRuntime(ctx)
		if inspectErr != nil || !found || !runtimeState.Running ||
			(runtimeState.ImageRef != appmanifest.BitcoinCoreImage && !isLegacyBitcoinCoreImage(runtimeState.ImageRef)) {
			return state, errors.New("bitcoin core runtime cannot be safely observed")
		}
		containerID = runtimeState.ContainerID
	}

	chainRaw, err := manager.runBitcoinCoreCLI(ctx, containerID, "getblockchaininfo")
	if err != nil {
		return state, errors.New("bitcoin core RPC is unavailable")
	}
	var chain bitcoinCoreChainStatus
	if err := decodeBitcoinCoreCLIStatus(chainRaw, &chain); err != nil {
		return state, errors.New("bitcoin core RPC response is invalid")
	}
	state = BitcoinCoreStatusState{
		Chain:                chain.Chain,
		Blocks:               chain.Blocks,
		Headers:              chain.Headers,
		VerificationProgress: chain.VerificationProgress,
		InitialBlockDownload: chain.InitialBlockDownload,
		BestBlockHash:        chain.BestBlockHash,
		Pruned:               chain.Pruned,
		PruneHeight:          chain.PruneHeight,
		PruneTargetSize:      chain.PruneTargetSize,
		SizeOnDisk:           chain.SizeOnDisk,
	}
	if err := validateBitcoinCoreStatusState(state); err != nil {
		return BitcoinCoreStatusState{}, errors.New("bitcoin core RPC response is invalid")
	}

	if bitcoinCoreStatusHasOptionalBudget(ctx) {
		networkRaw, networkErr := manager.runBitcoinCoreCLI(ctx, containerID, "getnetworkinfo")
		var network bitcoinCoreNetworkStatus
		if networkErr == nil && decodeBitcoinCoreCLIStatus(networkRaw, &network) == nil {
			state.NetworkOK = true
			state.Version = network.Version
			state.Subversion = network.Subversion
			state.Connections = network.Connections
		}
	}
	if bitcoinCoreStatusHasOptionalBudget(ctx) {
		headerRaw, headerErr := manager.runBitcoinCoreCLI(ctx, containerID, "getblockheader", state.BestBlockHash, "true")
		var header bitcoinCoreHeaderStatus
		if headerErr == nil && decodeBitcoinCoreCLIStatus(headerRaw, &header) == nil && header.Time > 0 {
			state.BestBlockTime = header.Time
			if cadence, cadenceErr := manager.bitcoinCoreCadence(ctx, containerID, header); cadenceErr == nil {
				state.BlockCadenceWindowSec = bitcoinCoreCadenceWindowSec
				state.BlockCadence = cadence
			}
		}
	}
	if err := validateBitcoinCoreStatusState(state); err != nil {
		return BitcoinCoreStatusState{}, errors.New("bitcoin core RPC response is invalid")
	}
	return state, nil
}

func (manager *ComposeAppManager) bitcoinCoreCadence(ctx context.Context, containerID string, best bitcoinCoreHeaderStatus) ([]BitcoinCoreCadenceBucket, error) {
	if best.Time <= 0 {
		return nil, errors.New("bitcoin core best block time is unavailable")
	}
	startTime := best.Time - (bitcoinCoreCadenceWindowSec * bitcoinCoreCadenceBucketCount)
	buckets := make([]BitcoinCoreCadenceBucket, bitcoinCoreCadenceBucketCount)
	for index := range buckets {
		start := startTime + int64(index)*bitcoinCoreCadenceWindowSec
		buckets[index] = BitcoinCoreCadenceBucket{StartTime: start, EndTime: start + bitcoinCoreCadenceWindowSec}
	}

	current := best
	for steps := 0; steps < bitcoinCoreCadenceMaxScanBlocks; steps++ {
		if current.Time < startTime {
			return buckets, nil
		}
		index := int((current.Time - startTime) / bitcoinCoreCadenceWindowSec)
		if index >= 0 && index < len(buckets) {
			buckets[index].Count++
		}

		nextHash := strings.TrimSpace(current.PreviousBlockHash)
		if nextHash == "" {
			return buckets, nil
		}
		if !bitcoinCoreBlockHashPattern.MatchString(nextHash) {
			return nil, errors.New("bitcoin core previous block hash is invalid")
		}
		raw, err := manager.runBitcoinCoreCLI(ctx, containerID, "getblockheader", nextHash, "true")
		if err != nil {
			return nil, err
		}
		var next bitcoinCoreHeaderStatus
		if decodeBitcoinCoreCLIStatus(raw, &next) != nil || next.Time <= 0 {
			return nil, errors.New("bitcoin core block header is invalid")
		}
		current = next
	}
	if current.Time < startTime {
		return buckets, nil
	}
	return nil, errors.New("bitcoin core cadence scan limit reached")
}

func (manager *ComposeAppManager) runBitcoinCoreCLI(ctx context.Context, containerID string, args ...string) (string, error) {
	command := []string{
		"exec", "-i", containerID,
		"bitcoin-cli",
		"-datadir=" + appmanifest.BitcoinCoreContainerDataDir,
		"-conf=" + appmanifest.BitcoinCoreContainerConfig,
		"-rpcclienttimeout=40",
	}
	output, err := manager.Runner.Run(ctx, dockerPath, append(command, args...)...)
	if err != nil || len(output) == 0 || len(output) > maxBitcoinCoreStatusBytes {
		return "", errors.New("bitcoin core CLI failed")
	}
	return output, nil
}

func bitcoinCoreStatusHasOptionalBudget(ctx context.Context) bool {
	deadline, ok := ctx.Deadline()
	return !ok || time.Until(deadline) >= 10*time.Second
}

func decodeBitcoinCoreCLIStatus(raw string, target any) error {
	if len(raw) == 0 || len(raw) > maxBitcoinCoreStatusBytes {
		return errors.New("bitcoin core CLI response is invalid")
	}
	return json.Unmarshal([]byte(raw), target)
}

func validateBitcoinCoreStatusState(state BitcoinCoreStatusState) error {
	if state.Chain != "main" || state.Blocks < 0 || state.Headers < 0 || state.Blocks > state.Headers ||
		state.VerificationProgress < 0 || state.VerificationProgress > 1 || math.IsNaN(state.VerificationProgress) ||
		math.IsInf(state.VerificationProgress, 0) || !bitcoinCoreBlockHashPattern.MatchString(state.BestBlockHash) ||
		state.BestBlockTime < 0 || state.PruneHeight < 0 || state.PruneTargetSize < 0 || state.SizeOnDisk < 0 {
		return errors.New("invalid bitcoin core status")
	}
	if err := validateBitcoinCoreCadence(state); err != nil {
		return err
	}
	if !state.NetworkOK {
		if state.Version != 0 || state.Subversion != "" || state.Connections != 0 {
			return errors.New("invalid bitcoin core network status")
		}
		return nil
	}
	if state.Version <= 0 || state.Connections < 0 || len(state.Subversion) == 0 || len(state.Subversion) > 128 || strings.TrimSpace(state.Subversion) != state.Subversion {
		return errors.New("invalid bitcoin core network status")
	}
	for _, char := range []byte(state.Subversion) {
		if char < 0x20 || char > 0x7e {
			return errors.New("invalid bitcoin core network status")
		}
	}
	return nil
}

func validateBitcoinCoreCadence(state BitcoinCoreStatusState) error {
	if len(state.BlockCadence) == 0 {
		if state.BlockCadenceWindowSec != 0 {
			return errors.New("invalid bitcoin core cadence")
		}
		return nil
	}
	if state.BestBlockTime <= 0 || state.BlockCadenceWindowSec != bitcoinCoreCadenceWindowSec || len(state.BlockCadence) != bitcoinCoreCadenceBucketCount {
		return errors.New("invalid bitcoin core cadence")
	}
	for index, bucket := range state.BlockCadence {
		if bucket.StartTime <= 0 || bucket.EndTime-bucket.StartTime != bitcoinCoreCadenceWindowSec || bucket.Count < 0 || bucket.Count > bitcoinCoreCadenceMaxScanBlocks {
			return errors.New("invalid bitcoin core cadence")
		}
		if index > 0 && bucket.StartTime != state.BlockCadence[index-1].EndTime {
			return errors.New("invalid bitcoin core cadence")
		}
	}
	return nil
}

func containsExactLine(raw string, expected string) bool {
	for _, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == expected {
			return true
		}
	}
	return false
}
