package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"lightningos-light/internal/system"
)

const (
	tailscaleAppID   = "tailscale"
	tailscaleService = "tailscaled"
	tailscaleBin     = "/usr/bin/tailscale"
)

type tailscalePaths struct {
	Root        string
	DataDir     string
	AuthURLPath string
}

func tailscaleAppPaths() tailscalePaths {
	root := filepath.Join(appsRoot, tailscaleAppID)
	dataDir := filepath.Join(appsDataRoot, tailscaleAppID)
	return tailscalePaths{
		Root:        root,
		DataDir:     dataDir,
		AuthURLPath: filepath.Join(dataDir, "auth_url"),
	}
}

type tailscaleApp struct {
	server *Server
}

func newTailscaleApp(s *Server) appHandler {
	return tailscaleApp{server: s}
}

func (a tailscaleApp) Definition() appDefinition {
	return appDefinition{
		ID:          tailscaleAppID,
		Name:        "Tailscale",
		Description: "Tailscale VPN — secure mesh networking.",
		Port:        0,
	}
}

func (a tailscaleApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)
	if !fileExists(tailscaleBin) {
		return info, nil
	}
	info.Installed = true
	status, err := serviceActiveState(ctx, tailscaleService)
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (a tailscaleApp) Install(ctx context.Context) error {
	paths := tailscaleAppPaths()
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	if err := os.MkdirAll(paths.DataDir, 0750); err != nil {
		return fmt.Errorf("failed to create app data directory: %w", err)
	}
	script := "curl -fsSL https://tailscale.com/install.sh | sh"
	if _, err := runSystemd(ctx, "/bin/sh", "-c", script); err != nil {
		return fmt.Errorf("tailscale install script failed: %w", err)
	}
	if _, err := runSystemd(ctx, "systemctl", "enable", tailscaleService); err != nil {
		return fmt.Errorf("failed to enable tailscaled: %w", err)
	}
	if err := ensureTailscaleUfwAccess(ctx); err != nil && a.server.logger != nil {
		a.server.logger.Printf("tailscale: ufw rule failed: %v", err)
	}
	return nil
}

func (a tailscaleApp) Start(ctx context.Context) error {
	if _, err := runSystemd(ctx, "systemctl", "start", tailscaleService); err != nil {
		return fmt.Errorf("failed to start tailscaled: %w", err)
	}
	paths := tailscaleAppPaths()
	out, _ := runSystemd(ctx, "/bin/sh", "-c", "timeout 10 tailscale up --accept-routes 2>&1")
	if url := extractTailscaleAuthURL(out); url != "" {
		_ = writeFile(paths.AuthURLPath, url, 0640)
	}
	return nil
}

func (a tailscaleApp) Stop(ctx context.Context) error {
	_, _ = runSystemd(ctx, tailscaleBin, "down")
	if _, err := runSystemd(ctx, "systemctl", "stop", tailscaleService); err != nil {
		return fmt.Errorf("failed to stop tailscaled: %w", err)
	}
	return nil
}

func (a tailscaleApp) Uninstall(ctx context.Context) error {
	_, _ = runSystemd(ctx, tailscaleBin, "down")
	_, _ = runSystemd(ctx, "systemctl", "disable", "--now", tailscaleService)
	if _, err := runSystemd(ctx, "/bin/sh", "-c", "apt-get remove -y tailscale"); err != nil {
		return fmt.Errorf("failed to remove tailscale package: %w", err)
	}
	_ = os.Remove("/etc/apt/sources.list.d/tailscale.list")
	paths := tailscaleAppPaths()
	_ = os.RemoveAll(paths.DataDir)
	return nil
}

func extractTailscaleAuthURL(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "https://login.tailscale.com/") {
			return trimmed
		}
	}
	return ""
}

func ensureTailscaleUfwAccess(ctx context.Context) error {
	statusOut, err := system.RunCommandWithSudo(ctx, "ufw", "status")
	if err != nil || !strings.Contains(strings.ToLower(statusOut), "status: active") {
		return nil
	}
	_, err = system.RunCommandWithSudo(ctx, "ufw", "allow", "41641/udp")
	return err
}

// handleTailscaleAuthURL returns the Tailscale auth URL (or fetches a fresh one).
func (s *Server) handleTailscaleAuthURL(w http.ResponseWriter, r *http.Request) {
	paths := tailscaleAppPaths()
	url := readSecretFile(paths.AuthURLPath)
	if url == "" {
		out, _ := runSystemd(r.Context(), "/bin/sh", "-c", "timeout 10 tailscale up --accept-routes 2>&1")
		url = extractTailscaleAuthURL(out)
		if url != "" {
			_ = writeFile(paths.AuthURLPath, url, 0640)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// handleTailscaleStatus returns the output of `tailscale status --json`.
func (s *Server) handleTailscaleStatus(w http.ResponseWriter, r *http.Request) {
	out, err := runSystemd(r.Context(), tailscaleBin, "status", "--json")
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("tailscale status failed: %v", err))
		return
	}
	var raw json.RawMessage
	if jsonErr := json.Unmarshal([]byte(out), &raw); jsonErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to parse tailscale status")
		return
	}
	writeJSON(w, http.StatusOK, raw)
}

// handleTailscaleLogout runs `tailscale logout` and clears the stored auth URL.
func (s *Server) handleTailscaleLogout(w http.ResponseWriter, r *http.Request) {
	if _, err := runSystemd(r.Context(), tailscaleBin, "logout"); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("tailscale logout failed: %v", err))
		return
	}
	paths := tailscaleAppPaths()
	_ = os.Remove(paths.AuthURLPath)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
