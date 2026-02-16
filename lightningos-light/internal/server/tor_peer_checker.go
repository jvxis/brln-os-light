package server

import (
  "context"
  "errors"
  "fmt"
  "log"
  "net"
  "net/http"
  "strconv"
  "strings"
  "sync"
  "time"

  "lightningos-light/internal/lndclient"

  "github.com/jackc/pgx/v5"
  "github.com/jackc/pgx/v5/pgxpool"
)

const (
  torPeerCheckerConfigID             = 1
  torPeerCheckerDefaultIntervalHours = 2
  torPeerCheckerMinIntervalHours     = 2
  torPeerCheckerMaxIntervalHours     = 168
  torPeerCheckerLogCapacity          = 500
  torPeerCheckerDefaultLogLimit      = 100
  torPeerCheckerMaxLogLimit          = 500
  torPeerSwitchRetries               = 3
  torPeerConnectTimeoutSec           = uint64(12)
)

var errInvalidTorPeerCheckerConfig = errors.New("invalid tor peer checker config")

type TorPeerCheckerConfig struct {
  Enabled       bool `json:"enabled"`
  IntervalHours int  `json:"interval_hours"`
}

type TorPeerCheckerConfigUpdate struct {
  Enabled       *bool
  IntervalHours *int
  RunNow        bool
}

type torPeerCheckerStatusPayload struct {
  Enabled              bool   `json:"enabled"`
  Status               string `json:"status"`
  IntervalHours        int    `json:"interval_hours"`
  LastAttemptAt        string `json:"last_attempt_at,omitempty"`
  LastOkAt             string `json:"last_ok_at,omitempty"`
  LastError            string `json:"last_error,omitempty"`
  LastErrorAt          string `json:"last_error_at,omitempty"`
  LastCheckedCount     int    `json:"last_checked_count,omitempty"`
  LastHybridOnTorCount int    `json:"last_hybrid_on_tor_count,omitempty"`
  LastAttemptedCount   int    `json:"last_attempted_count,omitempty"`
  LastSwitchedCount    int    `json:"last_switched_count,omitempty"`
}

type torPeerCheckerLogEntry struct {
  Timestamp   string `json:"ts"`
  Alias       string `json:"alias"`
  PubKey      string `json:"pub_key,omitempty"`
  FromAddress string `json:"from_address,omitempty"`
  ToAddress   string `json:"to_address,omitempty"`
  Result      string `json:"result"`
  Detail      string `json:"detail,omitempty"`
}

type torPeerCheckerTrigger struct {
  force bool
}

type TorPeerChecker struct {
  db     *pgxpool.Pool
  lnd    *lndclient.Client
  logger *log.Logger

  mu                  sync.Mutex
  config              TorPeerCheckerConfig
  lastAttempt         time.Time
  lastOK              time.Time
  lastError           string
  lastErrorAt         time.Time
  lastCheckedCount    int
  lastHybridOnTor     int
  lastAttemptedCount  int
  lastSwitchedCount   int
  inFlight            bool
  started             bool
  stop                chan struct{}
  wake                chan torPeerCheckerTrigger
  intervalUpdated     chan struct{}
  logs                []torPeerCheckerLogEntry
}

func NewTorPeerChecker(db *pgxpool.Pool, lnd *lndclient.Client, logger *log.Logger) *TorPeerChecker {
  return &TorPeerChecker{
    db:     db,
    lnd:    lnd,
    logger: logger,
    config: defaultTorPeerCheckerConfig(),
  }
}

func defaultTorPeerCheckerConfig() TorPeerCheckerConfig {
  return TorPeerCheckerConfig{
    Enabled:       false,
    IntervalHours: torPeerCheckerDefaultIntervalHours,
  }
}

func normalizeTorPeerCheckerConfig(cfg TorPeerCheckerConfig) TorPeerCheckerConfig {
  if cfg.IntervalHours < torPeerCheckerMinIntervalHours {
    cfg.IntervalHours = torPeerCheckerMinIntervalHours
  }
  if cfg.IntervalHours > torPeerCheckerMaxIntervalHours {
    cfg.IntervalHours = torPeerCheckerMaxIntervalHours
  }
  return cfg
}

func validateTorPeerCheckerConfig(cfg TorPeerCheckerConfig) error {
  if cfg.IntervalHours < torPeerCheckerMinIntervalHours || cfg.IntervalHours > torPeerCheckerMaxIntervalHours {
    return fmt.Errorf(
      "%w: interval_hours must be between %d and %d",
      errInvalidTorPeerCheckerConfig,
      torPeerCheckerMinIntervalHours,
      torPeerCheckerMaxIntervalHours,
    )
  }
  return nil
}

func (m *TorPeerChecker) EnsureSchema(ctx context.Context) error {
  if m.db == nil {
    return errors.New("db unavailable")
  }

  if _, err := m.db.Exec(ctx, `
create table if not exists tor_peer_checker_config (
  id integer primary key,
  enabled boolean not null default false,
  interval_hours integer not null default 2,
  updated_at timestamptz not null default now()
);
`); err != nil {
    return err
  }

  _, err := m.db.Exec(ctx, `
insert into tor_peer_checker_config (id)
values ($1)
on conflict (id) do nothing
`, torPeerCheckerConfigID)
  return err
}

func (m *TorPeerChecker) GetConfig(ctx context.Context) (TorPeerCheckerConfig, error) {
  cfg := defaultTorPeerCheckerConfig()
  if m.db == nil {
    return cfg, errors.New("db unavailable")
  }

  err := m.db.QueryRow(ctx, `
select enabled, interval_hours
from tor_peer_checker_config
where id = $1
`, torPeerCheckerConfigID).Scan(
    &cfg.Enabled,
    &cfg.IntervalHours,
  )
  if err != nil {
    if errors.Is(err, pgx.ErrNoRows) {
      return cfg, nil
    }
    return cfg, err
  }
  return normalizeTorPeerCheckerConfig(cfg), nil
}

func (m *TorPeerChecker) upsertConfig(ctx context.Context, cfg TorPeerCheckerConfig) error {
  if m.db == nil {
    return errors.New("db unavailable")
  }
  _, err := m.db.Exec(ctx, `
insert into tor_peer_checker_config (id, enabled, interval_hours, updated_at)
values ($1, $2, $3, now())
on conflict (id) do update set
  enabled = excluded.enabled,
  interval_hours = excluded.interval_hours,
  updated_at = now()
`, torPeerCheckerConfigID, cfg.Enabled, cfg.IntervalHours)
  return err
}

func (m *TorPeerChecker) Start() {
  m.mu.Lock()
  if m.started {
    m.mu.Unlock()
    return
  }
  m.started = true
  m.stop = make(chan struct{})
  m.wake = make(chan torPeerCheckerTrigger, 1)
  m.intervalUpdated = make(chan struct{}, 1)
  m.mu.Unlock()

  if err := m.reloadConfig(); err != nil && m.logger != nil {
    m.logger.Printf("tor-peers: config load failed: %v", err)
  }

  go m.run()
}

func (m *TorPeerChecker) Stop() {
  m.mu.Lock()
  if !m.started || m.stop == nil {
    m.mu.Unlock()
    return
  }
  close(m.stop)
  m.stop = nil
  m.started = false
  m.mu.Unlock()
}

func (m *TorPeerChecker) reloadConfig() error {
  ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
  defer cancel()
  cfg, err := m.GetConfig(ctx)
  if err != nil {
    return err
  }
  m.mu.Lock()
  m.config = cfg
  m.mu.Unlock()
  return nil
}

func (m *TorPeerChecker) UpdateConfig(ctx context.Context, update TorPeerCheckerConfigUpdate) (torPeerCheckerStatusPayload, error) {
  current, err := m.GetConfig(ctx)
  if err != nil {
    return torPeerCheckerStatusPayload{}, err
  }

  if update.Enabled != nil {
    current.Enabled = *update.Enabled
  }
  if update.IntervalHours != nil {
    current.IntervalHours = *update.IntervalHours
  }

  if err := validateTorPeerCheckerConfig(current); err != nil {
    return torPeerCheckerStatusPayload{}, err
  }
  if err := m.upsertConfig(ctx, current); err != nil {
    return torPeerCheckerStatusPayload{}, err
  }

  m.mu.Lock()
  m.config = current
  intervalUpdated := m.intervalUpdated
  m.mu.Unlock()

  if intervalUpdated != nil {
    select {
    case intervalUpdated <- struct{}{}:
    default:
    }
  }
  if update.RunNow {
    m.trigger(true)
  }
  return m.Snapshot(), nil
}

func (m *TorPeerChecker) Snapshot() torPeerCheckerStatusPayload {
  m.mu.Lock()
  cfg := m.config
  lastAttempt := m.lastAttempt
  lastOK := m.lastOK
  lastError := m.lastError
  lastErrorAt := m.lastErrorAt
  lastCheckedCount := m.lastCheckedCount
  lastHybridOnTor := m.lastHybridOnTor
  lastAttempted := m.lastAttemptedCount
  lastSwitched := m.lastSwitchedCount
  inFlight := m.inFlight
  m.mu.Unlock()

  status := "disabled"
  if cfg.Enabled {
    status = "checking"
    interval := time.Duration(localMaxInt(cfg.IntervalHours, 1)) * time.Hour
    if inFlight {
      status = "checking"
    } else if lastError != "" && (lastOK.IsZero() || lastErrorAt.After(lastOK)) {
      status = "warn"
    } else if !lastOK.IsZero() {
      status = "ok"
      if time.Since(lastOK) > interval*2 {
        status = "warn"
      }
    }
  }

  payload := torPeerCheckerStatusPayload{
    Enabled:              cfg.Enabled,
    Status:               status,
    IntervalHours:        cfg.IntervalHours,
    LastCheckedCount:     lastCheckedCount,
    LastHybridOnTorCount: lastHybridOnTor,
    LastAttemptedCount:   lastAttempted,
    LastSwitchedCount:    lastSwitched,
  }
  if !lastAttempt.IsZero() {
    payload.LastAttemptAt = lastAttempt.UTC().Format(time.RFC3339)
  }
  if !lastOK.IsZero() {
    payload.LastOkAt = lastOK.UTC().Format(time.RFC3339)
  }
  if lastError != "" {
    payload.LastError = lastError
  }
  if !lastErrorAt.IsZero() {
    payload.LastErrorAt = lastErrorAt.UTC().Format(time.RFC3339)
  }
  return payload
}

func (m *TorPeerChecker) Logs(limit int) []torPeerCheckerLogEntry {
  if limit <= 0 {
    limit = torPeerCheckerDefaultLogLimit
  }
  if limit > torPeerCheckerMaxLogLimit {
    limit = torPeerCheckerMaxLogLimit
  }

  m.mu.Lock()
  defer m.mu.Unlock()

  if len(m.logs) == 0 {
    return []torPeerCheckerLogEntry{}
  }
  out := make([]torPeerCheckerLogEntry, 0, localMinInt(limit, len(m.logs)))
  for i := len(m.logs) - 1; i >= 0 && len(out) < limit; i-- {
    out = append(out, m.logs[i])
  }
  return out
}

func (m *TorPeerChecker) appendLog(entry torPeerCheckerLogEntry) {
  m.mu.Lock()
  defer m.mu.Unlock()
  if len(m.logs) >= torPeerCheckerLogCapacity {
    copy(m.logs, m.logs[1:])
    m.logs[len(m.logs)-1] = entry
    return
  }
  m.logs = append(m.logs, entry)
}

func (m *TorPeerChecker) trigger(force bool) {
  m.mu.Lock()
  wake := m.wake
  m.mu.Unlock()
  if wake == nil {
    return
  }
  select {
  case wake <- torPeerCheckerTrigger{force: force}:
  default:
  }
}

func (m *TorPeerChecker) currentInterval() time.Duration {
  m.mu.Lock()
  interval := m.config.IntervalHours
  m.mu.Unlock()
  if interval < torPeerCheckerMinIntervalHours {
    interval = torPeerCheckerMinIntervalHours
  }
  if interval > torPeerCheckerMaxIntervalHours {
    interval = torPeerCheckerMaxIntervalHours
  }
  return time.Duration(interval) * time.Hour
}

func (m *TorPeerChecker) run() {
  timer := time.NewTimer(m.currentInterval())
  defer timer.Stop()

  for {
    select {
    case <-timer.C:
      m.tick(false)
      timer.Reset(m.currentInterval())
    case trigger := <-m.wake:
      m.tick(trigger.force)
    case <-m.intervalUpdated:
      if !timer.Stop() {
        select {
        case <-timer.C:
        default:
        }
      }
      timer.Reset(m.currentInterval())
    case <-m.stop:
      return
    }
  }
}

func (m *TorPeerChecker) tick(force bool) {
  m.mu.Lock()
  cfg := m.config
  if (!cfg.Enabled && !force) || m.inFlight {
    m.mu.Unlock()
    return
  }
  m.inFlight = true
  m.lastAttempt = time.Now().UTC()
  m.mu.Unlock()

  defer func() {
    m.mu.Lock()
    m.inFlight = false
    m.mu.Unlock()
  }()

  channelsCtx, channelsCancel := context.WithTimeout(context.Background(), lndRPCTimeout)
  channels, err := m.lnd.ListChannels(channelsCtx)
  channelsCancel()
  if err != nil {
    m.recordFailure(err, 0, 0, 0, 0)
    return
  }

  peersCtx, peersCancel := context.WithTimeout(context.Background(), lndRPCTimeout)
  peers, err := m.lnd.ListPeers(peersCtx)
  peersCancel()
  if err != nil {
    m.recordFailure(err, 0, 0, 0, 0)
    return
  }

  channelPeerSet := map[string]struct{}{}
  for _, ch := range channels {
    pubkey := strings.TrimSpace(ch.RemotePubkey)
    if pubkey == "" {
      continue
    }
    channelPeerSet[pubkey] = struct{}{}
  }

  checked := 0
  hybridOnTor := 0
  attempted := 0
  switched := 0
  failures := 0
  var lastErr error

  for _, peer := range peers {
    pubkey := strings.TrimSpace(peer.PubKey)
    if pubkey == "" {
      continue
    }
    if len(channelPeerSet) > 0 {
      if _, ok := channelPeerSet[pubkey]; !ok {
        continue
      }
    }

    checked++
    currentAddr := normalizeSocket(peer.Address)
    if currentAddr == "" || !isOnionSocket(currentAddr) {
      continue
    }

    infoCtx, infoCancel := context.WithTimeout(context.Background(), lndRPCTimeout)
    details, detailsErr := m.lnd.GetNodeDetails(infoCtx, pubkey)
    infoCancel()
    if detailsErr != nil {
      failures++
      lastErr = detailsErr
      alias := fallbackAlias(peer.Alias, "", pubkey)
      m.appendLog(torPeerCheckerLogEntry{
        Timestamp:   time.Now().UTC().Format(time.RFC3339),
        Alias:       alias,
        PubKey:      pubkey,
        FromAddress: currentAddr,
        Result:      "error",
        Detail:      fmt.Sprintf("getnodeinfo failed: %v", detailsErr),
      })
      continue
    }

    clearnet, hasOnion := splitNodeSockets(details.Addresses)
    if !hasOnion || len(clearnet) == 0 {
      continue
    }

    hybridOnTor++
    attempted++
    alias := fallbackAlias(peer.Alias, details.Alias, pubkey)

    newAddr, switchErr := m.switchPeerToClearnet(pubkey, currentAddr, clearnet)
    if switchErr != nil {
      failures++
      lastErr = switchErr
      m.appendLog(torPeerCheckerLogEntry{
        Timestamp:   time.Now().UTC().Format(time.RFC3339),
        Alias:       alias,
        PubKey:      pubkey,
        FromAddress: currentAddr,
        Result:      "failed",
        Detail:      switchErr.Error(),
      })
      continue
    }

    switched++
    m.appendLog(torPeerCheckerLogEntry{
      Timestamp:   time.Now().UTC().Format(time.RFC3339),
      Alias:       alias,
      PubKey:      pubkey,
      FromAddress: currentAddr,
      ToAddress:   newAddr,
      Result:      "switched",
      Detail:      "connected over clearnet",
    })
  }

  m.appendLog(torPeerCheckerLogEntry{
    Timestamp: time.Now().UTC().Format(time.RFC3339),
    Alias:     "run",
    Result:    "summary",
    Detail: fmt.Sprintf(
      "checked=%d hybrid_on_tor=%d attempted=%d switched=%d",
      checked,
      hybridOnTor,
      attempted,
      switched,
    ),
  })

  if failures > 0 {
    if lastErr == nil {
      lastErr = errors.New("tor peer checker run completed with failures")
    } else {
      lastErr = fmt.Errorf("tor peer checker run completed with failures: %w", lastErr)
    }
    m.recordFailure(lastErr, checked, hybridOnTor, attempted, switched)
    return
  }
  m.recordSuccess(checked, hybridOnTor, attempted, switched)
}

func (m *TorPeerChecker) switchPeerToClearnet(pubkey string, currentAddr string, candidates []string) (string, error) {
  oldSocket := normalizeSocket(currentAddr)
  for _, candidate := range candidates {
    socket := normalizeSocket(candidate)
    if socket == "" || isOnionSocket(socket) || !socketHasPort(socket) {
      continue
    }

    for i := 0; i < torPeerSwitchRetries; i++ {
      disconnectCtx, disconnectCancel := context.WithTimeout(context.Background(), lndRPCTimeout)
      _ = m.lnd.DisconnectPeer(disconnectCtx, pubkey)
      disconnectCancel()

      connectCtx, connectCancel := context.WithTimeout(context.Background(), lndRPCTimeout)
      err := m.lnd.ConnectPeerWithTimeout(connectCtx, pubkey, socket, false, torPeerConnectTimeoutSec)
      connectCancel()
      if err != nil && !isAlreadyConnectedErr(err) {
        if m.logger != nil {
          m.logger.Printf("tor-peers: connect attempt failed for %s via %s: %v", pubkey, socket, err)
        }
      }

      latestAddr, addrErr := m.peerCurrentAddress(pubkey)
      if addrErr == nil && latestAddr != "" && !isOnionSocket(latestAddr) {
        return latestAddr, nil
      }
      if i < torPeerSwitchRetries-1 {
        time.Sleep(2 * time.Second)
      }
    }
  }

  if oldSocket != "" && socketHasPort(oldSocket) {
    reconnectCtx, reconnectCancel := context.WithTimeout(context.Background(), lndRPCTimeout)
    _ = m.lnd.ConnectPeerWithTimeout(reconnectCtx, pubkey, oldSocket, false, torPeerConnectTimeoutSec)
    reconnectCancel()
  }

  return "", errors.New("failed to switch peer to clearnet after multiple attempts")
}

func (m *TorPeerChecker) peerCurrentAddress(pubkey string) (string, error) {
  ctx, cancel := context.WithTimeout(context.Background(), lndRPCTimeout)
  peers, err := m.lnd.ListPeers(ctx)
  cancel()
  if err != nil {
    return "", err
  }
  for _, peer := range peers {
    if strings.TrimSpace(peer.PubKey) == strings.TrimSpace(pubkey) {
      return normalizeSocket(peer.Address), nil
    }
  }
  return "", nil
}

func (m *TorPeerChecker) recordFailure(err error, checked int, hybridOnTor int, attempted int, switched int) {
  msg := strings.TrimSpace(err.Error())
  if msg == "" {
    msg = "tor peer checker failed"
  }

  m.mu.Lock()
  m.lastError = msg
  m.lastErrorAt = time.Now().UTC()
  m.lastCheckedCount = checked
  m.lastHybridOnTor = hybridOnTor
  m.lastAttemptedCount = attempted
  m.lastSwitchedCount = switched
  m.mu.Unlock()

  if m.logger != nil {
    m.logger.Printf("tor-peers: %s", msg)
  }
}

func (m *TorPeerChecker) recordSuccess(checked int, hybridOnTor int, attempted int, switched int) {
  m.mu.Lock()
  hadErr := m.lastError != ""
  m.lastOK = time.Now().UTC()
  m.lastError = ""
  m.lastErrorAt = time.Time{}
  m.lastCheckedCount = checked
  m.lastHybridOnTor = hybridOnTor
  m.lastAttemptedCount = attempted
  m.lastSwitchedCount = switched
  m.mu.Unlock()

  if hadErr && m.logger != nil {
    m.logger.Printf("tor-peers: recovered")
  }
  if m.logger != nil {
    m.logger.Printf("tor-peers: run completed checked=%d hybrid_on_tor=%d attempted=%d switched=%d", checked, hybridOnTor, attempted, switched)
  }
}

func splitNodeSockets(addresses []lndclient.NodeAddress) ([]string, bool) {
  out := make([]string, 0, len(addresses))
  seen := map[string]struct{}{}
  hasOnion := false
  for _, item := range addresses {
    socket := normalizeSocket(item.Addr)
    if socket == "" {
      continue
    }
    if isOnionSocket(socket) {
      hasOnion = true
      continue
    }
    if !socketHasPort(socket) {
      continue
    }
    if _, ok := seen[socket]; ok {
      continue
    }
    seen[socket] = struct{}{}
    out = append(out, socket)
  }
  return out, hasOnion
}

func normalizeSocket(value string) string {
  socket := strings.TrimSpace(value)
  if socket == "" {
    return ""
  }
  if strings.Contains(socket, "@") {
    parts := strings.SplitN(socket, "@", 2)
    socket = strings.TrimSpace(parts[1])
  }
  return socket
}

func socketHasPort(socket string) bool {
  _, port := splitHostPortLoose(socket)
  return strings.TrimSpace(port) != ""
}

func isOnionSocket(socket string) bool {
  host, _ := splitHostPortLoose(socket)
  host = strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
  return strings.HasSuffix(host, ".onion")
}

func splitHostPortLoose(socket string) (string, string) {
  raw := normalizeSocket(socket)
  if raw == "" {
    return "", ""
  }
  if strings.HasPrefix(raw, "[") {
    if host, port, err := net.SplitHostPort(raw); err == nil {
      return strings.Trim(host, "[]"), port
    }
  }
  if host, port, err := net.SplitHostPort(raw); err == nil {
    return host, port
  }
  if strings.Count(raw, ":") == 1 {
    idx := strings.LastIndex(raw, ":")
    if idx > 0 && idx < len(raw)-1 {
      return raw[:idx], raw[idx+1:]
    }
  }
  return strings.Trim(raw, "[]"), ""
}

func fallbackAlias(peerAlias string, detailsAlias string, pubkey string) string {
  if alias := strings.TrimSpace(peerAlias); alias != "" {
    return alias
  }
  if alias := strings.TrimSpace(detailsAlias); alias != "" {
    return alias
  }
  return shortIdentifier(pubkey)
}

func isAlreadyConnectedErr(err error) bool {
  if err == nil {
    return false
  }
  msg := strings.ToLower(strings.TrimSpace(err.Error()))
  return strings.Contains(msg, "already connected")
}

func parseTorPeerCheckerLogLimit(raw string) int {
  raw = strings.TrimSpace(raw)
  if raw == "" {
    return torPeerCheckerDefaultLogLimit
  }
  n, err := strconv.Atoi(raw)
  if err != nil {
    return torPeerCheckerDefaultLogLimit
  }
  if n <= 0 {
    return torPeerCheckerDefaultLogLimit
  }
  if n > torPeerCheckerMaxLogLimit {
    return torPeerCheckerMaxLogLimit
  }
  return n
}

func (s *Server) handleLNTorPeerCheckerGet(w http.ResponseWriter, r *http.Request) {
  svc, errMsg := s.torPeerCheckerService()
  if svc == nil {
    if errMsg == "" {
      errMsg = "tor peer checker unavailable"
    }
    writeError(w, http.StatusServiceUnavailable, errMsg)
    return
  }
  writeJSON(w, http.StatusOK, svc.Snapshot())
}

func (s *Server) handleLNTorPeerCheckerPost(w http.ResponseWriter, r *http.Request) {
  svc, errMsg := s.torPeerCheckerService()
  if svc == nil {
    if errMsg == "" {
      errMsg = "tor peer checker unavailable"
    }
    writeError(w, http.StatusServiceUnavailable, errMsg)
    return
  }

  var req struct {
    Enabled       *bool `json:"enabled"`
    IntervalHours *int  `json:"interval_hours"`
    RunNow        bool  `json:"run_now"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
  defer cancel()
  payload, err := svc.UpdateConfig(ctx, TorPeerCheckerConfigUpdate{
    Enabled:       req.Enabled,
    IntervalHours: req.IntervalHours,
    RunNow:        req.RunNow,
  })
  if err != nil {
    if errors.Is(err, errInvalidTorPeerCheckerConfig) {
      writeError(w, http.StatusBadRequest, err.Error())
      return
    }
    writeError(w, http.StatusInternalServerError, err.Error())
    return
  }
  writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleLNTorPeerCheckerLogs(w http.ResponseWriter, r *http.Request) {
  svc, errMsg := s.torPeerCheckerService()
  if svc == nil {
    if errMsg == "" {
      errMsg = "tor peer checker unavailable"
    }
    writeError(w, http.StatusServiceUnavailable, errMsg)
    return
  }
  limit := parseTorPeerCheckerLogLimit(r.URL.Query().Get("limit"))
  entries := svc.Logs(limit)
  writeJSON(w, http.StatusOK, map[string]any{
    "entries": entries,
  })
}
