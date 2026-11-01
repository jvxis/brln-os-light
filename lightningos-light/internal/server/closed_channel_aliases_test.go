package server

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
)

func TestClosedChannelAliasesEnrichOnceAndPreserveHistory(t *testing.T) {
	a, b := "02"+strings.Repeat("a", 64), "03"+strings.Repeat("b", 64)
	channels := []lndclient.ClosedChannelInfo{
		{RemotePubkey: strings.ToUpper(a), ChannelPoint: "funding:1", ClosingTxHash: "closing", ChanID: 7, SettledBalanceSat: 100},
		{RemotePubkey: a, ChanID: 6},
		{RemotePubkey: b, PeerAlias: "  Existing alias  ", ChanID: 5},
		{RemotePubkey: "", ChanID: 4},
	}
	before := append([]lndclient.ClosedChannelInfo(nil), channels...)
	calls := 0
	out := resolveClosedChannelAliases(context.Background(), channels, func(ctx context.Context, keys []string) (map[string]GraphExplorerNodeLookup, error) {
		calls++
		if !reflect.DeepEqual(keys, []string{a}) {
			t.Fatalf("expected one deduplicated missing peer, got %v", keys)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > closedChannelAliasTimeout {
			t.Fatal("lookup needs bounded context")
		}
		return map[string]GraphExplorerNodeLookup{a: {Alias: "  Archived peer  "}, b: {Alias: "Do not overwrite"}}, nil
	})
	if calls != 1 || out[0].PeerAlias != "Archived peer" || out[1].PeerAlias != "Archived peer" || out[2].PeerAlias != "Existing alias" {
		t.Fatalf("missing/wrong aliases or batch count: calls=%d rows=%+v", calls, out)
	}
	if !reflect.DeepEqual(channels, before) {
		t.Fatal("enrichment mutated input history")
	}
	for i := range out {
		out[i].PeerAlias = before[i].PeerAlias
		if !reflect.DeepEqual(out[i], before[i]) {
			t.Fatal("non-alias history field/order changed")
		}
	}
}

func TestClosedChannelAliasesUnavailableKeepsPubkeyFallback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		channels := []lndclient.ClosedChannelInfo{{RemotePubkey: "02abc", ClosingTxHash: "keep"}}
		out := resolveClosedChannelAliases(context.Background(), channels, func(context.Context, []string) (map[string]GraphExplorerNodeLookup, error) {
			if fail {
				return nil, errors.New("graph unavailable")
			}
			return map[string]GraphExplorerNodeLookup{"02abc": {Alias: "unable to lookup peer alias: node not found in graph"}}, nil
		})
		if out[0].PeerAlias != "" || out[0].RemotePubkey != "02abc" || out[0].ClosingTxHash != "keep" {
			t.Fatalf("fallback damaged: %+v", out)
		}
	}
	channels := []lndclient.ClosedChannelInfo{{PeerAlias: "Known"}}
	resolveClosedChannelAliases(context.Background(), channels, func(context.Context, []string) (map[string]GraphExplorerNodeLookup, error) {
		t.Fatal("no missing peers: must not query")
		return nil, nil
	})
	if got := (*Server).enrichClosedChannelAliases(&Server{}, context.Background(), channels); !reflect.DeepEqual(got, channels) {
		t.Fatal("nil database must preserve history")
	}
}

func TestClosedChannelAliasLookupHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	channels := []lndclient.ClosedChannelInfo{{RemotePubkey: "02abc"}}
	out := resolveClosedChannelAliases(ctx, channels, func(queryCtx context.Context, _ []string) (map[string]GraphExplorerNodeLookup, error) {
		if queryCtx.Err() != context.Canceled {
			t.Fatal("caller cancellation lost")
		}
		return nil, queryCtx.Err()
	})
	if !reflect.DeepEqual(out, channels) {
		t.Fatal("cancelled enrichment must retain history")
	}
}

func TestClosedChannelAliasesPreserveEmptyResponseShape(t *testing.T) {
	for _, channels := range [][]lndclient.ClosedChannelInfo{nil, {}} {
		out := resolveClosedChannelAliases(context.Background(), channels, nil)
		if !reflect.DeepEqual(out, channels) {
			t.Fatal("nil/empty history shape changed")
		}
	}
}
