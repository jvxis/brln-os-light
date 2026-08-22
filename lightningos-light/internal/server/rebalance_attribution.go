package server

import (
	"context"
	"sort"
	"time"
)

// rebalanceAttributionLot represents paid liquidity made available by one
// completed rebalance job. Forwards consume these lots FIFO per target.
type rebalanceAttributionLot struct {
	JobID           int64
	TargetChannelID uint64
	Status          string
	Reason          string
	TriggerReason   string
	CompletedAt     time.Time
	SentSat         int64
	FeePaidSat      int64
	ExplorationSlot bool
}

type rebalanceAttributionForward struct {
	ID              int64
	TargetChannelID uint64
	OccurredAt      time.Time
	AmountSat       int64
	FeeMsat         int64
}

type rebalanceAttributionResult struct {
	ForwardAmountSat int64
	ForwardFeeMsat   int64
}

// attributeRebalanceForwardsFIFO consumes forward volume from the oldest
// eligible paid-liquidity lot of the same target. A forward may span multiple
// lots, but every sat and its proportional fee are attributed at most once for
// a given measurement window.
func attributeRebalanceForwardsFIFO(lots []rebalanceAttributionLot, forwards []rebalanceAttributionForward, window time.Duration) map[int64]rebalanceAttributionResult {
	result := make(map[int64]rebalanceAttributionResult, len(lots))
	if window <= 0 || len(lots) == 0 || len(forwards) == 0 {
		return result
	}

	lotsByTarget := make(map[uint64][]rebalanceAttributionLot)
	for _, lot := range lots {
		if lot.JobID <= 0 || lot.TargetChannelID == 0 || lot.CompletedAt.IsZero() || lot.SentSat <= 0 {
			continue
		}
		lotsByTarget[lot.TargetChannelID] = append(lotsByTarget[lot.TargetChannelID], lot)
	}
	for targetID := range lotsByTarget {
		sort.SliceStable(lotsByTarget[targetID], func(i, j int) bool {
			left := lotsByTarget[targetID][i]
			right := lotsByTarget[targetID][j]
			if !left.CompletedAt.Equal(right.CompletedAt) {
				return left.CompletedAt.Before(right.CompletedAt)
			}
			return left.JobID < right.JobID
		})
	}

	forwardsByTarget := make(map[uint64][]rebalanceAttributionForward)
	for _, forward := range forwards {
		if forward.TargetChannelID == 0 || forward.OccurredAt.IsZero() || forward.AmountSat <= 0 {
			continue
		}
		forwardsByTarget[forward.TargetChannelID] = append(forwardsByTarget[forward.TargetChannelID], forward)
	}
	for targetID := range forwardsByTarget {
		sort.SliceStable(forwardsByTarget[targetID], func(i, j int) bool {
			left := forwardsByTarget[targetID][i]
			right := forwardsByTarget[targetID][j]
			if !left.OccurredAt.Equal(right.OccurredAt) {
				return left.OccurredAt.Before(right.OccurredAt)
			}
			return left.ID < right.ID
		})
	}

	for targetID, targetLots := range lotsByTarget {
		targetForwards := forwardsByTarget[targetID]
		if len(targetForwards) == 0 {
			continue
		}
		remaining := make([]int64, len(targetLots))
		for i := range targetLots {
			remaining[i] = targetLots[i].SentSat
		}
		lotIndex := 0

		for _, forward := range targetForwards {
			unassignedSat := forward.AmountSat
			assignedFromEventSat := int64(0)
			for unassignedSat > 0 && lotIndex < len(targetLots) {
				lot := targetLots[lotIndex]
				if remaining[lotIndex] <= 0 || !forward.OccurredAt.Before(lot.CompletedAt.Add(window)) {
					lotIndex++
					continue
				}
				if forward.OccurredAt.Before(lot.CompletedAt) {
					break
				}

				attributedSat := unassignedSat
				if attributedSat > remaining[lotIndex] {
					attributedSat = remaining[lotIndex]
				}
				if attributedSat <= 0 {
					continue
				}

				feeShareMsat := int64(0)
				if forward.FeeMsat > 0 {
					// Difference of cumulative floors preserves the proportional
					// fee when one event is split across multiple lots.
					before := (forward.FeeMsat * assignedFromEventSat) / forward.AmountSat
					after := (forward.FeeMsat * (assignedFromEventSat + attributedSat)) / forward.AmountSat
					feeShareMsat = after - before
				}

				current := result[lot.JobID]
				current.ForwardAmountSat += attributedSat
				current.ForwardFeeMsat += feeShareMsat
				result[lot.JobID] = current
				remaining[lotIndex] -= attributedSat
				unassignedSat -= attributedSat
				assignedFromEventSat += attributedSat
				if remaining[lotIndex] == 0 {
					lotIndex++
				}
			}
		}
	}

	return result
}

// loadRebalanceAttributionInputs loads all completed jobs in the context
// horizon, not only the jobs that will be reported. Older context lots must be
// allowed to consume forwards first or a reporting-window boundary would move
// their revenue to newer jobs.
func (s *RebalanceService) loadRebalanceAttributionInputs(ctx context.Context, since time.Time, targetIDs []uint64) ([]rebalanceAttributionLot, []rebalanceAttributionForward, error) {
	if s.db == nil {
		return nil, nil, nil
	}

	ids := make([]int64, 0, len(targetIDs))
	seenTargets := make(map[uint64]struct{}, len(targetIDs))
	for _, targetID := range targetIDs {
		if targetID == 0 {
			continue
		}
		if _, ok := seenTargets[targetID]; ok {
			continue
		}
		seenTargets[targetID] = struct{}{}
		ids = append(ids, int64(targetID))
	}

	rows, err := s.db.Query(ctx, `
select
  j.id,
  j.target_channel_id,
  j.status,
  coalesce(j.reason, ''),
  coalesce(j.trigger_reason, ''),
  j.completed_at,
  coalesce(sum(a.amount_sat) filter (where a.status='succeeded'), 0) as sent_sat,
  coalesce(sum(a.fee_paid_sat) filter (where a.status='succeeded'), 0) as fee_paid_sat,
  j.exploration_slot
from rebalance_jobs j
left join rebalance_attempts a on a.job_id = j.id
where j.completed_at is not null
  and j.completed_at >= $1
  and j.status in ('succeeded','partial','failed')
  and (cardinality($2::bigint[]) = 0 or j.target_channel_id = any($2::bigint[]))
group by j.id, j.target_channel_id, j.status, j.reason, j.trigger_reason, j.completed_at, j.exploration_slot
order by j.completed_at, j.id
`, since, ids)
	if err != nil {
		return nil, nil, err
	}

	lots := make([]rebalanceAttributionLot, 0)
	for rows.Next() {
		var lot rebalanceAttributionLot
		var targetID int64
		if err := rows.Scan(
			&lot.JobID,
			&targetID,
			&lot.Status,
			&lot.Reason,
			&lot.TriggerReason,
			&lot.CompletedAt,
			&lot.SentSat,
			&lot.FeePaidSat,
			&lot.ExplorationSlot,
		); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if targetID <= 0 {
			continue
		}
		lot.TargetChannelID = uint64(targetID)
		lots = append(lots, lot)
		seenTargets[lot.TargetChannelID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()
	if len(lots) == 0 {
		return lots, nil, nil
	}

	ids = ids[:0]
	for targetID := range seenTargets {
		ids = append(ids, int64(targetID))
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	forwardRows, err := s.db.Query(ctx, `
select
  id,
  channel_id,
  occurred_at,
  amount_sat,
  case when fee_msat > 0 then fee_msat else fee_sat * 1000 end as fee_msat
from notifications
where type='forward'
  and occurred_at >= $1
  and occurred_at <= now()
  and channel_id = any($2::bigint[])
order by occurred_at, id
`, since, ids)
	if err != nil {
		return nil, nil, err
	}
	defer forwardRows.Close()

	forwards := make([]rebalanceAttributionForward, 0)
	for forwardRows.Next() {
		var forward rebalanceAttributionForward
		var targetID int64
		if err := forwardRows.Scan(&forward.ID, &targetID, &forward.OccurredAt, &forward.AmountSat, &forward.FeeMsat); err != nil {
			return nil, nil, err
		}
		if targetID <= 0 {
			continue
		}
		forward.TargetChannelID = uint64(targetID)
		forwards = append(forwards, forward)
	}
	if err := forwardRows.Err(); err != nil {
		return nil, nil, err
	}
	return lots, forwards, nil
}

type sovereignAttributionSnapshot struct {
	MetricSince time.Time
	Lots        []rebalanceAttributionLot
	Fast        map[int64]rebalanceAttributionResult
	Slow        map[int64]rebalanceAttributionResult
}

func (s *RebalanceService) loadSovereignAttributionSnapshot(ctx context.Context, cfg RebalanceConfig) (sovereignAttributionSnapshot, error) {
	metricSince := time.Now().Add(-7 * 24 * time.Hour)
	fastWindow := time.Duration(sovereignAttributionWindowHoursForConfig(cfg)) * time.Hour
	slowWindow := time.Duration(sovereignSlowSellerWindowHoursForConfig(cfg)) * time.Hour
	contextWindow := slowWindow
	if fastWindow > contextWindow {
		contextWindow = fastWindow
	}
	lots, forwards, err := s.loadRebalanceAttributionInputs(ctx, metricSince.Add(-contextWindow), nil)
	if err != nil {
		return sovereignAttributionSnapshot{}, err
	}
	return sovereignAttributionSnapshot{
		MetricSince: metricSince,
		Lots:        lots,
		Fast:        attributeRebalanceForwardsFIFO(lots, forwards, fastWindow),
		Slow:        attributeRebalanceForwardsFIFO(lots, forwards, slowWindow),
	}, nil
}
