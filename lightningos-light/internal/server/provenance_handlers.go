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

// handleProvenanceHealth reports whether any tx source in the provenance
// chain is reachable. The UI uses `ok` to gate the Wallet Flow tab,
// `backend` for the source badge, and `no_txindex_hint` for the one-time
// txindex banner. The legacy fields `active` and `electrs_available` are
// kept as aliases for one release so older UIs keep working.
func (s *Server) handleProvenanceHealth(w http.ResponseWriter, r *http.Request) {
	s.initProvenance()

	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()

	chain := s.provenanceChain
	if chain == nil {
		// Fall back to the legacy single-client probe so the UI can still
		// surface electrs status even when init failed partway.
		client := electrs.New("")
		if _, err := client.Ping(ctx); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":                false,
				"electrs_addr":      client.Addr(),
				"electrs_available": false,
				"error":             err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":                true,
			"backend":           "electrs",
			"active":            "local electrs",
			"electrs_addr":      client.Addr(),
			"electrs_available": true,
		})
		return
	}

	sources := []map[string]any{}
	anyOk := false
	backendName := ""
	for _, src := range chain.Sources() {
		entry := map[string]any{"name": src.Name()}
		cctx, ccancel := context.WithTimeout(ctx, 4*time.Second)
		ok := src.Available(cctx)
		ccancel()
		entry["available"] = ok
		if ok {
			if !anyOk {
				backendName = src.Name()
			}
			anyOk = true
		}
		sources = append(sources, entry)
	}
	if last := chain.LastGood(); last != nil {
		backendName = last.Name()
	}

	hint := false
	if s.provenanceBitcoind != nil {
		hint = s.provenanceBitcoind.NoTxIndexHint()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                anyOk,
		"backend":           backendName, // preferred field
		"active":            backendName, // legacy alias; remove after one release
		"sources":           sources,
		"no_txindex_hint":   hint,
		"electrs_available": anyOk, // legacy field for older UIs
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
