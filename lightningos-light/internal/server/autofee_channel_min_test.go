package server

import (
	"context"
	"testing"
	"time"
)

// Friendspool, 2026-10-31: a 5M channel opened at 2000 ppm with a 1600 ppm
// floor was idle-refreshed to its liquidity-adjusted seed (800) after 8 hours.
// evaluateChannel had already held the fee at the floor; the refresh replaced
// that decision and nothing applied the floor again.
func TestAutofeeChannelMinHoldsAfterIdleRefresh(t *testing.T) {
	now := time.Date(2026, 10, 31, 19, 11, 0, 0, time.UTC)
	cfg := goldenDefaultCfg()
	cfg.IdleRefreshEnabled = true
	cfg.NativeSeedEnabled = true
	e := newGoldenEngine(t, cfg, goldenDefaultCalib(), now)
	ch := goldenChannel(311, 5_000_000, 4_999_000, 1600, true)
	e.nativeSeedCache[ch.RemotePubkey] = autofeeSeedResult{Seed: 1000, Ok: true}
	e.channelMinPpm = map[uint64]int{ch.ChannelID: 1600}

	st := &autofeeChannelState{ChannelID: ch.ChannelID, LastPpm: 1600}
	d := &decision{LocalPpm: 1600, NewPpm: 1600, Target: 1151, ChannelAgeHours: 30 * 24, State: st}
	d = e.maybeApplyIdleRefresh(context.Background(), ch, d, forwardStat{}, forwardStat{}, inboundStat{}, rebalStat{}, rebalStat{}, rebalStat{})
	if !containsTag(d.Tags, "idle-refresh") || d.NewPpm >= 1600 {
		t.Fatalf("setup: the refresh should ask for less than the floor: new=%d tags=%v", d.NewPpm, d.Tags)
	}
	d = enforceAutofeeChannelMinPpm(d, e.channelMinPpm[ch.ChannelID], now)
	if d.NewPpm != 1600 || d.TargetFinal != 1600 || d.Apply || !containsTag(d.Tags, "channel-min") {
		t.Fatalf("the published fee must stay at the 1600 floor: new=%d final=%d apply=%v tags=%v", d.NewPpm, d.TargetFinal, d.Apply, d.Tags)
	}
	if st.LastPpm != 1600 {
		t.Fatalf("state must follow the published fee: last=%d", st.LastPpm)
	}
}

func TestEnforceAutofeeChannelMinPpm(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name               string
		local, next, min   int
		apply              bool
		inbound, prevInb   int
		wantPpm            int
		wantApply, wantTag bool
	}{
		{name: "no floor", local: 1600, next: 800, min: 0, apply: true, wantPpm: 800, wantApply: true},
		{name: "decrease above the floor", local: 2000, next: 1700, min: 1600, apply: true, wantPpm: 1700, wantApply: true},
		{name: "decrease below the floor from the floor", local: 1600, next: 800, min: 1600, apply: true, wantPpm: 1600, wantApply: false, wantTag: true},
		{name: "decrease below the floor from above", local: 2000, next: 800, min: 1600, apply: true, wantPpm: 1600, wantApply: true, wantTag: true},
		{name: "held below the floor is raised", local: 800, next: 800, min: 1600, apply: false, wantPpm: 1600, wantApply: true, wantTag: true},
		{name: "held at the floor is left alone", local: 1600, next: 1200, min: 1600, apply: false, wantPpm: 1200, wantApply: false},
		{name: "inbound-only change keeps the inbound apply", local: 1600, next: 800, min: 1600, apply: true, inbound: 50, wantPpm: 1600, wantApply: true, wantTag: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &autofeeChannelState{LastPpm: tc.next}
			d := &decision{LocalPpm: tc.local, NewPpm: tc.next, TargetFinal: tc.next, Apply: tc.apply, InboundDiscount: tc.inbound, PrevInboundDiscount: tc.prevInb, State: st}
			d = enforceAutofeeChannelMinPpm(d, tc.min, now)
			if d.NewPpm != tc.wantPpm || d.Apply != tc.wantApply || containsTag(d.Tags, "channel-min") != tc.wantTag {
				t.Fatalf("got new=%d apply=%v tags=%v; want new=%d apply=%v tag=%v", d.NewPpm, d.Apply, d.Tags, tc.wantPpm, tc.wantApply, tc.wantTag)
			}
			if tc.wantTag && st.LastPpm != tc.wantPpm {
				t.Fatalf("state last=%d want %d", st.LastPpm, tc.wantPpm)
			}
		})
	}
}

func TestAutofeeIdleRefreshSkipsChannelsYoungerThanWindow(t *testing.T) {
	const tipHeight = 900_000
	scidAt := func(height int64) uint64 { return uint64(height)<<40 | 1<<16 | 1 }
	for _, tc := range []struct {
		name          string
		channelID     uint64
		blockHeight   int64
		firstSeenAgeH float64
		wantYoung     bool
	}{
		{name: "opened 48 blocks ago", channelID: scidAt(tipHeight - 48), blockHeight: tipHeight, firstSeenAgeH: 8, wantYoung: true},
		{name: "opened a month ago, AutoFee enabled an hour ago", channelID: scidAt(tipHeight - 4320), blockHeight: tipHeight, firstSeenAgeH: 1, wantYoung: false},
		{name: "opened 6 days ago", channelID: scidAt(tipHeight - 864), blockHeight: tipHeight, firstSeenAgeH: 200, wantYoung: true},
		{name: "no block height, first seen 8h ago", channelID: scidAt(tipHeight - 48), blockHeight: 0, firstSeenAgeH: 8, wantYoung: true},
		{name: "no block height, first seen a month ago", channelID: scidAt(tipHeight - 48), blockHeight: 0, firstSeenAgeH: 30 * 24, wantYoung: false},
		{name: "alias scid above the tip uses first seen", channelID: scidAt(16_000_000), blockHeight: tipHeight, firstSeenAgeH: 30 * 24, wantYoung: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := autofeeIdleRefreshChannelTooYoung(tc.channelID, tc.blockHeight, tc.firstSeenAgeH); got != tc.wantYoung {
				t.Fatalf("young=%v want %v", got, tc.wantYoung)
			}
		})
	}

	now := time.Date(2026, 10, 31, 19, 11, 0, 0, time.UTC)
	cfg := goldenDefaultCfg()
	cfg.IdleRefreshEnabled = true
	cfg.NativeSeedEnabled = true
	e := newGoldenEngine(t, cfg, goldenDefaultCalib(), now)
	e.blockHeight = tipHeight
	ch := goldenChannel(scidAt(tipHeight-48), 5_000_000, 4_999_000, 1600, true)
	e.nativeSeedCache[ch.RemotePubkey] = autofeeSeedResult{Seed: 1000, Ok: true}
	st := &autofeeChannelState{ChannelID: ch.ChannelID, LastPpm: 1600}
	d := &decision{LocalPpm: 1600, NewPpm: 1600, Target: 1151, ChannelAgeHours: 8, State: st}
	d = e.maybeApplyIdleRefresh(context.Background(), ch, d, forwardStat{}, forwardStat{}, inboundStat{}, rebalStat{}, rebalStat{}, rebalStat{})
	if d.NewPpm != 1600 || containsTag(d.Tags, "idle-refresh") || !containsTag(d.Tags, "idle-refresh-young-channel") || !st.LastIdleRefreshTs.IsZero() {
		t.Fatalf("an 8-hour-old channel is not idle: new=%d tags=%v", d.NewPpm, d.Tags)
	}
}
