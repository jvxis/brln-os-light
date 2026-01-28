package server

import (
  "bytes"
  "context"
  "encoding/json"
  "errors"
  "fmt"
  "log"
  "net/http"
  "os"
  "strconv"
  "strings"
  "sync"
  "time"

  "lightningos-light/internal/lndclient"
)

const (
  ambossHealthURL = "https://api.amboss.space/graphql"
  ambossHealthEnabledEnv = "AMBOSS_HEALTHCHECK_ENABLED"
  ambossHealthIntervalEnv = "AMBOSS_HEALTHCHECK_INTERVAL_SEC"
  ambossHealthDefaultInterval = 300 * time.Second
)

type ambossHealthPayload struct {
  Enabled bool `json:"enabled"`
  Status string `json:"status"`
  LastOkAt string `json:"last_ok_at,omitempty"`
  LastError string `json:"last_error,omitempty"`
  LastErrorAt string `json:"last_error_at,omitempty"`
  LastAttemptAt string `json:"last_attempt_at,omitempty"`
  IntervalSec int `json:"interval_sec"`
  ConsecutiveFailures int `json:"consecutive_failures,omitempty"`
}

type AmbossHealthChecker struct {
  lnd *lndclient.Client
  logger *log.Logger

  mu sync.Mutex
  enabled bool
  interval time.Duration
  lastAttempt time.Time
  lastOK time.Time
  lastError string
  lastErrorAt time.Time
  consecutiveFailures int
  inFlight bool
  started bool
  stop chan struct{}
  wake chan struct{}
}

func NewAmbossHealthChecker(lnd *lndclient.Client, logger *log.Logger) *AmbossHealthChecker {
  enabled := readAmbossEnabled()
  interval := readAmbossInterval()
  return &AmbossHealthChecker{
    lnd: lnd,
    logger: logger,
    enabled: enabled,
    interval: interval,
  }
}

func (a *AmbossHealthChecker) Start() {
  a.mu.Lock()
  if a.started {
    a.mu.Unlock()
    return
  }
	a.started = true
	a.stop = make(chan struct{})
	a.wake = make(chan struct{}, 1)
	interval := a.interval
	enabled := a.enabled
	if interval <= 0 {
		interval = ambossHealthDefaultInterval
		a.interval = interval
	}
	a.mu.Unlock()

	go a.run(interval)
	if enabled {
		a.trigger()
	}
}

func (a *AmbossHealthChecker) Stop() {
  a.mu.Lock()
  if !a.started || a.stop == nil {
    a.mu.Unlock()
    return
  }
  close(a.stop)
  a.stop = nil
  a.started = false
  a.mu.Unlock()
}

func (a *AmbossHealthChecker) SetEnabled(enabled bool) error {
  if err := storeAmbossEnabled(enabled); err != nil {
    return err
  }
  a.mu.Lock()
  a.enabled = enabled
  a.mu.Unlock()

  if enabled {
    a.trigger()
  }
  return nil
}

func (a *AmbossHealthChecker) Snapshot() ambossHealthPayload {
  a.mu.Lock()
  enabled := a.enabled
  interval := a.interval
  lastAttempt := a.lastAttempt
  lastOK := a.lastOK
  lastError := a.lastError
  lastErrorAt := a.lastErrorAt
  failures := a.consecutiveFailures
  a.mu.Unlock()

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
      if interval > 0 && time.Since(lastOK) > interval*2 {
        status = "warn"
      }
    }
  }

  payload := ambossHealthPayload{
    Enabled: enabled,
    Status: status,
    IntervalSec: int(interval.Seconds()),
    ConsecutiveFailures: failures,
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

func (a *AmbossHealthChecker) trigger() {
  a.mu.Lock()
  wake := a.wake
  a.mu.Unlock()
  if wake == nil {
    return
  }
  select {
  case wake <- struct{}{}:
  default:
  }
}

func (a *AmbossHealthChecker) run(interval time.Duration) {
  ticker := time.NewTicker(interval)
  defer ticker.Stop()

  for {
    select {
    case <-ticker.C:
      a.tick()
    case <-a.wake:
      a.tick()
    case <-a.stop:
      return
    }
  }
}

func (a *AmbossHealthChecker) tick() {
  a.mu.Lock()
  if !a.enabled || a.inFlight {
    a.mu.Unlock()
    return
  }
  a.inFlight = true
  a.lastAttempt = time.Now().UTC()
  a.mu.Unlock()

  defer func() {
    a.mu.Lock()
    a.inFlight = false
    a.mu.Unlock()
  }()

  ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
  err := a.sendHealthCheck(ctx)
  cancel()

  if err != nil {
    a.recordFailure(err)
    return
  }
  a.recordSuccess()
}

func (a *AmbossHealthChecker) recordFailure(err error) {
  msg := strings.TrimSpace(err.Error())
  if msg == "" {
    msg = "amboss health check failed"
  }

  a.mu.Lock()
  a.lastError = msg
  a.lastErrorAt = time.Now().UTC()
  a.consecutiveFailures++
  a.mu.Unlock()

  if a.logger != nil {
    a.logger.Printf("amboss health check failed: %s", msg)
  }
}

func (a *AmbossHealthChecker) recordSuccess() {
  a.mu.Lock()
  hadFailures := a.consecutiveFailures > 0
  a.lastOK = time.Now().UTC()
  a.lastError = ""
  a.lastErrorAt = time.Time{}
  a.consecutiveFailures = 0
  a.mu.Unlock()

  if hadFailures && a.logger != nil {
    a.logger.Printf("amboss health check recovered")
  }
}

type ambossGraphQLRequest struct {
  Query string `json:"query"`
  Variables map[string]string `json:"variables"`
}

type ambossGraphQLResponse struct {
	Data *struct {
		HealthCheck any `json:"healthCheck"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (a *AmbossHealthChecker) sendHealthCheck(ctx context.Context) error {
  if a.lnd == nil {
    return errors.New("lnd client unavailable")
  }

  now := time.Now().UTC()
  timestamp := now.Format("2006-01-02T15:04:05-0700")

  signature, err := a.lnd.SignMessage(ctx, timestamp)
  if err != nil {
    return fmt.Errorf("sign message failed: %w", err)
  }

  payload := ambossGraphQLRequest{
    Query: "mutation HealthCheck($signature: String!, $timestamp: String!) { healthCheck(signature: $signature, timestamp: $timestamp) }",
    Variables: map[string]string{
      "signature": signature,
      "timestamp": timestamp,
    },
  }

  body, err := json.Marshal(payload)
  if err != nil {
    return err
  }

  req, err := http.NewRequestWithContext(ctx, http.MethodPost, ambossHealthURL, bytes.NewReader(body))
  if err != nil {
    return err
  }
  req.Header.Set("Content-Type", "application/json")

  client := &http.Client{Timeout: 5 * time.Second}
  resp, err := client.Do(req)
  if err != nil {
    return err
  }
  defer resp.Body.Close()

  if resp.StatusCode < 200 || resp.StatusCode >= 300 {
    return fmt.Errorf("amboss api status %d", resp.StatusCode)
  }

  var result ambossGraphQLResponse
  if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
    return err
  }
  if len(result.Errors) > 0 {
    return errors.New(strings.TrimSpace(result.Errors[0].Message))
  }
  if result.Data == nil {
    return errors.New("amboss api returned empty response")
  }
  return nil
}

func (s *Server) handleAmbossHealthGet(w http.ResponseWriter, r *http.Request) {
  if s.amboss == nil {
    writeJSON(w, http.StatusOK, ambossHealthPayload{
      Enabled: false,
      Status: "disabled",
      IntervalSec: int(ambossHealthDefaultInterval.Seconds()),
    })
    return
  }
  writeJSON(w, http.StatusOK, s.amboss.Snapshot())
}

func (s *Server) handleAmbossHealthPost(w http.ResponseWriter, r *http.Request) {
  if s.amboss == nil {
    writeError(w, http.StatusServiceUnavailable, "amboss health check unavailable")
    return
  }
  var req struct {
    Enabled bool `json:"enabled"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  if err := s.amboss.SetEnabled(req.Enabled); err != nil {
    writeError(w, http.StatusInternalServerError, err.Error())
    return
  }
  writeJSON(w, http.StatusOK, s.amboss.Snapshot())
}

func readAmbossEnabled() bool {
  if val := strings.TrimSpace(os.Getenv(ambossHealthEnabledEnv)); val != "" {
    if parsed, ok := parseEnvBool(val); ok {
      return parsed
    }
  }
  if val, err := readEnvFileValue(secretsPath, ambossHealthEnabledEnv); err == nil {
    if parsed, ok := parseEnvBool(val); ok {
      return parsed
    }
  }
  return false
}

func storeAmbossEnabled(enabled bool) error {
  if err := ensureSecretsDir(); err != nil {
    return err
  }
  value := "0"
  if enabled {
    value = "1"
  }
  if err := writeEnvFileValue(secretsPath, ambossHealthEnabledEnv, value); err != nil {
    return err
  }
  _ = os.Setenv(ambossHealthEnabledEnv, value)
  return nil
}

func readAmbossInterval() time.Duration {
  if val := strings.TrimSpace(os.Getenv(ambossHealthIntervalEnv)); val != "" {
    if parsed := parseEnvSeconds(val); parsed > 0 {
      return parsed
    }
  }
  if val, err := readEnvFileValue(secretsPath, ambossHealthIntervalEnv); err == nil {
    if parsed := parseEnvSeconds(val); parsed > 0 {
      return parsed
    }
  }
  return ambossHealthDefaultInterval
}

func parseEnvSeconds(raw string) time.Duration {
  trimmed := strings.TrimSpace(raw)
  if trimmed == "" {
    return 0
  }
  seconds, err := strconv.Atoi(trimmed)
  if err != nil || seconds <= 0 {
    return 0
  }
  return time.Duration(seconds) * time.Second
}

func parseEnvBool(raw string) (bool, bool) {
  trimmed := strings.ToLower(strings.TrimSpace(raw))
  switch trimmed {
  case "1", "true", "yes", "y", "on", "enabled":
    return true, true
  case "0", "false", "no", "n", "off", "disabled":
    return false, true
  default:
    return false, false
  }
}
