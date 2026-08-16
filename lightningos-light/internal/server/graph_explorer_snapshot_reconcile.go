package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lightningos-light/internal/lndclient"

	"github.com/jackc/pgx/v5"
)

const graphExplorerSnapshotCloseGrace = 12 * time.Hour

var errGraphExplorerEmptySnapshot = errors.New("graph snapshot has no public channels")

// reconcileSnapshotPresence commits the inexpensive correctness-sensitive part
// of a graph refresh before nodes, policies, and policy history are refreshed.
// Older databases can take longer than the full refresh deadline to update. A
// separate transaction ensures channels missing from LND's current snapshot do
// not remain open merely because that heavier transaction times out later.
func (s *GraphExplorerService) reconcileSnapshotPresence(
	ctx context.Context,
	channels []lndclient.GraphChannel,
	observedAt time.Time,
	localPubkey string,
) error {
	channelIDs := graphExplorerSnapshotChannelIDs(channels)
	if len(channelIDs) == 0 {
		// Mainnet's public graph cannot legitimately be empty on a synced LND.
		// Refuse to turn a malformed or incomplete snapshot into mass closures.
		return errGraphExplorerEmptySnapshot
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin snapshot presence transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	if err := stageGraphSnapshotChannelIDs(ctx, tx, channelIDs); err != nil {
		return fmt.Errorf("stage current channel ids: %w", err)
	}
	affectedPubkeys, err := loadGraphLocalChannelPubkeys(ctx, tx, localPubkey)
	if err != nil {
		return fmt.Errorf("load local channel nodes: %w", err)
	}
	if err := s.reconcileLocalOpenChannels(ctx, tx, localPubkey, observedAt); err != nil {
		return fmt.Errorf("reconcile local open channels: %w", err)
	}
	if err := s.reconcileLocalClosedChannels(ctx, tx, observedAt); err != nil {
		return fmt.Errorf("reconcile local closed channels: %w", err)
	}
	snapshotAffectedPubkeys, err := reconcileSnapshotClosedChannels(ctx, tx, observedAt, localPubkey)
	if err != nil {
		return fmt.Errorf("close channels missing from snapshot: %w", err)
	}
	affectedPubkeys = append(affectedPubkeys, snapshotAffectedPubkeys...)
	if len(affectedPubkeys) > 0 {
		if err := recomputeGraphNodeAggregatesForPubKeys(ctx, tx, observedAt, affectedPubkeys); err != nil {
			return fmt.Errorf("recompute affected node aggregates: %w", err)
		}
	}
	if err := upsertGraphSnapshotState(ctx, tx, observedAt); err != nil {
		return fmt.Errorf("update snapshot state: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit snapshot presence: %w", err)
	}
	return nil
}

func graphExplorerSnapshotChannelIDs(channels []lndclient.GraphChannel) []int64 {
	result := make([]int64, 0, len(channels))
	seen := make(map[uint64]struct{}, len(channels))
	for _, channel := range channels {
		if channel.ChannelID == 0 {
			continue
		}
		if _, ok := seen[channel.ChannelID]; ok {
			continue
		}
		seen[channel.ChannelID] = struct{}{}
		result = append(result, int64(channel.ChannelID))
	}
	return result
}

func loadGraphLocalChannelPubkeys(ctx context.Context, tx pgx.Tx, localPubkey string) ([]string, error) {
	localPubkey = graphExplorerNormalizePubkey(localPubkey)
	if localPubkey == "" {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `
select node1_pubkey, node2_pubkey
from graph_channels
where node1_pubkey = $1 or node2_pubkey = $1
`, localPubkey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pubkeys := []string{localPubkey}
	for rows.Next() {
		var node1 string
		var node2 string
		if err := rows.Scan(&node1, &node2); err != nil {
			return nil, err
		}
		pubkeys = append(pubkeys, node1, node2)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return graphExplorerUniquePubKeys(pubkeys), nil
}

func stageGraphSnapshotChannelIDs(ctx context.Context, tx pgx.Tx, channelIDs []int64) error {
	if len(channelIDs) == 0 {
		return errGraphExplorerEmptySnapshot
	}
	if _, err := tx.Exec(ctx, `
create temporary table graph_snapshot_open_channels (
  chan_id bigint primary key
) on commit drop
`); err != nil {
		return err
	}
	if _, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"graph_snapshot_open_channels"},
		[]string{"chan_id"},
		pgx.CopyFromSlice(len(channelIDs), func(index int) ([]any, error) {
			return []any{channelIDs[index]}, nil
		}),
	); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `analyze graph_snapshot_open_channels`)
	return err
}

func reconcileSnapshotClosedChannels(ctx context.Context, tx pgx.Tx, observedAt time.Time, localPubkey string) ([]string, error) {
	localPubkey = graphExplorerNormalizePubkey(localPubkey)
	closeCutoff := observedAt.Add(-graphExplorerSnapshotCloseGrace)

	if _, err := tx.Exec(ctx, `
create temporary table graph_snapshot_closed_channels (
  chan_id bigint primary key
) on commit drop
`); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
insert into graph_snapshot_closed_channels (chan_id)
select ch.chan_id
from graph_channels ch
where ch.status = 'open'
  and ch.last_seen_at < $1
  and not exists (
    select 1
    from graph_snapshot_open_channels current_snapshot
    where current_snapshot.chan_id = ch.chan_id
  )
  and (
    $2 = ''
    or (
      coalesce(ch.node1_pubkey, '') <> $2
      and coalesce(ch.node2_pubkey, '') <> $2
    )
  )
`, closeCutoff, localPubkey); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
update graph_channels ch
set status = 'closed',
    closed_at = coalesce(ch.closed_at, $1),
    close_source = case
      when coalesce(ch.close_source, '') = '' then 'native+snapshot'
      else ch.close_source
    end,
    last_indexed_at = $1
from graph_snapshot_closed_channels snapshot_closed
where snapshot_closed.chan_id = ch.chan_id
`, observedAt); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
update graph_close_events e
set node1_pubkey = case when coalesce(ch.node1_pubkey, '') <> '' then ch.node1_pubkey else e.node1_pubkey end,
    node2_pubkey = case when coalesce(ch.node2_pubkey, '') <> '' then ch.node2_pubkey else e.node2_pubkey end,
    capacity_sat = case when ch.capacity_sat > 0 then ch.capacity_sat else e.capacity_sat end,
    closed_height = case when coalesce(ch.closed_height, 0) > 0 then ch.closed_height else e.closed_height end,
    observed_at = coalesce(e.observed_at, ch.closed_at, $1),
    close_source = case
      when coalesce(e.close_source, '') = '' or e.close_source = 'native' then ch.close_source
      else e.close_source
    end,
    metadata_json = coalesce(e.metadata_json, '{}'::jsonb) || '{"reconciled":true,"source":"snapshot_missing"}'::jsonb
from graph_channels ch
join graph_snapshot_closed_channels snapshot_closed on snapshot_closed.chan_id = ch.chan_id
where (e.chan_id > 0 and e.chan_id = ch.chan_id)
   or (coalesce(ch.chan_point, '') <> '' and e.chan_point = ch.chan_point)
`, observedAt); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
insert into graph_close_events (
  chan_id, chan_point, node1_pubkey, node2_pubkey, capacity_sat, closed_height, observed_at, close_source, metadata_json
)
select
  ch.chan_id,
  ch.chan_point,
  ch.node1_pubkey,
  ch.node2_pubkey,
  ch.capacity_sat,
  coalesce(ch.closed_height, 0),
  coalesce(ch.closed_at, $1),
  coalesce(nullif(ch.close_source, ''), 'native+snapshot'),
  '{"reconciled":true,"source":"snapshot_missing"}'::jsonb
from graph_channels ch
join graph_snapshot_closed_channels snapshot_closed on snapshot_closed.chan_id = ch.chan_id
where not exists (
  select 1
  from graph_close_events e
  where (e.chan_id > 0 and e.chan_id = ch.chan_id)
     or (coalesce(ch.chan_point, '') <> '' and e.chan_point = ch.chan_point)
)
`, observedAt); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
select ch.node1_pubkey, ch.node2_pubkey
from graph_channels ch
join graph_snapshot_closed_channels snapshot_closed on snapshot_closed.chan_id = ch.chan_id
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pubkeys := make([]string, 0)
	for rows.Next() {
		var node1 string
		var node2 string
		if err := rows.Scan(&node1, &node2); err != nil {
			return nil, err
		}
		pubkeys = append(pubkeys, node1, node2)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return graphExplorerUniquePubKeys(pubkeys), nil
}

func upsertGraphSnapshotState(ctx context.Context, tx pgx.Tx, observedAt time.Time) error {
	_, err := tx.Exec(ctx, `
insert into graph_sync_state (id, last_snapshot_at, updated_at)
values (true, $1, now())
on conflict (id) do update set
  last_snapshot_at = excluded.last_snapshot_at,
  updated_at = now()
`, observedAt)
	return err
}
