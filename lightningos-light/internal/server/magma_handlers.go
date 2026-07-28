package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (s *Server) requireMagmaService(w http.ResponseWriter) *MagmaService {
	svc, reason := s.magmaService()
	if svc == nil {
		if strings.TrimSpace(reason) == "" {
			reason = "Magma Inbound Sales unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, reason)
	}
	return svc
}

func (s *Server) handleMagmaOverview(w http.ResponseWriter, r *http.Request) {
	svc := s.requireMagmaService(w)
	if svc == nil {
		return
	}
	overview, err := svc.Overview(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) handleMagmaOrders(w http.ResponseWriter, r *http.Request) {
	svc := s.requireMagmaService(w)
	if svc == nil {
		return
	}
	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	orders, err := svc.ListOrders(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": orders})
}

func (s *Server) handleMagmaOrderEvents(w http.ResponseWriter, r *http.Request) {
	svc := s.requireMagmaService(w)
	if svc == nil {
		return
	}
	orderID := strings.TrimSpace(chi.URLParam(r, "id"))
	if orderID == "" {
		writeError(w, http.StatusBadRequest, "order id required")
		return
	}
	events, err := svc.ListOrderEvents(r.Context(), orderID, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) handleMagmaSettingsPost(w http.ResponseWriter, r *http.Request) {
	svc := s.requireMagmaService(w)
	if svc == nil {
		return
	}
	var update MagmaSettingsUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	settings, err := svc.UpdateSettings(r.Context(), update)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleMagmaRefresh(w http.ResponseWriter, r *http.Request) {
	svc := s.requireMagmaService(w)
	if svc == nil {
		return
	}
	if err := svc.RefreshNow(r.Context()); err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, errMagmaUnauthorized) {
			status = http.StatusUnauthorized
		}
		writeError(w, status, err.Error())
		return
	}
	overview, err := svc.Overview(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, overview)
}
