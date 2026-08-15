package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"lightningos-light/internal/system"
)

const (
	bitcoinCoreMinPruneMiB      = 550
	blockCadenceWindowSec       = 600
	blockCadenceBucketCount     = 12
	blockCadenceCacheTTL        = 60 * time.Second
	blockCadenceMaxSteps        = 144
	bitcoinNetworkInfoTimeout   = 2 * time.Second
	bitcoinCadenceTimeout       = 8 * time.Second
	bitcoinCadenceMinBudget     = 3 * time.Second
	maxBitcoinBrokerStatusBytes = 64 * 1024
)

type bitcoinLocalStatus struct {
	Installed             bool                 `json:"installed"`
	Status                string               `json:"status"`
	Source                string               `json:"source"`
	DataDir               string               `json:"data_dir"`
	RPCOk                 bool                 `json:"rpc_ok"`
	Connections           int                  `json:"connections,omitempty"`
	Chain                 string               `json:"chain,omitempty"`
	Blocks                int64                `json:"blocks,omitempty"`
	Headers               int64                `json:"headers,omitempty"`
	BestBlockTime         int64                `json:"best_block_time,omitempty"`
	BlockCadenceWindowSec int64                `json:"block_cadence_window_sec,omitempty"`
	BlockCadence          []blockCadenceBucket `json:"block_cadence,omitempty"`
	VerificationProgress  float64              `json:"verification_progress,omitempty"`
	InitialBlockDownload  bool                 `json:"initial_block_download,omitempty"`
	BestBlockHash         string               `json:"best_block_hash,omitempty"`
	Version               int                  `json:"version,omitempty"`
	Subversion            string               `json:"subversion,omitempty"`
	Pruned                bool                 `json:"pruned,omitempty"`
	PruneHeight           int64                `json:"prune_height,omitempty"`
	PruneTargetSize       int64                `json:"prune_target_size,omitempty"`
	SizeOnDisk            int64                `json:"size_on_disk,omitempty"`
}

type bitcoinLocalConfig struct {
	Mode        string  `json:"mode"`
	PruneSizeGB float64 `json:"prune_size_gb,omitempty"`
	MinPruneGB  float64 `json:"min_prune_gb"`
	DataDir     string  `json:"data_dir"`
}

type bitcoinLocalConfigUpdate struct {
	Mode        string  `json:"mode"`
	PruneSizeGB float64 `json:"prune_size_gb"`
	ApplyNow    bool    `json:"apply_now"`
}

type bitcoinCLIChainInfo struct {
	Chain                string  `json:"chain"`
	Blocks               int64   `json:"blocks"`
	Headers              int64   `json:"headers"`
	VerificationProgress float64 `json:"verificationprogress"`
	InitialBlockDownload bool    `json:"initialblockdownload"`
	Pruned               bool    `json:"pruned"`
	PruneHeight          int64   `json:"pruneheight"`
	PruneTargetSize      int64   `json:"prune_target_size"`
	SizeOnDisk           int64   `json:"size_on_disk"`
	BestBlockHash        string  `json:"bestblockhash"`
}

type bitcoinCLINetworkInfo struct {
	Version     int    `json:"version"`
	Subversion  string `json:"subversion"`
	Connections int    `json:"connections"`
}

type bitcoinLogTip struct {
	Hash     string
	Height   int64
	Time     int64
	Progress float64
}

type blockCadenceBucket struct {
	StartTime int64 `json:"start_time"`
	EndTime   int64 `json:"end_time"`
	Count     int   `json:"count"`
}

type bitcoinCLIBlockHeader struct {
	Time              int64  `json:"time"`
	PreviousBlockHash string `json:"previousblockhash"`
}

type bitcoinBlockHeaderRPCResponse struct {
	Result bitcoinCLIBlockHeader `json:"result"`
	Error  *rpcErrorDetail       `json:"error"`
}

type blockCadenceCache struct {
	BestHash  string
	BestTime  int64
	Buckets   []blockCadenceBucket
	ExpiresAt time.Time
}

var blockCadenceMu sync.Mutex
var blockCadenceState blockCadenceCache

func (s *Server) handleBitcoinLocalStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), bitcoinLocalHandlerTimeout())
	defer cancel()

	resp, err := s.bitcoinLocalStatusCached(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "bitcoin local status error")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) bitcoinLocalStatus(ctx context.Context) (bitcoinLocalStatus, error) {
	paths := bitcoinCoreAppPaths()
	resp := bitcoinLocalStatus{
		Installed: false,
		Status:    "not_installed",
		Source:    "none",
		DataDir:   paths.DataDir,
	}
	if !fileExists(paths.ComposePath) {
		cfg, err := readBitcoinLocalRPCConfig(ctx)
		if err != nil {
			return resp, nil
		}
		resp.Source = "external"
		resp.Status = "external"
		info, rpcErr := fetchBitcoinInfo(ctx, cfg.Host, cfg.User, cfg.Pass)
		if rpcErr != nil {
			resp.RPCOk = false
			return resp, nil
		}
		applyBitcoinInfoToLocalStatus(&resp, info)
		if netInfo, ok := fetchBitcoinNetworkInfoBestEffort(ctx, cfg.Host, cfg.User, cfg.Pass); ok {
			applyBitcoinNetworkInfoToLocalStatus(&resp, netInfo)
		}
		bestTime, buckets, cadenceOk := fetchBitcoinLocalCadenceBestEffort(ctx, paths, info.BestBlockHash)
		if cadenceOk {
			resp.BestBlockTime = bestTime
			resp.BlockCadenceWindowSec = blockCadenceWindowSec
			resp.BlockCadence = buckets
		}
		return resp, nil
	}
	resp.Installed = true
	resp.Source = "app"

	if brokerRaw, handled, brokerErr := system.ReadBitcoinCoreStatusWithBroker(ctx); handled {
		resp.Status = "running"
		if brokerErr != nil {
			resp.Status = "unknown"
			resp.RPCOk = false
			return resp, nil
		}
		brokerStatus, err := parseBitcoinCoreBrokerStatus(brokerRaw)
		if err != nil {
			resp.Status = "unknown"
			resp.RPCOk = false
			return resp, nil
		}
		brokerStatus.Installed = resp.Installed
		brokerStatus.Status = resp.Status
		brokerStatus.Source = resp.Source
		brokerStatus.DataDir = resp.DataDir
		return brokerStatus, nil
	}

	resp.Status = "unknown"
	resp.RPCOk = false
	return resp, nil
}

func (s *Server) handleBitcoinLocalConfigGet(w http.ResponseWriter, r *http.Request) {
	paths := bitcoinCoreAppPaths()
	minPruneGB := roundGB(float64(bitcoinCoreMinPruneMiB) / 1024.0)
	resp := bitcoinLocalConfig{
		Mode:       "full",
		MinPruneGB: minPruneGB,
		DataDir:    paths.DataDir,
	}
	if !fileExists(paths.ComposePath) {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

	raw, err := readBitcoinCoreConfig(ctx, paths)
	if err != nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	pruned, pruneMiB := parseBitcoinCorePrune(raw)
	if pruned {
		resp.Mode = "pruned"
		resp.PruneSizeGB = roundGB(float64(pruneMiB) / 1024.0)
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleBitcoinLocalConfigPost(w http.ResponseWriter, r *http.Request) {
	var req bitcoinLocalConfigUpdate
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "full"
	}
	if mode != "full" && mode != "pruned" {
		writeError(w, http.StatusBadRequest, "mode must be full or pruned")
		return
	}

	pruneMiB := 0
	if mode == "pruned" {
		if req.PruneSizeGB <= 0 {
			writeError(w, http.StatusBadRequest, "prune_size_gb required for pruned mode")
			return
		}
		pruneMiB = int(math.Round(req.PruneSizeGB * 1024.0))
		if pruneMiB < bitcoinCoreMinPruneMiB {
			pruneMiB = bitcoinCoreMinPruneMiB
		}
	}

	paths := bitcoinCoreAppPaths()
	if !fileExists(paths.ComposePath) {
		writeError(w, http.StatusBadRequest, "Bitcoin Core is not installed")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()

	raw, err := readBitcoinCoreConfig(ctx, paths)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read bitcoin.conf")
		return
	}
	updated := updateBitcoinCoreConfig(raw, mode, pruneMiB)

	if err := writeBitcoinCoreConfig(ctx, paths, updated); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write bitcoin.conf")
		return
	}

	if req.ApplyNow {
		if err := runBitcoinCoreLifecycle(ctx, "restart"); err != nil {
			writeError(w, http.StatusInternalServerError, "restart failed")
			return
		}
	}
	s.invalidateBitcoinStatusCaches()

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func readBitcoinCoreAppRPCConfig(ctx context.Context, paths bitcoinCorePaths) (bitcoinRPCConfig, bool) {
	if !fileExists(paths.ComposePath) {
		return bitcoinRPCConfig{}, false
	}
	raw, err := readBitcoinCoreConfigRaw(ctx, paths)
	if err == nil {
		user, pass, zmqBlock, zmqTx := parseBitcoinCoreRPCConfig(raw)
		if user != "" && pass != "" {
			return bitcoinRPCConfig{
				Host:     "127.0.0.1:8332",
				User:     user,
				Pass:     pass,
				ZMQBlock: normalizeLocalZMQ(zmqBlock, "tcp://127.0.0.1:28332"),
				ZMQTx:    normalizeLocalZMQ(zmqTx, "tcp://127.0.0.1:28333"),
			}, true
		}
	}

	// Older source-switching flows retained the App Store credential in the
	// commented LightningOS Bitcoin Local block while LND used a remote node.
	// Reuse it only for this compatibility status path and only when its host
	// remains local. The enforce path authenticates through the runtime cookie.
	if cfg, ok := readBitcoinTaggedRPCConfigFromLNDConf("local"); ok && isLocalRPCHost(cfg.Host) {
		return cfg, true
	}
	return bitcoinRPCConfig{}, false
}

func bitcoinInfoToCLIChainInfo(info bitcoinInfo) bitcoinCLIChainInfo {
	return bitcoinCLIChainInfo{
		Chain:                info.Chain,
		Blocks:               info.Blocks,
		Headers:              info.Headers,
		VerificationProgress: info.VerificationProgress,
		InitialBlockDownload: info.InitialBlockDownload,
		Pruned:               info.Pruned,
		PruneHeight:          info.PruneHeight,
		PruneTargetSize:      info.PruneTargetSize,
		SizeOnDisk:           info.SizeOnDisk,
		BestBlockHash:        info.BestBlockHash,
	}
}

func bitcoinNetworkInfoToCLINetworkInfo(info bitcoinNetworkInfo) bitcoinCLINetworkInfo {
	return bitcoinCLINetworkInfo{
		Version:     info.Version,
		Subversion:  info.Subversion,
		Connections: info.Connections,
	}
}

func getBitcoinLocalCadence(ctx context.Context, paths bitcoinCorePaths, bestHash string) (int64, []blockCadenceBucket, error) {
	trimmed := strings.TrimSpace(bestHash)
	if trimmed == "" {
		return 0, nil, errors.New("best block hash missing")
	}

	blockCadenceMu.Lock()
	cached := blockCadenceState
	if cached.BestHash == trimmed && time.Now().Before(cached.ExpiresAt) {
		buckets := make([]blockCadenceBucket, len(cached.Buckets))
		copy(buckets, cached.Buckets)
		blockCadenceMu.Unlock()
		return cached.BestTime, buckets, nil
	}
	blockCadenceMu.Unlock()

	var bestTime int64
	var buckets []blockCadenceBucket
	var err error
	if fileExists(paths.ComposePath) {
		if cfg, ok := readBitcoinCoreAppRPCConfig(ctx, paths); ok {
			bestTime, buckets, err = computeBitcoinLocalCadenceRPC(ctx, cfg, trimmed)
		} else {
			err = errors.New("local Bitcoin RPC credentials are unavailable")
		}
	} else {
		cfg, cfgErr := readBitcoinLocalRPCConfig(ctx)
		if cfgErr != nil {
			return 0, nil, cfgErr
		}
		bestTime, buckets, err = computeBitcoinLocalCadenceRPC(ctx, cfg, trimmed)
	}
	if err != nil {
		return 0, nil, err
	}

	blockCadenceMu.Lock()
	blockCadenceState = blockCadenceCache{
		BestHash:  trimmed,
		BestTime:  bestTime,
		Buckets:   buckets,
		ExpiresAt: time.Now().Add(blockCadenceCacheTTL),
	}
	blockCadenceMu.Unlock()

	return bestTime, buckets, nil
}

func computeBitcoinLocalCadenceRPC(ctx context.Context, cfg bitcoinRPCConfig, bestHash string) (int64, []blockCadenceBucket, error) {
	header, err := fetchBitcoinBlockHeaderRPC(ctx, cfg.Host, cfg.User, cfg.Pass, bestHash)
	if err != nil {
		return 0, nil, err
	}

	bestTime := header.Time
	if bestTime == 0 {
		return 0, nil, errors.New("best block time missing")
	}

	windowSec := int64(blockCadenceWindowSec)
	startTime := bestTime - (windowSec * int64(blockCadenceBucketCount))
	buckets := make([]blockCadenceBucket, blockCadenceBucketCount)
	for i := 0; i < blockCadenceBucketCount; i++ {
		start := startTime + (int64(i) * windowSec)
		buckets[i] = blockCadenceBucket{
			StartTime: start,
			EndTime:   start + windowSec,
			Count:     0,
		}
	}

	current := header
	complete := false
	for steps := 0; steps < blockCadenceMaxSteps; steps++ {
		if current.Time < startTime {
			complete = true
			break
		}
		idx := int((current.Time - startTime) / windowSec)
		if idx >= 0 && idx < len(buckets) {
			buckets[idx].Count++
		}

		nextHash := strings.TrimSpace(current.PreviousBlockHash)
		if nextHash == "" {
			complete = true
			break
		}

		nextHeader, err := fetchBitcoinBlockHeaderRPC(ctx, cfg.Host, cfg.User, cfg.Pass, nextHash)
		if err != nil {
			return 0, nil, fmt.Errorf("fetch previous block header failed: %w", err)
		}
		current = nextHeader
	}
	if !complete && current.Time < startTime {
		complete = true
	}
	if !complete {
		return 0, nil, errors.New("block cadence scan limit reached before window start")
	}

	return bestTime, buckets, nil
}

func fetchBitcoinNetworkInfoBestEffort(ctx context.Context, host, user, pass string) (bitcoinNetworkInfo, bool) {
	if !contextHasBudget(ctx, bitcoinCadenceMinBudget) {
		return bitcoinNetworkInfo{}, false
	}
	infoCtx, cancel := context.WithTimeout(ctx, bitcoinNetworkInfoTimeout)
	defer cancel()
	info, err := fetchBitcoinNetworkInfo(infoCtx, host, user, pass)
	if err != nil {
		return bitcoinNetworkInfo{}, false
	}
	return info, true
}

func fetchBitcoinLocalCadenceBestEffort(ctx context.Context, paths bitcoinCorePaths, bestHash string) (int64, []blockCadenceBucket, bool) {
	if !contextHasBudget(ctx, bitcoinCadenceMinBudget) {
		return 0, nil, false
	}
	cadenceCtx, cancel := context.WithTimeout(ctx, bitcoinCadenceTimeout)
	defer cancel()
	bestTime, buckets, err := getBitcoinLocalCadence(cadenceCtx, paths, bestHash)
	if err != nil || bestTime <= 0 {
		return 0, nil, false
	}
	return bestTime, buckets, true
}

func contextHasBudget(ctx context.Context, budget time.Duration) bool {
	if budget <= 0 {
		return true
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return true
	}
	return time.Until(deadline) >= budget
}

func parseBitcoinBlockHeaderRPC(body []byte) (bitcoinCLIBlockHeader, error) {
	var payload bitcoinBlockHeaderRPCResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return bitcoinCLIBlockHeader{}, err
	}
	if payload.Error != nil {
		return bitcoinCLIBlockHeader{}, errors.New(payload.Error.Message)
	}
	return payload.Result, nil
}

func doBitcoinRPCParams(ctx context.Context, url, user, pass, method string, params []any) ([]byte, error) {
	payload := map[string]any{
		"jsonrpc": "1.0",
		"id":      "lightningos",
		"method":  method,
		"params":  params,
	}
	buf, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(user, pass)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		msg := parseRPCError(body)
		return nil, rpcStatusError{statusCode: resp.StatusCode, message: msg}
	}
	if msg := parseRPCError(body); msg != "" {
		return nil, rpcStatusError{statusCode: resp.StatusCode, message: msg}
	}
	return body, nil
}

func fetchBitcoinRPCParams(ctx context.Context, host, user, pass, method string, params []any) ([]byte, error) {
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return doBitcoinRPCParams(ctx, host, user, pass, method, params)
	}

	body, err := doBitcoinRPCParams(ctx, "http://"+host, user, pass, method, params)
	if err == nil {
		return body, nil
	}
	var statusErr rpcStatusError
	if err != nil && errors.As(err, &statusErr) {
		return nil, err
	}

	body, httpsErr := doBitcoinRPCParams(ctx, "https://"+host, user, pass, method, params)
	if httpsErr == nil {
		return body, nil
	}
	if err != nil && httpsErr != nil {
		return nil, fmt.Errorf("rpc http failed: %v; https failed: %v", err, httpsErr)
	}
	if err != nil {
		return nil, err
	}
	return nil, httpsErr
}

func fetchBitcoinBlockHeaderRPC(ctx context.Context, host, user, pass, hash string) (bitcoinCLIBlockHeader, error) {
	body, err := fetchBitcoinRPCParams(ctx, host, user, pass, "getblockheader", []any{hash, true})
	if err != nil {
		return bitcoinCLIBlockHeader{}, err
	}
	return parseBitcoinBlockHeaderRPC(body)
}

func parseBitcoinLogTip(raw string) (bitcoinLogTip, bool) {
	var latest bitcoinLogTip
	found := false
	for _, line := range strings.Split(raw, "\n") {
		if !strings.Contains(line, "UpdateTip: new best=") {
			continue
		}
		hash, hashOK := bitcoinLogValue(line, "new best=", " ")
		heightRaw, heightOK := bitcoinLogValue(line, "height=", " ")
		dateRaw, dateOK := bitcoinLogValue(line, "date='", "'")
		progressRaw, progressOK := bitcoinLogValue(line, "progress=", " ")
		if !hashOK || !heightOK || !dateOK || !progressOK {
			continue
		}
		height, heightErr := strconv.ParseInt(heightRaw, 10, 64)
		progress, progressErr := strconv.ParseFloat(progressRaw, 64)
		blockTime, timeErr := time.Parse(time.RFC3339, dateRaw)
		if heightErr != nil || progressErr != nil || timeErr != nil || height < 0 || progress < 0 || progress > 1 {
			continue
		}
		latest = bitcoinLogTip{
			Hash:     hash,
			Height:   height,
			Time:     blockTime.Unix(),
			Progress: progress,
		}
		found = true
	}
	return latest, found
}

func bitcoinLogValue(line string, prefix string, terminator string) (string, bool) {
	start := strings.Index(line, prefix)
	if start < 0 {
		return "", false
	}
	value := line[start+len(prefix):]
	end := strings.Index(value, terminator)
	if end < 0 {
		return "", false
	}
	value = strings.TrimSpace(value[:end])
	return value, value != ""
}

func applyBitcoinLogTipToLocalStatus(status *bitcoinLocalStatus, tip bitcoinLogTip) {
	if status == nil {
		return
	}
	status.Chain = "main"
	status.Blocks = tip.Height
	status.BestBlockHash = tip.Hash
	status.BestBlockTime = tip.Time
	status.VerificationProgress = tip.Progress
	status.InitialBlockDownload = tip.Progress < 0.999999
}

func parseBitcoinCoreBrokerStatus(raw string) (bitcoinLocalStatus, error) {
	var status bitcoinLocalStatus
	if len(raw) == 0 || len(raw) > maxBitcoinBrokerStatusBytes {
		return status, errors.New("bitcoin broker status is invalid")
	}
	if err := json.Unmarshal([]byte(raw), &status); err != nil || status.Chain != "main" ||
		status.Blocks < 0 || status.Headers < 0 || status.Blocks > status.Headers ||
		status.VerificationProgress < 0 || status.VerificationProgress > 1 || strings.TrimSpace(status.BestBlockHash) == "" {
		return bitcoinLocalStatus{}, errors.New("bitcoin broker status is invalid")
	}
	status.RPCOk = true
	return status, nil
}

func applyBitcoinInfoToLocalStatus(status *bitcoinLocalStatus, info bitcoinInfo) {
	if status == nil {
		return
	}
	status.RPCOk = true
	status.Chain = info.Chain
	status.Blocks = info.Blocks
	status.Headers = info.Headers
	status.VerificationProgress = info.VerificationProgress
	status.InitialBlockDownload = info.InitialBlockDownload
	status.BestBlockHash = info.BestBlockHash
	status.Pruned = info.Pruned
	status.PruneHeight = info.PruneHeight
	status.PruneTargetSize = info.PruneTargetSize
	status.SizeOnDisk = info.SizeOnDisk
}

func applyBitcoinNetworkInfoToLocalStatus(status *bitcoinLocalStatus, info bitcoinNetworkInfo) {
	if status == nil {
		return
	}
	status.Version = info.Version
	status.Subversion = info.Subversion
	status.Connections = info.Connections
}

func applyBitcoinLocalStatusToStatus(status *bitcoinStatus, local bitcoinLocalStatus) {
	if status == nil {
		return
	}
	status.Installed = local.Installed
	status.Status = local.Status
	status.Source = local.Source
	status.DataDir = local.DataDir
	status.RPCOk = local.RPCOk
	status.Connections = local.Connections
	status.Version = local.Version
	status.Subversion = local.Subversion
	status.Chain = local.Chain
	status.Blocks = local.Blocks
	status.Headers = local.Headers
	status.VerificationProgress = local.VerificationProgress
	status.InitialBlockDownload = local.InitialBlockDownload
	status.BestBlockHash = local.BestBlockHash
	status.BestBlockTime = local.BestBlockTime
	status.Pruned = local.Pruned
	status.PruneHeight = local.PruneHeight
	status.PruneTargetSize = local.PruneTargetSize
	status.SizeOnDisk = local.SizeOnDisk
	status.BlockCadenceWindowSec = local.BlockCadenceWindowSec
	if len(local.BlockCadence) > 0 {
		status.BlockCadence = append([]blockCadenceBucket(nil), local.BlockCadence...)
	} else {
		status.BlockCadence = nil
	}
}

func applyBitcoinCLIChainInfoToLocalStatus(status *bitcoinLocalStatus, info bitcoinCLIChainInfo) {
	if status == nil {
		return
	}
	status.RPCOk = true
	status.Chain = info.Chain
	status.Blocks = info.Blocks
	status.Headers = info.Headers
	status.VerificationProgress = info.VerificationProgress
	status.InitialBlockDownload = info.InitialBlockDownload
	status.BestBlockHash = info.BestBlockHash
	status.Pruned = info.Pruned
	status.PruneHeight = info.PruneHeight
	status.PruneTargetSize = info.PruneTargetSize
	status.SizeOnDisk = info.SizeOnDisk
}

func applyBitcoinCLINetworkInfoToLocalStatus(status *bitcoinLocalStatus, info bitcoinCLINetworkInfo) {
	if status == nil {
		return
	}
	status.Version = info.Version
	status.Subversion = info.Subversion
	status.Connections = info.Connections
}

func readBitcoinCoreConfig(ctx context.Context, paths bitcoinCorePaths) (string, error) {
	raw, err := readBitcoinCoreConfigRaw(ctx, paths)
	if err != nil {
		return "", err
	}
	return sanitizeBitcoinCoreConfig(raw), nil
}

func readBitcoinCoreConfigRaw(ctx context.Context, paths bitcoinCorePaths) (string, error) {
	content, handled, err := system.ReadBitcoinCoreConfigWithBroker(ctx, paths.DataDir)
	if handled {
		if err != nil {
			return "", fmt.Errorf("read bitcoin.conf failed: %w", err)
		}
		return content, nil
	}
	return "", errors.New("read bitcoin.conf requires privileged broker enforce mode")
}

func writeBitcoinCoreConfig(ctx context.Context, paths bitcoinCorePaths, content string) error {
	if handled, err := system.WriteBitcoinCoreConfigWithBroker(ctx, paths.DataDir, ensureTrailingNewline(content)); handled {
		if err != nil {
			return fmt.Errorf("write bitcoin.conf failed: %w", err)
		}
		return nil
	}
	return errors.New("write bitcoin.conf requires privileged broker enforce mode")
}

func parseBitcoinCorePrune(raw string) (bool, int) {
	pruned := false
	pruneMiB := 0
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "prune=") {
			value := strings.TrimSpace(strings.TrimPrefix(trimmed, "prune="))
			if value == "" {
				continue
			}
			parsed, err := strconv.Atoi(value)
			if err != nil {
				continue
			}
			if parsed > 0 {
				pruned = true
				pruneMiB = parsed
			}
		}
	}
	if pruned && pruneMiB < bitcoinCoreMinPruneMiB {
		pruneMiB = bitcoinCoreMinPruneMiB
	}
	return pruned, pruneMiB
}

func parseBitcoinCoreRPCConfig(raw string) (string, string, string, string) {
	var user string
	var pass string
	var zmqBlock string
	var zmqTx string
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		switch key {
		case "rpcuser":
			user = value
		case "rpcpassword":
			pass = value
		case "zmqpubrawblock":
			zmqBlock = value
		case "zmqpubrawtx":
			zmqTx = value
		}
	}
	return user, pass, zmqBlock, zmqTx
}

func updateBitcoinCoreConfig(raw string, mode string, pruneMiB int) string {
	trimmed := strings.TrimRight(sanitizeBitcoinCoreConfig(raw), "\n")
	lines := []string{}
	if trimmed != "" {
		lines = strings.Split(trimmed, "\n")
	}
	updated := []string{}
	for _, line := range lines {
		check := strings.TrimSpace(line)
		if check != "" && !strings.HasPrefix(check, "#") && !strings.HasPrefix(check, ";") {
			if looksLikeEntrypointLog(check) {
				continue
			}
			if strings.HasPrefix(check, "prune=") {
				continue
			}
			if strings.HasPrefix(check, "txindex=") {
				continue
			}
		}
		updated = append(updated, line)
	}
	if mode == "pruned" {
		updated = append(updated, fmt.Sprintf("prune=%d", pruneMiB))
	} else {
		updated = append(updated, "txindex=1")
		updated = append(updated, "prune=0")
	}
	return ensureTrailingNewline(strings.Join(updated, "\n"))
}

func ensureTrailingNewline(value string) string {
	if value == "" {
		return "\n"
	}
	if strings.HasSuffix(value, "\n") {
		return value
	}
	return value + "\n"
}

func sanitizeBitcoinCoreConfig(raw string) string {
	lines := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if looksLikeEntrypointLog(trimmed) {
			continue
		}
		if key, value, ok := bitcoinCoreConfigKeyValue(trimmed); ok && strings.EqualFold(key, "rpcallowip") {
			if _, valid := normalizeRPCAllowIPValue(value); !valid {
				continue
			}
		}
		lines = append(lines, line)
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}

func bitcoinCoreConfigKeyValue(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
		return "", "", false
	}
	parts := strings.SplitN(trimmed, "=", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

func normalizeRPCAllowIPValue(value string) (string, bool) {
	trimmed := stripInlineConfigComment(strings.TrimSpace(value))
	if trimmed == "" {
		return "", false
	}
	if strings.Contains(trimmed, "/") {
		if _, cidr, err := net.ParseCIDR(trimmed); err == nil && cidr != nil {
			return cidr.String(), true
		}
		if normalized, ok := normalizeIPv4DottedMaskCIDR(trimmed); ok {
			return normalized, true
		}
		return "", false
	}
	ip := net.ParseIP(trimmed)
	if ip == nil {
		return "", false
	}
	return ip.String(), true
}

func stripInlineConfigComment(value string) string {
	for _, marker := range []string{"#", ";"} {
		if idx := strings.Index(value, marker); idx > 0 {
			prev := value[idx-1]
			if prev == ' ' || prev == '\t' {
				value = strings.TrimSpace(value[:idx])
			}
		}
	}
	return strings.TrimSpace(value)
}

func normalizeIPv4DottedMaskCIDR(value string) (string, bool) {
	ipPart, maskPart, ok := strings.Cut(value, "/")
	if !ok {
		return "", false
	}
	ip := net.ParseIP(strings.TrimSpace(ipPart)).To4()
	maskIP := net.ParseIP(strings.TrimSpace(maskPart)).To4()
	if ip == nil || maskIP == nil {
		return "", false
	}
	mask := net.IPMask(maskIP)
	ones, bits := mask.Size()
	if bits != 32 || ones < 0 {
		return "", false
	}
	network := net.IPNet{IP: ip.Mask(mask), Mask: mask}
	return network.String(), true
}

func looksLikeEntrypointLog(line string) bool {
	if line == "" {
		return false
	}
	if strings.Contains(line, "/entrypoint.sh:") {
		return true
	}
	if strings.Contains(line, "assuming uid:gid") {
		return true
	}
	if strings.Contains(line, "setting data directory") {
		return true
	}
	return false
}

func roundGB(value float64) float64 {
	return math.Round(value*100) / 100
}

func (s *Server) bitcoinLocalReady(ctx context.Context) (bool, string) {
	cfg, err := readBitcoinLocalRPCConfig(ctx)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "not installed") {
			return false, "not_installed"
		}
		return false, "rpc_unavailable"
	}
	return bitcoinRPCReady(ctx, cfg)
}
