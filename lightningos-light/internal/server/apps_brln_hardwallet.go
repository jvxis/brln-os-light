package server

import (
	"context"
	"errors"
	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/system"
	"net/http"
)

type hardwalletApp struct{ server *Server }

func newHardwalletApp(s *Server) appHandler { return hardwalletApp{s} }
func (a hardwalletApp) Definition() appDefinition {
	return appDefinition{ID: appmanifest.HardwalletID, Name: "BR⚡LN Hardwallet", Description: "Experimental Bitcoin coordinator for an ESP32 signer. Device-only secrets, local Electrs or the public club Electrum endpoint. Trusted HTTPS on your own node is required. No real funds until hardware validation.", Port: appmanifest.HardwalletPort}
}
func (a hardwalletApp) Info(ctx context.Context) (appInfo, error) {
	info := newAppInfo(a.Definition())
	info.Scheme = "https"
	state, err := system.HardwalletStatusWithBroker(ctx)
	info.ElectrumSource = state.Source
	info.Installed = state.Installed
	info.Status = state.Status
	info.ExternalURL = hardwalletLaunchURL(state)
	if !a.server.auth.Enabled() {
		info.Available = false
		info.UnavailableMessage = "Enable LightningOS login before using Hardwallet"
	}
	return info, err
}

// Keep the Manager's current host and session, whether accessed over LAN or VPN.
func hardwalletLaunchURL(state system.HardwalletBrokerState) string {
	if !state.Installed || appmanifest.ValidateHardwalletSettings(appmanifest.HardwalletSettings{Source: state.Source, BindIPs: state.BindIPs}) != nil {
		return ""
	}
	return "/#hardwallet-open"
}
func (a hardwalletApp) Install(ctx context.Context) error { return a.server.prepareHardwallet(ctx, "") }
func (a hardwalletApp) Start(ctx context.Context) error   { return a.server.prepareHardwallet(ctx, "") }
func (a hardwalletApp) Stop(ctx context.Context) error {
	return system.HardwalletControlWithBroker(ctx, "stop", "")
}
func (a hardwalletApp) Uninstall(ctx context.Context) error {
	return system.HardwalletControlWithBroker(ctx, "remove", "")
}
func (s *Server) prepareHardwallet(ctx context.Context, source string) error {
	if !s.auth.Enabled() {
		return errors.New("enable LightningOS login before installing Hardwallet")
	}
	if source != "" && source != "local" && source != "club" {
		return errors.New("invalid Electrum source")
	}
	if err := ensureDockerForCatalogAppEnforce(ctx); err != nil {
		return err
	}
	if err := system.HardwalletControlWithBroker(ctx, "ensure", source); err != nil {
		return err
	}
	for _, variant := range []string{"app", "proxy"} {
		handled, err := system.PrepareAppImageWithBroker(ctx, appmanifest.HardwalletID, variant)
		if !handled {
			return errors.New("Hardwallet image requires broker enforce mode")
		}
		if err != nil {
			return err
		}
		handled, runnable, err := system.ProbeAppImageWithBroker(ctx, appmanifest.HardwalletID, variant)
		if !handled || err != nil || !runnable {
			return errors.New("Hardwallet image verification failed")
		}
	}
	if err := system.HardwalletControlWithBroker(ctx, "start", ""); err != nil {
		return err
	}
	return system.HardwalletControlWithBroker(ctx, "firewall", "")
}

// Every app route, including static assets, passes this check. Login-disabled
// LOS installations do not turn this application into an unauthenticated app.
func (s *Server) handleHardwalletAuthorization(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	_, ok := authSessionFromContext(r.Context())
	if !s.auth.Enabled() || !ok {
		writeError(w, http.StatusUnauthorized, "LightningOS session required")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
