package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleBalancedOpenStatusGet(w http.ResponseWriter, r *http.Request) {
	enabled := s.cfg.Features.BalancedOpenEnabled()
	if !enabled {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":   false,
			"available": false,
			"error":     "balanced open disabled",
		})
		return
	}

	svc, errMsg := s.balancedOpenService()
	available := svc != nil && errMsg == ""

	payload := map[string]any{
		"enabled":   true,
		"available": available,
	}
	if errMsg != "" {
		payload["error"] = errMsg
	}
	if available {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		details, detailsErr := s.lnd.GetWalletBalanceDetails(ctx)
		cancel()
		if detailsErr == nil {
			payload["wallet"] = map[string]int64{
				"total_sat":               details.TotalSat,
				"confirmed_sat":           details.ConfirmedSat,
				"unconfirmed_sat":         details.UnconfirmedSat,
				"locked_sat":              details.LockedSat,
				"reserved_anchor_sat":     details.ReservedAnchorSat,
				"estimated_spendable_sat": details.EstimatedSpendableSat,
			}
		} else {
			payload["wallet_error"] = lndStatusMessage(detailsErr)
		}
	}

	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleBalancedOpenSessionsGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.balancedOpenService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "balanced open unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	limit := balancedOpenDefaultListLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = n
	}

	filter := BalancedOpenListFilter{
		Limit:      limit,
		State:      strings.TrimSpace(r.URL.Query().Get("state")),
		Role:       strings.TrimSpace(r.URL.Query().Get("role")),
		PeerPubkey: strings.TrimSpace(r.URL.Query().Get("peer_pubkey")),
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	items, err := svc.ListSessions(ctx, filter)
	if err != nil {
		switch {
		case errors.Is(err, ErrBalancedOpenInvalidState), errors.Is(err, ErrBalancedOpenInvalidRole), errors.Is(err, ErrBalancedOpenInvalidPeerKey):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleBalancedOpenSessionsPost(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.balancedOpenService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "balanced open unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	var req struct {
		PeerAddress  string `json:"peer_address"`
		CapacitySat  int64  `json:"capacity_sat"`
		FeeRateSatVb int64  `json:"fee_rate_sat_vb"`
		Private      bool   `json:"private"`
		CloseAddress string `json:"close_address"`
		Role         string `json:"role"`
		Metadata     any    `json:"metadata"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	pubkey, host, err := parseBalancedOpenPeer(req.PeerAddress)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var rawMetadata []byte
	if req.Metadata != nil {
		rawMetadata, err = marshalBalancedOpenJSON(req.Metadata)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid metadata")
			return
		}
	}

	params := BalancedOpenCreateParams{
		PeerPubkey:   pubkey,
		PeerHost:     host,
		CapacitySat:  req.CapacitySat,
		FeeRateSatVb: req.FeeRateSatVb,
		Private:      req.Private,
		CloseAddress: req.CloseAddress,
		Role:         req.Role,
		Metadata:     rawMetadata,
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	session, err := svc.CreateSession(ctx, params)
	if err != nil {
		switch {
		case errors.Is(err, ErrBalancedOpenInvalidPeerKey),
			errors.Is(err, ErrBalancedOpenInvalidCapacity),
			errors.Is(err, ErrBalancedOpenInvalidFeeRate),
			errors.Is(err, ErrBalancedOpenInvalidRole):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) handleBalancedOpenSessionGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.balancedOpenService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "balanced open unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	sessionID := strings.TrimSpace(chi.URLParam(r, "id"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session id required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	session, err := svc.GetSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, ErrBalancedOpenSessionNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleBalancedOpenSessionEventsGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.balancedOpenService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "balanced open unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	sessionID := strings.TrimSpace(chi.URLParam(r, "id"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session id required")
		return
	}

	limit := balancedOpenDefaultEventList
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = n
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	events, err := svc.ListSessionEvents(ctx, sessionID, limit)
	if err != nil {
		switch {
		case errors.Is(err, ErrBalancedOpenSessionNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": events,
	})
}

func (s *Server) handleBalancedOpenSessionCancelPost(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.balancedOpenService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "balanced open unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	sessionID := strings.TrimSpace(chi.URLParam(r, "id"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session id required")
		return
	}

	var req struct {
		Reason string `json:"reason"`
	}
	if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	session, err := svc.CancelSession(ctx, sessionID, req.Reason)
	if err != nil {
		switch {
		case errors.Is(err, ErrBalancedOpenSessionNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, ErrBalancedOpenTerminalState):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleBalancedOpenSessionProposePost(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.balancedOpenService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "balanced open unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	sessionID := strings.TrimSpace(chi.URLParam(r, "id"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session id required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	session, err := svc.ProposeSession(ctx, sessionID)
	if err != nil {
		switch {
		case errors.Is(err, ErrBalancedOpenSessionNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, ErrBalancedOpenInvalidAction),
			errors.Is(err, ErrBalancedOpenInvalidState),
			errors.Is(err, ErrBalancedOpenInvalidRole),
			errors.Is(err, ErrBalancedOpenInsufficientOnchainSafety):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, ErrBalancedOpenTerminalState):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleBalancedOpenSessionAcceptPost(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.balancedOpenService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "balanced open unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	sessionID := strings.TrimSpace(chi.URLParam(r, "id"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session id required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	session, err := svc.AcceptSession(ctx, sessionID)
	if err != nil {
		switch {
		case errors.Is(err, ErrBalancedOpenSessionNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, ErrBalancedOpenInvalidAction),
			errors.Is(err, ErrBalancedOpenInvalidState),
			errors.Is(err, ErrBalancedOpenInvalidRole),
			errors.Is(err, ErrBalancedOpenInsufficientOnchainSafety):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, ErrBalancedOpenTerminalState):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleBalancedOpenSessionExecutePost(w http.ResponseWriter, r *http.Request) {
	if s.rejectLNDMaintenanceAction(w, r, "balanced channel open") {
		return
	}
	var req struct {
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := readOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !s.requireLightningFundsReauth(w, r, req.ConfirmPassword) {
		return
	}

	svc, errMsg := s.balancedOpenService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "balanced open unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	sessionID := strings.TrimSpace(chi.URLParam(r, "id"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session id required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	session, err := svc.ExecuteSession(ctx, sessionID)
	if err != nil {
		s.recordAuditEventAsync(r, "channel.balanced_open_execute.failed", sessionID, nil)
		switch {
		case errors.Is(err, ErrBalancedOpenSessionNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, ErrBalancedOpenInvalidAction),
			errors.Is(err, ErrBalancedOpenInvalidState),
			errors.Is(err, ErrBalancedOpenInvalidRole),
			errors.Is(err, ErrBalancedOpenInsufficientOnchainSafety):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, ErrBalancedOpenTerminalState):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	s.recordAuditEventAsync(r, "channel.balanced_open_execute.submitted", sessionID, map[string]any{
		"state": session.State,
		"role":  session.Role,
	})

	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleBalancedOpenSessionRetryBroadcastPost(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.balancedOpenService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "balanced open unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	sessionID := strings.TrimSpace(chi.URLParam(r, "id"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session id required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	session, err := svc.RetrySessionBroadcast(ctx, sessionID)
	if err != nil {
		switch {
		case errors.Is(err, ErrBalancedOpenSessionNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, ErrBalancedOpenInvalidAction),
			errors.Is(err, ErrBalancedOpenInvalidState),
			errors.Is(err, ErrBalancedOpenInvalidRole):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, ErrBalancedOpenTerminalState):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleBalancedOpenSessionRecoverPost(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.balancedOpenService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "balanced open unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	sessionID := strings.TrimSpace(chi.URLParam(r, "id"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session id required")
		return
	}

	var req struct {
		SatPerVbyte int64 `json:"sat_per_vbyte"`
	}
	if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	session, err := svc.RecoverSessionTransit(ctx, sessionID, req.SatPerVbyte)
	if err != nil {
		switch {
		case errors.Is(err, ErrBalancedOpenSessionNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, ErrBalancedOpenInvalidAction),
			errors.Is(err, ErrBalancedOpenInvalidState),
			errors.Is(err, ErrBalancedOpenInvalidRole),
			errors.Is(err, ErrBalancedOpenInvalidFeeRate),
			errors.Is(err, ErrBalancedOpenInsufficientOnchainSafety),
			errors.Is(err, ErrBalancedOpenTransitOutpointUnavailable):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, ErrBalancedOpenTerminalState):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, session)
}

func parseBalancedOpenPeer(value string) (string, string, error) {
	peer := strings.TrimSpace(value)
	if peer == "" {
		return "", "", errors.New("peer_address required")
	}

	if strings.Contains(peer, "@") {
		parts := strings.SplitN(peer, "@", 2)
		pubkey := strings.ToLower(strings.TrimSpace(parts[0]))
		host := strings.TrimSpace(parts[1])
		if !isValidPubkeyHex(pubkey) {
			return "", "", errors.New("invalid peer pubkey")
		}
		if host == "" {
			return "", "", errors.New("invalid peer host")
		}
		return pubkey, host, nil
	}

	pubkey := strings.ToLower(peer)
	if !isValidPubkeyHex(pubkey) {
		return "", "", errors.New("invalid peer pubkey")
	}
	return pubkey, "", nil
}
