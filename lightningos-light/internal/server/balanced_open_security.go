package server

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

const balancedSessionColumns = `id, session_id, role, peer_pubkey, peer_host, capacity_sat, fee_rate_sat_vb, is_private, close_address, state, state_updated_at, attempts, next_retry_at, last_error, metadata, created_at, updated_at`
const balancedProposalLifetime = 7 * 24 * time.Hour

var ErrBalancedOpenTermsConflict = errors.New("balanced open session ownership or terms conflict; use a new session")
var ErrBalancedOpenProposalExpired = errors.New("balanced open proposal expired; cancel/recover the old session before creating a new one")
var ErrBalancedOpenProposalQuota = errors.New("too many pending balanced open proposals")

func validateBalancedEnvelope(sender string, msg balancedOpenProtocolMessage, kind string) error {
	if !isValidPubkeyHex(sender) || (msg.FromPubkey != "" && !strings.EqualFold(strings.TrimSpace(msg.FromPubkey), sender)) {
		return ErrBalancedOpenInvalidPeerKey
	}
	if msg.Version != balancedOpenProtocolVersion || msg.Kind != kind || len(msg.SessionID) == 0 || len(msg.SessionID) > 128 {
		return ErrBalancedOpenInvalidSession
	}
	if msg.ExecutionMode != "" && normalizeBalancedExecutionMode(msg.ExecutionMode) == "" {
		return ErrBalancedOpenInvalidAction
	}
	if msg.PendingChanIDHex != "" && !isBalancedPendingChanIDHex(msg.PendingChanIDHex) {
		return ErrBalancedOpenInvalidSession
	}
	for _, ch := range msg.SessionID {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("._:-", ch)) {
			return ErrBalancedOpenInvalidSession
		}
	}
	if len(msg.PeerHost) > 512 || len(msg.CloseAddress) > 128 || len(msg.TransitOutputPk) > 20000 || len(msg.TransitInputStack) > 16 {
		return ErrBalancedOpenInvalidSession
	}
	for _, item := range msg.TransitInputStack {
		if len(item) > 20000 {
			return ErrBalancedOpenInvalidSession
		}
	}
	return nil
}
func balancedProposalMatches(session BalancedOpenSession, sender string, msg balancedOpenProtocolMessage) bool {
	if session.Role != balancedOpenRoleAccepter || !strings.EqualFold(session.PeerPubkey, sender) || session.CapacitySat != msg.CapacitySat || session.FeeRateSatVb != msg.FeeRateSatVb || session.Private != msg.Private || session.CloseAddress != strings.TrimSpace(msg.CloseAddress) {
		return false
	}
	meta, err := decodeBalancedOpenMetadata(session.Metadata)
	if err != nil {
		return false
	}
	raw, err := decodeBalancedOpenMetadataMap(session.Metadata)
	if err != nil {
		return false
	}
	if original, ok := raw["proposal_pending_chan_id"]; ok && original != strings.ToLower(strings.TrimSpace(msg.PendingChanIDHex)) {
		return false
	}
	mode := normalizeBalancedExecutionMode(msg.ExecutionMode)
	if mode == "" {
		mode = balancedOpenExecutionModePush
	}
	current := normalizeBalancedExecutionMode(meta.ExecutionMode)
	if current == "" {
		current = balancedOpenExecutionModePush
	}
	if current != mode {
		return false
	}
	if mode != balancedOpenExecutionModeDual {
		return true
	}
	return strings.EqualFold(meta.InitiatorMultisigKey.PublicKey, msg.MultisigPubkey) && balancedTransitMatches(meta.InitiatorTransit, msg)
}

func balancedPreallocatedProposal(session BalancedOpenSession, sender string, msg balancedOpenProtocolMessage) bool {
	if session.Role != balancedOpenRoleAccepter || session.State != balancedOpenStateSessionCreated || !strings.EqualFold(session.PeerPubkey, sender) || session.CapacitySat != msg.CapacitySat || session.FeeRateSatVb != msg.FeeRateSatVb || session.Private != msg.Private || session.CloseAddress != strings.TrimSpace(msg.CloseAddress) {
		return false
	}
	meta, err := decodeBalancedOpenMetadata(session.Metadata)
	if err != nil {
		return false
	}
	mode := normalizeBalancedExecutionMode(msg.ExecutionMode)
	if mode == "" {
		mode = balancedOpenExecutionModePush
	}
	current := meta.ExecutionMode
	if current == "" {
		current = balancedOpenExecutionModeDual
	}
	return current == mode && meta.InitiatorMultisigKey.PublicKey == "" && meta.AccepterMultisigKey.PublicKey == "" && meta.InitiatorTransit.TxID == "" && meta.AccepterTransit.TxID == ""
}
func balancedTransitMatches(transit balancedOpenTransitDetails, msg balancedOpenProtocolMessage) bool {
	return strings.EqualFold(transit.TxID, msg.TransitTxID) && transit.Vout == msg.TransitTxVout && transit.OutputSat == msg.TransitOutputSat && strings.EqualFold(transit.OutputScript, msg.TransitOutputPk)
}
func balancedAcceptMatches(session BalancedOpenSession, msg balancedOpenProtocolMessage) bool {
	meta, err := decodeBalancedOpenMetadata(session.Metadata)
	if err != nil {
		return false
	}
	mode := normalizeBalancedExecutionMode(msg.ExecutionMode)
	current := normalizeBalancedExecutionMode(meta.ExecutionMode)
	if current == "" {
		current = balancedOpenExecutionModePush
	}
	if mode == "" {
		mode = current
	}
	if mode != current {
		return false
	}
	if mode != balancedOpenExecutionModeDual {
		return true
	}
	return strings.EqualFold(meta.AccepterMultisigKey.PublicKey, msg.MultisigPubkey) && balancedTransitMatches(meta.AccepterTransit, msg) && strings.Join(meta.AccepterInputWitness, ",") == strings.Join(msg.TransitInputStack, ",") && (msg.PendingChanIDHex == "" || strings.EqualFold(meta.PendingChanID, msg.PendingChanIDHex))
}
func (s *BalancedOpenService) existingProposal(ctx context.Context, tx pgx.Tx, sender string, msg balancedOpenProtocolMessage) (BalancedOpenSession, bool, error) {
	// Serialize only incoming creation/quota checks, never hold this lock over RPC.
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(420695301)`); err != nil {
		return BalancedOpenSession{}, false, err
	}
	session, err := scanBalancedOpenSession(tx.QueryRow(ctx, `select `+balancedSessionColumns+` from balanced_open_sessions where session_id=$1 for update`, msg.SessionID))
	if err == nil {
		if balancedPreallocatedProposal(session, sender, msg) {
			return session, true, s.checkProposalQuota(ctx, tx, sender)
		}
		if !balancedProposalMatches(session, sender, msg) {
			return session, true, ErrBalancedOpenTermsConflict
		}
		return session, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return session, false, err
	}
	return session, false, s.checkProposalQuota(ctx, tx, sender)
}

func (s *BalancedOpenService) checkProposalQuota(ctx context.Context, tx pgx.Tx, sender string) error {
	var perPeer, total int
	err := tx.QueryRow(ctx, `select count(*) filter(where peer_pubkey=$1),count(*) from balanced_open_sessions where role='accepter' and state='proposal_received' and state_updated_at > now()-interval '7 days'`, sender).Scan(&perPeer, &total)
	if err != nil {
		return err
	}
	if perPeer >= 16 || total >= 256 {
		return ErrBalancedOpenProposalQuota
	}
	return nil
}
