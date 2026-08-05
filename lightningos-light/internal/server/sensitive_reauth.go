package server

import (
	"net/http"
	"strings"
)

func (s *Server) requireSensitiveReauth(w http.ResponseWriter, r *http.Request, scope string, confirmPassword string, code string, message string) bool {
	if s.auth == nil || !s.auth.Enabled() {
		return true
	}

	session, ok := authSessionFromContext(r.Context())
	if !ok {
		writeErrorCode(w, http.StatusUnauthorized, "auth_required", "authentication required")
		return false
	}
	if s.auth.HasRecentReauth(session.ID, scope) {
		return true
	}

	confirmPassword = strings.TrimSpace(confirmPassword)
	if confirmPassword == "" {
		writeJSON(w, http.StatusPreconditionRequired, map[string]any{
			"error":                          message,
			"code":                           code,
			"scope":                          scope,
			"requires_password_confirmation": true,
		})
		return false
	}
	if _, err := s.auth.reauth(session.ID, confirmPassword, scope); err != nil {
		writeErrorCode(w, http.StatusUnauthorized, "auth_invalid_credentials", "invalid credentials")
		return false
	}
	return true
}

func (s *Server) requireLightningFundsReauth(w http.ResponseWriter, r *http.Request, confirmPassword string) bool {
	return s.requireSensitiveReauth(
		w,
		r,
		authScopeLightningFunds,
		confirmPassword,
		"lightning_funds_reauth_required",
		"password confirmation required for Lightning fund operations",
	)
}
