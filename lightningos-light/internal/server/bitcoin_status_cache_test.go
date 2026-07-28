package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBitcoinStatusTTLUsesShortTTLForStaleOK(t *testing.T) {
	status := bitcoinStatus{
		RPCOk:    true,
		RPCStale: true,
	}

	if got := bitcoinStatusTTL(status, nil); got != bitcoinStatusCacheStale {
		t.Fatalf("expected stale ttl %s, got %s", bitcoinStatusCacheStale, got)
	}
}

func TestStaleBitcoinActiveStatusReturnsExpiredOKWithinGrace(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	fetchedAt := now.Add(-90 * time.Second)
	s := &Server{
		bitcoinActiveCache: map[string]cachedBitcoinStatus{
			"remote": {
				value: bitcoinStatus{
					RPCOk:  true,
					Chain:  "main",
					Blocks: 900000,
				},
				expiresAt: now.Add(-time.Second),
				fetchedAt: fetchedAt,
			},
		},
	}

	status, gotFetchedAt, ok := s.staleBitcoinActiveStatus("remote", now)
	if !ok {
		t.Fatalf("expected stale OK status within grace")
	}
	if gotFetchedAt != fetchedAt {
		t.Fatalf("expected fetchedAt %s, got %s", fetchedAt, gotFetchedAt)
	}
	if !status.RPCOk || status.Chain != "main" || status.Blocks != 900000 {
		t.Fatalf("expected cached OK payload, got %+v", status)
	}
}

func TestStaleBitcoinActiveStatusExpiresAfterGrace(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	s := &Server{
		bitcoinActiveCache: map[string]cachedBitcoinStatus{
			"remote": {
				value: bitcoinStatus{
					RPCOk: true,
					Chain: "main",
				},
				expiresAt: now.Add(-time.Second),
				fetchedAt: now.Add(-(bitcoinStatusStaleOKGrace + time.Second)),
			},
		},
	}

	if status, _, ok := s.staleBitcoinActiveStatus("remote", now); ok {
		t.Fatalf("expected stale OK to expire after grace, got %+v", status)
	}
}

func TestMarkBitcoinStatusStaleAnnotatesLastOKAge(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	fetchedAt := now.Add(-75 * time.Second)

	status := markBitcoinStatusStale(bitcoinStatus{RPCOk: false, Chain: "main"}, fetchedAt, now)

	if !status.RPCOk {
		t.Fatalf("expected stale status to keep rpc_ok true")
	}
	if !status.RPCStale {
		t.Fatalf("expected rpc_stale to be true")
	}
	if status.RPCLastOKAgeSeconds != 75 {
		t.Fatalf("expected last OK age 75, got %d", status.RPCLastOKAgeSeconds)
	}
}

func TestBitcoinActiveStatusCachedKeepsHealthyStatusDuringBackgroundRefresh(t *testing.T) {
	now := time.Now()
	s := &Server{
		bitcoinActiveCache: map[string]cachedBitcoinStatus{
			"remote": {
				value: bitcoinStatus{
					RPCOk:  true,
					Chain:  "main",
					Blocks: 900000,
				},
				expiresAt: now.Add(-time.Second),
				fetchedAt: now.Add(-31 * time.Second),
			},
		},
	}
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})

	status, err := s.bitcoinActiveStatusCachedWithFetch(
		context.Background(),
		"remote",
		func(ctx context.Context) (bitcoinStatus, error) {
			close(refreshStarted)
			select {
			case <-releaseRefresh:
				return bitcoinStatus{RPCOk: true, Chain: "main", Blocks: 900001}, nil
			case <-ctx.Done():
				return bitcoinStatus{}, ctx.Err()
			}
		},
	)
	if err != nil {
		t.Fatalf("unexpected error while refresh is in flight: %v", err)
	}
	if !status.RPCOk || status.RPCStale {
		t.Fatalf("healthy cached status must stay healthy during refresh, got %+v", status)
	}

	select {
	case <-refreshStarted:
	case <-time.After(time.Second):
		t.Fatal("background refresh did not start")
	}
	close(releaseRefresh)

	refreshed := waitForCachedBitcoinStatus(t, s, "remote", func(status bitcoinStatus) bool {
		return status.Blocks == 900001
	})
	if !refreshed.RPCOk || refreshed.RPCStale {
		t.Fatalf("successful refresh must store a healthy status, got %+v", refreshed)
	}
}

func TestBitcoinActiveStatusCachedMarksStaleAfterBackgroundRefreshFailure(t *testing.T) {
	now := time.Now()
	s := &Server{
		bitcoinActiveCache: map[string]cachedBitcoinStatus{
			"remote": {
				value: bitcoinStatus{
					RPCOk:  true,
					Chain:  "main",
					Blocks: 900000,
				},
				expiresAt: now.Add(-time.Second),
				fetchedAt: now.Add(-31 * time.Second),
			},
		},
	}
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})

	status, err := s.bitcoinActiveStatusCachedWithFetch(
		context.Background(),
		"remote",
		func(ctx context.Context) (bitcoinStatus, error) {
			close(refreshStarted)
			select {
			case <-releaseRefresh:
				return bitcoinStatus{}, errors.New("rpc unavailable")
			case <-ctx.Done():
				return bitcoinStatus{}, ctx.Err()
			}
		},
	)
	if err != nil {
		t.Fatalf("unexpected error while refresh is in flight: %v", err)
	}
	if !status.RPCOk || status.RPCStale {
		t.Fatalf("refresh in flight must not preemptively mark status stale, got %+v", status)
	}

	select {
	case <-refreshStarted:
	case <-time.After(time.Second):
		t.Fatal("background refresh did not start")
	}
	close(releaseRefresh)

	stale := waitForCachedBitcoinStatus(t, s, "remote", func(status bitcoinStatus) bool {
		return status.RPCStale
	})
	if !stale.RPCOk {
		t.Fatalf("failed refresh must preserve the last known healthy result, got %+v", stale)
	}
}

func waitForCachedBitcoinStatus(
	t *testing.T,
	s *Server,
	source string,
	match func(bitcoinStatus) bool,
) bitcoinStatus {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, err, ok := s.cachedBitcoinActiveStatus(source, time.Now())
		if ok && err == nil && match(status) {
			return status
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for cached Bitcoin status")
	return bitcoinStatus{}
}
