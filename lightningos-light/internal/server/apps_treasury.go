package server

import (
	"context"
	"errors"
	"os"
	"runtime"
)

const (
	treasuryAppID        = "stablecoin-treasury"
	treasuryBridgeSocket = "/run/lightningos-treasury/bridge.sock"
)

type treasuryApp struct{ server *Server }

func (a treasuryApp) Definition() appDefinition {
	return appDefinition{
		ID: treasuryAppID, Name: "Stablecoin Treasury",
		Description: "Convert between your Lightning balance and native USDC on Base.",
	}
}

func treasuryBridgeAvailable() bool {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return false
	}
	info, err := os.Lstat(treasuryBridgeSocket)
	return err == nil && info.Mode()&os.ModeSocket != 0 && info.Mode().Perm()&0007 == 0
}

func (a treasuryApp) Info(ctx context.Context) (appInfo, error) {
	info := newAppInfo(a.Definition())
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		info.Available = false
		info.UnavailableMessage = "Stablecoin Treasury requires Linux amd64."
		return info, nil
	}
	if a.server.db == nil {
		info.Available = false
		info.UnavailableMessage = "PostgreSQL is unavailable."
		return info, nil
	}
	var installed, enabled bool
	err := a.server.db.QueryRow(ctx, `SELECT installed, enabled
		FROM stablecoin_treasury_config WHERE singleton`).Scan(&installed, &enabled)
	if err != nil && !treasuryStateNotCreated(err) {
		return info, errors.New("could not read treasury state")
	}
	info.Installed = installed
	if installed {
		info.Status = "stopped"
		if enabled {
			info.Status = "running"
		}
	}
	switch {
	case !treasuryBridgeAvailable():
		info.Available = false
		info.UnavailableMessage = "The isolated Breez bridge is unavailable."
	case a.server.auth == nil || !a.server.auth.Enabled():
		info.Available = false
		info.UnavailableMessage = "Enable login protection to use Stablecoin Treasury."
	}
	return info, nil
}

func (a treasuryApp) Install(ctx context.Context) error   { return a.setMode(ctx, "install") }
func (a treasuryApp) Start(ctx context.Context) error     { return a.setMode(ctx, "start") }
func (a treasuryApp) Stop(ctx context.Context) error      { return a.setMode(ctx, "stop") }
func (a treasuryApp) Uninstall(ctx context.Context) error { return a.setMode(ctx, "uninstall") }

func (a treasuryApp) setMode(ctx context.Context, mode string) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return errors.New("stablecoin treasury requires Linux amd64")
	}
	if a.server.auth == nil || !a.server.auth.Enabled() {
		return errors.New("enable login protection to use stablecoin treasury")
	}
	if (mode == "install" || mode == "start") && !treasuryBridgeAvailable() {
		return errors.New("isolated Breez bridge unavailable")
	}
	if err := a.server.ensureTreasuryState(ctx); err != nil {
		return err
	}
	return a.server.setTreasuryMode(ctx, mode)
}
