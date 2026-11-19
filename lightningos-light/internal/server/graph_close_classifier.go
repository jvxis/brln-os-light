package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	graphCloseClassifierWriteReserve = 5 * time.Second
	graphCloseCandidateTimeout       = 5 * time.Second
	graphCloseRPCRetryDelay          = 10 * time.Minute
)

type graphCloseClassificationCandidate struct {
	EventID      int64
	ChanID       uint64
	ChanPoint    string
	Node1PubKey  string
	Node2PubKey  string
	ClosedHeight int
}

type graphCloseClassificationResult struct {
	CloseType       string
	CloseSource     string
	CloseTxID       string
	CloseFeeSat     *int64
	CloseClassifier string
	CloseConfidence string
	CloseReason     string
	ClassifiedAt    time.Time
}

type graphCloseClassificationResolved struct {
	Candidate graphCloseClassificationCandidate
	Result    graphCloseClassificationResult
}

type graphCloseClassificationAttempt struct {
	EventID   int64
	Error     string
	Retryable bool
}

type graphCloseBitcoinClassifier func(context.Context, graphCloseClassificationCandidate) (graphCloseClassificationResult, bool, string, error)

func (s *GraphExplorerService) runCloseClassifierLoop(stopCh <-chan struct{}) {
	if s == nil || s.db == nil {
		return
	}

	s.classifyPendingCloseEventsBackground()

	ticker := time.NewTicker(graphCloseClassifierInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			s.classifyPendingCloseEventsBackground()
		}
	}
}

func (s *GraphExplorerService) classifyPendingCloseEventsBackground() {
	if s == nil || s.db == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), graphCloseClassifierTimeout)
	defer cancel()

	if err := s.classifyPendingCloseEvents(ctx); err != nil && s.logger != nil {
		s.logger.Printf("graph close classifier failed: %v", err)
	}
}

func (s *GraphExplorerService) classifyPendingCloseEvents(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrGraphExplorerDBUnavailable
	}
	if s.lnd != nil && lndRuntimeShouldDeferNonessential(s.lnd.CachedRuntimeInfo()) {
		// Classification inputs are durable in PostgreSQL. Leave them pending and
		// resume after graph sync instead of querying LND during bootstrap.
		return nil
	}

	// Do not give RPCs the context needed to commit completed work. The
	// background cycle still has its original overall deadline.
	deadline := time.Now().Add(graphCloseClassifierTimeout - graphCloseClassifierWriteReserve)
	if outer, ok := ctx.Deadline(); ok && outer.Add(-graphCloseClassifierWriteReserve).Before(deadline) {
		deadline = outer.Add(-graphCloseClassifierWriteReserve)
	}
	workCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	// Check durable work before touching LND. Empty installations normally have
	// no close events, so their classifier remains a PostgreSQL-only no-op.
	candidates, err := loadPendingGraphCloseClassificationCandidates(workCtx, s.db, "", graphCloseClassifierBatchSize)
	if err != nil || len(candidates) == 0 {
		return err
	}

	localPubkey := s.loadLocalPubkey(workCtx)
	if localPubkey != "" {
		candidates, err = loadPendingGraphCloseClassificationCandidates(workCtx, s.db, localPubkey, graphCloseClassifierBatchSize)
		if err != nil || len(candidates) == 0 {
			return err
		}
	}
	localLookup := s.loadLocalClosedChannelLookup(workCtx)
	var bitcoin graphCloseBitcoinClassifier
	if cfg, err := resolveBitcoinRPCConfigForClosedChannels(workCtx); err == nil {
		bitcoin = func(ctx context.Context, candidate graphCloseClassificationCandidate) (graphCloseClassificationResult, bool, string, error) {
			return classifyGraphCloseCandidateWithBitcoin(ctx, candidate, cfg)
		}
	}

	resolved, attempts := collectGraphCloseClassifications(workCtx, candidates, localPubkey, localLookup, bitcoin)
	if err := s.persistGraphCloseClassifications(ctx, resolved, attempts); err != nil {
		return err
	}
	if s.logger != nil {
		rpcFailures := 0
		for _, attempt := range attempts {
			if attempt.Retryable {
				rpcFailures++
			}
		}
		if rpcFailures > 0 {
			s.logger.Printf("graph close classifier: saved %d classifications; deferred %d events after Bitcoin RPC failures", len(resolved), rpcFailures)
		}
	}
	return nil
}

func collectGraphCloseClassifications(ctx context.Context, candidates []graphCloseClassificationCandidate, localPubkey string, localLookup graphExplorerLocalClosedChannelLookup, bitcoin graphCloseBitcoinClassifier) ([]graphCloseClassificationResolved, []graphCloseClassificationAttempt) {
	resolved := make([]graphCloseClassificationResolved, 0, len(candidates))
	attempts := make([]graphCloseClassificationAttempt, 0, len(candidates))

	for _, candidate := range candidates {
		if result, ok := classifyGraphCloseCandidatePhase1(candidate, localPubkey, localLookup); ok {
			resolved = append(resolved, graphCloseClassificationResolved{
				Candidate: candidate,
				Result:    result,
			})
			continue
		}

		if bitcoin == nil || ctx.Err() != nil {
			continue
		}

		candidateCtx, cancel := context.WithTimeout(ctx, graphCloseCandidateTimeout)
		result, ok, attemptError, err := bitcoin(candidateCtx, candidate)
		timedOut := errors.Is(candidateCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded)
		cancel()
		if err != nil {
			reason := "bitcoin_rpc_unavailable"
			if timedOut {
				reason = "bitcoin_rpc_timeout"
			}
			attempts = append(attempts, graphCloseClassificationAttempt{EventID: candidate.EventID, Error: reason, Retryable: true})
			continue
		}
		if ok {
			resolved = append(resolved, graphCloseClassificationResolved{
				Candidate: candidate,
				Result:    result,
			})
			continue
		}
		attempts = append(attempts, graphCloseClassificationAttempt{
			EventID: candidate.EventID,
			Error:   attemptError,
		})
	}
	return resolved, attempts
}

func (s *GraphExplorerService) persistGraphCloseClassifications(ctx context.Context, resolved []graphCloseClassificationResolved, attempts []graphCloseClassificationAttempt) error {
	if len(resolved) == 0 && len(attempts) == 0 {
		return nil
	}

	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	for _, item := range resolved {
		if err := applyGraphCloseClassificationResult(ctx, tx, item.Candidate, item.Result); err != nil {
			return err
		}
	}
	for _, item := range attempts {
		if err := markGraphCloseClassificationAttempt(ctx, tx, item); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func loadPendingGraphCloseClassificationCandidates(ctx context.Context, db interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, localPubkey string, limit int) ([]graphCloseClassificationCandidate, error) {
	if limit <= 0 {
		limit = graphCloseClassifierBatchSize
	}
	localPubkey = graphExplorerNormalizePubkey(localPubkey)

	rows, err := db.Query(ctx, `
select
  id,
  chan_id,
  coalesce(chan_point, ''),
  coalesce(node1_pubkey, ''),
  coalesce(node2_pubkey, ''),
  coalesce(closed_height, 0)
from graph_close_events
where classified_at is null
  and classification_attempts < $1
  and (classification_retry_at is null or classification_retry_at <= now())
order by
  case when $3 <> '' and (lower(coalesce(node1_pubkey, '')) = $3 or lower(coalesce(node2_pubkey, '')) = $3) then 0 else 1 end,
  classification_rpc_failures asc,
  observed_at asc,
  id asc
limit $2
`, graphCloseClassifierMaxAttempts, limit, localPubkey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]graphCloseClassificationCandidate, 0, limit)
	for rows.Next() {
		var item graphCloseClassificationCandidate
		var chanID int64
		if err := rows.Scan(&item.EventID, &chanID, &item.ChanPoint, &item.Node1PubKey, &item.Node2PubKey, &item.ClosedHeight); err != nil {
			return nil, err
		}
		item.ChanID = uint64(chanID)
		item.ChanPoint = graphExplorerNormalizeChanPoint(item.ChanPoint)
		item.Node1PubKey = graphExplorerNormalizePubkey(item.Node1PubKey)
		item.Node2PubKey = graphExplorerNormalizePubkey(item.Node2PubKey)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func classifyGraphCloseCandidatePhase1(candidate graphCloseClassificationCandidate, localPubkey string, lookup graphExplorerLocalClosedChannelLookup) (graphCloseClassificationResult, bool) {
	localPubkey = graphExplorerNormalizePubkey(localPubkey)
	if localPubkey == "" {
		return graphCloseClassificationResult{}, false
	}
	if candidate.Node1PubKey != localPubkey && candidate.Node2PubKey != localPubkey {
		return graphCloseClassificationResult{}, false
	}

	localInfo, ok := lookup.find(candidate.ChanID, candidate.ChanPoint)
	if !ok {
		return graphCloseClassificationResult{}, false
	}
	closeType := normalizeGraphExplorerCloseType(localInfo.CloseType)
	if closeType == "unknown" {
		return graphCloseClassificationResult{}, false
	}

	return graphCloseClassificationResult{
		CloseType:       closeType,
		CloseSource:     "native+lnd",
		CloseTxID:       strings.ToLower(strings.TrimSpace(localInfo.CloseTxID)),
		CloseClassifier: "lnd",
		CloseConfidence: "high",
		CloseReason:     "lnd_closedchannels",
		ClassifiedAt:    time.Now().UTC(),
	}, true
}

func classifyGraphCloseCandidateWithBitcoin(ctx context.Context, candidate graphCloseClassificationCandidate, cfg bitcoinRPCConfig) (graphCloseClassificationResult, bool, string, error) {
	if candidate.ClosedHeight <= 0 {
		return graphCloseClassificationResult{}, false, "invalid_closed_height", nil
	}

	fundingTxID, fundingVout, err := parseGraphCloseFundingOutpoint(candidate.ChanPoint)
	if err != nil {
		return graphCloseClassificationResult{}, false, "invalid_chan_point", nil
	}

	closeTx, err := resolveGraphCloseSpendingTransaction(ctx, cfg, candidate.ClosedHeight, fundingTxID, fundingVout)
	if err != nil {
		return graphCloseClassificationResult{}, false, "", err
	}
	if closeTx == nil {
		return graphCloseClassificationResult{}, false, "close_tx_not_found", nil
	}

	closeType, confidence, reason := classifyGraphCloseTransactionShape(*closeTx)
	if closeType == "unknown" {
		return graphCloseClassificationResult{}, false, reason, nil
	}

	closeFeeSat, feeErr := estimateGraphCloseFeeSat(ctx, cfg, fundingTxID, fundingVout, *closeTx)
	if feeErr != nil {
		closeFeeSat = nil
	}

	return graphCloseClassificationResult{
		CloseType:       closeType,
		CloseSource:     "native+bitcoind",
		CloseTxID:       strings.ToLower(strings.TrimSpace(closeTx.TxID)),
		CloseFeeSat:     closeFeeSat,
		CloseClassifier: "bitcoind",
		CloseConfidence: confidence,
		CloseReason:     reason,
		ClassifiedAt:    time.Now().UTC(),
	}, true, "", nil
}

func markGraphCloseClassificationAttempt(ctx context.Context, tx pgx.Tx, attempt graphCloseClassificationAttempt) error {
	if attempt.Retryable {
		_, err := tx.Exec(ctx, `
update graph_close_events
set classification_error = $2,
    classification_rpc_failures = classification_rpc_failures + 1,
    classification_retry_at = now() + ($3::integer * interval '1 second')
where id = $1 and classified_at is null
`, attempt.EventID, attempt.Error, int(graphCloseRPCRetryDelay/time.Second))
		return err
	}
	classificationError := strings.TrimSpace(attempt.Error)
	if classificationError == "" {
		classificationError = "classification_unresolved"
	}
	_, err := tx.Exec(ctx, `
update graph_close_events
set classification_error = $2,
    classification_retry_at = null,
    classification_attempts = classification_attempts + 1
where id = $1 and classified_at is null
`, attempt.EventID, classificationError)
	return err
}

func applyGraphCloseClassificationResult(ctx context.Context, tx pgx.Tx, candidate graphCloseClassificationCandidate, result graphCloseClassificationResult) error {
	result.CloseType = normalizeGraphExplorerCloseType(result.CloseType)
	result.CloseSource = strings.TrimSpace(result.CloseSource)
	result.CloseTxID = strings.ToLower(strings.TrimSpace(result.CloseTxID))
	result.CloseClassifier = strings.TrimSpace(result.CloseClassifier)
	result.CloseConfidence = strings.TrimSpace(result.CloseConfidence)
	result.CloseReason = strings.TrimSpace(result.CloseReason)
	if result.ClassifiedAt.IsZero() {
		result.ClassifiedAt = time.Now().UTC()
	}
	var closeFeeSat any
	if result.CloseFeeSat != nil {
		closeFeeSat = *result.CloseFeeSat
	}

	_, err := tx.Exec(ctx, `
update graph_close_events
set close_type = case when $2 <> '' then $2 else close_type end,
    close_source = case when $3 <> '' then $3 else close_source end,
    close_txid = case when $4 <> '' then $4 else close_txid end,
    close_classifier = case when $5 <> '' then $5 else close_classifier end,
    close_confidence = case when $6 <> '' then $6 else close_confidence end,
    close_reason = case when $7 <> '' then $7 else close_reason end,
    close_fee_sat = coalesce($8, close_fee_sat),
    classified_at = $9,
    classification_error = null,
    classification_retry_at = null,
    classification_attempts = classification_attempts + 1
where id = $1
`, candidate.EventID, result.CloseType, result.CloseSource, result.CloseTxID, result.CloseClassifier, result.CloseConfidence, result.CloseReason, closeFeeSat, result.ClassifiedAt)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
update graph_channels
set close_type = case when $2 <> '' then $2 else close_type end,
    close_source = case when $3 <> '' then $3 else close_source end,
    close_txid = case when $4 <> '' then $4 else close_txid end,
    close_confidence = case when $5 <> '' then $5 else close_confidence end,
    classified_at = $6
where chan_id = $1
`, int64(candidate.ChanID), result.CloseType, result.CloseSource, result.CloseTxID, result.CloseConfidence, result.ClassifiedAt)
	return err
}
