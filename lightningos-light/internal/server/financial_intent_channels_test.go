package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFinancialChannelOpenFailureDefinitive(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"peer disconnected", status.Error(codes.Unknown, "peer 02ab disconnected"), true},
		{"not enough witness outputs", errors.New("rpc error: code = Unknown desc = not enough witness outputs to create funding transaction"), true},
		{"already have channel", status.Error(codes.FailedPrecondition, "already have a channel with peer"), true},
		{"grpc deadline", status.Error(codes.DeadlineExceeded, "context deadline exceeded"), false},
		{"grpc canceled", status.Error(codes.Canceled, "context canceled"), false},
		{"grpc unavailable", status.Error(codes.Unavailable, "transport is closing"), false},
		{"context deadline", context.DeadlineExceeded, false},
		{"wrapped cancel", errors.Join(errors.New("open failed"), context.Canceled), false},
		{"plain connection refused", errors.New("dial tcp 127.0.0.1:10009: connection refused"), false},
	}
	for _, tc := range cases {
		if got := financialChannelOpenFailureDefinitive(tc.err); got != tc.want {
			t.Fatalf("%s: definitive=%v want %v", tc.name, got, tc.want)
		}
	}
}

func TestFinancialChannelOpenRejection(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	// Definitive failure recorded on the proof resolves immediately with the reason.
	failed := financialNativeProof{Kind: "channel_open", DispatchStarted: true, StartedAt: now.Add(-time.Minute), DispatchFailed: true, FailedAt: now.Add(-time.Minute), FailureReason: "peer 02ab disconnected"}
	body, ok := financialChannelOpenRejection(failed, now)
	if !ok {
		t.Fatal("recorded dispatch failure must resolve the intent")
	}
	var payload map[string]string
	if err := json.Unmarshal(body, &payload); err != nil || payload["code"] != "channel_open_rejected" || payload["error"] == "" {
		t.Fatalf("unexpected rejection body %s", body)
	}
	// Fresh dispatch without a failure mark stays uncertain.
	fresh := financialNativeProof{Kind: "channel_open", DispatchStarted: true, StartedAt: now.Add(-10 * time.Minute)}
	if _, ok := financialChannelOpenRejection(fresh, now); ok {
		t.Fatal("a recent dispatch without a channel must stay uncertain")
	}
	// Legacy proof (no failure mark) older than the stale window resolves as not found.
	stale := financialNativeProof{Kind: "channel_open", DispatchStarted: true, StartedAt: now.Add(-financialChannelOpenStaleAfter)}
	body, ok = financialChannelOpenRejection(stale, now)
	if !ok {
		t.Fatal("a dispatch older than the stale window with no channel must resolve")
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload["code"] != "channel_open_not_found" {
		t.Fatalf("unexpected stale body %s", body)
	}
	// Proof without a start time never resolves by age.
	if _, ok := financialChannelOpenRejection(financialNativeProof{Kind: "channel_open", DispatchStarted: true}, now); ok {
		t.Fatal("proof without start time must not resolve by age")
	}
}

func TestFinancialNativeProofFailureFieldsRoundTrip(t *testing.T) {
	plain, _ := json.Marshal(financialNativeProof{Kind: "channel_open", DispatchStarted: true, StartedAt: time.Now()})
	if json.Valid(plain) && containsKey(plain, "failed_at") {
		t.Fatalf("zero failure fields must be omitted: %s", plain)
	}
	var decoded financialNativeProof
	if err := json.Unmarshal(plain, &decoded); err != nil || decoded.DispatchFailed {
		t.Fatalf("legacy proof must decode without a failure mark: %v %+v", err, decoded)
	}
}

func containsKey(raw []byte, key string) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	_, ok := m[key]
	return ok
}
