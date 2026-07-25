package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

const loopOutBRLNReauthRequiredCode = "loopout_brln_reauth_required"

func (s *Server) requireLoopOutBRLNService(w http.ResponseWriter) *LoopOutBRLNService {
	svc, reason := s.loopOutBRLNService()
	if svc == nil {
		if strings.TrimSpace(reason) == "" {
			reason = "Loop Out BRLN unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, reason)
	}
	return svc
}

func (s *Server) handleLoopOutBRLNStatus(w http.ResponseWriter, r *http.Request) {
	svc := s.requireLoopOutBRLNService(w)
	if svc == nil {
		return
	}
	status, err := svc.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleLoopOutBRLNValidateAddress(w http.ResponseWriter, r *http.Request) {
	svc := s.requireLoopOutBRLNService(w)
	if svc == nil {
		return
	}
	var req struct {
		LightningAddress string `json:"lightning_address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	address := strings.ToLower(strings.TrimSpace(req.LightningAddress))
	if _, _, err := splitLightningAddress(address); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	payInfo, err := inspectLightningAddress(r.Context(), address)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, LoopOutBRLNAddressValidation{
		LightningAddress: address,
		MinSendableMsat:  payInfo.MinSendable,
		MaxSendableMsat:  payInfo.MaxSendable,
		CommentAllowed:   payInfo.CommentAllowed,
	})
}

func (s *Server) handleLoopOutBRLNPreview(w http.ResponseWriter, r *http.Request) {
	svc := s.requireLoopOutBRLNService(w)
	if svc == nil {
		return
	}
	var req LoopOutBRLNRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	normalized, err := normalizeLoopOutBRLNRequest(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	payInfo, err := inspectLightningAddress(r.Context(), normalized.LightningAddress)
	if err != nil {
		writeError(w, http.StatusBadRequest, "lightning address: "+err.Error())
		return
	}
	if err := validateLoopOutBRLNProvider(payInfo, normalized); err != nil {
		writeError(w, http.StatusBadRequest, "lightning address: "+err.Error())
		return
	}
	preview, err := svc.Preview(r.Context(), normalized)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleLoopOutBRLNJobs(w http.ResponseWriter, r *http.Request) {
	svc := s.requireLoopOutBRLNService(w)
	if svc == nil {
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 200")
			return
		}
		limit = parsed
	}
	jobs, err := svc.ListJobs(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (s *Server) handleLoopOutBRLNCreateJob(w http.ResponseWriter, r *http.Request) {
	svc := s.requireLoopOutBRLNService(w)
	if svc == nil {
		return
	}
	var req LoopOutBRLNRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !s.requireSensitiveReauth(w, r, authScopeLoopOutBRLN, req.ConfirmPassword,
		loopOutBRLNReauthRequiredCode, "password confirmation required before starting Loop Out BRLN") {
		return
	}
	normalized, err := normalizeLoopOutBRLNRequest(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	payInfo, err := inspectLightningAddress(r.Context(), normalized.LightningAddress)
	if err != nil {
		writeError(w, http.StatusBadRequest, "lightning address: "+err.Error())
		return
	}
	if err := validateLoopOutBRLNProvider(payInfo, normalized); err != nil {
		writeError(w, http.StatusBadRequest, "lightning address: "+err.Error())
		return
	}
	job, err := svc.CreateJob(r.Context(), normalized)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.recordAuditEvent(r, "loopout_brln.job.start", strconv.FormatInt(job.ID, 10), map[string]any{
		"lightning_address": job.LightningAddress,
		"total_sat":         job.TotalSat, "tranche_sat": job.TrancheSat, "max_fee_ppm": job.MaxFeePPM,
		"min_local_percent": job.MinLocalPercent, "suppress_failed_telegram": job.SuppressFailedTelegram,
	})
	writeJSON(w, http.StatusAccepted, job)
}

func validateLoopOutBRLNProvider(payInfo lnurlPayResponse, req LoopOutBRLNRequest) error {
	_, lastTranche := loopOutBRLNParts(req.TotalSat, req.TrancheSat)
	if err := validateLNURLPayParameters(payInfo, req.TrancheSat, req.Comment); err != nil {
		return err
	}
	if lastTranche != req.TrancheSat {
		if err := validateLNURLPayParameters(payInfo, lastTranche, req.Comment); err != nil {
			return errors.New("final payment: " + err.Error())
		}
	}
	return nil
}

func parseLoopOutBRLNJobID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid job id")
	}
	return id, nil
}

func (s *Server) handleLoopOutBRLNJobDetail(w http.ResponseWriter, r *http.Request) {
	svc := s.requireLoopOutBRLNService(w)
	if svc == nil {
		return
	}
	id, err := parseLoopOutBRLNJobID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	detail, err := svc.JobDetail(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleLoopOutBRLNPauseJob(w http.ResponseWriter, r *http.Request) {
	s.handleLoopOutBRLNJobAction(w, r, "pause")
}

func (s *Server) handleLoopOutBRLNResumeJob(w http.ResponseWriter, r *http.Request) {
	s.handleLoopOutBRLNJobAction(w, r, "resume")
}

func (s *Server) handleLoopOutBRLNCancelJob(w http.ResponseWriter, r *http.Request) {
	s.handleLoopOutBRLNJobAction(w, r, "cancel")
}

func (s *Server) handleLoopOutBRLNJobAction(w http.ResponseWriter, r *http.Request, action string) {
	svc := s.requireLoopOutBRLNService(w)
	if svc == nil {
		return
	}
	id, err := parseLoopOutBRLNJobID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var payload struct {
		ConfirmPassword string `json:"confirm_password"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&payload)
	}
	if action == "resume" && !s.requireSensitiveReauth(w, r, authScopeLoopOutBRLN, payload.ConfirmPassword,
		loopOutBRLNReauthRequiredCode, "password confirmation required before resuming Loop Out BRLN") {
		return
	}
	var job LoopOutBRLNJob
	switch action {
	case "pause":
		job, err = svc.PauseJob(r.Context(), id)
	case "resume":
		job, err = svc.ResumeJob(r.Context(), id)
	case "cancel":
		job, err = svc.CancelJob(r.Context(), id)
	default:
		err = errors.New("unsupported action")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.recordAuditEvent(r, "loopout_brln.job."+action, strconv.FormatInt(id, 10), map[string]any{"status": job.Status})
	writeJSON(w, http.StatusOK, job)
}
