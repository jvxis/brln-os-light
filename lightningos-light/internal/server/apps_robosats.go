package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lightningos-light/internal/system"
)

type robosatsPaths struct {
	Root        string
	DataDir     string
	ComposePath string
}

type robosatsApp struct {
	server *Server
}

const robosatsImage = "recksato/robosats-client:latest"

func newRobosatsApp(s *Server) appHandler {
	return robosatsApp{server: s}
}

func robosatsDefinition() appDefinition {
	return appDefinition{
		ID:          "robosats",
		Name:        "RoboSats Gateway",
		Description: "Self-hosted RoboSats client for P2P Bitcoin trading over Tor.",
		Port:        12596,
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
		Root:        root,
		DataDir:     dataDir,
		ComposePath: filepath.Join(root, "docker-compose.yaml"),
	}
}

func (s *Server) installRobosats(ctx context.Context) error {
	if err := ensureDocker(ctx); err != nil {
		return err
	}
	if err := ensureTorProxy(ctx); err != nil {
		return err
	}
	paths := robosatsAppPaths()
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	if err := os.MkdirAll(paths.DataDir, 0750); err != nil {
		return fmt.Errorf("failed to create app data directory: %w", err)
	}
	if err := ensureRobosatsImage(ctx); err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, robosatsComposeContents(paths)); err != nil {
		return err
	}
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
		return err
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
	if err := ensureTorProxy(ctx); err != nil {
		return err
	}
	paths := robosatsAppPaths()
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	if err := os.MkdirAll(paths.DataDir, 0750); err != nil {
		return fmt.Errorf("failed to create app data directory: %w", err)
	}
	if err := ensureRobosatsImage(ctx); err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, robosatsComposeContents(paths)); err != nil {
		return err
	}
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
		return err
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
  robosats:
    image: %s
    user: "0:0"
    restart: unless-stopped
    ports:
      - "12596:12596"
    environment:
      TOR_PROXY_IP: host.docker.internal
      TOR_PROXY_PORT: 9050
    volumes:
      - %s:/usr/src/robosats/data
    extra_hosts:
      - "host.docker.internal:host-gateway"
`, robosatsImage, paths.DataDir)
}

func ensureRobosatsImage(ctx context.Context) error {
	if _, err := system.RunCommandWithSudo(ctx, "docker", "image", "inspect", robosatsImage); err == nil {
		return nil
	}
	out, err := system.RunCommandWithSudo(ctx, "docker", "pull", robosatsImage)
	if err != nil {
		msg := strings.TrimSpace(out)
		if msg == "" {
			return fmt.Errorf("failed to pull %s: %w", robosatsImage, err)
		}
		return fmt.Errorf("failed to pull %s: %s", robosatsImage, msg)
	}
	return nil
}

func ensureTorProxy(ctx context.Context) error {
	dialer := net.Dialer{
		Timeout: 5 * time.Second,
	}
	conn, err := dialer.DialContext(ctx, "tcp", "127.0.0.1:9050")
	if err != nil {
		return errors.New("Tor proxy not accessible at 127.0.0.1:9050. Please ensure Tor is running (tor@default.service)")
	}
	conn.Close()
	return nil
}
