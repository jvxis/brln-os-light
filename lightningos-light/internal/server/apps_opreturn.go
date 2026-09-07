package server

import (
	"context"
	"errors"
)

type opreturnApp struct{ server *Server }

func newOPReturnApp(s *Server) appHandler { return opreturnApp{s} }
func (a opreturnApp) Definition() appDefinition {
	return appDefinition{ID: "opreturn", Name: "Bitcoin OP_RETURN", Description: "Publish up to 80 UTF-8 bytes in a Bitcoin transaction. Confirmed content is public and permanent; publication spends on-chain funds."}
}
func (a opreturnApp) Info(ctx context.Context) (appInfo, error) {
	info := newAppInfo(a.Definition())
	if err := a.server.ensureOPReturnSchema(ctx); err != nil {
		return info, err
	}
	var enabled bool
	err := a.server.db.QueryRow(ctx, `SELECT installed, enabled FROM opreturn_app WHERE id=1`).Scan(&info.Installed, &enabled)
	if info.Installed {
		info.Status = "stopped"
		if enabled {
			info.Status = "running"
		}
	}
	return info, err
}
func (a opreturnApp) set(ctx context.Context, installed, enabled bool, requireInstalled bool) error {
	// Lifecycle changes cannot race a publication already approved by the operator.
	if !a.server.opreturnPublishMu.TryLock() {
		return errors.New("OP_RETURN operation in progress")
	}
	defer a.server.opreturnPublishMu.Unlock()
	if err := a.server.ensureOPReturnSchema(ctx); err != nil {
		return err
	}
	result, err := a.server.db.Exec(ctx, `UPDATE opreturn_app SET installed=$1, enabled=$2 WHERE id=1 AND (NOT $3 OR installed)`, installed, enabled, requireInstalled)
	if err == nil && result.RowsAffected() != 1 {
		return errors.New("OP_RETURN is not installed")
	}
	return err
}
func (a opreturnApp) Install(ctx context.Context) error   { return a.set(ctx, true, true, false) }
func (a opreturnApp) Uninstall(ctx context.Context) error { return a.set(ctx, false, false, false) }
func (a opreturnApp) Start(ctx context.Context) error     { return a.set(ctx, true, true, true) }
func (a opreturnApp) Stop(ctx context.Context) error      { return a.set(ctx, true, false, true) }
