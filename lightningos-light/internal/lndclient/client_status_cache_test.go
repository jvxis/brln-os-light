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
