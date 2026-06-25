package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"
)

const (
	lnChannelDetailDefaultLimit = 25
	lnChannelDetailMaxLimit     = 100
	lnChannelNoteMaxBytes       = 8000
)

type lnChannelDetailResponse struct {
	Channel             lndclient.ChannelInfo              `json:"channel"`
	Peer                *lndclient.PeerInfo                `json:"peer,omitempty"`
	CurrentBlockHeight  int64                              `json:"current_block_height,omitempty"`
	ShortChannelID      string                             `json:"short_channel_id,omitempty"`
	OpenBlockHeight     int64                              `json:"open_block_height,omitempty"`
	OpenedConfirmations int64                              `json:"opened_confirmations,omitempty"`
	Settings            lnChannelDetailSettings            `json:"settings"`
	Periods             []lnChannelDetailPeriod            `json:"periods"`
	FeeLogs             []lnChannelDetailFeeLog            `json:"fee_logs"`
	Routed              []lnChannelDetailForward           `json:"routed"`
	Rebalances          []lnChannelDetailRebalance         `json:"rebalances"`
	Sent                []lnChannelDetailPayment           `json:"sent"`
	Received            []lnChannelDetailPayment           `json:"received"`
	FailedHTLCs         []lnChannelDetailFailure           `json:"failed_htlcs"`
	PeerEvents          []lnChannelDetailPeerEvent         `json:"peer_events"`
	Note                string                             `json:"note"`
	Coverage            lnChannelDetailCoverage            `json:"coverage"`
	DataSourceWarnings  []string                           `json:"data_source_warnings,omitempty"`
	PendingHTLCs        []lndclient.ChannelPendingHtlcInfo `json:"pending_htlcs,omitempty"`
}

type lnChannelDetailSettings struct {
	AutofeeEnabled        bool     `json:"autofee_enabled"`
	AutofeeConfigured     bool     `json:"autofee_configured"`
	AutofeeLastPpm        *int     `json:"autofee_last_ppm,omitempty"`
	AutofeeLastInboundPpm *int     `json:"autofee_last_inbound_ppm,omitempty"`
	AutofeeLastAt         string   `json:"autofee_last_at,omitempty"`
	AutofeeLastDirection  string   `json:"autofee_last_direction,omitempty"`
	AutofeeClassLabel     string   `json:"autofee_class_label,omitempty"`
	AutofeeStalledRounds  int      `json:"autofee_stalled_rounds,omitempty"`
	RebalanceConfigured   bool     `json:"rebalance_configured"`
	RebalanceAutoEnabled  bool     `json:"rebalance_auto_enabled"`
	ManualRestartEnabled  bool     `json:"manual_restart_enabled"`
	TargetOutboundPct     float64  `json:"target_outbound_pct,omitempty"`
	UseDefaultEconRatio   bool     `json:"use_default_econ_ratio"`
	EconRatioOverride     *float64 `json:"econ_ratio_override,omitempty"`
	AutoBypassCostGate    bool     `json:"auto_bypass_cost_gate"`
	ExcludedAsSource      bool     `json:"excluded_as_source"`
}

type lnChannelDetailPeriod struct {
	Period                string  `json:"period"`
	Days                  int     `json:"days,omitempty"`
	ForwardCount          int64   `json:"forward_count"`
	ForwardInCount        int64   `json:"forward_in_count"`
	ForwardOutCount       int64   `json:"forward_out_count"`
	ForwardInAmountSat    int64   `json:"forward_in_amount_sat"`
	ForwardOutAmountSat   int64   `json:"forward_out_amount_sat"`
	RebalanceInCount      int64   `json:"rebalance_in_count"`
	RebalanceOutCount     int64   `json:"rebalance_out_count"`
	RebalanceInAmountSat  int64   `json:"rebalance_in_amount_sat"`
	RebalanceOutAmountSat int64   `json:"rebalance_out_amount_sat"`
	RevenueSat            int64   `json:"revenue_sat"`
	CostSat               int64   `json:"cost_sat"`
	ProfitSat             int64   `json:"profit_sat"`
	OutPpm                int     `json:"out_ppm,omitempty"`
	RebalancePpm          int     `json:"rebalance_ppm,omitempty"`
	APY                   float64 `json:"apy,omitempty"`
}

type lnChannelDetailFeeLog struct {
	CapturedAt           string   `json:"captured_at"`
	Side                 string   `json:"side"`
	OldFeeRatePpm        *int64   `json:"old_fee_rate_ppm,omitempty"`
	NewFeeRatePpm        int64    `json:"new_fee_rate_ppm"`
	OldBaseMsat          *int64   `json:"old_base_msat,omitempty"`
	NewBaseMsat          int64    `json:"new_base_msat"`
	OldInboundFeeRatePpm *int64   `json:"old_inbound_fee_rate_ppm,omitempty"`
	NewInboundFeeRatePpm int64    `json:"new_inbound_fee_rate_ppm"`
	OldDisabled          *bool    `json:"old_disabled,omitempty"`
	NewDisabled          bool     `json:"new_disabled"`
	ChangePct            *float64 `json:"change_pct,omitempty"`
}

type lnChannelDetailForward struct {
	OccurredAt   string `json:"occurred_at"`
	PaymentHash  string `json:"payment_hash,omitempty"`
	ChanIDIn     uint64 `json:"chan_id_in,omitempty"`
	ChanIDOut    uint64 `json:"chan_id_out,omitempty"`
	ChanInAlias  string `json:"chan_in_alias,omitempty"`
	ChanOutAlias string `json:"chan_out_alias,omitempty"`
	AmountInSat  int64  `json:"amount_in_sat"`
	AmountOutSat int64  `json:"amount_out_sat"`
	FeeSat       int64  `json:"fee_sat"`
	PPM          int    `json:"ppm,omitempty"`
	Status       string `json:"status,omitempty"`
}

type lnChannelDetailRebalance struct {
	OccurredAt      string `json:"occurred_at"`
	Status          string `json:"status,omitempty"`
	Direction       string `json:"direction"`
	SourceChannelID uint64 `json:"source_channel_id,omitempty"`
	TargetChannelID uint64 `json:"target_channel_id,omitempty"`
	SourceAlias     string `json:"source_alias,omitempty"`
	TargetAlias     string `json:"target_alias,omitempty"`
	SourcePoint     string `json:"source_point,omitempty"`
	TargetPoint     string `json:"target_point,omitempty"`
	AmountSat       int64  `json:"amount_sat"`
	FeeSat          int64  `json:"fee_sat,omitempty"`
	PPM             int    `json:"ppm,omitempty"`
	PaymentHash     string `json:"payment_hash,omitempty"`
	Memo            string `json:"memo,omitempty"`
}

type lnChannelDetailPayment struct {
	OccurredAt   string `json:"occurred_at"`
	Type         string `json:"type"`
	Status       string `json:"status"`
	AmountSat    int64  `json:"amount_sat"`
	FeeSat       int64  `json:"fee_sat,omitempty"`
	PaymentHash  string `json:"payment_hash,omitempty"`
	Memo         string `json:"memo,omitempty"`
	ChannelAlias string `json:"channel_alias,omitempty"`
}

type lnChannelDetailFailure struct {
	OccurredAt        string `json:"occurred_at"`
	Source            string `json:"source"`
	IncomingChannelID string `json:"incoming_channel_id,omitempty"`
	IncomingAlias     string `json:"incoming_alias,omitempty"`
	OutgoingChannelID string `json:"outgoing_channel_id,omitempty"`
	OutgoingAlias     string `json:"outgoing_alias,omitempty"`
	AmountSat         int64  `json:"amount_sat,omitempty"`
	PotentialFeeSat   int64  `json:"potential_fee_sat,omitempty"`
	FailureCode       string `json:"failure_code,omitempty"`
	FailureDetail     string `json:"failure_detail,omitempty"`
	PaymentHash       string `json:"payment_hash,omitempty"`
}

type lnChannelDetailPeerEvent struct {
	OccurredAt string `json:"occurred_at"`
	Side       string `json:"side"`
	Setting    string `json:"setting"`
	OldValue   string `json:"old_value,omitempty"`
	NewValue   string `json:"new_value"`
}

type lnChannelDetailCoverage struct {
	NotificationsSince string `json:"notifications_since,omitempty"`
	NotificationsUntil string `json:"notifications_until,omitempty"`
	PolicySince        string `json:"policy_since,omitempty"`
	PolicyUntil        string `json:"policy_until,omitempty"`
}

func (s *Server) ensureChannelNotesSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.Exec(ctx, `
create table if not exists ln_channel_notes (
  channel_point text primary key,
  note text not null default '',
  updated_at timestamptz not null default now()
);
`)
	return err
}

func (s *Server) handleLNChannelDetail(w http.ResponseWriter, r *http.Request) {
	channelPoint := strings.TrimSpace(r.URL.Query().Get("channel_point"))
	channelID, channelIDOK := parseLNChannelDetailChannelID(r.URL.Query().Get("channel_id"))
	if channelPoint == "" && !channelIDOK {
		writeError(w, http.StatusBadRequest, "channel_point or channel_id required")
		return
	}
	limit, err := parseLNChannelDetailLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	lndCtx, lndCancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer lndCancel()

	currentBlockHeight := int64(0)
	selfPubkey := ""
	if status, statusErr := s.lnd.GetStatus(lndCtx); statusErr == nil {
		currentBlockHeight = status.BlockHeight
		selfPubkey = strings.TrimSpace(status.Pubkey)
	}

	channels, err := s.lnd.ListChannels(lndCtx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}

	if s.db != nil {
		dbCtx, dbCancel := context.WithTimeout(r.Context(), 2*time.Second)
		if err := s.applyPersistedChannelDowntime(dbCtx, channels); err != nil && s.logger != nil {
			s.logger.Printf("channel detail downtime sync failed: %v", err)
		}
		dbCancel()
	}

	aliasByID := lnChannelAliasByID(channels)
	var selected lndclient.ChannelInfo
	found := false
	normalizedPoint := normalizeChannelPoint(channelPoint)
	for _, ch := range channels {
		if normalizedPoint != "" && normalizeChannelPoint(ch.ChannelPoint) == normalizedPoint {
			selected = ch
			found = true
			break
		}
		if channelIDOK && ch.ChannelID == channelID {
			selected = ch
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}

	detail := lnChannelDetailResponse{
		Channel:            selected,
		CurrentBlockHeight: currentBlockHeight,
		ShortChannelID:     formatShortChanID(selected.ChannelID),
		OpenBlockHeight:    int64(channelBlockHeight(selected.ChannelID)),
		PendingHTLCs:       selected.PendingHtlcs,
	}
	if detail.OpenBlockHeight > 0 && currentBlockHeight >= detail.OpenBlockHeight {
		detail.OpenedConfirmations = currentBlockHeight - detail.OpenBlockHeight + 1
	}

	detail.Peer = s.lookupLNChannelDetailPeer(r.Context(), selected.RemotePubkey)

	warnings := []string{}
	if s.db == nil {
		warnings = append(warnings, "postgres unavailable: historical channel detail is limited")
		detail.DataSourceWarnings = warnings
		writeJSON(w, http.StatusOK, detail)
		return
	}

	dbCtx, dbCancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer dbCancel()

	if err := s.ensureChannelNotesSchema(dbCtx); err != nil && s.logger != nil {
		s.logger.Printf("channel notes schema unavailable: %v", err)
	}
	if err := s.applyLNChannelDetailEnrichment(dbCtx, &detail.Channel); err != nil && s.logger != nil {
		s.logger.Printf("channel detail live enrichment failed: %v", err)
	}
	settings, err := s.loadLNChannelDetailSettings(dbCtx, detail.Channel)
	if err != nil {
		warnings = append(warnings, "settings unavailable")
		if s.logger != nil {
			s.logger.Printf("channel detail settings failed: %v", err)
		}
	} else {
		detail.Settings = settings
	}
	if periods, err := s.loadLNChannelDetailPeriods(dbCtx, detail.Channel.ChannelID, detail.Channel.CapacitySat); err != nil {
		warnings = append(warnings, "period stats unavailable")
		if s.logger != nil {
			s.logger.Printf("channel detail periods failed: %v", err)
		}
	} else {
		detail.Periods = periods
		lnApplySevenDayStatsToChannel(&detail.Channel, periods)
	}
	if feeLogs, err := s.loadLNChannelDetailFeeLogs(dbCtx, detail.Channel, selfPubkey, limit); err != nil {
		warnings = append(warnings, "fee logs unavailable")
		if s.logger != nil {
			s.logger.Printf("channel detail fee logs failed: %v", err)
		}
	} else {
		detail.FeeLogs = feeLogs
		detail.PeerEvents = lnChannelPeerEventsFromFeeLogs(feeLogs)
	}
	if routed, err := s.loadLNChannelDetailForwards(dbCtx, detail.Channel.ChannelID, aliasByID, limit); err != nil {
		warnings = append(warnings, "routed payments unavailable")
		if s.logger != nil {
			s.logger.Printf("channel detail forwards failed: %v", err)
		}
	} else {
		detail.Routed = routed
	}
	if rebalances, err := s.loadLNChannelDetailRebalances(dbCtx, detail.Channel, aliasByID, limit); err != nil {
		warnings = append(warnings, "rebalances unavailable")
		if s.logger != nil {
			s.logger.Printf("channel detail rebalances failed: %v", err)
		}
	} else {
		detail.Rebalances = rebalances
	}
	if sent, err := s.loadLNChannelDetailPayments(dbCtx, detail.Channel, "sent", limit); err != nil {
		warnings = append(warnings, "sent payments unavailable")
		if s.logger != nil {
			s.logger.Printf("channel detail sent payments failed: %v", err)
		}
	} else {
		detail.Sent = sent
	}
	if received, err := s.loadLNChannelDetailPayments(dbCtx, detail.Channel, "received", limit); err != nil {
		warnings = append(warnings, "received payments unavailable")
		if s.logger != nil {
			s.logger.Printf("channel detail received payments failed: %v", err)
		}
	} else {
		detail.Received = received
	}
	if failed, err := s.loadLNChannelDetailFailures(dbCtx, detail.Channel.ChannelID, aliasByID, limit); err != nil {
		warnings = append(warnings, "failed HTLC history unavailable")
		if s.logger != nil {
			s.logger.Printf("channel detail failures failed: %v", err)
		}
	} else {
		detail.FailedHTLCs = append(detail.FailedHTLCs, failed...)
	}
	detail.FailedHTLCs = append(detail.FailedHTLCs, s.loadLNChannelDetailHTLCManagerFailures(detail.Channel.ChannelID, aliasByID, limit)...)
	if len(detail.FailedHTLCs) > limit {
		detail.FailedHTLCs = detail.FailedHTLCs[:limit]
	}
	if note, err := s.loadLNChannelNote(dbCtx, detail.Channel.ChannelPoint); err == nil {
		detail.Note = note
	}
	if coverage, err := s.loadLNChannelDetailCoverage(dbCtx, detail.Channel.ChannelID); err == nil {
		detail.Coverage = coverage
	}
	detail.DataSourceWarnings = warnings

	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleLNChannelNotesPost(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "db unavailable")
		return
	}
	var req struct {
		ChannelPoint string `json:"channel_point"`
		Note         string `json:"note"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	channelPoint := strings.TrimSpace(req.ChannelPoint)
	if channelPoint == "" {
		writeError(w, http.StatusBadRequest, "channel_point required")
		return
	}
	note := strings.TrimSpace(req.Note)
	if len([]byte(note)) > lnChannelNoteMaxBytes {
		writeError(w, http.StatusBadRequest, "note too long")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.ensureChannelNotesSchema(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := s.db.Exec(ctx, `
insert into ln_channel_notes (channel_point, note, updated_at)
values ($1, $2, now())
on conflict (channel_point) do update
set note = excluded.note,
    updated_at = now()
`, normalizeChannelPoint(channelPoint), note); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "note": note})
}

func parseLNChannelDetailLimit(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return lnChannelDetailDefaultLimit, nil
	}
	value, err := strconv.Atoi(trimmed)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid limit")
	}
	if value > lnChannelDetailMaxLimit {
		value = lnChannelDetailMaxLimit
	}
	return value, nil
}

func parseLNChannelDetailChannelID(raw string) (uint64, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, false
	}
	if id, ok := parseShortChannelID(trimmed); ok {
		return id, true
	}
	id, err := strconv.ParseUint(trimmed, 10, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	return id, true
}

func lnChannelAliasByID(channels []lndclient.ChannelInfo) map[uint64]string {
	out := make(map[uint64]string, len(channels))
	for _, ch := range channels {
		if ch.ChannelID == 0 {
			continue
		}
		alias := strings.TrimSpace(ch.PeerAlias)
		if alias == "" {
			alias = shortIdentifier(ch.RemotePubkey)
		}
		out[ch.ChannelID] = alias
	}
	return out
}

func (s *Server) lookupLNChannelDetailPeer(ctx context.Context, remotePubkey string) *lndclient.PeerInfo {
	remotePubkey = strings.ToLower(strings.TrimSpace(remotePubkey))
	if remotePubkey == "" || s == nil || s.lnd == nil {
		return nil
	}
	peerCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	peers, err := s.lnd.ListPeers(peerCtx)
	if err != nil {
		return nil
	}
	peers = s.enrichPeerAliasesFromGraph(peerCtx, peers)
	for i := range peers {
		if strings.ToLower(strings.TrimSpace(peers[i].PubKey)) == remotePubkey {
			peer := peers[i]
			return &peer
		}
	}
	return nil
}

func (s *Server) applyLNChannelDetailEnrichment(ctx context.Context, channel *lndclient.ChannelInfo) error {
	if s == nil || s.db == nil || channel == nil {
		return nil
	}
	var classLabel string
	var lastPpm *int
	var lastInbound *int
	err := s.db.QueryRow(ctx, `
select coalesce(class_label, ''), last_ppm, last_inbound_discount_ppm
from autofee_state
where channel_id = $1
`, int64(channel.ChannelID)).Scan(&classLabel, &lastPpm, &lastInbound)
	if err == nil {
		channel.ClassLabel = classLabel
		if channel.FeeRatePpm == nil && lastPpm != nil {
			v := int64(*lastPpm)
			channel.FeeRatePpm = &v
		}
		if channel.InboundFeeRatePpm == nil && lastInbound != nil {
			v := int64(*lastInbound)
			channel.InboundFeeRatePpm = &v
		}
	}
	movement, err := s.loadChannelMovement7d(ctx)
	if err == nil {
		if item, ok := movement[channel.ChannelID]; ok {
			channel.Movement7d = &item
		}
	}
	return nil
}

func (s *Server) loadLNChannelDetailSettings(ctx context.Context, channel lndclient.ChannelInfo) (lnChannelDetailSettings, error) {
	settings := lnChannelDetailSettings{
		AutofeeEnabled:      true,
		UseDefaultEconRatio: true,
	}
	var autofeeEnabled bool
	err := s.db.QueryRow(ctx, `
select enabled
from autofee_channel_settings
where channel_id = $1
`, int64(channel.ChannelID)).Scan(&autofeeEnabled)
	if err == nil {
		settings.AutofeeConfigured = true
		settings.AutofeeEnabled = autofeeEnabled
	}

	var lastPpm *int
	var lastInbound *int
	var lastAt *time.Time
	var lastDir string
	var classLabel string
	var stalledRounds int
	err = s.db.QueryRow(ctx, `
select last_ppm, last_inbound_discount_ppm, last_ts, coalesce(last_dir, ''), coalesce(class_label, ''), stalled_rounds
from autofee_state
where channel_id = $1
`, int64(channel.ChannelID)).Scan(&lastPpm, &lastInbound, &lastAt, &lastDir, &classLabel, &stalledRounds)
	if err == nil {
		settings.AutofeeLastPpm = lastPpm
		settings.AutofeeLastInboundPpm = lastInbound
		if lastAt != nil && !lastAt.IsZero() {
			settings.AutofeeLastAt = lastAt.UTC().Format(time.RFC3339)
		}
		settings.AutofeeLastDirection = strings.TrimSpace(lastDir)
		settings.AutofeeClassLabel = strings.TrimSpace(classLabel)
		settings.AutofeeStalledRounds = stalledRounds
	}

	var (
		targetPct          float64
		autoEnabled        bool
		manualRestart      bool
		useDefaultEcon     bool
		econOverride       *float64
		autoBypassCostGate bool
	)
	err = s.db.QueryRow(ctx, `
select target_outbound_pct, auto_enabled, manual_restart_enabled, use_default_econ_ratio, econ_ratio_override, auto_bypass_cost_gate
from rebalance_channel_settings
where channel_id = $1
`, int64(channel.ChannelID)).Scan(&targetPct, &autoEnabled, &manualRestart, &useDefaultEcon, &econOverride, &autoBypassCostGate)
	if err == nil {
		settings.RebalanceConfigured = true
		settings.TargetOutboundPct = targetPct
		settings.RebalanceAutoEnabled = autoEnabled
		settings.ManualRestartEnabled = manualRestart
		settings.UseDefaultEconRatio = useDefaultEcon
		settings.EconRatioOverride = econOverride
		settings.AutoBypassCostGate = autoBypassCostGate
	}
	var excluded bool
	err = s.db.QueryRow(ctx, `select exists(select 1 from rebalance_source_exclusions where channel_id = $1)`, int64(channel.ChannelID)).Scan(&excluded)
	if err == nil {
		settings.ExcludedAsSource = excluded
	}
	if settings.AutofeeClassLabel == "" {
		settings.AutofeeClassLabel = channel.ClassLabel
	}
	return settings, nil
}

func (s *Server) loadLNChannelDetailPeriods(ctx context.Context, channelID uint64, capacitySat int64) ([]lnChannelDetailPeriod, error) {
	specs := []struct {
		Name      string
		Days      int
		Condition string
	}{
		{Name: "1d", Days: 1, Condition: "occurred_at >= now() - interval '1 day'"},
		{Name: "7d", Days: 7, Condition: "occurred_at >= now() - interval '7 day'"},
		{Name: "30d", Days: 30, Condition: "occurred_at >= now() - interval '30 day'"},
		{Name: "lifetime", Days: 0, Condition: "true"},
	}
	out := make([]lnChannelDetailPeriod, 0, len(specs))
	for _, spec := range specs {
		query := fmt.Sprintf(`
select
  coalesce(count(*) filter (where type = 'forward' and (chan_id_in = $1 or chan_id_out = $1)), 0)::bigint,
  coalesce(count(*) filter (where type = 'forward' and chan_id_in = $1), 0)::bigint,
  coalesce(count(*) filter (where type = 'forward' and chan_id_out = $1), 0)::bigint,
  coalesce(sum(case when type = 'forward' and chan_id_in = $1 then case when amount_in_msat > 0 then amount_in_msat else amount_sat * 1000 end else 0 end), 0)::bigint,
  coalesce(sum(case when type = 'forward' and chan_id_out = $1 then case when amount_out_msat > 0 then amount_out_msat else amount_sat * 1000 end else 0 end), 0)::bigint,
  coalesce(sum(case when type = 'forward' and coalesce(chan_id_out, channel_id) = $1 then case when fee_msat > 0 then fee_msat else fee_sat * 1000 end else 0 end), 0)::bigint,
  coalesce(count(*) filter (where type = 'rebalance' and status in ('SETTLED', 'SUCCEEDED') and coalesce(rebal_target_chan_id, channel_id) = $1), 0)::bigint,
  coalesce(count(*) filter (where type = 'rebalance' and status in ('SETTLED', 'SUCCEEDED') and rebal_source_chan_id = $1), 0)::bigint,
  coalesce(sum(case when type = 'rebalance' and status in ('SETTLED', 'SUCCEEDED') and coalesce(rebal_target_chan_id, channel_id) = $1 then case when amount_sat > 0 then amount_sat * 1000 when amount_out_msat > 0 then amount_out_msat else 0 end else 0 end), 0)::bigint,
  coalesce(sum(case when type = 'rebalance' and status in ('SETTLED', 'SUCCEEDED') and rebal_source_chan_id = $1 then case when amount_sat > 0 then amount_sat * 1000 when amount_out_msat > 0 then amount_out_msat else 0 end else 0 end), 0)::bigint,
  coalesce(sum(case when type = 'rebalance' and status in ('SETTLED', 'SUCCEEDED') and coalesce(rebal_target_chan_id, channel_id) = $1 then case when fee_msat > 0 then fee_msat else fee_sat * 1000 end else 0 end), 0)::bigint
from notifications
where %s
`, spec.Condition)
		var row lnChannelDetailPeriod
		var forwardInMsat, forwardOutMsat, revenueMsat, rebalInMsat, rebalOutMsat, costMsat int64
		err := s.db.QueryRow(ctx, query, int64(channelID)).Scan(
			&row.ForwardCount,
			&row.ForwardInCount,
			&row.ForwardOutCount,
			&forwardInMsat,
			&forwardOutMsat,
			&revenueMsat,
			&row.RebalanceInCount,
			&row.RebalanceOutCount,
			&rebalInMsat,
			&rebalOutMsat,
			&costMsat,
		)
		if err != nil {
			return nil, err
		}
		row.Period = spec.Name
		row.Days = spec.Days
		row.ForwardInAmountSat = msatToSatCeil(forwardInMsat)
		row.ForwardOutAmountSat = msatToSatCeil(forwardOutMsat)
		row.RebalanceInAmountSat = msatToSatCeil(rebalInMsat)
		row.RebalanceOutAmountSat = msatToSatCeil(rebalOutMsat)
		row.RevenueSat = msatToSatCeil(revenueMsat)
		row.CostSat = msatToSatCeil(costMsat)
		row.ProfitSat = row.RevenueSat - row.CostSat
		row.OutPpm = ppmMsat(revenueMsat, forwardOutMsat)
		row.RebalancePpm = ppmMsat(costMsat, rebalInMsat)
		if spec.Days > 0 && capacitySat > 0 {
			row.APY = (float64(row.ProfitSat) / float64(capacitySat)) * (365.0 / float64(spec.Days)) * 100.0
		}
		out = append(out, row)
	}
	return out, nil
}

func lnApplySevenDayStatsToChannel(channel *lndclient.ChannelInfo, periods []lnChannelDetailPeriod) {
	if channel == nil {
		return
	}
	for _, period := range periods {
		if period.Period != "7d" {
			continue
		}
		outPpm := period.OutPpm
		rebalPpm := period.RebalancePpm
		forwardFee := period.RevenueSat
		rebalFee := period.CostSat
		profit := period.ProfitSat
		channel.OutPpm7d = &outPpm
		channel.RebalPpm7d = &rebalPpm
		channel.ForwardFee7dSat = &forwardFee
		channel.RebalFee7dSat = &rebalFee
		channel.ProfitFee7dSat = &profit
		return
	}
}

func (s *Server) loadLNChannelDetailFeeLogs(ctx context.Context, channel lndclient.ChannelInfo, selfPubkey string, limit int) ([]lnChannelDetailFeeLog, error) {
	rows, err := s.db.Query(ctx, `
with history as (
  select
    captured_at,
    advertising_pubkey,
    fee_rate_ppm,
    fee_base_msat,
    inbound_fee_rate_ppm,
    disabled,
    lag(fee_rate_ppm) over (partition by advertising_pubkey order by captured_at asc, id asc) as old_fee_rate_ppm,
    lag(fee_base_msat) over (partition by advertising_pubkey order by captured_at asc, id asc) as old_base_msat,
    lag(inbound_fee_rate_ppm) over (partition by advertising_pubkey order by captured_at asc, id asc) as old_inbound_fee_rate_ppm,
    lag(disabled) over (partition by advertising_pubkey order by captured_at asc, id asc) as old_disabled
  from graph_channel_policy_history
  where chan_id = $1
    and captured_at >= now() - interval '30 day'
)
select captured_at, advertising_pubkey, fee_rate_ppm, fee_base_msat, inbound_fee_rate_ppm, disabled,
  old_fee_rate_ppm, old_base_msat, old_inbound_fee_rate_ppm, old_disabled
from history
where old_fee_rate_ppm is null
   or old_fee_rate_ppm <> fee_rate_ppm
   or old_base_msat <> fee_base_msat
   or old_inbound_fee_rate_ppm <> inbound_fee_rate_ppm
   or old_disabled <> disabled
order by captured_at desc
limit $2
`, int64(channel.ChannelID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []lnChannelDetailFeeLog{}
	remotePubkey := strings.ToLower(strings.TrimSpace(channel.RemotePubkey))
	selfPubkey = strings.ToLower(strings.TrimSpace(selfPubkey))
	for rows.Next() {
		var (
			capturedAt        time.Time
			advertisingPubkey string
			newFeeRate        int64
			newBase           int64
			newInbound        int64
			newDisabled       bool
			oldFeeRate        *int64
			oldBase           *int64
			oldInbound        *int64
			oldDisabled       *bool
		)
		if err := rows.Scan(&capturedAt, &advertisingPubkey, &newFeeRate, &newBase, &newInbound, &newDisabled, &oldFeeRate, &oldBase, &oldInbound, &oldDisabled); err != nil {
			return nil, err
		}
		side := "network"
		pubkey := strings.ToLower(strings.TrimSpace(advertisingPubkey))
		if selfPubkey != "" && pubkey == selfPubkey {
			side = "local"
		} else if remotePubkey != "" && pubkey == remotePubkey {
			side = "peer"
		}
		var changePct *float64
		if oldFeeRate != nil && *oldFeeRate > 0 {
			v := ((float64(newFeeRate) - float64(*oldFeeRate)) / float64(*oldFeeRate)) * 100.0
			changePct = &v
		}
		out = append(out, lnChannelDetailFeeLog{
			CapturedAt:           capturedAt.UTC().Format(time.RFC3339),
			Side:                 side,
			OldFeeRatePpm:        oldFeeRate,
			NewFeeRatePpm:        newFeeRate,
			OldBaseMsat:          oldBase,
			NewBaseMsat:          newBase,
			OldInboundFeeRatePpm: oldInbound,
			NewInboundFeeRatePpm: newInbound,
			OldDisabled:          oldDisabled,
			NewDisabled:          newDisabled,
			ChangePct:            changePct,
		})
	}
	return out, rows.Err()
}

func lnChannelPeerEventsFromFeeLogs(logs []lnChannelDetailFeeLog) []lnChannelDetailPeerEvent {
	out := []lnChannelDetailPeerEvent{}
	for _, item := range logs {
		if item.OldDisabled != nil && *item.OldDisabled != item.NewDisabled {
			out = append(out, lnChannelDetailPeerEvent{
				OccurredAt: item.CapturedAt,
				Side:       item.Side,
				Setting:    "disabled",
				OldValue:   strconv.FormatBool(*item.OldDisabled),
				NewValue:   strconv.FormatBool(item.NewDisabled),
			})
		}
		if item.OldFeeRatePpm != nil && *item.OldFeeRatePpm != item.NewFeeRatePpm {
			out = append(out, lnChannelDetailPeerEvent{
				OccurredAt: item.CapturedAt,
				Side:       item.Side,
				Setting:    "fee_rate_ppm",
				OldValue:   strconv.FormatInt(*item.OldFeeRatePpm, 10),
				NewValue:   strconv.FormatInt(item.NewFeeRatePpm, 10),
			})
		}
	}
	if len(out) > 25 {
		return out[:25]
	}
	return out
}

func (s *Server) loadLNChannelDetailForwards(ctx context.Context, channelID uint64, aliasByID map[uint64]string, limit int) ([]lnChannelDetailForward, error) {
	rows, err := s.db.Query(ctx, `
select occurred_at, coalesce(payment_hash, ''), coalesce(chan_id_in, 0), coalesce(chan_id_out, 0),
  coalesce(case when amount_in_msat > 0 then amount_in_msat else amount_sat * 1000 end, 0),
  coalesce(case when amount_out_msat > 0 then amount_out_msat else amount_sat * 1000 end, 0),
  coalesce(case when fee_msat > 0 then fee_msat else fee_sat * 1000 end, 0),
  coalesce(status, '')
from notifications
where type = 'forward'
  and (chan_id_in = $1 or chan_id_out = $1)
order by occurred_at desc
limit $2
`, int64(channelID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []lnChannelDetailForward{}
	for rows.Next() {
		var row lnChannelDetailForward
		var occurredAt time.Time
		var chanIn, chanOut int64
		var amountInMsat, amountOutMsat, feeMsat int64
		if err := rows.Scan(&occurredAt, &row.PaymentHash, &chanIn, &chanOut, &amountInMsat, &amountOutMsat, &feeMsat, &row.Status); err != nil {
			return nil, err
		}
		row.OccurredAt = occurredAt.UTC().Format(time.RFC3339)
		if chanIn > 0 {
			row.ChanIDIn = uint64(chanIn)
			row.ChanInAlias = aliasByID[row.ChanIDIn]
		}
		if chanOut > 0 {
			row.ChanIDOut = uint64(chanOut)
			row.ChanOutAlias = aliasByID[row.ChanIDOut]
		}
		row.AmountInSat = msatToSatCeil(amountInMsat)
		row.AmountOutSat = msatToSatCeil(amountOutMsat)
		row.FeeSat = msatToSatCeil(feeMsat)
		row.PPM = ppmMsat(feeMsat, amountOutMsat)
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Server) loadLNChannelDetailRebalances(ctx context.Context, channel lndclient.ChannelInfo, aliasByID map[uint64]string, limit int) ([]lnChannelDetailRebalance, error) {
	rows, err := s.db.Query(ctx, `
select occurred_at,
  coalesce(status, ''),
  coalesce(rebal_source_chan_id, 0),
  coalesce(rebal_target_chan_id, channel_id, 0),
  coalesce(rebal_source_point, ''),
  coalesce(rebal_target_point, channel_point, ''),
  coalesce(case when amount_sat > 0 then amount_sat * 1000 when amount_out_msat > 0 then amount_out_msat else 0 end, 0),
  coalesce(case when fee_msat > 0 then fee_msat else fee_sat * 1000 end, 0),
  coalesce(payment_hash, ''),
  coalesce(memo, '')
from notifications
where type = 'rebalance'
  and (
    rebal_target_chan_id = $1
    or channel_id = $1
    or rebal_source_chan_id = $1
  )
order by occurred_at desc
limit $2
`, int64(channel.ChannelID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []lnChannelDetailRebalance{}
	for rows.Next() {
		var row lnChannelDetailRebalance
		var occurredAt time.Time
		var sourceID, targetID int64
		var amountMsat, feeMsat int64
		if err := rows.Scan(&occurredAt, &row.Status, &sourceID, &targetID, &row.SourcePoint, &row.TargetPoint, &amountMsat, &feeMsat, &row.PaymentHash, &row.Memo); err != nil {
			return nil, err
		}
		row.OccurredAt = occurredAt.UTC().Format(time.RFC3339)
		if sourceID > 0 {
			row.SourceChannelID = uint64(sourceID)
			row.SourceAlias = aliasByID[row.SourceChannelID]
		}
		if targetID > 0 {
			row.TargetChannelID = uint64(targetID)
			row.TargetAlias = aliasByID[row.TargetChannelID]
		}
		row.AmountSat = msatToSatCeil(amountMsat)
		row.FeeSat = msatToSatCeil(feeMsat)
		row.PPM = ppmMsat(feeMsat, amountMsat)
		switch {
		case row.TargetChannelID == channel.ChannelID:
			row.Direction = "in"
		case row.SourceChannelID == channel.ChannelID:
			row.Direction = "out"
		default:
			row.Direction = "related"
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Server) loadLNChannelDetailPayments(ctx context.Context, channel lndclient.ChannelInfo, action string, limit int) ([]lnChannelDetailPayment, error) {
	rows, err := s.db.Query(ctx, `
select occurred_at, type, status, amount_sat, fee_sat, coalesce(payment_hash, ''), coalesce(memo, ''), coalesce(channel_alias, '')
from notifications
where type in ('lightning', 'keysend')
  and action = $1
  and (channel_id = $2 or lower(coalesce(channel_point, '')) = $3)
order by occurred_at desc
limit $4
`, action, int64(channel.ChannelID), normalizeChannelPoint(channel.ChannelPoint), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []lnChannelDetailPayment{}
	for rows.Next() {
		var row lnChannelDetailPayment
		var occurredAt time.Time
		if err := rows.Scan(&occurredAt, &row.Type, &row.Status, &row.AmountSat, &row.FeeSat, &row.PaymentHash, &row.Memo, &row.ChannelAlias); err != nil {
			return nil, err
		}
		row.OccurredAt = occurredAt.UTC().Format(time.RFC3339)
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Server) loadLNChannelDetailFailures(ctx context.Context, channelID uint64, aliasByID map[uint64]string, limit int) ([]lnChannelDetailFailure, error) {
	rows, err := s.db.Query(ctx, `
select
  coalesce(a.attempt_resolved_at, a.attempt_started_at, a.payment_created_at) as occurred_at,
  a.payment_hash,
  coalesce(a.failure_code, ''),
  coalesce(a.failure_source_index, -1),
  coalesce(h.channel_id, 0),
  coalesce(h.amt_to_forward_msat, 0),
  coalesce(h.fee_msat, 0)
from payment_route_attempts a
join payment_route_hops h on h.payment_hash = a.payment_hash and h.attempt_id = a.attempt_id
where h.channel_id = $1
  and upper(a.attempt_status) not in ('SUCCEEDED', 'SETTLED')
order by occurred_at desc
limit $2
`, int64(channelID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []lnChannelDetailFailure{}
	for rows.Next() {
		var (
			occurredAt    time.Time
			paymentHash   string
			failureCode   string
			failureSource int
			hopChannelID  int64
			amountMsat    int64
			feeMsat       int64
		)
		if err := rows.Scan(&occurredAt, &paymentHash, &failureCode, &failureSource, &hopChannelID, &amountMsat, &feeMsat); err != nil {
			return nil, err
		}
		shortID := ""
		alias := ""
		if hopChannelID > 0 {
			id := uint64(hopChannelID)
			shortID = formatShortChanID(id)
			alias = aliasByID[id]
		}
		detail := ""
		if failureSource >= 0 {
			detail = fmt.Sprintf("failure source hop %d", failureSource)
		}
		out = append(out, lnChannelDetailFailure{
			OccurredAt:        occurredAt.UTC().Format(time.RFC3339),
			Source:            "route_attempt",
			OutgoingChannelID: shortID,
			OutgoingAlias:     alias,
			AmountSat:         msatToSatCeil(amountMsat),
			PotentialFeeSat:   msatToSatCeil(feeMsat),
			FailureCode:       failureCode,
			FailureDetail:     detail,
			PaymentHash:       paymentHash,
		})
	}
	return out, rows.Err()
}

func (s *Server) loadLNChannelDetailHTLCManagerFailures(channelID uint64, aliasByID map[uint64]string, limit int) []lnChannelDetailFailure {
	svc, _ := s.htlcManagerService()
	if svc == nil {
		return nil
	}
	shortID := formatShortChanID(channelID)
	if shortID == "" {
		return nil
	}
	entries := svc.Failed(limit * 4)
	out := []lnChannelDetailFailure{}
	for _, entry := range entries {
		if entry.IncomingChannelID != shortID && entry.OutgoingChannelID != shortID {
			continue
		}
		amountMsat := entry.OutgoingAmtMsat
		if amountMsat == 0 {
			amountMsat = entry.IncomingAmtMsat
		}
		inAlias := strings.TrimSpace(entry.IncomingAlias)
		outAlias := strings.TrimSpace(entry.OutgoingAlias)
		if inAlias == "" {
			if id, ok := parseShortChannelID(entry.IncomingChannelID); ok {
				inAlias = aliasByID[id]
			}
		}
		if outAlias == "" {
			if id, ok := parseShortChannelID(entry.OutgoingChannelID); ok {
				outAlias = aliasByID[id]
			}
		}
		out = append(out, lnChannelDetailFailure{
			OccurredAt:        entry.Timestamp,
			Source:            "htlc_manager",
			IncomingChannelID: entry.IncomingChannelID,
			IncomingAlias:     inAlias,
			OutgoingChannelID: entry.OutgoingChannelID,
			OutgoingAlias:     outAlias,
			AmountSat:         msatToSatCeil(int64(amountMsat)),
			PotentialFeeSat:   msatToSatCeil(entry.PotentialFeeMsat),
			FailureCode:       strings.TrimSpace(entry.FailureCode),
			FailureDetail:     strings.TrimSpace(firstNonEmpty(entry.FailureDetail, entry.FailureReason, entry.Event)),
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (s *Server) loadLNChannelNote(ctx context.Context, channelPoint string) (string, error) {
	if s == nil || s.db == nil {
		return "", nil
	}
	var note string
	err := s.db.QueryRow(ctx, `
select note
from ln_channel_notes
where channel_point = $1
`, normalizeChannelPoint(channelPoint)).Scan(&note)
	return note, err
}

func (s *Server) loadLNChannelDetailCoverage(ctx context.Context, channelID uint64) (lnChannelDetailCoverage, error) {
	var out lnChannelDetailCoverage
	var notifSince, notifUntil *time.Time
	err := s.db.QueryRow(ctx, `
select min(occurred_at), max(occurred_at)
from notifications
where channel_id = $1
   or chan_id_in = $1
   or chan_id_out = $1
   or rebal_source_chan_id = $1
   or rebal_target_chan_id = $1
`, int64(channelID)).Scan(&notifSince, &notifUntil)
	if err != nil {
		return out, err
	}
	if notifSince != nil {
		out.NotificationsSince = notifSince.UTC().Format(time.RFC3339)
	}
	if notifUntil != nil {
		out.NotificationsUntil = notifUntil.UTC().Format(time.RFC3339)
	}
	var policySince, policyUntil *time.Time
	err = s.db.QueryRow(ctx, `
select min(captured_at), max(captured_at)
from graph_channel_policy_history
where chan_id = $1
`, int64(channelID)).Scan(&policySince, &policyUntil)
	if err != nil {
		return out, err
	}
	if policySince != nil {
		out.PolicySince = policySince.UTC().Format(time.RFC3339)
	}
	if policyUntil != nil {
		out.PolicyUntil = policyUntil.UTC().Format(time.RFC3339)
	}
	return out, nil
}
