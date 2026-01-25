package server

import (
  "encoding/json"
  "net/http"
  "os"
  "strconv"
  "strings"
)

type reportsConfigPayload struct {
  LiveTimeoutSec *int `json:"live_timeout_sec,omitempty"`
  LiveLookbackHours *int `json:"live_lookback_hours,omitempty"`
  RunTimeoutSec *int `json:"run_timeout_sec,omitempty"`
}

func (s *Server) handleReportsConfigGet(w http.ResponseWriter, r *http.Request) {
  payload := reportsConfigPayload{
    LiveTimeoutSec: readEnvInt(secretsPath, "REPORTS_LIVE_TIMEOUT_SEC"),
    LiveLookbackHours: readEnvInt(secretsPath, "REPORTS_LIVE_LOOKBACK_HOURS"),
    RunTimeoutSec: readEnvInt(secretsPath, "REPORTS_RUN_TIMEOUT_SEC"),
  }
  writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleReportsConfigPost(w http.ResponseWriter, r *http.Request) {
  var payload reportsConfigPayload
  if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
    writeError(w, http.StatusBadRequest, "invalid payload")
    return
  }

  if err := ensureSecretsDir(); err != nil {
    writeError(w, http.StatusInternalServerError, "failed to prepare secrets")
    return
  }

  if err := applyEnvInt(secretsPath, "REPORTS_LIVE_TIMEOUT_SEC", payload.LiveTimeoutSec); err != nil {
    writeError(w, http.StatusInternalServerError, "failed to update live timeout")
    return
  }
  if err := applyEnvInt(secretsPath, "REPORTS_LIVE_LOOKBACK_HOURS", payload.LiveLookbackHours); err != nil {
    writeError(w, http.StatusInternalServerError, "failed to update live lookback")
    return
  }
  if err := applyEnvInt(secretsPath, "REPORTS_RUN_TIMEOUT_SEC", payload.RunTimeoutSec); err != nil {
    writeError(w, http.StatusInternalServerError, "failed to update report timeout")
    return
  }

  writeJSON(w, http.StatusOK, payload)
}

func readEnvInt(path string, key string) *int {
  val, err := readEnvFileValue(path, key)
  if err != nil || strings.TrimSpace(val) == "" {
    val = os.Getenv(key)
  }
  val = strings.TrimSpace(val)
  if val == "" {
    return nil
  }
  parsed, err := strconv.Atoi(val)
  if err != nil || parsed <= 0 {
    return nil
  }
  return &parsed
}

func applyEnvInt(path string, key string, value *int) error {
  if value == nil || *value <= 0 {
    _ = removeEnvFileValue(path, key)
    _ = os.Unsetenv(key)
    return nil
  }
  if err := writeEnvFileValue(path, key, strconv.Itoa(*value)); err != nil {
    return err
  }
  _ = os.Setenv(key, strconv.Itoa(*value))
  return nil
}

func removeEnvFileValue(path string, key string) error {
  data, err := os.ReadFile(path)
  if err != nil {
    if os.IsNotExist(err) {
      return nil
    }
    return err
  }
  lines := strings.Split(string(data), "\n")
  prefix := key + "="
  filtered := make([]string, 0, len(lines))
  for _, line := range lines {
    trimmed := strings.TrimSpace(line)
    if strings.HasPrefix(trimmed, prefix) {
      continue
    }
    if strings.TrimSpace(line) == "" {
      continue
    }
    filtered = append(filtered, line)
  }
  output := strings.TrimRight(strings.Join(filtered, "\n"), "\n") + "\n"
  return os.WriteFile(path, []byte(output), 0o660)
}
