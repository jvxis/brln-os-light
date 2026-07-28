package server

import (
	"context"
	"errors"
	"strings"
)

const magmaAppID = "magma-sales"

type magmaApp struct{ server *Server }

func newMagmaApp(s *Server) appHandler { return magmaApp{server: s} }

func magmaDefinition() appDefinition {
	return appDefinition{
		ID:   magmaAppID,
		Name: "Magma Inbound Sales",
		Description: "Monitors your Amboss Magma sell orders and alerts when a buyer is waiting on you. " +
			"Read-only: it never accepts orders or opens channels.",
		Port: 0,
	}
}

func (a magmaApp) Definition() appDefinition { return magmaDefinition() }

func (a magmaApp) service() (*MagmaService, error) {
	svc, reason := a.server.magmaService()
	if svc == nil {
		if strings.TrimSpace(reason) == "" {
			reason = "Magma Inbound Sales unavailable"
		}
		return nil, errors.New(reason)
	}
	return svc, nil
}

func (a magmaApp) Info(ctx context.Context) (appInfo, error) {
	info := newAppInfo(a.Definition())
	svc, err := a.service()
	if err != nil {
		return info, err
	}
	installed, enabled, err := svc.AppState(ctx)
	if err != nil {
		return info, err
	}
	if !installed {
		return info, nil
	}
	info.Installed = true
	if enabled {
		info.Status = "running"
	} else {
		info.Status = "stopped"
	}

	// The Amboss credential is shared with the Fee Center and is a JWT that
	// expires, so a working install can go stale without anything else changing.
	// Surface that as app availability instead of letting the poller fail quietly.
	token := svc.TokenState(ctx)
	switch {
	case !token.Configured:
		info.Available = false
		info.UnavailableReason = "amboss_token_missing"
		info.UnavailableMessage = "Set the Amboss API token in the Fee Center to use Magma Inbound Sales."
	case token.Expired:
		info.Available = false
		info.UnavailableReason = "amboss_token_expired"
		info.UnavailableMessage = "The Amboss API token has expired. Renew it in the Fee Center."
	}
	return info, nil
}

func (a magmaApp) Install(ctx context.Context) error {
	svc, err := a.service()
	if err != nil {
		return err
	}
	return svc.SetAppInstalled(ctx, true, true)
}

func (a magmaApp) Uninstall(ctx context.Context) error {
	svc, err := a.service()
	if err != nil {
		return err
	}
	return svc.SetAppInstalled(ctx, false, false)
}

func (a magmaApp) Start(ctx context.Context) error {
	svc, err := a.service()
	if err != nil {
		return err
	}
	return svc.SetAppEnabled(ctx, true)
}

func (a magmaApp) Stop(ctx context.Context) error {
	svc, err := a.service()
	if err != nil {
		return err
	}
	return svc.SetAppEnabled(ctx, false)
}
