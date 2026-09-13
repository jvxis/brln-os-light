package server

import (
	"context"
	"errors"
	"time"
)

func (m *meshService) canRetry(id string) bool {
	o := m.outgoing[id]
	return o != nil && o.Paused && o.Resumes < 2 && len(o.Packets) > 0 && o.Packets[0].Expires > time.Now().Unix()
}
func (m *meshService) retry(ctx context.Context, id string) error {
	if !m.canRetry(id) {
		return errors.New("original transmission unavailable or retry limit reached; consult wallet and contact")
	}
	o := m.outgoing[id]
	mode, err := m.mode(ctx)
	if err != nil || mode == "relay" {
		return errors.New("enable send mode before resuming")
	}
	if _, _, err = m.radioPeer(ctx, o.Packets[0].To); err != nil {
		return err
	}
	tag, err := m.db.Exec(ctx, "UPDATE los_mesh_sessions SET state='sending',last_error='',updated=now() WHERE id=$1 AND state='incomplete' AND expires>now()", id)
	if err != nil || tag.RowsAffected() != 1 {
		return errors.New("session state changed; refresh before resuming")
	}
	// Reuse exact session, hash, signature/invoice, expiry and acknowledged offset.
	// Seal will produce a fresh encryption nonce. Never call wallet methods here.
	o.Paused = false
	o.Resumes++
	o.Attempts = 0
	o.Sent = time.Time{}
	return nil
}
