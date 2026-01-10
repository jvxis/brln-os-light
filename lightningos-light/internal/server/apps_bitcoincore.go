package server

import (
  "context"
  "errors"
  "fmt"
  "os"
  "path/filepath"
  "strings"

  "lightningos-light/internal/system"
)

type bitcoinCorePaths struct {
  Root string
  DataDir string
  ConfigPath string
  SeedConfigPath string
  ComposePath string
}

type bitcoinCoreApp struct {
  server *Server
}

const bitcoinCoreImage = "bitcoin/bitcoin:30.2"

func newBitcoinCoreApp(s *Server) appHandler {
  return bitcoinCoreApp{server: s}
}

func bitcoincoreDefinition() appDefinition {
  return appDefinition{
    ID: "bitcoincore",
    Name: "Bitcoin Core",
    Description: "Run a local Bitcoin Core node with Docker.",
    Port: 0,
  }
}

func (a bitcoinCoreApp) Definition() appDefinition {
  return bitcoincoreDefinition()
}

func (a bitcoinCoreApp) Info(ctx context.Context) (appInfo, error) {
  def := a.Definition()
  info := newAppInfo(def)
  paths := bitcoinCoreAppPaths()
  if !fileExists(paths.ComposePath) {
    return info, nil
  }
  info.Installed = true
  status, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, "bitcoind")
  if err != nil {
    info.Status = "unknown"
    return info, err
  }
  info.Status = status
  return info, nil
}

func (a bitcoinCoreApp) Install(ctx context.Context) error {
  return a.server.installBitcoinCore(ctx)
}

func (a bitcoinCoreApp) Uninstall(ctx context.Context) error {
  return a.server.uninstallBitcoinCore(ctx)
}

func (a bitcoinCoreApp) Start(ctx context.Context) error {
  return a.server.startBitcoinCore(ctx)
}

func (a bitcoinCoreApp) Stop(ctx context.Context) error {
  return a.server.stopBitcoinCore(ctx)
}

func bitcoinCoreAppPaths() bitcoinCorePaths {
  root := filepath.Join(appsRoot, "bitcoincore")
  dataDir := "/data/bitcoin"
  return bitcoinCorePaths{
    Root: root,
    DataDir: dataDir,
    ConfigPath: filepath.Join(dataDir, "bitcoin.conf"),
    SeedConfigPath: filepath.Join(root, "bitcoin.conf"),
    ComposePath: filepath.Join(root, "docker-compose.yaml"),
  }
}

func (s *Server) installBitcoinCore(ctx context.Context) error {
  if err := ensureDocker(ctx); err != nil {
    return err
  }
  paths := bitcoinCoreAppPaths()
  if err := os.MkdirAll(paths.Root, 0750); err != nil {
    return fmt.Errorf("failed to create app directory: %w", err)
  }
  if err := ensureBitcoinCoreSeedConfig(paths); err != nil {
    return err
  }
  if err := syncBitcoinCoreConfig(ctx, paths); err != nil {
    return err
  }
  if _, err := ensureFileWithChange(paths.ComposePath, bitcoinCoreComposeContents(paths)); err != nil {
    return err
  }
  return runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d")
}

func (s *Server) uninstallBitcoinCore(ctx context.Context) error {
  paths := bitcoinCoreAppPaths()
  if fileExists(paths.ComposePath) {
    _ = runCompose(ctx, paths.Root, paths.ComposePath, "down", "--remove-orphans")
  }
  if err := os.RemoveAll(paths.Root); err != nil {
    return fmt.Errorf("failed to remove app files: %w", err)
  }
  return nil
}

func (s *Server) startBitcoinCore(ctx context.Context) error {
  paths := bitcoinCoreAppPaths()
  if err := os.MkdirAll(paths.Root, 0750); err != nil {
    return fmt.Errorf("failed to create app directory: %w", err)
  }
  if err := ensureBitcoinCoreSeedConfig(paths); err != nil {
    return err
  }
  if err := syncBitcoinCoreConfig(ctx, paths); err != nil {
    return err
  }
  if _, err := ensureFileWithChange(paths.ComposePath, bitcoinCoreComposeContents(paths)); err != nil {
    return err
  }
  return runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d")
}

func (s *Server) stopBitcoinCore(ctx context.Context) error {
  paths := bitcoinCoreAppPaths()
  if !fileExists(paths.ComposePath) {
    return errors.New("Bitcoin Core is not installed")
  }
  return runCompose(ctx, paths.Root, paths.ComposePath, "stop")
}

func bitcoinCoreComposeContents(paths bitcoinCorePaths) string {
  return fmt.Sprintf(`services:
  bitcoind:
    image: %s
    user: "0:0"
    restart: unless-stopped
    ports:
      - "8333:8333"
      - "127.0.0.1:8332:8332"
      - "127.0.0.1:28332:28332"
      - "127.0.0.1:28333:28333"
    volumes:
      - %s:/home/bitcoin/.bitcoin
`, bitcoinCoreImage, paths.DataDir)
}

func ensureBitcoinCoreSeedConfig(paths bitcoinCorePaths) error {
  info, err := os.Stat(paths.SeedConfigPath)
  if err == nil {
    if info.IsDir() {
      return fmt.Errorf("%s is a directory", paths.SeedConfigPath)
    }
    return nil
  }
  if !os.IsNotExist(err) {
    return fmt.Errorf("failed to stat %s: %w", paths.SeedConfigPath, err)
  }
  content, err := defaultBitcoinCoreConfig()
  if err != nil {
    return err
  }
  return writeFile(paths.SeedConfigPath, content, 0640)
}

func defaultBitcoinCoreConfig() (string, error) {
  password, err := randomToken(32)
  if err != nil {
    return "", err
  }
  lines := []string{
    "server=1",
    "printtoconsole=1",
    "rpcuser=lightningos",
    "rpcpassword=" + password,
    "rpcbind=0.0.0.0:8332",
    "rpcallowip=127.0.0.1",
    "rpcallowip=172.17.0.0/16",
    "zmqpubrawblock=tcp://0.0.0.0:28332",
    "zmqpubrawtx=tcp://0.0.0.0:28333",
    "",
  }
  return strings.Join(lines, "\n"), nil
}

func syncBitcoinCoreConfig(ctx context.Context, paths bitcoinCorePaths) error {
  if !fileExists(paths.SeedConfigPath) {
    return fmt.Errorf("missing seed config %s", paths.SeedConfigPath)
  }
  cmd := "if [ ! -f /home/bitcoin/.bitcoin/bitcoin.conf ]; then cp /tmp/bitcoin.conf /home/bitcoin/.bitcoin/bitcoin.conf; chmod 640 /home/bitcoin/.bitcoin/bitcoin.conf; fi"
  _, err := system.RunCommandWithSudo(
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
    fmt.Sprintf("%s:/tmp/bitcoin.conf:ro", paths.SeedConfigPath),
    bitcoinCoreImage,
    "-c",
    cmd,
  )
  if err != nil {
    return fmt.Errorf("failed to seed bitcoin.conf: %w", err)
  }
  return nil
}
