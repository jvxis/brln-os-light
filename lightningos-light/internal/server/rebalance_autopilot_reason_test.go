package server

import "testing"

func TestRebalanceAutopilotReason(t *testing.T) {
	cfg := RebalanceConfig{DeadbandPct: 2, SovereignCandidateScope: rebalanceSovereignScopeAutoAndManualRestart}
	eligible := RebalanceChannel{Active: true, AutoEnabled: true, LocalPct: 5, TargetOutboundPct: 20, EligibleAsManualTarget: true, EligibleAsTarget: true}
	cases := []struct {
		name     string
		mutate   func(*RebalanceChannel)
		cfg      func(*RebalanceConfig)
		last     *RebalanceSovereignDecision
		scanned  bool
		want     string
		fromScan bool
	}{
		{name: "inactive", mutate: func(c *RebalanceChannel) { c.Active = false }, want: "inactive"},
		{name: "parked", mutate: func(c *RebalanceChannel) { c.AutomationMode = channelAutomationModeParked }, want: "parked"},
		{name: "both flags off", mutate: func(c *RebalanceChannel) { c.AutoEnabled = false }, want: "automation_off"},
		{name: "manual restart counts in the auto+restart scope", mutate: func(c *RebalanceChannel) { c.AutoEnabled = false; c.ManualRestartEnabled = true }, scanned: true, want: "eligible_not_ranked", fromScan: true},
		{name: "manual restart outside the scope", mutate: func(c *RebalanceChannel) { c.AutoEnabled = false; c.ManualRestartEnabled = true }, cfg: func(c *RebalanceConfig) { c.SovereignCandidateScope = rebalanceSovereignScopeAutoOnly }, want: "automation_off"},
		// 21/10/2026: target lowered to the local balance, no deficit, never a candidate.
		{name: "target at the local balance", mutate: func(c *RebalanceChannel) { c.LocalPct = 10; c.TargetOutboundPct = 10 }, want: "at_target"},
		{name: "deficit inside the deadband", mutate: func(c *RebalanceChannel) { c.LocalPct = 18.5 }, want: "at_target"},
		{name: "peer cost above the fee budget", mutate: func(c *RebalanceChannel) { c.EligibleAsManualTarget = false; c.EligibleAsTarget = false }, want: "pricing_infeasible"},
		{name: "no sale in 30 days", mutate: func(c *RebalanceChannel) { c.NoSale30d = true; c.EligibleAsTarget = false }, want: "no_sale_30d"},
		{name: "historical cost gate", mutate: func(c *RebalanceChannel) { c.EligibleAsTarget = false }, want: "cost_gate"},
		{name: "selected in the last scan", last: &RebalanceSovereignDecision{Selected: true, Reason: "queued"}, scanned: true, want: "selected", fromScan: true},
		{name: "skipped in the last scan", last: &RebalanceSovereignDecision{Reason: "roi_guardrail"}, scanned: true, want: "roi_guardrail", fromScan: true},
		{name: "eligible, not among the recorded decisions", scanned: true, want: "eligible_not_ranked", fromScan: true},
		{name: "eligible, no scan yet", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := eligible
			if tc.mutate != nil {
				tc.mutate(&ch)
			}
			c := cfg
			if tc.cfg != nil {
				tc.cfg(&c)
			}
			got, fromScan := rebalanceAutopilotReason(ch, c, tc.last, tc.scanned)
			if got != tc.want || fromScan != tc.fromScan {
				t.Fatalf("got %q (fromScan=%v), want %q (fromScan=%v)", got, fromScan, tc.want, tc.fromScan)
			}
		})
	}
}
