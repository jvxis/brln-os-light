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

// Commitments are read by the channel list, which must not fail when the Magma
// app is absent or unhealthy: an empty list simply means no badge.
func (s *Server) handleMagmaCommitments(w http.ResponseWriter, r *http.Request) {
	svc, _ := s.magmaService()
	if svc == nil {
		writeJSON(w, http.StatusOK, map[string]any{"commitments": []MagmaChannelCommitment{}})
		return
	}
	items, err := svc.ActiveCommitments(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"commitments": []MagmaChannelCommitment{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"commitments": items})
}

func (s *Server) handleMagmaEvents(w http.ResponseWriter, r *http.Request) {
	svc := s.requireMagmaService(w)
	if svc == nil {
		return
	}
	limit := 60
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	events, err := svc.ListRecentEvents(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
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
	if s.rejectLNDMaintenanceAction(w, r, "Magma settings update") {
		return
	}
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

// magmaActionStatus maps service errors onto HTTP codes. State conflicts are 409
// rather than 400: the request was well formed, the order simply moved on.
func magmaActionStatus(err error) int {
	switch {
	case errors.Is(err, errMagmaUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, errMagmaNotFound):
		return http.StatusNotFound
	case errors.Is(err, errMagmaWrongState):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

func (s *Server) handleMagmaAcceptOrder(w http.ResponseWriter, r *http.Request) {
	svc := s.requireMagmaService(w)
	if svc == nil {
		return
	}
	orderID := strings.TrimSpace(chi.URLParam(r, "id"))
	if orderID == "" {
		writeError(w, http.StatusBadRequest, "order id required")
		return
	}
	order, err := svc.AcceptOrder(r.Context(), orderID)
	if err != nil {
		writeError(w, magmaActionStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func (s *Server) handleMagmaRejectOrder(w http.ResponseWriter, r *http.Request) {
	svc := s.requireMagmaService(w)
	if svc == nil {
		return
	}
	orderID := strings.TrimSpace(chi.URLParam(r, "id"))
	if orderID == "" {
		writeError(w, http.StatusBadRequest, "order id required")
		return
	}
	order, err := svc.RejectOrder(r.Context(), orderID)
	if err != nil {
		writeError(w, magmaActionStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func (s *Server) handleMagmaOpenPreview(w http.ResponseWriter, r *http.Request) {
	svc := s.requireMagmaService(w)
	if svc == nil {
		return
	}
	orderID := strings.TrimSpace(chi.URLParam(r, "id"))
	if orderID == "" {
		writeError(w, http.StatusBadRequest, "order id required")
		return
	}
	var satPerVbyte int64
	if raw := strings.TrimSpace(r.URL.Query().Get("sat_per_vbyte")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
			satPerVbyte = parsed
		}
	}
	preview, err := svc.OpenChannelPreview(r.Context(), orderID, satPerVbyte)
	if err != nil {
		writeError(w, magmaActionStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleMagmaOpenChannel(w http.ResponseWriter, r *http.Request) {
	if s.rejectLNDMaintenanceAction(w, r, "Magma channel open") {
		return
	}
	svc := s.requireMagmaService(w)
	if svc == nil {
		return
	}
	orderID := strings.TrimSpace(chi.URLParam(r, "id"))
	if orderID == "" {
		writeError(w, http.StatusBadRequest, "order id required")
		return
	}
	var req MagmaOpenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !s.requireLightningFundsReauth(w, r, req.ConfirmPassword) {
		return
	}
	order, err := svc.OpenChannelForOrder(r.Context(), orderID, req)
	if err != nil {
		s.recordAuditEventAsync(r, "channel.magma_open.failed", orderID, map[string]any{
			"sat_per_vbyte": req.SatPerVbyte,
		})
		writeError(w, magmaActionStatus(err), err.Error())
		return
	}
	s.recordAuditEventAsync(r, "channel.magma_open.submitted", orderID, map[string]any{
		"sat_per_vbyte": req.SatPerVbyte,
		"state":         order.LocalState,
	})
	writeJSON(w, http.StatusOK, order)
}

func (s *Server) handleMagmaPolicyPost(w http.ResponseWriter, r *http.Request) {
	svc := s.requireMagmaService(w)
	if svc == nil {
		return
	}
	// Decoding onto the stored policy means an omitted field keeps its current
	// value instead of silently resetting to the zero value, which for a ceiling
	// would mean "no limit".
	policy, err := svc.PolicyForUpdate(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	saved, err := svc.UpdatePolicy(r.Context(), policy)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

// Backfill is split into a GET that only reports and a POST that writes, so
// looking is never one typo away from changing historical revenue.
func (s *Server) handleMagmaBackfillPreview(w http.ResponseWriter, r *http.Request) {
	svc := s.requireMagmaService(w)
	if svc == nil {
		return
	}
	report, err := svc.BackfillRevenue(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleMagmaBackfillApply(w http.ResponseWriter, r *http.Request) {
	svc := s.requireMagmaService(w)
	if svc == nil {
		return
	}
	report, err := svc.BackfillRevenue(r.Context(), true)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleMagmaOffersGet(w http.ResponseWriter, r *http.Request) {
	svc := s.requireMagmaService(w)
	if svc == nil {
		return
	}
	view, err := svc.ListOffers(r.Context())
	if err != nil {
		writeError(w, magmaActionStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleMagmaOfferSave(w http.ResponseWriter, r *http.Request) {
	svc := s.requireMagmaService(w)
	if svc == nil {
		return
	}
	var offer MagmaOffer
	if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	view, err := svc.SaveOffer(r.Context(), offer)
	if err != nil {
		writeError(w, magmaActionStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleMagmaOfferToggle(w http.ResponseWriter, r *http.Request) {
	svc := s.requireMagmaService(w)
	if svc == nil {
		return
	}
	offerID := strings.TrimSpace(chi.URLParam(r, "id"))
	if offerID == "" {
		writeError(w, http.StatusBadRequest, "offer id required")
		return
	}
	view, err := svc.ToggleOffer(r.Context(), offerID)
	if err != nil {
		writeError(w, magmaActionStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
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
