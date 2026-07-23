package server

import (
	"context"
	"net/http"
	"time"
)

func (s *Server) handleMenuPreferencesGet(w http.ResponseWriter, r *http.Request) {
	service, errMsg := s.uiPreferencesService()
	if service == nil {
		if errMsg == "" {
			errMsg = "ui preferences unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	preferences, err := service.LoadMenu(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, preferences)
}

func (s *Server) handleMenuPreferencesPut(w http.ResponseWriter, r *http.Request) {
	service, errMsg := s.uiPreferencesService()
	if service == nil {
		if errMsg == "" {
			errMsg = "ui preferences unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	var preferences MenuPreferences
	if err := readJSON(r, &preferences); err != nil {
		writeError(w, http.StatusBadRequest, "invalid menu preferences")
		return
	}
	normalized, err := normalizeMenuPreferences(preferences)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	saved, err := service.SaveMenu(ctx, normalized)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}
