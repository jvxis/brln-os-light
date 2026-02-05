package server

import (
  "context"
  "fmt"
  "log"
  "net/http"
  "os"
  "strings"
  "sync"
  "time"

  "lightningos-light/internal/lndclient"
)

const (
  chanHealEnabledEnv = "LND_CHAN_HEAL_ENABLED"
  chanHealIntervalEnv = "LND_CHAN_HEAL_INTERVAL_SEC"
  chanHealDefaultInterval = 300 * time.Second
)

type chanStatusHealPayload struct {
  Enabled bool `json:"enabled"`
  Status string `json:"status"`
  IntervalSec int `json:"interval_sec"`
  LastAttemptAt string `json:"last_attempt_at,omitempty"`
  LastOkAt string `json:"last_ok_at,omitempty"`
  LastError string `json:"last_error,omitempty"`
  LastErrorAt string `json:"last_error_at,omitempty"`
  LastUpdated int `json:"last_updated,omitempty"`
}

type ChanStatusHealer struct {
  lnd *lndclient.Client
  logger *log.Logger

  mu sync.Mutex
  enabled bool
  interval time.Duration
  lastAttempt time.Time
  lastOK time.Time
  lastError string
  lastErrorAt time.Time
  lastUpdated int
  inFlight bool
  started bool
  stop chan struct{}
  wake chan struct{}
  intervalUpdated chan struct{}
}

func NewChanStatusHealer(lnd *lndclient.Client, logger *log.Logger) *ChanStatusHealer {
  enabled := readChanHealEnabled()
  interval := readChanHealInterval()
  return &ChanStatusHealer{
    lnd: lnd,
    logger: logger,
    enabled: enabled,
    interval: interval,
  }
}

func (c *ChanStatusHealer) Start() {
  c.mu.Lock()
  if c.started {
    c.mu.Unlock()
    return
  }
  c.started = true
  c.stop = make(chan struct{})
  c.wake = make(chan struct{}, 1)
  c.intervalUpdated = make(chan struct{}, 1)
  if c.interval <= 0 {
    c.interval = chanHealDefaultInterval
  }
  enabled := c.enabled
  c.mu.Unlock()

  go c.run()
  if enabled {
    c.trigger()
  }
}

func (c *ChanStatusHealer) Stop() {
  c.mu.Lock()
  if !c.started || c.stop == nil {
    c.mu.Unlock()
    return
  }
  close(c.stop)
  c.stop = nil
  c.started = false
  c.mu.Unlock()
}

func (c *ChanStatusHealer) SetEnabled(enabled bool) error {
  if err := storeChanHealEnabled(enabled); err != nil {
    return err
  }
  c.mu.Lock()
  c.enabled = enabled
  c.mu.Unlock()
  if enabled {
    c.trigger()
  }
  return nil
}

func (c *ChanStatusHealer) SetInterval(seconds int) error {
  if seconds <= 0 {
    return fmt.Errorf("interval_sec must be positive")
  }
  if err := storeChanHealInterval(seconds); err != nil {
    return err
  }
  c.mu.Lock()
  c.interval = time.Duration(seconds) * time.Second
  intervalUpdated := c.intervalUpdated
  c.mu.Unlock()

  if intervalUpdated != nil {
    select {
    case intervalUpdated <- struct{}{}:
    default:
    }
  }
  return nil
}

func (c *ChanStatusHealer) Snapshot() chanStatusHealPayload {
  c.mu.Lock()
  enabled := c.enabled
  interval := c.interval
  lastAttempt := c.lastAttempt
  lastOK := c.lastOK
  lastError := c.lastError
  lastErrorAt := c.lastErrorAt
  lastUpdated := c.lastUpdated
  c.mu.Unlock()

  if interval <= 0 {
    interval = chanHealDefaultInterval
  }

  status := "disabled"
  if enabled {
    status = "checking"
    if lastError != "" {
      status = "warn"
    }
    if !lastOK.IsZero() {
      status = "ok"
      if lastError != "" && lastErrorAt.After(lastOK) {
        status = "warn"
      }
      if time.Since(lastOK) > interval*2 {
        status = "warn"
      }
    }
  }

  payload := chanStatusHealPayload{
    Enabled: enabled,
    Status: status,
    IntervalSec: int(interval.Seconds()),
    LastUpdated: lastUpdated,
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

func (c *ChanStatusHealer) trigger() {
  c.mu.Lock()
  wake := c.wake
  c.mu.Unlock()
  if wake == nil {
    return
  }
  select {
  case wake <- struct{}{}:
  default:
  }
}

func (c *ChanStatusHealer) currentInterval() time.Duration {
  c.mu.Lock()
  interval := c.interval
  c.mu.Unlock()
  if interval <= 0 {
    interval = chanHealDefaultInterval
  }
  return interval
}

func (c *ChanStatusHealer) run() {
  timer := time.NewTimer(c.currentInterval())
  defer timer.Stop()

  for {
    select {
    case <-timer.C:
      c.tick()
      timer.Reset(c.currentInterval())
    case <-c.wake:
      c.tick()
    case <-c.intervalUpdated:
      if !timer.Stop() {
        select {
        case <-timer.C:
        default:
        }
      }
      timer.Reset(c.currentInterval())
    case <-c.stop:
      return
    }
  }
}

func (c *ChanStatusHealer) tick() {
  c.mu.Lock()
  if !c.enabled || c.inFlight {
    c.mu.Unlock()
    return
  }
  c.inFlight = true
  c.lastAttempt = time.Now().UTC()
  c.mu.Unlock()

  defer func() {
    c.mu.Lock()
    c.inFlight = false
    c.mu.Unlock()
  }()

  ctx, cancel := context.WithTimeout(context.Background(), lndRPCTimeout)
  channels, err := c.lnd.ListChannels(ctx)
  cancel()
  if err != nil {
    c.recordFailure(err)
    return
  }

  if len(channels) == 0 {
    c.recordSuccess(0)
    return
  }

  updated := 0
  var lastErr error
  for _, ch := range channels {
    if !ch.Active {
      continue
    }
    if !isChanLocallyDisabled(ch) {
      continue
    }
    if strings.TrimSpace(ch.ChannelPoint) == "" {
      continue
    }
    ctx, cancel := context.WithTimeout(context.Background(), lndRPCTimeout)
    err := c.lnd.UpdateChanStatus(ctx, ch.ChannelPoint, true)
    cancel()
    if err != nil {
      lastErr = err
      if c.logger != nil {
        c.logger.Printf("chan-heal: failed to enable %s: %v", ch.ChannelPoint, err)
      }
      continue
    }
    updated++
  }

  if lastErr != nil {
    c.recordFailure(lastErr)
    return
  }
  c.recordSuccess(updated)
}

func (c *ChanStatusHealer) recordFailure(err error) {
  msg := strings.TrimSpace(err.Error())
  if msg == "" {
    msg = "channel status heal failed"
  }

  c.mu.Lock()
  c.lastError = msg
  c.lastErrorAt = time.Now().UTC()
  c.mu.Unlock()

  if c.logger != nil {
    c.logger.Printf("chan-heal: %s", msg)
  }
}

func (c *ChanStatusHealer) recordSuccess(updated int) {
  c.mu.Lock()
  hadError := c.lastError != ""
  c.lastOK = time.Now().UTC()
  c.lastError = ""
  c.lastErrorAt = time.Time{}
  c.lastUpdated = updated
  c.mu.Unlock()

  if hadError && c.logger != nil {
    c.logger.Printf("chan-heal: recovered")
  }
  if c.logger != nil && updated > 0 {
    c.logger.Printf("chan-heal: enabled %d channel(s)", updated)
  }
}

func isChanLocallyDisabled(ch lndclient.ChannelInfo) bool {
  if ch.LocalDisabled {
    return true
  }
  return isLocalChanDisabled(ch.ChanStatusFlags)
}

func isLocalChanDisabled(flags string) bool {
  trimmed := strings.TrimSpace(flags)
  if trimmed == "" {
    return false
  }
  normalized := strings.ToLower(trimmed)
  return (strings.Contains(normalized, "local") && strings.Contains(normalized, "disabled")) ||
    strings.Contains(normalized, "localchandisabled") ||
    strings.Contains(normalized, "local_chan_disabled")
}

func readChanHealEnabled() bool {
  if val := strings.TrimSpace(os.Getenv(chanHealEnabledEnv)); val != "" {
    if parsed, ok := parseEnvBool(val); ok {
      return parsed
    }
  }
  if val, err := readEnvFileValue(secretsPath, chanHealEnabledEnv); err == nil {
    if parsed, ok := parseEnvBool(val); ok {
      return parsed
    }
  }
  return false
}

func storeChanHealEnabled(enabled bool) error {
  if err := ensureSecretsDir(); err != nil {
    return err
  }
  value := "0"
  if enabled {
    value = "1"
  }
  if err := writeEnvFileValue(secretsPath, chanHealEnabledEnv, value); err != nil {
    return err
  }
  _ = os.Setenv(chanHealEnabledEnv, value)
  return nil
}

func readChanHealInterval() time.Duration {
  if val := strings.TrimSpace(os.Getenv(chanHealIntervalEnv)); val != "" {
    if parsed := parseEnvSeconds(val); parsed > 0 {
      return parsed
    }
  }
  if val, err := readEnvFileValue(secretsPath, chanHealIntervalEnv); err == nil {
    if parsed := parseEnvSeconds(val); parsed > 0 {
      return parsed
    }
  }
  return chanHealDefaultInterval
}

func storeChanHealInterval(seconds int) error {
  if err := ensureSecretsDir(); err != nil {
    return err
  }
  if err := writeEnvFileValue(secretsPath, chanHealIntervalEnv, fmt.Sprintf("%d", seconds)); err != nil {
    return err
  }
  _ = os.Setenv(chanHealIntervalEnv, fmt.Sprintf("%d", seconds))
  return nil
}

func (s *Server) handleLNChanHealGet(w http.ResponseWriter, r *http.Request) {
  if s.chanHealer == nil {
    writeJSON(w, http.StatusOK, chanStatusHealPayload{
      Enabled: false,
      Status: "disabled",
      IntervalSec: int(chanHealDefaultInterval.Seconds()),
    })
    return
  }
  writeJSON(w, http.StatusOK, s.chanHealer.Snapshot())
}

func (s *Server) handleLNChanHealPost(w http.ResponseWriter, r *http.Request) {
  if s.chanHealer == nil {
    writeError(w, http.StatusServiceUnavailable, "channel healer unavailable")
    return
  }
  var req struct {
    Enabled *bool `json:"enabled"`
    IntervalSec *int `json:"interval_sec"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  if req.Enabled != nil {
    if err := s.chanHealer.SetEnabled(*req.Enabled); err != nil {
      writeError(w, http.StatusInternalServerError, err.Error())
      return
    }
  }
  if req.IntervalSec != nil {
    if err := s.chanHealer.SetInterval(*req.IntervalSec); err != nil {
      writeError(w, http.StatusBadRequest, err.Error())
      return
    }
  }
  writeJSON(w, http.StatusOK, s.chanHealer.Snapshot())
}
