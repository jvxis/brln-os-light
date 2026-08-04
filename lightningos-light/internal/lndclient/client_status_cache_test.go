package lndclient

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestGetStatusReturnsExpiredCacheDuringBackgroundRefresh(t *testing.T) {
	wantStatus := Status{
		ServiceActive: true,
		WalletState:   "unlocked",
		SyncedToChain: true,
		BlockHeight:   900000,
	}
	wantErr := errors.New("cached warning")
	client := &Client{
		statusCached:     true,
		statusCache:      wantStatus,
		statusErr:        wantErr,
		statusNextFetch:  time.Now().Add(-time.Second),
		statusRefreshing: true,
	}

	gotStatus, gotErr := client.GetStatus(context.Background())
	if !reflect.DeepEqual(gotStatus, wantStatus) {
		t.Fatalf("got status %+v, want %+v", gotStatus, wantStatus)
	}
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("got error %v, want %v", gotErr, wantErr)
	}
}

func TestGetStatusReturnsValidCacheWithoutStartingRefresh(t *testing.T) {
	wantStatus := Status{
		ServiceActive: true,
		WalletState:   "unlocked",
		SyncedToChain: true,
		BlockHeight:   900000,
	}
	client := &Client{
		statusCached:    true,
		statusCache:     wantStatus,
		statusNextFetch: time.Now().Add(time.Minute),
	}

	gotStatus, gotErr := client.GetStatus(context.Background())
	if gotErr != nil {
		t.Fatalf("unexpected error: %v", gotErr)
	}
	if !reflect.DeepEqual(gotStatus, wantStatus) {
		t.Fatalf("got status %+v, want %+v", gotStatus, wantStatus)
	}
	if client.statusRefreshing {
		t.Fatalf("valid cache should not start a background refresh")
	}
}

func TestStatusCacheIgnoresOlderFetchCompletion(t *testing.T) {
	client := &Client{}
	olderFetch := client.beginStatusFetch()
	newerFetch := client.beginStatusFetch()
	wantStatus := Status{
		ServiceActive: true,
		WalletState:   "unlocked",
		BlockHeight:   900001,
	}

	client.storeStatusCache(newerFetch, wantStatus, nil)
	client.storeStatusCache(olderFetch, Status{WalletState: "unknown"}, errors.New("older timeout"))

	gotStatus, gotErr := client.GetStatus(context.Background())
	if gotErr != nil {
		t.Fatalf("older completion replaced newer success: %v", gotErr)
	}
	if !reflect.DeepEqual(gotStatus, wantStatus) {
		t.Fatalf("got status %+v, want newer status %+v", gotStatus, wantStatus)
	}
}

func TestInvalidateStatusCacheRejectsInflightFetchCompletion(t *testing.T) {
	client := &Client{}
	inflightFetch := client.beginStatusFetch()

	client.InvalidateStatusCache()
	client.storeStatusCache(inflightFetch, Status{
		ServiceActive: true,
		WalletState:   "unlocked",
	}, nil)

	client.statusMu.Lock()
	cached := client.statusCached
	client.statusMu.Unlock()
	if cached {
		t.Fatal("in-flight fetch repopulated an explicitly invalidated cache")
	}
}

func TestCachedRuntimeInfoCarriesChannelFootprint(t *testing.T) {
	now := time.Now()
	client := &Client{
		infoCacheValid: true,
		infoCacheAt:    now,
		infoCache: infoSnapshot{
			SyncedToChain:       true,
			SyncedToGraph:       false,
			BlockHeight:         900123,
			NumActiveChannels:   2,
			NumInactiveChannels: 1,
			NumPendingChannels:  3,
			NumPeers:            4,
		},
	}

	info := client.CachedRuntimeInfo()
	if !info.Known || info.Stale {
		t.Fatalf("unexpected readiness flags: %+v", info)
	}
	if info.NumActiveChannels != 2 || info.NumInactiveChannels != 1 ||
		info.NumPendingChannels != 3 || info.NumPeers != 4 {
		t.Fatalf("unexpected channel footprint: %+v", info)
	}
}

func TestRuntimeInfoFailureUsesBoundedBackoffAndPreservesSnapshot(t *testing.T) {
	client := &Client{
		infoCacheValid: true,
		infoCacheAt:    time.Now(),
		infoCache:      infoSnapshot{SyncedToChain: true, BlockHeight: 900123},
	}

	for i := 0; i < 10; i++ {
		client.recordRuntimeInfoFailure(errors.New("timeout"))
	}
	info := client.CachedRuntimeInfo()
	if !info.Known || !info.Stale || info.BlockHeight != 900123 {
		t.Fatalf("failed probe should preserve a stale snapshot: %+v", info)
	}

	client.statusMu.Lock()
	delay := time.Until(client.infoNextProbe)
	client.statusMu.Unlock()
	if delay < runtimeInfoBackoffMax-time.Second || delay > runtimeInfoBackoffMax+time.Second {
		t.Fatalf("backoff = %s, want approximately %s", delay, runtimeInfoBackoffMax)
	}
}

func TestIdentityPubkeyCacheDoesNotMarkRuntimeInfoKnown(t *testing.T) {
	const pubkey = "0356939a5900213e563c3c259909f463108d1319c1b61659c2857ce21c7517447d"
	client := &Client{}

	client.cacheIdentityPubkey(pubkey)

	if got := client.CachedPubkey(); got != pubkey {
		t.Fatalf("CachedPubkey() = %q, want %q", got, pubkey)
	}
	if info := client.CachedRuntimeInfo(); info.Known {
		t.Fatalf("identity-only cache must not imply a successful GetInfo snapshot: %+v", info)
	}
}

func TestIdentityPubkeyCacheRejectsInvalidKeys(t *testing.T) {
	client := &Client{}
	client.cacheIdentityPubkey("not-a-pubkey")

	if got := client.CachedPubkey(); got != "" {
		t.Fatalf("CachedPubkey() = %q after invalid key", got)
	}
}
