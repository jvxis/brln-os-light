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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"lightningos-light/internal/system"
)

const (
	bitcoinCoreMinPruneMiB           = 550
	bitcoinCoreDataDirInContainer    = "/home/bitcoin/.bitcoin"
	bitcoinCoreConfigPathInContainer = bitcoinCoreDataDirInContainer + "/bitcoin.conf"
	blockCadenceWindowSec            = 600
	blockCadenceBucketCount          = 12
	blockCadenceCacheTTL             = 60 * time.Second
	blockCadenceMaxSteps             = 144
	bitcoinNetworkInfoTimeout        = 2 * time.Second
	bitcoinCadenceTimeout            = 8 * time.Second
	bitcoinCadenceMinBudget          = 3 * time.Second
	bitcoinCLIRPCWaitTimeoutSec      = 10
	bitcoinCLIRPCClientTimeoutSec    = 8
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
		cfg, _, err := readBitcoinLocalRPCConfig(ctx)
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

	status, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, "bitcoind")
	if err != nil {
		resp.Status = "unknown"
		return resp, nil
	}
	resp.Status = status
	if status != "running" {
		return resp, nil
	}

	logTip, logTipOK := fetchBitcoinLocalLogTip(ctx, paths)
	chainInfo, err := fetchBitcoinLocalChainInfoBest(ctx, paths)
	if err != nil {
		resp.RPCOk = false
		if logTipOK {
			applyBitcoinLogTipToLocalStatus(&resp, logTip)
		}
		return resp, nil
	}

	applyBitcoinCLIChainInfoToLocalStatus(&resp, chainInfo)
	if netInfo, ok := fetchBitcoinLocalNetworkInfoBestEffort(ctx, paths); ok {
		applyBitcoinCLINetworkInfoToLocalStatus(&resp, netInfo)
	}

	bestTime, buckets, cadenceOk := fetchBitcoinLocalCadenceBestEffort(ctx, paths, chainInfo.BestBlockHash)
	if cadenceOk {
		resp.BestBlockTime = bestTime
		resp.BlockCadenceWindowSec = blockCadenceWindowSec
		resp.BlockCadence = buckets
	}

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
	if err := writeFile(paths.SeedConfigPath, updated, 0640); err != nil {
		s.logger.Printf("bitcoin local: failed to update seed config: %v", err)
	}

	if req.ApplyNow {
		if err := runCompose(ctx, paths.Root, paths.ComposePath, "restart", "bitcoind"); err != nil {
			writeError(w, http.StatusInternalServerError, "restart failed")
			return
		}
	}
	s.invalidateBitcoinStatusCaches()

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func fetchBitcoinLocalChainInfo(ctx context.Context, paths bitcoinCorePaths) (bitcoinCLIChainInfo, error) {
	out, err := execBitcoinCLI(ctx, paths, "getblockchaininfo")
	if err != nil {
		return bitcoinCLIChainInfo{}, err
	}
	chainInfo := bitcoinCLIChainInfo{}
	if err := json.Unmarshal([]byte(out), &chainInfo); err != nil {
		return bitcoinCLIChainInfo{}, err
	}
	return chainInfo, nil
}

func fetchBitcoinLocalChainInfoBest(ctx context.Context, paths bitcoinCorePaths) (bitcoinCLIChainInfo, error) {
	if cfg, ok := readBitcoinCoreAppRPCConfig(ctx, paths); ok {
		info, err := fetchBitcoinInfo(ctx, cfg.Host, cfg.User, cfg.Pass)
		if err == nil {
			return bitcoinInfoToCLIChainInfo(info), nil
		}
	}
	return fetchBitcoinLocalChainInfo(ctx, paths)
}

func fetchBitcoinLocalNetworkInfo(ctx context.Context, paths bitcoinCorePaths) (bitcoinCLINetworkInfo, error) {
	netOut, err := execBitcoinCLI(ctx, paths, "getnetworkinfo")
	if err != nil {
		return bitcoinCLINetworkInfo{}, err
	}
	netInfo := bitcoinCLINetworkInfo{}
	if err := json.Unmarshal([]byte(netOut), &netInfo); err != nil {
		return bitcoinCLINetworkInfo{}, err
	}
	return netInfo, nil
}

func fetchBitcoinLocalNetworkInfoBest(ctx context.Context, paths bitcoinCorePaths) (bitcoinCLINetworkInfo, error) {
	if cfg, ok := readBitcoinCoreAppRPCConfig(ctx, paths); ok {
		info, err := fetchBitcoinNetworkInfo(ctx, cfg.Host, cfg.User, cfg.Pass)
		if err == nil {
			return bitcoinNetworkInfoToCLINetworkInfo(info), nil
		}
	}
	return fetchBitcoinLocalNetworkInfo(ctx, paths)
}

func readBitcoinCoreAppRPCConfig(ctx context.Context, paths bitcoinCorePaths) (bitcoinRPCConfig, bool) {
	if !fileExists(paths.ComposePath) {
		return bitcoinRPCConfig{}, false
	}
	raw, err := readBitcoinCoreConfigRaw(ctx, paths)
	if err != nil {
		return bitcoinRPCConfig{}, false
	}
	user, pass, zmqBlock, zmqTx := parseBitcoinCoreRPCConfig(raw)
	if user == "" || pass == "" {
		return bitcoinRPCConfig{}, false
	}
	return bitcoinRPCConfig{
		Host:     "127.0.0.1:8332",
		User:     user,
		Pass:     pass,
		ZMQBlock: normalizeLocalZMQ(zmqBlock, "tcp://127.0.0.1:28332"),
		ZMQTx:    normalizeLocalZMQ(zmqTx, "tcp://127.0.0.1:28333"),
	}, true
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
		computed := false
		if cfg, ok := readBitcoinCoreAppRPCConfig(ctx, paths); ok {
			bestTime, buckets, err = computeBitcoinLocalCadenceRPC(ctx, cfg, trimmed)
			if err == nil {
				computed = true
			}
		}
		if !computed {
			bestTime, buckets, err = computeBitcoinLocalCadence(ctx, paths, trimmed)
		}
	} else {
		cfg, _, cfgErr := readBitcoinLocalRPCConfig(ctx)
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

func computeBitcoinLocalCadence(ctx context.Context, paths bitcoinCorePaths, bestHash string) (int64, []blockCadenceBucket, error) {
	header, err := fetchBitcoinLocalBlockHeader(ctx, paths, bestHash)
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

		nextHeader, err := fetchBitcoinLocalBlockHeader(ctx, paths, nextHash)
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

func fetchBitcoinLocalBlockHeader(ctx context.Context, paths bitcoinCorePaths, hash string) (bitcoinCLIBlockHeader, error) {
	out, err := execBitcoinCLI(ctx, paths, "getblockheader", hash, "true")
	if err != nil {
		return bitcoinCLIBlockHeader{}, err
	}
	header := bitcoinCLIBlockHeader{}
	if err := json.Unmarshal([]byte(out), &header); err != nil {
		return bitcoinCLIBlockHeader{}, err
	}
	return header, nil
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

func fetchBitcoinLocalNetworkInfoBestEffort(ctx context.Context, paths bitcoinCorePaths) (bitcoinCLINetworkInfo, bool) {
	if !contextHasBudget(ctx, bitcoinCadenceMinBudget) {
		return bitcoinCLINetworkInfo{}, false
	}
	infoCtx, cancel := context.WithTimeout(ctx, bitcoinNetworkInfoTimeout)
	defer cancel()
	info, err := fetchBitcoinLocalNetworkInfoBest(infoCtx, paths)
	if err != nil {
		return bitcoinCLINetworkInfo{}, false
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

func execBitcoinCLI(ctx context.Context, paths bitcoinCorePaths, args ...string) (string, error) {
	containerID, err := composeContainerID(ctx, paths.Root, paths.ComposePath, "bitcoind")
	if err != nil {
		return "", err
	}
	if containerID == "" {
		return "", errors.New("bitcoind container not running")
	}
	cliArgs := bitcoinCLIExecArgs(containerID, args...)
	out, err := system.RunCommandWithSudo(ctx, "docker", cliArgs...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func bitcoinCLIExecArgs(containerID string, args ...string) []string {
	return append([]string{
		"exec", "-i", containerID,
		"bitcoin-cli",
		"-datadir=" + bitcoinCoreDataDirInContainer,
		"-conf=" + bitcoinCoreConfigPathInContainer,
		fmt.Sprintf("-rpcclienttimeout=%d", bitcoinCLIRPCClientTimeoutSec),
		"-rpcwait",
		fmt.Sprintf("-rpcwaittimeout=%d", bitcoinCLIRPCWaitTimeoutSec),
	}, args...)
}

func fetchBitcoinLocalLogTip(ctx context.Context, paths bitcoinCorePaths) (bitcoinLogTip, bool) {
	containerID, err := composeContainerID(ctx, paths.Root, paths.ComposePath, "bitcoind")
	if err != nil || containerID == "" {
		return bitcoinLogTip{}, false
	}
	out, err := system.RunCommandWithSudo(ctx, "docker", "logs", "--tail", "500", containerID)
	if err != nil {
		return bitcoinLogTip{}, false
	}
	return parseBitcoinLogTip(out)
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
	if fileExists(paths.ConfigPath) {
		raw, err := os.ReadFile(paths.ConfigPath)
		if err == nil {
			return string(raw), nil
		}
	}

	containerID, err := composeContainerID(ctx, paths.Root, paths.ComposePath, "bitcoind")
	if err == nil && containerID != "" {
		out, execErr := system.RunCommandWithSudo(ctx, "docker", "exec", "-i", containerID, "sh", "-c", "cat "+bitcoinCoreConfigPathInContainer)
		if execErr == nil {
			return out, nil
		}
	}

	if err := ensureBitcoinCoreImage(ctx); err != nil {
		return "", err
	}
	out, err := system.RunCommandWithSudo(
		ctx,
		"docker",
		"run",
		"--rm",
		"--entrypoint",
		"sh",
		"--user",
		"0:0",
		"-v",
		fmt.Sprintf("%s:/home/bitcoin/.bitcoin", paths.DataDir),
		bitcoinCoreImage,
		"-c",
		"cat "+bitcoinCoreConfigPathInContainer,
	)
	if err == nil {
		return out, nil
	}
	if strings.Contains(strings.ToLower(out), "no such file") {
		if fileExists(paths.SeedConfigPath) {
			raw, readErr := os.ReadFile(paths.SeedConfigPath)
			if readErr == nil {
				return string(raw), nil
			}
		}
	}
	msg := strings.TrimSpace(out)
	if msg == "" {
		return "", fmt.Errorf("read bitcoin.conf failed: %w", err)
	}
	return "", fmt.Errorf("read bitcoin.conf failed: %s", msg)
}

func writeBitcoinCoreConfig(ctx context.Context, paths bitcoinCorePaths, content string) error {
	tmpPath := filepath.Join(paths.Root, "bitcoin.conf.tmp")
	if err := writeFile(tmpPath, ensureTrailingNewline(content), 0640); err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	cmd := strings.Join([]string{
		"cp /tmp/bitcoin.conf " + bitcoinCoreConfigPathInContainer,
		"chown 101:101 " + bitcoinCoreConfigPathInContainer,
		"chmod 640 " + bitcoinCoreConfigPathInContainer,
	}, " && ")
	if err := ensureBitcoinCoreImage(ctx); err != nil {
		return err
	}
	out, err := system.RunCommandWithSudo(
		ctx,
		"docker",
		"run",
		"--rm",
		"--entrypoint",
		"sh",
		"--user",
		"0:0",
		"-v",
		fmt.Sprintf("%s:/home/bitcoin/.bitcoin", paths.DataDir),
		"-v",
		fmt.Sprintf("%s:/tmp/bitcoin.conf:ro", tmpPath),
		bitcoinCoreImage,
		"-c",
		cmd,
	)
	if err != nil {
		msg := strings.TrimSpace(out)
		if msg == "" {
			return fmt.Errorf("write bitcoin.conf failed: %w", err)
		}
		return fmt.Errorf("write bitcoin.conf failed: %s", msg)
	}
	return nil
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

func normalizeBitcoinCoreConfigText(raw string) string {
	return ensureTrailingNewline(strings.TrimRight(strings.ReplaceAll(raw, "\r\n", "\n"), "\n"))
}

func rpcAllowLineValue(line string) (string, bool) {
	key, value, ok := bitcoinCoreConfigKeyValue(line)
	if !ok || !strings.EqualFold(key, "rpcallowip") {
		return "", false
	}
	return normalizeRPCAllowIPValue(value)
}

func rpcAllowValueIsCIDR(value string) bool {
	return strings.Contains(value, "/")
}

func rpcAllowCIDRContainsIP(cidrValue string, ipValue string) bool {
	_, cidr, err := net.ParseCIDR(cidrValue)
	if err != nil || cidr == nil {
		return false
	}
	ip := net.ParseIP(ipValue)
	return ip != nil && cidr.Contains(ip)
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

func syncBitcoinCoreRPCAllowList(ctx context.Context, paths bitcoinCorePaths) (string, bool, error) {
	raw, err := readBitcoinCoreConfigRaw(ctx, paths)
	if err != nil {
		return "", false, err
	}

	allowList := []string{"127.0.0.1"}
	if gateway, gwErr := dockerGatewayIP(ctx); gwErr == nil && gateway != "" {
		allowList = append(allowList, gateway)
	}
	if containerID, idErr := composeContainerID(ctx, paths.Root, paths.ComposePath, "bitcoind"); idErr == nil && containerID != "" {
		for _, gateway := range dockerContainerGateways(ctx, containerID) {
			allowList = append(allowList, gateway)
		}
		for _, cidr := range dockerContainerCIDRs(ctx, containerID) {
			allowList = append(allowList, cidr)
		}
	}

	updated, changed := ensureBitcoinCoreRPCAllowList(raw, allowList)
	if !changed {
		return raw, false, nil
	}
	if err := writeBitcoinCoreConfig(ctx, paths, updated); err != nil {
		return "", false, err
	}
	_ = writeFile(paths.SeedConfigPath, updated, 0640)
	return updated, true, nil
}

func ensureBitcoinCoreRPCAllowList(raw string, allow []string) (string, bool) {
	normalized := sanitizeBitcoinCoreConfig(raw)
	lines := strings.Split(strings.TrimRight(normalized, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = []string{}
	}

	changed := normalized != normalizeBitcoinCoreConfigText(raw)
	for _, entry := range allow {
		trimmed, valid := normalizeRPCAllowIPValue(entry)
		if !valid {
			continue
		}
		if rpcAllowListContains(lines, trimmed) {
			continue
		}
		lines = append(lines, "rpcallowip="+trimmed)
		changed = true
	}

	if !changed {
		return normalized, false
	}
	return ensureTrailingNewline(strings.Join(lines, "\n")), true
}

func rpcAllowListContains(lines []string, value string) bool {
	value, valid := normalizeRPCAllowIPValue(value)
	if !valid {
		return false
	}
	if rpcAllowValueIsCIDR(value) {
		for _, line := range lines {
			candidate, ok := rpcAllowLineValue(line)
			if !ok || !rpcAllowValueIsCIDR(candidate) {
				continue
			}
			if candidate == value {
				return true
			}
		}
		return false
	}

	for _, line := range lines {
		candidate, ok := rpcAllowLineValue(line)
		if !ok {
			continue
		}
		if rpcAllowValueIsCIDR(candidate) {
			if rpcAllowCIDRContainsIP(candidate, value) {
				return true
			}
			continue
		}
		if candidate == value {
			return true
		}
	}
	return false
}

func roundGB(value float64) float64 {
	return math.Round(value*100) / 100
}

func (s *Server) bitcoinLocalReady(ctx context.Context) (bool, string) {
	cfg, _, err := readBitcoinLocalRPCConfig(ctx)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "not installed") {
			return false, "not_installed"
		}
		return false, "rpc_unavailable"
	}
	info, err := fetchBitcoinInfo(ctx, cfg.Host, cfg.User, cfg.Pass)
	if err != nil {
		return false, "rpc_unavailable"
	}
	if info.InitialBlockDownload {
		return false, "syncing"
	}
	if info.VerificationProgress < 0.9999 {
		return false, "syncing"
	}
	if info.Headers > 0 && info.Blocks < info.Headers {
		return false, "syncing"
	}
	return true, "ready"
}
