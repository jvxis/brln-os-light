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

const (
  robosatsImage = "recksato/robosats-client:latest"
  robosatsTorImage = "osminogin/tor-simple:0.4.9.5"
  robosatsPort = 12596
)

type robosatsPaths struct {
  Root string
  DataDir string
  ComposePath string
}

type robosatsApp struct {
  server *Server
}

func newRobosatsApp(s *Server) appHandler {
  return robosatsApp{server: s}
}

func robosatsDefinition() appDefinition {
  return appDefinition{
    ID: "robosats",
    Name: "RoboSats Gateway",
    Description: "Self-hosted RoboSats client for P2P Bitcoin trading over Tor.",
    Port: robosatsPort,
  }
}

func (a robosatsApp) Definition() appDefinition {
  return robosatsDefinition()
}

func (a robosatsApp) Info(ctx context.Context) (appInfo, error) {
  def := a.Definition()
  info := newAppInfo(def)
  paths := robosatsAppPaths()
  if !fileExists(paths.ComposePath) {
    return info, nil
  }
  info.Installed = true
  status, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, "robosats")
  if err != nil {
    info.Status = "unknown"
    return info, err
  }
  info.Status = status
  return info, nil
}

func (a robosatsApp) Install(ctx context.Context) error {
  return a.server.installRobosats(ctx)
}

func (a robosatsApp) Uninstall(ctx context.Context) error {
  return a.server.uninstallRobosats(ctx)
}

func (a robosatsApp) Start(ctx context.Context) error {
  return a.server.startRobosats(ctx)
}

func (a robosatsApp) Stop(ctx context.Context) error {
  return a.server.stopRobosats(ctx)
}

func robosatsAppPaths() robosatsPaths {
  root := filepath.Join(appsRoot, "robosats")
  dataDir := filepath.Join(appsDataRoot, "robosats")
  return robosatsPaths{
    Root: root,
    DataDir: dataDir,
    ComposePath: filepath.Join(root, "docker-compose.yaml"),
  }
}

func (s *Server) installRobosats(ctx context.Context) error {
  if err := ensureDocker(ctx); err != nil {
    return err
  }
  paths := robosatsAppPaths()
  if err := os.MkdirAll(paths.Root, 0750); err != nil {
    return fmt.Errorf("failed to create app directory: %w", err)
  }
  if err := os.MkdirAll(paths.DataDir, 0750); err != nil {
    return fmt.Errorf("failed to create app data directory: %w", err)
  }
  if err := ensureRobosatsImages(ctx); err != nil {
    return err
  }
  if _, err := ensureFileWithChange(paths.ComposePath, robosatsComposeContents(paths)); err != nil {
    return err
  }
  if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
    return err
  }
  if err := ensureRobosatsUfwAccess(ctx); err != nil && s.logger != nil {
    s.logger.Printf("robosats: ufw rule failed: %v", err)
  }
  return nil
}

func (s *Server) uninstallRobosats(ctx context.Context) error {
  paths := robosatsAppPaths()
  if fileExists(paths.ComposePath) {
    _ = runCompose(ctx, paths.Root, paths.ComposePath, "down", "--remove-orphans")
  }
  if err := os.RemoveAll(paths.Root); err != nil {
    return fmt.Errorf("failed to remove app files: %w", err)
  }
  return nil
}

func (s *Server) startRobosats(ctx context.Context) error {
  paths := robosatsAppPaths()
  if err := os.MkdirAll(paths.Root, 0750); err != nil {
    return fmt.Errorf("failed to create app directory: %w", err)
  }
  if err := os.MkdirAll(paths.DataDir, 0750); err != nil {
    return fmt.Errorf("failed to create app data directory: %w", err)
  }
  if err := ensureRobosatsImages(ctx); err != nil {
    return err
  }
  if _, err := ensureFileWithChange(paths.ComposePath, robosatsComposeContents(paths)); err != nil {
    return err
  }
  if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
    return err
  }
  if err := ensureRobosatsUfwAccess(ctx); err != nil && s.logger != nil {
    s.logger.Printf("robosats: ufw rule failed: %v", err)
  }
  return nil
}

func (s *Server) stopRobosats(ctx context.Context) error {
  paths := robosatsAppPaths()
  if !fileExists(paths.ComposePath) {
    return errors.New("RoboSats is not installed")
  }
  return runCompose(ctx, paths.Root, paths.ComposePath, "stop")
}

func robosatsComposeContents(paths robosatsPaths) string {
  return fmt.Sprintf(`services:
  tor:
    image: %s
    restart: unless-stopped
    volumes:
      - tor-data:/var/lib/tor
      - tor-log:/var/log/tor
  robosats:
    image: %s
    user: "0:0"
    restart: unless-stopped
    depends_on:
      - tor
    ports:
      - "%d:%d"
    environment:
      TOR_PROXY_IP: tor
      TOR_PROXY_PORT: 9050
    volumes:
      - %s:/usr/src/robosats/data
volumes:
  tor-data:
  tor-log:
`, robosatsTorImage, robosatsImage, robosatsPort, robosatsPort, paths.DataDir)
}

func ensureRobosatsImages(ctx context.Context) error {
  if err := ensureDockerImage(ctx, robosatsImage); err != nil {
    return err
  }
  if err := ensureDockerImage(ctx, robosatsTorImage); err != nil {
    return err
  }
  return nil
}

func ensureDockerImage(ctx context.Context, image string) error {
  if _, err := system.RunCommandWithSudo(ctx, "docker", "image", "inspect", image); err == nil {
    return nil
  }
  out, err := system.RunCommandWithSudo(ctx, "docker", "pull", image)
  if err != nil {
    msg := strings.TrimSpace(out)
    if msg == "" {
      return fmt.Errorf("failed to pull %s: %w", image, err)
    }
    return fmt.Errorf("failed to pull %s: %s", image, msg)
  }
  return nil
}

func ensureRobosatsUfwAccess(ctx context.Context) error {
  statusOut, err := system.RunCommandWithSudo(ctx, "ufw", "status")
  if err != nil || !strings.Contains(strings.ToLower(statusOut), "status: active") {
    return nil
  }
  _, err = system.RunCommandWithSudo(ctx, "ufw", "allow", fmt.Sprintf("%d/tcp", robosatsPort))
  return err
}
