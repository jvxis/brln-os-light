package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"lightningos-light/internal/system"
)

const authDefaultConfigPath = "/etc/lightningos/config.yaml"

func (s *Server) handleAuthEnableLogin(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.cfg == nil {
		writeError(w, http.StatusInternalServerError, "config unavailable")
		return
	}
	if s.cfg.Features.LoginEnabled() {
		writeErrorCode(w, http.StatusConflict, "auth_already_enabled", "login protection is already enabled")
		return
	}

	configPath := authConfigPath(s.cfg.Path)
	handledByBroker, err := system.EnableLoginConfigWithBroker(r.Context(), configPath)
	if !handledByBroker {
		if s.logger != nil {
			s.logger.Printf("auth enable rejected: privileged broker enforce mode is required")
		}
		writeErrorCode(w, http.StatusServiceUnavailable, "auth_enable_broker_required", "privileged broker enforce mode is required to enable login protection")
		return
	}
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("auth enable failed: %v", err)
		}
		writeErrorCode(w, http.StatusInternalServerError, "auth_enable_failed", "failed to enable login protection")
		return
	}

	enabled := true
	s.cfg.Features.EnableLogin = &enabled
	s.scheduleManagerRestart(1500 * time.Millisecond)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":                true,
		"restart_scheduled": true,
		"config_path":       configPath,
	})
}

func authConfigPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return authDefaultConfigPath
	}
	return trimmed
}

func (s *Server) scheduleManagerRestart(delay time.Duration) {
	if s == nil {
		return
	}
	if delay <= 0 {
		delay = time.Second
	}
	time.AfterFunc(delay, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := system.RestartServiceWithBroker(ctx, "lightningos-manager", true); err != nil && s.logger != nil {
			s.logger.Printf("auth enable restart failed: %v", err)
		}
	})
}
