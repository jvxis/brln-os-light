package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	lndMaintenanceTransitionOff          = "off"
	lndMaintenanceTransitionActivating   = "activating"
	lndMaintenanceTransitionActive       = "active"
	lndMaintenanceTransitionDeactivating = "deactivating"
)

type lndMaintenanceState struct {
	Active                       bool
	Transition                   string
	PreviousRejectHTLCPresent    bool
	PreviousRejectHTLCValue      string
	PreviousMaxPendingPresent    bool
	PreviousMaxPendingValue      string
	PreviousRebalanceAutoEnabled bool
	PreviousMagmaModePresent     bool
	PreviousMagmaMode            string
	ActivatedAt                  *time.Time
	UpdatedAt                    time.Time
	LastError                    string
}

type lndMaintenanceStatus struct {
	Enabled                  bool   `json:"enabled"`
	Phase                    string `json:"phase"`
	RoutingDisabled          bool   `json:"routing_disabled"`
	IncomingChannelsDisabled bool   `json:"incoming_channels_disabled"`
	LOSChannelOpeningBlocked bool   `json:"los_channel_opening_blocked"`
	RebalanceDisabled        bool   `json:"rebalance_disabled"`
	MagmaAutoOpenDisabled    bool   `json:"magma_auto_open_disabled"`
	PendingHTLCs             int    `json:"pending_htlcs"`
	PendingHTLCsKnown        bool   `json:"pending_htlcs_known"`
	LNDReachable             bool   `json:"lnd_reachable"`
	ActivatedAt              string `json:"activated_at,omitempty"`
	UpdatedAt                string `json:"updated_at,omitempty"`
	LastError                string `json:"last_error,omitempty"`
}

func (s *Server) ensureLNDMaintenanceSchema(ctx context.Context) error {
	s.lndMaintenanceSchemaMu.Lock()
	defer s.lndMaintenanceSchemaMu.Unlock()
	if s.lndMaintenanceSchemaReady {
		return nil
	}
	if s.db == nil {
		s.initRebalance()
	}
	if s.db == nil {
		return errors.New("maintenance mode requires the LightningOS database")
	}
	_, err := s.db.Exec(ctx, `
create table if not exists lnd_maintenance_state (
  id smallint primary key check (id = 1),
  active boolean not null default false,
  transition text not null default 'off',
  previous_reject_htlc_present boolean not null default false,
  previous_reject_htlc_value text not null default '',
  previous_max_pending_present boolean not null default false,
  previous_max_pending_value text not null default '',
  previous_rebalance_auto_enabled boolean not null default false,
  previous_magma_mode_present boolean not null default false,
  previous_magma_mode text not null default '',
  activated_at timestamptz,
  updated_at timestamptz not null default now(),
  last_error text not null default ''
);
insert into lnd_maintenance_state (id) values (1) on conflict (id) do nothing`)
	if err != nil {
		return fmt.Errorf("initialize maintenance state: %w", err)
	}
	s.lndMaintenanceSchemaReady = true
	return nil
}

func (s *Server) loadLNDMaintenanceState(ctx context.Context) (lndMaintenanceState, error) {
	if err := s.ensureLNDMaintenanceSchema(ctx); err != nil {
		return lndMaintenanceState{}, err
	}
	var state lndMaintenanceState
	err := s.db.QueryRow(ctx, `
select active, transition,
       previous_reject_htlc_present, previous_reject_htlc_value,
       previous_max_pending_present, previous_max_pending_value,
       previous_rebalance_auto_enabled,
       previous_magma_mode_present, previous_magma_mode,
       activated_at, updated_at, last_error
from lnd_maintenance_state where id=1`).Scan(
		&state.Active, &state.Transition,
		&state.PreviousRejectHTLCPresent, &state.PreviousRejectHTLCValue,
		&state.PreviousMaxPendingPresent, &state.PreviousMaxPendingValue,
		&state.PreviousRebalanceAutoEnabled,
		&state.PreviousMagmaModePresent, &state.PreviousMagmaMode,
		&state.ActivatedAt, &state.UpdatedAt, &state.LastError,
	)
	return state, err
}

func (s *Server) saveLNDMaintenanceActivation(ctx context.Context, state lndMaintenanceState) error {
	_, err := s.db.Exec(ctx, `
update lnd_maintenance_state set
  active=true, transition=$1,
  previous_reject_htlc_present=$2, previous_reject_htlc_value=$3,
  previous_max_pending_present=$4, previous_max_pending_value=$5,
  previous_rebalance_auto_enabled=$6,
  previous_magma_mode_present=$7, previous_magma_mode=$8,
  activated_at=now(), updated_at=now(), last_error=''
where id=1`, lndMaintenanceTransitionActivating,
		state.PreviousRejectHTLCPresent, state.PreviousRejectHTLCValue,
		state.PreviousMaxPendingPresent, state.PreviousMaxPendingValue,
		state.PreviousRebalanceAutoEnabled,
		state.PreviousMagmaModePresent, state.PreviousMagmaMode)
	return err
}

func (s *Server) setLNDMaintenanceTransition(ctx context.Context, active bool, transition, lastError string) error {
	_, err := s.db.Exec(ctx, `
update lnd_maintenance_state
set active=$1, transition=$2, updated_at=now(), last_error=$3,
    activated_at=case when $1 then activated_at else null end
where id=1`, active, transition, strings.TrimSpace(lastError))
	return err
}

func readLNDOption(raw, sectionName, optionName string) (string, bool) {
	section := ""
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.TrimSpace(strings.Trim(trimmed, "[]"))
			continue
		}
		if !strings.EqualFold(section, sectionName) || trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), optionName) {
			return strings.TrimSpace(parts[1]), true
		}
	}
	return "", false
}

func setOrRemoveLNDOption(raw, sectionName, optionName, value string, present bool) string {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	start, end := -1, len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "[") || !strings.HasSuffix(trimmed, "]") {
			continue
		}
		name := strings.TrimSpace(strings.Trim(trimmed, "[]"))
		if strings.EqualFold(name, sectionName) {
			start = i
			continue
		}
		if start >= 0 && i > start {
			end = i
			break
		}
	}
	if start < 0 {
		if !present {
			return raw
		}
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, "["+sectionName+"]", optionName+"="+value)
		return strings.Join(lines, "\n")
	}

	found := false
	for i := start + 1; i < end; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 || !strings.EqualFold(strings.TrimSpace(parts[0]), optionName) {
			continue
		}
		if !present {
			lines[i] = ""
		} else if !found {
			lines[i] = strings.TrimSpace(parts[0]) + "=" + value
		} else {
			lines[i] = ""
		}
		found = true
	}
	if present && !found {
		lines = append(lines[:end], append([]string{optionName + "=" + value}, lines[end:]...)...)
	}
	return strings.Join(lines, "\n")
}

func applyLNDMaintenanceOptions(raw string) string {
	updated := setOrRemoveLNDOption(raw, "Application Options", "rejecthtlc", "true", true)
	return setOrRemoveLNDOption(updated, "Application Options", "maxpendingchannels", "0", true)
}

func restoreLNDMaintenanceOptions(raw string, state lndMaintenanceState) string {
	updated := setOrRemoveLNDOption(raw, "Application Options", "rejecthtlc", state.PreviousRejectHTLCValue, state.PreviousRejectHTLCPresent)
	return setOrRemoveLNDOption(updated, "Application Options", "maxpendingchannels", state.PreviousMaxPendingValue, state.PreviousMaxPendingPresent)
}

func (s *Server) lndMaintenanceActive(ctx context.Context) (bool, error) {
	state, err := s.loadLNDMaintenanceState(ctx)
	return state.Active, err
}

func (s *Server) rejectLNDMaintenanceAction(w http.ResponseWriter, r *http.Request, action string) bool {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	active, err := s.lndMaintenanceActive(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("maintenance gate unavailable for %s: %v", action, err)
		}
		return false
	}
	if !active {
		return false
	}
	writeErrorCode(w, http.StatusConflict, "lnd_maintenance_active", "action blocked while LND maintenance mode is active")
	return true
}

func (s *Server) handleLNDMaintenanceGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	state, err := s.loadLNDMaintenanceState(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	status := lndMaintenanceStatus{Enabled: state.Active, Phase: state.Transition, LOSChannelOpeningBlocked: state.Active, LastError: state.LastError}
	if !state.UpdatedAt.IsZero() {
		status.UpdatedAt = state.UpdatedAt.UTC().Format(time.RFC3339)
	}
	if state.ActivatedAt != nil {
		status.ActivatedAt = state.ActivatedAt.UTC().Format(time.RFC3339)
	}
	raw, _ := os.ReadFile(lndConfPath)
	rejectValue, rejectPresent := readLNDOption(string(raw), "Application Options", "rejecthtlc")
	maxPendingValue, maxPendingPresent := readLNDOption(string(raw), "Application Options", "maxpendingchannels")
	status.RoutingDisabled = rejectPresent && strings.EqualFold(rejectValue, "true")
	status.IncomingChannelsDisabled = maxPendingPresent && strings.TrimSpace(maxPendingValue) == "0"

	if s.rebalance != nil {
		if cfg, cfgErr := s.rebalance.GetConfig(ctx); cfgErr == nil {
			status.RebalanceDisabled = !cfg.AutoEnabled
		}
		if state.Active {
			if channels, channelsErr := s.rebalance.listChannelsCached(ctx); channelsErr == nil {
				status.LNDReachable = true
				status.PendingHTLCsKnown = true
				for _, channel := range channels {
					status.PendingHTLCs += channel.PendingHtlcCount
				}
			}
		}
	}
	if state.Active {
		// Activation either moved Auto to Monitor or observed that Magma was not
		// automatic/available. The settings endpoint is gated while active, so no
		// extra database read is needed on every status poll.
		status.MagmaAutoOpenDisabled = true
	} else if s.magma != nil {
		if settings, settingsErr := s.magma.Settings(ctx); settingsErr == nil {
			status.MagmaAutoOpenDisabled = settings.Mode != magmaModeAuto
		}
	} else {
		status.MagmaAutoOpenDisabled = true
	}

	if state.Active && state.Transition == lndMaintenanceTransitionActive {
		switch {
		case !status.RoutingDisabled || !status.IncomingChannelsDisabled || !status.RebalanceDisabled || !status.MagmaAutoOpenDisabled:
			status.Phase = "degraded"
		case !status.PendingHTLCsKnown:
			status.Phase = "verifying"
		case status.PendingHTLCs > 0:
			status.Phase = "draining"
		default:
			status.Phase = lndMaintenanceTransitionActive
		}
	}
	if state.Active && state.Transition == lndMaintenanceTransitionActivating {
		if status.RoutingDisabled && status.IncomingChannelsDisabled && status.RebalanceDisabled && status.MagmaAutoOpenDisabled {
			if transitionErr := s.setLNDMaintenanceTransition(ctx, true, lndMaintenanceTransitionActive, ""); transitionErr == nil {
				status.Phase = lndMaintenanceTransitionActive
			}
		} else {
			status.Phase = "degraded"
		}
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleLNDMaintenancePost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled         bool   `json:"enabled"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !s.requireSensitiveReauth(w, r, authScopeLNDMaintenance, req.ConfirmPassword,
		"lnd_maintenance_reauth_required", "password confirmation required to change LND maintenance mode") {
		return
	}

	s.lndMaintenanceMu.Lock()
	defer s.lndMaintenanceMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := s.ensureLNDMaintenanceSchema(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if s.rebalance == nil {
		s.initRebalance()
	}
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance service is required to change maintenance mode")
		return
	}
	state, err := s.loadLNDMaintenanceState(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if state.Active == req.Enabled && state.Transition != lndMaintenanceTransitionActivating && state.Transition != lndMaintenanceTransitionDeactivating {
		s.handleLNDMaintenanceGet(w, r)
		return
	}

	if req.Enabled {
		err = s.activateLNDMaintenance(ctx)
	} else {
		err = s.deactivateLNDMaintenance(ctx, state)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAuditEventAsync(r, "lnd.maintenance.changed", fmt.Sprintf("enabled=%t", req.Enabled), nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": req.Enabled, "lnd_restarting": true})
}

func (s *Server) activateLNDMaintenance(ctx context.Context) error {
	raw, err := os.ReadFile(lndConfPath)
	if err != nil {
		return fmt.Errorf("read lnd.conf: %w", err)
	}
	rebalanceCfg, err := s.rebalance.GetConfig(ctx)
	if err != nil {
		return fmt.Errorf("read rebalance config: %w", err)
	}
	state := lndMaintenanceState{PreviousRebalanceAutoEnabled: rebalanceCfg.AutoEnabled}
	state.PreviousRejectHTLCValue, state.PreviousRejectHTLCPresent = readLNDOption(string(raw), "Application Options", "rejecthtlc")
	state.PreviousMaxPendingValue, state.PreviousMaxPendingPresent = readLNDOption(string(raw), "Application Options", "maxpendingchannels")

	s.initMagma()
	if s.magma != nil {
		if settings, settingsErr := s.magma.Settings(ctx); settingsErr == nil && settings.Mode == magmaModeAuto {
			state.PreviousMagmaModePresent = true
			state.PreviousMagmaMode = settings.Mode
		}
	}
	if err := s.saveLNDMaintenanceActivation(ctx, state); err != nil {
		return fmt.Errorf("save maintenance snapshot: %w", err)
	}

	rollback := func(cause error) error {
		_ = os.WriteFile(lndConfPath, raw, 0660)
		rebalanceCfg.AutoEnabled = state.PreviousRebalanceAutoEnabled
		_, _ = s.rebalance.UpdateConfig(context.Background(), rebalanceCfg)
		if state.PreviousMagmaModePresent && s.magma != nil {
			mode := state.PreviousMagmaMode
			_, _ = s.magma.UpdateSettings(context.Background(), MagmaSettingsUpdate{Mode: &mode})
		}
		restartCtx, restartCancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = restartLNDService(restartCtx)
		restartCancel()
		s.markLNDRestart()
		_ = s.setLNDMaintenanceTransition(context.Background(), false, lndMaintenanceTransitionOff, cause.Error())
		return cause
	}

	if rebalanceCfg.AutoEnabled {
		rebalanceCfg.AutoEnabled = false
		if _, err := s.rebalance.UpdateConfig(ctx, rebalanceCfg); err != nil {
			return rollback(fmt.Errorf("disable automatic rebalances: %w", err))
		}
	}
	s.rebalance.StopAllActiveJobs()
	if state.PreviousMagmaModePresent && s.magma != nil {
		mode := magmaModeMonitor
		if _, err := s.magma.UpdateSettings(ctx, MagmaSettingsUpdate{Mode: &mode}); err != nil {
			return rollback(fmt.Errorf("disable automatic Magma channel opens: %w", err))
		}
	}
	updated := applyLNDMaintenanceOptions(string(raw))
	if err := os.WriteFile(lndConfPath, []byte(updated), 0660); err != nil {
		return rollback(fmt.Errorf("write lnd.conf: %w", err))
	}
	if err := restartLNDService(ctx); err != nil {
		return rollback(fmt.Errorf("restart LND: %w", err))
	}
	s.markLNDRestart()
	if err := s.setLNDMaintenanceTransition(ctx, true, lndMaintenanceTransitionActive, ""); err != nil {
		return fmt.Errorf("finalize maintenance state: %w", err)
	}
	return nil
}

func (s *Server) deactivateLNDMaintenance(ctx context.Context, state lndMaintenanceState) error {
	if !state.Active {
		return nil
	}
	if err := s.setLNDMaintenanceTransition(ctx, true, lndMaintenanceTransitionDeactivating, ""); err != nil {
		return err
	}
	raw, err := os.ReadFile(lndConfPath)
	if err != nil {
		_ = s.setLNDMaintenanceTransition(context.Background(), true, lndMaintenanceTransitionActive, err.Error())
		return fmt.Errorf("read lnd.conf: %w", err)
	}
	updated := restoreLNDMaintenanceOptions(string(raw), state)
	if err := os.WriteFile(lndConfPath, []byte(updated), 0660); err != nil {
		_ = s.setLNDMaintenanceTransition(context.Background(), true, lndMaintenanceTransitionActive, err.Error())
		return fmt.Errorf("restore lnd.conf: %w", err)
	}
	if err := restartLNDService(ctx); err != nil {
		_ = os.WriteFile(lndConfPath, raw, 0660)
		_ = s.setLNDMaintenanceTransition(context.Background(), true, lndMaintenanceTransitionActive, err.Error())
		return fmt.Errorf("restart LND: %w", err)
	}
	s.markLNDRestart()

	cfg, err := s.rebalance.GetConfig(ctx)
	if err != nil {
		return fmt.Errorf("read rebalance config for restore: %w", err)
	}
	cfg.AutoEnabled = state.PreviousRebalanceAutoEnabled
	if _, err := s.rebalance.UpdateConfig(ctx, cfg); err != nil {
		return fmt.Errorf("restore rebalance config: %w", err)
	}
	if state.PreviousMagmaModePresent && s.magma != nil {
		mode := state.PreviousMagmaMode
		if _, err := s.magma.UpdateSettings(ctx, MagmaSettingsUpdate{Mode: &mode}); err != nil {
			return fmt.Errorf("restore Magma mode: %w", err)
		}
	}
	return s.setLNDMaintenanceTransition(ctx, false, lndMaintenanceTransitionOff, "")
}
