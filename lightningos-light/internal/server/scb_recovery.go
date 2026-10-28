package server

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"
)

const scbRecoveryConfirmPhrase = "I UNDERSTAND FORCE CLOSE"

func (s *Server) handleLNSCBRestore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MultiChanBackup string `json:"multi_chan_backup"`
		ConfirmPhrase   string `json:"confirm_phrase"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	if normalizeSCBConfirmPhrase(req.ConfirmPhrase) != scbRecoveryConfirmPhrase {
		writeError(w, http.StatusBadRequest, "confirmation phrase mismatch")
		return
	}

	backup, err := decodeSCBBackup(req.MultiChanBackup)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.scbRestoreMu.Lock()
	defer s.scbRestoreMu.Unlock()

	statusCtx, statusCancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	status, err := s.lnd.GetStatus(statusCtx)
	statusCancel()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, lndDetailedErrorMessage(err))
		return
	}
	if status.WalletState != "unlocked" {
		writeError(w, http.StatusPreconditionFailed, "lnd wallet must be unlocked")
		return
	}
	if !status.SyncedToChain || !status.SyncedToGraph {
		writeError(w, http.StatusPreconditionFailed, "lnd must be synced to chain and graph before SCB recovery")
		return
	}

	channelsCtx, channelsCancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	channels, err := s.lnd.ListChannels(channelsCtx)
	channelsCancel()
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}
	if len(channels) > 0 {
		writeError(w, http.StatusConflict, "SCB recovery is only allowed when there are no open channels")
		return
	}

	pendingCtx, pendingCancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	pending, err := s.lnd.ListPendingChannels(pendingCtx)
	pendingCancel()
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}
	if len(pending) > 0 {
		writeError(w, http.StatusConflict, "SCB recovery is only allowed when there are no pending channels")
		return
	}

	verifyCtx, verifyCancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	chanPoints, err := s.lnd.VerifyChannelBackup(verifyCtx, backup)
	verifyCancel()
	if err != nil {
		msg := lndRPCErrorMessage(err)
		if msg == "" {
			msg = "invalid static channel backup"
		}
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if len(chanPoints) == 0 {
		writeError(w, http.StatusBadRequest, "backup contains no channels")
		return
	}

	restoreCtx, restoreCancel := context.WithTimeout(r.Context(), 60*time.Second)
	if err := validateFinancialSnapshot(restoreCtx, "scb_restore", map[string]any{"channel_points": chanPoints}); err != nil {
		restoreCancel()
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err := commitFinancialWorkflow(restoreCtx, "scb_restore"); err != nil {
		restoreCancel()
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	numRestored, err := s.lnd.RestoreChannelBackups(restoreCtx, backup)
	restoreCancel()
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"num_restored":      numRestored,
		"verified_channels": len(chanPoints),
	})
}

func normalizeSCBConfirmPhrase(value string) string {
	cleaned := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	return strings.ToUpper(cleaned)
}

func decodeSCBBackup(raw string) ([]byte, error) {
	compact := strings.TrimSpace(raw)
	if compact == "" {
		return nil, errors.New("multi_chan_backup required")
	}
	if strings.HasPrefix(strings.ToLower(compact), "data:") {
		if idx := strings.Index(compact, ","); idx >= 0 && idx < len(compact)-1 {
			compact = compact[idx+1:]
		}
	}
	compact = strings.Join(strings.Fields(compact), "")
	if compact == "" {
		return nil, errors.New("multi_chan_backup required")
	}

	decoders := []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString,
		base64.RawURLEncoding.DecodeString,
	}
	for _, decode := range decoders {
		data, err := decode(compact)
		if err != nil {
			continue
		}
		if len(data) == 0 {
			continue
		}
		return data, nil
	}

	return nil, errors.New("invalid base64 static channel backup")
}
