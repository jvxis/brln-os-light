package server

import (
	"context"
	"errors"
	"net/http"
	"time"
)

const successionLiveControlReauthRequiredCode = "succession_live_control_reauth_required"

type successionConfigPostRequest struct {
	Enabled               *bool   `json:"enabled"`
	DryRun                *bool   `json:"dry_run"`
	DestinationAddress    *string `json:"destination_address"`
	PreapproveFCOffline   *bool   `json:"preapprove_fc_offline"`
	PreapproveFCStuckHTLC *bool   `json:"preapprove_fc_stuck_htlc"`
	StuckHTLCThresholdSec *int64  `json:"stuck_htlc_threshold_sec"`
	SweepMinConfs         *int    `json:"sweep_min_confs"`
	SweepSatPerVbyte      *int64  `json:"sweep_sat_per_vbyte"`
	CheckPeriodDays       *int    `json:"check_period_days"`
	ReminderPeriodDays    *int    `json:"reminder_period_days"`
	ConfirmPassword       string  `json:"confirm_password"`
}

func (s *Server) handleSuccessionStatusGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.successionService()
	if svc == nil {
		payload := map[string]any{
			"available": false,
		}
		if errMsg != "" {
			payload["error"] = errMsg
		}
		writeJSON(w, http.StatusOK, payload)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	status, err := svc.Status(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"available": false,
			"error":     err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleSuccessionConfigGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.successionService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "succession unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	cfg, err := svc.GetConfig(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleSuccessionConfigPost(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.successionService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "succession unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	var req successionConfigPostRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	current, err := svc.GetConfig(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if successionConfigPostRequiresLiveReauth(current, req) {
		if !s.requireSensitiveReauth(
			w,
			r,
			authScopeSuccessionLive,
			req.ConfirmPassword,
			successionLiveControlReauthRequiredCode,
			"password confirmation required for live succession mode",
		) {
			return
		}
	}

	cfg, err := svc.UpdateConfig(ctx, SuccessionConfigUpdate{
		Enabled:               req.Enabled,
		DryRun:                req.DryRun,
		DestinationAddress:    req.DestinationAddress,
		PreapproveFCOffline:   req.PreapproveFCOffline,
		PreapproveFCStuckHTLC: req.PreapproveFCStuckHTLC,
		StuckHTLCThresholdSec: req.StuckHTLCThresholdSec,
		SweepMinConfs:         req.SweepMinConfs,
		SweepSatPerVbyte:      req.SweepSatPerVbyte,
		CheckPeriodDays:       req.CheckPeriodDays,
		ReminderPeriodDays:    req.ReminderPeriodDays,
	})
	if err != nil {
		if errors.Is(err, ErrSuccessionTelegramMirrorRequired) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, ErrSuccessionDestinationRequired) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleSuccessionAlivePost(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.successionService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "succession unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	var req struct {
		Source string `json:"source"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	cfg, err := svc.MarkAlive(ctx, req.Source)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleSuccessionSimulatePost(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.successionService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "succession unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	var req struct {
		Action string `json:"action"`
		Source string `json:"source"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := svc.Simulate(ctx, req.Action, req.Source); err != nil {
		if errors.Is(err, ErrSuccessionInvalidAction) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func successionConfigPostRequiresLiveReauth(current SuccessionConfig, req successionConfigPostRequest) bool {
	enabled := current.Enabled
	dryRun := current.DryRun
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	return enabled && !dryRun
}
