package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"lightningos-light/internal/electrs"
	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrProvenanceDBUnavailable = errors.New("provenance db unavailable")

const (
	provenanceMaxLookbackBlocks      = int32(2 << 16) // headroom for reorgs on incremental refresh
	provenanceElectrsEnrichBatchSize = 200
)

// ProvenanceTx is one node in the wallet flow graph.
type ProvenanceTx struct {
	Txid          string    `json:"txid"`
	BlockHeight   int32     `json:"block_height"`
	Confirmations int32     `json:"confirmations"`
	Timestamp     int64     `json:"timestamp"`
	AmountSat     int64     `json:"amount_sat"`
	FeeSat        int64     `json:"fee_sat"`
	Label         string    `json:"label,omitempty"`
	IsExternal    bool      `json:"is_external"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ProvenanceOutput is an output of a tx — and one half of an edge: if
// SpentByTxid is set, an edge runs from this output to that tx.
type ProvenanceOutput struct {
	Txid         string `json:"txid"`
	Vout         uint32 `json:"vout"`
	Address      string `json:"address,omitempty"`
	AmountSat    int64  `json:"amount_sat"`
	IsOurs       bool   `json:"is_ours"`
	SpentByTxid  string `json:"spent_by_txid,omitempty"`
	SpentInVin   *int32 `json:"spent_in_vin,omitempty"`
	IsCurrentUtxo bool  `json:"is_current_utxo"`
}

// ProvenanceState mirrors the single-row state table used to drive
// incremental refresh.
type ProvenanceState struct {
	LastSyncHeight int32     `json:"last_sync_height"`
	LastSyncAt     time.Time `json:"last_sync_at"`
	LastError      string    `json:"last_error,omitempty"`
	TxCount        int       `json:"tx_count"`
	OutputCount    int       `json:"output_count"`
	OursOutputs    int       `json:"ours_outputs"`
	InFlight       bool      `json:"in_flight"`
}

// ProvenanceGraph is what the GET endpoint returns.
type ProvenanceGraph struct {
	State   ProvenanceState    `json:"state"`
	Txs     []ProvenanceTx     `json:"txs"`
	Outputs []ProvenanceOutput `json:"outputs"`
}

type ProvenanceService struct {
	db      *pgxpool.Pool
	logger  *log.Logger
	lnd     *lndclient.Client
	electrs electrs.TxSource
	mu      sync.Mutex
	running bool
}

func NewProvenanceService(db *pgxpool.Pool, logger *log.Logger, lnd *lndclient.Client, source electrs.TxSource) *ProvenanceService {
	return &ProvenanceService{db: db, logger: logger, lnd: lnd, electrs: source}
}

func (s *ProvenanceService) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrProvenanceDBUnavailable
	}
	_, err := s.db.Exec(ctx, `
create table if not exists provenance_tx (
  txid text primary key,
  block_height integer not null default 0,
  confirmations integer not null default 0,
  timestamp bigint not null default 0,
  amount_sat bigint not null default 0,
  fee_sat bigint not null default 0,
  label text not null default '',
  is_external boolean not null default false,
  updated_at timestamptz not null default now()
);
create index if not exists provenance_tx_height_idx on provenance_tx (block_height desc);

create table if not exists provenance_output (
  txid text not null,
  vout integer not null,
  address text not null default '',
  amount_sat bigint not null default 0,
  is_ours boolean not null default false,
  spent_by_txid text not null default '',
  spent_in_vin integer,
  enriched_at timestamptz,
  primary key (txid, vout)
);
create index if not exists provenance_output_spent_idx on provenance_output (spent_by_txid) where spent_by_txid <> '';
create index if not exists provenance_output_ours_unspent_idx on provenance_output (is_ours, spent_by_txid)
  where is_ours = true and spent_by_txid = '';

create table if not exists provenance_state (
  id boolean primary key default true,
  last_sync_height integer not null default 0,
  last_sync_at timestamptz,
  last_error text not null default '',
  tx_count integer not null default 0,
  output_count integer not null default 0,
  ours_outputs integer not null default 0
);
insert into provenance_state (id) values (true) on conflict do nothing;
`)
	return err
}

// RefreshNow walks the LND tx history. If fullRebuild is true the DB is
// truncated first; otherwise only transactions since last_sync_height are
// fetched and merged.
func (s *ProvenanceService) RefreshNow(ctx context.Context, fullRebuild bool) error {
	if s == nil || s.db == nil {
		return ErrProvenanceDBUnavailable
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errors.New("refresh already running")
	}
	s.running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	start := int32(0)
	if !fullRebuild {
		prev, err := s.loadState(ctx)
		if err != nil {
			return err
		}
		start = prev.LastSyncHeight - provenanceMaxLookbackBlocks
		if start < 0 {
			start = 0
		}
	}

	txs, err := s.lnd.ListAllTransactions(ctx, start, -1)
	if err != nil {
		s.recordError(ctx, fmt.Sprintf("list tx failed: %v", err))
		return err
	}

	if fullRebuild {
		if _, err := s.db.Exec(ctx, `truncate provenance_tx, provenance_output`); err != nil {
			s.recordError(ctx, fmt.Sprintf("truncate failed: %v", err))
			return err
		}
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	maxHeight := int32(0)
	for _, t := range txs {
		if t == nil || strings.TrimSpace(t.GetTxHash()) == "" {
			continue
		}
		if t.GetBlockHeight() > maxHeight {
			maxHeight = t.GetBlockHeight()
		}
		if err := upsertProvenanceTx(ctx, tx, t); err != nil {
			s.recordError(ctx, fmt.Sprintf("upsert tx %s: %v", t.GetTxHash(), err))
			return err
		}
		if err := upsertProvenanceOutputs(ctx, tx, t); err != nil {
			s.recordError(ctx, fmt.Sprintf("upsert outputs %s: %v", t.GetTxHash(), err))
			return err
		}
		if err := markSpentByInputs(ctx, tx, t); err != nil {
			s.recordError(ctx, fmt.Sprintf("mark spent for %s: %v", t.GetTxHash(), err))
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		s.recordError(ctx, fmt.Sprintf("commit failed: %v", err))
		return err
	}

	// Best-effort enrichment of external sources via electrs.
	if s.electrs != nil {
		if err := s.enrichExternalInputs(ctx); err != nil {
			s.logger.Printf("provenance: electrs enrichment partial: %v", err)
		}
	}

	if err := s.updateStateAfterSync(ctx, maxHeight); err != nil {
		s.logger.Printf("provenance: update state failed: %v", err)
	}
	return nil
}

// LoadGraphOptions controls how much of the graph the caller wants. Defaults
// are applied if zero/empty: Mode=ours, Limit=300.
type LoadGraphOptions struct {
	Mode            string // "live" | "ours" | "all" | "lineage"
	Limit           int    // max txs to return (safety cap; 0 = use default)
	RootTxid        string // for Mode=lineage: walk backwards from this tx
	Hops            int    // for Mode=lineage: how many generations to include (1-20)
	IncludeExternal bool   // for Mode=lineage: also walk past the wallet boundary via electrs
	MaxExternalTxs  int    // safety cap on electrs fetches per request (0 = default 500)
}

func (s *ProvenanceService) LoadGraph(ctx context.Context, opts LoadGraphOptions) (ProvenanceGraph, error) {
	if s == nil || s.db == nil {
		return ProvenanceGraph{}, ErrProvenanceDBUnavailable
	}
	state, err := s.loadState(ctx)
	if err != nil {
		return ProvenanceGraph{}, err
	}

	mode := strings.ToLower(strings.TrimSpace(opts.Mode))
	switch mode {
	case "live", "ours", "all", "lineage":
	default:
		mode = "ours"
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 300
	}
	if limit > 2000 {
		limit = 2000
	}

	graph := ProvenanceGraph{
		State:   state,
		Txs:     []ProvenanceTx{},
		Outputs: []ProvenanceOutput{},
	}

	// Lineage mode walks backwards from a chosen tx via the spent_by_txid
	// relation. Each hop adds one generation of ancestors.
	if mode == "lineage" {
		root := strings.ToLower(strings.TrimSpace(opts.RootTxid))
		if root == "" {
			return graph, errors.New("root txid required for lineage mode")
		}
		hops := opts.Hops
		if hops <= 0 {
			hops = 1
		}
		if hops > 20 {
			hops = 20
		}
		if opts.IncludeExternal && s.electrs != nil {
			maxFetches := opts.MaxExternalTxs
			if maxFetches <= 0 {
				maxFetches = 500
			}
			if err := s.walkExternalLineage(ctx, root, hops, maxFetches); err != nil {
				s.logger.Printf("provenance: external walk partial: %v", err)
			}
		}
		return s.loadLineageGraph(ctx, graph, root, hops, limit)
	}

	// Build the predicate for "which txs to include". For 'all' we include
	// everything, including external placeholder rows. For 'ours' we restrict
	// to txs that touched our wallet (have at least one ours output). For
	// 'live' we restrict to txs that produced a still-unspent ours output.
	var txWhere string
	switch mode {
	case "all":
		txWhere = `true`
	case "ours":
		txWhere = `txid in (select distinct txid from provenance_output where is_ours = true)`
	default: // "live"
		txWhere = `txid in (select distinct txid from provenance_output where is_ours = true and spent_by_txid = '')`
	}

	txRows, err := s.db.Query(ctx, `
select txid, block_height, confirmations, timestamp, amount_sat, fee_sat, label, is_external, updated_at
from provenance_tx
where `+txWhere+`
order by block_height desc, txid asc
limit $1`, limit)
	if err != nil {
		return graph, err
	}
	defer txRows.Close()
	keepTxids := make(map[string]struct{}, limit)
	for txRows.Next() {
		var t ProvenanceTx
		if err := txRows.Scan(&t.Txid, &t.BlockHeight, &t.Confirmations, &t.Timestamp, &t.AmountSat, &t.FeeSat, &t.Label, &t.IsExternal, &t.UpdatedAt); err != nil {
			return graph, err
		}
		graph.Txs = append(graph.Txs, t)
		keepTxids[t.Txid] = struct{}{}
	}
	if err := txRows.Err(); err != nil {
		return graph, err
	}
	if len(keepTxids) == 0 {
		return graph, nil
	}

	// Fetch outputs that are either produced by a kept tx OR spent by a kept
	// tx, so edges between displayed txs always have both ends.
	ids := make([]string, 0, len(keepTxids))
	for id := range keepTxids {
		ids = append(ids, id)
	}
	outRows, err := s.db.Query(ctx, `
select txid, vout, address, amount_sat, is_ours, spent_by_txid, spent_in_vin
from provenance_output
where txid = any($1) or spent_by_txid = any($1)
order by txid asc, vout asc`, ids)
	if err != nil {
		return graph, err
	}
	defer outRows.Close()
	for outRows.Next() {
		var o ProvenanceOutput
		var spentVin *int32
		if err := outRows.Scan(&o.Txid, &o.Vout, &o.Address, &o.AmountSat, &o.IsOurs, &o.SpentByTxid, &spentVin); err != nil {
			return graph, err
		}
		o.SpentInVin = spentVin
		o.IsCurrentUtxo = o.IsOurs && o.SpentByTxid == ""
		graph.Outputs = append(graph.Outputs, o)
	}
	return graph, outRows.Err()
}

// loadLineageGraph walks backwards from root via the spent_by_txid relation
// and returns every ancestor tx up to `hops` generations, plus the edges
// between displayed txs.
func (s *ProvenanceService) loadLineageGraph(ctx context.Context, graph ProvenanceGraph, root string, hops, limit int) (ProvenanceGraph, error) {
	txRows, err := s.db.Query(ctx, `
with recursive lineage(txid, hop) as (
  select $1::text, 0
  union
  select o.txid, l.hop + 1
  from lineage l
  join provenance_output o on o.spent_by_txid = l.txid
  where l.hop < $2 and o.txid <> ''
)
select t.txid, t.block_height, t.confirmations, t.timestamp, t.amount_sat,
       t.fee_sat, t.label, t.is_external, t.updated_at
from provenance_tx t
where t.txid in (select distinct txid from lineage)
order by t.block_height desc, t.txid asc
limit $3`, root, hops, limit)
	if err != nil {
		return graph, err
	}
	defer txRows.Close()

	keepTxids := make(map[string]struct{}, 64)
	for txRows.Next() {
		var t ProvenanceTx
		if err := txRows.Scan(&t.Txid, &t.BlockHeight, &t.Confirmations, &t.Timestamp, &t.AmountSat, &t.FeeSat, &t.Label, &t.IsExternal, &t.UpdatedAt); err != nil {
			return graph, err
		}
		graph.Txs = append(graph.Txs, t)
		keepTxids[t.Txid] = struct{}{}
	}
	if err := txRows.Err(); err != nil {
		return graph, err
	}
	if len(keepTxids) == 0 {
		return graph, nil
	}

	ids := make([]string, 0, len(keepTxids))
	for id := range keepTxids {
		ids = append(ids, id)
	}
	outRows, err := s.db.Query(ctx, `
select txid, vout, address, amount_sat, is_ours, spent_by_txid, spent_in_vin
from provenance_output
where txid = any($1) or spent_by_txid = any($1)
order by txid asc, vout asc`, ids)
	if err != nil {
		return graph, err
	}
	defer outRows.Close()
	for outRows.Next() {
		var o ProvenanceOutput
		var spentVin *int32
		if err := outRows.Scan(&o.Txid, &o.Vout, &o.Address, &o.AmountSat, &o.IsOurs, &o.SpentByTxid, &spentVin); err != nil {
			return graph, err
		}
		o.SpentInVin = spentVin
		o.IsCurrentUtxo = o.IsOurs && o.SpentByTxid == ""
		graph.Outputs = append(graph.Outputs, o)
	}
	return graph, outRows.Err()
}

func (s *ProvenanceService) Status(ctx context.Context) (ProvenanceState, error) {
	return s.loadState(ctx)
}

func (s *ProvenanceService) loadState(ctx context.Context) (ProvenanceState, error) {
	var st ProvenanceState
	row := s.db.QueryRow(ctx, `
select coalesce(s.last_sync_height, 0),
       coalesce(s.last_sync_at, to_timestamp(0)),
       coalesce(s.last_error, ''),
       coalesce((select count(*) from provenance_tx), 0),
       coalesce((select count(*) from provenance_output), 0),
       coalesce((select count(*) from provenance_output where is_ours = true and spent_by_txid = ''), 0)
from provenance_state s
where s.id = true`)
	if err := row.Scan(&st.LastSyncHeight, &st.LastSyncAt, &st.LastError, &st.TxCount, &st.OutputCount, &st.OursOutputs); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return st, err
		}
	}
	s.mu.Lock()
	st.InFlight = s.running
	s.mu.Unlock()
	return st, nil
}

func (s *ProvenanceService) recordError(ctx context.Context, msg string) {
	if _, err := s.db.Exec(ctx, `update provenance_state set last_error = $1, last_sync_at = now() where id = true`, msg); err != nil {
		s.logger.Printf("provenance: record error failed: %v", err)
	}
}

func (s *ProvenanceService) updateStateAfterSync(ctx context.Context, maxHeight int32) error {
	_, err := s.db.Exec(ctx, `
update provenance_state set
  last_sync_height = greatest(last_sync_height, $1),
  last_sync_at = now(),
  last_error = '',
  tx_count = (select count(*) from provenance_tx),
  output_count = (select count(*) from provenance_output),
  ours_outputs = (select count(*) from provenance_output where is_ours = true and spent_by_txid = '')
where id = true`, maxHeight)
	return err
}

func upsertProvenanceTx(ctx context.Context, tx pgx.Tx, t *lnrpc.Transaction) error {
	_, err := tx.Exec(ctx, `
insert into provenance_tx (txid, block_height, confirmations, timestamp, amount_sat, fee_sat, label, is_external, updated_at)
values ($1, $2, $3, $4, $5, $6, $7, false, now())
on conflict (txid) do update set
  block_height = excluded.block_height,
  confirmations = excluded.confirmations,
  timestamp = excluded.timestamp,
  amount_sat = excluded.amount_sat,
  fee_sat = excluded.fee_sat,
  label = excluded.label,
  is_external = false,
  updated_at = now()`,
		strings.ToLower(strings.TrimSpace(t.GetTxHash())),
		t.GetBlockHeight(), t.GetNumConfirmations(), t.GetTimeStamp(),
		t.GetAmount(), t.GetTotalFees(), t.GetLabel(),
	)
	return err
}

func upsertProvenanceOutputs(ctx context.Context, tx pgx.Tx, t *lnrpc.Transaction) error {
	txHash := strings.ToLower(strings.TrimSpace(t.GetTxHash()))
	for _, od := range t.GetOutputDetails() {
		if od == nil {
			continue
		}
		_, err := tx.Exec(ctx, `
insert into provenance_output (txid, vout, address, amount_sat, is_ours)
values ($1, $2, $3, $4, $5)
on conflict (txid, vout) do update set
  address = case when excluded.address <> '' then excluded.address else provenance_output.address end,
  amount_sat = case when excluded.amount_sat > 0 then excluded.amount_sat else provenance_output.amount_sat end,
  is_ours = provenance_output.is_ours or excluded.is_ours`,
			txHash,
			int32(od.GetOutputIndex()),
			od.GetAddress(),
			od.GetAmount(),
			od.GetIsOurAddress(),
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func markSpentByInputs(ctx context.Context, tx pgx.Tx, t *lnrpc.Transaction) error {
	spenderTxid := strings.ToLower(strings.TrimSpace(t.GetTxHash()))
	for vinIdx, in := range t.GetPreviousOutpoints() {
		if in == nil {
			continue
		}
		outpoint := strings.TrimSpace(in.GetOutpoint())
		if outpoint == "" {
			continue
		}
		prevTxid, prevVout, ok := splitOutpoint(outpoint)
		if !ok {
			continue
		}
		// Coinbase inputs use a null prev outpoint (txid=000…000, vout=0xFFFFFFFF).
		// They don't reference a real prior output, so they're not edges; skip.
		if prevVout == 0xFFFFFFFF || strings.Trim(prevTxid, "0") == "" {
			continue
		}
		// Ensure a placeholder row exists for the prev output so the edge
		// is renderable even if the prev tx isn't in our wallet history yet.
		_, err := tx.Exec(ctx, `
insert into provenance_output (txid, vout, is_ours, spent_by_txid, spent_in_vin)
values ($1, $2, $3, $4, $5)
on conflict (txid, vout) do update set
  is_ours = provenance_output.is_ours or excluded.is_ours,
  spent_by_txid = excluded.spent_by_txid,
  spent_in_vin = excluded.spent_in_vin`,
			prevTxid, int32(prevVout), in.GetIsOurOutput(), spenderTxid, int32(vinIdx),
		)
		if err != nil {
			return err
		}

		// Ensure a placeholder tx node exists too if the prev tx is external.
		_, err = tx.Exec(ctx, `
insert into provenance_tx (txid, is_external)
values ($1, true)
on conflict (txid) do nothing`, prevTxid)
		if err != nil {
			return err
		}
	}
	return nil
}

// enrichExternalInputs walks unresolved external outputs (placeholder rows
// with amount_sat=0 and address='') and fills them in from electrs.
func (s *ProvenanceService) enrichExternalInputs(ctx context.Context) error {
	if s.electrs == nil {
		return nil
	}
	rows, err := s.db.Query(ctx, `
select txid, vout
from provenance_output
where amount_sat = 0 and address = '' and is_ours = false
limit $1`, provenanceElectrsEnrichBatchSize)
	if err != nil {
		return err
	}
	type target struct {
		txid string
		vout uint32
	}
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.txid, &t.vout); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, t)
	}
	rows.Close()

	if len(targets) == 0 {
		return nil
	}
	// Group by txid so we fetch each verbose tx once.
	byTxid := map[string][]uint32{}
	for _, t := range targets {
		byTxid[t.txid] = append(byTxid[t.txid], t.vout)
	}

	for txid, vouts := range byTxid {
		verbose, err := s.electrs.GetTransaction(ctx, txid)
		if err != nil {
			s.logger.Printf("provenance: electrs get %s failed: %v", txid, err)
			continue
		}
		for _, vout := range vouts {
			if int(vout) >= len(verbose.Vout) {
				continue
			}
			v := verbose.Vout[vout]
			addr := v.ScriptPubKey.Address
			if addr == "" && len(v.ScriptPubKey.Addresses) > 0 {
				addr = v.ScriptPubKey.Addresses[0]
			}
			amountSat := int64(v.Value*1e8 + 0.5)
			if _, err := s.db.Exec(ctx, `
update provenance_output set
  address = $1,
  amount_sat = $2,
  enriched_at = now()
where txid = $3 and vout = $4`, addr, amountSat, txid, int32(vout)); err != nil {
				s.logger.Printf("provenance: electrs update %s:%d failed: %v", txid, vout, err)
			}
		}
	}
	return nil
}

// walkExternalLineage BFS-walks backwards from rootTxid through external
// (non-wallet) ancestors using electrs, up to `hops` generations. For each
// fetched tx it inserts a provenance_tx row (is_external=true), every vout as
// a provenance_output, and placeholder rows for every vin so the recursive
// lineage CTE can later traverse them as edges. Capped by maxFetches to
// bound runaway scans on heavily-fanned-out graphs.
func (s *ProvenanceService) walkExternalLineage(ctx context.Context, rootTxid string, hops, maxFetches int) error {
	if s.electrs == nil {
		return nil
	}
	visited := make(map[string]struct{}, 64)
	enriched := make(map[string]struct{}, 64)

	// Seed: find every external prev-outpoint reachable from this lineage in
	// the *existing* DB. We don't need to fetch the root itself (it's ours);
	// we start from its known external inputs and work backwards.
	frontier, err := s.findExternalAncestors(ctx, rootTxid)
	if err != nil {
		return err
	}
	for _, txid := range frontier {
		visited[txid] = struct{}{}
	}

	fetches := 0
	for hop := 0; hop < hops && len(frontier) > 0 && fetches < maxFetches; hop++ {
		next := make([]string, 0, len(frontier))
		for _, txid := range frontier {
			if fetches >= maxFetches {
				break
			}
			if _, ok := enriched[txid]; ok {
				continue
			}
			fetches++

			verbose, err := s.electrs.GetTransaction(ctx, txid)
			if err != nil {
				s.logger.Printf("provenance: external fetch %s failed: %v", txid, err)
				continue
			}
			enriched[txid] = struct{}{}
			if err := s.persistExternalTx(ctx, txid, verbose); err != nil {
				s.logger.Printf("provenance: persist external %s failed: %v", txid, err)
				continue
			}

			// Queue this tx's own inputs for the next hop.
			for _, vin := range verbose.Vin {
				if vin.Txid == "" || vin.Vout == 0xFFFFFFFF {
					continue
				}
				if strings.Trim(vin.Txid, "0") == "" {
					continue // coinbase
				}
				parent := strings.ToLower(strings.TrimSpace(vin.Txid))
				if _, seen := visited[parent]; seen {
					continue
				}
				visited[parent] = struct{}{}
				next = append(next, parent)
			}
		}
		frontier = next
	}
	return nil
}

// findExternalAncestors returns the set of txids that:
//   - are referenced as the producer of an output that's spent by rootTxid OR
//     by something already reachable from rootTxid in the existing graph
//   - have no entry in provenance_tx, OR their entry has is_external=true
//     (i.e. we haven't enriched them yet)
//
// This bootstraps the BFS without re-walking ours-side ancestors.
func (s *ProvenanceService) findExternalAncestors(ctx context.Context, rootTxid string) ([]string, error) {
	rows, err := s.db.Query(ctx, `
with recursive lineage(txid, hop) as (
  select $1::text, 0
  union
  select o.txid, l.hop + 1
  from lineage l
  join provenance_output o on o.spent_by_txid = l.txid
  where l.hop < 20 and o.txid <> ''
)
select distinct l.txid
from lineage l
left join provenance_tx t on t.txid = l.txid
where l.txid <> $1
  and (t.txid is null or t.is_external = true)`, rootTxid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0, 32)
	for rows.Next() {
		var txid string
		if err := rows.Scan(&txid); err != nil {
			return nil, err
		}
		out = append(out, txid)
	}
	return out, rows.Err()
}

// persistExternalTx writes one electrs-fetched tx and its inputs/outputs into
// the provenance tables. is_external is always true for these rows.
func (s *ProvenanceService) persistExternalTx(ctx context.Context, txid string, v electrs.VerboseTx) error {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Tx node: don't clobber an existing row that might already be in our
	// wallet ('is_external = false'). Only upsert when there's nothing there
	// or the existing row is also external.
	if _, err := tx.Exec(ctx, `
insert into provenance_tx (txid, block_height, timestamp, is_external, updated_at)
values ($1, 0, $2, true, now())
on conflict (txid) do update set
  timestamp = case when provenance_tx.timestamp = 0 then excluded.timestamp else provenance_tx.timestamp end,
  is_external = provenance_tx.is_external,
  updated_at = now()`, txid, v.Time); err != nil {
		return err
	}

	// Persist outputs of the external tx.
	for _, vo := range v.Vout {
		addr := vo.ScriptPubKey.Address
		if addr == "" && len(vo.ScriptPubKey.Addresses) > 0 {
			addr = vo.ScriptPubKey.Addresses[0]
		}
		amountSat := int64(vo.Value*1e8 + 0.5)
		if _, err := tx.Exec(ctx, `
insert into provenance_output (txid, vout, address, amount_sat, is_ours, enriched_at)
values ($1, $2, $3, $4, false, now())
on conflict (txid, vout) do update set
  address = case when excluded.address <> '' then excluded.address else provenance_output.address end,
  amount_sat = case when excluded.amount_sat > 0 then excluded.amount_sat else provenance_output.amount_sat end,
  enriched_at = now()`, txid, int32(vo.N), addr, amountSat); err != nil {
			return err
		}
	}

	// Mark this tx as spender of its inputs' prev outputs (placeholder rows).
	for vinIdx, vin := range v.Vin {
		if vin.Txid == "" || vin.Vout == 0xFFFFFFFF {
			continue
		}
		if strings.Trim(vin.Txid, "0") == "" {
			continue // coinbase
		}
		prevTxid := strings.ToLower(strings.TrimSpace(vin.Txid))
		if _, err := tx.Exec(ctx, `
insert into provenance_output (txid, vout, is_ours, spent_by_txid, spent_in_vin)
values ($1, $2, false, $3, $4)
on conflict (txid, vout) do update set
  spent_by_txid = excluded.spent_by_txid,
  spent_in_vin = excluded.spent_in_vin`, prevTxid, int32(vin.Vout), txid, int32(vinIdx)); err != nil {
			return err
		}
		// Placeholder parent tx node so the lineage CTE can step through it.
		if _, err := tx.Exec(ctx, `
insert into provenance_tx (txid, is_external)
values ($1, true)
on conflict (txid) do nothing`, prevTxid); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func splitOutpoint(s string) (string, uint32, bool) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return "", 0, false
	}
	idx, err := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 32)
	if err != nil {
		return "", 0, false
	}
	return strings.ToLower(strings.TrimSpace(parts[0])), uint32(idx), true
}
