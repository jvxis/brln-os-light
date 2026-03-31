package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleGraphExplorerStatusGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.graphExplorerService()
	if svc == nil {
		msg := strings.TrimSpace(errMsg)
		if msg == "" {
			msg = "graph explorer unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, msg)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	status, err := svc.Status(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load graph explorer status")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleGraphExplorerRecomputePost(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.graphExplorerService()
	if svc == nil {
		msg := strings.TrimSpace(errMsg)
		if msg == "" {
			msg = "graph explorer unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, msg)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), graphExplorerRefreshTimeout)
	defer cancel()
	if err := svc.Refresh(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to recompute graph explorer state")
		return
	}

	status, err := svc.Status(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load graph explorer status")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"status": status,
	})
}

func (s *Server) handleGraphExplorerSearchGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.graphExplorerService()
	if svc == nil {
		msg := strings.TrimSpace(errMsg)
		if msg == "" {
			msg = "graph explorer unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, msg)
		return
	}

	limit := 10
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	response, err := svc.SearchNodes(ctx, r.URL.Query().Get("q"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to search graph explorer nodes")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleGraphExplorerGeneralGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.graphExplorerService()
	if svc == nil {
		msg := strings.TrimSpace(errMsg)
		if msg == "" {
			msg = "graph explorer unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, msg)
		return
	}

	pubkey := strings.TrimSpace(chi.URLParam(r, "pubkey"))
	if decoded, err := url.PathUnescape(pubkey); err == nil {
		pubkey = strings.TrimSpace(decoded)
	}
	if pubkey == "" {
		writeError(w, http.StatusBadRequest, "pubkey required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	response, err := svc.GetNodeGeneral(ctx, pubkey)
	if err != nil {
		if errors.Is(err, ErrGraphExplorerNodeNotFound) {
			writeError(w, http.StatusNotFound, "graph explorer node not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load graph explorer node")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleGraphExplorerChannelsGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.graphExplorerService()
	if svc == nil {
		msg := strings.TrimSpace(errMsg)
		if msg == "" {
			msg = "graph explorer unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, msg)
		return
	}

	pubkey := strings.TrimSpace(chi.URLParam(r, "pubkey"))
	if decoded, err := url.PathUnescape(pubkey); err == nil {
		pubkey = strings.TrimSpace(decoded)
	}
	if pubkey == "" {
		writeError(w, http.StatusBadRequest, "pubkey required")
		return
	}

	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	response, err := svc.ListNodeChannels(ctx, pubkey, limit)
	if err != nil {
		if errors.Is(err, ErrGraphExplorerNodeNotFound) {
			writeError(w, http.StatusNotFound, "graph explorer node not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load graph explorer channels")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleGraphExplorerClosedGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.graphExplorerService()
	if svc == nil {
		msg := strings.TrimSpace(errMsg)
		if msg == "" {
			msg = "graph explorer unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, msg)
		return
	}

	pubkey := strings.TrimSpace(chi.URLParam(r, "pubkey"))
	if decoded, err := url.PathUnescape(pubkey); err == nil {
		pubkey = strings.TrimSpace(decoded)
	}
	if pubkey == "" {
		writeError(w, http.StatusBadRequest, "pubkey required")
		return
	}

	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	response, err := svc.ListNodeClosedChannels(ctx, pubkey, r.URL.Query().Get("range"), limit)
	if err != nil {
		if errors.Is(err, ErrGraphExplorerNodeNotFound) {
			writeError(w, http.StatusNotFound, "graph explorer node not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load graph explorer closed channels")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleGraphExplorerFeesGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.graphExplorerService()
	if svc == nil {
		msg := strings.TrimSpace(errMsg)
		if msg == "" {
			msg = "graph explorer unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, msg)
		return
	}

	pubkey := strings.TrimSpace(chi.URLParam(r, "pubkey"))
	if decoded, err := url.PathUnescape(pubkey); err == nil {
		pubkey = strings.TrimSpace(decoded)
	}
	if pubkey == "" {
		writeError(w, http.StatusBadRequest, "pubkey required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	response, err := svc.GetNodeFeeReport(ctx, pubkey, r.URL.Query().Get("range"))
	if err != nil {
		if errors.Is(err, ErrGraphExplorerNodeNotFound) {
			writeError(w, http.StatusNotFound, "graph explorer node not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load graph explorer fee report")
		return
	}
	writeJSON(w, http.StatusOK, response)
}
