package server

import (
	"context"
	"errors"
	"strings"
)

const loopOutBRLNAppID = "loopout-brln"

type loopOutBRLNApp struct{ server *Server }

func newLoopOutBRLNApp(s *Server) appHandler { return loopOutBRLNApp{server: s} }

func loopOutBRLNDefinition() appDefinition {
	return appDefinition{
		ID:          loopOutBRLNAppID,
		Name:        "Loop Out BR⚡LN",
		Description: "Move outbound Lightning liquidity to an external wallet in controlled Lightning Address payments.",
		Port:        0,
	}
}

func (a loopOutBRLNApp) Definition() appDefinition { return loopOutBRLNDefinition() }

func (a loopOutBRLNApp) service() (*LoopOutBRLNService, error) {
	svc, reason := a.server.loopOutBRLNService()
	if svc == nil {
		if strings.TrimSpace(reason) == "" {
			reason = "Loop Out BRLN unavailable"
		}
		return nil, errors.New(reason)
	}
	return svc, nil
}

func (a loopOutBRLNApp) Info(ctx context.Context) (appInfo, error) {
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
	return info, nil
}

func (a loopOutBRLNApp) Install(ctx context.Context) error {
	svc, err := a.service()
	if err != nil {
		return err
	}
	return svc.SetAppInstalled(ctx, true, true)
}

func (a loopOutBRLNApp) Uninstall(ctx context.Context) error {
	svc, err := a.service()
	if err != nil {
		return err
	}
	return svc.SetAppInstalled(ctx, false, false)
}

func (a loopOutBRLNApp) Start(ctx context.Context) error {
	svc, err := a.service()
	if err != nil {
		return err
	}
	return svc.SetAppEnabled(ctx, true)
}

func (a loopOutBRLNApp) Stop(ctx context.Context) error {
	svc, err := a.service()
	if err != nil {
		return err
	}
	return svc.SetAppEnabled(ctx, false)
}
