package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"lightningos-light/internal/lndclient"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/ripemd160"
)

const (
	balancedOpenDefaultListLimit         = 50
	balancedOpenMaxListLimit             = 500
	balancedOpenDefaultEventList         = 100
	balancedOpenMaxEventList             = 500
	balancedOpenMinCapacitySat           = 20000
	balancedOpenProtocolVersion          = 1
	balancedOpenCustomMsgType            = uint32(42069)
	balancedOpenReconcileEvery           = 20 * time.Second
	balancedOpenExecutionModePush        = "push_sat_v1"
	balancedOpenExecutionModeDual        = "dual_funded_v1"
	balancedOpenMultiSigKeyFamily        = int32(0)
	balancedOpenTransitKeyFamily         = int32(805)
	balancedOpenFundingVBytes            = int64(190)
	balancedOpenAnchorSafetySat          = int64(10000)
	balancedOpenPublishMaxAttempts       = 3
	balancedOpenPendingStuckAfter        = 20 * time.Minute
	balancedOpenPendingAutoRetryCooldown = 10 * time.Minute
)

const (
	balancedOpenRoleInitiator = "initiator"
	balancedOpenRoleAccepter  = "accepter"
)

const (
	balancedOpenStateSessionCreated       = "session_created"
	balancedOpenStateProposalSent         = "proposal_sent"
	balancedOpenStateProposalReceived     = "proposal_received"
	balancedOpenStateAccepted             = "accepted"
	balancedOpenStateFundingTxHalfSigned  = "funding_tx_half_signed"
	balancedOpenStateFundingTxFullySigned = "funding_tx_fully_signed"
	balancedOpenStateChannelProposed      = "channel_proposed_to_lnd"
	balancedOpenStateFundingBroadcasted   = "funding_broadcasted"
	balancedOpenStatePendingOpenDetected  = "pending_open_detected"
	balancedOpenStateActive               = "active"
	balancedOpenStateFailed               = "failed"
	balancedOpenStateCanceled             = "canceled"
	balancedOpenStateRecoveryRequired     = "recovery_required"
	balancedOpenStateRecovered            = "recovered"
)

const (
	balancedOpenMessageKindProposal              = "proposal"
	balancedOpenMessageKindAccept                = "accept"
	balancedOpenMessageKindCancel                = "cancel"
	balancedOpenMessageKindOrphanRecoveryRequest = "orphan_recovery_request"
	balancedOpenMessageKindOrphanRecoverySig     = "orphan_recovery_sig"
)

var (
	ErrBalancedOpenDBUnavailable              = errors.New("balanced open db unavailable")
	ErrBalancedOpenSessionNotFound            = errors.New("balanced open session not found")
	ErrBalancedOpenInvalidPeerKey             = errors.New("invalid peer pubkey")
	ErrBalancedOpenInvalidCapacity            = errors.New("invalid capacity")
	ErrBalancedOpenInvalidFeeRate             = errors.New("invalid fee rate")
	ErrBalancedOpenInvalidRole                = errors.New("invalid role")
	ErrBalancedOpenInvalidState               = errors.New("invalid state")
	ErrBalancedOpenTerminalState              = errors.New("session already in terminal state")
	ErrBalancedOpenInvalidSession             = errors.New("invalid session")
	ErrBalancedOpenInvalidAction              = errors.New("invalid session action")
	ErrBalancedOpenInsufficientOnchainSafety  = errors.New("insufficient on-chain spendable balance for anchor reserve")
	ErrBalancedOpenTransitOutpointUnavailable = errors.New("transit outpoint unavailable in wallet")
)

const (
	balancedOpenRecoverySweepVBytes  = int64(110)
	balancedOpenRecoveryMinOutput    = int64(330)
	balancedOpenOrphanRecoveryVBytes = int64(180)
)

type BalancedOpenService struct {
	db       *pgxpool.Pool
	lnd      *lndclient.Client
	logger   *log.Logger
	notifier *Notifier

	mu           sync.Mutex
	started      bool
	stopCh       chan struct{}
	streamCancel context.CancelFunc
}

type BalancedOpenSession struct {
	ID             int64           `json:"id"`
	SessionID      string          `json:"session_id"`
	Role           string          `json:"role"`
	PeerPubkey     string          `json:"peer_pubkey"`
	PeerHost       string          `json:"peer_host,omitempty"`
	CapacitySat    int64           `json:"capacity_sat"`
	FeeRateSatVb   int64           `json:"fee_rate_sat_vb"`
	Private        bool            `json:"private"`
	CloseAddress   string          `json:"close_address,omitempty"`
	State          string          `json:"state"`
	StateUpdatedAt time.Time       `json:"state_updated_at"`
	Attempts       int             `json:"attempts"`
	NextRetryAt    *time.Time      `json:"next_retry_at,omitempty"`
	LastError      string          `json:"last_error,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type BalancedOpenEvent struct {
	ID        int64           `json:"id"`
	SessionID string          `json:"session_id"`
	EventType string          `json:"event_type"`
	Detail    json.RawMessage `json:"detail,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type BalancedOpenCreateParams struct {
	PeerPubkey   string
	PeerHost     string
	CapacitySat  int64
	FeeRateSatVb int64
	Private      bool
	CloseAddress string
	Role         string
	Metadata     json.RawMessage
}

type BalancedOpenListFilter struct {
	State      string
	Role       string
	PeerPubkey string
	Limit      int
}

type balancedOpenProtocolMessage struct {
	Version           int      `json:"v"`
	Kind              string   `json:"kind"`
	SessionID         string   `json:"session_id"`
	FromPubkey        string   `json:"from_pubkey,omitempty"`
	ToPubkey          string   `json:"to_pubkey,omitempty"`
	PeerHost          string   `json:"peer_host,omitempty"`
	CapacitySat       int64    `json:"capacity_sat,omitempty"`
	FeeRateSatVb      int64    `json:"fee_rate_sat_vb,omitempty"`
	Private           bool     `json:"private,omitempty"`
	CloseAddress      string   `json:"close_address,omitempty"`
	Reason            string   `json:"reason,omitempty"`
	ExecutionMode     string   `json:"execution_mode,omitempty"`
	PendingChanIDHex  string   `json:"pending_chan_id,omitempty"`
	MultisigPubkey    string   `json:"multisig_pubkey,omitempty"`
	TransitTxID       string   `json:"transit_tx_id,omitempty"`
	TransitTxVout     uint32   `json:"transit_tx_vout,omitempty"`
	TransitOutputSat  int64    `json:"transit_output_sat,omitempty"`
	TransitOutputPk   string   `json:"transit_output_script,omitempty"`
	TransitInputStack []string `json:"transit_input_witness,omitempty"`
	FundingTxID       string   `json:"funding_tx_id,omitempty"`
	FundingTxVout     uint32   `json:"funding_tx_vout,omitempty"`
	RecoverySigHex    string   `json:"recovery_sig,omitempty"`
	RecoveryFeeRate   int64    `json:"recovery_fee_rate_sat_vb,omitempty"`
	SentAtUnix        int64    `json:"sent_at_unix"`
}

type balancedOpenKeyDescriptor struct {
	PublicKey string `json:"public_key"`
	Family    int32  `json:"family"`
	Index     int32  `json:"index"`
}

type balancedOpenTransitDetails struct {
	TxID         string                    `json:"tx_id"`
	Vout         uint32                    `json:"vout"`
	OutputSat    int64                     `json:"output_sat"`
	OutputScript string                    `json:"output_script"`
	Key          balancedOpenKeyDescriptor `json:"key,omitempty"`
}

type balancedOpenMetadata struct {
	ExecutionMode          string                     `json:"execution_mode,omitempty"`
	PendingChanID          string                     `json:"pending_chan_id,omitempty"`
	InitiatorMultisigKey   balancedOpenKeyDescriptor  `json:"initiator_multisig_key,omitempty"`
	AccepterMultisigKey    balancedOpenKeyDescriptor  `json:"accepter_multisig_key,omitempty"`
	InitiatorTransit       balancedOpenTransitDetails `json:"initiator_transit,omitempty"`
	AccepterTransit        balancedOpenTransitDetails `json:"accepter_transit,omitempty"`
	AccepterInputWitness   []string                   `json:"accepter_input_witness,omitempty"`
	FundingTxHex           string                     `json:"funding_tx_hex,omitempty"`
	FundingTxID            string                     `json:"funding_tx_id,omitempty"`
	FundingTxVout          uint32                     `json:"funding_tx_vout,omitempty"`
	ChannelPoint           string                     `json:"channel_point,omitempty"`
	OrphanRecoveryFeeRate  int64                      `json:"orphan_recovery_fee_rate_sat_vb,omitempty"`
	OrphanInitiatorSigHex  string                     `json:"orphan_initiator_sig,omitempty"`
	OrphanAccepterSigHex   string                     `json:"orphan_accepter_sig,omitempty"`
	OrphanRecoveryTxID     string                     `json:"orphan_recovery_txid,omitempty"`
	OrphanLocalSweepTxID   string                     `json:"orphan_local_sweep_txid,omitempty"`
	OrphanLocalSweepAddr   string                     `json:"orphan_local_sweep_address,omitempty"`
	LastExecutionErr       string                     `json:"last_execution_err,omitempty"`
	LastExecutionErrorUnix int64                      `json:"last_execution_error_unix,omitempty"`
}

type balancedOpenScanner interface {
	Scan(dest ...any) error
}

type balancedOpenEventScanner interface {
	Scan(dest ...any) error
}

func NewBalancedOpenService(db *pgxpool.Pool, lnd *lndclient.Client, logger *log.Logger, notifier *Notifier) *BalancedOpenService {
	return &BalancedOpenService{
		db:       db,
		lnd:      lnd,
		logger:   logger,
		notifier: notifier,
	}
}

func (s *BalancedOpenService) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrBalancedOpenDBUnavailable
	}

	_, err := s.db.Exec(ctx, `
create table if not exists balanced_open_sessions (
  id bigserial primary key,
  session_id text not null unique,
  role text not null check (role in ('initiator', 'accepter')),
  peer_pubkey text not null,
  peer_host text not null default '',
  capacity_sat bigint not null,
  fee_rate_sat_vb bigint not null default 0,
  is_private boolean not null default false,
  close_address text not null default '',
  state text not null,
  state_updated_at timestamptz not null default now(),
  attempts integer not null default 0,
  next_retry_at timestamptz,
  last_error text not null default '',
  metadata jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

alter table balanced_open_sessions add column if not exists peer_host text not null default '';
alter table balanced_open_sessions add column if not exists fee_rate_sat_vb bigint not null default 0;
alter table balanced_open_sessions add column if not exists is_private boolean not null default false;
alter table balanced_open_sessions add column if not exists close_address text not null default '';
alter table balanced_open_sessions add column if not exists state_updated_at timestamptz not null default now();
alter table balanced_open_sessions add column if not exists attempts integer not null default 0;
alter table balanced_open_sessions add column if not exists next_retry_at timestamptz;
alter table balanced_open_sessions add column if not exists last_error text not null default '';
alter table balanced_open_sessions add column if not exists metadata jsonb not null default '{}'::jsonb;

create index if not exists balanced_open_sessions_state_idx on balanced_open_sessions (state, updated_at desc);
create index if not exists balanced_open_sessions_peer_idx on balanced_open_sessions (peer_pubkey, created_at desc);
create index if not exists balanced_open_sessions_retry_idx on balanced_open_sessions (next_retry_at) where next_retry_at is not null;

create table if not exists balanced_open_events (
  id bigserial primary key,
  session_id text not null references balanced_open_sessions(session_id) on delete cascade,
  event_type text not null,
  detail jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now()
);

create index if not exists balanced_open_events_session_idx on balanced_open_events (session_id, created_at desc);
`)
	return err
}

func (s *BalancedOpenService) Start() {
	if s == nil || s.lnd == nil {
		return
	}

	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	go s.runMessageLoop()
	go s.runReconcileLoop()
}

func (s *BalancedOpenService) Stop() {
	if s == nil {
		return
	}

	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	s.started = false
	stopCh := s.stopCh
	s.stopCh = nil
	cancel := s.streamCancel
	s.streamCancel = nil
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if stopCh != nil {
		close(stopCh)
	}
}

func (s *BalancedOpenService) runMessageLoop() {
	for {
		s.mu.Lock()
		stopCh := s.stopCh
		s.mu.Unlock()

		if stopCh == nil {
			return
		}

		streamCtx, cancel := context.WithCancel(context.Background())
		s.mu.Lock()
		s.streamCancel = cancel
		s.mu.Unlock()

		msgs, errs := s.lnd.SubscribeCustomMessages(streamCtx)
		shouldStop := false

		for !shouldStop {
			select {
			case <-stopCh:
				shouldStop = true
			case msg, ok := <-msgs:
				if !ok {
					shouldStop = true
					break
				}
				s.handleIncomingCustomMessage(msg)
			case err, ok := <-errs:
				if ok && err != nil && s.logger != nil {
					s.logger.Printf("balanced-open: custom message stream error: %v", err)
				}
				shouldStop = true
			}
		}

		cancel()

		s.mu.Lock()
		s.streamCancel = nil
		stopCh = s.stopCh
		s.mu.Unlock()

		if stopCh == nil {
			return
		}

		select {
		case <-stopCh:
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (s *BalancedOpenService) runReconcileLoop() {
	ticker := time.NewTicker(balancedOpenReconcileEvery)
	defer ticker.Stop()

	for {
		s.mu.Lock()
		stopCh := s.stopCh
		s.mu.Unlock()
		if stopCh == nil {
			return
		}

		select {
		case <-stopCh:
			return
		case <-ticker.C:
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		if err := s.reconcileSessions(ctx); err != nil && s.logger != nil {
			s.logger.Printf("balanced-open: reconcile failed: %v", err)
		}
		cancel()
	}
}

func (s *BalancedOpenService) ExecuteSession(ctx context.Context, sessionID string) (BalancedOpenSession, error) {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	if isBalancedOpenTerminalState(session.State) {
		return BalancedOpenSession{}, ErrBalancedOpenTerminalState
	}
	if session.Role != balancedOpenRoleInitiator {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidAction
	}
	if session.State != balancedOpenStateAccepted && session.State != balancedOpenStateRecoveryRequired {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidAction
	}

	meta, err := decodeBalancedOpenMetadata(session.Metadata)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	switch normalizeBalancedExecutionMode(meta.ExecutionMode) {
	case balancedOpenExecutionModeDual:
		return s.executeSessionDual(ctx, session, meta)
	default:
		return s.executeSessionPush(ctx, session)
	}
}

func (s *BalancedOpenService) executeSessionPush(ctx context.Context, session BalancedOpenSession) (BalancedOpenSession, error) {
	pushSat := session.CapacitySat / 2
	if pushSat <= 0 {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidCapacity
	}

	submitted, err := s.transitionSession(ctx, session.SessionID, balancedOpenStateChannelProposed, "", "channel_open_submitted", map[string]any{
		"execution_mode":    balancedOpenExecutionModePush,
		"capacity_sat":      session.CapacitySat,
		"local_funding_sat": session.CapacitySat,
		"push_sat":          pushSat,
	})
	if err != nil {
		return BalancedOpenSession{}, err
	}

	if strings.TrimSpace(submitted.PeerHost) != "" {
		if err := s.connectPeerForBalancedOpen(ctx, submitted.PeerPubkey, submitted.PeerHost); err != nil {
			_, _ = s.transitionSession(ctx, submitted.SessionID, balancedOpenStateRecoveryRequired, err.Error(), "channel_open_connect_failed", map[string]any{
				"execution_mode": balancedOpenExecutionModePush,
			})
			return BalancedOpenSession{}, err
		}
	}

	channelPoint, err := s.lnd.OpenChannelWithPush(
		ctx,
		submitted.PeerPubkey,
		submitted.CapacitySat,
		pushSat,
		submitted.CloseAddress,
		submitted.Private,
		submitted.FeeRateSatVb,
	)
	if err != nil {
		_, _ = s.transitionSession(ctx, submitted.SessionID, balancedOpenStateRecoveryRequired, err.Error(), "channel_open_failed", map[string]any{
			"execution_mode": balancedOpenExecutionModePush,
			"capacity_sat":   submitted.CapacitySat,
			"push_sat":       pushSat,
		})
		return BalancedOpenSession{}, err
	}

	updated, err := s.transitionSessionWithMetadata(ctx, submitted.SessionID, balancedOpenStateFundingBroadcasted, "", "funding_broadcasted_local", map[string]any{
		"execution_mode": balancedOpenExecutionModePush,
		"channel_point":  channelPoint,
		"capacity_sat":   submitted.CapacitySat,
		"push_sat":       pushSat,
	}, map[string]any{
		"channel_point":      channelPoint,
		"execution_mode":     balancedOpenExecutionModePush,
		"local_funding_sat":  submitted.CapacitySat,
		"push_sat":           pushSat,
		"opened_at_unix":     time.Now().UTC().Unix(),
		"last_execution_err": "",
	})
	if err != nil {
		return BalancedOpenSession{}, err
	}
	return updated, nil
}

func (s *BalancedOpenService) executeSessionDual(ctx context.Context, session BalancedOpenSession, meta balancedOpenMetadata) (BalancedOpenSession, error) {
	derivedPendingID, derivedPendingErr := balancedPendingChanIDHexFromMultisig(meta.InitiatorMultisigKey.PublicKey, meta.AccepterMultisigKey.PublicKey)
	if derivedPendingErr == nil && isBalancedPendingChanIDHex(derivedPendingID) {
		meta.PendingChanID = derivedPendingID
	}

	if err := validateBalancedDualExecuteArtifacts(session, meta); err != nil {
		return BalancedOpenSession{}, err
	}
	localFundingSat := session.CapacitySat
	pushSat := session.CapacitySat / 2

	// LND anchor reserve checks are sensitive to local_funding_amount even in
	// externally funded shim flows. Pre-check with the same amount to fail early.
	budget, err := s.ensureBalancedOnchainBudget(ctx, localFundingSat, balancedOpenAnchorSafetySat)
	if err != nil {
		_, _ = s.transitionSessionWithMetadata(ctx, session.SessionID, balancedOpenStateRecoveryRequired, err.Error(), "anchor_reserve_precheck_failed", map[string]any{
			"execution_mode":          balancedOpenExecutionModeDual,
			"local_funding_sat":       localFundingSat,
			"estimated_spendable_sat": budget.EstimatedSpendableSat,
			"total_sat":               budget.TotalSat,
			"locked_sat":              budget.LockedSat,
			"reserved_anchor_sat":     budget.ReservedAnchorSat,
			"required_remaining_sat":  balancedOpenAnchorSafetySat,
		}, map[string]any{
			"last_execution_err":        err.Error(),
			"last_execution_error_unix": time.Now().UTC().Unix(),
		})
		return BalancedOpenSession{}, err
	}

	plan, err := buildBalancedDualFundingPlan(session, meta)
	if err != nil {
		return BalancedOpenSession{}, err
	}

	submitted, err := s.transitionSessionWithMetadata(ctx, session.SessionID, balancedOpenStateChannelProposed, "", "channel_open_submitted", map[string]any{
		"execution_mode":            balancedOpenExecutionModeDual,
		"capacity_sat":              session.CapacitySat,
		"local_funding_sat":         localFundingSat,
		"local_onchain_funding_sat": meta.InitiatorTransit.OutputSat,
		"peer_onchain_funding_sat":  meta.AccepterTransit.OutputSat,
		"push_sat":                  pushSat,
	}, map[string]any{
		"execution_mode":  balancedOpenExecutionModeDual,
		"pending_chan_id": meta.PendingChanID,
		"funding_tx_id":   plan.FundingTxID,
		"funding_tx_vout": plan.FundingVout,
		"funding_tx_hex":  plan.TxHex,
	})
	if err != nil {
		return BalancedOpenSession{}, err
	}

	if strings.TrimSpace(submitted.PeerHost) != "" {
		if err := s.connectPeerForBalancedOpen(ctx, submitted.PeerPubkey, submitted.PeerHost); err != nil {
			_, _ = s.transitionSessionWithMetadata(ctx, submitted.SessionID, balancedOpenStateRecoveryRequired, err.Error(), "channel_open_connect_failed", map[string]any{
				"execution_mode": balancedOpenExecutionModeDual,
			}, map[string]any{
				"last_execution_err":        err.Error(),
				"last_execution_error_unix": time.Now().UTC().Unix(),
			})
			return BalancedOpenSession{}, err
		}
	}

	localInputScript, err := s.lnd.ComputeInputScript(ctx, lndclient.ComputeInputScriptParams{
		RawTxHex:        plan.TxHex,
		InputIndex:      uint32(plan.InitiatorInputIndex),
		OutputScriptHex: meta.InitiatorTransit.OutputScript,
		OutputSat:       meta.InitiatorTransit.OutputSat,
		Key: lndclient.DerivedKey{
			PublicKey: meta.InitiatorTransit.Key.PublicKey,
			Family:    meta.InitiatorTransit.Key.Family,
			Index:     meta.InitiatorTransit.Key.Index,
		},
	})
	if err != nil {
		_, _ = s.transitionSessionWithMetadata(ctx, submitted.SessionID, balancedOpenStateRecoveryRequired, err.Error(), "funding_sign_failed", map[string]any{
			"execution_mode": balancedOpenExecutionModeDual,
			"side":           balancedOpenRoleInitiator,
		}, map[string]any{
			"last_execution_err":        err.Error(),
			"last_execution_error_unix": time.Now().UTC().Unix(),
		})
		return BalancedOpenSession{}, err
	}

	remoteWitness, err := balancedDecodeWitnessStack(meta.AccepterInputWitness)
	if err != nil {
		return BalancedOpenSession{}, err
	}

	plan.Tx.TxIn[plan.InitiatorInputIndex].Witness = cloneBalancedWitness(localInputScript.Witness)
	plan.Tx.TxIn[plan.InitiatorInputIndex].SignatureScript = append([]byte(nil), localInputScript.SigScript...)
	plan.Tx.TxIn[plan.AccepterInputIndex].Witness = cloneBalancedWitness(remoteWitness)

	finalTxHex, err := encodeBalancedTxHex(plan.Tx)
	if err != nil {
		return BalancedOpenSession{}, err
	}

	pendingID, err := hex.DecodeString(meta.PendingChanID)
	if err != nil {
		return BalancedOpenSession{}, errors.New("invalid pending channel id")
	}

	if err := s.lnd.RegisterChanPointShim(ctx, lndclient.ChanPointShimParams{
		CapacitySat:   session.CapacitySat,
		PendingChanID: pendingID,
		FundingTxID:   plan.FundingTxID,
		FundingVout:   plan.FundingVout,
		LocalKey: lndclient.DerivedKey{
			PublicKey: meta.InitiatorMultisigKey.PublicKey,
			Family:    meta.InitiatorMultisigKey.Family,
			Index:     meta.InitiatorMultisigKey.Index,
		},
		RemoteKeyHex: meta.AccepterMultisigKey.PublicKey,
	}); err != nil {
		_, _ = s.transitionSessionWithMetadata(ctx, submitted.SessionID, balancedOpenStateRecoveryRequired, err.Error(), "shim_register_failed", map[string]any{
			"execution_mode": balancedOpenExecutionModeDual,
		}, map[string]any{
			"last_execution_err":        err.Error(),
			"last_execution_error_unix": time.Now().UTC().Unix(),
		})
		return BalancedOpenSession{}, err
	}

	channelPoint, err := s.lnd.OpenChannelWithShim(ctx, lndclient.OpenChannelWithShimParams{
		PubkeyHex:       submitted.PeerPubkey,
		CapacitySat:     submitted.CapacitySat,
		LocalFundingSat: localFundingSat,
		PushSat:         pushSat,
		CloseAddress:    submitted.CloseAddress,
		Private:         submitted.Private,
		ChanPointShimArgs: lndclient.ChanPointShimParams{
			CapacitySat:   submitted.CapacitySat,
			PendingChanID: pendingID,
			FundingTxID:   plan.FundingTxID,
			FundingVout:   plan.FundingVout,
			LocalKey: lndclient.DerivedKey{
				PublicKey: meta.InitiatorMultisigKey.PublicKey,
				Family:    meta.InitiatorMultisigKey.Family,
				Index:     meta.InitiatorMultisigKey.Index,
			},
			RemoteKeyHex: meta.AccepterMultisigKey.PublicKey,
		},
	})
	if err != nil {
		_, _ = s.transitionSessionWithMetadata(ctx, submitted.SessionID, balancedOpenStateRecoveryRequired, err.Error(), "channel_open_failed", map[string]any{
			"execution_mode": balancedOpenExecutionModeDual,
		}, map[string]any{
			"last_execution_err":        err.Error(),
			"last_execution_error_unix": time.Now().UTC().Unix(),
		})
		return BalancedOpenSession{}, err
	}
	expectedPoint := balancedOpenCanonicalChannelPoint(plan.FundingTxID, plan.FundingVout)
	actualPoint := balancedOpenCanonicalChannelPointFromString(channelPoint)
	if expectedPoint != "" && actualPoint != "" && expectedPoint != actualPoint {
		_ = s.lnd.CancelFundingShim(ctx, pendingID)
		errMsg := fmt.Sprintf("channel point mismatch: expected %s got %s", expectedPoint, actualPoint)
		_, _ = s.transitionSessionWithMetadata(ctx, submitted.SessionID, balancedOpenStateRecoveryRequired, errMsg, "channel_point_mismatch", map[string]any{
			"execution_mode":         balancedOpenExecutionModeDual,
			"expected_channel_point": expectedPoint,
			"actual_channel_point":   actualPoint,
			"funding_tx_id":          plan.FundingTxID,
			"funding_tx_vout":        plan.FundingVout,
		}, map[string]any{
			"last_execution_err":        errMsg,
			"last_execution_error_unix": time.Now().UTC().Unix(),
		})
		// Best-effort immediate recovery of local transit funds when a shim mismatch
		// indicates LND opened a different channel point than the negotiated one.
		recoverCtx, recoverCancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, _ = s.RecoverSessionTransit(recoverCtx, submitted.SessionID, submitted.FeeRateSatVb)
		recoverCancel()
		return BalancedOpenSession{}, errors.New(errMsg)
	}
	submitted, err = s.transitionSessionWithMetadata(ctx, submitted.SessionID, submitted.State, "", "channel_open_acknowledged", map[string]any{
		"execution_mode":  balancedOpenExecutionModeDual,
		"channel_point":   channelPoint,
		"funding_tx_id":   plan.FundingTxID,
		"funding_tx_vout": plan.FundingVout,
	}, map[string]any{
		"channel_point":             channelPoint,
		"funding_tx_id":             plan.FundingTxID,
		"funding_tx_vout":           plan.FundingVout,
		"funding_tx_hex":            finalTxHex,
		"last_execution_err":        "",
		"last_execution_error_unix": int64(0),
	})
	if err != nil {
		return BalancedOpenSession{}, err
	}

	if err := s.publishBalancedTxWithRetry(ctx, finalTxHex, fmt.Sprintf("balanced-open-%s", submitted.SessionID), balancedOpenPublishMaxAttempts); err != nil {
		channelPointHint := balancedOpenCanonicalChannelPointFromString(channelPoint)
		observed := s.isBalancedOpenFundingObserved(ctx, submitted, channelPointHint, plan.FundingTxID, meta.InitiatorTransit, meta.AccepterTransit)
		if !observed {
			_, _ = s.transitionSessionWithMetadata(ctx, submitted.SessionID, balancedOpenStateChannelProposed, err.Error(), "funding_broadcast_failed_retrying", map[string]any{
				"execution_mode": balancedOpenExecutionModeDual,
				"funding_tx_id":  plan.FundingTxID,
			}, map[string]any{
				"last_execution_err":        err.Error(),
				"last_execution_error_unix": time.Now().UTC().Unix(),
				"funding_tx_hex":            finalTxHex,
			})
			return BalancedOpenSession{}, err
		}
		_, _ = s.transitionSessionWithMetadata(ctx, submitted.SessionID, submitted.State, "", "funding_broadcast_observed_after_error", map[string]any{
			"execution_mode": balancedOpenExecutionModeDual,
			"funding_tx_id":  plan.FundingTxID,
			"channel_point":  channelPointHint,
			"publish_error":  err.Error(),
		}, map[string]any{
			"last_execution_err":        "",
			"last_execution_error_unix": int64(0),
		})
	}

	updated, err := s.transitionSessionWithMetadata(ctx, submitted.SessionID, balancedOpenStateFundingBroadcasted, "", "funding_broadcasted_local", map[string]any{
		"execution_mode":            balancedOpenExecutionModeDual,
		"channel_point":             channelPoint,
		"capacity_sat":              submitted.CapacitySat,
		"push_sat":                  pushSat,
		"funding_tx_id":             plan.FundingTxID,
		"funding_tx_vout":           plan.FundingVout,
		"local_onchain_funding_sat": meta.InitiatorTransit.OutputSat,
		"peer_onchain_funding_sat":  meta.AccepterTransit.OutputSat,
	}, map[string]any{
		"channel_point":             channelPoint,
		"execution_mode":            balancedOpenExecutionModeDual,
		"funding_tx_id":             plan.FundingTxID,
		"funding_tx_vout":           plan.FundingVout,
		"funding_tx_hex":            finalTxHex,
		"local_funding_sat":         localFundingSat,
		"push_sat":                  pushSat,
		"opened_at_unix":            time.Now().UTC().Unix(),
		"last_execution_err":        "",
		"last_execution_error_unix": int64(0),
	})
	if err != nil {
		return BalancedOpenSession{}, err
	}

	return updated, nil
}

func (s *BalancedOpenService) reconcileSessions(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrBalancedOpenDBUnavailable
	}

	sessions, err := s.listSessionsByStates(ctx, []string{
		balancedOpenStateAccepted,
		balancedOpenStateChannelProposed,
		balancedOpenStateFundingBroadcasted,
		balancedOpenStatePendingOpenDetected,
		balancedOpenStateRecoveryRequired,
	})
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		return nil
	}

	pending, err := s.lnd.ListPendingChannels(ctx)
	if err != nil {
		return err
	}
	active, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return err
	}

	for _, session := range sessions {
		if isBalancedOpenTerminalState(session.State) {
			continue
		}

		if session.Role == balancedOpenRoleInitiator && session.State == balancedOpenStateAccepted {
			if _, err := s.ExecuteSession(ctx, session.SessionID); err != nil && s.logger != nil {
				s.logger.Printf("balanced-open: auto execute failed session=%s err=%v", session.SessionID, err)
			}
			continue
		}
		if session.Role == balancedOpenRoleInitiator && session.State == balancedOpenStateChannelProposed {
			if updated, err := s.ensureInitiatorDualBroadcastFromMetadata(ctx, session); err == nil {
				session = updated
			} else if s.logger != nil {
				s.logger.Printf("balanced-open: reconcile broadcast check failed session=%s err=%v", session.SessionID, err)
			}
		}

		if err := s.tryPromoteSessionByChannelState(ctx, session, pending, active); err != nil && s.logger != nil {
			s.logger.Printf("balanced-open: state promotion failed session=%s err=%v", session.SessionID, err)
		}
	}

	return nil
}

func (s *BalancedOpenService) tryPromoteSessionByChannelState(ctx context.Context, session BalancedOpenSession, pending []lndclient.PendingChannelInfo, active []lndclient.ChannelInfo) error {
	channelPointHint := balancedOpenSessionChannelPoint(session.Metadata)
	meta := balancedOpenMetadata{}
	metaReady := false
	if channelPointHint == "" {
		if decoded, err := decodeBalancedOpenMetadata(session.Metadata); err == nil {
			meta = decoded
			metaReady = true
			channelPointHint = balancedOpenCanonicalChannelPoint(meta.FundingTxID, meta.FundingTxVout)
		}
	} else if decoded, err := decodeBalancedOpenMetadata(session.Metadata); err == nil {
		meta = decoded
		metaReady = true
	}

	if activeCh, ok := matchBalancedOpenActiveChannel(session, channelPointHint, active); ok {
		if session.State != balancedOpenStateActive {
			_, err := s.transitionSessionWithMetadata(ctx, session.SessionID, balancedOpenStateActive, "", "channel_active_detected", map[string]any{
				"channel_point": activeCh.ChannelPoint,
				"capacity_sat":  activeCh.CapacitySat,
			}, map[string]any{
				"channel_point": activeCh.ChannelPoint,
			})
			return err
		}
		return nil
	}

	if pendingCh, ok := matchBalancedOpenPendingChannel(session, channelPointHint, pending); ok {
		if !s.canPromoteBalancedPending(ctx, session, metaReady, meta) {
			if session.State == balancedOpenStatePendingOpenDetected || session.State == balancedOpenStateFundingBroadcasted || session.State == balancedOpenStateChannelProposed {
				s.maybeMarkPendingOpenStuck(ctx, session, pendingCh)
			}
			return nil
		}
		pendingSession := session
		if session.State != balancedOpenStatePendingOpenDetected {
			updated, err := s.transitionSessionWithMetadata(ctx, session.SessionID, balancedOpenStatePendingOpenDetected, "", "pending_open_detected", map[string]any{
				"channel_point":              pendingCh.ChannelPoint,
				"capacity_sat":               pendingCh.CapacitySat,
				"confirmations_until_active": pendingCh.ConfirmationsUntilActive,
			}, map[string]any{
				"channel_point": pendingCh.ChannelPoint,
			})
			if err != nil {
				return err
			}
			pendingSession = updated
		}
		s.maybeMarkPendingOpenStuck(ctx, pendingSession, pendingCh)
	}

	return nil
}

func (s *BalancedOpenService) maybeMarkPendingOpenStuck(ctx context.Context, session BalancedOpenSession, pendingCh lndclient.PendingChannelInfo) {
	if session.State != balancedOpenStatePendingOpenDetected &&
		session.State != balancedOpenStateFundingBroadcasted &&
		session.State != balancedOpenStateChannelProposed {
		return
	}
	if pendingCh.ConfirmationHeight > 0 || pendingCh.ConfirmationsUntilActive == 0 {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(pendingCh.Status), "opening") {
		return
	}
	if session.StateUpdatedAt.IsZero() {
		return
	}
	now := time.Now().UTC()
	stuckFor := now.Sub(session.StateUpdatedAt)
	if stuckFor < balancedOpenPendingStuckAfter {
		return
	}

	metaMap, err := decodeBalancedOpenMetadataMap(session.Metadata)
	if err != nil {
		return
	}
	meta, _ := decodeBalancedOpenMetadata(session.Metadata)
	currentPoint := balancedOpenCanonicalChannelPointFromString(pendingCh.ChannelPoint)
	if currentPoint == "" {
		currentPoint = strings.TrimSpace(pendingCh.ChannelPoint)
	}
	if currentPoint == "" {
		return
	}

	fundingTxID := strings.ToLower(strings.TrimSpace(meta.FundingTxID))
	if !isBalancedOpenTxID(fundingTxID) {
		parts := strings.Split(currentPoint, ":")
		if len(parts) == 2 && isBalancedOpenTxID(parts[0]) {
			fundingTxID = strings.ToLower(strings.TrimSpace(parts[0]))
		}
	}
	externalSeen, externalChecked := balancedOpenTxSeenOnExternalMempool(ctx, fundingTxID)

	lastStuckPoint := balancedOpenMetadataString(metaMap, "pending_stuck_channel_point")
	if !strings.EqualFold(lastStuckPoint, currentPoint) {
		suggestion := "pending open has 0 confirmations for over 20 minutes; try Retry broadcast first. If it stays stuck, cancel on both sides, abandon pending channel in LND, then recover funds."
		_, _ = s.transitionSessionWithMetadata(ctx, session.SessionID, session.State, "", "pending_open_stuck_detected", map[string]any{
			"channel_point":              pendingCh.ChannelPoint,
			"capacity_sat":               pendingCh.CapacitySat,
			"confirmations_until_active": pendingCh.ConfirmationsUntilActive,
			"confirmation_height":        pendingCh.ConfirmationHeight,
			"stuck_for_seconds":          int64(stuckFor.Seconds()),
			"suggested_action":           suggestion,
			"external_mempool_checked":   externalChecked,
			"external_mempool_seen":      externalSeen,
			"funding_tx_id":              fundingTxID,
		}, map[string]any{
			"pending_stuck_channel_point":            currentPoint,
			"pending_stuck_notified_unix":            now.Unix(),
			"pending_stuck_external_mempool_checked": externalChecked,
			"pending_stuck_external_mempool_seen":    externalSeen,
		})
	}

	if session.Role != balancedOpenRoleInitiator {
		return
	}
	if normalizeBalancedExecutionMode(meta.ExecutionMode) != balancedOpenExecutionModeDual {
		return
	}
	if externalChecked && externalSeen {
		return
	}
	channelPoint := strings.TrimSpace(meta.ChannelPoint)
	if balancedOpenCanonicalChannelPointFromString(channelPoint) == "" {
		channelPoint = currentPoint
	}
	txHex := strings.TrimSpace(meta.FundingTxHex)
	if channelPoint == "" || !balancedTxHexAppearsSigned(txHex) {
		return
	}
	lastRetryPoint := balancedOpenMetadataString(metaMap, "pending_stuck_autoretry_channel_point")
	lastRetryUnix := int64(0)
	if raw, ok := metaMap["pending_stuck_autoretry_unix"]; ok {
		switch v := raw.(type) {
		case float64:
			lastRetryUnix = int64(v)
		case int64:
			lastRetryUnix = v
		case int:
			lastRetryUnix = int64(v)
		case string:
			if parsed, parseErr := strconv.ParseInt(strings.TrimSpace(v), 10, 64); parseErr == nil {
				lastRetryUnix = parsed
			}
		}
	}
	if strings.EqualFold(lastRetryPoint, currentPoint) && lastRetryUnix > 0 {
		last := time.Unix(lastRetryUnix, 0).UTC()
		if now.Sub(last) < balancedOpenPendingAutoRetryCooldown {
			return
		}
	}

	retryErr := s.publishBalancedTxWithRetry(ctx, txHex, fmt.Sprintf("balanced-open-stuck-retry-%s", session.SessionID), balancedOpenPublishMaxAttempts)
	patch := map[string]any{
		"pending_stuck_autoretry_channel_point":  currentPoint,
		"pending_stuck_autoretry_unix":           now.Unix(),
		"pending_stuck_external_mempool_checked": externalChecked,
		"pending_stuck_external_mempool_seen":    externalSeen,
	}
	if retryErr != nil {
		_, _ = s.transitionSessionWithMetadata(ctx, session.SessionID, session.State, "", "pending_open_stuck_autoretry_failed", map[string]any{
			"channel_point":            currentPoint,
			"funding_tx_id":            fundingTxID,
			"publish_error":            retryErr.Error(),
			"external_mempool_checked": externalChecked,
			"external_mempool_seen":    externalSeen,
		}, patch)
		return
	}
	_, _ = s.transitionSessionWithMetadata(ctx, session.SessionID, session.State, "", "pending_open_stuck_autoretry_submitted", map[string]any{
		"channel_point":            currentPoint,
		"funding_tx_id":            fundingTxID,
		"external_mempool_checked": externalChecked,
		"external_mempool_seen":    externalSeen,
		"auto_retry_attempt":       true,
	}, patch)
}

func (s *BalancedOpenService) canPromoteBalancedPending(ctx context.Context, session BalancedOpenSession, metaReady bool, meta balancedOpenMetadata) bool {
	if !metaReady {
		return true
	}
	if normalizeBalancedExecutionMode(meta.ExecutionMode) != balancedOpenExecutionModeDual {
		return true
	}

	localTransit, ok := balancedOpenLocalTransitForRole(session.Role, meta)
	if !ok {
		return false
	}
	spendingTxid, found, err := s.lnd.FindSpendingTransactionByOutpoint(ctx, localTransit.TxID, localTransit.Vout)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("balanced-open: pending promotion spend-check failed session=%s outpoint=%s:%d err=%v", session.SessionID, localTransit.TxID, localTransit.Vout, err)
		}
		return false
	}
	if !found || strings.TrimSpace(spendingTxid) == "" {
		return false
	}
	expectedFundingTxid := strings.ToLower(strings.TrimSpace(meta.FundingTxID))
	if expectedFundingTxid != "" && !strings.EqualFold(expectedFundingTxid, spendingTxid) {
		return false
	}
	return true
}

func (s *BalancedOpenService) isBalancedOpenFundingObserved(
	ctx context.Context,
	session BalancedOpenSession,
	channelPointHint string,
	fundingTxID string,
	localTransit balancedOpenTransitDetails,
	peerTransit balancedOpenTransitDetails,
) bool {
	point := balancedOpenCanonicalChannelPointFromString(channelPointHint)
	if point != "" {
		pending, err := s.lnd.ListPendingChannels(ctx)
		if err == nil {
			if pendingCh, found := matchBalancedOpenPendingChannel(session, point, pending); found {
				// Pending-only can be a false-positive when funding shim exists but tx
				// was never propagated. Only trust pending as observed when it already
				// has a confirmation height.
				if pendingCh.ConfirmationHeight > 0 {
					return true
				}
			}
		}
		active, err := s.lnd.ListChannels(ctx)
		if err == nil {
			if _, found := matchBalancedOpenActiveChannel(session, point, active); found {
				return true
			}
		}
	}

	expectedFundingTxID := strings.ToLower(strings.TrimSpace(fundingTxID))
	if expectedFundingTxID == "" {
		return false
	}
	if seen, checked := balancedOpenTxSeenOnExternalMempool(ctx, expectedFundingTxID); checked && seen {
		return true
	}
	_ = localTransit
	_ = peerTransit
	return false
}

func (s *BalancedOpenService) ensureInitiatorDualBroadcastFromMetadata(ctx context.Context, session BalancedOpenSession) (BalancedOpenSession, error) {
	meta, err := decodeBalancedOpenMetadata(session.Metadata)
	if err != nil {
		return session, err
	}
	if normalizeBalancedExecutionMode(meta.ExecutionMode) != balancedOpenExecutionModeDual {
		return session, nil
	}
	if strings.TrimSpace(meta.ChannelPoint) == "" {
		return session, nil
	}
	txHex := strings.TrimSpace(meta.FundingTxHex)
	if !balancedTxHexAppearsSigned(txHex) {
		return session, nil
	}

	publishErr := s.publishBalancedTxWithRetry(ctx, txHex, fmt.Sprintf("balanced-open-reconcile-%s", session.SessionID), balancedOpenPublishMaxAttempts)
	if publishErr != nil {
		observed := s.isBalancedOpenFundingObserved(ctx, session, strings.TrimSpace(meta.ChannelPoint), meta.FundingTxID, meta.InitiatorTransit, meta.AccepterTransit)
		if !observed {
			targetState := balancedOpenStateChannelProposed
			if session.State == balancedOpenStatePendingOpenDetected || session.State == balancedOpenStateFundingBroadcasted {
				targetState = session.State
			}
			updated, txErr := s.transitionSessionWithMetadata(ctx, session.SessionID, targetState, publishErr.Error(), "funding_broadcast_retry_failed", map[string]any{
				"execution_mode": balancedOpenExecutionModeDual,
				"funding_tx_id":  strings.ToLower(strings.TrimSpace(meta.FundingTxID)),
			}, map[string]any{
				"last_execution_err":        publishErr.Error(),
				"last_execution_error_unix": time.Now().UTC().Unix(),
			})
			if txErr != nil {
				return session, txErr
			}
			return updated, publishErr
		}
		_, _ = s.transitionSessionWithMetadata(ctx, session.SessionID, session.State, "", "funding_broadcast_observed_after_retry_error", map[string]any{
			"execution_mode": balancedOpenExecutionModeDual,
			"funding_tx_id":  strings.ToLower(strings.TrimSpace(meta.FundingTxID)),
			"channel_point":  strings.TrimSpace(meta.ChannelPoint),
			"publish_error":  publishErr.Error(),
		}, map[string]any{
			"last_execution_err":        "",
			"last_execution_error_unix": int64(0),
		})
	}

	nextState := balancedOpenStateFundingBroadcasted
	if session.State == balancedOpenStatePendingOpenDetected {
		nextState = balancedOpenStatePendingOpenDetected
	}
	updated, err := s.transitionSessionWithMetadata(ctx, session.SessionID, nextState, "", "funding_broadcasted_reconcile", map[string]any{
		"execution_mode":  balancedOpenExecutionModeDual,
		"funding_tx_id":   strings.ToLower(strings.TrimSpace(meta.FundingTxID)),
		"funding_tx_vout": meta.FundingTxVout,
		"channel_point":   strings.TrimSpace(meta.ChannelPoint),
	}, map[string]any{
		"last_execution_err":        "",
		"last_execution_error_unix": int64(0),
	})
	if err != nil {
		return session, err
	}
	return updated, nil
}

func (s *BalancedOpenService) ProposeSession(ctx context.Context, sessionID string) (BalancedOpenSession, error) {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	if isBalancedOpenTerminalState(session.State) {
		return BalancedOpenSession{}, ErrBalancedOpenTerminalState
	}
	if session.Role != balancedOpenRoleInitiator {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidAction
	}
	if !isProposalEligibleState(session.State) {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidAction
	}

	meta, err := decodeBalancedOpenMetadata(session.Metadata)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	mode := normalizeBalancedExecutionMode(meta.ExecutionMode)
	if mode == "" {
		mode = balancedOpenExecutionModeDual
	}

	if mode == balancedOpenExecutionModeDual {
		localFundingSat := session.CapacitySat
		artifactsReady := isValidPubkeyHex(meta.InitiatorMultisigKey.PublicKey) && hasBalancedTransit(meta.InitiatorTransit, true)
		preflightSpendingSat := localFundingSat
		prefundTransitSat := int64(0)
		if !artifactsReady {
			feeRate := balancedEffectiveFeeRate(session.FeeRateSatVb)
			transitSat, transitErr := balancedTransitContribution(session.CapacitySat, feeRate)
			if transitErr != nil {
				return BalancedOpenSession{}, transitErr
			}
			prefundTransitSat = transitSat
			preflightSpendingSat += transitSat
		}

		budget, precheckErr := s.ensureBalancedOnchainBudget(ctx, preflightSpendingSat, balancedOpenAnchorSafetySat)
		if precheckErr != nil {
			_, _ = s.transitionSessionWithMetadata(ctx, session.SessionID, session.State, precheckErr.Error(), "proposal_preflight_failed", map[string]any{
				"execution_mode":          balancedOpenExecutionModeDual,
				"local_funding_sat":       localFundingSat,
				"preflight_spending_sat":  preflightSpendingSat,
				"prefund_transit_sat":     prefundTransitSat,
				"estimated_spendable_sat": budget.EstimatedSpendableSat,
				"total_sat":               budget.TotalSat,
				"locked_sat":              budget.LockedSat,
				"reserved_anchor_sat":     budget.ReservedAnchorSat,
				"required_remaining_sat":  balancedOpenAnchorSafetySat,
			}, map[string]any{
				"last_execution_err":        precheckErr.Error(),
				"last_execution_error_unix": time.Now().UTC().Unix(),
			})
			return BalancedOpenSession{}, precheckErr
		}

		nextMeta, created, err := s.ensureInitiatorDualArtifacts(ctx, session, meta)
		if err != nil {
			return BalancedOpenSession{}, err
		}
		meta = nextMeta
		if created {
			patched, err := s.transitionSessionWithMetadata(ctx, session.SessionID, session.State, "", "funding_artifacts_prepared", map[string]any{
				"execution_mode": balancedOpenExecutionModeDual,
			}, balancedEncodeMetadata(meta))
			if err != nil {
				return BalancedOpenSession{}, err
			}
			session = patched
		}
	}

	msg := balancedOpenProtocolMessage{
		Version:       balancedOpenProtocolVersion,
		Kind:          balancedOpenMessageKindProposal,
		SessionID:     session.SessionID,
		ToPubkey:      session.PeerPubkey,
		PeerHost:      session.PeerHost,
		CapacitySat:   session.CapacitySat,
		FeeRateSatVb:  session.FeeRateSatVb,
		Private:       session.Private,
		CloseAddress:  session.CloseAddress,
		ExecutionMode: mode,
		SentAtUnix:    time.Now().UTC().Unix(),
	}
	if mode == balancedOpenExecutionModeDual {
		msg.PendingChanIDHex = meta.PendingChanID
		msg.MultisigPubkey = meta.InitiatorMultisigKey.PublicKey
		msg.TransitTxID = meta.InitiatorTransit.TxID
		msg.TransitTxVout = meta.InitiatorTransit.Vout
		msg.TransitOutputSat = meta.InitiatorTransit.OutputSat
		msg.TransitOutputPk = meta.InitiatorTransit.OutputScript
	}

	if self, err := s.lnd.SelfPubkey(ctx); err == nil {
		msg.FromPubkey = strings.ToLower(strings.TrimSpace(self))
	}

	if strings.TrimSpace(session.PeerHost) != "" {
		if err := s.connectPeerForBalancedOpen(ctx, session.PeerPubkey, session.PeerHost); err != nil {
			return BalancedOpenSession{}, err
		}
	}

	if err := s.sendProtocolMessage(ctx, session.PeerPubkey, msg); err != nil {
		return BalancedOpenSession{}, err
	}

	updated, err := s.transitionSessionWithMetadata(ctx, session.SessionID, balancedOpenStateProposalSent, "", "proposal_sent", map[string]any{
		"kind":           msg.Kind,
		"execution_mode": mode,
		"sent_at_unix":   msg.SentAtUnix,
	}, balancedEncodeMetadata(meta))
	if err != nil {
		return BalancedOpenSession{}, err
	}
	s.notifyBalancedOpenStart(ctx, updated, meta, msg.FromPubkey)
	return updated, nil
}

func (s *BalancedOpenService) AcceptSession(ctx context.Context, sessionID string) (BalancedOpenSession, error) {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	if isBalancedOpenTerminalState(session.State) {
		return BalancedOpenSession{}, ErrBalancedOpenTerminalState
	}
	if session.Role != balancedOpenRoleAccepter {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidAction
	}
	if session.State != balancedOpenStateProposalReceived && session.State != balancedOpenStateFundingTxHalfSigned {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidAction
	}

	meta, err := decodeBalancedOpenMetadata(session.Metadata)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	mode := normalizeBalancedExecutionMode(meta.ExecutionMode)
	if mode == "" {
		mode = balancedOpenExecutionModePush
	}

	if mode == balancedOpenExecutionModeDual {
		artifactsReady := isValidPubkeyHex(meta.AccepterMultisigKey.PublicKey) &&
			hasBalancedTransit(meta.AccepterTransit, true) &&
			len(meta.AccepterInputWitness) > 0

		if !artifactsReady {
			feeRate := balancedEffectiveFeeRate(session.FeeRateSatVb)
			transitSat, transitErr := balancedTransitContribution(session.CapacitySat, feeRate)
			if transitErr != nil {
				return BalancedOpenSession{}, transitErr
			}
			budget, precheckErr := s.ensureBalancedOnchainBudget(ctx, transitSat, balancedOpenAnchorSafetySat)
			if precheckErr != nil {
				_, _ = s.transitionSessionWithMetadata(ctx, session.SessionID, session.State, precheckErr.Error(), "accept_preflight_failed", map[string]any{
					"execution_mode":          balancedOpenExecutionModeDual,
					"preflight_spending_sat":  transitSat,
					"estimated_spendable_sat": budget.EstimatedSpendableSat,
					"total_sat":               budget.TotalSat,
					"locked_sat":              budget.LockedSat,
					"reserved_anchor_sat":     budget.ReservedAnchorSat,
					"required_remaining_sat":  balancedOpenAnchorSafetySat,
				}, map[string]any{
					"last_execution_err":        precheckErr.Error(),
					"last_execution_error_unix": time.Now().UTC().Unix(),
				})
				return BalancedOpenSession{}, precheckErr
			}
		}
	}

	var localWitness []string
	if mode == balancedOpenExecutionModeDual {
		nextMeta, witness, created, err := s.ensureAccepterDualArtifacts(ctx, session, meta)
		meta = nextMeta
		if created {
			patched, err := s.transitionSessionWithMetadata(ctx, session.SessionID, session.State, "", "funding_artifacts_prepared", map[string]any{
				"execution_mode": mode,
				"side":           balancedOpenRoleAccepter,
			}, balancedEncodeMetadata(meta))
			if err != nil {
				return BalancedOpenSession{}, err
			}
			session = patched
		}
		if err != nil {
			return BalancedOpenSession{}, err
		}
		localWitness = witness

		patched, err := s.transitionSessionWithMetadata(ctx, session.SessionID, balancedOpenStateFundingTxHalfSigned, "", "funding_tx_half_signed", map[string]any{
			"execution_mode": mode,
			"side":           balancedOpenRoleAccepter,
		}, balancedEncodeMetadata(meta))
		if err != nil {
			return BalancedOpenSession{}, err
		}
		session = patched
	}

	msg := balancedOpenProtocolMessage{
		Version:       balancedOpenProtocolVersion,
		Kind:          balancedOpenMessageKindAccept,
		SessionID:     session.SessionID,
		ToPubkey:      session.PeerPubkey,
		ExecutionMode: mode,
		SentAtUnix:    time.Now().UTC().Unix(),
	}
	if mode == balancedOpenExecutionModeDual {
		msg.PendingChanIDHex = meta.PendingChanID
		msg.MultisigPubkey = meta.AccepterMultisigKey.PublicKey
		msg.TransitTxID = meta.AccepterTransit.TxID
		msg.TransitTxVout = meta.AccepterTransit.Vout
		msg.TransitOutputSat = meta.AccepterTransit.OutputSat
		msg.TransitOutputPk = meta.AccepterTransit.OutputScript
		msg.TransitInputStack = append([]string(nil), localWitness...)
	}

	if self, err := s.lnd.SelfPubkey(ctx); err == nil {
		msg.FromPubkey = strings.ToLower(strings.TrimSpace(self))
	}

	if strings.TrimSpace(session.PeerHost) != "" {
		if err := s.connectPeerForBalancedOpen(ctx, session.PeerPubkey, session.PeerHost); err != nil {
			return BalancedOpenSession{}, err
		}
	}

	if err := s.sendProtocolMessage(ctx, session.PeerPubkey, msg); err != nil {
		return BalancedOpenSession{}, err
	}

	updated, err := s.transitionSessionWithMetadata(ctx, session.SessionID, balancedOpenStateAccepted, "", "proposal_accepted_local", map[string]any{
		"kind":           msg.Kind,
		"execution_mode": mode,
		"sent_at_unix":   msg.SentAtUnix,
	}, balancedEncodeMetadata(meta))
	if err != nil {
		return BalancedOpenSession{}, err
	}
	return updated, nil
}

func (s *BalancedOpenService) CreateSession(ctx context.Context, params BalancedOpenCreateParams) (BalancedOpenSession, error) {
	if s == nil || s.db == nil {
		return BalancedOpenSession{}, ErrBalancedOpenDBUnavailable
	}

	pubkey := strings.ToLower(strings.TrimSpace(params.PeerPubkey))
	if !isValidPubkeyHex(pubkey) {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidPeerKey
	}
	if params.FeeRateSatVb < 0 {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidFeeRate
	}

	role := strings.TrimSpace(params.Role)
	if role == "" {
		role = balancedOpenRoleInitiator
	}
	if !isBalancedOpenRole(role) {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidRole
	}

	meta := params.Metadata
	if len(meta) == 0 {
		meta = json.RawMessage(fmt.Sprintf(`{"execution_mode":"%s"}`, balancedOpenExecutionModeDual))
	} else if !json.Valid(meta) {
		return BalancedOpenSession{}, errors.New("invalid metadata json")
	}
	metaState, err := decodeBalancedOpenMetadata(meta)
	if err != nil {
		return BalancedOpenSession{}, errors.New("invalid metadata json")
	}
	mode := normalizeBalancedExecutionMode(metaState.ExecutionMode)
	if mode == "" {
		mode = balancedOpenExecutionModeDual
	}
	if err := validateBalancedCapacityByMode(params.CapacitySat, mode); err != nil {
		return BalancedOpenSession{}, err
	}

	sessionID, err := newBalancedOpenSessionID()
	if err != nil {
		return BalancedOpenSession{}, err
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return BalancedOpenSession{}, err
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
insert into balanced_open_sessions (
  session_id,
  role,
  peer_pubkey,
  peer_host,
  capacity_sat,
  fee_rate_sat_vb,
  is_private,
  close_address,
  state,
  metadata
)
values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)
returning
  id,
  session_id,
  role,
  peer_pubkey,
  peer_host,
  capacity_sat,
  fee_rate_sat_vb,
  is_private,
  close_address,
  state,
  state_updated_at,
  attempts,
  next_retry_at,
  last_error,
  metadata,
  created_at,
  updated_at
`,
		sessionID,
		role,
		pubkey,
		strings.TrimSpace(params.PeerHost),
		params.CapacitySat,
		params.FeeRateSatVb,
		params.Private,
		strings.TrimSpace(params.CloseAddress),
		balancedOpenStateSessionCreated,
		meta,
	)

	session, err := scanBalancedOpenSession(row)
	if err != nil {
		return BalancedOpenSession{}, err
	}

	if err := s.appendEventTx(ctx, tx, sessionID, "session_created", map[string]any{
		"role":         role,
		"capacity_sat": params.CapacitySat,
		"fee_rate":     params.FeeRateSatVb,
		"private":      params.Private,
	}); err != nil {
		return BalancedOpenSession{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return BalancedOpenSession{}, err
	}

	return session, nil
}

func (s *BalancedOpenService) GetSession(ctx context.Context, sessionID string) (BalancedOpenSession, error) {
	if s == nil || s.db == nil {
		return BalancedOpenSession{}, ErrBalancedOpenDBUnavailable
	}

	id := strings.TrimSpace(sessionID)
	if id == "" {
		return BalancedOpenSession{}, ErrBalancedOpenSessionNotFound
	}

	row := s.db.QueryRow(ctx, `
select
  id,
  session_id,
  role,
  peer_pubkey,
  peer_host,
  capacity_sat,
  fee_rate_sat_vb,
  is_private,
  close_address,
  state,
  state_updated_at,
  attempts,
  next_retry_at,
  last_error,
  metadata,
  created_at,
  updated_at
from balanced_open_sessions
where session_id = $1
`, id)

	session, err := scanBalancedOpenSession(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BalancedOpenSession{}, ErrBalancedOpenSessionNotFound
		}
		return BalancedOpenSession{}, err
	}
	return session, nil
}

func (s *BalancedOpenService) ListSessionEvents(ctx context.Context, sessionID string, limit int) ([]BalancedOpenEvent, error) {
	if s == nil || s.db == nil {
		return nil, ErrBalancedOpenDBUnavailable
	}

	id := strings.TrimSpace(sessionID)
	if id == "" {
		return nil, ErrBalancedOpenSessionNotFound
	}

	if _, err := s.GetSession(ctx, id); err != nil {
		return nil, err
	}

	n := limit
	if n <= 0 {
		n = balancedOpenDefaultEventList
	}
	if n > balancedOpenMaxEventList {
		n = balancedOpenMaxEventList
	}

	rows, err := s.db.Query(ctx, `
select
  id,
  session_id,
  event_type,
  detail,
  created_at
from balanced_open_events
where session_id = $1
order by created_at desc
limit $2
`, id, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]BalancedOpenEvent, 0)
	for rows.Next() {
		event, err := scanBalancedOpenEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *BalancedOpenService) ListSessions(ctx context.Context, filter BalancedOpenListFilter) ([]BalancedOpenSession, error) {
	if s == nil || s.db == nil {
		return nil, ErrBalancedOpenDBUnavailable
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = balancedOpenDefaultListLimit
	}
	if limit > balancedOpenMaxListLimit {
		limit = balancedOpenMaxListLimit
	}

	state := strings.TrimSpace(filter.State)
	role := strings.TrimSpace(filter.Role)
	peer := strings.ToLower(strings.TrimSpace(filter.PeerPubkey))

	query := `
select
  id,
  session_id,
  role,
  peer_pubkey,
  peer_host,
  capacity_sat,
  fee_rate_sat_vb,
  is_private,
  close_address,
  state,
  state_updated_at,
  attempts,
  next_retry_at,
  last_error,
  metadata,
  created_at,
  updated_at
from balanced_open_sessions
where 1=1
`
	args := []any{}
	arg := 1

	if state != "" {
		if !isBalancedOpenState(state) {
			return nil, ErrBalancedOpenInvalidState
		}
		query += fmt.Sprintf(" and state = $%d", arg)
		args = append(args, state)
		arg++
	}

	if role != "" {
		if !isBalancedOpenRole(role) {
			return nil, ErrBalancedOpenInvalidRole
		}
		query += fmt.Sprintf(" and role = $%d", arg)
		args = append(args, role)
		arg++
	}

	if peer != "" {
		if !isValidPubkeyHex(peer) {
			return nil, ErrBalancedOpenInvalidPeerKey
		}
		query += fmt.Sprintf(" and peer_pubkey = $%d", arg)
		args = append(args, peer)
		arg++
	}

	query += fmt.Sprintf(" order by created_at desc limit $%d", arg)
	args = append(args, limit)

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]BalancedOpenSession, 0)
	for rows.Next() {
		session, err := scanBalancedOpenSession(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *BalancedOpenService) CancelSession(ctx context.Context, sessionID string, reason string) (BalancedOpenSession, error) {
	if s == nil || s.db == nil {
		return BalancedOpenSession{}, ErrBalancedOpenDBUnavailable
	}

	current, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	if isBalancedOpenTerminalState(current.State) {
		return BalancedOpenSession{}, ErrBalancedOpenTerminalState
	}
	s.tryCancelDualFundingShim(ctx, current)

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return BalancedOpenSession{}, err
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
update balanced_open_sessions
set
  state = $2,
  state_updated_at = now(),
  updated_at = now(),
  last_error = $3
where session_id = $1
returning
  id,
  session_id,
  role,
  peer_pubkey,
  peer_host,
  capacity_sat,
  fee_rate_sat_vb,
  is_private,
  close_address,
  state,
  state_updated_at,
  attempts,
  next_retry_at,
  last_error,
  metadata,
  created_at,
  updated_at
`, strings.TrimSpace(sessionID), balancedOpenStateCanceled, strings.TrimSpace(reason))

	session, err := scanBalancedOpenSession(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BalancedOpenSession{}, ErrBalancedOpenSessionNotFound
		}
		return BalancedOpenSession{}, err
	}

	if err := s.appendEventTx(ctx, tx, session.SessionID, "session_canceled", map[string]any{
		"reason": strings.TrimSpace(reason),
	}); err != nil {
		return BalancedOpenSession{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return BalancedOpenSession{}, err
	}

	return session, nil
}

func (s *BalancedOpenService) tryCancelDualFundingShim(ctx context.Context, session BalancedOpenSession) {
	meta, _ := decodeBalancedOpenMetadata(session.Metadata)
	pendingChanIDHex := strings.TrimSpace(meta.PendingChanID)
	if normalizeBalancedExecutionMode(meta.ExecutionMode) != balancedOpenExecutionModeDual ||
		pendingChanIDHex == "" ||
		session.State == balancedOpenStateActive {
		return
	}
	pendingChanID, decodeErr := hex.DecodeString(pendingChanIDHex)
	if decodeErr != nil || len(pendingChanID) == 0 {
		return
	}
	if shimErr := s.lnd.CancelFundingShim(ctx, pendingChanID); shimErr != nil {
		if !isBalancedOpenShimCancelIgnorable(shimErr) {
			_, _ = s.transitionSessionWithMetadata(ctx, session.SessionID, session.State, shimErr.Error(), "funding_shim_cancel_failed", map[string]any{
				"execution_mode":  balancedOpenExecutionModeDual,
				"pending_chan_id": pendingChanIDHex,
			}, map[string]any{
				"last_execution_err":        shimErr.Error(),
				"last_execution_error_unix": time.Now().UTC().Unix(),
			})
		}
		return
	}
	_, _ = s.transitionSessionWithMetadata(ctx, session.SessionID, session.State, "", "funding_shim_canceled", map[string]any{
		"execution_mode":  balancedOpenExecutionModeDual,
		"pending_chan_id": pendingChanIDHex,
	}, map[string]any{
		"last_execution_err":        "",
		"last_execution_error_unix": int64(0),
	})
}

func isBalancedOpenShimCancelIgnorable(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(value, "not found") ||
		strings.Contains(value, "unknown") ||
		strings.Contains(value, "no shim") ||
		strings.Contains(value, "pending chan id")
}

func (s *BalancedOpenService) RecoverSessionTransit(ctx context.Context, sessionID string, satPerVbyte int64) (BalancedOpenSession, error) {
	if s == nil || s.db == nil {
		return BalancedOpenSession{}, ErrBalancedOpenDBUnavailable
	}
	if satPerVbyte < 0 {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidFeeRate
	}

	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return BalancedOpenSession{}, err
	}

	meta, err := decodeBalancedOpenMetadata(session.Metadata)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	if normalizeBalancedExecutionMode(meta.ExecutionMode) != balancedOpenExecutionModeDual {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidAction
	}
	txidKey, addressKey, unavailableKey, ok := balancedOpenRecoveryKeysForRole(session.Role)
	if !ok {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidRole
	}
	metaMap, _ := decodeBalancedOpenMetadataMap(session.Metadata)
	recoveredRetry := session.State == balancedOpenStateRecovered &&
		balancedOpenMetadataString(metaMap, unavailableKey) != "" &&
		balancedOpenMetadataString(metaMap, txidKey) == ""
	orphanEligible := balancedOpenHasOrphanFundingCandidate(session, meta)
	orphanSweepPending := strings.TrimSpace(meta.OrphanRecoveryTxID) != "" && strings.TrimSpace(meta.OrphanLocalSweepTxID) == ""
	orphanRecoverable := orphanEligible || balancedOpenCanProcessOrphanRecovery(meta) || orphanSweepPending

	if session.State == balancedOpenStateRecovered && !recoveredRetry && !orphanRecoverable {
		return BalancedOpenSession{}, ErrBalancedOpenTerminalState
	}
	if orphanRecoverable && strings.TrimSpace(meta.OrphanRecoveryTxID) != "" {
		if strings.TrimSpace(meta.OrphanLocalSweepTxID) == "" {
			if swept, sweepErr := s.sweepLocalOrphanRecoveredOutput(ctx, session, meta, satPerVbyte); sweepErr == nil {
				return swept, nil
			}
		}
		return session, nil
	}
	if session.State == balancedOpenStateActive && !orphanRecoverable {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidAction
	}
	if session.State != balancedOpenStateRecoveryRequired &&
		session.State != balancedOpenStateCanceled &&
		!(session.State == balancedOpenStateActive && orphanRecoverable) &&
		!(session.State == balancedOpenStateRecovered && orphanRecoverable) &&
		!recoveredRetry {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidAction
	}

	localTransit, ok := balancedOpenLocalTransitForRole(session.Role, meta)
	if !ok {
		return BalancedOpenSession{}, errors.New("missing local transit details")
	}
	outpoint := fmt.Sprintf("%s:%d", strings.ToLower(strings.TrimSpace(localTransit.TxID)), localTransit.Vout)

	pending, err := s.lnd.ListPendingChannels(ctx)
	if err == nil {
		if pendingCh, found := matchBalancedOpenPendingChannel(session, balancedOpenSessionChannelPoint(session.Metadata), pending); found {
			if strings.EqualFold(strings.TrimSpace(pendingCh.Status), "opening") {
				return BalancedOpenSession{}, errors.New("cannot recover transit while channel is pending open")
			}
		}
	}
	active, err := s.lnd.ListChannels(ctx)
	if err == nil {
		_, _ = matchBalancedOpenActiveChannel(session, balancedOpenSessionChannelPoint(session.Metadata), active)
	}

	if orphanRecoverable && strings.TrimSpace(meta.OrphanRecoveryTxID) == "" {
		spendingTxid, foundSpender, lookupErr := s.lnd.FindSpendingTransactionByOutpoint(ctx, localTransit.TxID, localTransit.Vout)
		if lookupErr == nil && foundSpender && strings.EqualFold(strings.TrimSpace(spendingTxid), strings.TrimSpace(meta.FundingTxID)) {
			return s.recoverOrphanFundingOutput(ctx, session, meta, satPerVbyte, true, true)
		}
	}

	txid, address, err := s.publishBalancedTransitRecovery(ctx, session, localTransit, satPerVbyte)
	if err != nil {
		if isBalancedOpenOutpointUnavailableError(err) {
			spendingTxid, foundSpender, lookupErr := s.lnd.FindSpendingTransactionByOutpoint(ctx, localTransit.TxID, localTransit.Vout)
			if lookupErr == nil &&
				foundSpender &&
				orphanRecoverable &&
				strings.EqualFold(strings.TrimSpace(spendingTxid), strings.TrimSpace(meta.FundingTxID)) &&
				strings.TrimSpace(meta.OrphanRecoveryTxID) == "" {
				return s.recoverOrphanFundingOutput(ctx, session, meta, satPerVbyte, true, true)
			}
			if lookupErr == nil && foundSpender && strings.TrimSpace(spendingTxid) != "" {
				patch := map[string]any{
					"last_execution_err":        "",
					"last_execution_error_unix": int64(0),
				}
				patch[txidKey] = spendingTxid
				patch[unavailableKey] = ""
				updated, txErr := s.transitionSessionWithMetadata(ctx, session.SessionID, balancedOpenStateRecovered, "", "transit_outpoint_already_spent", map[string]any{
					"role":            session.Role,
					"transit_tx_id":   localTransit.TxID,
					"transit_tx_vout": localTransit.Vout,
					"outpoint":        outpoint,
					"spending_txid":   spendingTxid,
				}, patch)
				if txErr != nil {
					return BalancedOpenSession{}, txErr
				}
				return updated, nil
			}
			return BalancedOpenSession{}, fmt.Errorf("%w: %s", ErrBalancedOpenTransitOutpointUnavailable, outpoint)
		}
		return BalancedOpenSession{}, err
	}

	patch := map[string]any{
		"last_execution_err":        "",
		"last_execution_error_unix": int64(0),
	}
	patch[txidKey] = txid
	patch[addressKey] = address
	patch[unavailableKey] = ""

	updated, err := s.transitionSessionWithMetadata(ctx, session.SessionID, balancedOpenStateRecovered, "", "transit_recovered", map[string]any{
		"role":             session.Role,
		"transit_tx_id":    localTransit.TxID,
		"transit_tx_vout":  localTransit.Vout,
		"recovery_txid":    txid,
		"recovery_address": address,
		"sat_per_vbyte":    satPerVbyte,
	}, patch)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	return updated, nil
}

func (s *BalancedOpenService) RetrySessionBroadcast(ctx context.Context, sessionID string) (BalancedOpenSession, error) {
	if s == nil || s.db == nil {
		return BalancedOpenSession{}, ErrBalancedOpenDBUnavailable
	}

	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	if isBalancedOpenTerminalState(session.State) {
		return BalancedOpenSession{}, ErrBalancedOpenTerminalState
	}
	if session.Role != balancedOpenRoleInitiator {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidAction
	}
	if session.State != balancedOpenStateChannelProposed &&
		session.State != balancedOpenStatePendingOpenDetected &&
		session.State != balancedOpenStateRecoveryRequired {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidAction
	}

	meta, err := decodeBalancedOpenMetadata(session.Metadata)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	if normalizeBalancedExecutionMode(meta.ExecutionMode) != balancedOpenExecutionModeDual {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidAction
	}
	if strings.TrimSpace(meta.ChannelPoint) == "" {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidAction
	}
	if !balancedTxHexAppearsSigned(meta.FundingTxHex) {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidAction
	}

	updated, err := s.ensureInitiatorDualBroadcastFromMetadata(ctx, session)
	if err != nil {
		return updated, err
	}
	return updated, nil
}

func (s *BalancedOpenService) handleIncomingCustomMessage(msg lndclient.CustomPeerMessage) {
	if msg.Type != balancedOpenCustomMsgType {
		return
	}

	sender := strings.ToLower(strings.TrimSpace(msg.PeerPubkey))
	if !isValidPubkeyHex(sender) {
		return
	}

	var envelope balancedOpenProtocolMessage
	if err := json.Unmarshal(msg.Data, &envelope); err != nil {
		if s.logger != nil {
			s.logger.Printf("balanced-open: invalid protocol payload from %s: %v", sender, err)
		}
		return
	}
	if envelope.Version != balancedOpenProtocolVersion || strings.TrimSpace(envelope.SessionID) == "" {
		return
	}
	if envelope.FromPubkey == "" {
		envelope.FromPubkey = sender
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	switch envelope.Kind {
	case balancedOpenMessageKindProposal:
		if _, err := s.upsertSessionFromProposal(ctx, sender, envelope); err != nil && s.logger != nil {
			s.logger.Printf("balanced-open: failed processing proposal session=%s from=%s err=%v", envelope.SessionID, sender, err)
		}
	case balancedOpenMessageKindAccept:
		if _, err := s.markAcceptedFromPeer(ctx, sender, envelope); err != nil && s.logger != nil {
			s.logger.Printf("balanced-open: failed processing accept session=%s from=%s err=%v", envelope.SessionID, sender, err)
		}
	case balancedOpenMessageKindCancel:
		if _, err := s.markCanceledFromPeer(ctx, sender, envelope); err != nil && s.logger != nil {
			s.logger.Printf("balanced-open: failed processing cancel session=%s from=%s err=%v", envelope.SessionID, sender, err)
		}
	case balancedOpenMessageKindOrphanRecoveryRequest:
		if _, err := s.handleOrphanRecoveryRequestFromPeer(ctx, sender, envelope); err != nil && s.logger != nil {
			s.logger.Printf("balanced-open: failed processing orphan recovery request session=%s from=%s err=%v", envelope.SessionID, sender, err)
		}
	case balancedOpenMessageKindOrphanRecoverySig:
		if _, err := s.handleOrphanRecoverySignatureFromPeer(ctx, sender, envelope); err != nil && s.logger != nil {
			s.logger.Printf("balanced-open: failed processing orphan recovery signature session=%s from=%s err=%v", envelope.SessionID, sender, err)
		}
	default:
		return
	}
}

func (s *BalancedOpenService) upsertSessionFromProposal(ctx context.Context, sender string, msg balancedOpenProtocolMessage) (BalancedOpenSession, error) {
	if !isValidPubkeyHex(sender) {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidPeerKey
	}
	if msg.FeeRateSatVb < 0 {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidFeeRate
	}
	mode := normalizeBalancedExecutionMode(msg.ExecutionMode)
	if mode == "" {
		mode = balancedOpenExecutionModePush
	}
	if err := validateBalancedCapacityByMode(msg.CapacitySat, mode); err != nil {
		return BalancedOpenSession{}, err
	}
	if mode == balancedOpenExecutionModeDual {
		if !isValidPubkeyHex(msg.MultisigPubkey) {
			return BalancedOpenSession{}, errors.New("proposal missing multisig pubkey")
		}
		if !isBalancedOpenTxID(msg.TransitTxID) {
			return BalancedOpenSession{}, errors.New("proposal missing transit tx id")
		}
		if msg.TransitOutputSat <= 0 {
			return BalancedOpenSession{}, errors.New("proposal missing transit output amount")
		}
		if !isBalancedOpenScriptHex(msg.TransitOutputPk) {
			return BalancedOpenSession{}, errors.New("proposal missing transit output script")
		}
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return BalancedOpenSession{}, err
	}
	defer tx.Rollback(ctx)

	metaPatch := map[string]any{
		"execution_mode":   mode,
		"protocol_version": msg.Version,
		"last_message": map[string]any{
			"kind":         msg.Kind,
			"from_pubkey":  sender,
			"to_pubkey":    msg.ToPubkey,
			"sent_at_unix": msg.SentAtUnix,
		},
	}
	if mode == balancedOpenExecutionModeDual {
		if isBalancedPendingChanIDHex(msg.PendingChanIDHex) {
			metaPatch["pending_chan_id"] = strings.ToLower(strings.TrimSpace(msg.PendingChanIDHex))
		}
		metaPatch["initiator_multisig_key"] = map[string]any{
			"public_key": strings.ToLower(strings.TrimSpace(msg.MultisigPubkey)),
			"family":     balancedOpenMultiSigKeyFamily,
			"index":      0,
		}
		metaPatch["initiator_transit"] = map[string]any{
			"tx_id":         strings.ToLower(strings.TrimSpace(msg.TransitTxID)),
			"vout":          msg.TransitTxVout,
			"output_sat":    msg.TransitOutputSat,
			"output_script": strings.ToLower(strings.TrimSpace(msg.TransitOutputPk)),
		}
	}

	meta, err := marshalBalancedOpenJSON(metaPatch)
	if err != nil {
		return BalancedOpenSession{}, err
	}

	row := tx.QueryRow(ctx, `
insert into balanced_open_sessions (
  session_id,
  role,
  peer_pubkey,
  peer_host,
  capacity_sat,
  fee_rate_sat_vb,
  is_private,
  close_address,
  state,
  state_updated_at,
  metadata
)
values ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), $10::jsonb)
on conflict (session_id) do update
set
  peer_pubkey = excluded.peer_pubkey,
  peer_host = case when balanced_open_sessions.peer_host = '' then excluded.peer_host else balanced_open_sessions.peer_host end,
  capacity_sat = excluded.capacity_sat,
  fee_rate_sat_vb = excluded.fee_rate_sat_vb,
  is_private = excluded.is_private,
  close_address = excluded.close_address,
  state = case
    when balanced_open_sessions.state in ('active', 'failed', 'canceled', 'recovered') then balanced_open_sessions.state
    else $9
  end,
  state_updated_at = case
    when balanced_open_sessions.state in ('active', 'failed', 'canceled', 'recovered') then balanced_open_sessions.state_updated_at
    else now()
  end,
  metadata = coalesce(balanced_open_sessions.metadata, '{}'::jsonb) || $10::jsonb,
  updated_at = now()
returning
  id,
  session_id,
  role,
  peer_pubkey,
  peer_host,
  capacity_sat,
  fee_rate_sat_vb,
  is_private,
  close_address,
  state,
  state_updated_at,
  attempts,
  next_retry_at,
  last_error,
  metadata,
  created_at,
  updated_at
`,
		msg.SessionID,
		balancedOpenRoleAccepter,
		sender,
		strings.TrimSpace(msg.PeerHost),
		msg.CapacitySat,
		msg.FeeRateSatVb,
		msg.Private,
		strings.TrimSpace(msg.CloseAddress),
		balancedOpenStateProposalReceived,
		meta,
	)

	session, err := scanBalancedOpenSession(row)
	if err != nil {
		return BalancedOpenSession{}, err
	}

	if err := s.appendEventTx(ctx, tx, session.SessionID, "proposal_received", map[string]any{
		"from_pubkey":    sender,
		"capacity_sat":   msg.CapacitySat,
		"fee_rate":       msg.FeeRateSatVb,
		"private":        msg.Private,
		"execution_mode": mode,
		"sent_at_unix":   msg.SentAtUnix,
	}); err != nil {
		return BalancedOpenSession{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return BalancedOpenSession{}, err
	}
	metaState, err := decodeBalancedOpenMetadata(session.Metadata)
	if err == nil {
		s.notifyBalancedOpenStart(ctx, session, metaState, sender)
	}
	return session, nil
}

func (s *BalancedOpenService) markAcceptedFromPeer(ctx context.Context, sender string, msg balancedOpenProtocolMessage) (BalancedOpenSession, error) {
	session, err := s.GetSession(ctx, msg.SessionID)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(session.PeerPubkey), sender) {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidPeerKey
	}
	if isBalancedOpenTerminalState(session.State) {
		return session, nil
	}

	meta, err := decodeBalancedOpenMetadata(session.Metadata)
	if err != nil {
		return BalancedOpenSession{}, err
	}

	mode := normalizeBalancedExecutionMode(msg.ExecutionMode)
	if mode == "" {
		mode = normalizeBalancedExecutionMode(meta.ExecutionMode)
	}
	if mode == "" {
		mode = balancedOpenExecutionModePush
	}

	if mode == balancedOpenExecutionModeDual {
		if !isValidPubkeyHex(msg.MultisigPubkey) {
			return BalancedOpenSession{}, errors.New("accept missing multisig pubkey")
		}
		if !isBalancedOpenTxID(msg.TransitTxID) {
			return BalancedOpenSession{}, errors.New("accept missing transit tx id")
		}
		if msg.TransitOutputSat <= 0 {
			return BalancedOpenSession{}, errors.New("accept missing transit output amount")
		}
		if !isBalancedOpenScriptHex(msg.TransitOutputPk) {
			return BalancedOpenSession{}, errors.New("accept missing transit output script")
		}
		if len(msg.TransitInputStack) == 0 {
			return BalancedOpenSession{}, errors.New("accept missing funding input witness")
		}
		derivedPendingID, err := balancedPendingChanIDHexFromMultisig(meta.InitiatorMultisigKey.PublicKey, msg.MultisigPubkey)
		if err != nil {
			return BalancedOpenSession{}, fmt.Errorf("accept missing valid pending channel id context: %w", err)
		}
		if isBalancedPendingChanIDHex(msg.PendingChanIDHex) {
			pendingID := strings.ToLower(strings.TrimSpace(msg.PendingChanIDHex))
			if pendingID != derivedPendingID {
				return BalancedOpenSession{}, errors.New("accept pending channel id mismatch")
			}
		}

		meta.ExecutionMode = balancedOpenExecutionModeDual
		meta.PendingChanID = derivedPendingID
		meta.AccepterMultisigKey = balancedOpenKeyDescriptor{
			PublicKey: strings.ToLower(strings.TrimSpace(msg.MultisigPubkey)),
			Family:    balancedOpenMultiSigKeyFamily,
			Index:     0,
		}
		meta.AccepterTransit = balancedOpenTransitDetails{
			TxID:         strings.ToLower(strings.TrimSpace(msg.TransitTxID)),
			Vout:         msg.TransitTxVout,
			OutputSat:    msg.TransitOutputSat,
			OutputScript: strings.ToLower(strings.TrimSpace(msg.TransitOutputPk)),
		}
		meta.AccepterInputWitness = append([]string(nil), msg.TransitInputStack...)
	}

	updated, err := s.transitionSessionWithMetadata(ctx, session.SessionID, balancedOpenStateAccepted, "", "proposal_accepted_remote", map[string]any{
		"from_pubkey":    sender,
		"execution_mode": mode,
		"sent_at_unix":   msg.SentAtUnix,
	}, balancedEncodeMetadata(meta))
	if err != nil {
		return BalancedOpenSession{}, err
	}
	return updated, nil
}

func (s *BalancedOpenService) markCanceledFromPeer(ctx context.Context, sender string, msg balancedOpenProtocolMessage) (BalancedOpenSession, error) {
	session, err := s.GetSession(ctx, msg.SessionID)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(session.PeerPubkey), sender) {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidPeerKey
	}
	if isBalancedOpenTerminalState(session.State) {
		s.tryCancelDualFundingShim(ctx, session)
		return session, nil
	}
	s.tryCancelDualFundingShim(ctx, session)

	reason := strings.TrimSpace(msg.Reason)
	if reason == "" {
		reason = "peer canceled"
	}
	return s.transitionSession(ctx, session.SessionID, balancedOpenStateCanceled, reason, "session_canceled_remote", map[string]any{
		"from_pubkey":  sender,
		"reason":       reason,
		"sent_at_unix": msg.SentAtUnix,
	})
}

func (s *BalancedOpenService) handleOrphanRecoveryRequestFromPeer(ctx context.Context, sender string, msg balancedOpenProtocolMessage) (BalancedOpenSession, error) {
	session, err := s.GetSession(ctx, msg.SessionID)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(session.PeerPubkey), sender) {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidPeerKey
	}

	meta, err := decodeBalancedOpenMetadata(session.Metadata)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	if normalizeBalancedExecutionMode(meta.ExecutionMode) != balancedOpenExecutionModeDual {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidAction
	}
	if !balancedOpenHasOrphanFundingCandidate(session, meta) && !balancedOpenCanProcessOrphanRecovery(meta) {
		return BalancedOpenSession{}, errors.New("orphan funding recovery not required")
	}
	if !isBalancedOpenSigHex(msg.RecoverySigHex) {
		return BalancedOpenSession{}, errors.New("missing orphan recovery signature")
	}
	if msg.RecoveryFeeRate < 0 {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidFeeRate
	}
	if txid := strings.ToLower(strings.TrimSpace(msg.FundingTxID)); txid != "" && !strings.EqualFold(txid, meta.FundingTxID) {
		return BalancedOpenSession{}, errors.New("orphan recovery funding tx mismatch")
	}
	if msg.FundingTxVout != 0 && msg.FundingTxVout != meta.FundingTxVout {
		return BalancedOpenSession{}, errors.New("orphan recovery funding vout mismatch")
	}

	remoteRole := balancedOpenCounterpartyRole(session.Role)
	if remoteRole == "" {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidRole
	}
	meta = balancedOpenSetOrphanSigForRole(meta, remoteRole, msg.RecoverySigHex)
	feeRate := msg.RecoveryFeeRate
	if feeRate <= 0 {
		feeRate = meta.OrphanRecoveryFeeRate
	}
	if feeRate <= 0 {
		feeRate = 1
	}
	if meta.OrphanRecoveryFeeRate > 0 && meta.OrphanRecoveryFeeRate != feeRate {
		return BalancedOpenSession{}, errors.New("orphan recovery fee rate mismatch")
	}
	meta.OrphanRecoveryFeeRate = feeRate

	progressState := balancedOpenOrphanProgressState(session.State)
	updated, err := s.transitionSessionWithMetadata(ctx, session.SessionID, progressState, "", "orphan_recovery_request_received", map[string]any{
		"from_pubkey":     sender,
		"funding_tx_id":   meta.FundingTxID,
		"funding_tx_vout": meta.FundingTxVout,
		"fee_rate":        feeRate,
	}, balancedEncodeMetadata(meta))
	if err != nil {
		return BalancedOpenSession{}, err
	}

	updatedMeta, err := decodeBalancedOpenMetadata(updated.Metadata)
	if err != nil {
		return BalancedOpenSession{}, err
	}

	updated, err = s.recoverOrphanFundingOutput(ctx, updated, updatedMeta, feeRate, false, true)
	if err != nil {
		return BalancedOpenSession{}, err
	}

	finalMeta, err := decodeBalancedOpenMetadata(updated.Metadata)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	localSig := balancedOpenOrphanSigForRole(finalMeta, updated.Role)
	if isBalancedOpenSigHex(localSig) {
		if err := s.sendOrphanRecoveryMessage(ctx, updated, finalMeta, balancedOpenMessageKindOrphanRecoverySig, feeRate, localSig); err != nil {
			return BalancedOpenSession{}, err
		}
	}

	return updated, nil
}

func (s *BalancedOpenService) handleOrphanRecoverySignatureFromPeer(ctx context.Context, sender string, msg balancedOpenProtocolMessage) (BalancedOpenSession, error) {
	session, err := s.GetSession(ctx, msg.SessionID)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(session.PeerPubkey), sender) {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidPeerKey
	}

	meta, err := decodeBalancedOpenMetadata(session.Metadata)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	if normalizeBalancedExecutionMode(meta.ExecutionMode) != balancedOpenExecutionModeDual {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidAction
	}
	if !balancedOpenHasOrphanFundingCandidate(session, meta) && !balancedOpenCanProcessOrphanRecovery(meta) {
		return BalancedOpenSession{}, errors.New("orphan funding recovery not required")
	}
	if !isBalancedOpenSigHex(msg.RecoverySigHex) {
		return BalancedOpenSession{}, errors.New("missing orphan recovery signature")
	}
	if msg.RecoveryFeeRate < 0 {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidFeeRate
	}
	if txid := strings.ToLower(strings.TrimSpace(msg.FundingTxID)); txid != "" && !strings.EqualFold(txid, meta.FundingTxID) {
		return BalancedOpenSession{}, errors.New("orphan recovery funding tx mismatch")
	}
	if msg.FundingTxVout != 0 && msg.FundingTxVout != meta.FundingTxVout {
		return BalancedOpenSession{}, errors.New("orphan recovery funding vout mismatch")
	}

	remoteRole := balancedOpenCounterpartyRole(session.Role)
	if remoteRole == "" {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidRole
	}
	meta = balancedOpenSetOrphanSigForRole(meta, remoteRole, msg.RecoverySigHex)
	feeRate := msg.RecoveryFeeRate
	if feeRate <= 0 {
		feeRate = meta.OrphanRecoveryFeeRate
	}
	if feeRate <= 0 {
		feeRate = 1
	}
	if meta.OrphanRecoveryFeeRate > 0 && meta.OrphanRecoveryFeeRate != feeRate {
		return BalancedOpenSession{}, errors.New("orphan recovery fee rate mismatch")
	}
	meta.OrphanRecoveryFeeRate = feeRate

	progressState := balancedOpenOrphanProgressState(session.State)
	updated, err := s.transitionSessionWithMetadata(ctx, session.SessionID, progressState, "", "orphan_recovery_signature_received", map[string]any{
		"from_pubkey":     sender,
		"funding_tx_id":   meta.FundingTxID,
		"funding_tx_vout": meta.FundingTxVout,
		"fee_rate":        feeRate,
	}, balancedEncodeMetadata(meta))
	if err != nil {
		return BalancedOpenSession{}, err
	}

	updatedMeta, err := decodeBalancedOpenMetadata(updated.Metadata)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	return s.recoverOrphanFundingOutput(ctx, updated, updatedMeta, feeRate, false, true)
}

func (s *BalancedOpenService) recoverOrphanFundingOutput(ctx context.Context, session BalancedOpenSession, meta balancedOpenMetadata, satPerVbyte int64, notifyPeer bool, allowWithoutCandidate bool) (BalancedOpenSession, error) {
	if normalizeBalancedExecutionMode(meta.ExecutionMode) != balancedOpenExecutionModeDual {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidAction
	}
	if !balancedOpenHasOrphanFundingCandidate(session, meta) && !(allowWithoutCandidate && balancedOpenCanProcessOrphanRecovery(meta)) {
		return BalancedOpenSession{}, errors.New("orphan funding recovery not required")
	}

	feeRate := satPerVbyte
	if feeRate <= 0 {
		feeRate = meta.OrphanRecoveryFeeRate
	}
	if feeRate <= 0 {
		feeRate = 1
	}
	if meta.OrphanRecoveryFeeRate > 0 && satPerVbyte > 0 && meta.OrphanRecoveryFeeRate != satPerVbyte {
		return BalancedOpenSession{}, errors.New("orphan recovery already started with another fee rate")
	}
	meta.OrphanRecoveryFeeRate = feeRate

	plan, err := buildBalancedOrphanRecoveryPlan(session, meta, feeRate)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	localKey, ok := balancedOpenLocalMultisigKeyForRole(session.Role, meta)
	if !ok {
		return BalancedOpenSession{}, errors.New("missing local multisig recovery key")
	}

	localSig := balancedOpenOrphanSigForRole(meta, session.Role)
	if !isBalancedOpenSigHex(localSig) {
		rawSig, err := s.lnd.SignOutputRaw(ctx, lndclient.SignOutputRawParams{
			RawTxHex:        plan.TxHex,
			InputIndex:      0,
			OutputScriptHex: plan.FundingPkScriptHex,
			OutputSat:       plan.FundingOutputSat,
			Key: lndclient.DerivedKey{
				PublicKey: localKey.PublicKey,
				Family:    localKey.Family,
				Index:     localKey.Index,
			},
			WitnessScriptHex: plan.WitnessScriptHex,
		})
		if err != nil {
			return BalancedOpenSession{}, err
		}
		// SignOutputRaw returns DER signature without sighash flag.
		if len(rawSig) == 0 || rawSig[0] != 0x30 {
			return BalancedOpenSession{}, errors.New("invalid orphan recovery signature format")
		}
		sigHex := hex.EncodeToString(append(append([]byte(nil), rawSig...), byte(0x01)))
		meta = balancedOpenSetOrphanSigForRole(meta, session.Role, sigHex)
		localSig = sigHex
	}

	progressState := balancedOpenOrphanProgressState(session.State)
	updated, err := s.transitionSessionWithMetadata(ctx, session.SessionID, progressState, "", "orphan_recovery_half_signed", map[string]any{
		"role":            session.Role,
		"funding_tx_id":   meta.FundingTxID,
		"funding_tx_vout": meta.FundingTxVout,
		"fee_rate":        feeRate,
	}, balancedEncodeMetadata(meta))
	if err != nil {
		return BalancedOpenSession{}, err
	}

	if notifyPeer {
		if err := s.sendOrphanRecoveryMessage(ctx, updated, meta, balancedOpenMessageKindOrphanRecoveryRequest, feeRate, localSig); err != nil {
			return BalancedOpenSession{}, err
		}
	}

	updatedMeta, err := decodeBalancedOpenMetadata(updated.Metadata)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	initSig := strings.ToLower(strings.TrimSpace(updatedMeta.OrphanInitiatorSigHex))
	acceptSig := strings.ToLower(strings.TrimSpace(updatedMeta.OrphanAccepterSigHex))
	if !isBalancedOpenSigHex(initSig) || !isBalancedOpenSigHex(acceptSig) {
		return updated, nil
	}

	finalTx := plan.Tx.Copy()
	witness, err := balancedBuildOrphanRecoveryWitness(updatedMeta, plan.WitnessScript, initSig, acceptSig)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	finalTx.TxIn[0].Witness = witness
	finalHex, err := encodeBalancedTxHex(finalTx)
	if err != nil {
		return BalancedOpenSession{}, err
	}

	recoveryTxID := finalTx.TxHash().String()
	publishErr := s.lnd.PublishTransaction(ctx, finalHex, fmt.Sprintf("balanced-open-orphan-recover-%s", updated.SessionID))
	if publishErr != nil && !isBalancedOpenAlreadyPublishedError(publishErr) {
		if isBalancedOpenOutpointUnavailableError(publishErr) {
			spendingTxid, foundSpender, lookupErr := s.lnd.FindSpendingTransactionByOutpoint(ctx, updatedMeta.FundingTxID, updatedMeta.FundingTxVout)
			if lookupErr == nil && foundSpender && strings.TrimSpace(spendingTxid) != "" {
				recoveryTxID = strings.TrimSpace(spendingTxid)
			} else {
				return BalancedOpenSession{}, publishErr
			}
		} else {
			return BalancedOpenSession{}, publishErr
		}
	}

	updatedMeta.OrphanRecoveryTxID = strings.ToLower(strings.TrimSpace(recoveryTxID))
	finalState := balancedOpenOrphanFinalState(updated.State)

	updated, err = s.transitionSessionWithMetadata(ctx, updated.SessionID, finalState, "", "orphan_funding_recovered", map[string]any{
		"funding_tx_id":        updatedMeta.FundingTxID,
		"funding_tx_vout":      updatedMeta.FundingTxVout,
		"orphan_recovery_txid": updatedMeta.OrphanRecoveryTxID,
		"fee_rate":             updatedMeta.OrphanRecoveryFeeRate,
		"initiator_output_sat": plan.InitiatorOutputSat,
		"accepter_output_sat":  plan.AccepterOutputSat,
	}, balancedEncodeMetadata(updatedMeta))
	if err != nil {
		return BalancedOpenSession{}, err
	}

	latestMeta, err := decodeBalancedOpenMetadata(updated.Metadata)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	if strings.TrimSpace(latestMeta.OrphanLocalSweepTxID) != "" {
		return updated, nil
	}
	if swept, sweepErr := s.sweepLocalOrphanRecoveredOutput(ctx, updated, latestMeta, feeRate); sweepErr == nil {
		return swept, nil
	}

	return updated, nil
}

func (s *BalancedOpenService) sendOrphanRecoveryMessage(ctx context.Context, session BalancedOpenSession, meta balancedOpenMetadata, kind string, feeRate int64, sigHex string) error {
	if !isBalancedOpenSigHex(sigHex) {
		return errors.New("invalid orphan recovery signature")
	}
	msg := balancedOpenProtocolMessage{
		Version:         balancedOpenProtocolVersion,
		Kind:            kind,
		SessionID:       session.SessionID,
		ToPubkey:        session.PeerPubkey,
		ExecutionMode:   balancedOpenExecutionModeDual,
		FundingTxID:     strings.ToLower(strings.TrimSpace(meta.FundingTxID)),
		FundingTxVout:   meta.FundingTxVout,
		RecoverySigHex:  strings.ToLower(strings.TrimSpace(sigHex)),
		RecoveryFeeRate: feeRate,
		SentAtUnix:      time.Now().UTC().Unix(),
	}
	if self, err := s.lnd.SelfPubkey(ctx); err == nil {
		msg.FromPubkey = strings.ToLower(strings.TrimSpace(self))
	}
	if strings.TrimSpace(session.PeerHost) != "" {
		if err := s.connectPeerForBalancedOpen(ctx, session.PeerPubkey, session.PeerHost); err != nil {
			return err
		}
	}
	return s.sendProtocolMessage(ctx, session.PeerPubkey, msg)
}

func (s *BalancedOpenService) sweepLocalOrphanRecoveredOutput(ctx context.Context, session BalancedOpenSession, meta balancedOpenMetadata, satPerVbyte int64) (BalancedOpenSession, error) {
	orphanTxID := strings.ToLower(strings.TrimSpace(meta.OrphanRecoveryTxID))
	if !isBalancedOpenTxID(orphanTxID) {
		return BalancedOpenSession{}, errors.New("missing orphan recovery tx id")
	}
	if strings.TrimSpace(meta.OrphanLocalSweepTxID) != "" {
		return session, nil
	}

	localTransit, ok := balancedOpenLocalTransitForRole(session.Role, meta)
	if !ok {
		return BalancedOpenSession{}, errors.New("missing local transit details")
	}

	feeRate := satPerVbyte
	if feeRate <= 0 {
		feeRate = meta.OrphanRecoveryFeeRate
	}
	if feeRate <= 0 {
		feeRate = 1
	}

	plan, err := buildBalancedOrphanRecoveryPlan(session, meta, meta.OrphanRecoveryFeeRate)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	localVout, localSat, ok := balancedOpenOrphanOutputForRole(session.Role, plan)
	if !ok {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidRole
	}
	localTransit.TxID = orphanTxID
	localTransit.Vout = localVout
	localTransit.OutputSat = localSat

	sweepTxID, sweepAddr, err := s.publishBalancedTransitRecovery(ctx, session, localTransit, feeRate)
	if err != nil {
		if isBalancedOpenOutpointUnavailableError(err) {
			spendingTxid, foundSpender, lookupErr := s.lnd.FindSpendingTransactionByOutpoint(ctx, orphanTxID, localVout)
			if lookupErr == nil && foundSpender && strings.TrimSpace(spendingTxid) != "" {
				patch := map[string]any{
					"orphan_local_sweep_txid":    strings.ToLower(strings.TrimSpace(spendingTxid)),
					"orphan_local_sweep_address": "",
					"last_execution_err":         "",
					"last_execution_error_unix":  int64(0),
				}
				return s.transitionSessionWithMetadata(ctx, session.SessionID, session.State, "", "orphan_local_sweep_already_spent", map[string]any{
					"role":                 session.Role,
					"orphan_recovery_txid": orphanTxID,
					"orphan_output_vout":   localVout,
					"sweep_txid":           strings.ToLower(strings.TrimSpace(spendingTxid)),
				}, patch)
			}
		}
		return BalancedOpenSession{}, err
	}

	patch := map[string]any{
		"orphan_local_sweep_txid":    strings.ToLower(strings.TrimSpace(sweepTxID)),
		"orphan_local_sweep_address": strings.TrimSpace(sweepAddr),
		"last_execution_err":         "",
		"last_execution_error_unix":  int64(0),
	}
	return s.transitionSessionWithMetadata(ctx, session.SessionID, session.State, "", "orphan_local_sweep_broadcasted", map[string]any{
		"role":                 session.Role,
		"orphan_recovery_txid": orphanTxID,
		"orphan_output_vout":   localVout,
		"sweep_txid":           strings.ToLower(strings.TrimSpace(sweepTxID)),
		"sweep_address":        strings.TrimSpace(sweepAddr),
	}, patch)
}

func (s *BalancedOpenService) transitionSession(ctx context.Context, sessionID string, nextState string, lastError string, eventType string, detail any) (BalancedOpenSession, error) {
	return s.transitionSessionWithMetadata(ctx, sessionID, nextState, lastError, eventType, detail, nil)
}

func (s *BalancedOpenService) transitionSessionWithMetadata(ctx context.Context, sessionID string, nextState string, lastError string, eventType string, detail any, metadataPatch any) (BalancedOpenSession, error) {
	if !isBalancedOpenState(nextState) {
		return BalancedOpenSession{}, ErrBalancedOpenInvalidState
	}

	current, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return BalancedOpenSession{}, err
	}
	if isBalancedOpenTerminalState(current.State) {
		if !(current.State == balancedOpenStateCanceled && nextState == balancedOpenStateRecovered) &&
			!(current.State == balancedOpenStateRecovered && nextState == balancedOpenStateRecovered) {
			return BalancedOpenSession{}, ErrBalancedOpenTerminalState
		}
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return BalancedOpenSession{}, err
	}
	defer tx.Rollback(ctx)

	rawMeta, err := marshalBalancedOpenJSON(metadataPatch)
	if err != nil {
		return BalancedOpenSession{}, err
	}

	row := tx.QueryRow(ctx, `
update balanced_open_sessions
set
  state = $2,
  state_updated_at = now(),
  updated_at = now(),
  last_error = $3,
  metadata = coalesce(metadata, '{}'::jsonb) || $4::jsonb
where session_id = $1
returning
  id,
  session_id,
  role,
  peer_pubkey,
  peer_host,
  capacity_sat,
  fee_rate_sat_vb,
  is_private,
  close_address,
  state,
  state_updated_at,
  attempts,
  next_retry_at,
  last_error,
  metadata,
  created_at,
  updated_at
`, strings.TrimSpace(sessionID), nextState, strings.TrimSpace(lastError), rawMeta)
	session, err := scanBalancedOpenSession(row)
	if err != nil {
		return BalancedOpenSession{}, err
	}

	if strings.TrimSpace(eventType) != "" {
		if err := s.appendEventTx(ctx, tx, session.SessionID, eventType, detail); err != nil {
			return BalancedOpenSession{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return BalancedOpenSession{}, err
	}
	return session, nil
}

func (s *BalancedOpenService) listSessionsByStates(ctx context.Context, states []string) ([]BalancedOpenSession, error) {
	if len(states) == 0 {
		return []BalancedOpenSession{}, nil
	}

	rows, err := s.db.Query(ctx, `
select
  id,
  session_id,
  role,
  peer_pubkey,
  peer_host,
  capacity_sat,
  fee_rate_sat_vb,
  is_private,
  close_address,
  state,
  state_updated_at,
  attempts,
  next_retry_at,
  last_error,
  metadata,
  created_at,
  updated_at
from balanced_open_sessions
where state = any($1::text[])
order by updated_at asc
`, states)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]BalancedOpenSession, 0)
	for rows.Next() {
		session, err := scanBalancedOpenSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type balancedDualFundingInput struct {
	Side string
	TxID string
	Vout uint32
}

type balancedDualFundingPlan struct {
	Tx                  *wire.MsgTx
	TxHex               string
	FundingTxID         string
	FundingVout         uint32
	FundingScriptHex    string
	InitiatorInputIndex int
	AccepterInputIndex  int
}

type balancedOrphanRecoveryPlan struct {
	TxHex              string
	Tx                 *wire.MsgTx
	WitnessScript      []byte
	WitnessScriptHex   string
	FundingPkScriptHex string
	FundingOutputSat   int64
	InitiatorOutputSat int64
	AccepterOutputSat  int64
}

type balancedOnchainBudget struct {
	TotalSat              int64
	LockedSat             int64
	ReservedAnchorSat     int64
	EstimatedSpendableSat int64
}

func normalizeBalancedExecutionMode(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case balancedOpenExecutionModeDual:
		return balancedOpenExecutionModeDual
	case balancedOpenExecutionModePush:
		return balancedOpenExecutionModePush
	default:
		return ""
	}
}

func (s *BalancedOpenService) ensureBalancedOnchainBudget(ctx context.Context, spendingSat int64, minRemainingSat int64) (balancedOnchainBudget, error) {
	details, err := s.lnd.GetWalletBalanceDetails(ctx)
	if err != nil {
		return balancedOnchainBudget{}, err
	}

	budget := balancedOnchainBudget{
		TotalSat:              details.TotalSat,
		LockedSat:             details.LockedSat,
		ReservedAnchorSat:     details.ReservedAnchorSat,
		EstimatedSpendableSat: details.EstimatedSpendableSat,
	}

	remaining := budget.EstimatedSpendableSat - spendingSat
	if remaining < minRemainingSat {
		return budget, fmt.Errorf("%w: spendable=%d sats, spending=%d sats, remaining=%d sats, required_remaining=%d sats", ErrBalancedOpenInsufficientOnchainSafety, budget.EstimatedSpendableSat, spendingSat, remaining, minRemainingSat)
	}

	return budget, nil
}

func decodeBalancedOpenMetadata(raw json.RawMessage) (balancedOpenMetadata, error) {
	if len(raw) == 0 {
		return balancedOpenMetadata{}, nil
	}
	var meta balancedOpenMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return balancedOpenMetadata{}, err
	}
	meta.ExecutionMode = normalizeBalancedExecutionMode(meta.ExecutionMode)
	return meta, nil
}

func decodeBalancedOpenMetadataMap(raw json.RawMessage) (map[string]any, error) {
	out := map[string]any{}
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func balancedOpenMetadataString(meta map[string]any, key string) string {
	if len(meta) == 0 {
		return ""
	}
	value, ok := meta[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func balancedOpenRecoveryKeysForRole(role string) (txidKey string, addressKey string, unavailableKey string, ok bool) {
	switch strings.TrimSpace(role) {
	case balancedOpenRoleInitiator:
		return "initiator_recovery_txid", "initiator_recovery_address", "initiator_recovery_unavailable_outpoint", true
	case balancedOpenRoleAccepter:
		return "accepter_recovery_txid", "accepter_recovery_address", "accepter_recovery_unavailable_outpoint", true
	default:
		return "", "", "", false
	}
}

func balancedEncodeMetadata(meta balancedOpenMetadata) map[string]any {
	out := map[string]any{}
	if mode := normalizeBalancedExecutionMode(meta.ExecutionMode); mode != "" {
		out["execution_mode"] = mode
	}
	if id := strings.ToLower(strings.TrimSpace(meta.PendingChanID)); id != "" {
		out["pending_chan_id"] = id
	}
	if key := encodeBalancedOpenKey(meta.InitiatorMultisigKey); key != nil {
		out["initiator_multisig_key"] = key
	}
	if key := encodeBalancedOpenKey(meta.AccepterMultisigKey); key != nil {
		out["accepter_multisig_key"] = key
	}
	if transit := encodeBalancedTransit(meta.InitiatorTransit); transit != nil {
		out["initiator_transit"] = transit
	}
	if transit := encodeBalancedTransit(meta.AccepterTransit); transit != nil {
		out["accepter_transit"] = transit
	}
	if len(meta.AccepterInputWitness) > 0 {
		out["accepter_input_witness"] = append([]string(nil), meta.AccepterInputWitness...)
	}
	if txHex := strings.ToLower(strings.TrimSpace(meta.FundingTxHex)); txHex != "" {
		out["funding_tx_hex"] = txHex
	}
	if txid := strings.ToLower(strings.TrimSpace(meta.FundingTxID)); txid != "" {
		out["funding_tx_id"] = txid
		out["funding_tx_vout"] = meta.FundingTxVout
	}
	if point := strings.TrimSpace(meta.ChannelPoint); point != "" {
		out["channel_point"] = point
	}
	if meta.OrphanRecoveryFeeRate > 0 {
		out["orphan_recovery_fee_rate_sat_vb"] = meta.OrphanRecoveryFeeRate
	}
	if sig := strings.ToLower(strings.TrimSpace(meta.OrphanInitiatorSigHex)); sig != "" {
		out["orphan_initiator_sig"] = sig
	}
	if sig := strings.ToLower(strings.TrimSpace(meta.OrphanAccepterSigHex)); sig != "" {
		out["orphan_accepter_sig"] = sig
	}
	if txid := strings.ToLower(strings.TrimSpace(meta.OrphanRecoveryTxID)); txid != "" {
		out["orphan_recovery_txid"] = txid
	}
	if txid := strings.ToLower(strings.TrimSpace(meta.OrphanLocalSweepTxID)); txid != "" {
		out["orphan_local_sweep_txid"] = txid
	}
	if addr := strings.TrimSpace(meta.OrphanLocalSweepAddr); addr != "" {
		out["orphan_local_sweep_address"] = addr
	}
	if errText := strings.TrimSpace(meta.LastExecutionErr); errText != "" {
		out["last_execution_err"] = errText
	}
	if meta.LastExecutionErrorUnix > 0 {
		out["last_execution_error_unix"] = meta.LastExecutionErrorUnix
	}
	return out
}

func encodeBalancedOpenKey(key balancedOpenKeyDescriptor) map[string]any {
	pub := strings.ToLower(strings.TrimSpace(key.PublicKey))
	if !isValidPubkeyHex(pub) {
		return nil
	}
	return map[string]any{
		"public_key": pub,
		"family":     key.Family,
		"index":      key.Index,
	}
}

func encodeBalancedTransit(transit balancedOpenTransitDetails) map[string]any {
	txid := strings.ToLower(strings.TrimSpace(transit.TxID))
	if !isBalancedOpenTxID(txid) {
		return nil
	}
	if transit.OutputSat <= 0 {
		return nil
	}
	script := strings.ToLower(strings.TrimSpace(transit.OutputScript))
	if !isBalancedOpenScriptHex(script) {
		return nil
	}
	out := map[string]any{
		"tx_id":         txid,
		"vout":          transit.Vout,
		"output_sat":    transit.OutputSat,
		"output_script": script,
	}
	if key := encodeBalancedOpenKey(transit.Key); key != nil {
		out["key"] = key
	}
	return out
}

func validateBalancedDualExecuteArtifacts(session BalancedOpenSession, meta balancedOpenMetadata) error {
	if normalizeBalancedExecutionMode(meta.ExecutionMode) != balancedOpenExecutionModeDual {
		return ErrBalancedOpenInvalidSession
	}
	if !isBalancedPendingChanIDHex(meta.PendingChanID) {
		return errors.New("missing pending channel id")
	}
	if !isValidPubkeyHex(meta.InitiatorMultisigKey.PublicKey) || !isValidPubkeyHex(meta.AccepterMultisigKey.PublicKey) {
		return errors.New("missing multisig pubkeys")
	}
	if !hasBalancedTransit(meta.InitiatorTransit, true) || !hasBalancedTransit(meta.AccepterTransit, false) {
		return errors.New("missing transit funding details")
	}
	if len(meta.AccepterInputWitness) == 0 {
		return errors.New("missing accepter funding witness")
	}
	if err := validateBalancedCapacityByMode(session.CapacitySat, balancedOpenExecutionModeDual); err != nil {
		return err
	}
	return nil
}

func hasBalancedTransit(transit balancedOpenTransitDetails, requireKey bool) bool {
	if !isBalancedOpenTxID(transit.TxID) {
		return false
	}
	if transit.OutputSat <= 0 {
		return false
	}
	if !isBalancedOpenScriptHex(transit.OutputScript) {
		return false
	}
	if requireKey && !isValidPubkeyHex(transit.Key.PublicKey) {
		return false
	}
	return true
}

func balancedOpenLocalTransitForRole(role string, meta balancedOpenMetadata) (balancedOpenTransitDetails, bool) {
	switch strings.TrimSpace(role) {
	case balancedOpenRoleInitiator:
		if !hasBalancedTransit(meta.InitiatorTransit, true) {
			return balancedOpenTransitDetails{}, false
		}
		return meta.InitiatorTransit, true
	case balancedOpenRoleAccepter:
		if !hasBalancedTransit(meta.AccepterTransit, true) {
			return balancedOpenTransitDetails{}, false
		}
		return meta.AccepterTransit, true
	default:
		return balancedOpenTransitDetails{}, false
	}
}

func (s *BalancedOpenService) ensureInitiatorDualArtifacts(ctx context.Context, session BalancedOpenSession, meta balancedOpenMetadata) (balancedOpenMetadata, bool, error) {
	if normalizeBalancedExecutionMode(meta.ExecutionMode) == balancedOpenExecutionModeDual &&
		isValidPubkeyHex(meta.InitiatorMultisigKey.PublicKey) &&
		hasBalancedTransit(meta.InitiatorTransit, true) {
		return meta, false, nil
	}

	feeRate := balancedEffectiveFeeRate(session.FeeRateSatVb)
	transitAmount, err := balancedTransitContribution(session.CapacitySat, feeRate)
	if err != nil {
		return meta, false, err
	}
	if _, err := s.ensureBalancedOnchainBudget(ctx, transitAmount, balancedOpenAnchorSafetySat); err != nil {
		return meta, false, err
	}

	multisigKey, err := s.lnd.DeriveNextKey(ctx, balancedOpenMultiSigKeyFamily)
	if err != nil {
		return meta, false, err
	}
	transitKey, err := s.lnd.DeriveNextKey(ctx, balancedOpenTransitKeyFamily)
	if err != nil {
		return meta, false, err
	}
	transitScript, err := balancedP2WPKHScriptHex(transitKey.PublicKey)
	if err != nil {
		return meta, false, err
	}

	sendResult, err := s.lnd.SendOutputScript(ctx, lndclient.SendOutputScriptParams{
		SatPerVbyte:      feeRate,
		OutputScriptHex:  transitScript,
		AmountSat:        transitAmount,
		Label:            fmt.Sprintf("balanced-open-%s-initiator", session.SessionID),
		MinConfs:         0,
		SpendUnconfirmed: true,
	})
	if err != nil {
		return meta, false, err
	}

	meta.ExecutionMode = balancedOpenExecutionModeDual
	meta.InitiatorMultisigKey = balancedOpenKeyDescriptor{
		PublicKey: strings.ToLower(strings.TrimSpace(multisigKey.PublicKey)),
		Family:    multisigKey.Family,
		Index:     multisigKey.Index,
	}
	meta.InitiatorTransit = balancedOpenTransitDetails{
		TxID:         strings.ToLower(strings.TrimSpace(sendResult.TxID)),
		Vout:         sendResult.Vout,
		OutputSat:    transitAmount,
		OutputScript: strings.ToLower(strings.TrimSpace(transitScript)),
		Key: balancedOpenKeyDescriptor{
			PublicKey: strings.ToLower(strings.TrimSpace(transitKey.PublicKey)),
			Family:    transitKey.Family,
			Index:     transitKey.Index,
		},
	}

	return meta, true, nil
}

func (s *BalancedOpenService) ensureAccepterDualArtifacts(ctx context.Context, session BalancedOpenSession, meta balancedOpenMetadata) (balancedOpenMetadata, []string, bool, error) {
	if normalizeBalancedExecutionMode(meta.ExecutionMode) == balancedOpenExecutionModeDual &&
		isValidPubkeyHex(meta.AccepterMultisigKey.PublicKey) &&
		hasBalancedTransit(meta.AccepterTransit, true) &&
		len(meta.AccepterInputWitness) > 0 {
		return meta, append([]string(nil), meta.AccepterInputWitness...), false, nil
	}

	if !isValidPubkeyHex(meta.InitiatorMultisigKey.PublicKey) || !hasBalancedTransit(meta.InitiatorTransit, false) {
		return meta, nil, false, errors.New("proposal missing initiator artifacts")
	}

	created := false
	if !isValidPubkeyHex(meta.AccepterMultisigKey.PublicKey) || !hasBalancedTransit(meta.AccepterTransit, true) {
		feeRate := balancedEffectiveFeeRate(session.FeeRateSatVb)
		transitAmount, err := balancedTransitContribution(session.CapacitySat, feeRate)
		if err != nil {
			return meta, nil, false, err
		}
		if _, err := s.ensureBalancedOnchainBudget(ctx, transitAmount, balancedOpenAnchorSafetySat); err != nil {
			return meta, nil, false, err
		}

		multisigKey, err := s.lnd.DeriveNextKey(ctx, balancedOpenMultiSigKeyFamily)
		if err != nil {
			return meta, nil, false, err
		}
		transitKey, err := s.lnd.DeriveNextKey(ctx, balancedOpenTransitKeyFamily)
		if err != nil {
			return meta, nil, false, err
		}
		transitScript, err := balancedP2WPKHScriptHex(transitKey.PublicKey)
		if err != nil {
			return meta, nil, false, err
		}

		sendResult, err := s.lnd.SendOutputScript(ctx, lndclient.SendOutputScriptParams{
			SatPerVbyte:      feeRate,
			OutputScriptHex:  transitScript,
			AmountSat:        transitAmount,
			Label:            fmt.Sprintf("balanced-open-%s-accepter", session.SessionID),
			MinConfs:         0,
			SpendUnconfirmed: true,
		})
		if err != nil {
			return meta, nil, false, err
		}

		meta.ExecutionMode = balancedOpenExecutionModeDual
		meta.AccepterMultisigKey = balancedOpenKeyDescriptor{
			PublicKey: strings.ToLower(strings.TrimSpace(multisigKey.PublicKey)),
			Family:    multisigKey.Family,
			Index:     multisigKey.Index,
		}
		meta.AccepterTransit = balancedOpenTransitDetails{
			TxID:         strings.ToLower(strings.TrimSpace(sendResult.TxID)),
			Vout:         sendResult.Vout,
			OutputSat:    transitAmount,
			OutputScript: strings.ToLower(strings.TrimSpace(transitScript)),
			Key: balancedOpenKeyDescriptor{
				PublicKey: strings.ToLower(strings.TrimSpace(transitKey.PublicKey)),
				Family:    transitKey.Family,
				Index:     transitKey.Index,
			},
		}
		created = true
	}

	derivedPendingID, err := balancedPendingChanIDHexFromMultisig(meta.InitiatorMultisigKey.PublicKey, meta.AccepterMultisigKey.PublicKey)
	if err != nil {
		return meta, nil, created, err
	}
	if !strings.EqualFold(strings.TrimSpace(meta.PendingChanID), strings.TrimSpace(derivedPendingID)) {
		meta.PendingChanID = derivedPendingID
		created = true
	}

	meta.ExecutionMode = balancedOpenExecutionModeDual

	plan, err := buildBalancedDualFundingPlan(session, meta)
	if err != nil {
		return meta, nil, created, err
	}
	meta.FundingTxID = plan.FundingTxID
	meta.FundingTxVout = plan.FundingVout
	meta.FundingTxHex = plan.TxHex

	localInputScript, err := s.lnd.ComputeInputScript(ctx, lndclient.ComputeInputScriptParams{
		RawTxHex:        plan.TxHex,
		InputIndex:      uint32(plan.AccepterInputIndex),
		OutputScriptHex: meta.AccepterTransit.OutputScript,
		OutputSat:       meta.AccepterTransit.OutputSat,
		Key: lndclient.DerivedKey{
			PublicKey: meta.AccepterTransit.Key.PublicKey,
			Family:    meta.AccepterTransit.Key.Family,
			Index:     meta.AccepterTransit.Key.Index,
		},
	})
	if err != nil {
		return meta, nil, created, err
	}

	localWitness := balancedEncodeWitnessStack(localInputScript.Witness)
	if len(localWitness) == 0 {
		return meta, nil, created, errors.New("empty accepter funding witness")
	}
	meta.AccepterInputWitness = append([]string(nil), localWitness...)

	pendingID, err := hex.DecodeString(meta.PendingChanID)
	if err != nil {
		return meta, nil, created, errors.New("invalid pending channel id")
	}

	err = s.lnd.RegisterChanPointShim(ctx, lndclient.ChanPointShimParams{
		CapacitySat:   session.CapacitySat,
		PendingChanID: pendingID,
		FundingTxID:   plan.FundingTxID,
		FundingVout:   plan.FundingVout,
		LocalKey: lndclient.DerivedKey{
			PublicKey: meta.AccepterMultisigKey.PublicKey,
			Family:    meta.AccepterMultisigKey.Family,
			Index:     meta.AccepterMultisigKey.Index,
		},
		RemoteKeyHex: meta.InitiatorMultisigKey.PublicKey,
	})
	if err != nil && !isBalancedOpenAlreadyRegisteredErr(err) {
		return meta, nil, created, err
	}

	return meta, localWitness, created, nil
}

func buildBalancedDualFundingPlan(session BalancedOpenSession, meta balancedOpenMetadata) (balancedDualFundingPlan, error) {
	witnessScript, err := balancedFundingWitnessScript(meta.InitiatorMultisigKey.PublicKey, meta.AccepterMultisigKey.PublicKey)
	if err != nil {
		return balancedDualFundingPlan{}, err
	}
	fundingPkScript := balancedFundingOutputScript(witnessScript)

	inputs := []balancedDualFundingInput{
		{
			Side: balancedOpenRoleInitiator,
			TxID: strings.ToLower(strings.TrimSpace(meta.InitiatorTransit.TxID)),
			Vout: meta.InitiatorTransit.Vout,
		},
		{
			Side: balancedOpenRoleAccepter,
			TxID: strings.ToLower(strings.TrimSpace(meta.AccepterTransit.TxID)),
			Vout: meta.AccepterTransit.Vout,
		},
	}
	for _, input := range inputs {
		if !isBalancedOpenTxID(input.TxID) {
			return balancedDualFundingPlan{}, errors.New("invalid transit tx id")
		}
	}

	sort.SliceStable(inputs, func(i, j int) bool {
		a := balancedTxidSortKey(inputs[i].TxID)
		b := balancedTxidSortKey(inputs[j].TxID)
		if cmp := bytes.Compare(a, b); cmp != 0 {
			return cmp < 0
		}
		return inputs[i].Vout < inputs[j].Vout
	})

	tx := wire.NewMsgTx(2)
	initiatorIndex := -1
	accepterIndex := -1

	for idx, input := range inputs {
		hash, err := chainhash.NewHashFromStr(input.TxID)
		if err != nil {
			return balancedDualFundingPlan{}, err
		}
		txin := wire.NewTxIn(wire.NewOutPoint(hash, input.Vout), nil, nil)
		txin.Sequence = 0
		tx.AddTxIn(txin)
		if input.Side == balancedOpenRoleInitiator {
			initiatorIndex = idx
		} else {
			accepterIndex = idx
		}
	}

	tx.AddTxOut(wire.NewTxOut(session.CapacitySat, fundingPkScript))
	if initiatorIndex < 0 || accepterIndex < 0 {
		return balancedDualFundingPlan{}, errors.New("failed to map funding inputs")
	}

	txHex, err := encodeBalancedTxHex(tx)
	if err != nil {
		return balancedDualFundingPlan{}, err
	}

	return balancedDualFundingPlan{
		Tx:                  tx,
		TxHex:               txHex,
		FundingTxID:         tx.TxHash().String(),
		FundingVout:         0,
		FundingScriptHex:    hex.EncodeToString(fundingPkScript),
		InitiatorInputIndex: initiatorIndex,
		AccepterInputIndex:  accepterIndex,
	}, nil
}

func balancedP2WPKHScriptHex(pubkeyHex string) (string, error) {
	pubkey, err := hex.DecodeString(strings.TrimSpace(pubkeyHex))
	if err != nil || len(pubkey) != 33 {
		return "", errors.New("invalid transit pubkey")
	}
	sum := sha256.Sum256(pubkey)
	r := ripemd160.New()
	_, _ = r.Write(sum[:])
	hash160 := r.Sum(nil)

	script := make([]byte, 0, 22)
	script = append(script, txscript.OP_0, 0x14)
	script = append(script, hash160...)

	return hex.EncodeToString(script), nil
}

func balancedFundingWitnessScript(keyA string, keyB string) ([]byte, error) {
	pubA, err := hex.DecodeString(strings.TrimSpace(keyA))
	if err != nil || len(pubA) != 33 {
		return nil, errors.New("invalid local multisig key")
	}
	pubB, err := hex.DecodeString(strings.TrimSpace(keyB))
	if err != nil || len(pubB) != 33 {
		return nil, errors.New("invalid remote multisig key")
	}

	pubkeys := [][]byte{pubA, pubB}
	sort.SliceStable(pubkeys, func(i, j int) bool {
		return bytes.Compare(pubkeys[i], pubkeys[j]) < 0
	})

	builder := txscript.NewScriptBuilder()
	builder.AddOp(txscript.OP_2)
	builder.AddData(pubkeys[0])
	builder.AddData(pubkeys[1])
	builder.AddOp(txscript.OP_2)
	builder.AddOp(txscript.OP_CHECKMULTISIG)
	return builder.Script()
}

func balancedFundingOutputScript(witnessScript []byte) []byte {
	sum := sha256.Sum256(witnessScript)
	script := make([]byte, 0, 34)
	script = append(script, txscript.OP_0, 0x20)
	script = append(script, sum[:]...)
	return script
}

func balancedTxidSortKey(txid string) []byte {
	raw, err := hex.DecodeString(strings.TrimSpace(txid))
	if err != nil || len(raw) != 32 {
		return nil
	}
	for i := 0; i < len(raw)/2; i++ {
		raw[i], raw[len(raw)-1-i] = raw[len(raw)-1-i], raw[i]
	}
	return raw
}

func encodeBalancedTxHex(tx *wire.MsgTx) (string, error) {
	if tx == nil {
		return "", errors.New("missing transaction")
	}
	var b bytes.Buffer
	if err := tx.Serialize(&b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b.Bytes()), nil
}

func balancedDecodeWitnessStack(values []string) (wire.TxWitness, error) {
	if len(values) == 0 {
		return nil, errors.New("missing witness stack")
	}
	stack := make(wire.TxWitness, 0, len(values))
	for _, item := range values {
		raw, err := hex.DecodeString(strings.TrimSpace(item))
		if err != nil {
			return nil, errors.New("invalid witness item")
		}
		stack = append(stack, raw)
	}
	return stack, nil
}

func balancedEncodeWitnessStack(witness [][]byte) []string {
	if len(witness) == 0 {
		return nil
	}
	out := make([]string, 0, len(witness))
	for _, item := range witness {
		out = append(out, hex.EncodeToString(item))
	}
	return out
}

func cloneBalancedWitness(src wire.TxWitness) wire.TxWitness {
	if len(src) == 0 {
		return nil
	}
	out := make(wire.TxWitness, 0, len(src))
	for _, item := range src {
		out = append(out, append([]byte(nil), item...))
	}
	return out
}

func balancedTxHexAppearsSigned(txHex string) bool {
	raw, err := hex.DecodeString(strings.TrimSpace(txHex))
	if err != nil || len(raw) == 0 {
		return false
	}
	var tx wire.MsgTx
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
		return false
	}
	if len(tx.TxIn) == 0 {
		return false
	}
	for _, input := range tx.TxIn {
		if input == nil {
			return false
		}
		if len(input.Witness) == 0 && len(input.SignatureScript) == 0 {
			return false
		}
	}
	return true
}

func balancedEffectiveFeeRate(value int64) int64 {
	if value <= 0 {
		return 1
	}
	return value
}

func balancedTransitContribution(capacitySat int64, feeRateSatVb int64) (int64, error) {
	if err := validateBalancedCapacityByMode(capacitySat, balancedOpenExecutionModeDual); err != nil {
		return 0, err
	}
	if feeRateSatVb <= 0 {
		return 0, ErrBalancedOpenInvalidFeeRate
	}
	return (capacitySat + (balancedOpenFundingVBytes * feeRateSatVb)) / 2, nil
}

func (s *BalancedOpenService) publishBalancedTransitRecovery(ctx context.Context, session BalancedOpenSession, transit balancedOpenTransitDetails, satPerVbyte int64) (string, string, error) {
	if !hasBalancedTransit(transit, true) {
		return "", "", errors.New("missing transit recovery details")
	}
	feeRate := satPerVbyte
	if feeRate <= 0 {
		feeRate = 1
	}
	feeSat := feeRate * balancedOpenRecoverySweepVBytes
	outputSat := transit.OutputSat - feeSat
	if outputSat < balancedOpenRecoveryMinOutput {
		return "", "", fmt.Errorf("transit output too small to recover after fee: output=%d fee=%d", transit.OutputSat, feeSat)
	}

	recoveryAddress, err := s.lnd.NewAddress(ctx)
	if err != nil {
		return "", "", err
	}
	recoveryScript, err := balancedAddressToScript(recoveryAddress)
	if err != nil {
		return "", "", err
	}

	hash, err := chainhash.NewHashFromStr(strings.ToLower(strings.TrimSpace(transit.TxID)))
	if err != nil {
		return "", "", err
	}
	tx := wire.NewMsgTx(2)
	txin := wire.NewTxIn(wire.NewOutPoint(hash, transit.Vout), nil, nil)
	txin.Sequence = 0
	tx.AddTxIn(txin)
	tx.AddTxOut(wire.NewTxOut(outputSat, recoveryScript))

	unsignedTxHex, err := encodeBalancedTxHex(tx)
	if err != nil {
		return "", "", err
	}
	inputScript, err := s.lnd.ComputeInputScript(ctx, lndclient.ComputeInputScriptParams{
		RawTxHex:        unsignedTxHex,
		InputIndex:      0,
		OutputScriptHex: transit.OutputScript,
		OutputSat:       transit.OutputSat,
		Key: lndclient.DerivedKey{
			PublicKey: transit.Key.PublicKey,
			Family:    transit.Key.Family,
			Index:     transit.Key.Index,
		},
	})
	if err != nil {
		return "", "", err
	}
	tx.TxIn[0].Witness = cloneBalancedWitness(inputScript.Witness)
	tx.TxIn[0].SignatureScript = append([]byte(nil), inputScript.SigScript...)

	finalTxHex, err := encodeBalancedTxHex(tx)
	if err != nil {
		return "", "", err
	}
	publishErr := s.lnd.PublishTransaction(ctx, finalTxHex, fmt.Sprintf("balanced-open-recover-%s", session.SessionID))
	if publishErr != nil && !isBalancedOpenAlreadyPublishedError(publishErr) {
		return "", "", publishErr
	}

	return tx.TxHash().String(), strings.TrimSpace(recoveryAddress), nil
}

func balancedAddressToScript(address string) ([]byte, error) {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return nil, errors.New("recovery address required")
	}

	networks := []*chaincfg.Params{
		&chaincfg.MainNetParams,
		&chaincfg.TestNet3Params,
		&chaincfg.RegressionNetParams,
		&chaincfg.SigNetParams,
	}
	for _, net := range networks {
		decoded, err := btcutil.DecodeAddress(trimmed, net)
		if err != nil || decoded == nil || !decoded.IsForNet(net) {
			continue
		}
		script, err := txscript.PayToAddrScript(decoded)
		if err != nil {
			return nil, err
		}
		return script, nil
	}
	return nil, errors.New("failed to decode recovery address script")
}

func isBalancedOpenAlreadyPublishedError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(value, "already have transaction") ||
		strings.Contains(value, "transaction already in block chain") ||
		strings.Contains(value, "already exists")
}

func (s *BalancedOpenService) publishBalancedTxWithRetry(ctx context.Context, txHex string, label string, maxAttempts int) error {
	attempts := maxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		err := s.lnd.PublishTransaction(ctx, txHex, label)
		if err == nil || isBalancedOpenAlreadyPublishedError(err) {
			return nil
		}
		lastErr = err
		if attempt >= attempts {
			break
		}
		delay := balancedOpenPublishRetryDelay(attempt)
		if delay <= 0 {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return err
		case <-timer.C:
		}
	}
	return lastErr
}

func balancedOpenPublishRetryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 2 * time.Second
	case 2:
		return 5 * time.Second
	default:
		return 8 * time.Second
	}
}

func balancedOpenTxSeenOnExternalMempool(ctx context.Context, txid string) (seen bool, checked bool) {
	clean := strings.ToLower(strings.TrimSpace(txid))
	if !isBalancedOpenTxID(clean) {
		return false, false
	}
	endpoints := []string{
		"https://mempool.space/api/tx/" + clean,
		"https://blockstream.info/api/tx/" + clean,
	}
	client := &http.Client{Timeout: 5 * time.Second}
	observedCheck := false

	for _, endpoint := range endpoints {
		checkCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		if ctx != nil {
			if deadline, ok := ctx.Deadline(); ok {
				checkCtx, cancel = context.WithDeadline(context.Background(), deadline)
			}
		}
		req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, endpoint, nil)
		if err != nil {
			cancel()
			continue
		}
		resp, err := client.Do(req)
		cancel()
		if err != nil {
			continue
		}
		observedCheck = true
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return true, true
		}
		if resp.StatusCode == http.StatusNotFound {
			return false, true
		}
	}
	if observedCheck {
		return false, true
	}
	return false, false
}

func isBalancedOpenOutpointUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(value, "unknown utxo") ||
		strings.Contains(value, "missing inputs") ||
		strings.Contains(value, "missingorspent") ||
		strings.Contains(value, "already spent") ||
		strings.Contains(value, "txn-mempool-conflict")
}

func validateBalancedCapacityByMode(capacitySat int64, mode string) error {
	_ = mode
	if capacitySat < balancedOpenMinCapacitySat || capacitySat%2 != 0 {
		return ErrBalancedOpenInvalidCapacity
	}
	return nil
}

func balancedOpenCanonicalChannelPoint(txid string, vout uint32) string {
	cleanTxid := strings.ToLower(strings.TrimSpace(txid))
	if !isBalancedOpenTxID(cleanTxid) {
		return ""
	}
	return fmt.Sprintf("%s:%d", cleanTxid, vout)
}

func balancedOpenCanonicalChannelPointFromString(value string) string {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 {
		return ""
	}
	idx, err := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 32)
	if err != nil {
		return ""
	}
	return balancedOpenCanonicalChannelPoint(parts[0], uint32(idx))
}

func balancedOpenHasOrphanFundingCandidate(session BalancedOpenSession, meta balancedOpenMetadata) bool {
	if normalizeBalancedExecutionMode(meta.ExecutionMode) != balancedOpenExecutionModeDual {
		return false
	}
	expected := balancedOpenCanonicalChannelPoint(meta.FundingTxID, meta.FundingTxVout)
	if expected == "" {
		return false
	}
	actual := balancedOpenCanonicalChannelPointFromString(balancedOpenSessionChannelPoint(session.Metadata))
	if actual == "" {
		return false
	}
	return expected != actual
}

func balancedOpenCanProcessOrphanRecovery(meta balancedOpenMetadata) bool {
	if normalizeBalancedExecutionMode(meta.ExecutionMode) != balancedOpenExecutionModeDual {
		return false
	}
	if !isBalancedOpenTxID(meta.FundingTxID) {
		return false
	}
	if !isValidPubkeyHex(meta.InitiatorMultisigKey.PublicKey) || !isValidPubkeyHex(meta.AccepterMultisigKey.PublicKey) {
		return false
	}
	if !hasBalancedTransit(meta.InitiatorTransit, false) || !hasBalancedTransit(meta.AccepterTransit, false) {
		return false
	}
	return true
}

func balancedOpenCounterpartyRole(role string) string {
	switch strings.TrimSpace(role) {
	case balancedOpenRoleInitiator:
		return balancedOpenRoleAccepter
	case balancedOpenRoleAccepter:
		return balancedOpenRoleInitiator
	default:
		return ""
	}
}

func balancedOpenOrphanProgressState(current string) string {
	switch strings.TrimSpace(current) {
	case balancedOpenStateCanceled, balancedOpenStateRecovered:
		return balancedOpenStateRecovered
	default:
		return strings.TrimSpace(current)
	}
}

func balancedOpenOrphanFinalState(current string) string {
	switch strings.TrimSpace(current) {
	case balancedOpenStateActive:
		return balancedOpenStateActive
	case balancedOpenStateRecovered:
		return balancedOpenStateRecovered
	default:
		return balancedOpenStateRecovered
	}
}

func balancedOpenOrphanSigForRole(meta balancedOpenMetadata, role string) string {
	switch strings.TrimSpace(role) {
	case balancedOpenRoleInitiator:
		return strings.ToLower(strings.TrimSpace(meta.OrphanInitiatorSigHex))
	case balancedOpenRoleAccepter:
		return strings.ToLower(strings.TrimSpace(meta.OrphanAccepterSigHex))
	default:
		return ""
	}
}

func balancedOpenSetOrphanSigForRole(meta balancedOpenMetadata, role string, sigHex string) balancedOpenMetadata {
	switch strings.TrimSpace(role) {
	case balancedOpenRoleInitiator:
		meta.OrphanInitiatorSigHex = strings.ToLower(strings.TrimSpace(sigHex))
	case balancedOpenRoleAccepter:
		meta.OrphanAccepterSigHex = strings.ToLower(strings.TrimSpace(sigHex))
	}
	return meta
}

func balancedOpenLocalMultisigKeyForRole(role string, meta balancedOpenMetadata) (balancedOpenKeyDescriptor, bool) {
	switch strings.TrimSpace(role) {
	case balancedOpenRoleInitiator:
		if !isValidPubkeyHex(meta.InitiatorMultisigKey.PublicKey) {
			return balancedOpenKeyDescriptor{}, false
		}
		return meta.InitiatorMultisigKey, true
	case balancedOpenRoleAccepter:
		if !isValidPubkeyHex(meta.AccepterMultisigKey.PublicKey) {
			return balancedOpenKeyDescriptor{}, false
		}
		return meta.AccepterMultisigKey, true
	default:
		return balancedOpenKeyDescriptor{}, false
	}
}

func isBalancedOpenSigHex(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	raw, err := hex.DecodeString(trimmed)
	if err != nil {
		return false
	}
	if len(raw) < 9 || len(raw) > 90 {
		return false
	}
	return raw[0] == 0x30
}

func balancedExtractSigHex(witness wire.TxWitness) (string, error) {
	for _, item := range witness {
		if len(item) < 9 || len(item) > 90 {
			continue
		}
		if item[0] != 0x30 {
			continue
		}
		return hex.EncodeToString(item), nil
	}
	return "", errors.New("failed to extract orphan recovery signature")
}

func buildBalancedOrphanRecoveryPlan(session BalancedOpenSession, meta balancedOpenMetadata, feeRateSatVb int64) (balancedOrphanRecoveryPlan, error) {
	if !isBalancedOpenTxID(meta.FundingTxID) {
		return balancedOrphanRecoveryPlan{}, errors.New("missing funding tx id for orphan recovery")
	}
	if feeRateSatVb <= 0 {
		feeRateSatVb = 1
	}

	witnessScript, err := balancedFundingWitnessScript(meta.InitiatorMultisigKey.PublicKey, meta.AccepterMultisigKey.PublicKey)
	if err != nil {
		return balancedOrphanRecoveryPlan{}, err
	}
	fundingScript := balancedFundingOutputScript(witnessScript)
	fundingSat := session.CapacitySat

	fundingTxHex := strings.TrimSpace(meta.FundingTxHex)
	if fundingTxHex != "" {
		raw, err := hex.DecodeString(fundingTxHex)
		if err == nil {
			var fundingTx wire.MsgTx
			if fundingTx.Deserialize(bytes.NewReader(raw)) == nil {
				if int(meta.FundingTxVout) < len(fundingTx.TxOut) && fundingTx.TxOut[meta.FundingTxVout] != nil {
					fundingSat = fundingTx.TxOut[meta.FundingTxVout].Value
				}
			}
		}
	}
	if fundingSat <= 0 {
		return balancedOrphanRecoveryPlan{}, errors.New("invalid orphan funding amount")
	}

	feeSat := feeRateSatVb * balancedOpenOrphanRecoveryVBytes
	if feeSat <= 0 {
		feeSat = 1
	}
	remaining := fundingSat - feeSat
	if remaining <= 0 {
		return balancedOrphanRecoveryPlan{}, errors.New("orphan funding output too small after fee")
	}

	initScriptRaw, err := hex.DecodeString(strings.TrimSpace(meta.InitiatorTransit.OutputScript))
	if err != nil || len(initScriptRaw) == 0 {
		return balancedOrphanRecoveryPlan{}, errors.New("missing initiator recovery script")
	}
	accScriptRaw, err := hex.DecodeString(strings.TrimSpace(meta.AccepterTransit.OutputScript))
	if err != nil || len(accScriptRaw) == 0 {
		return balancedOrphanRecoveryPlan{}, errors.New("missing accepter recovery script")
	}

	initOut := (remaining / 2) + (remaining % 2)
	accOut := remaining / 2
	if initOut < balancedOpenRecoveryMinOutput || accOut < balancedOpenRecoveryMinOutput {
		return balancedOrphanRecoveryPlan{}, errors.New("orphan recovery outputs below dust threshold")
	}

	hash, err := chainhash.NewHashFromStr(strings.ToLower(strings.TrimSpace(meta.FundingTxID)))
	if err != nil {
		return balancedOrphanRecoveryPlan{}, err
	}
	tx := wire.NewMsgTx(2)
	txin := wire.NewTxIn(wire.NewOutPoint(hash, meta.FundingTxVout), nil, nil)
	txin.Sequence = 0
	tx.AddTxIn(txin)
	tx.AddTxOut(wire.NewTxOut(initOut, initScriptRaw))
	tx.AddTxOut(wire.NewTxOut(accOut, accScriptRaw))

	txHex, err := encodeBalancedTxHex(tx)
	if err != nil {
		return balancedOrphanRecoveryPlan{}, err
	}

	return balancedOrphanRecoveryPlan{
		TxHex:              txHex,
		Tx:                 tx,
		WitnessScript:      witnessScript,
		WitnessScriptHex:   hex.EncodeToString(witnessScript),
		FundingPkScriptHex: hex.EncodeToString(fundingScript),
		FundingOutputSat:   fundingSat,
		InitiatorOutputSat: initOut,
		AccepterOutputSat:  accOut,
	}, nil
}

func balancedBuildOrphanRecoveryWitness(meta balancedOpenMetadata, witnessScript []byte, initiatorSigHex string, accepterSigHex string) (wire.TxWitness, error) {
	initSig, err := hex.DecodeString(strings.TrimSpace(initiatorSigHex))
	if err != nil || len(initSig) == 0 {
		return nil, errors.New("invalid initiator orphan signature")
	}
	accSig, err := hex.DecodeString(strings.TrimSpace(accepterSigHex))
	if err != nil || len(accSig) == 0 {
		return nil, errors.New("invalid accepter orphan signature")
	}
	initKey, err := hex.DecodeString(strings.TrimSpace(meta.InitiatorMultisigKey.PublicKey))
	if err != nil || len(initKey) != 33 {
		return nil, errors.New("invalid initiator multisig key")
	}
	accKey, err := hex.DecodeString(strings.TrimSpace(meta.AccepterMultisigKey.PublicKey))
	if err != nil || len(accKey) != 33 {
		return nil, errors.New("invalid accepter multisig key")
	}

	firstSig := initSig
	secondSig := accSig
	if bytes.Compare(initKey, accKey) > 0 {
		firstSig = accSig
		secondSig = initSig
	}

	return wire.TxWitness{
		[]byte{},
		firstSig,
		secondSig,
		witnessScript,
	}, nil
}

func balancedOpenOrphanOutputForRole(role string, plan balancedOrphanRecoveryPlan) (uint32, int64, bool) {
	switch strings.TrimSpace(role) {
	case balancedOpenRoleInitiator:
		return 0, plan.InitiatorOutputSat, true
	case balancedOpenRoleAccepter:
		return 1, plan.AccepterOutputSat, true
	default:
		return 0, 0, false
	}
}

func balancedPendingChanIDHexFromMultisig(localKey string, remoteKey string) (string, error) {
	witnessScript, err := balancedFundingWitnessScript(localKey, remoteKey)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(witnessScript)

	return hex.EncodeToString(hash[:]), nil
}

func isBalancedOpenTxID(txid string) bool {
	value := strings.TrimSpace(txid)
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func isBalancedOpenScriptHex(scriptHex string) bool {
	value := strings.TrimSpace(scriptHex)
	if value == "" {
		return false
	}
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) > 0
}

func isBalancedPendingChanIDHex(value string) bool {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) != 64 {
		return false
	}
	_, err := hex.DecodeString(trimmed)
	return err == nil
}

func newBalancedOpenPendingChanIDHex() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func isBalancedOpenAlreadyRegisteredErr(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	if text == "" {
		return false
	}
	return strings.Contains(text, "already registered") ||
		strings.Contains(text, "already exists")
}

func balancedOpenSessionChannelPoint(metadata json.RawMessage) string {
	if len(metadata) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return ""
	}
	raw, ok := payload["channel_point"]
	if !ok {
		return ""
	}
	value, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func matchBalancedOpenPendingChannel(session BalancedOpenSession, channelPointHint string, pending []lndclient.PendingChannelInfo) (lndclient.PendingChannelInfo, bool) {
	wantedPeer := strings.ToLower(strings.TrimSpace(session.PeerPubkey))
	wantedPoint := strings.TrimSpace(channelPointHint)

	for _, ch := range pending {
		if !strings.EqualFold(strings.TrimSpace(ch.Status), "opening") {
			continue
		}
		if wantedPoint != "" {
			if strings.EqualFold(strings.TrimSpace(ch.ChannelPoint), wantedPoint) {
				return ch, true
			}
			continue
		}
		if wantedPeer != "" && strings.EqualFold(strings.TrimSpace(ch.RemotePubkey), wantedPeer) && ch.CapacitySat == session.CapacitySat {
			return ch, true
		}
	}

	return lndclient.PendingChannelInfo{}, false
}

func matchBalancedOpenActiveChannel(session BalancedOpenSession, channelPointHint string, active []lndclient.ChannelInfo) (lndclient.ChannelInfo, bool) {
	wantedPeer := strings.ToLower(strings.TrimSpace(session.PeerPubkey))
	wantedPoint := strings.TrimSpace(channelPointHint)

	for _, ch := range active {
		if wantedPoint != "" {
			if strings.EqualFold(strings.TrimSpace(ch.ChannelPoint), wantedPoint) {
				return ch, true
			}
			continue
		}
		if wantedPeer != "" && strings.EqualFold(strings.TrimSpace(ch.RemotePubkey), wantedPeer) && ch.CapacitySat == session.CapacitySat {
			return ch, true
		}
	}

	return lndclient.ChannelInfo{}, false
}

func (s *BalancedOpenService) notifyBalancedOpenStart(ctx context.Context, session BalancedOpenSession, meta balancedOpenMetadata, openerPubkey string) {
	if s == nil || s.notifier == nil {
		return
	}

	mode := normalizeBalancedExecutionMode(meta.ExecutionMode)
	if mode == "" {
		mode = balancedOpenExecutionModeDual
	}
	perSideSat := session.CapacitySat / 2
	localOnchainSat, peerOnchainSat := balancedOpenOnchainFundingByRole(session.Role, meta)
	if localOnchainSat <= 0 {
		localOnchainSat = perSideSat
	}
	if peerOnchainSat <= 0 {
		peerOnchainSat = perSideSat
	}

	normalizedOpener := strings.ToLower(strings.TrimSpace(openerPubkey))
	if !isValidPubkeyHex(normalizedOpener) {
		if self, err := s.lnd.SelfPubkey(ctx); err == nil && isValidPubkeyHex(self) {
			normalizedOpener = strings.ToLower(strings.TrimSpace(self))
		}
	}

	openerAlias := ""
	if strings.EqualFold(normalizedOpener, session.PeerPubkey) {
		openerAlias = strings.TrimSpace(s.notifier.lookupNodeAlias(session.PeerPubkey))
	} else {
		openerAlias = strings.TrimSpace(getNodeAlias(ctx, s.lnd))
	}
	if openerAlias == "" {
		openerAlias = shortPubKey(normalizedOpener)
	}
	if openerAlias == "" {
		openerAlias = "unknown"
	}

	peerAlias := strings.TrimSpace(s.notifier.lookupNodeAlias(session.PeerPubkey))
	baseMemo := fmt.Sprintf(
		"Balanced open start | opener %s | mode %s | target %d sats/side | local on-chain %d sats | peer on-chain %d sats | fee rate %d sat/vB",
		openerAlias,
		mode,
		perSideSat,
		localOnchainSat,
		peerOnchainSat,
		session.FeeRateSatVb,
	)
	memo := baseMemo
	if session.Role == balancedOpenRoleAccepter {
		memo = fmt.Sprintf(
			"\u26a0\ufe0f ACTION REQUIRED: open the Balanced Open Channels card in the app and choose Accept or Cancel this proposal | %s",
			baseMemo,
		)
	}

	evt := Notification{
		OccurredAt:   time.Now().UTC(),
		Type:         "channel",
		Action:       "opening",
		Direction:    "neutral",
		Status:       "BALANCED_START",
		AmountSat:    session.CapacitySat,
		PeerPubkey:   session.PeerPubkey,
		PeerAlias:    peerAlias,
		ChannelPoint: strings.TrimSpace(meta.ChannelPoint),
		Memo:         memo,
	}
	if evt.PeerAlias == "" && evt.PeerPubkey != "" {
		evt.PeerAlias = shortPubKey(evt.PeerPubkey)
	}

	eventKey := fmt.Sprintf("channel:balanced_open_start:%s:%s", strings.TrimSpace(session.SessionID), strings.TrimSpace(session.Role))
	nctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, _ = s.notifier.upsertNotification(nctx, eventKey, evt)
	cancel()
}

func balancedOpenOnchainFundingByRole(role string, meta balancedOpenMetadata) (int64, int64) {
	switch strings.TrimSpace(role) {
	case balancedOpenRoleInitiator:
		return meta.InitiatorTransit.OutputSat, meta.AccepterTransit.OutputSat
	case balancedOpenRoleAccepter:
		return meta.AccepterTransit.OutputSat, meta.InitiatorTransit.OutputSat
	default:
		return 0, 0
	}
}

func (s *BalancedOpenService) sendProtocolMessage(ctx context.Context, peerPubkey string, msg balancedOpenProtocolMessage) error {
	raw, err := marshalBalancedOpenJSON(msg)
	if err != nil {
		return err
	}
	return s.lnd.SendCustomMessage(ctx, peerPubkey, balancedOpenCustomMsgType, raw)
}

func (s *BalancedOpenService) connectPeerForBalancedOpen(ctx context.Context, pubkey string, host string) error {
	err := s.lnd.ConnectPeerWithTimeout(ctx, pubkey, host, false, 8)
	if err == nil {
		return nil
	}

	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(msg, "already connected") || strings.Contains(msg, "already have a connection") {
		return nil
	}
	return err
}

func (s *BalancedOpenService) appendEventTx(ctx context.Context, tx pgx.Tx, sessionID string, eventType string, detail any) error {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(eventType) == "" {
		return errors.New("event requires session id and type")
	}
	raw, err := marshalBalancedOpenJSON(detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
insert into balanced_open_events (session_id, event_type, detail)
values ($1, $2, $3::jsonb)
`, strings.TrimSpace(sessionID), strings.TrimSpace(eventType), raw)
	return err
}

func marshalBalancedOpenJSON(v any) ([]byte, error) {
	if v == nil {
		return []byte(`{}`), nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return []byte(`{}`), nil
	}
	return raw, nil
}

func scanBalancedOpenSession(row balancedOpenScanner) (BalancedOpenSession, error) {
	var session BalancedOpenSession
	var nextRetry sql.NullTime
	var metadata []byte

	if err := row.Scan(
		&session.ID,
		&session.SessionID,
		&session.Role,
		&session.PeerPubkey,
		&session.PeerHost,
		&session.CapacitySat,
		&session.FeeRateSatVb,
		&session.Private,
		&session.CloseAddress,
		&session.State,
		&session.StateUpdatedAt,
		&session.Attempts,
		&nextRetry,
		&session.LastError,
		&metadata,
		&session.CreatedAt,
		&session.UpdatedAt,
	); err != nil {
		return BalancedOpenSession{}, err
	}

	if nextRetry.Valid {
		ts := nextRetry.Time
		session.NextRetryAt = &ts
	}
	if len(metadata) == 0 {
		session.Metadata = json.RawMessage(`{}`)
	} else {
		session.Metadata = json.RawMessage(metadata)
	}

	return session, nil
}

func scanBalancedOpenEvent(row balancedOpenEventScanner) (BalancedOpenEvent, error) {
	var event BalancedOpenEvent
	var detail []byte

	if err := row.Scan(
		&event.ID,
		&event.SessionID,
		&event.EventType,
		&detail,
		&event.CreatedAt,
	); err != nil {
		return BalancedOpenEvent{}, err
	}

	if len(detail) == 0 {
		event.Detail = json.RawMessage(`{}`)
	} else {
		event.Detail = json.RawMessage(detail)
	}

	return event, nil
}

func newBalancedOpenSessionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func isBalancedOpenRole(role string) bool {
	switch strings.TrimSpace(role) {
	case balancedOpenRoleInitiator, balancedOpenRoleAccepter:
		return true
	default:
		return false
	}
}

func isBalancedOpenState(state string) bool {
	switch strings.TrimSpace(state) {
	case balancedOpenStateSessionCreated:
		return true
	case balancedOpenStateProposalSent:
		return true
	case balancedOpenStateProposalReceived:
		return true
	case balancedOpenStateAccepted:
		return true
	case balancedOpenStateFundingTxHalfSigned:
		return true
	case balancedOpenStateFundingTxFullySigned:
		return true
	case balancedOpenStateChannelProposed:
		return true
	case balancedOpenStateFundingBroadcasted:
		return true
	case balancedOpenStatePendingOpenDetected:
		return true
	case balancedOpenStateActive:
		return true
	case balancedOpenStateFailed:
		return true
	case balancedOpenStateCanceled:
		return true
	case balancedOpenStateRecoveryRequired:
		return true
	case balancedOpenStateRecovered:
		return true
	default:
		return false
	}
}

func isBalancedOpenTerminalState(state string) bool {
	switch strings.TrimSpace(state) {
	case balancedOpenStateActive:
		return true
	case balancedOpenStateFailed:
		return true
	case balancedOpenStateCanceled:
		return true
	case balancedOpenStateRecovered:
		return true
	default:
		return false
	}
}

func isProposalEligibleState(state string) bool {
	switch strings.TrimSpace(state) {
	case balancedOpenStateSessionCreated:
		return true
	case balancedOpenStateProposalSent:
		return true
	case balancedOpenStateProposalReceived:
		return true
	default:
		return false
	}
}
