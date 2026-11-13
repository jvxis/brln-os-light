package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Forward History backs the collapsible card on Lightning Ops that lets the
// operator browse every forward the node routed, filter by incoming/outgoing
// channel, sort, and see the filtered subtotal against the whole window.
//
// The data source is the notifications table: the Notifier already stores one
// row per settled forward (type='forward') with both channel ids, msat
// amounts, fee and the "in -> out" alias pair. No new table is needed.

const (
	forwardHistoryDefaultLimit = 200
	forwardHistoryMaxLimit     = 500
	forwardHistoryMaxOffset    = 50000
	forwardHistoryFlowLimit    = 12
	forwardHistoryQueryTimeout = 20 * time.Second
	forwardHistoryAliasTTL     = 60 * time.Second
)

type forwardHistoryOptions struct {
	Range    string
	Since    time.Time
	Until    time.Time
	ChanIn   uint64
	ChanOut  uint64
	Sort     string
	Order    string
	Limit    int
	Offset   int
	Location *time.Location
}

func (o forwardHistoryOptions) filtered() bool {
	return o.ChanIn != 0 || o.ChanOut != 0
}

type forwardHistoryTotals struct {
	Count     int64 `json:"count"`
	AmountSat int64 `json:"amount_sat"`
	FeeSat    int64 `json:"fee_sat"`
	FeeMsat   int64 `json:"fee_msat"`
	AvgPPM    int64 `json:"avg_ppm"`
}

type forwardHistoryChannel struct {
	ChannelID    uint64 `json:"channel_id"`
	ChannelIDStr string `json:"channel_id_str"`
	ChannelPoint string `json:"channel_point,omitempty"`
	PeerPubkey   string `json:"peer_pubkey,omitempty"`
	PeerAlias    string `json:"peer_alias"`
	Open         bool   `json:"open"`
	InCount      int64  `json:"in_count"`
	InAmountSat  int64  `json:"in_amount_sat"`
	InFeeSat     int64  `json:"in_fee_sat"`
	OutCount     int64  `json:"out_count"`
	OutAmountSat int64  `json:"out_amount_sat"`
	OutFeeSat    int64  `json:"out_fee_sat"`
}

type forwardHistoryFlowEntry struct {
	ChannelID    uint64 `json:"channel_id"`
	ChannelIDStr string `json:"channel_id_str"`
	PeerAlias    string `json:"peer_alias"`
	Count        int64  `json:"count"`
	AmountSat    int64  `json:"amount_sat"`
	FeeSat       int64  `json:"fee_sat"`
}

type forwardHistoryBucket struct {
	Start     time.Time `json:"start"`
	Count     int64     `json:"count"`
	AmountSat int64     `json:"amount_sat"`
	FeeSat    int64     `json:"fee_sat"`
}

type forwardHistoryItem struct {
	ID            int64     `json:"id"`
	OccurredAt    time.Time `json:"occurred_at"`
	ChanIDIn      uint64    `json:"chan_id_in"`
	ChanIDInStr   string    `json:"chan_id_in_str"`
	ChanIDOut     uint64    `json:"chan_id_out"`
	ChanIDOutStr  string    `json:"chan_id_out_str"`
	PeerAliasIn   string    `json:"peer_alias_in"`
	PeerAliasOut  string    `json:"peer_alias_out"`
	PeerPubkeyIn  string    `json:"peer_pubkey_in,omitempty"`
	PeerPubkeyOut string    `json:"peer_pubkey_out,omitempty"`
	AmountInSat   int64     `json:"amount_in_sat"`
	AmountSat     int64     `json:"amount_sat"`
	FeeSat        int64     `json:"fee_sat"`
	FeeMsat       int64     `json:"fee_msat"`
	PPM           int64     `json:"ppm"`
}

type forwardHistoryResponse struct {
	Range      string                    `json:"range"`
	Since      time.Time                 `json:"since"`
	Until      time.Time                 `json:"until"`
	Timezone   string                    `json:"timezone"`
	BucketUnit string                    `json:"bucket_unit"`
	ChanIn     string                    `json:"chan_in,omitempty"`
	ChanOut    string                    `json:"chan_out,omitempty"`
	Sort       string                    `json:"sort"`
	Order      string                    `json:"order"`
	Limit      int                       `json:"limit"`
	Offset     int                       `json:"offset"`
	HasMore    bool                      `json:"has_more"`
	Totals     forwardHistoryTotals      `json:"totals"`
	Filtered   forwardHistoryTotals      `json:"filtered"`
	Channels   []forwardHistoryChannel   `json:"channels"`
	FlowIn     []forwardHistoryFlowEntry `json:"flow_in"`
	FlowOut    []forwardHistoryFlowEntry `json:"flow_out"`
	Buckets    []forwardHistoryBucket    `json:"buckets"`
	Items      []forwardHistoryItem      `json:"items"`
}

// forwardChannelIdentity is what we know about a channel that appears in a
// forward: alias, pubkey and point. It comes from LND for open channels and
// from the stored notification columns for closed ones.
type forwardChannelIdentity struct {
	Alias  string
	Pubkey string
	Point  string
	Open   bool
}

func forwardHistoryRangeDuration(raw string) (string, time.Duration, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "7d":
		return "7d", 7 * 24 * time.Hour, true
	case "24h":
		return "24h", 24 * time.Hour, true
	case "30d":
		return "30d", 30 * 24 * time.Hour, true
	case "90d":
		return "90d", 90 * 24 * time.Hour, true
	}
	return "", 0, false
}

// forwardHistoryBucketUnit picks the timeline granularity: hourly for the
// 24h window, daily otherwise.
func forwardHistoryBucketUnit(rangeKey string) string {
	if rangeKey == "24h" {
		return "hour"
	}
	return "day"
}

func parseForwardHistoryChannelID(raw string) (uint64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		return 0, errors.New("channel id must be a positive integer")
	}
	return id, nil
}

func parseForwardHistoryOptions(r *http.Request, now time.Time) (forwardHistoryOptions, error) {
	q := r.URL.Query()
	opts := forwardHistoryOptions{Limit: forwardHistoryDefaultLimit, Sort: "time", Order: "desc", Location: time.UTC}

	rangeKey, dur, ok := forwardHistoryRangeDuration(q.Get("range"))
	if !ok {
		return opts, errors.New("range must be one of 24h, 7d, 30d, or 90d")
	}
	opts.Range = rangeKey
	opts.Until = now.UTC()
	opts.Since = opts.Until.Add(-dur)

	var err error
	if opts.ChanIn, err = parseForwardHistoryChannelID(q.Get("chan_in")); err != nil {
		return opts, fmt.Errorf("chan_in: %w", err)
	}
	if opts.ChanOut, err = parseForwardHistoryChannelID(q.Get("chan_out")); err != nil {
		return opts, fmt.Errorf("chan_out: %w", err)
	}

	switch raw := strings.ToLower(strings.TrimSpace(q.Get("sort"))); raw {
	case "":
	case "time", "amount", "fee", "ppm":
		opts.Sort = raw
	default:
		return opts, errors.New("sort must be one of time, amount, fee, or ppm")
	}
	switch raw := strings.ToLower(strings.TrimSpace(q.Get("order"))); raw {
	case "":
	case "asc", "desc":
		opts.Order = raw
	default:
		return opts, errors.New("order must be asc or desc")
	}

	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return opts, errors.New("limit must be a positive integer")
		}
		if v > forwardHistoryMaxLimit {
			v = forwardHistoryMaxLimit
		}
		opts.Limit = v
	}
	if raw := strings.TrimSpace(q.Get("offset")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 || v > forwardHistoryMaxOffset {
			return opts, fmt.Errorf("offset must be between 0 and %d", forwardHistoryMaxOffset)
		}
		opts.Offset = v
	}

	if raw := strings.TrimSpace(q.Get("tz")); raw != "" {
		loc, err := time.LoadLocation(raw)
		if err != nil {
			return opts, errors.New("tz must be a valid IANA time zone")
		}
		opts.Location = loc
	}
	return opts, nil
}

// splitForwardPeerAlias undoes the "in -> out" pair the Notifier stores in
// peer_alias. It is the fallback for channels LND no longer lists.
func splitForwardPeerAlias(pair string) (string, string) {
	pair = strings.TrimSpace(pair)
	if pair == "" {
		return "", ""
	}
	// The Notifier trims the pair, so a missing side leaves the arrow at an
	// edge: "-> Out" or "In ->".
	if strings.HasPrefix(pair, "-> ") {
		return "", strings.TrimSpace(pair[3:])
	}
	if strings.HasSuffix(pair, " ->") {
		return strings.TrimSpace(pair[:len(pair)-3]), ""
	}
	idx := strings.Index(pair, " -> ")
	if idx < 0 {
		return pair, ""
	}
	return strings.TrimSpace(pair[:idx]), strings.TrimSpace(pair[idx+4:])
}

// forwardPPM is the fee in parts per million of the outgoing amount, using
// the msat columns when the row has them and the sat columns otherwise.
func forwardPPM(feeMsat, feeSat, amountOutMsat, amountSat int64) int64 {
	fee := feeMsat
	if fee <= 0 {
		fee = feeSat * 1000
	}
	amount := amountOutMsat
	if amount <= 0 {
		amount = amountSat * 1000
	}
	if amount <= 0 || fee <= 0 {
		return 0
	}
	return int64(float64(fee) * 1_000_000 / float64(amount))
}

func forwardHistoryAvgPPM(feeMsat, amountSat int64) int64 {
	if amountSat <= 0 || feeMsat <= 0 {
		return 0
	}
	return int64(float64(feeMsat) * 1_000 / float64(amountSat))
}

// fillForwardHistoryBuckets returns one bucket per hour/day between since and
// until in the given location, with the aggregates from the query merged in,
// so the timeline shows quiet periods as empty columns instead of gaps.
func fillForwardHistoryBuckets(since, until time.Time, unit string, loc *time.Location, found map[time.Time]forwardHistoryBucket) []forwardHistoryBucket {
	if loc == nil {
		loc = time.UTC
	}
	truncate := func(ts time.Time) time.Time {
		local := ts.In(loc)
		if unit == "hour" {
			return time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, loc)
		}
		return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	}
	step := func(ts time.Time) time.Time {
		if unit == "hour" {
			return ts.Add(time.Hour)
		}
		return ts.AddDate(0, 0, 1)
	}
	normalized := make(map[int64]forwardHistoryBucket, len(found))
	for start, bucket := range found {
		key := truncate(start).Unix()
		existing := normalized[key]
		existing.Count += bucket.Count
		existing.AmountSat += bucket.AmountSat
		existing.FeeSat += bucket.FeeSat
		normalized[key] = existing
	}
	out := []forwardHistoryBucket{}
	for cursor := truncate(since); !cursor.After(until); cursor = step(cursor) {
		bucket := normalized[cursor.Unix()]
		bucket.Start = cursor
		out = append(out, bucket)
		if len(out) > 24*31*4 {
			break
		}
	}
	return out
}

// forwardHistoryChannelIdentities resolves channel ids to alias/pubkey/point
// via LND, cached for a minute because the card polls while open.
func (s *Server) forwardHistoryChannelIdentities(ctx context.Context) map[uint64]forwardChannelIdentity {
	s.forwardHistoryAliasMu.Lock()
	defer s.forwardHistoryAliasMu.Unlock()
	if s.forwardHistoryAliases != nil && time.Since(s.forwardHistoryAliasAt) < forwardHistoryAliasTTL {
		return s.forwardHistoryAliases
	}
	if s.lnd == nil {
		return map[uint64]forwardChannelIdentity{}
	}
	lndCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	channels, err := s.lnd.ListChannels(lndCtx)
	if err != nil {
		if s.forwardHistoryAliases != nil {
			return s.forwardHistoryAliases
		}
		return map[uint64]forwardChannelIdentity{}
	}
	mapped := make(map[uint64]forwardChannelIdentity, len(channels))
	for _, ch := range channels {
		if ch.ChannelID == 0 {
			continue
		}
		mapped[ch.ChannelID] = forwardChannelIdentity{Alias: strings.TrimSpace(ch.PeerAlias), Pubkey: ch.RemotePubkey, Point: ch.ChannelPoint, Open: true}
	}
	s.forwardHistoryAliases = mapped
	s.forwardHistoryAliasAt = time.Now()
	return mapped
}

type forwardHistoryWhere struct {
	clauses []string
	args    []any
}

func (w *forwardHistoryWhere) add(clause string, arg any) {
	w.args = append(w.args, arg)
	w.clauses = append(w.clauses, strings.ReplaceAll(clause, "?", fmt.Sprintf("$%d", len(w.args))))
}

func (w forwardHistoryWhere) sql() string {
	return strings.Join(w.clauses, " and ")
}

func forwardHistoryBaseWhere(opts forwardHistoryOptions, withChannels bool) forwardHistoryWhere {
	w := forwardHistoryWhere{}
	w.add("type = ?", "forward")
	w.add("occurred_at >= ?", opts.Since)
	w.add("occurred_at < ?", opts.Until)
	if withChannels {
		if opts.ChanIn != 0 {
			w.add("chan_id_in = ?", int64(opts.ChanIn))
		}
		if opts.ChanOut != 0 {
			w.add("chan_id_out = ?", int64(opts.ChanOut))
		}
	}
	return w
}

const forwardHistoryFeeMsatExpr = "(case when fee_msat > 0 then fee_msat else fee_sat * 1000 end)"
const forwardHistoryAmountMsatExpr = "coalesce(nullif(amount_out_msat, 0), amount_sat * 1000)"

func (s *Server) forwardHistoryTotals(ctx context.Context, opts forwardHistoryOptions, withChannels bool) (forwardHistoryTotals, error) {
	w := forwardHistoryBaseWhere(opts, withChannels)
	var out forwardHistoryTotals
	err := s.db.QueryRow(ctx, `select count(*), coalesce(sum(amount_sat), 0)::bigint, coalesce(sum(fee_sat), 0)::bigint, coalesce(sum(`+forwardHistoryFeeMsatExpr+`), 0)::bigint from notifications where `+w.sql(), w.args...).
		Scan(&out.Count, &out.AmountSat, &out.FeeSat, &out.FeeMsat)
	if err != nil {
		return out, err
	}
	out.AvgPPM = forwardHistoryAvgPPM(out.FeeMsat, out.AmountSat)
	return out, nil
}

type forwardHistoryChannelAgg struct {
	channelID uint64
	count     int64
	amountSat int64
	feeSat    int64
	aliasPair string
	pubkey    string
	point     string
}

// forwardHistoryAggregateBySide groups the window by chan_id_in ("in") or
// chan_id_out ("out"). The alias pair, pubkey and point of the most recent
// row ride along as the fallback identity for channels LND no longer lists.
func (s *Server) forwardHistoryAggregateBySide(ctx context.Context, opts forwardHistoryOptions, side string, withChannels bool, limit int) ([]forwardHistoryChannelAgg, error) {
	col, pubkeyCol, pointCol := "chan_id_in", "peer_pubkey_in", "channel_point_in"
	if side == "out" {
		col, pubkeyCol, pointCol = "chan_id_out", "peer_pubkey_out", "channel_point_out"
	}
	w := forwardHistoryBaseWhere(opts, withChannels)
	w.add(col+" is not null and "+col+" <> ?", int64(0))
	query := `select ` + col + `, count(*), coalesce(sum(amount_sat), 0)::bigint, coalesce(sum(fee_sat), 0)::bigint,
		coalesce((array_agg(peer_alias order by occurred_at desc, id desc))[1], ''),
		coalesce((array_agg(` + pubkeyCol + ` order by occurred_at desc, id desc))[1], ''),
		coalesce((array_agg(` + pointCol + ` order by occurred_at desc, id desc))[1], '')
		from notifications where ` + w.sql() + ` group by ` + col + ` order by sum(amount_sat) desc, count(*) desc`
	if limit > 0 {
		query += fmt.Sprintf(" limit %d", limit)
	}
	rows, err := s.db.Query(ctx, query, w.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []forwardHistoryChannelAgg{}
	for rows.Next() {
		var agg forwardHistoryChannelAgg
		var id int64
		if err := rows.Scan(&id, &agg.count, &agg.amountSat, &agg.feeSat, &agg.aliasPair, &agg.pubkey, &agg.point); err != nil {
			return nil, err
		}
		agg.channelID = uint64(id)
		out = append(out, agg)
	}
	return out, rows.Err()
}

func forwardHistoryIdentity(known map[uint64]forwardChannelIdentity, channelID uint64, aliasPair string, side string, pubkey, point string) forwardChannelIdentity {
	if identity, ok := known[channelID]; ok && identity.Alias != "" {
		return identity
	}
	inAlias, outAlias := splitForwardPeerAlias(aliasPair)
	alias := inAlias
	if side == "out" {
		alias = outAlias
	}
	identity := known[channelID]
	if identity.Alias == "" {
		identity.Alias = alias
	}
	if identity.Pubkey == "" {
		identity.Pubkey = pubkey
	}
	if identity.Point == "" {
		identity.Point = point
	}
	return identity
}

func (s *Server) forwardHistoryChannels(ctx context.Context, opts forwardHistoryOptions, known map[uint64]forwardChannelIdentity) ([]forwardHistoryChannel, error) {
	ins, err := s.forwardHistoryAggregateBySide(ctx, opts, "in", false, 0)
	if err != nil {
		return nil, err
	}
	outs, err := s.forwardHistoryAggregateBySide(ctx, opts, "out", false, 0)
	if err != nil {
		return nil, err
	}
	merged := map[uint64]*forwardHistoryChannel{}
	ensure := func(agg forwardHistoryChannelAgg, side string) *forwardHistoryChannel {
		entry, ok := merged[agg.channelID]
		if !ok {
			identity := forwardHistoryIdentity(known, agg.channelID, agg.aliasPair, side, agg.pubkey, agg.point)
			entry = &forwardHistoryChannel{ChannelID: agg.channelID, ChannelIDStr: strconv.FormatUint(agg.channelID, 10), ChannelPoint: identity.Point, PeerPubkey: identity.Pubkey, PeerAlias: identity.Alias, Open: identity.Open}
			merged[agg.channelID] = entry
		} else if entry.PeerAlias == "" {
			entry.PeerAlias = forwardHistoryIdentity(known, agg.channelID, agg.aliasPair, side, agg.pubkey, agg.point).Alias
		}
		return entry
	}
	for _, agg := range ins {
		entry := ensure(agg, "in")
		entry.InCount, entry.InAmountSat, entry.InFeeSat = agg.count, agg.amountSat, agg.feeSat
	}
	for _, agg := range outs {
		entry := ensure(agg, "out")
		entry.OutCount, entry.OutAmountSat, entry.OutFeeSat = agg.count, agg.amountSat, agg.feeSat
	}
	out := make([]forwardHistoryChannel, 0, len(merged))
	for _, entry := range merged {
		if entry.PeerAlias == "" {
			entry.PeerAlias = entry.ChannelIDStr
		}
		out = append(out, *entry)
	}
	sort.Slice(out, func(i, j int) bool {
		ti, tj := out[i].InAmountSat+out[i].OutAmountSat, out[j].InAmountSat+out[j].OutAmountSat
		if ti != tj {
			return ti > tj
		}
		return out[i].ChannelID < out[j].ChannelID
	})
	return out, nil
}

func (s *Server) forwardHistoryFlow(ctx context.Context, opts forwardHistoryOptions, side string, known map[uint64]forwardChannelIdentity) ([]forwardHistoryFlowEntry, error) {
	aggs, err := s.forwardHistoryAggregateBySide(ctx, opts, side, true, forwardHistoryFlowLimit)
	if err != nil {
		return nil, err
	}
	out := make([]forwardHistoryFlowEntry, 0, len(aggs))
	for _, agg := range aggs {
		identity := forwardHistoryIdentity(known, agg.channelID, agg.aliasPair, side, agg.pubkey, agg.point)
		alias := identity.Alias
		if alias == "" {
			alias = strconv.FormatUint(agg.channelID, 10)
		}
		out = append(out, forwardHistoryFlowEntry{ChannelID: agg.channelID, ChannelIDStr: strconv.FormatUint(agg.channelID, 10), PeerAlias: alias, Count: agg.count, AmountSat: agg.amountSat, FeeSat: agg.feeSat})
	}
	return out, nil
}

func (s *Server) forwardHistoryBuckets(ctx context.Context, opts forwardHistoryOptions) ([]forwardHistoryBucket, error) {
	unit := forwardHistoryBucketUnit(opts.Range)
	w := forwardHistoryBaseWhere(opts, true)
	// The unit is a vetted literal ("hour"/"day"); the time zone travels as an
	// argument after the where-clause ones.
	tzArg := len(w.args) + 1
	args := append(append([]any{}, w.args...), opts.Location.String())
	query := fmt.Sprintf(`select date_trunc('%s', occurred_at at time zone $%d) as bucket, count(*), coalesce(sum(amount_sat), 0)::bigint, coalesce(sum(fee_sat), 0)::bigint
		from notifications where %s group by bucket order by bucket`, unit, tzArg, w.sql())
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := map[time.Time]forwardHistoryBucket{}
	for rows.Next() {
		var naive time.Time
		var bucket forwardHistoryBucket
		if err := rows.Scan(&naive, &bucket.Count, &bucket.AmountSat, &bucket.FeeSat); err != nil {
			return nil, err
		}
		// date_trunc over "at time zone" yields a naive local timestamp; pin it
		// to the requested location so the Go side lines it up correctly.
		start := time.Date(naive.Year(), naive.Month(), naive.Day(), naive.Hour(), 0, 0, 0, opts.Location)
		found[start] = bucket
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return fillForwardHistoryBuckets(opts.Since, opts.Until, unit, opts.Location, found), nil
}

func forwardHistoryOrderBy(opts forwardHistoryOptions) string {
	dir := "desc"
	if opts.Order == "asc" {
		dir = "asc"
	}
	switch opts.Sort {
	case "amount":
		return fmt.Sprintf("amount_sat %s, occurred_at desc, id desc", dir)
	case "fee":
		return fmt.Sprintf("%s %s, occurred_at desc, id desc", forwardHistoryFeeMsatExpr, dir)
	case "ppm":
		return fmt.Sprintf("(%s::numeric / nullif(%s, 0)) %s nulls last, occurred_at desc, id desc", forwardHistoryFeeMsatExpr, forwardHistoryAmountMsatExpr, dir)
	default:
		return fmt.Sprintf("occurred_at %s, id %s", dir, dir)
	}
}

func (s *Server) forwardHistoryItems(ctx context.Context, opts forwardHistoryOptions, known map[uint64]forwardChannelIdentity) ([]forwardHistoryItem, bool, error) {
	w := forwardHistoryBaseWhere(opts, true)
	query := fmt.Sprintf(`select id, occurred_at, coalesce(chan_id_in, 0), coalesce(chan_id_out, 0), amount_sat, fee_sat, fee_msat,
		coalesce(amount_in_msat, 0), coalesce(amount_out_msat, 0), coalesce(peer_alias, ''), coalesce(peer_pubkey_in, ''), coalesce(peer_pubkey_out, '')
		from notifications where %s order by %s limit %d offset %d`, w.sql(), forwardHistoryOrderBy(opts), opts.Limit+1, opts.Offset)
	rows, err := s.db.Query(ctx, query, w.args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	items := []forwardHistoryItem{}
	for rows.Next() {
		var item forwardHistoryItem
		var chanIn, chanOut, amountInMsat, amountOutMsat int64
		var aliasPair string
		if err := rows.Scan(&item.ID, &item.OccurredAt, &chanIn, &chanOut, &item.AmountSat, &item.FeeSat, &item.FeeMsat, &amountInMsat, &amountOutMsat, &aliasPair, &item.PeerPubkeyIn, &item.PeerPubkeyOut); err != nil {
			return nil, false, err
		}
		item.ChanIDIn, item.ChanIDOut = uint64(chanIn), uint64(chanOut)
		item.ChanIDInStr, item.ChanIDOutStr = strconv.FormatUint(item.ChanIDIn, 10), strconv.FormatUint(item.ChanIDOut, 10)
		if item.FeeMsat <= 0 {
			item.FeeMsat = item.FeeSat * 1000
		}
		if amountInMsat > 0 {
			item.AmountInSat = amountInMsat / 1000
		} else {
			item.AmountInSat = item.AmountSat + item.FeeSat
		}
		item.PPM = forwardPPM(item.FeeMsat, item.FeeSat, amountOutMsat, item.AmountSat)
		inAlias, outAlias := splitForwardPeerAlias(aliasPair)
		if identity, ok := known[item.ChanIDIn]; ok && identity.Alias != "" {
			inAlias = identity.Alias
			if item.PeerPubkeyIn == "" {
				item.PeerPubkeyIn = identity.Pubkey
			}
		}
		if identity, ok := known[item.ChanIDOut]; ok && identity.Alias != "" {
			outAlias = identity.Alias
			if item.PeerPubkeyOut == "" {
				item.PeerPubkeyOut = identity.Pubkey
			}
		}
		// Rows without a channel id (legacy seeds) keep an empty alias; the UI
		// shows a dash instead of "0".
		if inAlias == "" && item.ChanIDIn != 0 {
			inAlias = item.ChanIDInStr
		}
		if outAlias == "" && item.ChanIDOut != 0 {
			outAlias = item.ChanIDOutStr
		}
		item.PeerAliasIn, item.PeerAliasOut = inAlias, outAlias
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(items) > opts.Limit
	if hasMore {
		items = items[:opts.Limit]
	}
	return items, hasMore, nil
}

func (s *Server) handleLNForwardHistoryGet(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "notifications database unavailable")
		return
	}
	opts, err := parseForwardHistoryOptions(r, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), forwardHistoryQueryTimeout)
	defer cancel()

	known := s.forwardHistoryChannelIdentities(ctx)
	resp := forwardHistoryResponse{
		Range: opts.Range, Since: opts.Since, Until: opts.Until, Timezone: opts.Location.String(), BucketUnit: forwardHistoryBucketUnit(opts.Range),
		Sort: opts.Sort, Order: opts.Order, Limit: opts.Limit, Offset: opts.Offset,
		Channels: []forwardHistoryChannel{}, FlowIn: []forwardHistoryFlowEntry{}, FlowOut: []forwardHistoryFlowEntry{}, Buckets: []forwardHistoryBucket{}, Items: []forwardHistoryItem{},
	}
	if opts.ChanIn != 0 {
		resp.ChanIn = strconv.FormatUint(opts.ChanIn, 10)
	}
	if opts.ChanOut != 0 {
		resp.ChanOut = strconv.FormatUint(opts.ChanOut, 10)
	}

	fail := func(stage string, err error) {
		if errors.Is(err, context.Canceled) {
			return
		}
		s.logger.Printf("forward history: %s failed: %v", stage, err)
		writeError(w, http.StatusInternalServerError, "forward history query failed")
	}

	if resp.Totals, err = s.forwardHistoryTotals(ctx, opts, false); err != nil {
		fail("totals", err)
		return
	}
	if opts.filtered() {
		if resp.Filtered, err = s.forwardHistoryTotals(ctx, opts, true); err != nil {
			fail("filtered totals", err)
			return
		}
	} else {
		resp.Filtered = resp.Totals
	}
	if resp.Channels, err = s.forwardHistoryChannels(ctx, opts, known); err != nil {
		fail("channels", err)
		return
	}
	if resp.FlowIn, err = s.forwardHistoryFlow(ctx, opts, "in", known); err != nil {
		fail("flow in", err)
		return
	}
	if resp.FlowOut, err = s.forwardHistoryFlow(ctx, opts, "out", known); err != nil {
		fail("flow out", err)
		return
	}
	if resp.Buckets, err = s.forwardHistoryBuckets(ctx, opts); err != nil {
		fail("buckets", err)
		return
	}
	if resp.Items, resp.HasMore, err = s.forwardHistoryItems(ctx, opts, known); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			resp.Items = []forwardHistoryItem{}
		} else {
			fail("items", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
