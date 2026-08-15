package server

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"time"

	"lightningos-light/internal/system"
)

const systemIntegrationsMarkerPath = "/var/lib/lightningos-privileged/system-integrations-20260815-v7"

//go:embed assets/lightningos-terminal.sh
var embeddedTerminalHelper string

//go:embed assets/lightningos-manager-firewall.sh
var embeddedManagerFirewallHelper string

//go:embed assets/setup-manager-tls-mdns.sh
var embeddedManagerTLSMDNSHelper string

func (s *Server) startSystemIntegrationReconciler() {
	go func() {
		select {
		case <-time.After(20 * time.Second):
		case <-s.shutdownContext().Done():
			return
		}

		statusCtx, statusCancel := context.WithTimeout(s.shutdownContext(), 5*time.Second)
		ready, handled, err := system.SystemIntegrationsReadyWithBroker(statusCtx)
		statusCancel()
		if handled && err == nil && ready {
			return
		}
		if err != nil && s.logger != nil {
			s.logger.Printf("system integrations marker check failed: %v", err)
		}

		ctx, cancel := context.WithTimeout(s.shutdownContext(), 20*time.Minute)
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
	if handled, err := system.EnsurePackageFeatureWithBroker(ctx, "mdns"); !handled {
		return errors.New("system integrations require privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("prepare mDNS package feature: %w", err)
	}

	assets := []system.SystemIntegrationAsset{
		{Name: "terminal", Content: embeddedTerminalHelper},
		{Name: "manager_firewall", Content: embeddedManagerFirewallHelper},
		{Name: "manager_tls_mdns", Content: embeddedManagerTLSMDNSHelper},
	}
	for _, asset := range assets {
		if asset.Content == "" {
			return fmt.Errorf("embedded system integration asset %s is empty", asset.Name)
		}
	}
	handled, terminalChanged, certificateChanged, err := system.ReconcileSystemIntegrationsWithBroker(ctx, assets)
	if !handled {
		return errors.New("system integrations require privileged broker enforce mode")
	}
	if err != nil {
		return fmt.Errorf("apply system integrations: %w", err)
	}

	if err := reconcileLegacyBitcoinStorageEnrollment(ctx); err != nil {
		return err
	}
	if terminalChanged {
		status, handled, err := system.ServiceStatusWithBroker(ctx, "lightningos-terminal")
		if !handled {
			return errors.New("terminal status requires privileged broker enforce mode")
		}
		if err != nil {
			return fmt.Errorf("read terminal service status: %w", err)
		}
		if status == "active" {
			if err := system.RestartServiceWithBroker(ctx, "lightningos-terminal", false); err != nil {
				return fmt.Errorf("restart terminal service: %w", err)
			}
		}
	}
	if handled, err := system.FinalizeSystemIntegrationsWithBroker(ctx); !handled {
		return errors.New("system integrations finalization requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("finalize system integrations: %w", err)
	}
	if certificateChanged {
		if err := system.RestartServiceWithBroker(ctx, "lightningos-manager", true); err != nil {
			return fmt.Errorf("schedule manager restart after certificate update: %w", err)
		}
	}
	return nil
}

func reconcileLegacyBitcoinStorageEnrollment(ctx context.Context) error {
	composePath := bitcoinCoreAppPaths().ComposePath
	composeInfo, err := os.Lstat(composePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect legacy Bitcoin Core declaration: %w", err)
	}
	if composeInfo.Mode()&os.ModeSymlink != 0 || !composeInfo.Mode().IsRegular() {
		return errors.New("refusing unsafe legacy Bitcoin Core declaration")
	}

	dataDir := bitcoinCoreDefaultDataDir
	statePath := bitcoinCoreDataDirStatePath()
	stateInfo, err := os.Lstat(statePath)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return fmt.Errorf("inspect legacy Bitcoin Core data-dir state: %w", err)
	case stateInfo.Mode()&os.ModeSymlink != 0 || !stateInfo.Mode().IsRegular() || stateInfo.Size() > 4096:
		return errors.New("refusing unsafe legacy Bitcoin Core data-dir state")
	default:
		raw, readErr := os.ReadFile(statePath)
		if readErr != nil {
			return fmt.Errorf("read legacy Bitcoin Core data-dir state: %w", readErr)
		}
		dataDir, readErr = normalizeBitcoinCoreDataDir(string(raw))
		if readErr != nil {
			return errors.New("legacy Bitcoin Core data-dir state is invalid")
		}
	}
	if handled, err := system.EnsureBitcoinCoreStorageWithBroker(ctx, dataDir); !handled {
		return errors.New("legacy Bitcoin Core storage enrollment requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("enroll legacy Bitcoin Core storage without restart: %w", err)
	}
	return nil
}
