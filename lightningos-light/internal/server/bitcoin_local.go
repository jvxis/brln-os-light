package server

import (
  "context"
  "encoding/json"
  "errors"
  "fmt"
  "math"
  "net/http"
  "os"
  "path/filepath"
  "strconv"
  "strings"
  "time"

  "lightningos-light/internal/system"
)

const (
  bitcoinCoreMinPruneMiB = 550
  bitcoinCoreConfigPathInContainer = "/home/bitcoin/.bitcoin/bitcoin.conf"
)

type bitcoinLocalStatus struct {
  Installed bool `json:"installed"`
  Status string `json:"status"`
  DataDir string `json:"data_dir"`
  RPCOk bool `json:"rpc_ok"`
  Connections int `json:"connections,omitempty"`
  Chain string `json:"chain,omitempty"`
  Blocks int64 `json:"blocks,omitempty"`
  Headers int64 `json:"headers,omitempty"`
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
}

type bitcoinCLINetworkInfo struct {
  Version int `json:"version"`
  Subversion string `json:"subversion"`
  Connections int `json:"connections"`
}

func (s *Server) handleBitcoinLocalStatus(w http.ResponseWriter, r *http.Request) {
  paths := bitcoinCoreAppPaths()
  resp := bitcoinLocalStatus{
    Installed: false,
    Status: "not_installed",
    DataDir: paths.DataDir,
  }
  if !fileExists(paths.ComposePath) {
    writeJSON(w, http.StatusOK, resp)
    return
  }
  resp.Installed = true

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

func roundGB(value float64) float64 {
  return math.Round(value*100) / 100
}
