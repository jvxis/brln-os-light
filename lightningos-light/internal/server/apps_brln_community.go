package server

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/system"
)

const (
	brlnCommunityAppID = appmanifest.BRLNCommunityID
	brlnCommunityPort  = appmanifest.BRLNCommunityPort
)

type brlnCommunityApp struct{ server *Server }

func newBRLNCommunityApp(s *Server) appHandler { return brlnCommunityApp{server: s} }

func brlnCommunityDefinition() appDefinition {
	return appDefinition{
		ID:          brlnCommunityAppID,
		Name:        "BR⚡LN Community",
		Description: "Test build of the BR⚡LN Club members chat, with your Nostr signer on this node. Chat data may be reset before launch.",
		Port:        brlnCommunityPort,
	}
}

func (a brlnCommunityApp) Definition() appDefinition { return brlnCommunityDefinition() }

func (a brlnCommunityApp) Info(ctx context.Context) (appInfo, error) {
	info := newAppInfo(a.Definition())
	info.Scheme = "https"
	handled, state, err := system.BRLNCommunityStatusWithBroker(ctx)
	if !handled {
		return info, errors.New("BR⚡LN Community status requires privileged broker enforce mode")
	}
	info.Installed, info.Status, info.UFWActive = state.Installed, state.Status, state.UFWActive
	if state.PasswordAvailable {
		info.AdminPasswordPath = brlnCommunityAccessPasswordPath()
	}
	return info, err
}

// brlnCommunityAccessPasswordPath is shown in the App Store; the manager never
// reads it and asks the broker for the value instead.
func brlnCommunityAccessPasswordPath() string {
	return filepath.Join(appsDataRoot, brlnCommunityAppID, "auth", "access_password")
}

func (a brlnCommunityApp) Install(ctx context.Context) error {
	return a.server.prepareAndStartBRLNCommunity(ctx)
}
func (a brlnCommunityApp) Uninstall(ctx context.Context) error {
	return a.server.uninstallBRLNCommunity(ctx)
}
func (a brlnCommunityApp) Start(ctx context.Context) error {
	return a.server.prepareAndStartBRLNCommunity(ctx)
}
func (a brlnCommunityApp) Stop(ctx context.Context) error { return a.server.stopBRLNCommunity(ctx) }

func (s *Server) prepareAndStartBRLNCommunity(ctx context.Context) error {
	if err := ensureDockerForCatalogAppEnforce(ctx); err != nil {
		return err
	}
	if handled, err := system.EnsureBRLNCommunityWithBroker(ctx); !handled {
		return errors.New("BR⚡LN Community preparation requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("BR⚡LN Community preparation failed: %w", err)
	}
	for _, variant := range appmanifest.BRLNCommunityImageVariants() {
		if handled, err := system.PrepareAppImageWithBroker(ctx, appmanifest.BRLNCommunityID, string(variant)); !handled {
			return errors.New("BR⚡LN Community image preparation requires privileged broker enforce mode")
		} else if err != nil {
			return fmt.Errorf("BR⚡LN Community image unavailable: %w", err)
		}
		if handled, runnable, err := system.ProbeAppImageWithBroker(ctx, appmanifest.BRLNCommunityID, string(variant)); !handled {
			return errors.New("BR⚡LN Community image verification requires privileged broker enforce mode")
		} else if err != nil || !runnable {
			return errors.New("BR⚡LN Community image verification failed")
		}
	}
	if handled, err := system.BRLNCommunityLifecycleWithBroker(ctx, "start"); !handled {
		return errors.New("BR⚡LN Community lifecycle requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("BR⚡LN Community start failed: %w", err)
	}
	if handled, err := system.EnsureBRLNCommunityFirewallWithBroker(ctx); !handled {
		return errors.New("BR⚡LN Community firewall requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("BR⚡LN Community firewall failed: %w", err)
	}
	return nil
}

// uninstallBRLNCommunity removes containers and the broker snapshot. The broker
// preserves the signer data and local secrets, so reinstalling keeps the npub.
func (s *Server) uninstallBRLNCommunity(ctx context.Context) error {
	if handled, err := system.RemoveBRLNCommunityWithBroker(ctx); !handled {
		return errors.New("BR⚡LN Community removal requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("BR⚡LN Community removal failed: %w", err)
	}
	return nil
}

func (s *Server) stopBRLNCommunity(ctx context.Context) error {
	if handled, err := system.BRLNCommunityLifecycleWithBroker(ctx, "stop"); !handled {
		return errors.New("BR⚡LN Community lifecycle requires privileged broker enforce mode")
	} else {
		return err
	}
}
