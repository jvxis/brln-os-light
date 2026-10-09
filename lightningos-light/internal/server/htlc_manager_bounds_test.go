package server

import (
	"context"
	"math"
	"strings"
	"testing"

	"lightningos-light/internal/lndclient"
)

func TestComputeHTLCTargetsNegotiatedBounds(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		bounds               *lndclient.ChannelHTLCBounds
		local, configuredMin int64
		pct                  int
		wantMin, wantMax     uint64
		wantErr              bool
	}{
		{"legacy missing constraints", nil, 250_000, 1, 0, 1000, 200_000_000, false},
		{"negotiated three sats", &lndclient.ChannelHTLCBounds{MinMsat: 3000, MaxMsat: math.MaxUint64}, 250_000, 1, 0, 3000, 200_000_000, false},
		{"negotiated ten thousand sats", &lndclient.ChannelHTLCBounds{MinMsat: 10_000_000, MaxMsat: 950_000_000}, 250_000, 1, 0, 10_000_000, 200_000_000, false},
		{"configured floor wins", &lndclient.ChannelHTLCBounds{MinMsat: 3000, MaxMsat: 950_000_000}, 250_000, 10, 0, 10_000, 200_000_000, false},
		{"drained channel raises maximum to floor", &lndclient.ChannelHTLCBounds{MinMsat: 100_001, MaxMsat: 950_000_000}, 0, 1, 0, 100_001, 100_001, false},
		{"negotiated ceiling preserves msat", &lndclient.ChannelHTLCBounds{MinMsat: 1, MaxMsat: 100_000_123}, 250_000, 1, 0, 1000, 100_000_123, false},
		{"buffer respects ceiling", &lndclient.ChannelHTLCBounds{MinMsat: 1, MaxMsat: 250_000_123}, 250_000, 1, 50, 1000, 250_000_123, false},
		{"unlimited maximum respects reserve", &lndclient.ChannelHTLCBounds{MinMsat: 1, MaxMsat: math.MaxUint64}, 990_000, 1, 50, 1000, 950_000_000, false},
		{"equal floor and ceiling", &lndclient.ChannelHTLCBounds{MinMsat: 3001, MaxMsat: 3001}, 0, 1, 0, 3001, 3001, false},
		{"configured floor above ceiling", &lndclient.ChannelHTLCBounds{MinMsat: 1, MaxMsat: 999}, 250_000, 1, 0, 0, 0, true},
		{"negotiated floor above capacity", &lndclient.ChannelHTLCBounds{MinMsat: 960_000_000, MaxMsat: math.MaxUint64}, 250_000, 1, 0, 0, 0, true},
		{"inconsistent negotiated bounds", &lndclient.ChannelHTLCBounds{MinMsat: 3001, MaxMsat: 3000}, 250_000, 1, 0, 0, 0, true},
		{"explicit zero ceiling", &lndclient.ChannelHTLCBounds{}, 250_000, 1, 0, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch := lndclient.ChannelInfo{CapacitySat: 1_000_000, LocalBalanceSat: tc.local, LocalChanReserveSat: 50_000, LocalHTLCBounds: tc.bounds}
			min, max, err := computeHTLCTargets(ch, HtlcManagerConfig{MinHtlcSat: tc.configuredMin, MaxLocalPct: tc.pct})
			if (err != nil) != tc.wantErr {
				t.Fatalf("min=%d max=%d err=%v, wantErr=%v", min, max, err, tc.wantErr)
			}
			if !tc.wantErr && (min != tc.wantMin || max != tc.wantMax) {
				t.Fatalf("targets=(%d,%d), want (%d,%d)", min, max, tc.wantMin, tc.wantMax)
			}
		})
	}
}

type htlcBoundsFixture struct {
	*lndclient.Client // Stream access is unused by tick.
	channels          []lndclient.ChannelInfo
	policies          map[string]lndclient.ChannelPolicy
	requests          []lndclient.UpdateChannelPolicyParams
	reject            bool
}

func (f *htlcBoundsFixture) ListChannels(context.Context) ([]lndclient.ChannelInfo, error) {
	return f.channels, nil
}

func (f *htlcBoundsFixture) GetChannelPolicy(_ context.Context, point string) (lndclient.ChannelPolicy, error) {
	return f.policies[point], nil
}

func (f *htlcBoundsFixture) UpdateChannelPolicy(_ context.Context, p lndclient.UpdateChannelPolicyParams) error {
	f.requests = append(f.requests, p)
	if f.reject {
		return &lndclient.PolicyUpdateError{FailedUpdates: 1, Reasons: map[string]int{"invalid_parameter": 1}}
	}
	for _, ch := range f.channels {
		if ch.ChannelPoint != p.ChannelPoint || ch.LocalHTLCBounds == nil {
			continue
		}
		b := ch.LocalHTLCBounds
		if *p.MinHtlcMsat < b.MinMsat || *p.MaxHtlcMsat > b.MaxMsat || *p.MinHtlcMsat > *p.MaxHtlcMsat {
			return &lndclient.PolicyUpdateError{FailedUpdates: 1, Reasons: map[string]int{"invalid_parameter": 1}}
		}
	}
	policy := f.policies[p.ChannelPoint]
	policy.MinHtlcMsat, policy.MaxHtlcMsat = *p.MinHtlcMsat, *p.MaxHtlcMsat
	f.policies[p.ChannelPoint] = policy
	return nil
}

func TestHTLCManagerTickNegotiatedBoundsAndRecovery(t *testing.T) {
	ch := lndclient.ChannelInfo{ChannelPoint: "negotiated", Active: true, CapacitySat: 1_000_000, LocalBalanceSat: 250_000, LocalChanReserveSat: 50_000, LocalHTLCBounds: &lndclient.ChannelHTLCBounds{MinMsat: 100_000, MaxMsat: 100_000_123}}
	policy := lndclient.ChannelPolicy{BaseFeeMsat: 123, FeeRatePpm: 456, TimeLockDelta: 80, InboundBaseMsat: -12, InboundFeeRatePpm: -34, MinHtlcMsat: 100_000, MaxHtlcMsat: 50_000_000}
	f := &htlcBoundsFixture{channels: []lndclient.ChannelInfo{ch}, policies: map[string]lndclient.ChannelPolicy{ch.ChannelPoint: policy}, reject: true}
	m := &HtlcManager{lnd: f, config: HtlcManagerConfig{Enabled: true, MinHtlcSat: 1}}
	m.tick(false)
	if !strings.Contains(m.lastError, "invalid_parameter") || m.lastChangedCount != 0 || len(m.logs) != 0 {
		t.Fatalf("rejected update incorrectly counted as success: %+v", m)
	}
	f.reject = false
	m.tick(false)
	if m.lastError != "" || m.lastOK.IsZero() || m.lastChangedCount != 1 || len(m.logs) != 1 {
		t.Fatalf("valid negotiated update did not recover: error=%s changed=%d logs=%d", m.lastError, m.lastChangedCount, len(m.logs))
	}
	p := f.requests[len(f.requests)-1]
	if p.ApplyAll || p.ChannelPoint != ch.ChannelPoint || !p.MinHtlcMsatSpecified || *p.MinHtlcMsat != 100_000 || *p.MaxHtlcMsat != 100_000_123 {
		t.Fatalf("wrong policy scope or bounds: %+v", p)
	}
	if p.BaseFeeMsat != policy.BaseFeeMsat || p.FeeRatePpm != policy.FeeRatePpm || p.TimeLockDelta != policy.TimeLockDelta || !p.InboundEnabled || p.InboundBaseMsat != policy.InboundBaseMsat || p.InboundFeeRatePpm != policy.InboundFeeRatePpm {
		t.Fatalf("unrelated policy changed: %+v", p)
	}
	m.tick(false)
	if len(f.requests) != 2 || m.lastChangedCount != 0 {
		t.Fatal("unchanged policy was sent again")
	}
	// Existing hysteresis still suppresses tiny balance changes on a valid policy.
	f.channels[0].LocalBalanceSat = 150_000
	m.tick(false)
	if len(f.requests) != 2 {
		t.Fatal("hysteresis no longer suppresses tiny changes")
	}
	// Inactive channels remain untouched.
	f.channels[0].Active = false
	f.channels[0].LocalBalanceSat = 0
	m.tick(false)
	if len(f.requests) != 2 {
		t.Fatal("inactive channel updated")
	}
}

func TestHTLCManagerTickSkipsImpossibleBoundsAndContinues(t *testing.T) {
	f := &htlcBoundsFixture{policies: map[string]lndclient.ChannelPolicy{}, channels: []lndclient.ChannelInfo{
		{ChannelPoint: "invalid", Active: true, CapacitySat: 1000, LocalHTLCBounds: &lndclient.ChannelHTLCBounds{MinMsat: 2000, MaxMsat: 1000}},
		{ChannelPoint: "valid", Active: true, CapacitySat: 1000, LocalBalanceSat: 500},
	}}
	m := &HtlcManager{lnd: f, config: HtlcManagerConfig{Enabled: true, MinHtlcSat: 1}}
	m.tick(false)
	if len(f.requests) != 1 || f.requests[0].ChannelPoint != "valid" || m.lastError == "" || m.lastChangedCount != 1 || len(m.logs) != 1 {
		t.Fatalf("must skip invalid request and continue other channels: requests=%d error=%s changed=%d", len(f.requests), m.lastError, m.lastChangedCount)
	}
}

func TestHTLCManagerHysteresisDoesNotKeepInvalidMaximum(t *testing.T) {
	for _, currentMax := range []uint64{99_999, 100_124} {
		ch := lndclient.ChannelInfo{ChannelPoint: "bounded", Active: true, CapacitySat: 1000, LocalBalanceSat: 500, LocalHTLCBounds: &lndclient.ChannelHTLCBounds{MinMsat: 100_000, MaxMsat: 100_123}}
		f := &htlcBoundsFixture{channels: []lndclient.ChannelInfo{ch}, policies: map[string]lndclient.ChannelPolicy{ch.ChannelPoint: {MinHtlcMsat: 100_000, MaxHtlcMsat: currentMax}}}
		m := &HtlcManager{lnd: f, config: HtlcManagerConfig{Enabled: true, MinHtlcSat: 1}}
		m.tick(false)
		if len(f.requests) != 1 || *f.requests[0].MaxHtlcMsat != 100_123 || m.lastError != "" {
			t.Fatalf("invalid max %d retained by hysteresis: requests=%d error=%s", currentMax, len(f.requests), m.lastError)
		}
	}
}
