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
	if financialIntentApproved(r, scope) {
		return true
	}
	if operation, covered := coreFinancialOperation(r.URL.EscapedPath()); covered && financialOperationConditional(operation) && operation.scope == scope {
		financialIntentChallenge(w, operation, "")
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
	if _, err := s.auth.reauth(w, r, session.ID, confirmPassword, scope); err != nil {
		if err == errReauthRateLimited {
			return false
		}
		s.recordAuditEventAsync(r, "auth.reauth.failed", strings.TrimSpace(scope), map[string]any{"reason": authAuditReason(err)})
		writeErrorCode(w, http.StatusUnauthorized, "auth_invalid_credentials", "invalid credentials")
		return false
	}
	s.recordAuditEventAsync(r, "auth.reauth.succeeded", strings.TrimSpace(scope), nil)
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
