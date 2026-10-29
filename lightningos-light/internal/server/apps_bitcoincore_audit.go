package server

import (
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	"lightningos-light/internal/appmanifest"
)

// Record the HTTP outcome without copying request bodies, credentials or raw
// broker errors into Audit. The catalog version is a target, not proof that a
// stopped/failed runtime has that version. Broker audit remains independent.
func (s *Server) auditBitcoinCoreOperation(w http.ResponseWriter, r *http.Request, appID, action string) (http.ResponseWriter, func()) {
	if appID != bitcoinCoreAppID {
		return w, func() {}
	}
	wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
	return wrapped, func() {
		status := wrapped.Status()
		if status == 0 {
			status = http.StatusOK
		}
		outcome := "error"
		if status >= 200 && status < 300 {
			outcome = "success"
		}
		s.recordAuditEventAsync(r, "app.bitcoincore."+action, bitcoinCoreAppID, map[string]any{
			"status": outcome, "http_status": status, "catalog_version": appmanifest.BitcoinCoreRelease,
		})
	}
}
