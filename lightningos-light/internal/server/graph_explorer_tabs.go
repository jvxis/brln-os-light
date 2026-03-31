package server

import (
	"context"
	"sort"
	"strings"
	"time"
)

type GraphExplorerChannelPolicy struct {
	FeeBaseMsat       int64      `json:"fee_base_msat"`
	FeeRatePpm        int64      `json:"fee_rate_ppm"`
	InboundBaseMsat   int64      `json:"inbound_base_msat"`
	InboundFeeRatePpm int64      `json:"inbound_fee_rate_ppm"`
	Disabled          bool       `json:"disabled"`
	LastUpdateAt      *time.Time `json:"last_update_at,omitempty"`
}

type GraphExplorerNodeChannelsResponse struct {
	CoverageSince *time.Time                 `json:"coverage_since,omitempty"`
	Items         []GraphExplorerNodeChannel `json:"items"`
}

type GraphExplorerNodeChannel struct {
	ChannelID           uint64                     `json:"channel_id"`
	ChanPoint           string                     `json:"chan_point,omitempty"`
	PeerPubKey          string                     `json:"peer_pubkey"`
	PeerAlias           string                     `json:"peer_alias,omitempty"`
	HasLocalOpenChannel bool                       `json:"has_local_open_channel"`
	CapacitySat         int64                      `json:"capacity_sat"`
	OpenBlockHeight     int                        `json:"open_block_height"`
	TargetPolicy        GraphExplorerChannelPolicy `json:"target_policy"`
	PeerPolicy          GraphExplorerChannelPolicy `json:"peer_policy"`
	LastPolicyUpdate    *time.Time                 `json:"last_policy_update,omitempty"`
}

type GraphExplorerNodeClosedChannelsResponse struct {
	CoverageSince *time.Time                        `json:"coverage_since,omitempty"`
	Range         string                            `json:"range"`
	Summary       GraphExplorerClosedChannelsReport `json:"summary"`
	Items         []GraphExplorerClosedChannel      `json:"items"`
}

type GraphExplorerClosedChannelsReport struct {
	TotalClosedChannels int   `json:"total_closed_channels"`
	TotalCapacitySat    int64 `json:"total_capacity_sat"`
	KnownTypeCount      int   `json:"known_type_count"`
	UnknownTypeCount    int   `json:"unknown_type_count"`
}

type GraphExplorerClosedChannel struct {
	ChannelID    uint64     `json:"channel_id"`
	ChanPoint    string     `json:"chan_point,omitempty"`
	PeerPubKey   string     `json:"peer_pubkey,omitempty"`
	PeerAlias    string     `json:"peer_alias,omitempty"`
	CapacitySat  int64      `json:"capacity_sat"`
	ClosedHeight int        `json:"closed_height"`
	ObservedAt   *time.Time `json:"observed_at,omitempty"`
	CloseType    string     `json:"close_type,omitempty"`
	CloseSource  string     `json:"close_source,omitempty"`
}

type GraphExplorerNodeFeeReport struct {
	CoverageSince *time.Time                     `json:"coverage_since,omitempty"`
	Range         string                         `json:"range"`
	Outbound      GraphExplorerFeeSummary        `json:"outbound"`
	Inbound       GraphExplorerFeeSummary        `json:"inbound"`
	OutboundBins  []GraphExplorerFeeDistribution `json:"outbound_bins"`
	InboundBins   []GraphExplorerFeeDistribution `json:"inbound_bins"`
	History       []GraphExplorerFeeHistoryPoint `json:"history"`
}

type GraphExplorerFeeSummary struct {
	ChannelCount       int        `json:"channel_count"`
	DisabledCount      int        `json:"disabled_count"`
	MinPpm             int64      `json:"min_ppm"`
	MaxPpm             int64      `json:"max_ppm"`
	AvgPpm             int64      `json:"avg_ppm"`
	MedianPpm          int64      `json:"median_ppm"`
	WeightedAvgPpm     int64      `json:"weighted_avg_ppm"`
	TotalCapacitySat   int64      `json:"total_capacity_sat"`
	LastPolicyUpdateAt *time.Time `json:"last_policy_update_at,omitempty"`
}

type GraphExplorerFeeDistribution struct {
	Label           string `json:"label"`
	MinPpmInclusive int64  `json:"min_ppm_inclusive"`
	MaxPpmInclusive int64  `json:"max_ppm_inclusive"`
	ChannelCount    int    `json:"channel_count"`
	CapacitySat     int64  `json:"capacity_sat"`
}

type GraphExplorerFeeHistoryPoint struct {
	Day                    string `json:"day"`
	OutboundAvgPpm         int64  `json:"outbound_avg_ppm"`
	OutboundWeightedAvgPpm int64  `json:"outbound_weighted_avg_ppm"`
	OutboundSampleCount    int    `json:"outbound_sample_count"`
	InboundAvgPpm          int64  `json:"inbound_avg_ppm"`
	InboundWeightedAvgPpm  int64  `json:"inbound_weighted_avg_ppm"`
	InboundSampleCount     int    `json:"inbound_sample_count"`
}

type graphExplorerPolicySample struct {
	Ppm          int64
	CapacitySat  int64
	Disabled     bool
	LastUpdateAt *time.Time
}

type graphExplorerRangeSpec struct {
	name  string
	since *time.Time
}

func (s *GraphExplorerService) ListNodeChannels(ctx context.Context, pubkey string, limit int) (GraphExplorerNodeChannelsResponse, error) {
	if s == nil || s.db == nil {
		return GraphExplorerNodeChannelsResponse{}, ErrGraphExplorerDBUnavailable
	}
	pubkey = strings.TrimSpace(pubkey)
	if err := s.ensureNodeExists(ctx, pubkey); err != nil {
		return GraphExplorerNodeChannelsResponse{}, err
	}

	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}

	coverageSince, err := s.coverageSince(ctx)
	if err != nil {
		return GraphExplorerNodeChannelsResponse{}, err
	}
	localOpenPeers := s.loadLocalOpenPeerSet(ctx)

	rows, err := s.db.Query(ctx, `
select
  ch.chan_id,
  coalesce(ch.chan_point, ''),
  case when ch.node1_pubkey = $1 then coalesce(ch.node2_pubkey, '') else coalesce(ch.node1_pubkey, '') end as peer_pubkey,
  coalesce(peer.alias, ''),
  ch.capacity_sat,
  ch.open_block_height,
  coalesce(target.fee_base_msat, 0),
  coalesce(target.fee_rate_ppm, 0),
  coalesce(target.inbound_base_msat, 0),
  coalesce(target.inbound_fee_rate_ppm, 0),
  coalesce(target.disabled, false),
  target.policy_last_update_at,
  coalesce(peer_policy.fee_base_msat, 0),
  coalesce(peer_policy.fee_rate_ppm, 0),
  coalesce(peer_policy.inbound_base_msat, 0),
  coalesce(peer_policy.inbound_fee_rate_ppm, 0),
  coalesce(peer_policy.disabled, false),
  peer_policy.policy_last_update_at
from graph_channels ch
left join graph_nodes peer on peer.pubkey = case when ch.node1_pubkey = $1 then ch.node2_pubkey else ch.node1_pubkey end
left join graph_channel_policy_current target on target.chan_id = ch.chan_id and target.advertising_pubkey = $1
left join graph_channel_policy_current peer_policy on peer_policy.chan_id = ch.chan_id and peer_policy.advertising_pubkey = case when ch.node1_pubkey = $1 then ch.node2_pubkey else ch.node1_pubkey end
where ch.status = 'open'
  and (ch.node1_pubkey = $1 or ch.node2_pubkey = $1)
order by ch.capacity_sat desc, ch.chan_id desc
limit $2
`, pubkey, limit)
	if err != nil {
		return GraphExplorerNodeChannelsResponse{}, err
	}
	defer rows.Close()

	items := make([]GraphExplorerNodeChannel, 0, limit)
	for rows.Next() {
		var item GraphExplorerNodeChannel
		var channelID int64
		if err := rows.Scan(
			&channelID,
			&item.ChanPoint,
			&item.PeerPubKey,
			&item.PeerAlias,
			&item.CapacitySat,
			&item.OpenBlockHeight,
			&item.TargetPolicy.FeeBaseMsat,
			&item.TargetPolicy.FeeRatePpm,
			&item.TargetPolicy.InboundBaseMsat,
			&item.TargetPolicy.InboundFeeRatePpm,
			&item.TargetPolicy.Disabled,
			&item.TargetPolicy.LastUpdateAt,
			&item.PeerPolicy.FeeBaseMsat,
			&item.PeerPolicy.FeeRatePpm,
			&item.PeerPolicy.InboundBaseMsat,
			&item.PeerPolicy.InboundFeeRatePpm,
			&item.PeerPolicy.Disabled,
			&item.PeerPolicy.LastUpdateAt,
		); err != nil {
			return GraphExplorerNodeChannelsResponse{}, err
		}
		item.ChannelID = uint64(channelID)
		item.ChanPoint = strings.TrimSpace(item.ChanPoint)
		item.PeerPubKey = strings.TrimSpace(item.PeerPubKey)
		item.PeerAlias = strings.TrimSpace(item.PeerAlias)
		item.HasLocalOpenChannel = graphExplorerHasLocalOpenChannel(localOpenPeers, item.PeerPubKey)
		item.LastPolicyUpdate = latestGraphExplorerTime(item.TargetPolicy.LastUpdateAt, item.PeerPolicy.LastUpdateAt)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return GraphExplorerNodeChannelsResponse{}, err
	}

	return GraphExplorerNodeChannelsResponse{
		CoverageSince: coverageSince,
		Items:         items,
	}, nil
}

func (s *GraphExplorerService) ListNodeClosedChannels(ctx context.Context, pubkey, rangeName string, limit int) (GraphExplorerNodeClosedChannelsResponse, error) {
	if s == nil || s.db == nil {
		return GraphExplorerNodeClosedChannelsResponse{}, ErrGraphExplorerDBUnavailable
	}
	pubkey = strings.TrimSpace(pubkey)
	if err := s.ensureNodeExists(ctx, pubkey); err != nil {
		return GraphExplorerNodeClosedChannelsResponse{}, err
	}

	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}

	coverageSince, err := s.coverageSince(ctx)
	if err != nil {
		return GraphExplorerNodeClosedChannelsResponse{}, err
	}
	rangeSpec := graphExplorerResolveRange(rangeName, "90d", map[string]func(now time.Time) time.Time{
		"30d": func(now time.Time) time.Time { return now.AddDate(0, 0, -30) },
		"90d": func(now time.Time) time.Time { return now.AddDate(0, 0, -90) },
		"1y":  func(now time.Time) time.Time { return now.AddDate(-1, 0, 0) },
		"all": nil,
	})

	rows, err := s.db.Query(ctx, `
select
  e.chan_id,
  coalesce(e.chan_point, ''),
  case when e.node1_pubkey = $1 then coalesce(e.node2_pubkey, '') else coalesce(e.node1_pubkey, '') end as peer_pubkey,
  coalesce(peer.alias, ''),
  e.capacity_sat,
  e.closed_height,
  e.observed_at,
  coalesce(nullif(e.close_type, ''), nullif(ch.close_type, ''), 'unknown') as close_type,
  coalesce(nullif(e.close_source, ''), nullif(ch.close_source, ''), 'native') as close_source
from graph_close_events e
left join graph_channels ch on ch.chan_id = e.chan_id
left join graph_nodes peer on peer.pubkey = case when e.node1_pubkey = $1 then e.node2_pubkey else e.node1_pubkey end
where (e.node1_pubkey = $1 or e.node2_pubkey = $1)
  and ($2::timestamptz is null or e.observed_at >= $2)
order by e.observed_at desc, e.chan_id desc
limit $3
`, pubkey, rangeSpec.since, limit)
	if err != nil {
		return GraphExplorerNodeClosedChannelsResponse{}, err
	}
	defer rows.Close()

	items := make([]GraphExplorerClosedChannel, 0, limit)
	summary := GraphExplorerClosedChannelsReport{}
	for rows.Next() {
		var item GraphExplorerClosedChannel
		var channelID int64
		if err := rows.Scan(
			&channelID,
			&item.ChanPoint,
			&item.PeerPubKey,
			&item.PeerAlias,
			&item.CapacitySat,
			&item.ClosedHeight,
			&item.ObservedAt,
			&item.CloseType,
			&item.CloseSource,
		); err != nil {
			return GraphExplorerNodeClosedChannelsResponse{}, err
		}
		item.ChannelID = uint64(channelID)
		item.ChanPoint = strings.TrimSpace(item.ChanPoint)
		item.PeerPubKey = strings.TrimSpace(item.PeerPubKey)
		item.PeerAlias = strings.TrimSpace(item.PeerAlias)
		item.CloseType = normalizeGraphExplorerCloseType(item.CloseType)
		item.CloseSource = strings.TrimSpace(item.CloseSource)
		items = append(items, item)

		summary.TotalClosedChannels++
		summary.TotalCapacitySat += item.CapacitySat
		if item.CloseType == "unknown" {
			summary.UnknownTypeCount++
		} else {
			summary.KnownTypeCount++
		}
	}
	if err := rows.Err(); err != nil {
		return GraphExplorerNodeClosedChannelsResponse{}, err
	}

	return GraphExplorerNodeClosedChannelsResponse{
		CoverageSince: coverageSince,
		Range:         rangeSpec.name,
		Summary:       summary,
		Items:         items,
	}, nil
}

func (s *GraphExplorerService) GetNodeFeeReport(ctx context.Context, pubkey, rangeName string) (GraphExplorerNodeFeeReport, error) {
	if s == nil || s.db == nil {
		return GraphExplorerNodeFeeReport{}, ErrGraphExplorerDBUnavailable
	}
	pubkey = strings.TrimSpace(pubkey)
	if err := s.ensureNodeExists(ctx, pubkey); err != nil {
		return GraphExplorerNodeFeeReport{}, err
	}

	coverageSince, err := s.coverageSince(ctx)
	if err != nil {
		return GraphExplorerNodeFeeReport{}, err
	}
	rangeSpec := graphExplorerResolveRange(rangeName, "30d", map[string]func(now time.Time) time.Time{
		"7d":  func(now time.Time) time.Time { return now.AddDate(0, 0, -7) },
		"30d": func(now time.Time) time.Time { return now.AddDate(0, 0, -30) },
		"90d": func(now time.Time) time.Time { return now.AddDate(0, 0, -90) },
		"1y":  func(now time.Time) time.Time { return now.AddDate(-1, 0, 0) },
		"all": nil,
	})

	currentRows, err := s.db.Query(ctx, `
select
  ch.capacity_sat,
  coalesce(target.fee_rate_ppm, 0),
  coalesce(target.disabled, false),
  target.policy_last_update_at,
  coalesce(peer_policy.fee_rate_ppm, 0),
  coalesce(peer_policy.disabled, false),
  peer_policy.policy_last_update_at
from graph_channels ch
left join graph_channel_policy_current target on target.chan_id = ch.chan_id and target.advertising_pubkey = $1
left join graph_channel_policy_current peer_policy on peer_policy.chan_id = ch.chan_id and peer_policy.advertising_pubkey = case when ch.node1_pubkey = $1 then ch.node2_pubkey else ch.node1_pubkey end
where ch.status = 'open'
  and (ch.node1_pubkey = $1 or ch.node2_pubkey = $1)
`, pubkey)
	if err != nil {
		return GraphExplorerNodeFeeReport{}, err
	}
	defer currentRows.Close()

	outboundSamples := make([]graphExplorerPolicySample, 0)
	inboundSamples := make([]graphExplorerPolicySample, 0)
	for currentRows.Next() {
		var capacitySat int64
		var outboundPpm int64
		var outboundDisabled bool
		var outboundUpdatedAt *time.Time
		var inboundPpm int64
		var inboundDisabled bool
		var inboundUpdatedAt *time.Time
		if err := currentRows.Scan(
			&capacitySat,
			&outboundPpm,
			&outboundDisabled,
			&outboundUpdatedAt,
			&inboundPpm,
			&inboundDisabled,
			&inboundUpdatedAt,
		); err != nil {
			return GraphExplorerNodeFeeReport{}, err
		}
		if outboundUpdatedAt != nil {
			outboundSamples = append(outboundSamples, graphExplorerPolicySample{
				Ppm:          outboundPpm,
				CapacitySat:  capacitySat,
				Disabled:     outboundDisabled,
				LastUpdateAt: outboundUpdatedAt,
			})
		}
		if inboundUpdatedAt != nil {
			inboundSamples = append(inboundSamples, graphExplorerPolicySample{
				Ppm:          inboundPpm,
				CapacitySat:  capacitySat,
				Disabled:     inboundDisabled,
				LastUpdateAt: inboundUpdatedAt,
			})
		}
	}
	if err := currentRows.Err(); err != nil {
		return GraphExplorerNodeFeeReport{}, err
	}

	historyRows, err := s.db.Query(ctx, `
select
  to_char(date_trunc('day', h.captured_at at time zone 'UTC'), 'YYYY-MM-DD') as day,
  coalesce(round(avg(case when h.advertising_pubkey = $1 then h.fee_rate_ppm::numeric end)), 0)::bigint as outbound_avg_ppm,
  coalesce(round(
    sum(case when h.advertising_pubkey = $1 then h.fee_rate_ppm::numeric * greatest(ch.capacity_sat, 0)::numeric else 0 end)
    / nullif(sum(case when h.advertising_pubkey = $1 then greatest(ch.capacity_sat, 0)::numeric else 0 end), 0)
  ), 0)::bigint as outbound_weighted_avg_ppm,
  count(*) filter (where h.advertising_pubkey = $1) as outbound_sample_count,
  coalesce(round(avg(case when h.connecting_pubkey = $1 and h.advertising_pubkey <> $1 then h.fee_rate_ppm::numeric end)), 0)::bigint as inbound_avg_ppm,
  coalesce(round(
    sum(case when h.connecting_pubkey = $1 and h.advertising_pubkey <> $1 then h.fee_rate_ppm::numeric * greatest(ch.capacity_sat, 0)::numeric else 0 end)
    / nullif(sum(case when h.connecting_pubkey = $1 and h.advertising_pubkey <> $1 then greatest(ch.capacity_sat, 0)::numeric else 0 end), 0)
  ), 0)::bigint as inbound_weighted_avg_ppm,
  count(*) filter (where h.connecting_pubkey = $1 and h.advertising_pubkey <> $1) as inbound_sample_count
from graph_channel_policy_history h
join graph_channels ch on ch.chan_id = h.chan_id
where (h.advertising_pubkey = $1 or h.connecting_pubkey = $1)
  and ($2::timestamptz is null or h.captured_at >= $2)
group by 1
order by 1 desc
limit 90
`, pubkey, rangeSpec.since)
	if err != nil {
		return GraphExplorerNodeFeeReport{}, err
	}
	defer historyRows.Close()

	history := make([]GraphExplorerFeeHistoryPoint, 0, 90)
	for historyRows.Next() {
		var item GraphExplorerFeeHistoryPoint
		if err := historyRows.Scan(
			&item.Day,
			&item.OutboundAvgPpm,
			&item.OutboundWeightedAvgPpm,
			&item.OutboundSampleCount,
			&item.InboundAvgPpm,
			&item.InboundWeightedAvgPpm,
			&item.InboundSampleCount,
		); err != nil {
			return GraphExplorerNodeFeeReport{}, err
		}
		item.Day = strings.TrimSpace(item.Day)
		history = append(history, item)
	}
	if err := historyRows.Err(); err != nil {
		return GraphExplorerNodeFeeReport{}, err
	}

	return GraphExplorerNodeFeeReport{
		CoverageSince: coverageSince,
		Range:         rangeSpec.name,
		Outbound:      summarizeGraphExplorerPolicies(outboundSamples),
		Inbound:       summarizeGraphExplorerPolicies(inboundSamples),
		OutboundBins:  graphExplorerDistributePolicies(outboundSamples),
		InboundBins:   graphExplorerDistributePolicies(inboundSamples),
		History:       history,
	}, nil
}

func (s *GraphExplorerService) ensureNodeExists(ctx context.Context, pubkey string) error {
	pubkey = strings.TrimSpace(pubkey)
	if pubkey == "" {
		return ErrGraphExplorerNodeNotFound
	}
	var exists bool
	err := s.db.QueryRow(ctx, `
select exists(select 1 from graph_nodes where pubkey = $1)
`, pubkey).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return ErrGraphExplorerNodeNotFound
	}
	return nil
}

func graphExplorerResolveRange(raw, fallback string, spec map[string]func(now time.Time) time.Time) graphExplorerRangeSpec {
	name := strings.TrimSpace(strings.ToLower(raw))
	if _, ok := spec[name]; !ok {
		name = fallback
	}
	rangeFn := spec[name]
	if rangeFn == nil {
		return graphExplorerRangeSpec{name: name}
	}
	since := rangeFn(time.Now().UTC())
	return graphExplorerRangeSpec{name: name, since: &since}
}

func latestGraphExplorerTime(left, right *time.Time) *time.Time {
	switch {
	case left == nil:
		return right
	case right == nil:
		return left
	case right.After(*left):
		return right
	default:
		return left
	}
}

func normalizeGraphExplorerCloseType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	return value
}

func summarizeGraphExplorerPolicies(samples []graphExplorerPolicySample) GraphExplorerFeeSummary {
	if len(samples) == 0 {
		return GraphExplorerFeeSummary{}
	}

	values := make([]int64, 0, len(samples))
	var totalPpm int64
	var totalCapacity int64
	var weightedNumerator int64
	var minPpm int64
	var maxPpm int64
	disabledCount := 0
	var lastUpdateAt *time.Time

	for index, sample := range samples {
		values = append(values, sample.Ppm)
		totalPpm += sample.Ppm
		totalCapacity += sample.CapacitySat
		weightedNumerator += sample.Ppm * sample.CapacitySat
		if index == 0 || sample.Ppm < minPpm {
			minPpm = sample.Ppm
		}
		if index == 0 || sample.Ppm > maxPpm {
			maxPpm = sample.Ppm
		}
		if sample.Disabled {
			disabledCount++
		}
		lastUpdateAt = latestGraphExplorerTime(lastUpdateAt, sample.LastUpdateAt)
	}

	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	median := values[len(values)/2]
	if len(values)%2 == 0 {
		median = (values[len(values)/2-1] + values[len(values)/2]) / 2
	}

	weightedAvg := int64(0)
	if totalCapacity > 0 {
		weightedAvg = weightedNumerator / totalCapacity
	}

	return GraphExplorerFeeSummary{
		ChannelCount:       len(samples),
		DisabledCount:      disabledCount,
		MinPpm:             minPpm,
		MaxPpm:             maxPpm,
		AvgPpm:             totalPpm / int64(len(samples)),
		MedianPpm:          median,
		WeightedAvgPpm:     weightedAvg,
		TotalCapacitySat:   totalCapacity,
		LastPolicyUpdateAt: lastUpdateAt,
	}
}

func graphExplorerDistributePolicies(samples []graphExplorerPolicySample) []GraphExplorerFeeDistribution {
	bins := []GraphExplorerFeeDistribution{
		{Label: "0", MinPpmInclusive: 0, MaxPpmInclusive: 0},
		{Label: "1-25", MinPpmInclusive: 1, MaxPpmInclusive: 25},
		{Label: "26-100", MinPpmInclusive: 26, MaxPpmInclusive: 100},
		{Label: "101-250", MinPpmInclusive: 101, MaxPpmInclusive: 250},
		{Label: "251-500", MinPpmInclusive: 251, MaxPpmInclusive: 500},
		{Label: "501-1000", MinPpmInclusive: 501, MaxPpmInclusive: 1000},
		{Label: "1001-2500", MinPpmInclusive: 1001, MaxPpmInclusive: 2500},
		{Label: "2501-5000", MinPpmInclusive: 2501, MaxPpmInclusive: 5000},
		{Label: "5001+", MinPpmInclusive: 5001, MaxPpmInclusive: -1},
	}
	for _, sample := range samples {
		for index := range bins {
			maxAllowed := bins[index].MaxPpmInclusive
			if sample.Ppm < bins[index].MinPpmInclusive {
				continue
			}
			if maxAllowed >= 0 && sample.Ppm > maxAllowed {
				continue
			}
			bins[index].ChannelCount++
			bins[index].CapacitySat += sample.CapacitySat
			break
		}
	}
	return bins
}
