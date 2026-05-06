package server

import (
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
