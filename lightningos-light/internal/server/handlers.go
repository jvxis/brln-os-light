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
  "os"
  "path/filepath"
  "strconv"
  "strings"
  "time"

  "github.com/jackc/pgx/v5/pgxpool"

  "lightningos-light/internal/system"
)

const (
  secretsPath = "/etc/lightningos/secrets.env"
  lndConfPath = "/etc/lnd/lnd.conf"
  lndUserConfPath = "/etc/lnd/lnd.user.conf"
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
  ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
  defer cancel()

  issues := []healthIssue{}
  status := "OK"

  lndStatus, err := s.lnd.GetStatus(ctx)
  if err != nil {
    issues = append(issues, healthIssue{Component: "lnd", Level: "ERR", Message: "LND not reachable"})
    status = elevate(status, "ERR")
  } else if lndStatus.WalletState == "locked" {
    issues = append(issues, healthIssue{Component: "lnd", Level: "ERR", Message: "LND wallet locked"})
    status = elevate(status, "ERR")
  }

  bitcoin, err := s.bitcoinStatus(ctx)
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

  if !system.SystemctlIsActive(ctx, "postgresql") {
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
}

func (s *Server) handlePostgres(w http.ResponseWriter, r *http.Request) {
  ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
  defer cancel()

  resp := postgresResponse{
    ServiceActive: system.SystemctlIsActive(ctx, "postgresql"),
    DBName: s.cfg.Postgres.DBName,
  }

  dsn := os.Getenv("LND_PG_DSN")
  if dsn != "" {
    pool, err := pgxpool.New(ctx, dsn)
    if err == nil {
      defer pool.Close()
      var sizeBytes int64
      _ = pool.QueryRow(ctx, "select pg_database_size($1)", s.cfg.Postgres.DBName).Scan(&sizeBytes)
      resp.DBSizeMB = sizeBytes / (1024 * 1024)

      var connections int64
      _ = pool.QueryRow(ctx, "select count(*) from pg_stat_activity where datname=$1", s.cfg.Postgres.DBName).Scan(&connections)
      resp.Connections = connections
    }
  }

  writeJSON(w, http.StatusOK, resp)
}

type bitcoinStatus struct {
  Mode string `json:"mode"`
  RPCHost string `json:"rpchost"`
  ZMQRawBlock string `json:"zmq_rawblock"`
  ZMQRawTx string `json:"zmq_rawtx"`
  RPCOk bool `json:"rpc_ok"`
  ZMQRawBlockOk bool `json:"zmq_rawblock_ok"`
  ZMQRawTxOk bool `json:"zmq_rawtx_ok"`
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

func (s *Server) bitcoinStatus(ctx context.Context) (bitcoinStatus, error) {
  rpcUser := os.Getenv("BITCOIN_RPC_USER")
  rpcPass := os.Getenv("BITCOIN_RPC_PASS")
  status := bitcoinStatus{
    Mode: "remote",
    RPCHost: s.cfg.BitcoinRemote.RPCHost,
    ZMQRawBlock: s.cfg.BitcoinRemote.ZMQRawBlock,
    ZMQRawTx: s.cfg.BitcoinRemote.ZMQRawTx,
  }

  if rpcUser != "" && rpcPass != "" {
    ok, _ := testBitcoinRPC(ctx, s.cfg.BitcoinRemote.RPCHost, rpcUser, rpcPass)
    status.RPCOk = ok
  }

  status.ZMQRawBlockOk = testTCP(s.cfg.BitcoinRemote.ZMQRawBlock)
  status.ZMQRawTxOk = testTCP(s.cfg.BitcoinRemote.ZMQRawTx)

  return status, nil
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
  ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
  defer cancel()

  resp := lndStatusResponse{}
  resp.ServiceActive = system.SystemctlIsActive(ctx, "lnd")

  status, err := s.lnd.GetStatus(ctx)
  if err == nil {
    resp.WalletState = status.WalletState
    resp.SyncedToChain = status.SyncedToChain
    resp.SyncedToGraph = status.SyncedToGraph
    resp.BlockHeight = status.BlockHeight
    resp.Channels.Active = status.ChannelsActive
    resp.Channels.Inactive = status.ChannelsInactive
    resp.Balances.OnchainSat = status.OnchainSat
    resp.Balances.LightningSat = status.LightningSat
  } else {
    resp.WalletState = status.WalletState
  }

  writeJSON(w, http.StatusOK, resp)
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
  if strings.TrimSpace(req.RPCUser) == "" || strings.TrimSpace(req.RPCPass) == "" {
    writeError(w, http.StatusBadRequest, "rpcuser and rpcpass required")
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
  defer cancel()

  rpcOk, err := testBitcoinRPC(ctx, s.cfg.BitcoinRemote.RPCHost, req.RPCUser, req.RPCPass)
  if err != nil || !rpcOk {
    msg := "bitcoin rpc check failed"
    if err != nil {
      msg = fmt.Sprintf("bitcoin rpc check failed: %v", err)
      s.logger.Printf("bitcoin rpc check failed: %v", err)
    }
    writeError(w, http.StatusBadRequest, msg)
    return
  }

  if err := storeBitcoinSecrets(req.RPCUser, req.RPCPass); err != nil {
    s.logger.Printf("failed to store secrets: %v", err)
    writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to store secrets: %v", err))
    return
  }

  if err := updateLNDConfRPC(req.RPCUser, req.RPCPass); err != nil {
    writeError(w, http.StatusInternalServerError, "failed to update lnd.conf")
    return
  }

  _ = system.SystemctlRestart(ctx, "lnd")

  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleCreateWallet(w http.ResponseWriter, r *http.Request) {
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

  seed, err := s.lnd.GenSeed(ctx, req.WalletPassword)
  if err != nil {
    writeError(w, http.StatusInternalServerError, "gen seed failed")
    return
  }

  writeJSON(w, http.StatusOK, map[string]any{"seed_words": seed})
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

  ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
  defer cancel()

  if err := s.lnd.InitWallet(ctx, req.WalletPassword, req.SeedWords); err != nil {
    writeError(w, http.StatusInternalServerError, "init wallet failed")
    return
  }

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

  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func mapService(name string) string {
  switch name {
  case "lnd":
    return "lnd"
  case "lightningos-manager":
    return "lightningos-manager"
  case "postgresql":
    return "postgresql"
  default:
    return ""
  }
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
    writeError(w, http.StatusInternalServerError, "log read failed")
    return
  }

  writeJSON(w, http.StatusOK, map[string]any{"service": service, "lines": out})
}

func (s *Server) handleLNDConfigGet(w http.ResponseWriter, r *http.Request) {
  raw, _ := os.ReadFile(lndUserConfPath)

  current := parseLNDUserConf(string(raw))

  resp := map[string]any{
    "supported": map[string]bool{
      "alias": true,
      "min_channel_size_sat": true,
      "max_channel_size_sat": true,
    },
    "current": map[string]any{
      "alias": current.Alias,
      "min_channel_size_sat": current.MinChanSize,
      "max_channel_size_sat": current.MaxChanSize,
    },
    "raw_user_conf": string(raw),
  }

  writeJSON(w, http.StatusOK, resp)
}

type lndUserConf struct {
  Alias string
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

  content := buildLNDUserConf(req.Alias, req.MinChannelSizeSat, req.MaxChannelSizeSat)
  if err := writeUserConf(content); err != nil {
    writeError(w, http.StatusInternalServerError, "failed to write user conf")
    return
  }

  if req.ApplyNow {
    ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
    defer cancel()
    _ = system.SystemctlRestart(ctx, "lnd")
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

  prev, _ := os.ReadFile(lndUserConfPath)
  if err := writeUserConf(req.RawUserConf); err != nil {
    writeError(w, http.StatusInternalServerError, "failed to write user conf")
    return
  }

  if req.ApplyNow {
    ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
    defer cancel()
    if err := system.SystemctlRestart(ctx, "lnd"); err != nil {
      _ = writeUserConf(string(prev))
      writeError(w, http.StatusInternalServerError, "lnd restart failed, rollback applied")
      return
    }
  }

  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func buildLNDUserConf(alias string, minChanSize int64, maxChanSize int64) string {
  var buf strings.Builder
  buf.WriteString("[Application Options]\n")
  if alias != "" {
    buf.WriteString("alias=")
    buf.WriteString(alias)
    buf.WriteString("\n")
  }
  if minChanSize > 0 {
    buf.WriteString("minchansize=")
    buf.WriteString(strconv.FormatInt(minChanSize, 10))
    buf.WriteString("\n")
  }
  if maxChanSize > 0 {
    buf.WriteString("maxchansize=")
    buf.WriteString(strconv.FormatInt(maxChanSize, 10))
    buf.WriteString("\n")
  }
  return buf.String()
}

func writeUserConf(content string) error {
  if err := os.MkdirAll(filepath.Dir(lndUserConfPath), 0750); err != nil {
    return err
  }
  return os.WriteFile(lndUserConfPath, []byte(content), 0664)
}

func (s *Server) handleWalletSummary(w http.ResponseWriter, r *http.Request) {
  ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
  defer cancel()

  status, err := s.lnd.GetStatus(ctx)
  if err != nil {
    writeError(w, http.StatusInternalServerError, "lnd status error")
    return
  }

  activity, _ := s.lnd.ListRecent(ctx, 20)

  writeJSON(w, http.StatusOK, map[string]any{
    "balances": map[string]int64{
      "onchain_sat": status.OnchainSat,
      "lightning_sat": status.LightningSat,
    },
    "activity": activity,
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

  invoice, err := s.lnd.CreateInvoice(ctx, req.AmountSat, req.Memo)
  if err != nil {
    writeError(w, http.StatusInternalServerError, "invoice failed")
    return
  }

  writeJSON(w, http.StatusOK, map[string]string{"payment_request": invoice})
}

func (s *Server) handleWalletPay(w http.ResponseWriter, r *http.Request) {
  var req struct {
    PaymentRequest string `json:"payment_request"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  if strings.TrimSpace(req.PaymentRequest) == "" {
    writeError(w, http.StatusBadRequest, "payment_request required")
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
  defer cancel()

  if err := s.lnd.PayInvoice(ctx, req.PaymentRequest); err != nil {
    writeError(w, http.StatusInternalServerError, "payment failed")
    return
  }

  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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

type rpcErrorPayload struct {
  Error *struct {
    Code int `json:"code"`
    Message string `json:"message"`
  } `json:"error"`
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

func doBitcoinRPC(ctx context.Context, url, user, pass string) (bool, error) {
  payload := map[string]any{
    "jsonrpc": "1.0",
    "id": "lightningos",
    "method": "getblockchaininfo",
    "params": []any{},
  }
  buf, _ := json.Marshal(payload)

  req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
  if err != nil {
    return false, err
  }
  req.SetBasicAuth(user, pass)
  req.Header.Set("Content-Type", "application/json")

  resp, err := http.DefaultClient.Do(req)
  if err != nil {
    return false, err
  }
  defer resp.Body.Close()

  body, _ := io.ReadAll(resp.Body)
  if resp.StatusCode != http.StatusOK {
    msg := parseRPCError(body)
    return false, rpcStatusError{statusCode: resp.StatusCode, message: msg}
  }

  if msg := parseRPCError(body); msg != "" {
    return false, rpcStatusError{statusCode: resp.StatusCode, message: msg}
  }
  return true, nil
}

func testBitcoinRPC(ctx context.Context, host, user, pass string) (bool, error) {
  if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
    return doBitcoinRPC(ctx, host, user, pass)
  }

  ok, err := doBitcoinRPC(ctx, "http://"+host, user, pass)
  if err == nil && ok {
    return ok, nil
  }
  var statusErr rpcStatusError
  if err != nil && errors.As(err, &statusErr) {
    return false, err
  }

  ok, httpsErr := doBitcoinRPC(ctx, "https://"+host, user, pass)
  if httpsErr == nil && ok {
    return ok, nil
  }
  if err != nil && httpsErr != nil {
    return false, fmt.Errorf("rpc http failed: %v; https failed: %v", err, httpsErr)
  }
  if err != nil {
    return false, err
  }
  return false, httpsErr
}

func storeBitcoinSecrets(user, pass string) error {
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

func updateLNDConfRPC(user, pass string) error {
  content, err := os.ReadFile(lndConfPath)
  if err != nil {
    return err
  }

  if !bytes.Contains(content, []byte("bitcoind.rpcuser")) {
    return errors.New("lnd.conf missing bitcoind rpcuser")
  }

  lines := strings.Split(string(content), "\n")
  for i, line := range lines {
    if strings.HasPrefix(strings.TrimSpace(line), "bitcoind.rpcuser") {
      lines[i] = "bitcoind.rpcuser=" + user
    }
    if strings.HasPrefix(strings.TrimSpace(line), "bitcoind.rpcpass") {
      lines[i] = "bitcoind.rpcpass=" + pass
    }
  }

  return os.WriteFile(lndConfPath, []byte(strings.Join(lines, "\n")), 0664)
}
