package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
)

type opreturnFunding interface {
	Quote() lndclient.OPReturnQuote
	Finalize(context.Context) (string, string, error)
	Release() error
}
type opreturnWallet interface {
	Fund(context.Context, string, int64) (opreturnFunding, error)
	PublishTransaction(context.Context, string, string) error
	OPReturnTransaction(context.Context, string) (*lnrpc.Transaction, error)
}
type opreturnLNDWallet struct{ *lndclient.Client }
type opreturnLNDFunding struct{ funding *lndclient.OPReturnFunding }

func (f opreturnLNDFunding) Quote() lndclient.OPReturnQuote { return f.funding.Quote }
func (f opreturnLNDFunding) Finalize(ctx context.Context) (string, string, error) {
	return f.funding.Finalize(ctx)
}
func (f opreturnLNDFunding) Release() error { return f.funding.Release() }
func (w opreturnLNDWallet) Fund(ctx context.Context, text string, rate int64) (opreturnFunding, error) {
	f, err := w.Client.FundOPReturn(ctx, text, rate)
	if err != nil {
		return nil, err
	}
	return opreturnLNDFunding{f}, nil
}
func (s *Server) opreturnWallet() opreturnWallet {
	if s.opreturnWalletClient != nil {
		return s.opreturnWalletClient
	}
	if s.lnd == nil {
		return nil
	}
	return opreturnLNDWallet{s.lnd}
}

// Preview capabilities and publication attempts survive manager restarts. The
// unique partial index also prevents concurrent publishers in another process.
func (s *Server) ensureOPReturnSchema(ctx context.Context) error {
	s.opreturnMu.Lock()
	defer s.opreturnMu.Unlock()
	if s.opreturnSchema {
		return nil
	}
	if s.db == nil {
		return errors.New("OP_RETURN requires PostgreSQL")
	}
	_, err := s.db.Exec(ctx, `
CREATE TABLE IF NOT EXISTS opreturn_app (id integer PRIMARY KEY CHECK(id=1), installed boolean NOT NULL DEFAULT false, enabled boolean NOT NULL DEFAULT false);
INSERT INTO opreturn_app(id) VALUES(1) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS opreturn_previews (
 id text PRIMARY KEY, session_hash text NOT NULL, payload_hash text NOT NULL,
 quote jsonb NOT NULL, expires_at timestamptz NOT NULL, used boolean NOT NULL DEFAULT false);
CREATE TABLE IF NOT EXISTS opreturn_records (
 id text PRIMARY KEY, session_hash text NOT NULL, idempotency_key text NOT NULL,
 preview_id text NOT NULL UNIQUE REFERENCES opreturn_previews(id),
 quote jsonb NOT NULL, state text NOT NULL, txid text NOT NULL DEFAULT '',
 confirmations integer NOT NULL DEFAULT 0, block_height integer NOT NULL DEFAULT 0,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(session_hash,idempotency_key));
CREATE UNIQUE INDEX IF NOT EXISTS opreturn_one_inflight ON opreturn_records ((true)) WHERE state IN ('preparing','unknown');
`)
	if err == nil {
		s.opreturnSchema = true
		if s.shutdownCtx != nil {
			go s.runOPReturnReconciliation()
		}
	}
	return err
}

func opreturnHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
func opreturnID() (string, error) {
	b := make([]byte, 24)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}

type opreturnPreview struct {
	lndclient.OPReturnQuote
	PreviewID string    `json:"preview_id"`
	ExpiresAt time.Time `json:"expires_at"`
	MaxFeeSat int64     `json:"max_fee_sat"`
}
type opreturnRecord struct {
	ID            string                  `json:"id"`
	Quote         lndclient.OPReturnQuote `json:"quote"`
	State         string                  `json:"state"`
	TXID          string                  `json:"txid"`
	Confirmations int32                   `json:"confirmations"`
	BlockHeight   int32                   `json:"block_height"`
	CreatedAt     time.Time               `json:"created_at"`
}

func (s *Server) opreturnActive(ctx context.Context) error {
	if err := s.ensureOPReturnSchema(ctx); err != nil {
		return err
	}
	var active bool
	if err := s.db.QueryRow(ctx, `SELECT installed AND enabled FROM opreturn_app WHERE id=1`).Scan(&active); err != nil {
		return err
	}
	if !active {
		return errors.New("OP_RETURN is stopped or not installed")
	}
	return nil
}

func (s *Server) opreturnRecord(ctx context.Context, id string) (opreturnRecord, error) {
	var rec opreturnRecord
	var quote []byte
	err := s.db.QueryRow(ctx, `SELECT id,quote,state,txid,confirmations,block_height,created_at FROM opreturn_records WHERE id=$1`, id).Scan(&rec.ID, &quote, &rec.State, &rec.TXID, &rec.Confirmations, &rec.BlockHeight, &rec.CreatedAt)
	if err == nil {
		err = json.Unmarshal(quote, &rec.Quote)
	}
	return rec, err
}

// Reconciliation only observes the known TXID. An unknown outcome never funds
// or broadcasts a replacement transaction, even after a restart or new session.
func (s *Server) reconcileOPReturn(ctx context.Context, rec opreturnRecord) (opreturnRecord, bool) {
	if rec.TXID == "" || s.opreturnWallet() == nil {
		return rec, false
	}
	tx, err := s.opreturnWallet().OPReturnTransaction(ctx, rec.TXID)
	if err != nil || tx == nil || tx.TxHash != rec.TXID {
		return rec, false
	}
	state := "broadcast"
	if tx.NumConfirmations > 0 {
		state = "confirmed"
	}
	confirmed := rec.State != "confirmed" && state == "confirmed"
	changed, err := s.db.Exec(ctx, `UPDATE opreturn_records SET state=$2, confirmations=$3, block_height=$4, updated_at=now() WHERE id=$1 AND txid=$5 AND state=$6 AND confirmations=$7 AND block_height=$8`, rec.ID, state, tx.NumConfirmations, tx.BlockHeight, rec.TXID, rec.State, rec.Confirmations, rec.BlockHeight)
	if err == nil {
		rec.State = state
		rec.Confirmations = tx.NumConfirmations
		rec.BlockHeight = tx.BlockHeight
	}
	return rec, confirmed && err == nil && changed.RowsAffected() == 1
}

// A bounded in-process reconciler records confirmations even with no browser
// open. It also recovers attempts interrupted before the write-ahead barrier.
func (s *Server) runOPReturnReconciliation() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.shutdownCtx.Done():
			return
		case <-ticker.C:
		}
		if !s.opreturnPublishMu.TryLock() {
			continue
		}
		ctx, cancel := context.WithTimeout(s.shutdownCtx, 20*time.Second)
		s.recoverOPReturnPreparations(ctx)
		_, _ = s.db.Exec(ctx, `DELETE FROM opreturn_previews WHERE NOT used AND expires_at < now()`)
		rows, err := s.db.Query(ctx, `SELECT id FROM opreturn_records WHERE txid<>'' AND (state IN ('unknown','broadcast') OR (state='confirmed' AND confirmations<6)) ORDER BY updated_at LIMIT 25`)
		ids := []string{}
		if err == nil {
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			rows.Close()
		}
		for _, id := range ids {
			rec, err := s.opreturnRecord(ctx, id)
			if err != nil {
				continue
			}
			rec, confirmed := s.reconcileOPReturn(ctx, rec)
			if confirmed {
				s.opreturnAudit(nil, "confirmation", rec.Quote, rec.TXID, "confirmed")
			}
		}
		cancel()
		s.opreturnPublishMu.Unlock()
	}
}

func (s *Server) recoverOPReturnPreparations(ctx context.Context) {
	// Publication has a 90-second deadline. An interrupted preparation older
	// than three minutes cannot broadcast: the barrier requires state=preparing.
	rows, err := s.db.Query(ctx, `UPDATE opreturn_records SET state='failed',updated_at=now() WHERE state='preparing' AND txid='' AND created_at<now()-interval '3 minutes' RETURNING quote`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if rows.Scan(&raw) != nil {
			continue
		}
		var q lndclient.OPReturnQuote
		if json.Unmarshal(raw, &q) == nil {
			s.opreturnAudit(nil, "broadcast_failure", q, "", "interrupted_before_broadcast")
		}
	}
}
