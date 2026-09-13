package server

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"lightningos-light/internal/mesh"
	"time"
)

func (m *meshService) replyRequest(req meshAPIRequest, kind string) (*meshPending, error) {
	if req.RequestID == "" {
		return nil, nil
	}
	p := m.pending[req.RequestID]
	if p == nil || p.Peer != req.Node || p.Kind != kind || !time.Now().Before(p.Expires) || p.Amount != req.Amount {
		return nil, errors.New("request changed or expired; select the received request again")
	}
	if kind == "onchain_request" && (req.Address != p.Address || req.Raw != "") {
		return nil, errors.New("reply must use the requested address and amount")
	}
	return p, nil
}

// Match explicit IDs, peer, authenticated content hash, kind, expiry and terms.
// No amount/time heuristics, and no wallet operation occurs before this check.
func (m *meshService) acceptReply(ctx context.Context, p mesh.Packet, raw []byte) (byte, []byte, error) {
	kind, session, hash, content, err := mesh.DecodeReply(raw)
	if err != nil {
		return 0, nil, err
	}
	parent := fmt.Sprintf("%08x:%x", p.To, session)
	var operation, address, response string
	var amount int64
	err = m.db.QueryRow(ctx, `SELECT operation,request_address,request_amount,response_id FROM los_mesh_sessions WHERE id=$1 AND peer=$2 AND direction='out' AND hash=$3 AND expects_reply AND expires>now() AND state NOT IN ('cancelled','expired','rejected')`, parent, p.From, hex.EncodeToString(hash[:])).Scan(&operation, &address, &amount, &response)
	if err != nil || (response != "" && response != sessionID(p)) {
		return 0, nil, errors.New("unmatched reply")
	}
	if kind == mesh.Invoice {
		if operation != "invoice_request" {
			return 0, nil, errors.New("reply kind mismatch")
		}
		d, err := m.decodeInvoice(ctx, string(content))
		if err != nil || d.AmountSat != amount {
			return 0, nil, errors.New("reply amount mismatch")
		}
	} else {
		if operation != "onchain_request" {
			return 0, nil, errors.New("reply kind mismatch")
		}
		tx, err := mesh.ValidateTransaction(content)
		if err != nil {
			return 0, nil, err
		}
		matched := false
		for _, out := range tx.Outputs {
			if out.Address == address && out.Sats == amount {
				matched = true
			}
		}
		if !matched {
			return 0, nil, errors.New("reply output mismatch")
		}
	}
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, "UPDATE los_mesh_sessions SET response_id=$2,updated=now() WHERE id=$1 AND (response_id='' OR response_id=$2)", parent, sessionID(p))
	if err != nil || tag.RowsAffected() != 1 {
		return 0, nil, errors.New("reply already claimed")
	}
	if _, err = tx.Exec(ctx, "UPDATE los_mesh_sessions SET request_id=$2,updated=now() WHERE id=$1", sessionID(p), parent); err != nil {
		return 0, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, nil, err
	}
	// An authenticated matching response proves receipt of the original request,
	// even if its result/ACK was lost. Stop retrying it before it can overwrite
	// the answered state with a later transport timeout.
	delete(m.outgoing, parent)
	return kind, content, nil
}

// The source of a result remains visible through the child session direction.
// 'answered' only means a response is available, never settlement.
func (m *meshService) updateRequestResult(ctx context.Context, child, _ string) {
	// Read the committed child state rather than trusting a received status that
	// may have failed the guarded SQL update (wrong hash, peer or stale result).
	_, _ = m.db.Exec(ctx, `UPDATE los_mesh_sessions SET state=CASE WHEN response.state='awaiting_approval' THEN 'answered' ELSE response.state END,updated=now(),txid=CASE WHEN response.txid<>'' THEN response.txid ELSE los_mesh_sessions.txid END FROM (SELECT request_id,txid,state FROM los_mesh_sessions WHERE id=$1) response WHERE los_mesh_sessions.id=response.request_id AND los_mesh_sessions.response_id=$1 AND response.state IN ('awaiting_approval','paid','published','payment_unknown','publication_unknown','rejected','relay_disabled')`, child)
}
