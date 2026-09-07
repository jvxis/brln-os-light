package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"lightningos-light/internal/lndclient"
)

func (s *Server) opreturnSession(w http.ResponseWriter, r *http.Request) (string, bool) {
	session, ok := authSessionFromContext(r.Context())
	if !ok || s.auth == nil || !s.auth.Enabled() {
		writeError(w, http.StatusUnauthorized, "Enable LightningOS login and sign in to use OP_RETURN")
		return "", false
	}
	if err := s.opreturnActive(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "OP_RETURN is unavailable, stopped or not installed")
		return "", false
	}
	return session.ID, true
}

func decodeOPReturn(w http.ResponseWriter, r *http.Request, out any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil || !utf8.Valid(raw) || !opreturnJSONUnicodeValid(raw) {
		writeError(w, 400, "invalid UTF-8 request")
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		writeError(w, 400, "invalid request")
		return false
	}
	if d.Decode(new(any)) != io.EOF {
		writeError(w, 400, "invalid request")
		return false
	}
	return true
}

// encoding/json replaces lone UTF-16 surrogate escapes with U+FFFD. Reject
// those requests instead of silently changing the exact text to be published.
func opreturnJSONUnicodeValid(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

func (s *Server) handleOPReturnStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.opreturnSession(w, r); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	ready := s.lnd != nil && s.lnd.OPReturnReady(ctx) == nil
	if ready {
		utxos, err := s.lnd.ListOnchainUtxos(ctx, 1, 2147483647)
		ready = err == nil && len(utxos) > 0
	}
	writeJSON(w, 200, map[string]any{"installed": true, "enabled": true, "ready": ready, "max_bytes": 80, "max_sat_per_vbyte": 1000})
}

func (s *Server) handleOPReturnPreview(w http.ResponseWriter, r *http.Request) {
	session, ok := s.opreturnSession(w, r)
	if !ok {
		return
	}
	var req struct {
		Text string `json:"text"`
		Rate int64  `json:"sat_per_vbyte"`
	}
	if !decodeOPReturn(w, r, &req) {
		return
	}
	if _, err := lndclient.OPReturnScript(req.Text); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if req.Rate < 1 || req.Rate > 1000 {
		writeError(w, 400, "sat_per_vbyte must be between 1 and 1000")
		return
	}
	if !s.opreturnPublishMu.TryLock() {
		writeError(w, 409, "OP_RETURN operation in progress")
		return
	}
	defer s.opreturnPublishMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if err := s.opreturnActive(ctx); err != nil {
		writeError(w, 409, "OP_RETURN is stopped")
		return
	}
	if s.opreturnWallet() == nil {
		writeError(w, 503, "LND unavailable")
		return
	}
	f, err := s.opreturnWallet().Fund(ctx, req.Text, req.Rate)
	if err != nil {
		s.opreturnAudit(r, "preview", lndclient.OPReturnQuote{Text: req.Text, ByteCount: len(req.Text)}, "", "failed")
		writeError(w, 422, "WalletKit could not fund the preview; check mainnet sync, confirmed spendable funds and fee rate")
		return
	}
	// Leases are released with a fresh context even if the HTTP request expired.
	if err := f.Release(); err != nil {
		writeError(w, 503, "WalletKit lease cleanup failed; wait for leases to expire before retrying")
		return
	}
	id, err := opreturnID()
	if err != nil {
		writeError(w, 500, "preview unavailable")
		return
	}
	quote, err := json.Marshal(f.Quote())
	if err != nil {
		writeError(w, 500, "preview unavailable")
		return
	}
	expires := time.Now().UTC().Add(3 * time.Minute)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		writeError(w, 503, "preview persistence unavailable")
		return
	}
	defer tx.Rollback(context.Background())
	// Keep one current preview per session and expire unused plaintext promptly.
	_, err = tx.Exec(ctx, `DELETE FROM opreturn_previews WHERE NOT used AND (expires_at < now() OR session_hash=$1)`, opreturnHash(session))
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO opreturn_previews(id,session_hash,payload_hash,quote,expires_at) VALUES($1,$2,$3,$4,$5)`, id, opreturnHash(session), opreturnHash(req.Text), quote, expires)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		writeError(w, 503, "preview persistence unavailable")
		return
	}
	s.opreturnAudit(r, "preview", f.Quote(), "", "success")
	writeJSON(w, 200, opreturnPreview{OPReturnQuote: f.Quote(), PreviewID: id, ExpiresAt: expires, MaxFeeSat: f.Quote().FeeSat})
}

func (s *Server) handleOPReturnPublish(w http.ResponseWriter, r *http.Request) {
	session, ok := s.opreturnSession(w, r)
	if !ok {
		return
	}
	var req struct {
		PreviewID string `json:"preview_id"`
		Key       string `json:"idempotency_key"`
		Confirm   bool   `json:"confirm_publication"`
	}
	if !decodeOPReturn(w, r, &req) {
		return
	}
	if !req.Confirm || len(req.PreviewID) != 48 || len(req.Key) < 16 || len(req.Key) > 128 {
		writeError(w, 400, "preview, idempotency key and irreversible-publication confirmation required")
		return
	}
	if !s.auth.HasRecentReauth(session, "opreturn_publish") {
		writeErrorCode(w, 403, "opreturn_reauth_required", "Fresh password confirmation required")
		return
	}
	if !s.opreturnPublishMu.TryLock() {
		writeError(w, 409, "OP_RETURN operation in progress")
		return
	}
	defer s.opreturnPublishMu.Unlock()
	// Finish recording the known transaction outcome even if the browser closes.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 90*time.Second)
	defer cancel()
	if err := s.opreturnActive(ctx); err != nil {
		writeError(w, 409, "OP_RETURN is stopped")
		return
	}
	s.recoverOPReturnPreparations(ctx)
	var existing string
	err := s.db.QueryRow(ctx, `SELECT id FROM opreturn_records WHERE session_hash=$1 AND idempotency_key=$2`, opreturnHash(session), req.Key).Scan(&existing)
	if err == nil {
		var preview string
		if err = s.db.QueryRow(ctx, `SELECT preview_id FROM opreturn_records WHERE id=$1`, existing).Scan(&preview); err != nil || preview != req.PreviewID {
			writeError(w, 409, "idempotency key already bound to another preview")
			return
		}
		rec, err := s.opreturnRecord(ctx, existing)
		if err != nil {
			writeError(w, 503, "record unavailable")
			return
		}
		rec, confirmed := s.reconcileOPReturn(ctx, rec)
		if confirmed {
			s.opreturnAudit(r, "confirmation", rec.Quote, rec.TXID, "confirmed")
		}
		writeJSON(w, 200, rec)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 503, "publication persistence unavailable")
		return
	}
	id, err := opreturnID()
	if err != nil {
		writeError(w, 500, "publication unavailable")
		return
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		writeError(w, 503, "publication persistence unavailable")
		return
	}
	defer tx.Rollback(context.Background())
	var raw []byte
	var hash string
	err = tx.QueryRow(ctx, `UPDATE opreturn_previews SET used=true WHERE id=$1 AND session_hash=$2 AND NOT used AND expires_at>now() RETURNING quote,payload_hash`, req.PreviewID, opreturnHash(session)).Scan(&raw, &hash)
	var quote lndclient.OPReturnQuote
	if err != nil || json.Unmarshal(raw, &quote) != nil || opreturnHash(quote.Text) != hash {
		writeError(w, 409, "Preview expired or consumed; create a new preview")
		return
	}
	_, err = tx.Exec(ctx, `INSERT INTO opreturn_records(id,session_hash,idempotency_key,preview_id,quote,state) VALUES($1,$2,$3,$4,$5,'preparing')`, id, opreturnHash(session), req.Key, req.PreviewID, raw)
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		writeError(w, 409, "An unresolved publication exists; review history before publishing again")
		return
	}
	s.opreturnAudit(r, "publish_attempt", quote, "", "preparing")
	failed := func(message string) {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_, _ = s.db.Exec(cleanup, `UPDATE opreturn_records SET state='failed',updated_at=now() WHERE id=$1 AND state='preparing'`, id)
		s.opreturnAudit(r, "broadcast_failure", quote, "", "failed")
		writeError(w, 409, message)
	}
	if s.opreturnWallet() == nil {
		failed("LND unavailable; create a new preview")
		return
	}
	f, err := s.opreturnWallet().Fund(ctx, quote.Text, quote.SatPerVbyte)
	if err != nil {
		failed("Funding failed; create a new preview")
		return
	}
	defer func() {
		if err := f.Release(); err != nil {
			s.opreturnAudit(r, "lease_cleanup", f.Quote(), "", "failed")
		}
	}()
	if f.Quote().FeeSat > quote.FeeSat {
		failed("Actual fee exceeds the approved maximum; create a new preview")
		return
	}
	txid, txhex, err := f.Finalize(ctx)
	if err != nil {
		failed("WalletKit finalization failed; create a new preview")
		return
	}
	actual, _ := json.Marshal(f.Quote())
	// Write-ahead barrier: no broadcast can occur without a durable deterministic
	// TXID. All errors after this point are ambiguous and must only reconcile.
	barrier, err := s.db.Exec(ctx, `UPDATE opreturn_records SET state='unknown',txid=$2,quote=$3,updated_at=now() WHERE id=$1 AND state='preparing' AND txid=''`, id, txid, actual)
	if err != nil || barrier.RowsAffected() != 1 {
		failed("Could not persist transaction; publication cancelled")
		return
	}
	err = s.opreturnWallet().PublishTransaction(ctx, txhex, "lightningos:opreturn:"+id)
	result := "unknown"
	if err == nil {
		_, _ = s.db.Exec(ctx, `UPDATE opreturn_records SET state='broadcast',updated_at=now() WHERE id=$1`, id)
		result = "broadcast"
	}
	if err == nil {
		s.opreturnAudit(r, "broadcast_success", f.Quote(), txid, result)
	} else {
		s.opreturnAudit(r, "broadcast_failure", f.Quote(), txid, result)
	}
	rec, err := s.opreturnRecord(ctx, id)
	if err != nil {
		writeJSON(w, 202, opreturnRecord{ID: id, TXID: txid, State: result, Quote: f.Quote(), CreatedAt: time.Now().UTC()})
		return
	}
	rec, confirmed := s.reconcileOPReturn(ctx, rec)
	if confirmed {
		s.opreturnAudit(r, "confirmation", rec.Quote, rec.TXID, "confirmed")
	}
	writeJSON(w, 200, rec)
}

func (s *Server) opreturnAudit(r *http.Request, action string, q lndclient.OPReturnQuote, txid, result string) {
	s.recordAuditEventAsync(r, "opreturn."+action, "opreturn", map[string]any{"payload_hash": opreturnHash(q.Text), "byte_count": q.ByteCount, "txid": txid, "fee_sat": q.FeeSat, "result": result})
}

func (s *Server) handleOPReturnRecords(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.opreturnSession(w, r); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	id := chi.URLParam(r, "id")
	ids := []string{}
	if id != "" {
		ids = append(ids, id)
	} else {
		rows, err := s.db.Query(ctx, `SELECT id FROM opreturn_records ORDER BY created_at DESC LIMIT 100`)
		if err != nil {
			writeError(w, 503, "history unavailable")
			return
		}
		for rows.Next() {
			var key string
			if err = rows.Scan(&key); err != nil {
				break
			}
			ids = append(ids, key)
		}
		rows.Close()
		if err != nil || rows.Err() != nil {
			writeError(w, 503, "history unavailable")
			return
		}
	}
	records := make([]opreturnRecord, 0, len(ids))
	for _, key := range ids {
		rec, err := s.opreturnRecord(ctx, key)
		if err != nil {
			if id != "" {
				writeError(w, 404, "record not found")
				return
			}
			continue
		}
		var confirmed bool
		rec, confirmed = s.reconcileOPReturn(ctx, rec)
		if confirmed {
			s.opreturnAudit(r, "confirmation", rec.Quote, rec.TXID, "confirmed")
		}
		records = append(records, rec)
	}
	if id != "" {
		writeJSON(w, 200, records[0])
		return
	}
	writeJSON(w, 200, records)
}
