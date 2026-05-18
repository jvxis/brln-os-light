package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/electrs"
)

func (s *Server) handleProvenanceStatus(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.provenanceService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	state, err := svc.Status(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleProvenanceGraph(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.provenanceService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}
	opts := LoadGraphOptions{
		Mode: strings.TrimSpace(r.URL.Query().Get("mode")),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			opts.Limit = parsed
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("root")); raw != "" {
		// Accept either "txid" or "txid:vout"; we only need the txid for lineage.
		if idx := strings.IndexByte(raw, ':'); idx > 0 {
			raw = raw[:idx]
		}
		opts.RootTxid = strings.ToLower(raw)
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("hops")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			opts.Hops = parsed
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("include_external")); raw != "" {
		if parsed, err := strconv.ParseBool(raw); err == nil {
			opts.IncludeExternal = parsed
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("max_external")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			opts.MaxExternalTxs = parsed
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	graph, err := svc.LoadGraph(ctx, opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, graph)
}

// handleProvenanceHealth reports whether electrs is reachable. The UI uses
// this to decide whether to render the Wallet Flow page at all.
func (s *Server) handleProvenanceHealth(w http.ResponseWriter, r *http.Request) {
	client := electrs.New("")
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	info, err := client.Ping(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"electrs_available": false,
			"electrs_addr":      client.Addr(),
			"error":             err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"electrs_available": true,
		"electrs_addr":      client.Addr(),
		"electrs_server":    info[0],
		"electrs_protocol":  info[1],
	})
}

func (s *Server) handleProvenanceRebuild(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.provenanceService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	fullRebuild := false
	if raw := strings.TrimSpace(r.URL.Query().Get("full")); raw != "" {
		if parsed, err := strconv.ParseBool(raw); err == nil {
			fullRebuild = parsed
		}
	}

	// The walk + electrs enrichment can take a while on big wallets. Cap
	// generously; the client should poll /api/onchain/provenance/status to
	// see when the sync_at timestamp moves.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	go func() {
		defer cancel()
		if err := svc.RefreshNow(ctx, fullRebuild); err != nil {
			s.logger.Printf("provenance: refresh failed: %v", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true, "full": fullRebuild})
}
