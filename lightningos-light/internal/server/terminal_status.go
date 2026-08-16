package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"lightningos-light/internal/system"
)

var (
	terminalMutationMu          sync.Mutex
	terminalOperatorUserPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	terminalPasswordPattern     = regexp.MustCompile(`^[A-Za-z0-9]{16,128}$`)
	terminalRuntimeEnvPath      = "/etc/lightningos/terminal.env"
)

type terminalStatus struct {
	Enabled              bool   `json:"enabled"`
	CredentialConfigured bool   `json:"credential_configured"`
	AllowWrite           bool   `json:"allow_write"`
	Port                 int    `json:"port"`
	OperatorUser         string `json:"operator_user"`
}

type terminalCredentialRotateRequest struct {
	ConfirmPassword string `json:"confirm_password"`
}

type terminalCredentialRotateResponse struct {
	OperatorUser   string `json:"operator_user"`
	Password       string `json:"password"`
	RestartPending bool   `json:"restart_pending"`
}

type terminalControlRequest struct {
	Enabled         *bool  `json:"enabled"`
	AllowWrite      *bool  `json:"allow_write"`
	ConfirmPassword string `json:"confirm_password"`
}

type terminalControlResponse struct {
	Enabled    bool `json:"enabled"`
	AllowWrite bool `json:"allow_write"`
}

func (s *Server) handleTerminalStatus(w http.ResponseWriter, r *http.Request) {
	port := 7681
	if raw := terminalRuntimeValue("TERMINAL_PORT"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			port = parsed
		}
	}

	writeJSON(w, http.StatusOK, terminalStatus{
		Enabled:              terminalRuntimeValue("TERMINAL_ENABLED") == "1",
		CredentialConfigured: terminalCredentialConfigured(terminalStatusRuntimeValue("TERMINAL_CREDENTIAL")),
		AllowWrite:           terminalRuntimeValue("TERMINAL_ALLOW_WRITE") == "1",
		OperatorUser:         terminalStatusOperatorUser(),
		Port:                 port,
	})
}

func (s *Server) handleTerminalCredentialRotate(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil || !s.auth.Enabled() {
		writeErrorCode(w, http.StatusForbidden, "terminal_credential_login_required", "enable LightningOS login protection before rotating the terminal credential")
		return
	}

	var req terminalCredentialRotateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	if err := decoder.Decode(&req); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "terminal_credential_invalid_request", "invalid request body")
		return
	}
	if !s.requireSensitiveReauth(w, r, authScopeTerminalCredential, req.ConfirmPassword,
		"terminal_credential_reauth_required", "confirm your LightningOS password to rotate the terminal credential") {
		return
	}

	terminalMutationMu.Lock()
	defer terminalMutationMu.Unlock()

	operatorUser := terminalOperatorUser()
	if !terminalOperatorUserPattern.MatchString(operatorUser) {
		writeErrorCode(w, http.StatusInternalServerError, "terminal_operator_invalid", "terminal operator user is invalid")
		return
	}
	password, err := randomPassword(32)
	if err != nil {
		writeErrorCode(w, http.StatusInternalServerError, "terminal_credential_generation_failed", "failed to generate terminal credential")
		return
	}
	oldCredential := os.Getenv("TERMINAL_CREDENTIAL")
	newCredential := operatorUser + ":" + password
	updates := map[string]string{
		"TERMINAL_CREDENTIAL": newCredential,
	}
	if err := authPersistSecrets(updates); err != nil {
		writeErrorCode(w, http.StatusInternalServerError, "terminal_credential_persist_failed", "failed to persist terminal credential")
		return
	}
	_ = os.Setenv("TERMINAL_CREDENTIAL", newCredential)

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if handled, err := system.RotateTerminalCredentialWithBroker(ctx, operatorUser, password); !handled || err != nil {
		rollbackErr := authPersistSecrets(map[string]string{
			"TERMINAL_CREDENTIAL": oldCredential,
		})
		_ = os.Setenv("TERMINAL_CREDENTIAL", oldCredential)
		if s.logger != nil {
			s.logger.Printf("terminal credential rotation failed: %v; rollback: %v", err, rollbackErr)
		}
		writeErrorCode(w, http.StatusInternalServerError, "terminal_credential_apply_failed", "failed to apply terminal credential")
		return
	}

	restartPending := false
	if terminalRuntimeValue("TERMINAL_ENABLED") == "1" {
		if err := system.RestartServiceWithBroker(ctx, "lightningos-terminal", false); err != nil {
			restartPending = true
			if s.logger != nil {
				s.logger.Printf("terminal credential rotated but terminal restart failed: %v", err)
			}
		}
	}

	s.recordAuditEvent(r, "terminal.credential.rotate", operatorUser, map[string]any{"restart_pending": restartPending})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, terminalCredentialRotateResponse{
		OperatorUser:   operatorUser,
		Password:       password,
		RestartPending: restartPending,
	})
}

func (s *Server) handleTerminalControl(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil || !s.auth.Enabled() {
		writeErrorCode(w, http.StatusForbidden, "terminal_control_login_required", "enable LightningOS login protection before changing terminal state")
		return
	}

	var req terminalControlRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "terminal_control_invalid_request", "invalid request body")
		return
	}
	if req.Enabled == nil || decoder.Decode(&struct{}{}) != io.EOF || (*req.Enabled && req.AllowWrite == nil) || (!*req.Enabled && req.AllowWrite != nil && *req.AllowWrite) {
		writeErrorCode(w, http.StatusBadRequest, "terminal_control_invalid_request", "invalid request body")
		return
	}
	if !s.requireSensitiveReauth(w, r, authScopeTerminalControl, req.ConfirmPassword,
		"terminal_control_reauth_required", "confirm your LightningOS password to change terminal state") {
		return
	}

	terminalMutationMu.Lock()
	defer terminalMutationMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if *req.Enabled {
		if err := ensureTerminalRuntimeCredential(ctx); err != nil {
			if s.logger != nil {
				s.logger.Printf("terminal runtime credential migration failed")
			}
			writeErrorCode(w, http.StatusInternalServerError, "terminal_runtime_migration_failed", "failed to prepare terminal runtime configuration")
			return
		}
	}
	allowWrite := req.AllowWrite != nil && *req.AllowWrite
	if handled, err := system.SetTerminalEnabledWithBroker(ctx, *req.Enabled, allowWrite); !handled || err != nil {
		if s.logger != nil {
			s.logger.Printf("terminal control failed: %v", err)
		}
		writeErrorCode(w, http.StatusInternalServerError, "terminal_control_apply_failed", "failed to change terminal state")
		return
	}

	s.recordAuditEvent(r, "terminal.control", terminalOperatorUser(), map[string]any{"enabled": *req.Enabled, "allow_write": allowWrite})
	writeJSON(w, http.StatusOK, terminalControlResponse{Enabled: *req.Enabled, AllowWrite: allowWrite})
}

func terminalCredentialConfigured(raw string) bool {
	user, password, ok := strings.Cut(strings.TrimSpace(raw), ":")
	return ok && terminalOperatorUserPattern.MatchString(strings.TrimSpace(user)) && terminalPasswordPattern.MatchString(password)
}

func terminalOperatorUser() string {
	if user := terminalRuntimeValue("TERMINAL_OPERATOR_USER"); user != "" {
		return user
	}
	return "losop"
}

func terminalStatusOperatorUser() string {
	if user := terminalStatusRuntimeValue("TERMINAL_OPERATOR_USER"); terminalOperatorUserPattern.MatchString(user) {
		return user
	}
	if credential := terminalStatusRuntimeValue("TERMINAL_CREDENTIAL"); credential != "" {
		if user, _, ok := strings.Cut(credential, ":"); ok && terminalOperatorUserPattern.MatchString(user) {
			return user
		}
	}
	return "losop"
}

func terminalStatusRuntimeValue(key string) string {
	value, err := readEnvFileValue(terminalRuntimeEnvPath, key)
	if err == nil {
		return strings.TrimSpace(value)
	}
	if errors.Is(err, os.ErrNotExist) && (key == "TERMINAL_CREDENTIAL" || key == "TERMINAL_OPERATOR_USER") {
		return strings.TrimSpace(os.Getenv(key))
	}
	return ""
}

func ensureTerminalRuntimeCredential(ctx context.Context) error {
	info, err := os.Lstat(terminalRuntimeEnvPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("terminal runtime configuration is unsafe")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	credential := strings.TrimSpace(os.Getenv("TERMINAL_CREDENTIAL"))
	operatorUser, password, ok := strings.Cut(credential, ":")
	if !ok || !terminalOperatorUserPattern.MatchString(operatorUser) || !terminalPasswordPattern.MatchString(password) {
		return errors.New("legacy terminal credential is unavailable")
	}
	handled, err := system.RotateTerminalCredentialWithBroker(ctx, operatorUser, password)
	if !handled {
		return errors.New("terminal credential broker is unavailable")
	}
	return err
}

func terminalRuntimeValue(key string) string {
	value, err := readEnvFileValue(terminalRuntimeEnvPath, key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}
