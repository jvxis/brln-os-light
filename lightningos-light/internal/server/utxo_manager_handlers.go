package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"

	"github.com/go-chi/chi/v5"
)

const (
	utxoLeaseDefaultExpirySec uint64 = 30 * 24 * 60 * 60 // 30 days
	utxoLeaseMaxExpirySec     uint64 = 365 * 24 * 60 * 60
)

// auditUtxoLockAction emits an audit log for lock/unlock actions so that
// silent coin-selection blocks (which could starve autopilot/rebalance) can
// be traced back to a session. Lock/unlock don't require reauth — they're
// reversible and don't spend sats — but they are logged.
func (s *Server) auditUtxoLockAction(r *http.Request, action string, outpoints []string, expirySec uint64) {
	if s == nil || s.logger == nil {
		return
	}
	user := "anonymous"
	if session, ok := authSessionFromContext(r.Context()); ok {
		user = session.ID
	}
	if expirySec > 0 {
		s.logger.Printf("utxo %s by session=%s outpoints=%v expiry_sec=%d", action, user, outpoints, expirySec)
	} else {
		s.logger.Printf("utxo %s by session=%s outpoints=%v", action, user, outpoints)
	}
}

// EnrichedOnchainUtxo is the wire shape returned by GET /api/onchain/utxos.
// It joins LND's UTXO data with our local metadata and lease state.
type EnrichedOnchainUtxo struct {
	lndclient.OnchainUtxo
	Label            string `json:"label,omitempty"`
	Tag              string `json:"tag,omitempty"`
	Color            string `json:"color,omitempty"`
	GroupID          string `json:"group_id,omitempty"`
	Locked           bool   `json:"locked"`
	LeaseExpiration  uint64 `json:"lease_expiration,omitempty"`
	LeaseManagedHere bool   `json:"lease_managed_here,omitempty"`
}

// enrichOnchainUtxos merges UTXO metadata + lease state onto LND's list. It
// also opportunistically prunes metadata rows for outpoints that no longer
// exist in the wallet (auto-prune contract).
func (s *Server) enrichOnchainUtxos(ctx context.Context, items []lndclient.OnchainUtxo) []EnrichedOnchainUtxo {
	enriched := make([]EnrichedOnchainUtxo, 0, len(items))
	for _, item := range items {
		enriched = append(enriched, EnrichedOnchainUtxo{OnchainUtxo: item})
	}

	leases, leaseErr := s.lnd.ListLeases(ctx)
	if leaseErr != nil {
		s.logger.Printf("utxo manager: list leases failed: %v", leaseErr)
	} else {
		for i := range enriched {
			outpoint := strings.ToLower(strings.TrimSpace(enriched[i].Outpoint))
			if outpoint == "" {
				continue
			}
			if lease, ok := leases[outpoint]; ok {
				enriched[i].Locked = true
				enriched[i].LeaseExpiration = lease.Expiration
				// The lease ID is deterministic from the outpoint when we
				// created it; report whether this lease was ours.
				enriched[i].LeaseManagedHere = lease.ID != ""
			}
		}
	}

	svc, _ := s.utxoManagerService()
	if svc == nil {
		return enriched
	}

	metadata, err := svc.ListMetadata(ctx)
	if err != nil {
		s.logger.Printf("utxo manager: list metadata failed: %v", err)
		return enriched
	}

	live := make([]string, 0, len(items))
	for _, item := range items {
		live = append(live, item.Outpoint)
	}

	for i := range enriched {
		outpoint := strings.ToLower(strings.TrimSpace(enriched[i].Outpoint))
		meta, ok := metadata[outpoint]
		if !ok {
			continue
		}
		enriched[i].Label = meta.Label
		enriched[i].Tag = meta.Tag
		enriched[i].Color = meta.Color
		enriched[i].GroupID = meta.GroupID
	}

	// Best-effort prune; failures shouldn't block the read path.
	pruneCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := svc.Prune(pruneCtx, live); err != nil {
		s.logger.Printf("utxo manager: prune failed: %v", err)
	}

	return enriched
}

func (s *Server) handleUtxoMetadataUpsert(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.utxoManagerService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	var req struct {
		Outpoint string  `json:"outpoint"`
		Label    *string `json:"label,omitempty"`
		Tag      *string `json:"tag,omitempty"`
		Color    *string `json:"color,omitempty"`
		GroupID  *string `json:"group_id,omitempty"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if strings.TrimSpace(req.Outpoint) == "" {
		writeError(w, http.StatusBadRequest, "outpoint required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := svc.UpsertMetadata(ctx, UtxoMetadataUpdate{
		Outpoint: req.Outpoint,
		Label:    req.Label,
		Tag:      req.Tag,
		Color:    req.Color,
		GroupID:  req.GroupID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleUtxoLock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Outpoints []string `json:"outpoints"`
		ExpirySec uint64   `json:"expiry_sec"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(req.Outpoints) == 0 {
		writeError(w, http.StatusBadRequest, "outpoints required")
		return
	}
	expiry := req.ExpirySec
	if expiry == 0 {
		expiry = utxoLeaseDefaultExpirySec
	}
	if expiry > utxoLeaseMaxExpirySec {
		expiry = utxoLeaseMaxExpirySec
	}

	s.auditUtxoLockAction(r, "lock", req.Outpoints, expiry)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	type leaseResult struct {
		Outpoint   string `json:"outpoint"`
		Expiration uint64 `json:"expiration,omitempty"`
		Error      string `json:"error,omitempty"`
	}
	results := make([]leaseResult, 0, len(req.Outpoints))
	for _, outpoint := range req.Outpoints {
		info, err := s.lnd.LeaseOutput(ctx, outpoint, expiry)
		if err != nil {
			results = append(results, leaseResult{Outpoint: outpoint, Error: lndRPCErrorMessage(err)})
			continue
		}
		results = append(results, leaseResult{Outpoint: info.Outpoint, Expiration: info.Expiration})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) handleUtxoUnlock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Outpoints []string `json:"outpoints"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(req.Outpoints) == 0 {
		writeError(w, http.StatusBadRequest, "outpoints required")
		return
	}

	s.auditUtxoLockAction(r, "unlock", req.Outpoints, 0)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	type unlockResult struct {
		Outpoint string `json:"outpoint"`
		Error    string `json:"error,omitempty"`
	}
	results := make([]unlockResult, 0, len(req.Outpoints))
	for _, outpoint := range req.Outpoints {
		err := s.lnd.ReleaseOutput(ctx, outpoint)
		if err != nil {
			results = append(results, unlockResult{Outpoint: outpoint, Error: lndRPCErrorMessage(err)})
			continue
		}
		results = append(results, unlockResult{Outpoint: outpoint})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) handleUtxoGroupsList(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.utxoManagerService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	groups, err := svc.ListGroups(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

func (s *Server) handleUtxoGroupsUpsert(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.utxoManagerService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	var req struct {
		ID        string   `json:"id"`
		Name      string   `json:"name"`
		Color     string   `json:"color"`
		Outpoints []string `json:"outpoints"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if strings.TrimSpace(req.ID) == "" && strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name required for new group")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	id, err := svc.UpsertGroup(ctx, UtxoGroupUpsert{
		ID:    req.ID,
		Name:  req.Name,
		Color: req.Color,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if len(req.Outpoints) > 0 {
		if err := svc.AssignToGroup(ctx, id, req.Outpoints); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

func (s *Server) handleUtxoGroupAssign(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.utxoManagerService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	groupID := strings.TrimSpace(chi.URLParam(r, "id"))
	if groupID == "" {
		writeError(w, http.StatusBadRequest, "group id required")
		return
	}

	var req struct {
		Outpoints []string `json:"outpoints"`
		Detach    bool     `json:"detach"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(req.Outpoints) == 0 {
		writeError(w, http.StatusBadRequest, "outpoints required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	assignTo := groupID
	if req.Detach {
		assignTo = ""
	}
	if err := svc.AssignToGroup(ctx, assignTo, req.Outpoints); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleUtxoBump(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Outpoint        string `json:"outpoint"`
		SatPerVbyte     int64  `json:"sat_per_vbyte"`
		TargetConf      uint32 `json:"target_conf"`
		BudgetSat       int64  `json:"budget_sat"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if strings.TrimSpace(req.Outpoint) == "" {
		writeError(w, http.StatusBadRequest, "outpoint required")
		return
	}
	if req.SatPerVbyte <= 0 && req.TargetConf == 0 {
		writeError(w, http.StatusBadRequest, "sat_per_vbyte or target_conf required")
		return
	}

	// CPFP burns sats from the wallet, so it needs the same reauth posture as
	// /api/wallet/send (external destination). Reuses authScopeWalletSendExternal
	// so a recent reauth on either path covers both.
	if s.auth != nil && s.auth.Enabled() {
		session, ok := authSessionFromContext(r.Context())
		if !ok {
			writeErrorCode(w, http.StatusUnauthorized, "auth_required", "authentication required")
			return
		}
		if !s.auth.HasRecentReauth(session.ID, authScopeWalletSendExternal) {
			confirmPassword := strings.TrimSpace(req.ConfirmPassword)
			if confirmPassword == "" {
				writeJSON(w, http.StatusPreconditionRequired, map[string]any{
					"error":                          "password confirmation required for fee bump",
					"code":                           "utxo_bump_reauth_required",
					"requires_password_confirmation": true,
				})
				return
			}
			if _, err := s.auth.reauth(session.ID, confirmPassword, authScopeWalletSendExternal); err != nil {
				writeErrorCode(w, http.StatusUnauthorized, "auth_invalid_credentials", "invalid credentials")
				return
			}
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	if err := s.lnd.BumpFee(ctx, lndclient.BumpFeeParams{
		Outpoint:    req.Outpoint,
		SatPerVbyte: req.SatPerVbyte,
		TargetConf:  req.TargetConf,
		BudgetSat:   req.BudgetSat,
		Immediate:   true,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, lndRPCErrorMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleUtxoGroupDelete(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.utxoManagerService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	groupID := strings.TrimSpace(chi.URLParam(r, "id"))
	if groupID == "" {
		writeError(w, http.StatusBadRequest, "group id required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := svc.DeleteGroup(ctx, groupID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
