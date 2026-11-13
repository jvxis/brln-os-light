package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"lightningos-light/internal/privileged"
	"lightningos-light/internal/system"
)

// LND REST over Tor, for Lightning Address services such as pay.br-ln.com.
// The broker owns the torrc block, the Tor reload and the onion hostname; this
// handler only asks for the fixed status or the fixed ensure operation.

type torLNDRESTResponse struct {
	Status        string `json:"status"`
	Hostname      string `json:"hostname,omitempty"`
	URL           string `json:"url,omitempty"`
	Managed       bool   `json:"managed,omitempty"`
	RESTReachable bool   `json:"rest_reachable"`
}

func torLNDRESTResponseFrom(state privileged.TorLNDRESTState) torLNDRESTResponse {
	resp := torLNDRESTResponse{Status: state.Status, Hostname: state.Hostname, Managed: state.Managed, RESTReachable: state.RESTReachable}
	if state.Hostname != "" {
		resp.URL = "https://" + state.Hostname + ":8080"
	}
	return resp
}

var torLNDRESTBroker = system.TorLNDRESTWithBroker

func (s *Server) handleTorLNDRESTStatus(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	state, handled, err := torLNDRESTBroker(ctx, false)
	if !handled {
		writeError(w, http.StatusServiceUnavailable, "privileged broker is required for the LND REST onion")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, torLNDRESTResponseFrom(state))
}

func (s *Server) handleTorLNDRESTEnsure(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	var req struct {
		Confirm         bool   `json:"confirm"`
		ConfirmPassword string `json:"confirm_password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || !req.Confirm {
		writeError(w, http.StatusBadRequest, "explicit confirmation is required")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !s.requireSensitiveReauth(w, r, authScopeLNDConfig, req.ConfirmPassword, "lnd_config_reauth_required", "password confirmation required to expose LND REST over Tor") {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 75*time.Second)
	defer cancel()
	state, handled, err := torLNDRESTBroker(ctx, true)
	if !handled {
		writeError(w, http.StatusServiceUnavailable, "privileged broker is required for the LND REST onion")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, torLNDRESTResponseFrom(state))
}
