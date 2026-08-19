package server

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"
)

const (
	lnChannelDBImpactBlocksPerDay = 144.0
	lnChannelDBImpactCriticalPct  = 15.0
	lnChannelDBImpactReviewPct    = 5.0
)

type lnChannelDBImpactResponse struct {
	Available          bool                    `json:"available"`
	DBBackend          string                  `json:"db_backend"`
	SizeAvailable      bool                    `json:"size_available"`
	ChannelDBSizeBytes *int64                  `json:"channel_db_size_bytes,omitempty"`
	ChannelDBSizeGB    *float64                `json:"channel_db_size_gb,omitempty"`
	TotalUpdates       uint64                  `json:"total_updates"`
	TotalChannels      int                     `json:"total_channels"`
	Top10Updates       uint64                  `json:"top10_updates"`
	Top10SharePct      float64                 `json:"top10_share_pct"`
	GeneratedAt        string                  `json:"generated_at"`
	EstimationNote     string                  `json:"estimation_note"`
	Message            string                  `json:"message,omitempty"`
	Channels           []lnChannelDBImpactItem `json:"channels"`
}

type lnChannelDBImpactItem struct {
	ChannelPoint         string   `json:"channel_point"`
	ChannelID            uint64   `json:"channel_id"`
	ChannelIDString      string   `json:"channel_id_str"`
	ShortChannelID       string   `json:"short_channel_id,omitempty"`
	RemotePubkey         string   `json:"remote_pubkey"`
	PeerAlias            string   `json:"peer_alias,omitempty"`
	Active               bool     `json:"active"`
	Private              bool     `json:"private"`
	CapacitySat          int64    `json:"capacity_sat"`
	LocalBalanceSat      int64    `json:"local_balance_sat"`
	RemoteBalanceSat     int64    `json:"remote_balance_sat"`
	OpenBlockHeight      int64    `json:"open_block_height,omitempty"`
	AgeDays              float64  `json:"age_days,omitempty"`
	NumUpdates           uint64   `json:"num_updates"`
	SharePct             float64  `json:"share_pct"`
	EstimatedDBBytes     *int64   `json:"estimated_db_bytes,omitempty"`
	EstimatedDBGB        *float64 `json:"estimated_db_gb,omitempty"`
	UpdatesPerDay        float64  `json:"updates_per_day,omitempty"`
	UpdatesPerMillionSat float64  `json:"updates_per_million_sat,omitempty"`
	Recommendation       string   `json:"recommendation"`
}

func (s *Server) handleLNChannelDBImpact(w http.ResponseWriter, r *http.Request) {
	backend := detectLNDDBBackend()
	if backend != "bolt" {
		resp := buildLNChannelDBImpactResponse(backend, nil, 0, nil, time.Now())
		resp.Message = "channel.db impact is only available for bolt channel databases"
		writeJSON(w, http.StatusOK, resp)
		return
	}

	var sizeBytes *int64
	if size, err := lndChannelDBSizeBytes(r.Context()); err == nil && size > 0 {
		sizeBytes = &size
	}

	ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
	defer cancel()

	currentBlockHeight := int64(0)
	if status, err := s.lnd.GetStatus(ctx); err == nil {
		currentBlockHeight = status.BlockHeight
	}

	channels, err := s.lnd.ListChannelUpdateStats(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}

	resp := buildLNChannelDBImpactResponse(backend, sizeBytes, currentBlockHeight, channels, time.Now())
	if sizeBytes == nil {
		resp.Message = "channel.db file size unavailable; estimated database size fields are omitted"
	}
	writeJSON(w, http.StatusOK, resp)
}

func buildLNChannelDBImpactResponse(dbBackend string, sizeBytes *int64, currentBlockHeight int64, channels []lndclient.ChannelInfo, now time.Time) lnChannelDBImpactResponse {
	dbBackend = strings.ToLower(strings.TrimSpace(dbBackend))
	if dbBackend == "" {
		dbBackend = "unknown"
	}

	resp := lnChannelDBImpactResponse{
		Available:      dbBackend == "bolt",
		DBBackend:      dbBackend,
		TotalChannels:  len(channels),
		GeneratedAt:    now.UTC().Format(time.RFC3339),
		EstimationNote: "Per-channel database impact is estimated from each channel's share of LND num_updates; BoltDB does not expose exact bytes per channel over RPC.",
		Channels:       make([]lnChannelDBImpactItem, 0, len(channels)),
	}

	if sizeBytes != nil && *sizeBytes > 0 {
		sizeGB := float64(*sizeBytes) / (1000.0 * 1000.0 * 1000.0)
		resp.SizeAvailable = true
		resp.ChannelDBSizeBytes = sizeBytes
		resp.ChannelDBSizeGB = &sizeGB
	}

	for _, ch := range channels {
		resp.TotalUpdates += ch.NumUpdates
	}

	for _, ch := range channels {
		item := lnChannelDBImpactItem{
			ChannelPoint:     strings.TrimSpace(ch.ChannelPoint),
			ChannelID:        ch.ChannelID,
			ChannelIDString:  strings.TrimSpace(ch.ChannelIDString),
			ShortChannelID:   formatShortChanID(ch.ChannelID),
			RemotePubkey:     strings.TrimSpace(ch.RemotePubkey),
			PeerAlias:        strings.TrimSpace(ch.PeerAlias),
			Active:           ch.Active,
			Private:          ch.Private,
			CapacitySat:      ch.CapacitySat,
			LocalBalanceSat:  ch.LocalBalanceSat,
			RemoteBalanceSat: ch.RemoteBalanceSat,
			NumUpdates:       ch.NumUpdates,
		}

		if currentBlockHeight > 0 {
			openBlock := int64(channelBlockHeight(ch.ChannelID))
			if openBlock > 0 {
				item.OpenBlockHeight = openBlock
				if currentBlockHeight >= openBlock {
					ageBlocks := currentBlockHeight - openBlock + 1
					item.AgeDays = float64(ageBlocks) / lnChannelDBImpactBlocksPerDay
					if item.AgeDays > 0 {
						item.UpdatesPerDay = float64(ch.NumUpdates) / item.AgeDays
					}
				}
			}
		}

		if resp.TotalUpdates > 0 {
			item.SharePct = float64(ch.NumUpdates) * 100 / float64(resp.TotalUpdates)
			if resp.SizeAvailable && resp.ChannelDBSizeBytes != nil {
				estimatedBytes := int64(float64(*resp.ChannelDBSizeBytes) * item.SharePct / 100)
				estimatedGB := float64(estimatedBytes) / (1000.0 * 1000.0 * 1000.0)
				item.EstimatedDBBytes = &estimatedBytes
				item.EstimatedDBGB = &estimatedGB
			}
		}

		if ch.CapacitySat > 0 {
			item.UpdatesPerMillionSat = float64(ch.NumUpdates) / (float64(ch.CapacitySat) / 1000000.0)
		}
		item.Recommendation = lnChannelDBImpactRecommendation(item.NumUpdates, item.SharePct, item.UpdatesPerDay)

		resp.Channels = append(resp.Channels, item)
	}

	sort.Slice(resp.Channels, func(i, j int) bool {
		if resp.Channels[i].NumUpdates == resp.Channels[j].NumUpdates {
			if resp.Channels[i].SharePct == resp.Channels[j].SharePct {
				return strings.ToLower(resp.Channels[i].PeerAlias) < strings.ToLower(resp.Channels[j].PeerAlias)
			}
			return resp.Channels[i].SharePct > resp.Channels[j].SharePct
		}
		return resp.Channels[i].NumUpdates > resp.Channels[j].NumUpdates
	})

	limit := len(resp.Channels)
	if limit > 10 {
		limit = 10
	}
	for i := 0; i < limit; i++ {
		resp.Top10Updates += resp.Channels[i].NumUpdates
	}
	if resp.TotalUpdates > 0 {
		resp.Top10SharePct = float64(resp.Top10Updates) * 100 / float64(resp.TotalUpdates)
	}

	return resp
}

func lnChannelDBImpactRecommendation(numUpdates uint64, sharePct float64, updatesPerDay float64) string {
	switch {
	case sharePct >= lnChannelDBImpactCriticalPct || numUpdates >= 50000000 || updatesPerDay >= 100000:
		return "critical"
	case sharePct >= lnChannelDBImpactReviewPct || numUpdates >= 10000000 || updatesPerDay >= 25000:
		return "review"
	default:
		return "monitor"
	}
}
