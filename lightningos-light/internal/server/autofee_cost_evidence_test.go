package server

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
)

func TestAutofeeCostEvidenceProvenance(t *testing.T) {
	for source, kind := range map[string]string{
		"rebal": "channel_rebalance", "rebal-recent": "channel_rebalance",
		"rebal-21d": "historical_channel_rebalance", "rebal-mem": "historical_channel_rebalance",
		"rebal-global": "global_rebalance_reference", "rebal-blend": "blended_rebalance_reference",
		"outrate": "outgoing_reference", "outrate-mem": "outgoing_reference",
		"seed": "market_reference", "min": "configured_minimum", "future": "unknown",
	} {
		t.Run(source, func(t *testing.T) {
			tags := []string{"no-down-neg-margin"}
			e := newAutofeeCostEvidence(source, 200, 200, true, 7, tags)
			if e.Kind != kind || e.Source != source || e.MinAdjusted || !e.MarginActionable || !e.NegativeMarginGuard || e.ForwardCount != 7 {
				t.Fatalf("bad provenance: %+v", e)
			}
			if !reflect.DeepEqual(tags, []string{"no-down-neg-margin"}) {
				t.Fatal("diagnostics mutated tags")
			}
		})
	}
	e := newAutofeeCostEvidence("min", 0, 10, false, 0, nil)
	if !e.MinAdjusted || e.NegativeMarginGuard || e.ReferencePpm != 0 {
		t.Fatalf("bad minimum fallback: %+v", e)
	}
}

func TestEvaluateChannelCostEvidenceUsesMarginInput(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, mode, source                            string
		memory, minimum, global, reference, effective int
		rebal                                         rebalStat
		recent                                        recentRebalanceSignal
	}{
		{name: "outgoing memory is not acquisition cost", mode: "channel", memory: 400, minimum: 10, source: "outrate-mem", reference: 400, effective: 400},
		{name: "minimum clamps but preserves reference", mode: "channel", memory: 400, minimum: 500, source: "outrate-mem", reference: 400, effective: 500},
		{name: "absent global uses minimum", mode: "global", minimum: 10, source: "min", reference: 0, effective: 10},
		{name: "global is only reference", mode: "global", minimum: 10, global: 200, source: "rebal-global", reference: 200, effective: 200},
		{name: "observed channel cost", mode: "channel", minimum: 10, source: "rebal", reference: 300, effective: 300,
			rebal: rebalStat{AmtMsat: 2_000_000_000, FeeMsat: 600_000, Count: 10}},
		{name: "recent cost overrides memory", mode: "channel", memory: 400, minimum: 10, source: "rebal-recent", reference: 600, effective: 600,
			recent: recentRebalanceSignal{Count: 3, AmtSat: 1_000_000, FeeMsat: 600_000, LastAt: now.Add(-time.Hour)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := goldenDefaultCfg()
			cfg.RebalCostMode, cfg.MinPpm = tc.mode, tc.minimum
			e := newGoldenEngine(t, cfg, goldenDefaultCalib(), now)
			ch := goldenChannel(42, 5_000_000, 4_900_000, 400, true)
			st := &autofeeChannelState{ChannelID: 42, LastPpm: 400, LastSeed: 200, FirstSeen: now.Add(-60 * 24 * time.Hour), LastOutrate: tc.memory, LastOutrateTs: now.Add(-time.Hour)}
			rb := rebalStats{ByChannel: map[uint64]rebalStat{42: tc.rebal}}
			d := e.evaluateChannel(ch, st, nil, nil, nil, nil, nil, nil, rb, rb, rebalStats{}, map[uint64]recentRebalanceSignal{42: tc.recent}, nil, 0, tc.global, false)
			if d == nil || d.CostEvidence == nil {
				t.Fatal("missing evidence")
			}
			v := d.CostEvidence
			if v.Source != tc.source || v.ReferencePpm != tc.reference || v.EffectivePpm != tc.effective || v.MinAdjusted != (tc.effective > tc.reference) {
				t.Fatalf("wrong margin provenance: %+v", v)
			}
		})
	}
}

func TestAutofeeApplySummaryCountsAcknowledgedDirections(t *testing.T) {
	s := autofeeRunSummary{applyErrors: 2}
	for _, next := range []int{500, 300, 400} {
		s.recordAppliedDecision(&decision{LocalPpm: 400, NewPpm: next})
	}
	if s.applied != 3 || s.changedUp != 1 || s.changedDown != 1 || s.kept != 1 || s.applyErrors != 2 {
		t.Fatalf("incorrect counters: %+v", s)
	}
}

func TestAutofeeCostEvidenceLogRoundTrip(t *testing.T) {
	d := &decision{LocalPpm: 400, NewPpm: 400, Margin: -40, FloorBaseSrc: "seed", FloorBasePpm: 100,
		CostEvidence: newAutofeeCostEvidence("outrate-mem", 400, 400, true, 0, []string{"no-down-neg-margin"})}
	entry := buildAutofeeChannelLogEntry(d, "kept", false, nil)
	raw, err := json.Marshal(entry.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded autofeeLogItem
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.CostEvidence, d.CostEvidence) || decoded.FloorBaseSrc != "seed" || decoded.CostBasisMarginPpm != -40 {
		t.Fatal("cost evidence lost or replaced by independently adjusted floor")
	}
	legacy := autofeeLogItem{}
	if err := json.Unmarshal([]byte(`{"kind":"channel","margin":-40}`), &legacy); err != nil || legacy.CostEvidence != nil {
		t.Fatal("legacy results must remain unknown, not invented evidence")
	}
}

func TestAutofeePolicyRejectionIsNotRetried(t *testing.T) {
	err := &lndclient.PolicyUpdateError{FailedUpdates: 1, Reasons: map[string]int{"internal": 1}}
	if isTransientApplyError(err) || isTransientApplyError(fmt.Errorf("unavailable wrapper: %w", err)) {
		t.Fatal("application rejection must not use transport retries")
	}
	if !isTransientApplyError(fmt.Errorf("transport is closing")) {
		t.Fatal("existing transient retry lost")
	}
}
