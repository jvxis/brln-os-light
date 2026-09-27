package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Native seed v2: the same public-graph reference as fetchNativeSeed, with the
// sampling fixed (issue #184, entrega 1B):
//
//   - the node's own advertised policy toward the peer is excluded (it was
//     counted as "market" and, on peers with few channels, dominated the sample);
//   - one sample per (channel, day): repeated announcements no longer add weight;
//   - a policy that did not change during the day is still in force and is
//     carried forward, so stable channels stay in the sample;
//   - disabled directions and channels already closed on that day are excluded;
//   - confidence is explicit (days, distinct channels, samples).
//
// The seed math after the series (p65/median blend/volatility/skew/p95 cap) is
// the same as the legacy path so the only difference measured is the sampling.
// v2 runs side by side with the legacy seed and is applied only when
// native_seed_v2_enabled is on.

const (
	autofeeNativeSeedV2MinDays     = autofeeNativeSeedMinDays
	autofeeNativeSeedV2MinSamples  = autofeeNativeSeedMinSamples
	autofeeNativeSeedV2MinChannels = 2
	// autofeeNativeSeedV2CarryDays: how far before the lookback window we read
	// history so a policy announced earlier and unchanged since is still known.
	autofeeNativeSeedV2CarryDays = 30
)

type nativeSeedV2Row struct {
	ChanID      int64
	Advertising string
	Connecting  string
	Ppm         int64
	CapacitySat int64
	Disabled    bool
	CapturedAt  time.Time
	ClosedAt    *time.Time
}

type autofeeSeedV2Stats struct {
	Days             int
	Samples          int
	Channels         int
	SelfExcluded     int
	DisabledExcluded int
	ClosedExcluded   int
	Deduped          int
}

type autofeeSeedV2Result struct {
	Seed           float64
	SeedP95        float64
	PeerMarketSkew float64
	Stats          autofeeSeedV2Stats
	Tags           []string
	Ok             bool
	Err            error
}

type nativeSeedV2PolicyKey struct {
	ChanID      int64
	Advertising string
}

// buildNativeSeedV2Series replays the policy history day by day inside
// [since, end) and returns one corrected-average sample per day for the inbound
// (what others charge to reach the peer) and outbound (what the peer charges)
// directions. rows may include history before `since`; it seeds the carry
// forward state. selfPubkey may be empty (no self exclusion possible).
func buildNativeSeedV2Series(rows []nativeSeedV2Row, peerPubkey string, selfPubkey string, since time.Time, end time.Time) ([]float64, []float64, autofeeSeedV2Stats) {
	stats := autofeeSeedV2Stats{}
	peerPubkey = strings.TrimSpace(peerPubkey)
	selfPubkey = strings.TrimSpace(selfPubkey)
	if peerPubkey == "" || len(rows) == 0 || !end.After(since) {
		return nil, nil, stats
	}
	ordered := append([]nativeSeedV2Row(nil), rows...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].CapturedAt.Before(ordered[j].CapturedAt) })

	state := make(map[nativeSeedV2PolicyKey]nativeSeedV2Row)
	seenChannels := make(map[int64]struct{})
	inboundVals := make([]float64, 0, 8)
	outboundVals := make([]float64, 0, 8)
	next := 0
	dayStart := since.UTC().Truncate(24 * time.Hour)
	endUTC := end.UTC()
	// Rows inside the window counted once per (channel, direction, day).
	windowRows := 0
	windowKeys := make(map[string]struct{})
	for _, row := range ordered {
		if !row.CapturedAt.Before(since) && row.CapturedAt.Before(endUTC) {
			windowRows++
			windowKeys[fmt.Sprintf("%d|%s|%s", row.ChanID, row.Advertising, row.CapturedAt.UTC().Format("2006-01-02"))] = struct{}{}
		}
	}
	stats.Deduped = windowRows - len(windowKeys)

	for !dayStart.After(endUTC) && dayStart.Before(endUTC) {
		dayEnd := dayStart.Add(24 * time.Hour)
		for next < len(ordered) && ordered[next].CapturedAt.Before(dayEnd) {
			row := ordered[next]
			state[nativeSeedV2PolicyKey{ChanID: row.ChanID, Advertising: row.Advertising}] = row
			next++
		}
		inbound := make([]graphExplorerPolicySample, 0, len(state))
		outbound := make([]graphExplorerPolicySample, 0, 4)
		for key, row := range state {
			if row.ClosedAt != nil && !row.ClosedAt.IsZero() && row.ClosedAt.Before(dayEnd) {
				stats.ClosedExcluded++
				continue
			}
			isOutbound := key.Advertising == peerPubkey
			isInbound := row.Connecting == peerPubkey && key.Advertising != peerPubkey
			if !isOutbound && !isInbound {
				continue
			}
			if isInbound && selfPubkey != "" && key.Advertising == selfPubkey {
				stats.SelfExcluded++
				continue
			}
			if row.Disabled {
				stats.DisabledExcluded++
				continue
			}
			sample := graphExplorerPolicySample{Ppm: row.Ppm, CapacitySat: row.CapacitySat}
			if isOutbound {
				outbound = append(outbound, sample)
				continue
			}
			inbound = append(inbound, sample)
			seenChannels[row.ChanID] = struct{}{}
		}
		if len(inbound) > 0 {
			summary := summarizeGraphExplorerPolicies(inbound)
			inboundVals = append(inboundVals, float64(summary.CorrectedAvgPpm))
			stats.Samples += len(inbound)
			stats.Days++
		}
		if len(outbound) > 0 {
			summary := summarizeGraphExplorerPolicies(outbound)
			outboundVals = append(outboundVals, float64(summary.CorrectedAvgPpm))
		}
		dayStart = dayEnd
	}
	stats.Channels = len(seenChannels)
	return inboundVals, outboundVals, stats
}

func nativeSeedV2Sufficient(inboundVals []float64, stats autofeeSeedV2Stats) bool {
	return len(inboundVals) >= autofeeNativeSeedV2MinDays &&
		stats.Samples >= autofeeNativeSeedV2MinSamples &&
		stats.Channels >= autofeeNativeSeedV2MinChannels
}

// computeNativeSeedV2FromSeries mirrors the legacy seed math exactly so v2 only
// changes the sampling. Returns ok=false when the series is empty.
func computeNativeSeedV2FromSeries(inboundVals []float64, outboundVals []float64, maxPpm int) (float64, float64, float64, []string, bool) {
	if len(inboundVals) == 0 {
		return 0, 0, 0, nil, false
	}
	p65 := percentile(append([]float64{}, inboundVals...), 0.65)
	p95 := percentile(append([]float64{}, inboundVals...), 0.95)
	if p95 <= 0 {
		return 0, 0, 0, nil, false
	}
	seed := p65
	peerMarketSkew := 0.0
	tags := []string{"seed:native-v2-corrected"}

	incMedian := percentile(append([]float64{}, inboundVals...), 0.50)
	incMean := averageFloat64(inboundVals)
	incStd := stddevFloat64(inboundVals, incMean)
	if incMedian > 0 {
		seed = (1.0-0.30)*seed + 0.30*incMedian
		tags = append(tags, "seed:native-v2-med")
	}
	if incMean > 0 && incStd > 0 {
		sigmaMu := incStd / incMean
		pen := math.Min(0.15, 0.25*sigmaMu)
		if pen > 0 {
			seed = seed * (1.0 - pen)
			tags = append(tags, fmt.Sprintf("seed:native-v2-vol-%d%%", int(math.Round(pen*100))))
		}
	}
	outMean := averageFloat64(outboundVals)
	if incMean > 0 && outMean > 0 {
		peerMarketSkew = outMean / incMean
		f := 1.0 + 0.20*(peerMarketSkew-1.0)
		if f < 0.80 {
			f = 0.80
		} else if f > 1.50 {
			f = 1.50
		}
		if math.Abs(f-1.0) > 0.001 {
			seed = seed * f
			tags = append(tags, fmt.Sprintf("seed:native-v2-ratiox%.2f", f))
		}
	}
	if seed > p95 {
		seed = p95
		tags = append(tags, "seed:native-v2-p95cap")
	}
	if maxPpm > 0 && seed > float64(maxPpm) {
		seed = float64(maxPpm)
		tags = append(tags, "seed:maxppm")
	}
	return seed, p95, peerMarketSkew, tags, true
}

func (e *autofeeEngine) fetchNativeSeedV2(pubkey string) autofeeSeedV2Result {
	pubkey = strings.TrimSpace(pubkey)
	if pubkey == "" {
		return autofeeSeedV2Result{}
	}
	if e.nativeSeedV2Cache == nil {
		e.nativeSeedV2Cache = make(map[string]autofeeSeedV2Result)
	}
	if cached, ok := e.nativeSeedV2Cache[pubkey]; ok {
		return cached
	}
	if e.svc == nil || e.svc.db == nil {
		result := autofeeSeedV2Result{Tags: []string{"seed:native-v2-error"}, Err: errors.New("db unavailable")}
		e.nativeSeedV2Cache[pubkey] = result
		return result
	}
	end := e.now.UTC().Truncate(24 * time.Hour)
	since := end.AddDate(0, 0, -maxInt(autofeeMinLookbackDays, e.cfg.LookbackDays))
	carrySince := since.AddDate(0, 0, -autofeeNativeSeedV2CarryDays)
	rows, err := e.svc.db.Query(context.Background(), `
select
  h.chan_id,
  h.advertising_pubkey,
  h.connecting_pubkey,
  coalesce(h.fee_rate_ppm, 0),
  greatest(ch.capacity_sat, 0),
  coalesce(h.disabled, false),
  h.captured_at,
  ch.closed_at
from graph_channel_policy_history h
join graph_channels ch on ch.chan_id = h.chan_id
where (h.advertising_pubkey = $1 or h.connecting_pubkey = $1)
  and h.captured_at >= $2
  and h.captured_at < $3
order by h.captured_at asc
`, pubkey, carrySince, end)
	if err != nil {
		result := autofeeSeedV2Result{Tags: []string{"seed:native-v2-error"}, Err: err}
		e.nativeSeedV2Cache[pubkey] = result
		return result
	}
	defer rows.Close()
	history := make([]nativeSeedV2Row, 0, 64)
	for rows.Next() {
		var row nativeSeedV2Row
		var closedAt *time.Time
		if err := rows.Scan(&row.ChanID, &row.Advertising, &row.Connecting, &row.Ppm, &row.CapacitySat, &row.Disabled, &row.CapturedAt, &closedAt); err != nil {
			result := autofeeSeedV2Result{Tags: []string{"seed:native-v2-error"}, Err: err}
			e.nativeSeedV2Cache[pubkey] = result
			return result
		}
		row.CapturedAt = row.CapturedAt.UTC()
		if closedAt != nil && !closedAt.IsZero() {
			utc := closedAt.UTC()
			row.ClosedAt = &utc
		}
		history = append(history, row)
	}
	if err := rows.Err(); err != nil {
		result := autofeeSeedV2Result{Tags: []string{"seed:native-v2-error"}, Err: err}
		e.nativeSeedV2Cache[pubkey] = result
		return result
	}
	result := buildNativeSeedV2Result(history, pubkey, e.selfPubkey, since, end, e.cfg.MaxPpm)
	e.nativeSeedV2Cache[pubkey] = result
	return result
}

func buildNativeSeedV2Result(history []nativeSeedV2Row, peerPubkey string, selfPubkey string, since time.Time, end time.Time, maxPpm int) autofeeSeedV2Result {
	inboundVals, outboundVals, stats := buildNativeSeedV2Series(history, peerPubkey, selfPubkey, since, end)
	result := autofeeSeedV2Result{Stats: stats}
	if !nativeSeedV2Sufficient(inboundVals, stats) {
		result.Tags = []string{"seed:native-v2-insufficient"}
		return result
	}
	seed, p95, skew, tags, ok := computeNativeSeedV2FromSeries(inboundVals, outboundVals, maxPpm)
	if !ok {
		result.Tags = []string{"seed:native-v2-empty"}
		return result
	}
	result.Seed = seed
	result.SeedP95 = p95
	result.PeerMarketSkew = skew
	result.Ok = true
	result.Tags = append(tags, "seed:native-v2")
	return result
}

// nativeSeedV2DeltaPct compares the v2 seed with the seed actually used.
func nativeSeedV2DeltaPct(applied float64, v2 float64) float64 {
	if applied <= 0 || v2 <= 0 {
		return 0
	}
	return math.Round((v2/applied-1)*1000) / 10
}
