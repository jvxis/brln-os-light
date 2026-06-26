package server

import (
	"testing"

	"lightningos-light/internal/lndclient"
)

func TestParseLNChannelDetailLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    int
		wantErr bool
	}{
		{name: "default", raw: "", want: lnChannelDetailDefaultLimit},
		{name: "explicit", raw: "40", want: 40},
		{name: "trim", raw: " 7 ", want: 7},
		{name: "cap", raw: "1000", want: lnChannelDetailMaxLimit},
		{name: "zero", raw: "0", wantErr: true},
		{name: "negative", raw: "-1", wantErr: true},
		{name: "bad", raw: "abc", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseLNChannelDetailLimit(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("limit = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseLNChannelDetailChannelID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want uint64
		ok   bool
	}{
		{name: "empty", raw: "", ok: false},
		{name: "integer", raw: "1043834558060691457", want: 1043834558060691457, ok: true},
		{name: "short id", raw: "949362x1404x1", want: uint64(949362)<<40 | uint64(1404)<<16 | 1, ok: true},
		{name: "bad", raw: "not-a-channel", ok: false},
		{name: "zero", raw: "0", ok: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseLNChannelDetailChannelID(tt.raw)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("channel id = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestNormalizeLNPeerNotePubkey(t *testing.T) {
	t.Parallel()

	got := normalizeLNPeerNotePubkey("  02ABCdef  ")
	if got != "02abcdef" {
		t.Fatalf("normalized pubkey = %q, want %q", got, "02abcdef")
	}
}

func TestLNApplySevenDayStatsToChannel(t *testing.T) {
	t.Parallel()

	channel := lndclient.ChannelInfo{ChannelID: 123}
	periods := []lnChannelDetailPeriod{
		{Period: "1d", OutPpm: 10, RebalancePpm: 5, RevenueSat: 20, CostSat: 3, ProfitSat: 17},
		{Period: "7d", OutPpm: 80, RebalancePpm: 42, RevenueSat: 1000, CostSat: 350, ProfitSat: 650},
	}

	lnApplySevenDayStatsToChannel(&channel, periods)

	if channel.OutPpm7d == nil || *channel.OutPpm7d != 80 {
		t.Fatalf("OutPpm7d = %v, want 80", channel.OutPpm7d)
	}
	if channel.RebalPpm7d == nil || *channel.RebalPpm7d != 42 {
		t.Fatalf("RebalPpm7d = %v, want 42", channel.RebalPpm7d)
	}
	if channel.ForwardFee7dSat == nil || *channel.ForwardFee7dSat != 1000 {
		t.Fatalf("ForwardFee7dSat = %v, want 1000", channel.ForwardFee7dSat)
	}
	if channel.RebalFee7dSat == nil || *channel.RebalFee7dSat != 350 {
		t.Fatalf("RebalFee7dSat = %v, want 350", channel.RebalFee7dSat)
	}
	if channel.ProfitFee7dSat == nil || *channel.ProfitFee7dSat != 650 {
		t.Fatalf("ProfitFee7dSat = %v, want 650", channel.ProfitFee7dSat)
	}
}
