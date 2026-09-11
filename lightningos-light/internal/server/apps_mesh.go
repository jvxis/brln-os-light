package server

import (
	"context"
	"encoding/json"
	"errors"
	"lightningos-light/internal/mesh"
	"lightningos-light/internal/privileged"
	"lightningos-light/internal/system"
)

const meshAppID = "los-mesh"

type meshApp struct{ server *Server }

func newMeshApp(s *Server) appHandler { return meshApp{s} }
func (a meshApp) Definition() appDefinition {
	return appDefinition{ID: meshAppID, Name: "LOS Mesh", Description: "Bitcoin and Lightning requests over Meshtastic LoRa. Private pairing, signed transaction relay and local approval for payments.", Port: 0}
}
func (a meshApp) Info(ctx context.Context) (appInfo, error) {
	info := newAppInfo(a.Definition())
	raw, err := system.MeshControlWithBroker(ctx, "status", "")
	if err != nil {
		return info, err
	}
	var state privileged.MeshState
	if err = json.Unmarshal([]byte(raw), &state); err != nil {
		return info, err
	}
	info.Installed = state.Installed
	info.Status = state.Status
	return info, nil
}
func (a meshApp) Install(ctx context.Context) error {
	return errors.New("select the USB device in LOS Mesh setup")
}
func (a meshApp) Start(ctx context.Context) error {
	_, err := system.MeshControlWithBroker(ctx, "start", "")
	return err
}
func (a meshApp) Stop(ctx context.Context) error {
	_, err := system.MeshControlWithBroker(ctx, "stop", "")
	return err
}
func (a meshApp) Uninstall(ctx context.Context) error {
	_, err := system.MeshControlWithBroker(ctx, "remove", "")
	if err != nil {
		return err
	}
	if m, e := a.server.meshService(); e == nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.incoming = map[string]*mesh.Assembly{}
		m.outgoing = map[string]*meshOutbound{}
		m.pending = map[string]*meshPending{}
		_, err = m.db.Exec(ctx, "DELETE FROM los_mesh_peers; UPDATE los_mesh_settings SET mode='send'; UPDATE los_mesh_sessions SET state='cancelled' WHERE state IN ('receiving','sending','awaiting_approval','awaiting_result')")
	}
	return err
}
