package server

import (
	"errors"
	"net/http"
)

func (s *Server) respondLNDConfigApply(w http.ResponseWriter, r *http.Request, kind string, result lndConfigApplyResult, err error) {
	setNoStore(w)
	if err != nil {
		var failure *lndConfigApplyError
		if errors.As(err, &failure) {
			s.recordAuditEventAsync(r, "lnd.config.apply_failed", kind, map[string]any{"phase": failure.Phase, "config_restored": failure.ConfigRestored, "rollback_verified": failure.RollbackVerified, "wallet_unlock_required": failure.RollbackUnlockRequired})
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": failure.Error(), "code": failure.Code, "config_restored": failure.ConfigRestored, "rollback_verified": failure.RollbackVerified, "wallet_unlock_required": failure.RollbackUnlockRequired, "wallet_setup_required": failure.RollbackSetupRequired})
			return
		}
		s.recordAuditEventAsync(r, "lnd.config.rejected", kind, map[string]any{"reason": "security_policy"})
		writeErrorCode(w, http.StatusBadRequest, "lnd_config_unsafe", err.Error())
		return
	}
	if result.Applied {
		s.markLNDRestart()
	}
	s.recordAuditEventAsync(r, "lnd.config.changed", kind, map[string]any{"applied": result.Applied, "wallet_unlock_required": result.WalletUnlockRequired, "wallet_setup_required": result.WalletSetupRequired})
	writeJSON(w, http.StatusOK, result)
}
