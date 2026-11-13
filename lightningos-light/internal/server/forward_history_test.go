package server

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseForwardHistoryOptions(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

	opts, err := parseForwardHistoryOptions(httptest.NewRequest("GET", "/api/lnops/forwards", nil), now)
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if opts.Range != "7d" || opts.Sort != "time" || opts.Order != "desc" || opts.Limit != forwardHistoryDefaultLimit || opts.Offset != 0 || opts.Location != time.UTC {
		t.Fatalf("unexpected defaults: %+v", opts)
	}
	if !opts.Since.Equal(now.Add(-7*24*time.Hour)) || !opts.Until.Equal(now) {
		t.Fatalf("unexpected window: %s -> %s", opts.Since, opts.Until)
	}
	if opts.filtered() {
		t.Fatal("no channel filter must not count as filtered")
	}

	opts, err = parseForwardHistoryOptions(httptest.NewRequest("GET", "/api/lnops/forwards?range=24h&chan_in=123&chan_out=456&sort=ppm&order=asc&limit=9999&offset=40&tz=America/Sao_Paulo", nil), now)
	if err != nil {
		t.Fatalf("full query: %v", err)
	}
	if opts.Range != "24h" || opts.ChanIn != 123 || opts.ChanOut != 456 || opts.Sort != "ppm" || opts.Order != "asc" || opts.Offset != 40 {
		t.Fatalf("unexpected parsed options: %+v", opts)
	}
	if opts.Limit != forwardHistoryMaxLimit {
		t.Fatalf("limit must be capped at %d, got %d", forwardHistoryMaxLimit, opts.Limit)
	}
	if opts.Location.String() != "America/Sao_Paulo" {
		t.Fatalf("unexpected location %s", opts.Location)
	}
	if !opts.filtered() {
		t.Fatal("channel filter must count as filtered")
	}
	if forwardHistoryBucketUnit(opts.Range) != "hour" || forwardHistoryBucketUnit("30d") != "day" {
		t.Fatal("bucket unit must be hourly only for 24h")
	}

	for _, bad := range []string{"range=1y", "chan_in=abc", "chan_out=0", "sort=alias", "order=up", "limit=0", "offset=-1", "tz=Mars/Olympus"} {
		if _, err := parseForwardHistoryOptions(httptest.NewRequest("GET", "/api/lnops/forwards?"+bad, nil), now); err == nil {
			t.Fatalf("%s must be rejected", bad)
		}
	}
}

func TestSplitForwardPeerAlias(t *testing.T) {
	in, out := splitForwardPeerAlias("Alpha -> Beta Node")
	if in != "Alpha" || out != "Beta Node" {
		t.Fatalf("unexpected split: %q / %q", in, out)
	}
	in, out = splitForwardPeerAlias("-> Beta")
	if in != "" || out != "Beta" {
		t.Fatalf("missing in-alias must stay empty: %q / %q", in, out)
	}
	in, out = splitForwardPeerAlias("Alpha ->")
	if in != "Alpha" || out != "" {
		t.Fatalf("missing out-alias must stay empty: %q / %q", in, out)
	}
	in, out = splitForwardPeerAlias("Solo")
	if in != "Solo" || out != "" {
		t.Fatalf("pair without arrow: %q / %q", in, out)
	}
	if in, out = splitForwardPeerAlias(""); in != "" || out != "" {
		t.Fatal("empty pair must yield empty aliases")
	}
}

func TestForwardPPM(t *testing.T) {
	// 1,500 sat fee on a 1,000,000 sat forward = 1,500 ppm.
	if got := forwardPPM(1_500_000, 1500, 1_000_000_000, 1_000_000); got != 1500 {
		t.Fatalf("msat columns: got %d", got)
	}
	// Legacy rows stored only sats: 1 sat fee on 10,000 sats = 100 ppm.
	if got := forwardPPM(0, 1, 0, 10_000); got != 100 {
		t.Fatalf("sat fallback: got %d", got)
	}
	if got := forwardPPM(0, 0, 0, 0); got != 0 {
		t.Fatalf("zero amount must yield 0, got %d", got)
	}
	if got := forwardHistoryAvgPPM(2_000_000, 4_000_000); got != 500 {
		t.Fatalf("average ppm: got %d", got)
	}
}

func TestForwardHistoryIdentityPrefersLNDThenStoredPair(t *testing.T) {
	known := map[uint64]forwardChannelIdentity{7: {Alias: "Live Peer", Pubkey: "02live", Point: "txid:0", Open: true}}
	if got := forwardHistoryIdentity(known, 7, "Old -> Pair", "in", "02stored", "txid:9"); got.Alias != "Live Peer" || !got.Open {
		t.Fatalf("open channel must use the LND identity: %+v", got)
	}
	got := forwardHistoryIdentity(known, 8, "Closed In -> Closed Out", "out", "02closed", "txid:1")
	if got.Alias != "Closed Out" || got.Pubkey != "02closed" || got.Point != "txid:1" || got.Open {
		t.Fatalf("closed channel must fall back to the stored pair: %+v", got)
	}
	if got := forwardHistoryIdentity(known, 8, "Closed In -> Closed Out", "in", "", ""); got.Alias != "Closed In" {
		t.Fatalf("in side must take the left alias: %+v", got)
	}
}

func TestFillForwardHistoryBuckets(t *testing.T) {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	until := time.Date(2026, 11, 12, 15, 30, 0, 0, time.UTC) // 12:30 local
	since := until.Add(-24 * time.Hour)
	hit := time.Date(2026, 11, 12, 10, 0, 0, 0, loc)
	found := map[time.Time]forwardHistoryBucket{hit: {Count: 3, AmountSat: 300, FeeSat: 3}}
	buckets := fillForwardHistoryBuckets(since, until, "hour", loc, found)
	if len(buckets) != 25 {
		t.Fatalf("24h window must produce 25 hourly buckets (first and last partial), got %d", len(buckets))
	}
	if !buckets[0].Start.Equal(time.Date(2026, 11, 11, 12, 0, 0, 0, loc)) {
		t.Fatalf("first bucket must be the truncated since: %s", buckets[0].Start)
	}
	var matched bool
	for _, bucket := range buckets {
		if bucket.Start.Equal(hit) {
			matched = true
			if bucket.Count != 3 || bucket.AmountSat != 300 || bucket.FeeSat != 3 {
				t.Fatalf("aggregates not merged: %+v", bucket)
			}
		} else if bucket.Count != 0 {
			t.Fatalf("unexpected non-empty bucket %+v", bucket)
		}
	}
	if !matched {
		t.Fatal("found bucket missing from the filled timeline")
	}

	days := fillForwardHistoryBuckets(until.Add(-7*24*time.Hour), until, "day", loc, nil)
	if len(days) != 8 {
		t.Fatalf("7d window must produce 8 daily buckets, got %d", len(days))
	}
	if h := days[0].Start.Hour(); h != 0 {
		t.Fatalf("daily buckets must start at local midnight, got hour %d", h)
	}
}

func TestForwardHistoryOrderBy(t *testing.T) {
	cases := map[string]string{
		"time|desc":   "occurred_at desc, id desc",
		"time|asc":    "occurred_at asc, id asc",
		"amount|desc": "amount_sat desc, occurred_at desc, id desc",
		"fee|asc":     forwardHistoryFeeMsatExpr + " asc, occurred_at desc, id desc",
	}
	for key, want := range cases {
		parts := strings.SplitN(key, "|", 2)
		sortKey, order := parts[0], parts[1]
		if got := forwardHistoryOrderBy(forwardHistoryOptions{Sort: sortKey, Order: order}); got != want {
			t.Fatalf("%s: got %q want %q", key, got, want)
		}
	}
	if got := forwardHistoryOrderBy(forwardHistoryOptions{Sort: "ppm", Order: "desc"}); got == "" || got[:1] != "(" {
		t.Fatalf("ppm sort must compute the ratio: %q", got)
	}
}

func TestForwardHistoryBaseWhereNumbersPlaceholders(t *testing.T) {
	opts := forwardHistoryOptions{Since: time.Unix(0, 0), Until: time.Unix(10, 0), ChanIn: 1, ChanOut: 2}
	w := forwardHistoryBaseWhere(opts, true)
	if got := w.sql(); got != "type = $1 and occurred_at >= $2 and occurred_at < $3 and chan_id_in = $4 and chan_id_out = $5" {
		t.Fatalf("unexpected where: %s", got)
	}
	if len(w.args) != 5 {
		t.Fatalf("expected 5 args, got %d", len(w.args))
	}
	if got := forwardHistoryBaseWhere(opts, false).sql(); got != "type = $1 and occurred_at >= $2 and occurred_at < $3" {
		t.Fatalf("channel filters must be skipped for totals: %s", got)
	}
}
