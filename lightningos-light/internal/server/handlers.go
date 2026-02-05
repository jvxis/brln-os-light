package server

import (
  "bytes"
  "context"
  "encoding/json"
  "errors"
  "fmt"
  "io"
  "net"
  "net/http"
  "net/url"
  "os"
  "path/filepath"
  "sort"
  "strconv"
  "strings"
  "time"

  "github.com/jackc/pgx/v5/pgxpool"

  "lightningos-light/internal/system"
)

const (
  secretsPath = "/etc/lightningos/secrets.env"
  lndConfPath = "/data/lnd/lnd.conf"
  lndPasswordPath = "/data/lnd/password.txt"
  lndWalletDBPath = "/data/lnd/data/chain/bitcoin/mainnet/wallet.db"
  lndAdminMacaroonPath = "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon"
  lndFixPermsScript = "/usr/local/sbin/lightningos-fix-lnd-perms"
  mempoolBaseURL = "https://mempool.space/api/v1/lightning"
  boostPeersDefaultLimit = 25
  boostPeersMaxLimit = 100
  lndRPCTimeout = 15 * time.Second
  lndConnectTimeout = 30 * time.Second
  lndOpenChannelTimeout = 60 * time.Second
  lndWarmupPeriod = 90 * time.Second
)

type healthIssue struct {
  Component string `json:"component"`
  Level string `json:"level"`
  Message string `json:"message"`
}

type healthResponse struct {
  Status string `json:"status"`
  Issues []healthIssue `json:"issues"`
  Timestamp string `json:"timestamp"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
  issues := []healthIssue{}
  status := "OK"

  lndCtx, lndCancel := context.WithTimeout(r.Context(), lndRPCTimeout)
  defer lndCancel()
  lndStatus, err := s.lnd.GetStatus(lndCtx)
  if err != nil {
    if isTimeoutError(err) {
      if s.lndWarmupActive() {
        issues = append(issues, healthIssue{Component: "lnd", Level: "WARN", Message: "LND warming up after restart (GetInfo timeout)"})
        status = elevate(status, "WARN")
      } else {
        probeCtx, probeCancel := context.WithTimeout(r.Context(), 3*time.Second)
        defer probeCancel()
        if _, peerErr := s.lnd.ListPeers(probeCtx); peerErr == nil {
          issues = append(issues, healthIssue{Component: "lnd", Level: "WARN", Message: "LND GetInfo timeout (gRPC reachable)"})
          status = elevate(status, "WARN")
        } else {
          issues = append(issues, healthIssue{Component: "lnd", Level: "ERR", Message: lndStatusMessage(err)})
          status = elevate(status, "ERR")
        }
      }
    } else {
      issues = append(issues, healthIssue{Component: "lnd", Level: "ERR", Message: lndStatusMessage(err)})
      status = elevate(status, "ERR")
    }
  } else if lndStatus.WalletState == "locked" {
    issues = append(issues, healthIssue{Component: "lnd", Level: "ERR", Message: "LND wallet locked"})
    status = elevate(status, "ERR")
  }

  btcCtx, btcCancel := context.WithTimeout(r.Context(), 3*time.Second)
  defer btcCancel()
  bitcoin, err := s.bitcoinStatus(btcCtx)
  if err != nil {
    issues = append(issues, healthIssue{Component: "bitcoin", Level: "WARN", Message: "Bitcoin remote check failed"})
    status = elevate(status, "WARN")
  } else {
    if !bitcoin.RPCOk {
      issues = append(issues, healthIssue{Component: "bitcoin", Level: "ERR", Message: "Bitcoin RPC unreachable"})
      status = elevate(status, "ERR")
    }
    if !bitcoin.ZMQRawBlockOk || !bitcoin.ZMQRawTxOk {
      issues = append(issues, healthIssue{Component: "bitcoin", Level: "WARN", Message: "Bitcoin ZMQ unreachable"})
      status = elevate(status, "WARN")
    }
  }

  pgCtx, pgCancel := context.WithTimeout(r.Context(), 3*time.Second)
  defer pgCancel()
  if !system.SystemctlIsActive(pgCtx, "postgresql") {
    issues = append(issues, healthIssue{Component: "postgres", Level: "ERR", Message: "Postgres inactive"})
    status = elevate(status, "ERR")
  }

  resp := healthResponse{
    Status: status,
    Issues: issues,
    Timestamp: time.Now().UTC().Format(time.RFC3339),
  }

  writeJSON(w, http.StatusOK, resp)
}

func elevate(current string, next string) string {
  if current == "ERR" || next == "OK" {
    return current
  }
  if next == "ERR" {
    return "ERR"
  }
  if current == "OK" && next == "WARN" {
    return "WARN"
  }
  return current
}

func isTimeoutError(err error) bool {
  if err == nil {
    return false
  }
  msg := strings.ToLower(err.Error())
  return strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "context deadline exceeded")
}

func lndStatusMessage(err error) string {
  if err == nil {
    return ""
  }
  msg := strings.ToLower(err.Error())
  if strings.Contains(msg, "wallet locked") || strings.Contains(msg, "unlock") {
    return "LND wallet locked"
  }
  if strings.Contains(msg, "macaroon") {
    if strings.Contains(msg, "permission denied") {
      return "LND macaroon unreadable (check permissions)"
    }
    if strings.Contains(msg, "no such file") {
      return "LND macaroon missing"
    }
    return "LND macaroon error"
  }
  if strings.Contains(msg, "tls") || strings.Contains(msg, "cert") {
    if strings.Contains(msg, "permission denied") {
      return "LND TLS cert unreadable (check permissions)"
    }
    if strings.Contains(msg, "no such file") {
      return "LND TLS cert missing"
    }
    return "LND TLS error"
  }
  if strings.Contains(msg, "connection refused") {
    return "LND gRPC connection refused"
  }
  if strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "deadline exceeded") {
    return "LND gRPC timeout (retrying)"
  }
  return "LND not reachable"
}

func lndRPCErrorMessage(err error) string {
  if err == nil {
    return ""
  }
  msg := strings.TrimSpace(err.Error())
  if msg == "" {
    return "LND error"
  }
  lower := strings.ToLower(msg)
  if idx := strings.Index(lower, "desc ="); idx != -1 {
    detail := strings.TrimSpace(msg[idx+len("desc ="):])
    if detail != "" {
      return detail
    }
  }
  return msg
}

func lndDetailedErrorMessage(err error) string {
  msg := lndRPCErrorMessage(err)
  if msg == "" || msg == "LND error" {
    return lndStatusMessage(err)
  }
  return msg
}

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
  ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
  defer cancel()

  stats, err := system.GetSystemStats(ctx)
  if err != nil {
    writeError(w, http.StatusInternalServerError, "system stats error")
    return
  }
  writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleDisk(w http.ResponseWriter, r *http.Request) {
  ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
  defer cancel()

  disks, err := system.ReadDiskSmart(ctx)
  if err != nil {
    writeError(w, http.StatusInternalServerError, "smartctl error")
    return
  }
  writeJSON(w, http.StatusOK, disks)
}

type postgresResponse struct {
  ServiceActive bool `json:"service_active"`
  DBName string `json:"db_name"`
  DBSizeMB int64 `json:"db_size_mb"`
  Connections int64 `json:"connections"`
  Version string `json:"version"`
  Databases []postgresDatabase `json:"databases,omitempty"`
}

type postgresDatabase struct {
  Name string `json:"name"`
  Source string `json:"source"`
  SizeMB int64 `json:"size_mb"`
  Connections int64 `json:"connections"`
  Available bool `json:"available"`
}

func (s *Server) handlePostgres(w http.ResponseWriter, r *http.Request) {
  ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
  defer cancel()

  entries := postgresDSNEntries()
  databases := make([]postgresDatabase, 0, len(entries))
  version := ""

  for _, entry := range entries {
    dbName := databaseNameFromDSN(entry.DSN)
    if dbName == "" {
      continue
    }
    db := postgresDatabase{
      Name: dbName,
      Source: entry.Source,
    }
    pool, err := pgxpool.New(ctx, entry.DSN)
    if err == nil {
      db.Available = true
      var sizeBytes int64
      _ = pool.QueryRow(ctx, "select pg_database_size($1)", dbName).Scan(&sizeBytes)
      db.SizeMB = sizeBytes / (1024 * 1024)

      var connections int64
      _ = pool.QueryRow(ctx, "select count(*) from pg_stat_activity where datname=$1", dbName).Scan(&connections)
      db.Connections = connections

      if version == "" {
        _ = pool.QueryRow(ctx, "show server_version").Scan(&version)
      }
      pool.Close()
    }
    databases = append(databases, db)
  }

  resp := postgresResponse{
    ServiceActive: system.SystemctlIsActive(ctx, "postgresql"),
    DBName: s.cfg.Postgres.DBName,
    Databases: databases,
    Version: version,
  }

  if len(databases) > 0 {
    resp.DBName = databases[0].Name
    resp.DBSizeMB = databases[0].SizeMB
    resp.Connections = databases[0].Connections
  }

  writeJSON(w, http.StatusOK, resp)
}

type postgresDSNEntry struct {
  Source string
  DSN string
}

func postgresDSNEntries() []postgresDSNEntry {
  entries := []postgresDSNEntry{}
  if dsn := strings.TrimSpace(os.Getenv("LND_PG_DSN")); dsn != "" && !isPlaceholderDSN(dsn) {
    entries = append(entries, postgresDSNEntry{Source: "lnd", DSN: dsn})
  }
  if dsn := strings.TrimSpace(os.Getenv("NOTIFICATIONS_PG_DSN")); dsn != "" && !isPlaceholderDSN(dsn) {
    entries = append(entries, postgresDSNEntry{Source: "lightningos", DSN: dsn})
  }
  return entries
}

func databaseNameFromDSN(raw string) string {
  if strings.TrimSpace(raw) == "" {
    return ""
  }
  parsed, err := url.Parse(raw)
  if err != nil {
    return ""
  }
  name := strings.TrimPrefix(parsed.Path, "/")
  return strings.TrimSpace(name)
}

type bitcoinStatus struct {
  Mode string `json:"mode"`
  RPCHost string `json:"rpchost"`
  ZMQRawBlock string `json:"zmq_rawblock"`
  ZMQRawTx string `json:"zmq_rawtx"`
  RPCOk bool `json:"rpc_ok"`
  ZMQRawBlockOk bool `json:"zmq_rawblock_ok"`
  ZMQRawTxOk bool `json:"zmq_rawtx_ok"`
  Version int `json:"version,omitempty"`
  Subversion string `json:"subversion,omitempty"`
  Chain string `json:"chain,omitempty"`
  Blocks int64 `json:"blocks,omitempty"`
  Headers int64 `json:"headers,omitempty"`
  VerificationProgress float64 `json:"verification_progress,omitempty"`
  InitialBlockDownload bool `json:"initial_block_download,omitempty"`
  BestBlockHash string `json:"best_block_hash,omitempty"`
}

type mempoolConnectivityNode struct {
  PublicKey string `json:"publicKey"`
  Alias string `json:"alias"`
}

type mempoolNodeInfo struct {
  PublicKey string `json:"public_key"`
  Alias string `json:"alias"`
  Sockets string `json:"sockets"`
}

type mempoolFeeRecommendation struct {
  FastestFee int `json:"fastestFee"`
  HalfHourFee int `json:"halfHourFee"`
  HourFee int `json:"hourFee"`
  EconomyFee int `json:"economyFee"`
  MinimumFee int `json:"minimumFee"`
}

type boostPeersRequest struct {
  Limit int `json:"limit"`
}

type boostPeerResult struct {
  Pubkey string `json:"pubkey"`
  Alias string `json:"alias"`
  Socket string `json:"socket,omitempty"`
  Status string `json:"status"`
  Error string `json:"error,omitempty"`
}

type boostPeersResponse struct {
  Requested int `json:"requested"`
  Attempted int `json:"attempted"`
  Connected int `json:"connected"`
  Skipped int `json:"skipped"`
  Failed int `json:"failed"`
  Results []boostPeerResult `json:"results"`
}

type bitcoinRPCConfig struct {
  Host string
  User string
  Pass string
  ZMQBlock string
  ZMQTx string
}

func (s *Server) handleBitcoin(w http.ResponseWriter, r *http.Request) {
  ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
  defer cancel()

  status, err := s.bitcoinStatus(ctx)
  if err != nil {
    writeError(w, http.StatusInternalServerError, "bitcoin status error")
    return
  }
  writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleBitcoinActive(w http.ResponseWriter, r *http.Request) {
  ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
  defer cancel()

  source := readBitcoinSource()
  if source == "local" {
    status, err := s.bitcoinLocalStatusActive(ctx)
    if err != nil {
      writeError(w, http.StatusInternalServerError, "bitcoin local status error")
      return
    }
    writeJSON(w, http.StatusOK, status)
    return
  }

  status, err := s.bitcoinStatus(ctx)
  if err != nil {
    writeError(w, http.StatusInternalServerError, "bitcoin status error")
    return
  }
  writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleBitcoinSourceGet(w http.ResponseWriter, r *http.Request) {
  writeJSON(w, http.StatusOK, map[string]string{"source": readBitcoinSource()})
}

func (s *Server) handleBitcoinSourcePost(w http.ResponseWriter, r *http.Request) {
  var req struct {
    Source string `json:"source"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  source := strings.ToLower(strings.TrimSpace(req.Source))
  if source == "" {
    source = "remote"
  }
  if source != "remote" && source != "local" {
    writeError(w, http.StatusBadRequest, "source must be local or remote")
    return
  }
  if source == "local" {
    readyCtx, readyCancel := context.WithTimeout(r.Context(), 6*time.Second)
    defer readyCancel()
    ready, _ := s.bitcoinLocalReady(readyCtx)
    if !ready {
      writeError(w, http.StatusBadRequest, "local bitcoin is not fully synced")
      return
    }
  }

  remoteUser, remotePass := readBitcoinSecrets()
  if remoteUser == "" || remotePass == "" {
    writeError(w, http.StatusBadRequest, "remote RPC credentials missing")
    return
  }
  remoteCfg := bitcoinRPCConfig{
    Host: s.cfg.BitcoinRemote.RPCHost,
    User: remoteUser,
    Pass: remotePass,
    ZMQBlock: s.cfg.BitcoinRemote.ZMQRawBlock,
    ZMQTx: s.cfg.BitcoinRemote.ZMQRawTx,
  }

  localCfg, localUpdated, err := readBitcoinLocalRPCConfig(r.Context())
  if err != nil && source == "local" {
    writeError(w, http.StatusInternalServerError, err.Error())
    return
  }
  if err != nil && source != "local" {
    localCfg = bitcoinRPCConfig{
      Host: "127.0.0.1:8332",
      ZMQBlock: "tcp://127.0.0.1:28332",
      ZMQTx: "tcp://127.0.0.1:28333",
    }
    localUpdated = false
  }

  if err := updateLNDConfBitcoinSource(source, remoteCfg, localCfg); err != nil {
    writeError(w, http.StatusInternalServerError, "failed to update lnd.conf")
    return
  }
  if err := storeBitcoinSource(source); err != nil {
    s.logger.Printf("failed to store bitcoin source: %v", err)
  }

  needsBitcoinRestart := source == "local" && localUpdated
  if source == "local" && !needsBitcoinRestart {
    rpcCtx, rpcCancel := context.WithTimeout(r.Context(), 4*time.Second)
    defer rpcCancel()
    if _, err := fetchBitcoinInfo(rpcCtx, localCfg.Host, localCfg.User, localCfg.Pass); err != nil {
      var statusErr rpcStatusError
      if errors.As(err, &statusErr) && statusErr.statusCode == http.StatusForbidden {
        needsBitcoinRestart = true
      }
    }
  }

  if needsBitcoinRestart {
    paths := bitcoinCoreAppPaths()
    ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
    defer cancel()
    if err := runCompose(ctx, paths.Root, paths.ComposePath, "restart", "bitcoind"); err != nil {
      writeError(w, http.StatusInternalServerError, "bitcoin restart failed")
      return
    }
  }

  ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
  defer cancel()
  if err := system.SystemctlRestart(ctx, "lnd"); err != nil {
    if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
      s.markLNDRestart()
      writeJSON(w, http.StatusOK, map[string]any{
        "ok": true,
        "warning": "LND restart is taking longer than expected. Check status in a moment.",
      })
      return
    }
    writeError(w, http.StatusInternalServerError, "lnd restart failed")
    return
  }
  s.markLNDRestart()

  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) bitcoinStatus(ctx context.Context) (bitcoinStatus, error) {
  rpcUser := os.Getenv("BITCOIN_RPC_USER")
  rpcPass := os.Getenv("BITCOIN_RPC_PASS")
  if rpcUser == "" || rpcPass == "" {
    fileUser, filePass := readBitcoinSecrets()
    if rpcUser == "" {
      rpcUser = fileUser
    }
    if rpcPass == "" {
      rpcPass = filePass
    }
  }
  status := bitcoinStatus{
    Mode: "remote",
    RPCHost: s.cfg.BitcoinRemote.RPCHost,
    ZMQRawBlock: s.cfg.BitcoinRemote.ZMQRawBlock,
    ZMQRawTx: s.cfg.BitcoinRemote.ZMQRawTx,
  }

  if rpcUser != "" && rpcPass != "" {
    info, err := fetchBitcoinInfo(ctx, s.cfg.BitcoinRemote.RPCHost, rpcUser, rpcPass)
    if err == nil {
      status.RPCOk = true
      status.Chain = info.Chain
      status.Blocks = info.Blocks
      status.Headers = info.Headers
      status.VerificationProgress = info.VerificationProgress
      status.InitialBlockDownload = info.InitialBlockDownload
      status.BestBlockHash = info.BestBlockHash
      if netInfo, netErr := fetchBitcoinNetworkInfo(ctx, s.cfg.BitcoinRemote.RPCHost, rpcUser, rpcPass); netErr == nil {
        status.Version = netInfo.Version
        status.Subversion = netInfo.Subversion
      }
    } else {
      status.RPCOk = false
    }
  }

  status.ZMQRawBlockOk = testTCP(s.cfg.BitcoinRemote.ZMQRawBlock)
  status.ZMQRawTxOk = testTCP(s.cfg.BitcoinRemote.ZMQRawTx)

  return status, nil
}

func (s *Server) bitcoinLocalStatusActive(ctx context.Context) (bitcoinStatus, error) {
  paths := bitcoinCoreAppPaths()
  status := bitcoinStatus{
    Mode: "local",
    RPCHost: "127.0.0.1:8332",
    ZMQRawBlock: "tcp://127.0.0.1:28332",
    ZMQRawTx: "tcp://127.0.0.1:28333",
  }
  if !fileExists(paths.ComposePath) {
    cfg, _, err := readBitcoinLocalRPCConfig(ctx)
    if err == nil && strings.TrimSpace(cfg.Host) != "" {
      status.RPCHost = cfg.Host
      if strings.TrimSpace(cfg.ZMQBlock) != "" {
        status.ZMQRawBlock = cfg.ZMQBlock
      }
      if strings.TrimSpace(cfg.ZMQTx) != "" {
        status.ZMQRawTx = cfg.ZMQTx
      }
      if strings.TrimSpace(cfg.User) != "" && strings.TrimSpace(cfg.Pass) != "" {
        info, rpcErr := fetchBitcoinInfo(ctx, cfg.Host, cfg.User, cfg.Pass)
        if rpcErr == nil {
          status.RPCOk = true
          status.Chain = info.Chain
          status.Blocks = info.Blocks
          status.Headers = info.Headers
          status.VerificationProgress = info.VerificationProgress
          status.InitialBlockDownload = info.InitialBlockDownload
          status.BestBlockHash = info.BestBlockHash
          if netInfo, netErr := fetchBitcoinNetworkInfo(ctx, cfg.Host, cfg.User, cfg.Pass); netErr == nil {
            status.Version = netInfo.Version
            status.Subversion = netInfo.Subversion
          }
        } else {
          status.RPCOk = false
        }
      }
    }
    status.ZMQRawBlockOk = testTCP(status.ZMQRawBlock)
    status.ZMQRawTxOk = testTCP(status.ZMQRawTx)
    return status, nil
  }
  info, netInfo, err := fetchBitcoinLocalInfo(ctx, paths)
  if err == nil {
    status.RPCOk = true
    status.Chain = info.Chain
    status.Blocks = info.Blocks
    status.Headers = info.Headers
    status.VerificationProgress = info.VerificationProgress
    status.InitialBlockDownload = info.InitialBlockDownload
    status.BestBlockHash = info.BestBlockHash
    status.Version = netInfo.Version
    status.Subversion = netInfo.Subversion
  }
  status.ZMQRawBlockOk = testTCP(status.ZMQRawBlock)
  status.ZMQRawTxOk = testTCP(status.ZMQRawTx)
  return status, nil
}

func readBitcoinLocalRPCConfig(ctx context.Context) (bitcoinRPCConfig, bool, error) {
  paths := bitcoinCoreAppPaths()
  if !fileExists(paths.ComposePath) {
    configCandidates := []string{
      paths.ConfigPath,
      "/etc/bitcoin/bitcoin.conf",
      "/var/lib/bitcoind/bitcoin.conf",
      "/home/bitcoin/.bitcoin/bitcoin.conf",
      "/root/.bitcoin/bitcoin.conf",
    }
    for _, candidate := range configCandidates {
      if cfg, ok := readBitcoinConfRPCConfig(candidate); ok {
        return cfg, false, nil
      }
    }
    if cfg, ok := readBitcoinTaggedRPCConfigFromLNDConf("local"); ok {
      return cfg, false, nil
    }
    if cfg, ok := readBitcoindRPCConfigFromLNDConf(); ok {
      if isLocalRPCHost(cfg.Host) {
        return cfg, false, nil
      }
    }
    return bitcoinRPCConfig{}, false, errors.New("bitcoin core is not installed")
  }
  raw, updated, err := syncBitcoinCoreRPCAllowList(ctx, paths)
  if err != nil {
    return bitcoinRPCConfig{}, false, fmt.Errorf("failed to read local bitcoin.conf: %w", err)
  }
  user, pass, zmqBlock, zmqTx := parseBitcoinCoreRPCConfig(raw)
  if user == "" || pass == "" {
    return bitcoinRPCConfig{}, false, errors.New("local RPC credentials missing")
  }
  zmqBlock = normalizeLocalZMQ(zmqBlock, "tcp://127.0.0.1:28332")
  zmqTx = normalizeLocalZMQ(zmqTx, "tcp://127.0.0.1:28333")
  return bitcoinRPCConfig{
    Host: "127.0.0.1:8332",
    User: user,
    Pass: pass,
    ZMQBlock: zmqBlock,
    ZMQTx: zmqTx,
  }, updated, nil
}

func readBitcoinConfRPCConfig(path string) (bitcoinRPCConfig, bool) {
  raw, err := os.ReadFile(path)
  if err != nil {
    return bitcoinRPCConfig{}, false
  }
  normalized := strings.ReplaceAll(string(raw), "\r\n", "\n")
  user, pass, zmqBlock, zmqTx := parseBitcoinCoreRPCConfig(normalized)
  if strings.TrimSpace(user) == "" || strings.TrimSpace(pass) == "" {
    return bitcoinRPCConfig{}, false
  }
  zmqBlock = normalizeLocalZMQ(zmqBlock, "tcp://127.0.0.1:28332")
  zmqTx = normalizeLocalZMQ(zmqTx, "tcp://127.0.0.1:28333")

  host := "127.0.0.1:8332"
  if port, ok := parseBitcoinRPCPortFromConf(normalized); ok {
    host = fmt.Sprintf("127.0.0.1:%d", port)
  }
  return bitcoinRPCConfig{
    Host: host,
    User: user,
    Pass: pass,
    ZMQBlock: zmqBlock,
    ZMQTx: zmqTx,
  }, true
}

func parseBitcoinRPCPortFromConf(raw string) (int, bool) {
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
    if key != "rpcport" {
      continue
    }
    value := strings.TrimSpace(parts[1])
    if value == "" {
      continue
    }
    port, err := strconv.Atoi(value)
    if err != nil || port <= 0 {
      return 0, false
    }
    return port, true
  }
  return 0, false
}

func readBitcoindRPCConfigFromLNDConf() (bitcoinRPCConfig, bool) {
  raw, err := os.ReadFile(lndConfPath)
  if err != nil {
    return bitcoinRPCConfig{}, false
  }
  lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")

  inBitcoind := false
  cfg := bitcoinRPCConfig{
    Host: "127.0.0.1:8332",
    ZMQBlock: "tcp://127.0.0.1:28332",
    ZMQTx: "tcp://127.0.0.1:28333",
  }

  for _, line := range lines {
    trimmed := strings.TrimSpace(line)
    if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
      inBitcoind = strings.EqualFold(trimmed, "[Bitcoind]")
      continue
    }
    if !inBitcoind {
      continue
    }
    if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
      continue
    }
    parts := strings.SplitN(trimmed, "=", 2)
    if len(parts) != 2 {
      continue
    }
    key := strings.TrimSpace(parts[0])
    val := strings.TrimSpace(parts[1])

    switch key {
    case "bitcoind.rpchost":
      if val != "" {
        host := strings.TrimPrefix(val, "tcp://")
        if !strings.Contains(host, ":") {
          host = host + ":8332"
        }
        cfg.Host = host
      }
    case "bitcoind.rpcuser":
      cfg.User = val
    case "bitcoind.rpcpass":
      cfg.Pass = val
    case "bitcoind.zmqpubrawblock":
      cfg.ZMQBlock = normalizeLocalZMQ(val, cfg.ZMQBlock)
    case "bitcoind.zmqpubrawtx":
      cfg.ZMQTx = normalizeLocalZMQ(val, cfg.ZMQTx)
    }
  }

  if strings.TrimSpace(cfg.Host) == "" {
    return bitcoinRPCConfig{}, false
  }
  if strings.TrimSpace(cfg.User) == "" || strings.TrimSpace(cfg.Pass) == "" {
    return bitcoinRPCConfig{}, false
  }
  return cfg, true
}

func readBitcoinTaggedRPCConfigFromLNDConf(tag string) (bitcoinRPCConfig, bool) {
  raw, err := os.ReadFile(lndConfPath)
  if err != nil {
    return bitcoinRPCConfig{}, false
  }
  lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")

  inBitcoind := false
  inTarget := false
  cfg := bitcoinRPCConfig{
    Host: "127.0.0.1:8332",
    ZMQBlock: "tcp://127.0.0.1:28332",
    ZMQTx: "tcp://127.0.0.1:28333",
  }

  for _, line := range lines {
    trimmed := strings.TrimSpace(line)
    if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
      inBitcoind = strings.EqualFold(trimmed, "[Bitcoind]")
      inTarget = false
      continue
    }
    if !inBitcoind {
      continue
    }
    if trimmed == "" {
      continue
    }
    marker := strings.TrimSpace(strings.TrimLeft(trimmed, "#;"))
    if strings.EqualFold(marker, "LightningOS Bitcoin Remote") {
      inTarget = strings.EqualFold(tag, "remote")
      continue
    }
    if strings.EqualFold(marker, "LightningOS Bitcoin Local") {
      inTarget = strings.EqualFold(tag, "local")
      continue
    }
    if !inTarget {
      continue
    }
    clean := strings.TrimSpace(strings.TrimLeft(trimmed, "#;"))
    parts := strings.SplitN(clean, "=", 2)
    if len(parts) != 2 {
      continue
    }
    key := strings.TrimSpace(parts[0])
    val := strings.TrimSpace(parts[1])
    switch key {
    case "bitcoind.rpchost":
      if val != "" {
        host := strings.TrimPrefix(val, "tcp://")
        if !strings.Contains(host, ":") {
          host = host + ":8332"
        }
        cfg.Host = host
      }
    case "bitcoind.rpcuser":
      cfg.User = val
    case "bitcoind.rpcpass":
      cfg.Pass = val
    case "bitcoind.zmqpubrawblock":
      cfg.ZMQBlock = normalizeLocalZMQ(val, cfg.ZMQBlock)
    case "bitcoind.zmqpubrawtx":
      cfg.ZMQTx = normalizeLocalZMQ(val, cfg.ZMQTx)
    }
  }

  if strings.TrimSpace(cfg.Host) == "" {
    return bitcoinRPCConfig{}, false
  }
  if strings.TrimSpace(cfg.User) == "" || strings.TrimSpace(cfg.Pass) == "" {
    return bitcoinRPCConfig{}, false
  }
  return cfg, true
}

func readBitcoinSecrets() (string, string) {
  content, err := os.ReadFile(secretsPath)
  if err != nil {
    return "", ""
  }
  var user string
  var pass string
  for _, line := range strings.Split(string(content), "\n") {
    if strings.HasPrefix(line, "BITCOIN_RPC_USER=") {
      user = strings.TrimPrefix(line, "BITCOIN_RPC_USER=")
    }
    if strings.HasPrefix(line, "BITCOIN_RPC_PASS=") {
      pass = strings.TrimPrefix(line, "BITCOIN_RPC_PASS=")
    }
  }
  return strings.TrimSpace(user), strings.TrimSpace(pass)
}

func testTCP(addr string) bool {
  host, port, err := splitHostPort(addr)
  if err != nil {
    return false
  }
  d := net.Dialer{Timeout: 2 * time.Second}
  conn, err := d.Dial("tcp", net.JoinHostPort(host, port))
  if err != nil {
    return false
  }
  _ = conn.Close()
  return true
}

func splitHostPort(input string) (string, string, error) {
  if strings.HasPrefix(input, "tcp://") {
    input = strings.TrimPrefix(input, "tcp://")
  }
  if strings.Contains(input, "://") {
    return "", "", fmt.Errorf("invalid address")
  }
  host, port, err := net.SplitHostPort(input)
  return host, port, err
}

type lndStatusResponse struct {
  ServiceActive bool `json:"service_active"`
  WalletState string `json:"wallet_state"`
  SyncedToChain bool `json:"synced_to_chain"`
  SyncedToGraph bool `json:"synced_to_graph"`
  BlockHeight int64 `json:"block_height"`
  Version string `json:"version"`
  Pubkey string `json:"pubkey"`
  URI string `json:"uri"`
  InfoKnown bool `json:"info_known"`
  InfoStale bool `json:"info_stale"`
  InfoAgeSeconds int64 `json:"info_age_seconds"`
  Channels struct {
    Active int `json:"active"`
    Inactive int `json:"inactive"`
  } `json:"channels"`
  Balances struct {
    OnchainSat int64 `json:"onchain_sat"`
    LightningSat int64 `json:"lightning_sat"`
  } `json:"balances"`
}

func (s *Server) handleLNDStatus(w http.ResponseWriter, r *http.Request) {
  ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
  defer cancel()

  resp := lndStatusResponse{}
  resp.ServiceActive = system.SystemctlIsActive(ctx, "lnd")

  status, err := s.lnd.GetStatus(ctx)
  _ = err
  resp.WalletState = status.WalletState
  resp.SyncedToChain = status.SyncedToChain
  resp.SyncedToGraph = status.SyncedToGraph
  resp.BlockHeight = status.BlockHeight
  resp.Version = status.Version
  resp.Pubkey = status.Pubkey
  resp.URI = status.URI
  resp.InfoKnown = status.InfoKnown
  resp.InfoStale = status.InfoStale
  resp.InfoAgeSeconds = status.InfoAgeSeconds
  resp.Channels.Active = status.ChannelsActive
  resp.Channels.Inactive = status.ChannelsInactive
  resp.Balances.OnchainSat = status.OnchainSat
  resp.Balances.LightningSat = status.LightningSat

  writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleWizardStatus(w http.ResponseWriter, r *http.Request) {
  writeJSON(w, http.StatusOK, map[string]any{
    "wallet_exists": walletExists(),
  })
}

type wizardBitcoinReq struct {
  RPCUser string `json:"rpcuser"`
  RPCPass string `json:"rpcpass"`
}

func (s *Server) handleWizardBitcoinRemote(w http.ResponseWriter, r *http.Request) {
  var req wizardBitcoinReq
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  user := strings.TrimSpace(req.RPCUser)
  pass := strings.TrimSpace(req.RPCPass)
  if user == "" || pass == "" {
    writeError(w, http.StatusBadRequest, "rpcuser and rpcpass required")
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
  defer cancel()

  info, err := fetchBitcoinInfo(ctx, s.cfg.BitcoinRemote.RPCHost, user, pass)
  if err != nil {
    msg := "bitcoin rpc check failed"
    msg = fmt.Sprintf("bitcoin rpc check failed: %v", err)
    s.logger.Printf("bitcoin rpc check failed: %v", err)
    writeError(w, http.StatusBadRequest, msg)
    return
  }

  if err := storeBitcoinSecrets(user, pass); err != nil {
    s.logger.Printf("failed to store secrets: %v", err)
    writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to store secrets: %v", err))
    return
  }

  if err := updateLNDConfRPC(
    ctx,
    user,
    pass,
    s.cfg.BitcoinRemote.RPCHost,
    s.cfg.BitcoinRemote.ZMQRawBlock,
    s.cfg.BitcoinRemote.ZMQRawTx,
  ); err != nil {
    writeError(w, http.StatusInternalServerError, "failed to update lnd.conf")
    return
  }

  _ = storeBitcoinSource("remote")

  _ = system.SystemctlRestart(ctx, "lnd")

  writeJSON(w, http.StatusOK, map[string]any{"ok": true, "info": info})
}

func (s *Server) handleCreateWallet(w http.ResponseWriter, r *http.Request) {
  var req struct {
    WalletPassword string `json:"wallet_password"`
    SeedPassphrase string `json:"seed_passphrase"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }

  if walletExists() {
    writeError(w, http.StatusConflict, "wallet already exists")
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
  defer cancel()

  seedPassphrase := strings.TrimSpace(req.SeedPassphrase)
  seed, err := s.lnd.GenSeed(ctx, seedPassphrase)
  if err != nil {
    s.logger.Printf("gen seed failed: %v", err)
    writeError(w, http.StatusInternalServerError, fmt.Sprintf("gen seed failed: %v", err))
    return
  }

  writeJSON(w, http.StatusOK, map[string]any{"seed_words": seed})
}

func walletExists() bool {
  if walletPasswordAvailable() {
    return true
  }
  info, err := os.Stat(lndWalletDBPath)
  if err != nil {
    return false
  }
  return info.Size() > 0
}

func (s *Server) handleInitWallet(w http.ResponseWriter, r *http.Request) {
  var req struct {
    WalletPassword string `json:"wallet_password"`
    SeedWords []string `json:"seed_words"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  if req.WalletPassword == "" || len(req.SeedWords) == 0 {
    writeError(w, http.StatusBadRequest, "wallet_password and seed_words required")
    return
  }
  if walletExists() {
    writeError(w, http.StatusConflict, "wallet already exists")
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
  defer cancel()

  if err := s.lnd.InitWallet(ctx, req.WalletPassword, req.SeedWords); err != nil {
    writeError(w, http.StatusInternalServerError, "init wallet failed")
    return
  }
  if err := storeWalletUnlock(req.WalletPassword); err != nil {
    s.logger.Printf("wallet unlock setup failed: %v", err)
    writeError(w, http.StatusInternalServerError, "wallet unlock setup failed")
    return
  }
  s.scheduleLNDPermissionsFix("init wallet")

  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleUnlockWallet(w http.ResponseWriter, r *http.Request) {
  var req struct {
    WalletPassword string `json:"wallet_password"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  if req.WalletPassword == "" {
    writeError(w, http.StatusBadRequest, "wallet_password required")
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
  defer cancel()

  if err := s.lnd.UnlockWallet(ctx, req.WalletPassword); err != nil {
    writeError(w, http.StatusInternalServerError, "unlock failed")
    return
  }
  if err := storeWalletUnlock(req.WalletPassword); err != nil {
    s.logger.Printf("wallet unlock setup failed: %v", err)
    writeError(w, http.StatusInternalServerError, "wallet unlock setup failed")
    return
  }
  s.scheduleLNDPermissionsFix("unlock wallet")

  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
  var req struct {
    Service string `json:"service"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }

  service := mapService(req.Service)
  if service == "" {
    writeError(w, http.StatusBadRequest, "unsupported service")
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
  defer cancel()

  if err := system.SystemctlRestart(ctx, service); err != nil {
    writeError(w, http.StatusInternalServerError, "restart failed")
    return
  }
  if service == "lnd" {
    s.markLNDRestart()
  }

  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSystemAction(w http.ResponseWriter, r *http.Request) {
  var req struct {
    Action string `json:"action"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }

  action := strings.ToLower(strings.TrimSpace(req.Action))
  switch action {
  case "restart":
    action = "reboot"
  case "shutdown":
    action = "poweroff"
  }
  if action != "reboot" && action != "poweroff" {
    writeError(w, http.StatusBadRequest, "unsupported action")
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
  defer cancel()

  if err := system.SystemctlPower(ctx, action); err != nil {
    writeError(w, http.StatusInternalServerError, "system action failed")
    return
  }

  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func mapService(name string) string {
  switch name {
  case "lnd":
    return "lnd"
  case "lightningos-manager":
    return "lightningos-manager"
  case "lightningos-elements", "elementsd":
    return elementsServiceName
  case "lightningos-peerswapd", "peerswapd":
    return peerswapServiceName
  case "lightningos-psweb", "psweb":
    return pswebServiceName
  case "lnd-upgrade", "lightningos-lnd-upgrade":
    return lndUpgradeUnitName
  case "postgresql":
    return "postgresql"
  default:
    return ""
  }
}

func parsePeerAddress(address string) (string, string, error) {
  trimmed := strings.TrimSpace(address)
  if trimmed == "" {
    return "", "", errors.New("address required")
  }
  parts := strings.Split(trimmed, "@")
  if len(parts) != 2 {
    return "", "", errors.New("address must be pubkey@host")
  }
  pubkey := strings.TrimSpace(parts[0])
  host := strings.TrimSpace(parts[1])
  if pubkey == "" || host == "" {
    return "", "", errors.New("address must be pubkey@host")
  }
  return pubkey, host, nil
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
  service := r.URL.Query().Get("service")
  linesRaw := r.URL.Query().Get("lines")

  lines := 200
  if linesRaw != "" {
    if v, err := strconv.Atoi(linesRaw); err == nil {
      lines = v
    }
  }

  service = mapService(service)
  if service == "" {
    writeError(w, http.StatusBadRequest, "unsupported service")
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
  defer cancel()

  out, err := system.JournalTail(ctx, service, lines)
  if err != nil {
    writeError(w, http.StatusInternalServerError, fmt.Sprintf("log read failed: %v", err))
    return
  }

  writeJSON(w, http.StatusOK, map[string]any{"service": service, "lines": out})
}

func (s *Server) handleLNDConfigGet(w http.ResponseWriter, r *http.Request) {
  raw, _ := os.ReadFile(lndConfPath)

  current := parseLNDUserConf(string(raw))

  resp := map[string]any{
    "supported": map[string]bool{
      "alias": true,
      "color": true,
      "min_channel_size_sat": true,
      "max_channel_size_sat": true,
    },
    "current": map[string]any{
      "alias": current.Alias,
      "color": current.Color,
      "min_channel_size_sat": current.MinChanSize,
      "max_channel_size_sat": current.MaxChanSize,
    },
    "raw_user_conf": string(raw),
  }

  writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleLNChannels(w http.ResponseWriter, r *http.Request) {
  ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
  defer cancel()

  channels, err := s.lnd.ListChannels(ctx)
  if err != nil {
    writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
    return
  }

  pending, pendingErr := s.lnd.ListPendingChannels(ctx)
  if pendingErr != nil {
    pending = nil
  }

  active := 0
  inactive := 0
  for _, ch := range channels {
    if ch.Active {
      active++
    } else {
      inactive++
    }
  }

  pendingOpen := 0
  pendingClose := 0
  for _, ch := range pending {
    if ch.Status == "opening" {
      pendingOpen++
      continue
    }
    pendingClose++
  }

  writeJSON(w, http.StatusOK, map[string]any{
    "active_count": active,
    "inactive_count": inactive,
    "pending_open_count": pendingOpen,
    "pending_close_count": pendingClose,
    "channels": channels,
    "pending_channels": pending,
  })
}

func (s *Server) handleLNPeers(w http.ResponseWriter, r *http.Request) {
  ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
  defer cancel()

  peers, err := s.lnd.ListPeers(ctx)
  if err != nil {
    writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
    return
  }

  writeJSON(w, http.StatusOK, map[string]any{"peers": peers})
}

func (s *Server) handleLNConnectPeer(w http.ResponseWriter, r *http.Request) {
  var req struct {
    Address string `json:"address"`
    Pubkey string `json:"pubkey"`
    Host string `json:"host"`
    Perm *bool `json:"perm"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }

  pubkey := strings.TrimSpace(req.Pubkey)
  host := strings.TrimSpace(req.Host)
  if req.Address != "" {
    parsedPubkey, parsedHost, err := parsePeerAddress(req.Address)
    if err != nil {
      writeError(w, http.StatusBadRequest, err.Error())
      return
    }
    pubkey = parsedPubkey
    host = parsedHost
  }
  if pubkey == "" || host == "" {
    writeError(w, http.StatusBadRequest, "pubkey and host required")
    return
  }

  perm := true
  if req.Perm != nil {
    perm = *req.Perm
  }

  ctx, cancel := context.WithTimeout(r.Context(), lndConnectTimeout)
  defer cancel()

  if err := s.lnd.ConnectPeerWithTimeout(ctx, pubkey, host, perm, uint64(lndConnectTimeout/time.Second)); err != nil {
    writeError(w, http.StatusInternalServerError, peerConnectErrorMessage(err))
    return
  }

  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLNDisconnectPeer(w http.ResponseWriter, r *http.Request) {
  var req struct {
    Pubkey string `json:"pubkey"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  pubkey := strings.TrimSpace(req.Pubkey)
  if pubkey == "" {
    writeError(w, http.StatusBadRequest, "pubkey required")
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
  defer cancel()

  if err := s.lnd.DisconnectPeer(ctx, pubkey); err != nil {
    writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
    return
  }

  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLNBoostPeers(w http.ResponseWriter, r *http.Request) {
  var req boostPeersRequest
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }

  limit := req.Limit
  if limit <= 0 {
    limit = boostPeersDefaultLimit
  }
  if limit > boostPeersMaxLimit {
    limit = boostPeersMaxLimit
  }

  peersCtx, peersCancel := context.WithTimeout(r.Context(), lndRPCTimeout)
  peers, err := s.lnd.ListPeers(peersCtx)
  peersCancel()
  if err != nil {
    writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
    return
  }

  existing := map[string]bool{}
  for _, peer := range peers {
    if peer.PubKey != "" {
      existing[peer.PubKey] = true
    }
  }

  rankingCtx, rankingCancel := context.WithTimeout(r.Context(), 10*time.Second)
  ranking, err := fetchMempoolConnectivity(rankingCtx)
  rankingCancel()
  if err != nil {
    s.logger.Printf("mempool connectivity fetch failed: %v", err)
    writeError(w, http.StatusInternalServerError, "mempool connectivity fetch failed")
    return
  }

  if limit > len(ranking) {
    limit = len(ranking)
  }

  resp := boostPeersResponse{
    Requested: limit,
  }
  results := make([]boostPeerResult, 0, limit)

  for i := 0; i < limit; i++ {
    node := ranking[i]
    pubkey := strings.TrimSpace(node.PublicKey)
    alias := strings.TrimSpace(node.Alias)
    if pubkey == "" {
      results = append(results, boostPeerResult{
        Alias: alias,
        Status: "skipped",
        Error: "missing pubkey",
      })
      resp.Skipped++
      continue
    }
    if existing[pubkey] {
      results = append(results, boostPeerResult{
        Pubkey: pubkey,
        Alias: alias,
        Status: "skipped",
        Error: "already connected",
      })
      resp.Skipped++
      continue
    }

    infoCtx, infoCancel := context.WithTimeout(r.Context(), 8*time.Second)
    info, err := fetchMempoolNodeInfo(infoCtx, pubkey)
    infoCancel()
    if err != nil {
      results = append(results, boostPeerResult{
        Pubkey: pubkey,
        Alias: alias,
        Status: "failed",
        Error: "mempool node lookup failed",
      })
      resp.Failed++
      continue
    }
    if alias == "" {
      alias = strings.TrimSpace(info.Alias)
    }
    socket := firstSocket(info.Sockets)
    if socket == "" {
      results = append(results, boostPeerResult{
        Pubkey: pubkey,
        Alias: alias,
        Status: "skipped",
        Error: "no socket found",
      })
      resp.Skipped++
      continue
    }

    connectCtx, connectCancel := context.WithTimeout(r.Context(), lndRPCTimeout)
    err = s.lnd.ConnectPeer(connectCtx, pubkey, socket, true)
    connectCancel()
    resp.Attempted++
    if err != nil {
      if isAlreadyConnected(err) {
        results = append(results, boostPeerResult{
          Pubkey: pubkey,
          Alias: alias,
          Socket: socket,
          Status: "skipped",
          Error: "already connected",
        })
        resp.Skipped++
      } else {
        results = append(results, boostPeerResult{
          Pubkey: pubkey,
          Alias: alias,
          Socket: socket,
          Status: "failed",
          Error: err.Error(),
        })
        resp.Failed++
      }
      continue
    }

    existing[pubkey] = true
    results = append(results, boostPeerResult{
      Pubkey: pubkey,
      Alias: alias,
      Socket: socket,
      Status: "connected",
    })
    resp.Connected++
  }

  resp.Results = results
  writeJSON(w, http.StatusOK, resp)
}

func fetchMempoolConnectivity(ctx context.Context) ([]mempoolConnectivityNode, error) {
  var nodes []mempoolConnectivityNode
  url := mempoolBaseURL + "/nodes/rankings/connectivity"
  if err := fetchMempoolJSON(ctx, url, &nodes); err != nil {
    return nil, err
  }
  return nodes, nil
}

func fetchMempoolNodeInfo(ctx context.Context, pubkey string) (mempoolNodeInfo, error) {
  var info mempoolNodeInfo
  url := mempoolBaseURL + "/nodes/" + pubkey
  if err := fetchMempoolJSON(ctx, url, &info); err != nil {
    return mempoolNodeInfo{}, err
  }
  return info, nil
}

func fetchMempoolJSON(ctx context.Context, url string, dst any) error {
  req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
  if err != nil {
    return err
  }
  client := &http.Client{Timeout: 10 * time.Second}
  resp, err := client.Do(req)
  if err != nil {
    return err
  }
  defer resp.Body.Close()
  if resp.StatusCode != http.StatusOK {
    body, _ := io.ReadAll(resp.Body)
    msg := strings.TrimSpace(string(body))
    if msg == "" {
      msg = resp.Status
    }
    return fmt.Errorf("mempool api error: %s", msg)
  }
  return json.NewDecoder(resp.Body).Decode(dst)
}

func firstSocket(raw string) string {
  if raw == "" {
    return ""
  }
  parts := strings.Split(raw, ",")
  if len(parts) == 0 {
    return ""
  }
  socket := strings.TrimSpace(parts[0])
  if socket == "" {
    return ""
  }
  if strings.Contains(socket, "@") {
    pieces := strings.SplitN(socket, "@", 2)
    if len(pieces) == 2 {
      socket = strings.TrimSpace(pieces[1])
    }
  }
  return socket
}

func isAlreadyConnected(err error) bool {
  if err == nil {
    return false
  }
  msg := strings.ToLower(err.Error())
  return strings.Contains(msg, "already connected") ||
    strings.Contains(msg, "already have a connection") ||
    strings.Contains(msg, "already connected to peer")
}

func peerConnectErrorMessage(err error) string {
  if err == nil {
    return ""
  }
  if isTimeoutError(err) {
    return "Peer connection timed out"
  }
  msg := lndRPCErrorMessage(err)
  if msg == "" || msg == "LND error" {
    msg = lndStatusMessage(err)
  }
  if msg == "" {
    msg = "Peer connection failed"
  }
  return msg
}

func (s *Server) handleLNOpenChannel(w http.ResponseWriter, r *http.Request) {
  var req struct {
    PeerAddress string `json:"peer_address"`
    Pubkey string `json:"pubkey"`
    LocalFundingSat int64 `json:"local_funding_sat"`
    CloseAddress string `json:"close_address"`
    Private bool `json:"private"`
    SatPerVbyte int64 `json:"sat_per_vbyte"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  peerAddress := strings.TrimSpace(req.PeerAddress)
  if peerAddress == "" {
    peerAddress = strings.TrimSpace(req.Pubkey)
  }
  if peerAddress == "" {
    writeError(w, http.StatusBadRequest, "peer_address required")
    return
  }
  if req.LocalFundingSat <= 0 {
    writeError(w, http.StatusBadRequest, "local_funding_sat must be positive")
    return
  }
  if req.SatPerVbyte < 0 {
    writeError(w, http.StatusBadRequest, "sat_per_vbyte must be zero or positive")
    return
  }

  pubkey, host, err := parsePeerAddress(peerAddress)
  if err != nil {
    writeError(w, http.StatusBadRequest, err.Error())
    return
  }
  if !strings.Contains(host, ":") {
    writeError(w, http.StatusBadRequest, "peer host must include host:port")
    return
  }

  connectCtx, connectCancel := context.WithTimeout(r.Context(), lndConnectTimeout)
  if err := s.lnd.ConnectPeerWithTimeout(connectCtx, pubkey, host, false, uint64(lndConnectTimeout/time.Second)); err != nil && !isAlreadyConnected(err) {
    connectCancel()
    writeError(w, http.StatusInternalServerError, lndRPCErrorMessage(err))
    return
  }
  connectCancel()

  openCtx, openCancel := context.WithTimeout(r.Context(), lndOpenChannelTimeout)
  defer openCancel()

  channelPoint, err := s.lnd.OpenChannel(openCtx, pubkey, req.LocalFundingSat, req.CloseAddress, req.Private, req.SatPerVbyte)
  if err != nil {
    writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
    return
  }

  writeJSON(w, http.StatusOK, map[string]string{"channel_point": channelPoint})
}

func (s *Server) handleMempoolFees(w http.ResponseWriter, r *http.Request) {
  ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
  defer cancel()

  url := "https://mempool.space/api/v1/fees/recommended"
  var fees mempoolFeeRecommendation
  if err := fetchMempoolJSON(ctx, url, &fees); err != nil {
    writeError(w, http.StatusInternalServerError, "mempool fee fetch failed")
    return
  }
  writeJSON(w, http.StatusOK, fees)
}

func (s *Server) handleLNCloseChannel(w http.ResponseWriter, r *http.Request) {
  var req struct {
    ChannelPoint string `json:"channel_point"`
    Force bool `json:"force"`
    SatPerVbyte int64 `json:"sat_per_vbyte"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  if strings.TrimSpace(req.ChannelPoint) == "" {
    writeError(w, http.StatusBadRequest, "channel_point required")
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
  defer cancel()

  if req.SatPerVbyte < 0 {
    writeError(w, http.StatusBadRequest, "sat_per_vbyte must be zero or positive")
    return
  }

  if err := s.lnd.CloseChannel(ctx, req.ChannelPoint, req.Force, req.SatPerVbyte); err != nil {
    writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
    return
  }

  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLNUpdateFees(w http.ResponseWriter, r *http.Request) {
  var req struct {
    ChannelPoint string `json:"channel_point"`
    ApplyAll bool `json:"apply_all"`
    BaseFeeMsat int64 `json:"base_fee_msat"`
    FeeRatePpm int64 `json:"fee_rate_ppm"`
    TimeLockDelta int64 `json:"time_lock_delta"`
    InboundEnabled bool `json:"inbound_enabled"`
    InboundBaseMsat int64 `json:"inbound_base_msat"`
    InboundFeeRatePpm int64 `json:"inbound_fee_rate_ppm"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  if req.BaseFeeMsat < 0 || req.FeeRatePpm < 0 || req.TimeLockDelta < 0 {
    writeError(w, http.StatusBadRequest, "fees must be zero or positive")
    return
  }
  if req.ApplyAll && strings.TrimSpace(req.ChannelPoint) != "" {
    writeError(w, http.StatusBadRequest, "use apply_all or channel_point, not both")
    return
  }
  if !req.ApplyAll && strings.TrimSpace(req.ChannelPoint) == "" {
    writeError(w, http.StatusBadRequest, "channel_point required unless apply_all=true")
    return
  }
  if req.BaseFeeMsat == 0 && req.FeeRatePpm == 0 && req.TimeLockDelta == 0 && !req.InboundEnabled {
    writeError(w, http.StatusBadRequest, "at least one fee field is required")
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
  defer cancel()

  if err := s.lnd.UpdateChannelFees(ctx, req.ChannelPoint, req.ApplyAll, req.BaseFeeMsat, req.FeeRatePpm, req.TimeLockDelta, req.InboundEnabled, req.InboundBaseMsat, req.InboundFeeRatePpm); err != nil {
    if isTimeoutError(err) {
      writeJSON(w, http.StatusOK, map[string]any{
        "ok": true,
        "warning": "Update sent. LND is syncing; policy may already be updated.",
      })
      return
    }
    writeError(w, http.StatusInternalServerError, lndRPCErrorMessage(err))
    return
  }

  writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLNUpdateChanStatus(w http.ResponseWriter, r *http.Request) {
  var req struct {
    ChannelPoint string `json:"channel_point"`
    Enabled *bool `json:"enabled"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  if strings.TrimSpace(req.ChannelPoint) == "" {
    writeError(w, http.StatusBadRequest, "channel_point required")
    return
  }
  if req.Enabled == nil {
    writeError(w, http.StatusBadRequest, "enabled required")
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
  defer cancel()

  if err := s.lnd.UpdateChanStatus(ctx, req.ChannelPoint, *req.Enabled); err != nil {
    writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
    return
  }

  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLNChannelFees(w http.ResponseWriter, r *http.Request) {
  channelPoint := strings.TrimSpace(r.URL.Query().Get("channel_point"))
  if channelPoint == "" {
    writeError(w, http.StatusBadRequest, "channel_point required")
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
  defer cancel()

  policy, err := s.lnd.GetChannelPolicy(ctx, channelPoint)
  if err != nil {
    writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
    return
  }

  writeJSON(w, http.StatusOK, map[string]any{
    "channel_point": policy.ChannelPoint,
    "base_fee_msat": policy.BaseFeeMsat,
    "fee_rate_ppm": policy.FeeRatePpm,
    "time_lock_delta": policy.TimeLockDelta,
    "inbound_base_msat": policy.InboundBaseMsat,
    "inbound_fee_rate_ppm": policy.InboundFeeRatePpm,
  })
}

func (s *Server) handleChatMessages(w http.ResponseWriter, r *http.Request) {
  if s.chat == nil {
    writeError(w, http.StatusServiceUnavailable, "chat unavailable")
    return
  }
  peerPubkey := strings.TrimSpace(r.URL.Query().Get("peer_pubkey"))
  if peerPubkey == "" {
    writeError(w, http.StatusBadRequest, "peer_pubkey required")
    return
  }
  if !isValidPubkeyHex(peerPubkey) {
    writeError(w, http.StatusBadRequest, "invalid peer_pubkey")
    return
  }
  limit := chatMessageLimitDefault
  if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
    if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
      limit = parsed
    }
  }
  items, err := s.chat.Messages(peerPubkey, limit)
  if err != nil {
    writeError(w, http.StatusInternalServerError, "failed to load chat messages")
    return
  }
  writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleChatInbox(w http.ResponseWriter, r *http.Request) {
  if s.chat == nil {
    writeError(w, http.StatusServiceUnavailable, "chat unavailable")
    return
  }
  items, err := s.chat.Inbox()
  if err != nil {
    writeError(w, http.StatusInternalServerError, "failed to load chat inbox")
    return
  }
  writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleChatSend(w http.ResponseWriter, r *http.Request) {
  if s.chat == nil {
    writeError(w, http.StatusServiceUnavailable, "chat unavailable")
    return
  }
  var req struct {
    PeerPubkey string `json:"peer_pubkey"`
    Message string `json:"message"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  peerPubkey := strings.TrimSpace(req.PeerPubkey)
  if peerPubkey == "" {
    writeError(w, http.StatusBadRequest, "peer_pubkey required")
    return
  }
  if !isValidPubkeyHex(peerPubkey) {
    writeError(w, http.StatusBadRequest, "invalid peer_pubkey")
    return
  }
  message := strings.TrimSpace(req.Message)
  if err := validateChatMessage(message); err != nil {
    writeError(w, http.StatusBadRequest, err.Error())
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
  defer cancel()

  msg, err := s.chat.SendMessage(ctx, peerPubkey, message)
  if err != nil {
    detail := lndRPCErrorMessage(err)
    if isTimeoutError(err) {
      detail = lndStatusMessage(err)
    }
    if detail == "" || detail == "LND error" {
      detail = "Keysend failed"
    }
    writeError(w, http.StatusInternalServerError, detail)
    return
  }
  writeJSON(w, http.StatusOK, msg)
}

type lndUserConf struct {
  Alias string
  Color string
  MinChanSize int64
  MaxChanSize int64
}

func parseLNDUserConf(raw string) lndUserConf {
  conf := lndUserConf{}
  section := ""
  for _, line := range strings.Split(raw, "\n") {
    line = strings.TrimSpace(line)
    if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
      continue
    }
    if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
      section = strings.Trim(line, "[]")
      continue
    }
    if section != "Application Options" {
      continue
    }
    parts := strings.SplitN(line, "=", 2)
    if len(parts) != 2 {
      continue
    }
    key := strings.TrimSpace(parts[0])
    value := strings.TrimSpace(parts[1])
    switch key {
    case "alias":
      conf.Alias = value
    case "color":
      conf.Color = value
    case "minchansize":
      conf.MinChanSize, _ = strconv.ParseInt(value, 10, 64)
    case "maxchansize":
      conf.MaxChanSize, _ = strconv.ParseInt(value, 10, 64)
    }
  }
  return conf
}

func (s *Server) handleLNDConfigPost(w http.ResponseWriter, r *http.Request) {
  var req struct {
    Alias string `json:"alias"`
    Color string `json:"color"`
    MinChannelSizeSat int64 `json:"min_channel_size_sat"`
    MaxChannelSizeSat int64 `json:"max_channel_size_sat"`
    ApplyNow bool `json:"apply_now"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }

  if req.MinChannelSizeSat < 0 || req.MaxChannelSizeSat < 0 {
    writeError(w, http.StatusBadRequest, "channel sizes must be positive")
    return
  }
  if req.MinChannelSizeSat > 0 && req.MaxChannelSizeSat > 0 && req.MinChannelSizeSat >= req.MaxChannelSizeSat {
    writeError(w, http.StatusBadRequest, "min channel must be lower than max")
    return
  }
  if len(req.Alias) > 32 {
    writeError(w, http.StatusBadRequest, "alias too long")
    return
  }
  if strings.TrimSpace(req.Color) != "" && !isHexColor(req.Color) {
    writeError(w, http.StatusBadRequest, "color must be hex (#RRGGBB)")
    return
  }

  raw, err := os.ReadFile(lndConfPath)
  if err != nil {
    writeError(w, http.StatusInternalServerError, "failed to read lnd.conf")
    return
  }
  updated := updateLNDConfOptions(string(raw), req.Alias, req.Color, req.MinChannelSizeSat, req.MaxChannelSizeSat)
  if walletPasswordAvailable() {
    updated = ensureUnlockLines(updated)
  }
  if err := os.WriteFile(lndConfPath, []byte(updated), 0660); err != nil {
    writeError(w, http.StatusInternalServerError, "failed to write lnd.conf")
    return
  }

  if req.ApplyNow {
    ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
    defer cancel()
    if system.SystemctlRestart(ctx, "lnd") == nil {
      s.markLNDRestart()
    }
  }

  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLNDConfigRaw(w http.ResponseWriter, r *http.Request) {
  var req struct {
    RawUserConf string `json:"raw_user_conf"`
    ApplyNow bool `json:"apply_now"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }

  prev, _ := os.ReadFile(lndConfPath)
  updated := req.RawUserConf
  if walletPasswordAvailable() {
    updated = ensureUnlockLines(updated)
  }
  if err := os.WriteFile(lndConfPath, []byte(updated), 0660); err != nil {
    writeError(w, http.StatusInternalServerError, "failed to write lnd.conf")
    return
  }

  warning := ""
  if req.ApplyNow {
    ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
    defer cancel()
    if err := system.SystemctlRestart(ctx, "lnd"); err != nil {
      if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
        warning = "LND restart is taking longer than expected. Check status in a moment."
      } else {
        _ = os.WriteFile(lndConfPath, prev, 0660)
        writeError(w, http.StatusInternalServerError, "lnd restart failed, rollback applied")
        return
      }
    }
    s.markLNDRestart()
  }

  resp := map[string]any{"ok": true}
  if warning != "" {
    resp["warning"] = warning
  }
  writeJSON(w, http.StatusOK, resp)
}

type lndOptionUpdate struct {
  value string
  remove bool
  seen bool
}

func updateLNDConfOptions(raw string, alias string, color string, minChanSize int64, maxChanSize int64) string {
  updates := map[string]*lndOptionUpdate{
    "alias": {value: alias, remove: strings.TrimSpace(alias) == ""},
    "color": {value: color, remove: strings.TrimSpace(color) == ""},
    "minchansize": {value: strconv.FormatInt(minChanSize, 10), remove: minChanSize <= 0},
    "maxchansize": {value: strconv.FormatInt(maxChanSize, 10), remove: maxChanSize <= 0},
  }

  lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
  start := -1
  end := len(lines)
  for i, line := range lines {
    trimmed := strings.TrimSpace(line)
    if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
      if strings.EqualFold(trimmed, "[Application Options]") {
        start = i
        continue
      }
      if start != -1 && i > start {
        end = i
        break
      }
    }
  }

  if start == -1 {
    lines = append(lines, "[Application Options]")
    start = len(lines) - 1
    end = len(lines)
  }

  for i := start + 1; i < end; i++ {
    trimmed := strings.TrimSpace(lines[i])
    if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
      continue
    }
    parts := strings.SplitN(trimmed, "=", 2)
    if len(parts) != 2 {
      continue
    }
    key := strings.TrimSpace(parts[0])
    upd, ok := updates[key]
    if !ok {
      continue
    }
    upd.seen = true
    if upd.remove {
      lines[i] = ""
      continue
    }
    lines[i] = fmt.Sprintf("%s=%s", key, upd.value)
  }

  extra := []string{}
  for key, upd := range updates {
    if upd.seen || upd.remove {
      continue
    }
    extra = append(extra, fmt.Sprintf("%s=%s", key, upd.value))
  }
  if len(extra) > 0 {
    lines = append(lines[:end], append(extra, lines[end:]...)...)
  }

  return strings.Join(lines, "\n")
}

func isHexColor(value string) bool {
  trimmed := strings.TrimSpace(value)
  if len(trimmed) != 7 || !strings.HasPrefix(trimmed, "#") {
    return false
  }
  for _, r := range trimmed[1:] {
    if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
      continue
    }
    return false
  }
  return true
}

func (s *Server) markLNDRestart() {
  s.lndRestartMu.Lock()
  s.lastLNDRestart = time.Now()
  s.lndRestartMu.Unlock()
}

func (s *Server) lndWarmupActive() bool {
  s.lndRestartMu.RLock()
  last := s.lastLNDRestart
  s.lndRestartMu.RUnlock()
  if last.IsZero() {
    return false
  }
  return time.Since(last) <= lndWarmupPeriod
}

func (s *Server) handleOnchainUtxos(w http.ResponseWriter, r *http.Request) {
  minConfs := int32(0)
  maxConfs := int32(0)
  maxConfSet := false
  if raw := strings.TrimSpace(r.URL.Query().Get("include_unconfirmed")); raw != "" {
    if parsed, err := strconv.ParseBool(raw); err == nil && !parsed {
      minConfs = 1
    }
  }
  if raw := strings.TrimSpace(r.URL.Query().Get("min_conf")); raw != "" {
    if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
      minConfs = int32(parsed)
    }
  }
  if raw := strings.TrimSpace(r.URL.Query().Get("max_conf")); raw != "" {
    if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
      maxConfs = int32(parsed)
      maxConfSet = true
    }
  }
  if maxConfs > 0 && maxConfs < minConfs {
    maxConfs = minConfs
  }
  if !maxConfSet {
    maxConfs = int32(1 << 30)
  }

  limit := 500
  if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
    if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
      limit = parsed
    }
  }

  ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
  defer cancel()

  items, err := s.lnd.ListOnchainUtxos(ctx, minConfs, maxConfs)
  if err != nil {
    writeError(w, http.StatusInternalServerError, lndStatusMessage(err))
    return
  }

  if limit > 0 && len(items) > limit {
    items = items[:limit]
  }

  writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleOnchainTransactions(w http.ResponseWriter, r *http.Request) {
  limit := 0
  if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
    if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
      limit = parsed
    }
  }

  minConfs := int32(0)
  maxConfs := int32(0)
  if raw := strings.TrimSpace(r.URL.Query().Get("include_unconfirmed")); raw != "" {
    if parsed, err := strconv.ParseBool(raw); err == nil && !parsed {
      minConfs = 1
    }
  }
  if raw := strings.TrimSpace(r.URL.Query().Get("min_conf")); raw != "" {
    if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
      minConfs = int32(parsed)
    }
  }
  if raw := strings.TrimSpace(r.URL.Query().Get("max_conf")); raw != "" {
    if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
      maxConfs = int32(parsed)
    }
  }
  if maxConfs > 0 && maxConfs < minConfs {
    maxConfs = minConfs
  }

  ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
  defer cancel()

  items, err := s.lnd.ListOnchainTransactions(ctx, 0)
  if err != nil {
    writeError(w, http.StatusInternalServerError, lndStatusMessage(err))
    return
  }

  sort.Slice(items, func(i, j int) bool {
    return items[i].Timestamp.After(items[j].Timestamp)
  })

  if minConfs > 0 || maxConfs > 0 {
    filtered := items[:0]
    for _, item := range items {
      confs := item.Confirmations
      if minConfs > 0 && confs < minConfs {
        continue
      }
      if maxConfs > 0 && confs > maxConfs {
        continue
      }
      filtered = append(filtered, item)
    }
    items = filtered
  }

  if limit > 0 && len(items) > limit {
    items = items[:limit]
  }

  writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleWalletSummary(w http.ResponseWriter, r *http.Request) {
  ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
  defer cancel()

  balances, err := s.lnd.GetBalances(ctx)
  if err != nil {
    if isTimeoutError(err) && s.lndWarmupActive() {
      writeJSON(w, http.StatusOK, map[string]any{
        "balances": map[string]int64{
          "onchain_sat": 0,
          "lightning_sat": 0,
          "onchain_confirmed_sat": 0,
          "onchain_unconfirmed_sat": 0,
          "lightning_local_sat": 0,
          "lightning_unsettled_local_sat": 0,
        },
        "activity": []any{},
        "warning": "LND warming up after restart",
      })
      return
    }
    writeError(w, http.StatusInternalServerError, lndStatusMessage(err))
    return
  }

  lightningActivity, _ := s.lnd.ListRecent(ctx, walletActivityFetchLimit)

  onchainActivity, _ := s.lnd.ListOnchain(ctx, walletActivityFetchLimit)

  activity := append(lightningActivity, onchainActivity...)

  resp := map[string]any{
    "balances": map[string]int64{
      "onchain_sat": balances.OnchainSat,
      "lightning_sat": balances.LightningSat,
      "onchain_confirmed_sat": balances.OnchainConfirmedSat,
      "onchain_unconfirmed_sat": balances.OnchainUnconfirmedSat,
      "lightning_local_sat": balances.LightningLocalSat,
      "lightning_unsettled_local_sat": balances.LightningUnsettledLocalSat,
    },
    "activity": activity,
  }
  if len(balances.Warnings) > 0 {
    resp["warning"] = strings.Join(balances.Warnings, " ")
  }
  writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleWalletAddress(w http.ResponseWriter, r *http.Request) {
  ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
  defer cancel()

  addr, err := s.lnd.NewAddress(ctx)
  if err != nil {
    writeError(w, http.StatusInternalServerError, lndStatusMessage(err))
    return
  }

  writeJSON(w, http.StatusOK, map[string]string{
    "address": addr,
    "type": "p2wpkh",
  })
}

func (s *Server) handleWalletInvoice(w http.ResponseWriter, r *http.Request) {
  var req struct {
    AmountSat int64 `json:"amount_sat"`
    Memo string `json:"memo"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  if req.AmountSat <= 0 {
    writeError(w, http.StatusBadRequest, "amount_sat must be positive")
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
  defer cancel()

  invoice, err := s.lnd.CreateInvoice(ctx, req.AmountSat, req.Memo, 3600)
  if err != nil {
    writeError(w, http.StatusInternalServerError, "invoice failed")
    return
  }

  if invoice.PaymentHash != "" {
    s.recordWalletActivity(invoice.PaymentHash)
  }

  writeJSON(w, http.StatusOK, map[string]string{"payment_request": invoice.PaymentRequest})
}

func (s *Server) handleWalletDecode(w http.ResponseWriter, r *http.Request) {
  var req struct {
    PaymentRequest string `json:"payment_request"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  paymentRequest := normalizePaymentRequest(req.PaymentRequest)
  if paymentRequest == "" {
    writeError(w, http.StatusBadRequest, "payment_request required")
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
  defer cancel()

  decoded, err := s.lnd.DecodeInvoice(ctx, paymentRequest)
  if err != nil {
    msg := err.Error()
    lower := strings.ToLower(msg)
    if isTimeoutError(err) {
      msg = lndStatusMessage(err)
    } else if strings.Contains(lower, "invalid") || strings.Contains(lower, "payment request") {
      msg = "Invalid invoice"
    }
    writeError(w, http.StatusBadRequest, msg)
    return
  }

  writeJSON(w, http.StatusOK, map[string]any{
    "amount_sat": decoded.AmountSat,
    "amount_msat": decoded.AmountMsat,
    "memo": decoded.Memo,
    "destination": decoded.Destination,
    "expiry": decoded.Expiry,
    "timestamp": decoded.Timestamp,
  })
}

func (s *Server) handleWalletPay(w http.ResponseWriter, r *http.Request) {
  var req struct {
    PaymentRequest string `json:"payment_request"`
    ChannelPoint string `json:"channel_point"`
    AmountSat int64 `json:"amount_sat"`
    Comment string `json:"comment"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  paymentRequest := normalizePaymentRequest(req.PaymentRequest)
  if paymentRequest == "" {
    writeError(w, http.StatusBadRequest, "payment_request required")
    return
  }
  cleaned := strings.TrimSpace(paymentRequest)
  if strings.HasPrefix(strings.ToLower(cleaned), "lightning:") {
    cleaned = cleaned[len("lightning:"):]
  }
  if isLightningAddress(cleaned) {
    if req.AmountSat <= 0 {
      writeError(w, http.StatusBadRequest, "amount_sat must be positive for lightning address")
      return
    }
    resolved, err := resolveLightningAddress(r.Context(), cleaned, req.AmountSat, req.Comment)
    if err != nil {
      writeError(w, http.StatusBadRequest, fmt.Sprintf("lightning address error: %v", err))
      return
    }
    paymentRequest = resolved
  } else {
    paymentRequest = cleaned
  }

  ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
  defer cancel()

  outgoingChanID := uint64(0)
  selectedPoint := strings.ToLower(strings.TrimSpace(req.ChannelPoint))
  if selectedPoint != "" {
    channels, err := s.lnd.ListChannels(ctx)
    if err != nil {
      writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
      return
    }
    for _, ch := range channels {
      if strings.ToLower(strings.TrimSpace(ch.ChannelPoint)) == selectedPoint {
        outgoingChanID = ch.ChannelID
        break
      }
    }
    if outgoingChanID == 0 {
      writeError(w, http.StatusBadRequest, "selected channel not found")
      return
    }
  }

  paymentHash := ""
  if decoded, err := s.lnd.DecodeInvoice(ctx, paymentRequest); err == nil {
    paymentHash = decoded.PaymentHash
  }

  if err := s.lnd.PayInvoice(ctx, paymentRequest, outgoingChanID); err != nil {
    if paymentHash != "" {
      s.recordWalletActivity(paymentHash)
    }
    msg := lndRPCErrorMessage(err)
    if isTimeoutError(err) {
      msg = lndStatusMessage(err)
    }
    if msg == "" || msg == "LND error" {
      msg = "Payment failed"
    }
    writeError(w, http.StatusInternalServerError, msg)
    return
  }

  if paymentHash != "" {
    s.recordWalletActivity(paymentHash)
  }

  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleWalletSend(w http.ResponseWriter, r *http.Request) {
  var req struct {
    Address string `json:"address"`
    AmountSat int64 `json:"amount_sat"`
    SatPerVbyte int64 `json:"sat_per_vbyte"`
    SweepAll bool `json:"sweep_all"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  address := strings.TrimSpace(req.Address)
  if address == "" {
    writeError(w, http.StatusBadRequest, "address required")
    return
  }
  if !req.SweepAll && req.AmountSat <= 0 {
    writeError(w, http.StatusBadRequest, "amount_sat must be positive")
    return
  }
  if req.SweepAll {
    req.AmountSat = 0
  }

  ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
  defer cancel()

  txid, err := s.lnd.SendCoins(ctx, address, req.AmountSat, req.SatPerVbyte, req.SweepAll)
  if err != nil {
    msg := lndRPCErrorMessage(err)
    if isTimeoutError(err) {
      msg = lndStatusMessage(err)
    }
    if msg == "" || msg == "LND error" {
      msg = "On-chain send failed"
    }
    writeError(w, http.StatusInternalServerError, msg)
    return
  }

  writeJSON(w, http.StatusOK, map[string]string{"txid": txid})
}

type rpcStatusError struct {
  statusCode int
  message string
}

func (e rpcStatusError) Error() string {
  if e.message == "" {
    return fmt.Sprintf("rpc status %d", e.statusCode)
  }
  return fmt.Sprintf("rpc status %d: %s", e.statusCode, e.message)
}

type rpcErrorDetail struct {
  Code int `json:"code"`
  Message string `json:"message"`
}

type rpcErrorPayload struct {
  Error *rpcErrorDetail `json:"error"`
}

type bitcoinInfo struct {
  Chain string `json:"chain"`
  Blocks int64 `json:"blocks"`
  Headers int64 `json:"headers"`
  VerificationProgress float64 `json:"verificationprogress"`
  InitialBlockDownload bool `json:"initialblockdownload"`
  BestBlockHash string `json:"bestblockhash"`
  Pruned bool `json:"pruned"`
  PruneHeight int64 `json:"pruneheight"`
  PruneTargetSize int64 `json:"prune_target_size"`
  SizeOnDisk int64 `json:"size_on_disk"`
}

type bitcoinNetworkInfo struct {
  Version int `json:"version"`
  Subversion string `json:"subversion"`
  Connections int `json:"connections"`
}

type bitcoinRPCResponse struct {
  Result bitcoinInfo `json:"result"`
  Error *rpcErrorDetail `json:"error"`
}

type bitcoinNetworkRPCResponse struct {
  Result bitcoinNetworkInfo `json:"result"`
  Error *rpcErrorDetail `json:"error"`
}

func parseRPCError(body []byte) string {
  var payload rpcErrorPayload
  if err := json.Unmarshal(body, &payload); err != nil {
    return ""
  }
  if payload.Error == nil {
    return ""
  }
  return payload.Error.Message
}

func parseBitcoinInfo(body []byte) (bitcoinInfo, error) {
  var payload bitcoinRPCResponse
  if err := json.Unmarshal(body, &payload); err != nil {
    return bitcoinInfo{}, err
  }
  if payload.Error != nil {
    return bitcoinInfo{}, fmt.Errorf(payload.Error.Message)
  }
  return payload.Result, nil
}

func parseBitcoinNetworkInfo(body []byte) (bitcoinNetworkInfo, error) {
  var payload bitcoinNetworkRPCResponse
  if err := json.Unmarshal(body, &payload); err != nil {
    return bitcoinNetworkInfo{}, err
  }
  if payload.Error != nil {
    return bitcoinNetworkInfo{}, fmt.Errorf(payload.Error.Message)
  }
  return payload.Result, nil
}

func doBitcoinRPC(ctx context.Context, url, user, pass, method string) ([]byte, error) {
  payload := map[string]any{
    "jsonrpc": "1.0",
    "id": "lightningos",
    "method": method,
    "params": []any{},
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

func fetchBitcoinRPC(ctx context.Context, host, user, pass, method string) ([]byte, error) {
  if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
    return doBitcoinRPC(ctx, host, user, pass, method)
  }

  body, err := doBitcoinRPC(ctx, "http://"+host, user, pass, method)
  if err == nil {
    return body, nil
  }
  var statusErr rpcStatusError
  if err != nil && errors.As(err, &statusErr) {
    return nil, err
  }

  body, httpsErr := doBitcoinRPC(ctx, "https://"+host, user, pass, method)
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

func fetchBitcoinInfo(ctx context.Context, host, user, pass string) (bitcoinInfo, error) {
  body, err := fetchBitcoinRPC(ctx, host, user, pass, "getblockchaininfo")
  if err != nil {
    return bitcoinInfo{}, err
  }
  return parseBitcoinInfo(body)
}

func fetchBitcoinNetworkInfo(ctx context.Context, host, user, pass string) (bitcoinNetworkInfo, error) {
  body, err := fetchBitcoinRPC(ctx, host, user, pass, "getnetworkinfo")
  if err != nil {
    return bitcoinNetworkInfo{}, err
  }
  return parseBitcoinNetworkInfo(body)
}

func testBitcoinRPC(ctx context.Context, host, user, pass string) (bool, error) {
  _, err := fetchBitcoinInfo(ctx, host, user, pass)
  if err != nil {
    return false, err
  }
  return true, nil
}

func storeBitcoinSecrets(user, pass string) error {
  user = strings.TrimSpace(user)
  pass = strings.TrimSpace(pass)
  _ = os.Setenv("BITCOIN_RPC_USER", user)
  _ = os.Setenv("BITCOIN_RPC_PASS", pass)
  content, _ := os.ReadFile(secretsPath)
  lines := []string{}
  if len(content) > 0 {
    lines = strings.Split(string(content), "\n")
  }
  hasUser := false
  hasPass := false

  for i, line := range lines {
    if strings.HasPrefix(line, "BITCOIN_RPC_USER=") {
      lines[i] = "BITCOIN_RPC_USER=" + user
      hasUser = true
    }
    if strings.HasPrefix(line, "BITCOIN_RPC_PASS=") {
      lines[i] = "BITCOIN_RPC_PASS=" + pass
      hasPass = true
    }
  }

  if !hasUser {
    lines = append(lines, "BITCOIN_RPC_USER="+user)
  }
  if !hasPass {
    lines = append(lines, "BITCOIN_RPC_PASS="+pass)
  }

  if err := os.MkdirAll(filepath.Dir(secretsPath), 0750); err != nil {
    return err
  }
  return os.WriteFile(secretsPath, []byte(strings.Join(lines, "\n")), 0660)
}

func readBitcoinSource() string {
	if value := strings.TrimSpace(os.Getenv("BITCOIN_SOURCE")); value != "" {
		value = strings.ToLower(value)
		if value == "local" || value == "remote" {
			return value
		}
	}
	content, err := os.ReadFile(secretsPath)
	if err == nil {
		for _, line := range strings.Split(string(content), "\n") {
			if strings.HasPrefix(line, "BITCOIN_SOURCE=") {
				value := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "BITCOIN_SOURCE=")))
				if value == "local" || value == "remote" {
					return value
				}
			}
		}
	}
	if detected := detectBitcoinSourceFromLNDConf(); detected != "" {
		return detected
	}
	return "remote"
}

func detectBitcoinSourceFromLNDConf() string {
  raw, err := os.ReadFile(lndConfPath)
  if err != nil {
    return ""
  }
  lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
  inBitcoind := false
  for _, line := range lines {
    trimmed := strings.TrimSpace(line)
    if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
      inBitcoind = strings.EqualFold(trimmed, "[Bitcoind]")
      continue
    }
    if !inBitcoind {
      continue
    }
    if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
      continue
    }
    if strings.HasPrefix(trimmed, "bitcoind.rpchost=") {
      host := strings.TrimSpace(strings.TrimPrefix(trimmed, "bitcoind.rpchost="))
      if host == "" {
        continue
      }
      if isLocalRPCHost(host) {
        return "local"
      }
      return "remote"
    }
  }
  return ""
}

func isLocalRPCHost(value string) bool {
  host := extractHost(value)
  if host == "" {
    return false
  }
  lower := strings.ToLower(host)
  if lower == "localhost" {
    return true
  }
  ip := net.ParseIP(lower)
  if ip == nil {
    return false
  }
  if ip.IsLoopback() || ip.IsUnspecified() {
    return true
  }
  return isHostIP(ip)
}

func extractHost(value string) string {
  trimmed := strings.TrimSpace(value)
  if trimmed == "" {
    return ""
  }
  if strings.Contains(trimmed, "://") {
    if parsed, err := url.Parse(trimmed); err == nil && parsed.Host != "" {
      trimmed = parsed.Host
    }
  }
  if at := strings.LastIndex(trimmed, "@"); at != -1 {
    trimmed = trimmed[at+1:]
  }
  if host, _, err := net.SplitHostPort(trimmed); err == nil {
    return host
  }
  if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
    return strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]")
  }
  return trimmed
}

func isHostIP(ip net.IP) bool {
  for _, addr := range localInterfaceIPs() {
    if ip.Equal(addr) {
      return true
    }
  }
  return false
}

func localInterfaceIPs() []net.IP {
  ifaces, err := net.Interfaces()
  if err != nil {
    return nil
  }
  ips := []net.IP{}
  for _, iface := range ifaces {
    addrs, err := iface.Addrs()
    if err != nil {
      continue
    }
    for _, addr := range addrs {
      switch v := addr.(type) {
      case *net.IPNet:
        if v.IP != nil {
          ips = append(ips, v.IP)
        }
      case *net.IPAddr:
        if v.IP != nil {
          ips = append(ips, v.IP)
        }
      }
    }
  }
  return ips
}

func storeBitcoinSource(source string) error {
  trimmed := strings.ToLower(strings.TrimSpace(source))
  if trimmed == "" {
    trimmed = "remote"
  }
  if trimmed != "local" && trimmed != "remote" {
    return errors.New("invalid bitcoin source")
  }
  _ = os.Setenv("BITCOIN_SOURCE", trimmed)
  content, _ := os.ReadFile(secretsPath)
  lines := []string{}
  if len(content) > 0 {
    lines = strings.Split(string(content), "\n")
  }
  hasKey := false
  for i, line := range lines {
    if strings.HasPrefix(line, "BITCOIN_SOURCE=") {
      lines[i] = "BITCOIN_SOURCE=" + trimmed
      hasKey = true
    }
  }
  if !hasKey {
    lines = append(lines, "BITCOIN_SOURCE="+trimmed)
  }
  if err := os.MkdirAll(filepath.Dir(secretsPath), 0750); err != nil {
    return err
  }
  return os.WriteFile(secretsPath, []byte(strings.Join(lines, "\n")), 0660)
}

func normalizeLocalZMQ(value string, fallback string) string {
  trimmed := strings.TrimSpace(value)
  if trimmed == "" {
    return fallback
  }
  if strings.HasPrefix(trimmed, "tcp://0.0.0.0:") {
    return "tcp://127.0.0.1:" + strings.TrimPrefix(trimmed, "tcp://0.0.0.0:")
  }
  if strings.HasPrefix(trimmed, "0.0.0.0:") {
    return "tcp://127.0.0.1:" + strings.TrimPrefix(trimmed, "0.0.0.0:")
  }
  if strings.HasPrefix(trimmed, "tcp://") {
    return trimmed
  }
  return "tcp://" + trimmed
}

func dockerContainerGateways(ctx context.Context, containerID string) []string {
  if containerID == "" {
    return []string{}
  }
  out, err := system.RunCommandWithSudo(
    ctx,
    "docker",
    "inspect",
    "-f",
    "{{range $k,$v := .NetworkSettings.Networks}}{{println $v.Gateway}}{{end}}",
    containerID,
  )
  if err != nil {
    return []string{}
  }
  gateways := []string{}
  seen := map[string]bool{}
  for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
    trimmed := strings.TrimSpace(line)
    if trimmed == "" || seen[trimmed] {
      continue
    }
    seen[trimmed] = true
    gateways = append(gateways, trimmed)
  }
  return gateways
}

func updateLNDConfRPC(ctx context.Context, user, pass, host, zmqBlock, zmqTx string) error {
  remoteCfg := bitcoinRPCConfig{
    Host: host,
    User: user,
    Pass: pass,
    ZMQBlock: zmqBlock,
    ZMQTx: zmqTx,
  }
  localCfg, _, err := readBitcoinLocalRPCConfig(ctx)
  if err != nil {
    localCfg = bitcoinRPCConfig{
      Host: "127.0.0.1:8332",
      ZMQBlock: "tcp://127.0.0.1:28332",
      ZMQTx: "tcp://127.0.0.1:28333",
    }
  }
  return updateLNDConfBitcoinSource("remote", remoteCfg, localCfg)
}

func updateLNDConfBitcoinSource(active string, remoteCfg bitcoinRPCConfig, localCfg bitcoinRPCConfig) error {
  content, err := os.ReadFile(lndConfPath)
  if err != nil {
    return err
  }
  raw := strings.ReplaceAll(string(content), "\r\n", "\n")
  lines := strings.Split(raw, "\n")

  start := -1
  end := len(lines)
  for i, line := range lines {
    trimmed := strings.TrimSpace(line)
    if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
      if strings.EqualFold(trimmed, "[Bitcoind]") {
        start = i
        continue
      }
      if start != -1 && i > start {
        end = i
        break
      }
    }
  }

  if start == -1 {
    lines = append(lines, "[Bitcoind]")
    start = len(lines) - 1
    end = len(lines)
  }

  remoteUpdates := map[string]string{
    "bitcoind.rpchost": remoteCfg.Host,
    "bitcoind.rpcuser": remoteCfg.User,
    "bitcoind.rpcpass": remoteCfg.Pass,
    "bitcoind.zmqpubrawblock": remoteCfg.ZMQBlock,
    "bitcoind.zmqpubrawtx": remoteCfg.ZMQTx,
  }
  localUpdates := map[string]string{
    "bitcoind.rpchost": localCfg.Host,
    "bitcoind.rpcuser": localCfg.User,
    "bitcoind.rpcpass": localCfg.Pass,
    "bitcoind.zmqpubrawblock": localCfg.ZMQBlock,
    "bitcoind.zmqpubrawtx": localCfg.ZMQTx,
  }

  currentGroup := ""
  foundRemote := false
  foundLocal := false
  for i := start + 1; i < end; i++ {
    rawLine := lines[i]
    trimmed := strings.TrimSpace(rawLine)
    if trimmed == "" {
      continue
    }

    marker := strings.TrimSpace(strings.TrimLeft(trimmed, "#;"))
    if strings.EqualFold(marker, "LightningOS Bitcoin Remote") {
      lines[i] = "# LightningOS Bitcoin Remote"
      currentGroup = "remote"
      foundRemote = true
      continue
    }
    if strings.EqualFold(marker, "LightningOS Bitcoin Local") {
      lines[i] = "# LightningOS Bitcoin Local"
      currentGroup = "local"
      foundLocal = true
      continue
    }

    if currentGroup == "" {
      continue
    }

    clean := strings.TrimSpace(strings.TrimLeft(trimmed, "#;"))
    parts := strings.SplitN(clean, "=", 2)
    if len(parts) != 2 {
      continue
    }
    key := strings.TrimSpace(parts[0])

    var value string
    if currentGroup == "remote" {
      value = remoteUpdates[key]
    } else if currentGroup == "local" {
      value = localUpdates[key]
    }
    if value == "" {
      continue
    }

    prefix := ""
    if currentGroup != active {
      prefix = "# "
    }
    lines[i] = prefix + key + "=" + value
  }

  if !foundRemote || !foundLocal {
    remotePrefix := "# "
    localPrefix := "# "
    if active == "remote" {
      remotePrefix = ""
    } else if active == "local" {
      localPrefix = ""
    }
    block := []string{
      "",
      "# LightningOS Bitcoin Remote",
      remotePrefix + "bitcoind.rpchost=" + remoteCfg.Host,
      remotePrefix + "bitcoind.rpcuser=" + remoteCfg.User,
      remotePrefix + "bitcoind.rpcpass=" + remoteCfg.Pass,
      remotePrefix + "bitcoind.zmqpubrawblock=" + remoteCfg.ZMQBlock,
      remotePrefix + "bitcoind.zmqpubrawtx=" + remoteCfg.ZMQTx,
      "",
      "# LightningOS Bitcoin Local",
      localPrefix + "bitcoind.rpchost=" + localCfg.Host,
      localPrefix + "bitcoind.rpcuser=" + localCfg.User,
      localPrefix + "bitcoind.rpcpass=" + localCfg.Pass,
      localPrefix + "bitcoind.zmqpubrawblock=" + localCfg.ZMQBlock,
      localPrefix + "bitcoind.zmqpubrawtx=" + localCfg.ZMQTx,
    }
    lines = append(lines[:end], append(block, lines[end:]...)...)
  }

  return os.WriteFile(lndConfPath, []byte(strings.Join(lines, "\n")), 0660)
}

func storeWalletUnlock(password string) error {
  trimmed := strings.TrimSpace(password)
  if trimmed == "" {
    return errors.New("wallet password required")
  }
  if err := storeWalletPassword(trimmed); err != nil {
    return err
  }
  return ensureWalletUnlockConfig()
}

func (s *Server) scheduleLNDPermissionsFix(reason string) {
  if s == nil {
    return
  }
  go func() {
    waitCtx, waitCancel := context.WithTimeout(context.Background(), 12*time.Second)
    waitForFile(waitCtx, lndAdminMacaroonPath)
    waitCancel()
    runCtx, runCancel := context.WithTimeout(context.Background(), 6*time.Second)
    defer runCancel()
    if _, err := system.RunCommandWithSudo(runCtx, lndFixPermsScript); err != nil {
      s.logger.Printf("lnd permissions fix failed (%s): %v", reason, err)
    }
  }()
}

func waitForFile(ctx context.Context, path string) {
  ticker := time.NewTicker(300 * time.Millisecond)
  defer ticker.Stop()
  for {
    if _, err := os.Stat(path); err == nil {
      return
    }
    select {
    case <-ctx.Done():
      return
    case <-ticker.C:
    }
  }
}

func storeWalletPassword(password string) error {
  if _, err := os.Stat(lndPasswordPath); err != nil {
    if os.IsNotExist(err) {
      return fmt.Errorf("password file missing: %s", lndPasswordPath)
    }
    return err
  }
  return os.WriteFile(lndPasswordPath, []byte(password), 0660)
}

func walletPasswordAvailable() bool {
  info, err := os.Stat(lndPasswordPath)
  if err != nil || info.Size() == 0 {
    return false
  }
  content, err := os.ReadFile(lndPasswordPath)
  if err != nil {
    return false
  }
  return strings.TrimSpace(string(content)) != ""
}

func ensureWalletUnlockConfig() error {
  if err := os.MkdirAll(filepath.Dir(lndConfPath), 0750); err != nil {
    return err
  }
  raw, _ := os.ReadFile(lndConfPath)
  updated := ensureUnlockLines(string(raw))
  return os.WriteFile(lndConfPath, []byte(updated), 0660)
}

func ensureUnlockLines(raw string) string {
  lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
  start := -1
  end := len(lines)

  for i, line := range lines {
    trimmed := strings.TrimSpace(line)
    if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
      if strings.EqualFold(trimmed, "[Application Options]") {
        start = i
        continue
      }
      if start != -1 && i > start {
        end = i
        break
      }
    }
  }

  if start == -1 {
    lines = append(lines, "[Application Options]")
    start = len(lines) - 1
    end = len(lines)
  }

  hasPass := false
  hasAllow := false
  for i := start + 1; i < end; i++ {
    trimmed := strings.TrimSpace(lines[i])
    if strings.HasPrefix(trimmed, "wallet-unlock-password-file=") {
      lines[i] = "wallet-unlock-password-file=" + lndPasswordPath
      hasPass = true
    }
    if strings.HasPrefix(trimmed, "wallet-unlock-allow-create=") {
      lines[i] = "wallet-unlock-allow-create=true"
      hasAllow = true
    }
  }

  extra := []string{}
  if !hasPass {
    extra = append(extra, "wallet-unlock-password-file="+lndPasswordPath)
  }
  if !hasAllow {
    extra = append(extra, "wallet-unlock-allow-create=true")
  }
  if len(extra) > 0 {
    lines = append(lines[:end], append(extra, lines[end:]...)...)
  }

  return strings.Join(lines, "\n")
}
