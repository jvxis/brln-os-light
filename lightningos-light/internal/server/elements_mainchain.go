package server

import (
  "context"
  "net/http"
  "os"
  "strings"
  "time"

  "lightningos-light/internal/config"
)

type elementsMainchainState struct {
  Source string `json:"source"`
  RPCHost string `json:"rpchost,omitempty"`
  RPCPort int `json:"rpcport,omitempty"`
  LocalReady bool `json:"local_ready"`
  LocalStatus string `json:"local_status,omitempty"`
}

func (s *Server) handleElementsMainchainGet(w http.ResponseWriter, r *http.Request) {
  paths := elementsAppPaths()
  source := readElementsMainchainSource(paths)
  ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
  defer cancel()

  host, port := elementsMainchainHostPort(ctx, paths, source, s.cfg)
  ready, status := s.elementsLocalBitcoinReady(ctx)

  writeJSON(w, http.StatusOK, elementsMainchainState{
    Source: source,
    RPCHost: host,
    RPCPort: port,
    LocalReady: ready,
    LocalStatus: status,
  })
}

func (s *Server) handleElementsMainchainPost(w http.ResponseWriter, r *http.Request) {
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

  paths := elementsAppPaths()
  if !fileExists(paths.ElementsdPath) {
    writeError(w, http.StatusBadRequest, "Elements is not installed")
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
  defer cancel()

  if source == "local" {
    ready, _ := s.elementsLocalBitcoinReady(ctx)
    if !ready {
      writeError(w, http.StatusBadRequest, "local bitcoin is not fully synced")
      return
    }
  }

  if err := os.MkdirAll(paths.AppDataDir, 0750); err != nil {
    writeError(w, http.StatusInternalServerError, "failed to prepare app data")
    return
  }
  previousSource := readElementsMainchainSource(paths)
  if err := writeElementsMainchainSource(paths, source); err != nil {
    writeError(w, http.StatusInternalServerError, "failed to store mainchain source")
    return
  }
  if err := ensureElementsConfig(ctx, paths, s.cfg); err != nil {
    _ = writeElementsMainchainSource(paths, previousSource)
    writeError(w, http.StatusInternalServerError, err.Error())
    return
  }
  if _, err := runSystemd(ctx, "systemctl", "restart", elementsServiceName); err != nil {
    writeError(w, http.StatusInternalServerError, "elements restart failed")
    return
  }
  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func elementsMainchainHostPort(ctx context.Context, paths elementsPaths, source string, cfg *config.Config) (string, int) {
  raw, err := readElementsConfig(ctx, paths)
  if err == nil {
    host, port := parseElementsMainchainConfig(raw)
    if host != "" {
      if port == 0 {
        port = defaultElementsMainchainPort(source, cfg)
      }
      return host, port
    }
  }
  host := defaultElementsMainchainHost(source, cfg)
  port := defaultElementsMainchainPort(source, cfg)
  return host, port
}

func defaultElementsMainchainHost(source string, cfg *config.Config) string {
  if strings.ToLower(source) == "local" {
    return "127.0.0.1"
  }
  if cfg == nil {
    return ""
  }
  host, _ := parseMainchainRPC(cfg.BitcoinRemote.RPCHost)
  return host
}

func defaultElementsMainchainPort(source string, cfg *config.Config) int {
  if strings.ToLower(source) == "local" {
    return 8332
  }
  if cfg == nil {
    return 0
  }
  _, port := parseMainchainRPC(cfg.BitcoinRemote.RPCHost)
  return port
}

func (s *Server) elementsLocalBitcoinReady(ctx context.Context) (bool, string) {
  return s.bitcoinLocalReady(ctx)
}
