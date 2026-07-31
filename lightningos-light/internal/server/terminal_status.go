package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const terminalPasswordHelperPath = "/usr/local/sbin/lightningos-terminal-password"

var (
	terminalCredentialRotationMu sync.Mutex
	terminalOperatorUserPattern  = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
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

func (s *Server) handleTerminalStatus(w http.ResponseWriter, r *http.Request) {
	port := 7681
	if raw := strings.TrimSpace(os.Getenv("TERMINAL_PORT")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			port = parsed
		}
	}

	writeJSON(w, http.StatusOK, terminalStatus{
		Enabled:              strings.TrimSpace(os.Getenv("TERMINAL_ENABLED")) == "1",
		CredentialConfigured: terminalCredentialConfigured(os.Getenv("TERMINAL_CREDENTIAL")),
		AllowWrite:           strings.TrimSpace(os.Getenv("TERMINAL_ALLOW_WRITE")) == "1",
		OperatorUser:         terminalOperatorUser(),
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

	terminalCredentialRotationMu.Lock()
	defer terminalCredentialRotationMu.Unlock()

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
	stagedPath, err := stageTerminalPassword(password)
	if err != nil {
		writeErrorCode(w, http.StatusInternalServerError, "terminal_credential_stage_failed", "failed to stage terminal credential")
		return
	}
	defer os.Remove(stagedPath)

	oldCredential := os.Getenv("TERMINAL_CREDENTIAL")
	oldOperatorPassword := os.Getenv("TERMINAL_OPERATOR_PASSWORD")
	newCredential := operatorUser + ":" + password
	updates := map[string]string{
		"TERMINAL_CREDENTIAL":        newCredential,
		"TERMINAL_OPERATOR_PASSWORD": password,
	}
	if err := authPersistSecrets(updates); err != nil {
		writeErrorCode(w, http.StatusInternalServerError, "terminal_credential_persist_failed", "failed to persist terminal credential")
		return
	}
	_ = os.Setenv("TERMINAL_CREDENTIAL", newCredential)
	_ = os.Setenv("TERMINAL_OPERATOR_PASSWORD", password)

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if out, err := runSystemd(ctx, terminalPasswordHelperPath, stagedPath, operatorUser); err != nil {
		rollbackErr := authPersistSecrets(map[string]string{
			"TERMINAL_CREDENTIAL":        oldCredential,
			"TERMINAL_OPERATOR_PASSWORD": oldOperatorPassword,
		})
		_ = os.Setenv("TERMINAL_CREDENTIAL", oldCredential)
		_ = os.Setenv("TERMINAL_OPERATOR_PASSWORD", oldOperatorPassword)
		if s.logger != nil {
			s.logger.Printf("terminal credential rotation failed: %v (%s); rollback: %v", err, strings.TrimSpace(out), rollbackErr)
		}
		writeErrorCode(w, http.StatusInternalServerError, "terminal_credential_apply_failed", "failed to apply terminal credential")
		return
	}

	restartPending := false
	if out, err := runSystemd(ctx, "/bin/systemctl", "restart", "lightningos-terminal.service"); err != nil {
		restartPending = true
		if s.logger != nil {
			s.logger.Printf("terminal credential rotated but terminal restart failed: %v (%s)", err, strings.TrimSpace(out))
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

func terminalCredentialConfigured(raw string) bool {
	user, password, ok := strings.Cut(strings.TrimSpace(raw), ":")
	return ok && strings.TrimSpace(user) != "" && password != ""
}

func terminalOperatorUser() string {
	if user := strings.TrimSpace(os.Getenv("TERMINAL_OPERATOR_USER")); user != "" {
		return user
	}
	return "losop"
}

func stageTerminalPassword(password string) (string, error) {
	if err := os.MkdirAll("/var/lib/lightningos", 0o750); err != nil {
		return "", fmt.Errorf("create terminal staging directory: %w", err)
	}
	file, err := os.CreateTemp("/var/lib/lightningos", "terminal-password-")
	if err != nil {
		return "", fmt.Errorf("create terminal password staging file: %w", err)
	}
	path := file.Name()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("chmod terminal password staging file: %w", err)
	}
	if _, err := file.WriteString(password + "\n"); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("write terminal password staging file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close terminal password staging file: %w", err)
	}
	return path, nil
}
