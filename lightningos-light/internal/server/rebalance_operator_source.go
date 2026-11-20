package server

import (
	"context"
	"errors"
	"sync"
)

// operatorJobOptions carries the choices only the operator's manual form can
// make for one job: a fixed source channel, a one-job exception to that
// channel's persistent source exclusion, and an absolute fee cap. None of
// them is persisted and none of them is read by the autopilot, auto-restart
// or probe jobs.
type operatorJobOptions struct {
	SourceChannelID     uint64
	AllowExcludedSource bool
	MaxFeeMsat          int64
}

// Reasons a fixed source cannot be used. They are stable codes: the UI maps
// them to text and the job stores them in its failure reason.
const (
	fixedSourceReasonNotFound        = "source_not_found"
	fixedSourceReasonIsTarget        = "source_is_target"
	fixedSourceReasonInactive        = "source_inactive"
	fixedSourceReasonParked          = "source_parked"
	fixedSourceReasonQuarantined     = "source_quarantined"
	fixedSourceReasonNoFreeLiquidity = "source_no_free_liquidity"
	fixedSourceReasonExcluded        = "source_excluded"
)

// RebalanceRunPreviewSource tells the operator, before the job is queued,
// whether the channel picked as the fixed source can give liquidity and why
// not when it cannot.
type RebalanceRunPreviewSource struct {
	ChannelID          uint64 `json:"channel_id"`
	ChannelPoint       string `json:"channel_point"`
	PeerAlias          string `json:"peer_alias,omitempty"`
	Excluded           bool   `json:"excluded"`
	ExclusionException bool   `json:"exclusion_exception"`
	Eligible           bool   `json:"eligible"`
	BlockedReason      string `json:"blocked_reason,omitempty"`
	MaxSourceSat       int64  `json:"max_source_sat"`
}

// fixedSourceBlockedReason returns why a channel cannot be the fixed source
// of an operator job, or "" when it can. The one-job exception lifts the
// persistent source exclusion and nothing else: the source floor, the
// paid-liquidity protection, the quarantine and the parked mode all hold.
func fixedSourceBlockedReason(source RebalanceChannel, targetChannelID uint64, allowExcluded bool) string {
	switch {
	case source.ChannelID == 0:
		return fixedSourceReasonNotFound
	case source.ChannelID == targetChannelID:
		return fixedSourceReasonIsTarget
	case !source.Active:
		return fixedSourceReasonInactive
	case isChannelAutomationParked(source.AutomationMode):
		return fixedSourceReasonParked
	case source.SourceQuarantined:
		return fixedSourceReasonQuarantined
	case source.MaxSourceSat <= 0:
		return fixedSourceReasonNoFreeLiquidity
	case source.ExcludedAsSource && !allowExcluded:
		return fixedSourceReasonExcluded
	}
	return ""
}

// resolveFixedRebalanceSource picks the operator's fixed source out of the
// job's channel snapshots. It never falls back to another channel.
func resolveFixedRebalanceSource(snapshots []RebalanceChannel, targetChannelID uint64, sourceChannelID uint64, allowExcluded bool) (RebalanceChannel, string) {
	for _, snapshot := range snapshots {
		if snapshot.ChannelID != sourceChannelID {
			continue
		}
		return snapshot, fixedSourceBlockedReason(snapshot, targetChannelID, allowExcluded)
	}
	return RebalanceChannel{}, fixedSourceReasonNotFound
}

func buildRunPreviewSource(source RebalanceChannel, targetChannelID uint64, allowExcluded bool) *RebalanceRunPreviewSource {
	reason := fixedSourceBlockedReason(source, targetChannelID, allowExcluded)
	return &RebalanceRunPreviewSource{
		ChannelID:          source.ChannelID,
		ChannelPoint:       source.ChannelPoint,
		PeerAlias:          source.PeerAlias,
		Excluded:           source.ExcludedAsSource,
		ExclusionException: source.ExcludedAsSource && allowExcluded,
		Eligible:           reason == "",
		BlockedReason:      reason,
		MaxSourceSat:       source.MaxSourceSat,
	}
}

// operatorSourceSnapshot builds the snapshot of one channel the same way the
// job does, so the preview and the job agree on whether it can be a source.
// A channel that is not open comes back as the zero snapshot.
func (s *RebalanceService) operatorSourceSnapshot(ctx context.Context, cfg RebalanceConfig, sourceChannelID uint64) (RebalanceChannel, error) {
	channels, err := s.listChannelsCached(ctx)
	if err != nil {
		return RebalanceChannel{}, err
	}
	for _, ch := range channels {
		if ch.ChannelID != sourceChannelID {
			continue
		}
		settings, _ := s.loadChannelSettings(ctx)
		exclusions, _ := s.loadExclusions(ctx)
		ledger, _ := s.loadLedger(ctx, cfg)
		revenueByChannel, _ := s.fetchChannelRevenue7d(ctx)
		salePriceByChannel := s.loadSalePricesForSnapshots(ctx, cfg)
		costByChannel, _ := s.fetchChannelRebalanceCost7d(ctx)
		drainRateByChannel := s.fetchChannelDrainRate24h(ctx)
		return s.buildChannelSnapshot(ctx, cfg, false, ch, settings[ch.ChannelID], ledger[ch.ChannelID], revenueByChannel[ch.ChannelID], salePriceByChannel.forChannel(ch), costByChannel[ch.ChannelID], drainRateByChannel[ch.ChannelID], exclusions[ch.ChannelID]), nil
	}
	return RebalanceChannel{}, nil
}

var errOperatorFeeCapExhausted = errors.New("operator fee cap exhausted")

// operatorFeeCap is the absolute fee budget of one operator job. Every fee
// limit the job computes is clamped to what is left of it, and every settled
// lot is charged against it, so splits and retries share one total instead
// of each getting the full cap.
type operatorFeeCap struct {
	mu            sync.Mutex
	remainingMsat int64
}

func newOperatorFeeCap(maxFeeMsat int64) *operatorFeeCap {
	if maxFeeMsat <= 0 {
		return nil
	}
	return &operatorFeeCap{remainingMsat: maxFeeMsat}
}

// clamp returns the smaller of the policy fee limit and what is left of the
// cap. Once nothing is left it reports the cap as exhausted so the caller
// skips the attempt instead of sending with a zero limit.
func (c *operatorFeeCap) clamp(feeMsat int64) (int64, error) {
	if c == nil {
		return feeMsat, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.remainingMsat <= 0 {
		return 0, errOperatorFeeCapExhausted
	}
	if feeMsat > c.remainingMsat {
		return c.remainingMsat, nil
	}
	return feeMsat, nil
}

// spendSat charges a settled lot. The job accounts paid fees in whole sats,
// some of them truncated from msat, so the charge is rounded up to the next
// sat boundary: the total can end under the cap, never over it.
func (c *operatorFeeCap) spendSat(feePaidSat int64) {
	if c == nil {
		return
	}
	if feePaidSat < 0 {
		feePaidSat = 0
	}
	c.mu.Lock()
	c.remainingMsat -= feePaidSat*1000 + 999
	if c.remainingMsat < 0 {
		c.remainingMsat = 0
	}
	c.mu.Unlock()
}

func (c *operatorFeeCap) exhausted() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.remainingMsat <= 0
}
