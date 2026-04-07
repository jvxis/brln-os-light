package server

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

func reconcileSnapshotClosedChannels(ctx context.Context, tx pgx.Tx, observedAt time.Time) error {
	const updateQuery = `
with snapshot_closed as (
  update graph_channels
  set status = 'closed',
      closed_at = coalesce(closed_at, $1),
      close_source = case
        when coalesce(close_source, '') = '' then 'native+snapshot'
        else close_source
      end,
      last_indexed_at = $1
  where status = 'open'
    and last_seen_at < $1
  returning chan_id, chan_point, node1_pubkey, node2_pubkey, capacity_sat, coalesce(closed_height, 0) as closed_height,
            coalesce(closed_at, $1) as observed_at, close_source
)
update graph_close_events e
set node1_pubkey = case when coalesce(sc.node1_pubkey, '') <> '' then sc.node1_pubkey else e.node1_pubkey end,
    node2_pubkey = case when coalesce(sc.node2_pubkey, '') <> '' then sc.node2_pubkey else e.node2_pubkey end,
    capacity_sat = case when sc.capacity_sat > 0 then sc.capacity_sat else e.capacity_sat end,
    closed_height = case when sc.closed_height > 0 then sc.closed_height else e.closed_height end,
    observed_at = coalesce(e.observed_at, sc.observed_at),
    close_source = case when coalesce(e.close_source, '') = '' or e.close_source = 'native' then sc.close_source else e.close_source end,
    metadata_json = coalesce(e.metadata_json, '{}'::jsonb) || '{"reconciled":true,"source":"snapshot_missing"}'::jsonb
from snapshot_closed sc
where (e.chan_id > 0 and e.chan_id = sc.chan_id)
   or (coalesce(sc.chan_point, '') <> '' and e.chan_point = sc.chan_point)
`

	if _, err := tx.Exec(ctx, updateQuery, observedAt); err != nil {
		return err
	}

	const insertQuery = `
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
where ch.status = 'closed'
  and ch.last_indexed_at = $1
  and coalesce(ch.close_source, '') = 'native+snapshot'
  and not exists (
    select 1
    from graph_close_events e
    where (e.chan_id > 0 and e.chan_id = ch.chan_id)
       or (coalesce(ch.chan_point, '') <> '' and e.chan_point = ch.chan_point)
  )
`

	_, err := tx.Exec(ctx, insertQuery, observedAt)
	return err
}
