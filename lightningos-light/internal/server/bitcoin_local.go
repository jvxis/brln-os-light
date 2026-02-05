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
  bitcoinCoreMinPruneMiB = 550
  bitcoinCoreConfigPathInContainer = "/home/bitcoin/.bitcoin/bitcoin.conf"
  blockCadenceWindowSec = 600
  blockCadenceBucketCount = 12
  blockCadenceCacheTTL = 60 * time.Second
  blockCadenceMaxSteps = 144
)

type bitcoinLocalStatus struct {
  Installed bool `json:"installed"`
  Status string `json:"status"`
  Source string `json:"source"`
  DataDir string `json:"data_dir"`
  RPCOk bool `json:"rpc_ok"`
  Connections int `json:"connections,omitempty"`
  Chain string `json:"chain,omitempty"`
  Blocks int64 `json:"blocks,omitempty"`
  Headers int64 `json:"headers,omitempty"`
  BestBlockTime int64 `json:"best_block_time,omitempty"`
  BlockCadenceWindowSec int64 `json:"block_cadence_window_sec,omitempty"`
  BlockCadence []blockCadenceBucket `json:"block_cadence,omitempty"`
  VerificationProgress float64 `json:"verification_progress,omitempty"`
  InitialBlockDownload bool `json:"initial_block_download,omitempty"`
  Version int `json:"version,omitempty"`
  Subversion string `json:"subversion,omitempty"`
  Pruned bool `json:"pruned,omitempty"`
  PruneHeight int64 `json:"prune_height,omitempty"`
  PruneTargetSize int64 `json:"prune_target_size,omitempty"`
  SizeOnDisk int64 `json:"size_on_disk,omitempty"`
}

type bitcoinLocalConfig struct {
  Mode string `json:"mode"`
  PruneSizeGB float64 `json:"prune_size_gb,omitempty"`
  MinPruneGB float64 `json:"min_prune_gb"`
  DataDir string `json:"data_dir"`
}

type bitcoinLocalConfigUpdate struct {
  Mode string `json:"mode"`
  PruneSizeGB float64 `json:"prune_size_gb"`
  ApplyNow bool `json:"apply_now"`
}

type bitcoinCLIChainInfo struct {
  Chain string `json:"chain"`
  Blocks int64 `json:"blocks"`
  Headers int64 `json:"headers"`
  VerificationProgress float64 `json:"verificationprogress"`
  InitialBlockDownload bool `json:"initialblockdownload"`
  Pruned bool `json:"pruned"`
  PruneHeight int64 `json:"pruneheight"`
  PruneTargetSize int64 `json:"prune_target_size"`
  SizeOnDisk int64 `json:"size_on_disk"`
  BestBlockHash string `json:"bestblockhash"`
}

type bitcoinCLINetworkInfo struct {
  Version int `json:"version"`
  Subversion string `json:"subversion"`
  Connections int `json:"connections"`
}

type blockCadenceBucket struct {
  StartTime int64 `json:"start_time"`
  EndTime int64 `json:"end_time"`
  Count int `json:"count"`
}

type bitcoinCLIBlockHeader struct {
  Time int64 `json:"time"`
  PreviousBlockHash string `json:"previousblockhash"`
}

type bitcoinBlockHeaderRPCResponse struct {
  Result bitcoinCLIBlockHeader `json:"result"`
  Error *rpcErrorDetail `json:"error"`
}

type blockCadenceCache struct {
  BestHash string
  BestTime int64
  Buckets []blockCadenceBucket
  ExpiresAt time.Time
}

var blockCadenceMu sync.Mutex
var blockCadenceState blockCadenceCache

func (s *Server) handleBitcoinLocalStatus(w http.ResponseWriter, r *http.Request) {
  paths := bitcoinCoreAppPaths()
  resp := bitcoinLocalStatus{
    Installed: false,
    Status: "not_installed",
    Source: "none",
    DataDir: paths.DataDir,
  }
  if !fileExists(paths.ComposePath) {
    ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
    defer cancel()
    cfg, _, err := readBitcoinLocalRPCConfig(ctx)
    if err != nil {
      writeJSON(w, http.StatusOK, resp)
      return
    }
    resp.Source = "external"
    resp.Status = "external"
    info, rpcErr := fetchBitcoinInfo(ctx, cfg.Host, cfg.User, cfg.Pass)
    if rpcErr != nil {
      resp.RPCOk = false
      writeJSON(w, http.StatusOK, resp)
      return
    }
    resp.RPCOk = true
    resp.Chain = info.Chain
    resp.Blocks = info.Blocks
    resp.Headers = info.Headers
    resp.VerificationProgress = info.VerificationProgress
    resp.InitialBlockDownload = info.InitialBlockDownload
    resp.Pruned = info.Pruned
    resp.PruneHeight = info.PruneHeight
    resp.PruneTargetSize = info.PruneTargetSize
    resp.SizeOnDisk = info.SizeOnDisk
    if netInfo, netErr := fetchBitcoinNetworkInfo(ctx, cfg.Host, cfg.User, cfg.Pass); netErr == nil {
      resp.Version = netInfo.Version
      resp.Subversion = netInfo.Subversion
      resp.Connections = netInfo.Connections
    }
    bestTime, buckets, cadenceErr := getBitcoinLocalCadence(ctx, paths, info.BestBlockHash)
    if cadenceErr == nil && bestTime > 0 {
      resp.BestBlockTime = bestTime
      resp.BlockCadenceWindowSec = blockCadenceWindowSec
      resp.BlockCadence = buckets
    }
    writeJSON(w, http.StatusOK, resp)
    return
  }
  resp.Installed = true
  resp.Source = "app"

  ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
  defer cancel()

  status, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, "bitcoind")
  if err != nil {
    resp.Status = "unknown"
    writeJSON(w, http.StatusOK, resp)
    return
  }
  resp.Status = status
  if status != "running" {
    writeJSON(w, http.StatusOK, resp)
    return
  }

  chainInfo, netInfo, err := fetchBitcoinLocalInfo(ctx, paths)
  if err != nil {
    resp.RPCOk = false
    writeJSON(w, http.StatusOK, resp)
    return
  }

  resp.RPCOk = true
  resp.Chain = chainInfo.Chain
  resp.Blocks = chainInfo.Blocks
  resp.Headers = chainInfo.Headers
  resp.VerificationProgress = chainInfo.VerificationProgress
  resp.InitialBlockDownload = chainInfo.InitialBlockDownload
  resp.Pruned = chainInfo.Pruned
  resp.PruneHeight = chainInfo.PruneHeight
  resp.PruneTargetSize = chainInfo.PruneTargetSize
  resp.SizeOnDisk = chainInfo.SizeOnDisk
  resp.Version = netInfo.Version
  resp.Subversion = netInfo.Subversion
  resp.Connections = netInfo.Connections

  bestTime, buckets, cadenceErr := getBitcoinLocalCadence(ctx, paths, chainInfo.BestBlockHash)
  if cadenceErr == nil && bestTime > 0 {
    resp.BestBlockTime = bestTime
    resp.BlockCadenceWindowSec = blockCadenceWindowSec
    resp.BlockCadence = buckets
  }

  writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleBitcoinLocalConfigGet(w http.ResponseWriter, r *http.Request) {
  paths := bitcoinCoreAppPaths()
  minPruneGB := roundGB(float64(bitcoinCoreMinPruneMiB) / 1024.0)
  resp := bitcoinLocalConfig{
    Mode: "full",
    MinPruneGB: minPruneGB,
    DataDir: paths.DataDir,
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

  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func fetchBitcoinLocalInfo(ctx context.Context, paths bitcoinCorePaths) (bitcoinCLIChainInfo, bitcoinCLINetworkInfo, error) {
  out, err := execBitcoinCLI(ctx, paths, "getblockchaininfo")
  if err != nil {
    return bitcoinCLIChainInfo{}, bitcoinCLINetworkInfo{}, err
  }
  chainInfo := bitcoinCLIChainInfo{}
  if err := json.Unmarshal([]byte(out), &chainInfo); err != nil {
    return bitcoinCLIChainInfo{}, bitcoinCLINetworkInfo{}, err
  }

  netOut, err := execBitcoinCLI(ctx, paths, "getnetworkinfo")
  if err != nil {
    return chainInfo, bitcoinCLINetworkInfo{}, err
  }
  netInfo := bitcoinCLINetworkInfo{}
  if err := json.Unmarshal([]byte(netOut), &netInfo); err != nil {
    return chainInfo, bitcoinCLINetworkInfo{}, err
  }

  return chainInfo, netInfo, nil
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
    bestTime, buckets, err = computeBitcoinLocalCadence(ctx, paths, trimmed)
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
    BestHash: trimmed,
    BestTime: bestTime,
    Buckets: buckets,
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
      EndTime: start + windowSec,
      Count: 0,
    }
  }

  current := header
  for steps := 0; steps < blockCadenceMaxSteps; steps++ {
    if current.Time < startTime {
      break
    }
    idx := int((current.Time - startTime) / windowSec)
    if idx >= 0 && idx < len(buckets) {
      buckets[idx].Count++
    }

    nextHash := strings.TrimSpace(current.PreviousBlockHash)
    if nextHash == "" {
      break
    }

    nextHeader, err := fetchBitcoinLocalBlockHeader(ctx, paths, nextHash)
    if err != nil {
      break
    }
    current = nextHeader
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
      EndTime: start + windowSec,
      Count: 0,
    }
  }

  current := header
  for steps := 0; steps < blockCadenceMaxSteps; steps++ {
    if current.Time < startTime {
      break
    }
    idx := int((current.Time - startTime) / windowSec)
    if idx >= 0 && idx < len(buckets) {
      buckets[idx].Count++
    }

    nextHash := strings.TrimSpace(current.PreviousBlockHash)
    if nextHash == "" {
      break
    }

    nextHeader, err := fetchBitcoinBlockHeaderRPC(ctx, cfg.Host, cfg.User, cfg.Pass, nextHash)
    if err != nil {
      break
    }
    current = nextHeader
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

func parseBitcoinBlockHeaderRPC(body []byte) (bitcoinCLIBlockHeader, error) {
  var payload bitcoinBlockHeaderRPCResponse
  if err := json.Unmarshal(body, &payload); err != nil {
    return bitcoinCLIBlockHeader{}, err
  }
  if payload.Error != nil {
    return bitcoinCLIBlockHeader{}, fmt.Errorf(payload.Error.Message)
  }
  return payload.Result, nil
}

func doBitcoinRPCParams(ctx context.Context, url, user, pass, method string, params []any) ([]byte, error) {
  payload := map[string]any{
    "jsonrpc": "1.0",
    "id": "lightningos",
    "method": method,
    "params": params,
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
  cliArgs := append([]string{
    "exec", "-i", containerID,
    "bitcoin-cli",
    "-conf=" + bitcoinCoreConfigPathInContainer,
    "-rpcwait",
    "-rpcwaittimeout=5",
  }, args...)
  out, err := system.RunCommandWithSudo(ctx, "docker", cliArgs...)
  if err != nil {
    return "", err
  }
  return strings.TrimSpace(out), nil
}

func readBitcoinCoreConfig(ctx context.Context, paths bitcoinCorePaths) (string, error) {
  if fileExists(paths.ConfigPath) {
    raw, err := os.ReadFile(paths.ConfigPath)
    if err == nil {
      return sanitizeBitcoinCoreConfig(string(raw)), nil
    }
  }

  containerID, err := composeContainerID(ctx, paths.Root, paths.ComposePath, "bitcoind")
  if err == nil && containerID != "" {
    out, execErr := system.RunCommandWithSudo(ctx, "docker", "exec", "-i", containerID, "sh", "-c", "cat "+bitcoinCoreConfigPathInContainer)
    if execErr == nil {
      return sanitizeBitcoinCoreConfig(out), nil
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
    return sanitizeBitcoinCoreConfig(out), nil
  }
  if strings.Contains(strings.ToLower(out), "no such file") {
    if fileExists(paths.SeedConfigPath) {
      raw, readErr := os.ReadFile(paths.SeedConfigPath)
      if readErr == nil {
        return sanitizeBitcoinCoreConfig(string(raw)), nil
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
    }
    updated = append(updated, line)
  }
  if mode == "pruned" {
    updated = append(updated, fmt.Sprintf("prune=%d", pruneMiB))
  } else {
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
    if looksLikeEntrypointLog(strings.TrimSpace(line)) {
      continue
    }
    lines = append(lines, line)
  }
  return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
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
  raw, err := readBitcoinCoreConfig(ctx, paths)
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

  changed := false
  for _, entry := range allow {
    trimmed := strings.TrimSpace(entry)
    if trimmed == "" {
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
  ip := net.ParseIP(value)
  if ip == nil {
    return false
  }
  for _, line := range lines {
    trimmed := strings.TrimSpace(line)
    if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
      continue
    }
    if !strings.HasPrefix(trimmed, "rpcallowip=") {
      continue
    }
    candidate := strings.TrimSpace(strings.TrimPrefix(trimmed, "rpcallowip="))
    if candidate == "" {
      continue
    }
    if strings.Contains(candidate, "/") {
      if _, cidr, err := net.ParseCIDR(candidate); err == nil && cidr.Contains(ip) {
        return true
      }
      continue
    }
    if net.ParseIP(candidate) != nil && candidate == value {
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
