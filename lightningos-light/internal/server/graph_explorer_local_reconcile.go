package server

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"

	"github.com/jackc/pgx/v5"
)

type graphExplorerChannelKeySet struct {
	byChanID    map[uint64]struct{}
	byChanPoint map[string]struct{}
}

func (s *GraphExplorerService) reconcileLocalOpenChannels(ctx context.Context, tx pgx.Tx, localPubkey string, observedAt time.Time) error {
	if s == nil || s.lnd == nil {
		return nil
	}

	localPubkey = graphExplorerNormalizePubkey(localPubkey)
	if localPubkey == "" {
		return nil
	}

	channels, err := s.lnd.ListOpenChannelRefs(ctx)
	if err != nil {
		return err
	}

	const reopenQuery = `
update graph_channels
set node1_pubkey = case when $3 <> '' then $3 else node1_pubkey end,
    node2_pubkey = case when $4 <> '' then $4 else node2_pubkey end,
    capacity_sat = case when $5 > 0 then $5 else capacity_sat end,
    open_block_height = case when $6 > 0 then $6 else open_block_height end,
    status = 'open',
    last_seen_at = greatest(coalesce(last_seen_at, $7), $7),
    last_indexed_at = $7,
    closed_at = null,
    closed_height = null,
    close_source = null,
    close_type = null,
    close_txid = null,
    close_confidence = null,
    classified_at = null
where (chan_id > 0 and chan_id = $1)
   or ($2 <> '' and chan_point = $2)
`
	const cleanupQuery = `
delete from graph_close_events
where ((chan_id > 0 and chan_id = $1) or ($2 <> '' and chan_point = $2))
  and (
    close_source = 'native+snapshot'
    or metadata_json ->> 'source' = 'snapshot_missing'
  )
`

	batch := &pgx.Batch{}
	queued := 0
	for _, channel := range channels {
		chanPoint := graphExplorerNormalizeChanPoint(channel.ChannelPoint)
		if channel.ChannelID == 0 && chanPoint == "" {
			continue
		}
		remotePubkey := graphExplorerNormalizePubkey(channel.RemotePubkey)
		node1, node2 := canonicalPubKeyPair(localPubkey, remotePubkey)

		batch.Queue(reopenQuery,
			int64(channel.ChannelID),
			chanPoint,
			node1,
			node2,
			channel.CapacitySat,
			channelBlockHeight(channel.ChannelID),
			observedAt,
		)
		queued++
		batch.Queue(cleanupQuery, int64(channel.ChannelID), chanPoint)
		queued++
		if queued >= graphExplorerBatchSize {
			if err := executeBatch(ctx, tx, batch, queued); err != nil {
				return err
			}
			batch = &pgx.Batch{}
			queued = 0
		}
	}
	return executeBatch(ctx, tx, batch, queued)
}

func (s *GraphExplorerService) reconcileLocalClosedChannels(ctx context.Context, tx pgx.Tx, observedAt time.Time) error {
	if s == nil || s.lnd == nil {
		return nil
	}

	localPubkey := s.loadLocalPubkey(ctx)
	if localPubkey == "" {
		return nil
	}

	coverageSince, err := loadGraphCoverageSinceTx(ctx, tx)
	if err != nil {
		return err
	}
	coverageStart := observedAt
	if coverageSince != nil && !coverageSince.IsZero() {
		coverageStart = coverageSince.UTC()
	}

	openSet, err := loadGraphExplorerLocalOpenChannelKeySet(ctx, tx, localPubkey)
	if err != nil {
		return err
	}

	channels, err := s.lnd.ListClosedChannels(ctx)
	if err != nil {
		return err
	}

	for _, channel := range channels {
		if !graphExplorerShouldImportLocalClosedChannel(channel, coverageStart, openSet) {
			continue
		}
		if err := upsertGraphLocalClosedChannel(ctx, tx, localPubkey, channel, observedAt); err != nil {
			return err
		}
	}

	return nil
}

func loadGraphCoverageSinceTx(ctx context.Context, tx pgx.Tx) (*time.Time, error) {
	var coverageSince *time.Time
	err := tx.QueryRow(ctx, `
select first_native_coverage_at
from graph_sync_state
where id = true
`).Scan(&coverageSince)
	if err == nil {
		return coverageSince, nil
	}
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return nil, err
}

func loadGraphExplorerLocalOpenChannelKeySet(ctx context.Context, tx pgx.Tx, localPubkey string) (graphExplorerChannelKeySet, error) {
	result := graphExplorerChannelKeySet{
		byChanID:    map[uint64]struct{}{},
		byChanPoint: map[string]struct{}{},
	}

	rows, err := tx.Query(ctx, `
select chan_id, coalesce(chan_point, '')
from graph_channels
where status = 'open'
  and ($1 <> '' and (node1_pubkey = $1 or node2_pubkey = $1))
`, graphExplorerNormalizePubkey(localPubkey))
	if err != nil {
		return result, err
	}
	defer rows.Close()

	for rows.Next() {
		var chanID int64
		var chanPoint string
		if err := rows.Scan(&chanID, &chanPoint); err != nil {
			return result, err
		}
		if chanID > 0 {
			result.byChanID[uint64(chanID)] = struct{}{}
		}
		if point := graphExplorerNormalizeChanPoint(chanPoint); point != "" {
			result.byChanPoint[point] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func (s graphExplorerChannelKeySet) has(chanID uint64, chanPoint string) bool {
	if chanID > 0 {
		if _, ok := s.byChanID[chanID]; ok {
			return true
		}
	}
	if point := graphExplorerNormalizeChanPoint(chanPoint); point != "" {
		if _, ok := s.byChanPoint[point]; ok {
			return true
		}
	}
	return false
}

func graphExplorerShouldImportLocalClosedChannel(channel lndclient.ClosedChannelInfo, coverageStart time.Time, openSet graphExplorerChannelKeySet) bool {
	chanPoint := graphExplorerNormalizeChanPoint(channel.ChannelPoint)
	if channel.ChanID == 0 && chanPoint == "" {
		return false
	}
	if openSet.has(channel.ChanID, chanPoint) {
		return true
	}
	closedAt, ok := graphExplorerParseClosedAt(channel.ClosedAt)
	if !ok {
		return false
	}
	if coverageStart.IsZero() {
		return false
	}
	return !closedAt.Before(coverageStart)
}

func graphExplorerParseClosedAt(value string) (time.Time, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func upsertGraphLocalClosedChannel(ctx context.Context, tx pgx.Tx, localPubkey string, channel lndclient.ClosedChannelInfo, observedAt time.Time) error {
	localPubkey = graphExplorerNormalizePubkey(localPubkey)
	remotePubkey := graphExplorerNormalizePubkey(channel.RemotePubkey)
	node1, node2 := canonicalPubKeyPair(localPubkey, remotePubkey)
	chanPoint := graphExplorerNormalizeChanPoint(channel.ChannelPoint)
	closeType := normalizeGraphExplorerCloseType(channel.CloseTypeLabel)
	closeTxID := strings.ToLower(strings.TrimSpace(channel.ClosingTxHash))
	closedAt, ok := graphExplorerParseClosedAt(channel.ClosedAt)
	if !ok {
		closedAt = observedAt
	}
	classifiedAt := closedAt
	confidence := ""
	if closeType != "unknown" {
		confidence = "high"
	}

	_, err := tx.Exec(ctx, `
insert into graph_channels (
  chan_id, chan_point, node1_pubkey, node2_pubkey, capacity_sat, open_block_height,
  status, first_seen_at, last_seen_at, closed_at, closed_height, close_source, close_type,
  close_txid, close_confidence, classified_at, last_indexed_at
) values (
  $1,$2,$3,$4,$5,$6,'closed',$7,$7,$7,$8,'native+lnd',$9,$10,$11,$12,$7
)
on conflict (chan_id) do update set
  chan_point = case when excluded.chan_point <> '' then excluded.chan_point else graph_channels.chan_point end,
  node1_pubkey = case when excluded.node1_pubkey <> '' then excluded.node1_pubkey else graph_channels.node1_pubkey end,
  node2_pubkey = case when excluded.node2_pubkey <> '' then excluded.node2_pubkey else graph_channels.node2_pubkey end,
  capacity_sat = case when excluded.capacity_sat > 0 then excluded.capacity_sat else graph_channels.capacity_sat end,
  open_block_height = case when excluded.open_block_height > 0 then excluded.open_block_height else graph_channels.open_block_height end,
  status = 'closed',
  closed_at = coalesce(graph_channels.closed_at, excluded.closed_at),
  closed_height = case when excluded.closed_height > 0 then excluded.closed_height else graph_channels.closed_height end,
  close_source = case when excluded.close_source <> '' then excluded.close_source else graph_channels.close_source end,
  close_type = case when excluded.close_type <> '' then excluded.close_type else graph_channels.close_type end,
  close_txid = case when excluded.close_txid <> '' then excluded.close_txid else graph_channels.close_txid end,
  close_confidence = case when excluded.close_confidence <> '' then excluded.close_confidence else graph_channels.close_confidence end,
  classified_at = case when excluded.classified_at is not null then excluded.classified_at else graph_channels.classified_at end,
  last_indexed_at = excluded.last_indexed_at
`, int64(channel.ChanID), chanPoint, node1, node2, channel.CapacitySat, channelBlockHeight(channel.ChanID), closedAt, int(channel.CloseHeight), closeType, closeTxID, confidence, classifiedAt)
	if err != nil {
		return err
	}

	metadata, _ := json.Marshal(map[string]any{
		"reconciled":       true,
		"source":           "lnd_closedchannels",
		"close_initiator":  strings.TrimSpace(channel.CloseInitiatorLabel),
		"open_initiator":   strings.TrimSpace(channel.OpenInitiatorLabel),
		"resolution_count": len(channel.Resolutions),
	})

	_, err = tx.Exec(ctx, `
update graph_close_events
set node1_pubkey = case when $3 <> '' then $3 else node1_pubkey end,
    node2_pubkey = case when $4 <> '' then $4 else node2_pubkey end,
    capacity_sat = case when $5 > 0 then $5 else capacity_sat end,
    closed_height = case when $6 > 0 then $6 else closed_height end,
    observed_at = coalesce(observed_at, $7),
    close_source = 'native+lnd',
    close_type = case when $8 <> '' then $8 else close_type end,
    close_txid = case when $9 <> '' then $9 else close_txid end,
    close_classifier = 'lnd',
    close_confidence = case when $10 <> '' then $10 else close_confidence end,
    close_reason = 'refresh_local_closedchannels',
    classified_at = coalesce($11::timestamptz, classified_at),
    metadata_json = coalesce(metadata_json, '{}'::jsonb) || $12::jsonb
where (chan_id > 0 and chan_id = $1)
   or ($2 <> '' and chan_point = $2)
`, int64(channel.ChanID), chanPoint, node1, node2, channel.CapacitySat, int(channel.CloseHeight), closedAt, closeType, closeTxID, confidence, classifiedAt, string(metadata))
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
insert into graph_close_events (
  chan_id, chan_point, node1_pubkey, node2_pubkey, capacity_sat, closed_height, observed_at, close_source,
  close_type, close_txid, close_classifier, close_confidence, close_reason, classified_at, metadata_json
)
select
  $1,$2,$3,$4,$5,$6,$7,'native+lnd',$8,$9,'lnd',$10,'refresh_local_closedchannels',$11,$12::jsonb
where not exists (
  select 1
  from graph_close_events
  where (chan_id > 0 and chan_id = $1)
     or ($2 <> '' and chan_point = $2)
)
`, int64(channel.ChanID), chanPoint, node1, node2, channel.CapacitySat, int(channel.CloseHeight), closedAt, closeType, closeTxID, confidence, classifiedAt, string(metadata))
	return err
}
