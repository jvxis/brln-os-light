package server

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const systemIntegrationsMarkerPath = "/var/lib/lightningos/system-integrations-20260807-v3"

//go:embed assets/lightningos-terminal.sh
var embeddedTerminalHelper string

//go:embed assets/lightningos-terminal-password.sh
var embeddedTerminalPasswordHelper string

//go:embed assets/lightningos-manager-firewall.sh
var embeddedManagerFirewallHelper string

//go:embed assets/setup-manager-tls-mdns.sh
var embeddedManagerTLSMDNSHelper string

//go:embed assets/reconcile-system-integrations.sh
var embeddedSystemIntegrationsReconciler string

func (s *Server) startSystemIntegrationReconciler() {
	go func() {
		select {
		case <-time.After(20 * time.Second):
		case <-s.shutdownContext().Done():
			return
		}

		if _, err := os.Stat(systemIntegrationsMarkerPath); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) && s.logger != nil {
			s.logger.Printf("system integrations marker check failed: %v", err)
		}

		ctx, cancel := context.WithTimeout(s.shutdownContext(), 5*time.Minute)
		defer cancel()
		if err := reconcileSystemIntegrations(ctx); err != nil {
			if s.logger != nil {
				s.logger.Printf("system integrations reconciliation failed: %v", err)
			}
			return
		}
		if s.logger != nil {
			s.logger.Printf("system integrations reconciled")
		}
	}()
}

func reconcileSystemIntegrations(ctx context.Context) error {
	assets := []struct {
		pattern string
		content string
	}{
		{pattern: "lightningos-terminal-*.sh", content: embeddedTerminalHelper},
		{pattern: "lightningos-terminal-password-*.sh", content: embeddedTerminalPasswordHelper},
		{pattern: "lightningos-manager-firewall-*.sh", content: embeddedManagerFirewallHelper},
		{pattern: "lightningos-manager-tls-mdns-*.sh", content: embeddedManagerTLSMDNSHelper},
		{pattern: "lightningos-reconcile-system-*.sh", content: embeddedSystemIntegrationsReconciler},
	}

	stageDir := "/var/lib/lightningos"
	if err := os.MkdirAll(stageDir, 0750); err != nil {
		return fmt.Errorf("create integration staging directory: %w", err)
	}

	paths := make([]string, 0, len(assets))
	defer func() {
		for _, path := range paths {
			_ = os.Remove(path)
		}
	}()
	for _, asset := range assets {
		if strings.TrimSpace(asset.content) == "" {
			return fmt.Errorf("embedded integration asset %s is empty", asset.pattern)
		}
		file, err := os.CreateTemp(stageDir, asset.pattern)
		if err != nil {
			return fmt.Errorf("stage integration asset %s: %w", asset.pattern, err)
		}
		path := file.Name()
		paths = append(paths, path)
		if _, err := file.WriteString(asset.content); err != nil {
			_ = file.Close()
			return fmt.Errorf("write integration asset %s: %w", asset.pattern, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close integration asset %s: %w", asset.pattern, err)
		}
		if err := os.Chmod(path, 0700); err != nil {
			return fmt.Errorf("chmod integration asset %s: %w", asset.pattern, err)
		}
	}
	if len(paths) != 5 {
		return errors.New("invalid staged system integration asset count")
	}
	out, err := runSystemd(ctx, "/bin/bash", paths[4], paths[0], paths[1], paths[2], paths[3], systemIntegrationsMarkerPath)
	if err != nil {
		if detail := strings.TrimSpace(out); detail != "" {
			return fmt.Errorf("root reconciliation failed: %w: %s", err, detail)
		}
		return fmt.Errorf("root reconciliation failed: %w", err)
	}
	return nil
}
