package server

import (
	"context"
	"net/http"
	"time"
)

func (s *Server) handleBIP110Monitor(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), bip110RequestTimeout)
	defer cancel()

	if s.bip110Monitor == nil {
		writeJSON(w, http.StatusOK, unavailableBIP110Status(time.Now(), nil))
		return
	}
	writeJSON(w, http.StatusOK, s.bip110Monitor.status(ctx))
}
