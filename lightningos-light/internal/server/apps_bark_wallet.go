package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/system"
)

const (
	barkWalletAppID = appmanifest.BarkWalletID
	barkWalletPort  = appmanifest.BarkWalletPort
)

type barkWalletPaths struct {
	Root              string
	AdminPasswordPath string
}

type barkWalletApp struct{ server *Server }

func newBarkWalletApp(s *Server) appHandler { return barkWalletApp{server: s} }

func barkWalletDefinition() appDefinition {
	return appDefinition{
		ID:          barkWalletAppID,
		Name:        "Bark Wallet",
		Description: "Self-custodial Ark, Lightning, and on-chain wallet powered by Second's public Ark server (beta; does not use local LND).",
		Port:        barkWalletPort,
	}
}

func (a barkWalletApp) Definition() appDefinition { return barkWalletDefinition() }

func (a barkWalletApp) Info(ctx context.Context) (appInfo, error) {
	info := newAppInfo(a.Definition())
	info.Scheme = "https"
	handled, state, err := system.BarkWalletStatusWithBroker(ctx)
	if !handled {
		return info, errors.New("Bark Wallet status requires privileged broker enforce mode")
	}
	info.Installed, info.Status, info.UFWActive = state.Installed, state.Status, state.UFWActive
	if state.PasswordAvailable {
		info.AdminPasswordPath = barkWalletAppPaths().AdminPasswordPath
	}
	return info, err
}

func (a barkWalletApp) Install(ctx context.Context) error   { return a.server.installBarkWallet(ctx) }
func (a barkWalletApp) Uninstall(ctx context.Context) error { return a.server.uninstallBarkWallet(ctx) }
func (a barkWalletApp) Start(ctx context.Context) error     { return a.server.startBarkWallet(ctx) }
func (a barkWalletApp) Stop(ctx context.Context) error      { return a.server.stopBarkWallet(ctx) }

func barkWalletAppPaths() barkWalletPaths {
	return barkWalletPaths{
		Root:              filepath.Join(appsRoot, barkWalletAppID),
		AdminPasswordPath: filepath.Join(appsDataRoot, barkWalletAppID, "auth", "ui_password"),
	}
}

func (s *Server) installBarkWallet(ctx context.Context) error {
	return s.prepareAndStartBarkWallet(ctx)
}
func (s *Server) startBarkWallet(ctx context.Context) error { return s.prepareAndStartBarkWallet(ctx) }

func (s *Server) prepareAndStartBarkWallet(ctx context.Context) error {
	if err := ensureDockerForCatalogAppEnforce(ctx); err != nil {
		return err
	}
	if handled, err := system.EnsureBarkWalletWithBroker(ctx); !handled {
		return errors.New("Bark Wallet preparation requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("Bark Wallet preparation failed: %w", err)
	}
	for _, variant := range appmanifest.BarkWalletImageVariants() {
		if handled, err := system.PrepareAppImageWithBroker(ctx, appmanifest.BarkWalletID, string(variant)); !handled {
			return errors.New("Bark Wallet image preparation requires privileged broker enforce mode")
		} else if err != nil {
			return fmt.Errorf("Bark Wallet image unavailable: %w", err)
		}
		if handled, runnable, err := system.ProbeAppImageWithBroker(ctx, appmanifest.BarkWalletID, string(variant)); !handled {
			return errors.New("Bark Wallet image verification requires privileged broker enforce mode")
		} else if err != nil || !runnable {
			return errors.New("official Bark Wallet image verification failed")
		}
	}
	if handled, err := system.BarkWalletLifecycleWithBroker(ctx, "start"); !handled {
		return errors.New("Bark Wallet lifecycle requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("Bark Wallet start failed: %w", err)
	}
	if handled, err := system.EnsureBarkWalletFirewallWithBroker(ctx); !handled {
		return errors.New("Bark Wallet firewall requires privileged broker enforce mode")
	} else if err != nil && s.logger != nil {
		s.logger.Printf("bark-wallet: firewall rule failed: %v", err)
	}
	return nil
}

func (s *Server) uninstallBarkWallet(ctx context.Context) error {
	if handled, err := system.RemoveBarkWalletWithBroker(ctx); !handled {
		return errors.New("Bark Wallet removal requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("Bark Wallet removal failed: %w", err)
	}
	// Only the legacy manager-owned execution directory is removed. The broker
	// preserves wallet state and authentication material under apps-data.
	if err := os.RemoveAll(barkWalletAppPaths().Root); err != nil {
		return fmt.Errorf("failed to remove legacy Bark Wallet app files: %w", err)
	}
	return nil
}

func (s *Server) stopBarkWallet(ctx context.Context) error {
	handled, state, err := system.BarkWalletStatusWithBroker(ctx)
	if !handled {
		return errors.New("Bark Wallet status requires privileged broker enforce mode")
	}
	if err != nil {
		return err
	}
	if !state.Installed {
		return errors.New("Bark Wallet is not installed")
	}
	// A legacy installation may be running before its first 0.5.3 start. Enroll
	// its declaration and permissions without recreating containers so stop
	// remains available immediately after an in-place upgrade.
	if handled, err := system.EnsureBarkWalletWithBroker(ctx); !handled {
		return errors.New("Bark Wallet preparation requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("Bark Wallet preparation failed: %w", err)
	}
	if handled, err := system.BarkWalletLifecycleWithBroker(ctx, "stop"); !handled {
		return errors.New("Bark Wallet lifecycle requires privileged broker enforce mode")
	} else {
		return err
	}
}

func (s *Server) resetBarkWalletAdminPassword(ctx context.Context) error {
	if handled, err := system.ResetBarkWalletPasswordWithBroker(ctx); !handled {
		return errors.New("Bark Wallet password reset requires privileged broker enforce mode")
	} else if err == nil {
		return nil
	}
	// A legacy install has no broker snapshot yet. Promote its fixed assets and
	// retry once; existing wallet/auth contents remain unchanged by Ensure.
	if handled, err := system.EnsureBarkWalletWithBroker(ctx); !handled {
		return errors.New("Bark Wallet preparation requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("Bark Wallet preparation failed: %w", err)
	}
	if handled, err := system.ResetBarkWalletPasswordWithBroker(ctx); !handled {
		return errors.New("Bark Wallet password reset requires privileged broker enforce mode")
	} else {
		return err
	}
}
