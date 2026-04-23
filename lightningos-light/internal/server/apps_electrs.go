package server

import (
  "bufio"
  "context"
  "errors"
  "fmt"
  "io"
  "net/http"
  "os"
  "path/filepath"
  "strconv"
  "strings"
  "time"
)

const (
  electrsAppID       = "electrs"
  electrsRPCPort     = 50001
  electrsMonitorPort = 4224
  electrsImageName   = "lightningos/electrs:v0.11.1"
  electrsVolumeName  = "electrs_data"
)

type electrsPaths struct {
  Root        string
  ComposePath string
  CookiePath  string
}

type electrsRuntimeValues struct {
  BitcoinRPCUser string
  BitcoinRPCPass string
  BitcoinRPCPort int
  Network        string
  BitcoinP2PPort int
}

type electrsApp struct {
  server *Server
}

func newElectrsApp(s *Server) appHandler {
  return electrsApp{server: s}
}

func electrsDefinition() appDefinition {
  return appDefinition{
    ID:          electrsAppID,
    Name:        "Electrs",
    Description: "Electrum server (romanz/electrs) indexing the local Bitcoin Core node.",
    Port:        0,
  }
}

func (a electrsApp) Definition() appDefinition {
  return electrsDefinition()
}

func (a electrsApp) Info(ctx context.Context) (appInfo, error) {
  def := a.Definition()
  info := newAppInfo(def)
  paths := electrsAppPaths()
  if !fileExists(paths.ComposePath) {
    return info, nil
  }
  info.Installed = true
  status, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, "electrs")
  if err != nil {
    info.Status = "unknown"
    return info, err
  }
  info.Status = status
  return info, nil
}

func (a electrsApp) Install(ctx context.Context) error {
  return a.server.applyElectrs(ctx)
}

func (a electrsApp) Start(ctx context.Context) error {
  return a.server.applyElectrs(ctx)
}

func (a electrsApp) Stop(ctx context.Context) error {
  paths := electrsAppPaths()
  if !fileExists(paths.ComposePath) {
    return errors.New("Electrs is not installed")
  }
  return runCompose(ctx, paths.Root, paths.ComposePath, "stop")
}

func (a electrsApp) Uninstall(ctx context.Context) error {
  paths := electrsAppPaths()
  if fileExists(paths.ComposePath) {
    // --volumes wipes the rocksdb index. Re-indexing costs hours on mainnet
    // but avoids the silent-credentials-drift failure mode where a rotated
    // bitcoin.cookie no longer matches the bitcoind RPC user stored in an
    // old index. The index is fully reproducible from Bitcoin Core.
    _ = runCompose(ctx, paths.Root, paths.ComposePath, "down", "--volumes", "--remove-orphans")
  }
  if err := os.RemoveAll(paths.Root); err != nil {
    return fmt.Errorf("failed to remove app files: %w", err)
  }
  return nil
}

func electrsAppPaths() electrsPaths {
  root := filepath.Join(appsRoot, electrsAppID)
  return electrsPaths{
    Root:        root,
    ComposePath: filepath.Join(root, "docker-compose.yaml"),
    CookiePath:  filepath.Join(root, "bitcoin.cookie"),
  }
}

func (s *Server) applyElectrs(ctx context.Context) error {
  if err := ensureDocker(ctx); err != nil {
    return err
  }

  bitcoinPaths := bitcoinCoreAppPaths()
  if !fileExists(bitcoinPaths.ComposePath) {
    return errors.New("Electrs requires the Bitcoin Core app to be installed")
  }

  paths := electrsAppPaths()
  if err := os.MkdirAll(paths.Root, 0750); err != nil {
    return fmt.Errorf("failed to create app directory: %w", err)
  }

  values, err := s.resolveElectrsRuntimeValues(ctx, bitcoinPaths)
  if err != nil {
    return err
  }

  // electrs v0.11 reads bitcoind credentials from --cookie-file; the file must
  // be a single line "user:pass" with NO trailing newline — rust-bitcoincore-rpc
  // uses the file contents verbatim as HTTP basic auth, so a stray \n breaks it
  // and bitcoind responds with HTTP 401. World-readable because the container
  // runs as uid 1000 and the parent dir is 0750 on the host.
  cookie := []byte(values.BitcoinRPCUser + ":" + values.BitcoinRPCPass)
  if err := os.WriteFile(paths.CookiePath, cookie, 0644); err != nil {
    return fmt.Errorf("failed to write bitcoin cookie file: %w", err)
  }

  if _, err := ensureFileWithChange(paths.ComposePath, electrsComposeContents(paths, values)); err != nil {
    return err
  }

  return runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d")
}

func (s *Server) resolveElectrsRuntimeValues(ctx context.Context, bitcoinPaths bitcoinCorePaths) (electrsRuntimeValues, error) {
  localCfg, updated, err := readBitcoinLocalRPCConfig(ctx)
  if err != nil {
    return electrsRuntimeValues{}, fmt.Errorf("local bitcoin RPC unavailable: %w", err)
  }
  if strings.TrimSpace(localCfg.User) == "" || strings.TrimSpace(localCfg.Pass) == "" {
    return electrsRuntimeValues{}, errors.New("local bitcoin RPC credentials missing")
  }
  if updated {
    if err := runCompose(ctx, bitcoinPaths.Root, bitcoinPaths.ComposePath, "restart", "bitcoind"); err != nil {
      return electrsRuntimeValues{}, fmt.Errorf("failed to restart local bitcoind after RPC allowlist update: %w", err)
    }
  }
  _, rpcPort := parseMainchainRPC(localCfg.Host)
  // /data/bitcoin/bitcoin.conf is root-owned on a deployed host, so read via
  // the same docker-exec ladder readBitcoinCoreConfig uses — direct ReadFile
  // would silently permission-deny and fall back to treating the node as
  // mainnet, misconfiguring electrs.
  rawConf, err := readBitcoinCoreConfig(ctx, bitcoinPaths)
  if err != nil {
    return electrsRuntimeValues{}, fmt.Errorf("failed to read bitcoin.conf for chain detection: %w", err)
  }
  network, p2pPort := detectBitcoinCoreChain(rawConf)
  return electrsRuntimeValues{
    BitcoinRPCUser: localCfg.User,
    BitcoinRPCPass: localCfg.Pass,
    BitcoinRPCPort: rpcPort,
    Network:        network,
    BitcoinP2PPort: p2pPort,
  }, nil
}

// detectBitcoinCoreChain scans a bitcoin.conf body for a top-level chain
// selector (testnet/signet/regtest) and returns the electrs --network value
// plus the default bitcoind P2P port for that chain. Only top-level keys
// (above any [section] header) are considered, matching how bitcoind itself
// interprets chain selectors.
func detectBitcoinCoreChain(raw string) (string, int) {
  for _, line := range strings.Split(raw, "\n") {
    trimmed := strings.TrimSpace(line)
    if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
      continue
    }
    if strings.HasPrefix(trimmed, "[") {
      break
    }
    parts := strings.SplitN(trimmed, "=", 2)
    if len(parts) != 2 {
      continue
    }
    key := strings.TrimSpace(parts[0])
    value := strings.TrimSpace(parts[1])
    if value != "1" {
      continue
    }
    switch key {
    case "testnet":
      return "testnet", 18333
    case "signet":
      return "signet", 38333
    case "regtest":
      return "regtest", 18444
    }
  }
  return "bitcoin", 8333
}

// electrsComposeContents builds the docker-compose file. The rocksdb index
// lives in a Docker-managed named volume (electrs_data) rather than a host
// bind mount so the manager (running as `lightningos`) doesn't need to
// escalate via systemd-run to chown /data/electrs to the container uid.
// The lightningos/electrs image declares VOLUME /data/db with the expected
// ownership, so Docker sets the perms correctly on the first mount.
func electrsComposeContents(_ electrsPaths, values electrsRuntimeValues) string {
  return fmt.Sprintf(`services:
  electrs:
    image: %s
    container_name: electrs
    restart: unless-stopped
    stop_grace_period: 1m
    networks:
      - default
      - bitcoincore
    ports:
      - "127.0.0.1:%d:%d"
      - "127.0.0.1:%d:%d"
    volumes:
      - %s:/data/db
      - ./bitcoin.cookie:/run/bitcoin.cookie:ro
    command:
      - --network=%s
      - --db-dir=/data/db
      - --daemon-rpc-addr=bitcoind:%d
      - --daemon-p2p-addr=bitcoind:%d
      - --electrum-rpc-addr=0.0.0.0:%d
      - --monitoring-addr=0.0.0.0:%d
      - --cookie-file=/run/bitcoin.cookie
      - --index-batch-size=10
      - --log-filters=INFO

networks:
  default:
    name: electrs_default
  bitcoincore:
    external: true
    name: bitcoincore_default

volumes:
  %s:
    name: %s
`,
    electrsImageName,
    electrsRPCPort, electrsRPCPort,
    electrsMonitorPort, electrsMonitorPort,
    electrsVolumeName,
    values.Network,
    values.BitcoinRPCPort,
    values.BitcoinP2PPort,
    electrsRPCPort,
    electrsMonitorPort,
    electrsVolumeName,
    electrsVolumeName,
  )
}

type electrsStatus struct {
  Installed   bool   `json:"installed"`
  Running     bool   `json:"running"`
  RPCPort     int    `json:"rpc_port"`
  IndexHeight int64  `json:"index_height"`
  TipHeight   int64  `json:"tip_height"`
  Indexing    bool   `json:"indexing"`
  Message     string `json:"message,omitempty"`
}

// fetchElectrsStatus scrapes electrs' Prometheus endpoint on 127.0.0.1:4224
// for the indexed tip (electrs_index_height{type="tip"}) and pulls the
// bitcoind chain tip from the local Bitcoin Core app (if installed) so the
// UI can show indexing progress.
func (s *Server) fetchElectrsStatus(ctx context.Context) electrsStatus {
  out := electrsStatus{RPCPort: electrsRPCPort}

  paths := electrsAppPaths()
  if !fileExists(paths.ComposePath) {
    out.Message = "not installed"
    return out
  }
  out.Installed = true

  composeStatus, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, "electrs")
  if err == nil && composeStatus == "running" {
    out.Running = true
  }
  if !out.Running {
    out.Message = "container not running"
    return out
  }

  indexHeight, scrapeErr := scrapeElectrsIndexHeight(ctx)
  if scrapeErr != nil {
    out.Message = fmt.Sprintf("metrics unreachable: %v", scrapeErr)
  } else {
    out.IndexHeight = indexHeight
  }

  bitcoinPaths := bitcoinCoreAppPaths()
  if fileExists(bitcoinPaths.ComposePath) {
    chainCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()
    info, err := fetchBitcoinLocalChainInfo(chainCtx, bitcoinPaths)
    if err == nil {
      out.TipHeight = info.Blocks
    }
  }

  if out.IndexHeight > 0 && out.TipHeight > 0 {
    out.Indexing = out.IndexHeight < out.TipHeight-1
  }
  return out
}

func scrapeElectrsIndexHeight(ctx context.Context) (int64, error) {
  reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
  defer cancel()
  req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/metrics", electrsMonitorPort), nil)
  if err != nil {
    return 0, err
  }
  resp, err := http.DefaultClient.Do(req)
  if err != nil {
    return 0, err
  }
  defer resp.Body.Close()
  if resp.StatusCode != http.StatusOK {
    _, _ = io.Copy(io.Discard, resp.Body)
    return 0, fmt.Errorf("metrics http %d", resp.StatusCode)
  }
  return parseElectrsIndexHeight(resp.Body)
}

// parseElectrsIndexHeight scans a Prometheus exposition stream for
// electrs_index_height{type="tip"} and returns its value. electrs also
// exposes type="committed" and type="best" — we only want "tip".
func parseElectrsIndexHeight(r io.Reader) (int64, error) {
  scanner := bufio.NewScanner(r)
  scanner.Buffer(make([]byte, 64*1024), 1024*1024)
  for scanner.Scan() {
    line := scanner.Text()
    if line == "" || line[0] == '#' {
      continue
    }
    if !strings.HasPrefix(line, "electrs_index_height") {
      continue
    }
    braceOpen := strings.IndexByte(line, '{')
    braceClose := strings.IndexByte(line, '}')
    if braceOpen < 0 || braceClose < 0 || braceClose < braceOpen {
      continue
    }
    labels := line[braceOpen+1 : braceClose]
    if !strings.Contains(labels, `type="tip"`) {
      continue
    }
    rest := strings.TrimSpace(line[braceClose+1:])
    if rest == "" {
      continue
    }
    fields := strings.Fields(rest)
    if len(fields) == 0 {
      continue
    }
    v, err := strconv.ParseFloat(fields[0], 64)
    if err != nil {
      return 0, err
    }
    return int64(v), nil
  }
  if err := scanner.Err(); err != nil {
    return 0, err
  }
  return 0, errors.New(`metric electrs_index_height{type="tip"} not found`)
}
